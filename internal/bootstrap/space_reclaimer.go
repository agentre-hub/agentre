package bootstrap

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	// autoVacuumIncremental 是 PRAGMA auto_vacuum 的 INCREMENTAL 取值。只有这个模式下
	// incremental_vacuum 才会真的归还空闲页;其它模式要转换得跑整库 VACUUM(长时间独占
	// 写锁),这里不做。
	autoVacuumIncremental = 2

	// 一批 2000 页(≈8 MiB):每批是一条独立的短语句,写锁只在这一批内被持有,批与批之间
	// 写入方可以插队。一次运行最多 64 批(≈512 MiB),足够一次清掉 ~556 MB 的历史空闲页;
	// 没清完的留给下一次触发/定时。
	defaultReclaimBatchPages = 2000
	defaultReclaimMaxBatches = 64
	// 批间让出时间,让排队的写事务拿到锁。
	defaultReclaimYield = 20 * time.Millisecond
	// 空闲定时回收的间隔:低频兜底,主要覆盖升级后遗留的历史空闲页和未触发的删除。
	defaultReclaimInterval = 15 * time.Minute
)

type spaceReclaimerOptions struct {
	BatchPages int
	MaxBatches int
	Yield      time.Duration
	Interval   time.Duration
}

// spaceReclaimer 把 SQLite 的空闲页分批还给文件系统(PRAGMA incremental_vacuum(N))。
// 回收由 Trigger(删除会话后,合并多次触发)和低频定时器驱动,跑在一条后台 goroutine 里;
// Stop 会等 goroutine 退出。回收失败只记日志,绝不影响触发方。
type spaceReclaimer struct {
	db   *gorm.DB
	opts spaceReclaimerOptions

	trigger chan struct{} // 容量 1:已有待处理触发时再触发直接合并
	stop    chan struct{}
	done    chan struct{}

	startOnce sync.Once
	stopOnce  sync.Once
}

func newSpaceReclaimer(db *gorm.DB, opts spaceReclaimerOptions) *spaceReclaimer {
	if opts.BatchPages <= 0 {
		opts.BatchPages = defaultReclaimBatchPages
	}
	if opts.MaxBatches <= 0 {
		opts.MaxBatches = defaultReclaimMaxBatches
	}
	if opts.Yield <= 0 {
		opts.Yield = defaultReclaimYield
	}
	if opts.Interval <= 0 {
		opts.Interval = defaultReclaimInterval
	}
	return &spaceReclaimer{
		db:      db,
		opts:    opts,
		trigger: make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Trigger 请求一次回收。永不阻塞:已有待处理触发时合并。Stop 之后调用是安全的空操作。
func (r *spaceReclaimer) Trigger() {
	select {
	case r.trigger <- struct{}{}:
	default:
	}
}

// Start 起后台 goroutine(只生效一次)。ctx 取消等同 Stop。
func (r *spaceReclaimer) Start(ctx context.Context) {
	r.startOnce.Do(func() { go r.run(ctx) })
}

// Stop 通知后台 goroutine 退出并等它结束;幂等,也可用于从未 Start 的实例。
func (r *spaceReclaimer) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })
	started := true
	r.startOnce.Do(func() { started = false; close(r.done) })
	if started {
		<-r.done
	}
}

func (r *spaceReclaimer) run(ctx context.Context) {
	defer close(r.done)
	// 回收 ctx 随 stop 一并取消,让正在跑的批间等待/循环立即收手。
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	go func() {
		select {
		case <-r.stop:
		case <-ctx.Done():
		}
		cancel()
	}()

	ticker := time.NewTicker(r.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			return
		case <-r.trigger:
		case <-ticker.C:
		}
		batches, err := r.reclaimOnce(runCtx)
		switch {
		case err != nil:
			logger.Ctx(runCtx).Warn("bootstrap.spaceReclaimer.run: reclaim failed", zap.Int("batches", batches), zap.Error(err))
		case batches > 0:
			logger.Ctx(runCtx).Info("bootstrap.spaceReclaimer.run: reclaimed free pages", zap.Int("batches", batches), zap.Int("batchPages", r.opts.BatchPages))
		}
	}
}

// reclaimOnce 在预算内分批回收,返回执行的批数。auto_vacuum 不是 INCREMENTAL 时什么都不做。
// 每批一条独立语句,批间让出写锁;空闲页清零、预算耗尽或 ctx 结束即停。
func (r *spaceReclaimer) reclaimOnce(ctx context.Context) (int, error) {
	var mode int
	if err := r.db.WithContext(ctx).Raw("PRAGMA auto_vacuum").Scan(&mode).Error; err != nil {
		return 0, fmt.Errorf("read auto_vacuum: %w", err)
	}
	if mode != autoVacuumIncremental {
		logger.Ctx(ctx).Debug("bootstrap.spaceReclaimer.reclaimOnce: auto_vacuum is not incremental, skipping", zap.Int("autoVacuum", mode))
		return 0, nil
	}
	batches := 0
	for batches < r.opts.MaxBatches {
		if ctx.Err() != nil {
			return batches, nil
		}
		var free int
		if err := r.db.WithContext(ctx).Raw("PRAGMA freelist_count").Scan(&free).Error; err != nil {
			return batches, fmt.Errorf("read freelist_count: %w", err)
		}
		if free == 0 {
			return batches, nil
		}
		if err := r.vacuumBatch(ctx); err != nil {
			if ctx.Err() != nil {
				return batches, nil
			}
			return batches, fmt.Errorf("incremental_vacuum: %w", err)
		}
		batches++
		select {
		case <-ctx.Done():
			return batches, nil
		case <-time.After(r.opts.Yield):
		}
	}
	return batches, nil
}

// vacuumBatch 执行一批 incremental_vacuum(N)。该 PRAGMA 是逐行步进的:每 step 一次只释放
// 一页,必须把结果集读到底整批才算做完(Exec 只 step 一次,实测只回收 1~2 页)。
func (r *spaceReclaimer) vacuumBatch(ctx context.Context) error {
	rows, err := r.db.WithContext(ctx).Raw(fmt.Sprintf("PRAGMA incremental_vacuum(%d)", r.opts.BatchPages)).Rows()
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
	}
	return rows.Err()
}
