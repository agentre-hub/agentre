// Package sync_svc 是桌面端工作区多端同步的引擎：编辑当场上行、30 秒轮询下行、
// 按基版本判冲突、墓碑、乱序暂缓、离线队列、超窗口重同步
// （docs/specs/2026-08-07-workspace-sync.md「双向同步的行为」）。
//
// 三条贯穿全包的约束：
//
//   - **同步不阻塞本地写（R8）。** 域服务只在改动落库成功之后交出一条 LocalChange，
//     入队之外的一切都在后台；同步层的任何失败都不回传、更不回滚本地写入。
//   - **未登录 = 什么都没有（R12）。** 没有账号（或本地行属于另一个账号，R13a）时
//     不入队、不发请求、不写任何同步侧的表。
//   - **载荷、路径、prompt 与 EnvJSON 一律不进日志。** 日志里只有对象类型、同步
//     标识、版本号与条数。
package sync_svc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
)

// SyncSvc 桌面端同步引擎。
type SyncSvc interface {
	// NotifyLocalChange 由域服务在改动落库成功后调用：入队并当场触发一次上行（R3）。
	// 永不返回错误，也永不阻塞调用方（R8）。
	NotifyLocalChange(ctx context.Context, ch LocalChange)
	// SyncOnce 跑一次完整来回：先把出站队列推上去，再按游标拉下来。
	SyncOnce(ctx context.Context) error
	// Start 起 30 秒周期的轮询（R3），并试着挂上账号级实时通道当第二个下行触发源；
	// 重复调用只起一次。通道连不上时只剩轮询，那本身是一个完整可用的形态。
	Start(ctx context.Context)
	// Stop 停掉轮询与实时通道，等它们真的退出才返回。
	Stop()
	// Status 设置里同步区要展示的状态。
	Status(ctx context.Context) (*Status, error)
	// ListLostChanges 「没能同步的改动」列表（R5）。
	ListLostChanges(ctx context.Context) ([]*LostChangeView, error)
	// RestoreLostChange 把某一版恢复成当前值；目标已被删除时明确失败（R5a）。
	RestoreLostChange(ctx context.Context, id int64) (*RestoreOutcome, error)
	// RecreateFromLostChange 按这份内容新建一个对象：分配新的同步标识，正常上行（R5a）。
	RecreateFromLostChange(ctx context.Context, id int64) error
	// DiscardLostChange 丢掉一条「没能同步的改动」。
	DiscardLostChange(ctx context.Context, id int64) error
	// BoardJoinNoticePending 报告「看板首次并入同步组」的一次性说明还欠着没给
	// （规格「首次上行的后果要说在前面」）。Status 里也带着同一个值。
	BoardJoinNoticePending(ctx context.Context) (bool, error)
	// AcknowledgeBoardJoinNotice 记下那条说明已经给过：之后永不再出现。
	AcknowledgeBoardJoinNotice(ctx context.Context) error
	// SetEmitter 注入「下行落地了」的通知函数；不注入就是静默（单机构建 / 单元测试）。
	SetEmitter(emit Emitter)
	// ReportLocalPathsNow 立刻上报一次本机路径整份快照（规格 2026-08-21 决策 4）。
	// 与 R16 的 30 秒轮询共用同一条路径与同一枚内容指纹——内容没变时不发请求——
	// 区别只在触发时机：从 web 改完本机路径那一刻，界面就要能读到新值。
	ReportLocalPathsNow(ctx context.Context) error
}

// Emitter 把「这一轮下行落地了哪几类对象」推给上层（生产是 Wails EventsEmit，
// 单测是 spy）。只给类型不给条数：界面据此决定重拉哪几份数据，条数它不关心。
type Emitter func(kinds []string)

// AppliedEvent 是上面那条通知在 Wails 事件总线上的名字。常量放在这里而不是让
// 两端各自手抄一个字符串 —— 前端 `stores/sync-applied.ts` 订阅的就是它。
const AppliedEvent = "sync:applied"

var defaultSvc SyncSvc

// Default 取默认实现；未装配时返回 nil —— 调用方用包级 Notify 兜底。
func Default() SyncSvc { return defaultSvc }

// SetDefault 由 bootstrap 注入实现。
func SetDefault(s SyncSvc) { defaultSvc = s }

// Notify 是域服务侧的调用入口：同步未装配（单机构建 / 单元测试）时是空操作，
// 本地写入路径因此不需要知道同步存不存在（R8/R12）。
func Notify(ctx context.Context, ch LocalChange) {
	if s := defaultSvc; s != nil {
		s.NotifyLocalChange(ctx, ch)
	}
}

// ReportLocalPathsNow 是包级调用入口：同步未装配（单机构建 / 单元测试）时是空
// 操作，写本机路径那条路径因此不需要知道同步存不存在（与 Notify 同一条口径）。
func ReportLocalPathsNow(ctx context.Context) error {
	if s := defaultSvc; s != nil {
		return s.ReportLocalPathsNow(ctx)
	}
	return nil
}

// NotifyCreate / NotifyUpdate / NotifyDelete 是三个调用点糖：域服务在改动落库
// **成功之后**交出这一行的同步元数据（同步标识由仓储层的 EnsureSyncID 生成）。
func NotifyCreate(ctx context.Context, kind string, localID int64, meta syncmeta_entity.SyncMeta) {
	Notify(ctx, LocalChange{Kind: kind, LocalID: localID, Op: OpCreate, Meta: meta})
}

func NotifyUpdate(ctx context.Context, kind string, localID int64, meta syncmeta_entity.SyncMeta) {
	Notify(ctx, LocalChange{Kind: kind, LocalID: localID, Op: OpUpdate, Meta: meta})
}

func NotifyDelete(ctx context.Context, kind string, localID int64, meta syncmeta_entity.SyncMeta) {
	Notify(ctx, LocalChange{Kind: kind, LocalID: localID, Op: OpDelete, Meta: meta})
}

type service struct {
	adapters map[string]adapter
	now      func() int64
	// background 跑一次后台同步；默认 go f()，测试注入同步执行让断言可判。
	background func(func())
	// pollEvery 是轮询周期。生产恒为 PollInterval —— 规格写死「30 秒轮询保留，
	// 不缩短也不删除」，它是通道的兜底，也是「不丢变更」的依据。这里留一个字段
	// 只为让「通道死掉时轮询照样把变更带回来」那条守卫真的跑一遍循环，而不是
	// 让测试等 30 秒或者只断言一句「没崩」。零值按 PollInterval 处理。
	pollEvery time.Duration
	// channelRetryWait 是账号级实时通道两次拨号之间的等待，返回 false 表示该收工。
	// 同样只是时钟接缝：生产等 accountChannelRetry，测试立刻返回。
	channelRetryWait func(ctx context.Context) bool

	mu        sync.Mutex
	transport Transport
	emit      Emitter
	lastErr   string
	stopCh    chan struct{}
	// doneCh 在轮询循环（连同它带起来的实时通道）真的退出之后关闭。Stop 等它：
	// 一个「已经返回、后台却还在写」的 Stop 是句空话，测试也就无从在停机之后
	// 如实读状态。
	doneCh chan struct{}
	// syncing 串行化上行/下行：轮询与「编辑当场上行」可能同时发生。
	syncing sync.Mutex
}

// New 构造引擎。transport 为 nil = 单机模式：入队照旧（未登录时连队都不入），
// 但一行也不会发出去。
func New(transport Transport) SyncSvc {
	return &service{
		adapters:         defaultAdapters(transport),
		now:              func() int64 { return time.Now().UnixMilli() },
		background:       func(f func()) { go f() },
		transport:        transport,
		pollEvery:        PollInterval,
		channelRetryWait: waitAccountChannelRetry,
	}
}

func (s *service) NotifyRuntimeClaim(ctx context.Context, ch LocalChange) {
	if !kindKnown(ch.Kind) || ch.Meta.SyncID == "" {
		return
	}
	accountID, _, _, loggedIn := s.account(ctx)
	if loggedIn && !ch.Meta.EligibleForSync(accountID) {
		return
	}
	if !loggedIn {
		// A row already owned by an account stays queued for that account; only
		// genuinely unowned legacy rows use the anonymous holding key.
		accountID = ch.Meta.SyncAccountID
	}
	if err := s.enqueue(ctx, accountID, ch); err != nil {
		logger.Ctx(ctx).Warn("sync_svc.NotifyRuntimeClaim: enqueue failed",
			zap.String("kind", ch.Kind), zap.String("op", ch.Op), zap.Error(err))
		return
	}
	if !loggedIn {
		return
	}
	bgCtx := context.WithoutCancel(ctx)
	s.background(func() {
		if err := s.SyncOnce(bgCtx); err != nil {
			logger.Ctx(bgCtx).Debug("sync_svc.NotifyRuntimeClaim: push failed, staying queued",
				zap.String("kind", ch.Kind), zap.Error(err))
		}
	})
}

func (s *service) getTransport() Transport {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transport
}

// SetEmitter 由 App.Startup 在 wails ctx 就绪后绑定（与 server_svc / cc_usage_svc
// 同一个套路）。装配之前的那几轮同步没有听众，静默是对的。
func (s *service) SetEmitter(emit Emitter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emit = emit
}

func (s *service) getEmitter() Emitter {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emit
}

func (s *service) setLastErr(err error) {
	s.mu.Lock()
	if err == nil {
		s.lastErr = ""
	} else {
		s.lastErr = err.Error()
	}
	s.mu.Unlock()
}

func (s *service) getLastErr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

// account 取当前登录账号、本机设备 ID 与本机指纹；未登录返回 ok = false（R12）。
//
// 指纹是「最后修改来自哪台机器」落库与上行时记的那个值（规格
// 2026-08-27-schema-overhaul 决策 14）：server 的 devices.id 是它自己的本地主键，
// 本机离线创建的行没有它，而本工作区其余跨机引用一律用指纹。设备 ID 仍然取着，
// 它是日志里认这台机器的那一维。
func (s *service) account(ctx context.Context) (accountID, deviceID int64, fingerprint string, ok bool) {
	row, err := server_state_repo.ServerState().Get(ctx)
	if err != nil {
		logger.Ctx(ctx).Warn("sync_svc.account: read server state failed", zap.Error(err))
		return 0, 0, "", false
	}
	if row == nil || !row.IsLoggedIn() {
		return 0, 0, "", false
	}
	return row.ServerUserID, row.DeviceID, row.DeviceFingerprint, true
}

// Start 起 30 秒周期的轮询（R3），同一个节奏也驱动本机路径的整份快照上报（R16）。
// 轮询失败只记状态，不打断循环。
//
// reportLocalPathsOnce 刻意只挂在这个 ticker 上，绝不并进 SyncOnce：
// NotifyLocalChange 会在账号级对象的编辑当场触发 SyncOnce（R3），如果上报也挂在
// SyncOnce 里，编辑一个项目名字就会连带触发一次本机路径上报——这与 R16「本地编辑
// 不即时触发上报，30 秒一轮」的刻意区分矛盾。
func (s *service) Start(ctx context.Context) {
	s.mu.Lock()
	if s.stopCh != nil {
		s.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	s.stopCh, s.doneCh = stop, done
	s.mu.Unlock()

	go func() {
		defer close(done)
		// 账号级实时通道与这个 ticker **并行**跑：它是第二个下行触发源，不是替代品
		// （规格「同步传播」：通道在时即时，通道断时最多 30 秒）。两者共用同一段
		// 生命周期——收工时先取消通道、等它退干净，再宣告 done。
		watchCtx, cancelWatch := context.WithCancel(ctx)
		var watching sync.WaitGroup
		defer watching.Wait()
		defer cancelWatch()
		watching.Add(1)
		go func() {
			defer watching.Done()
			s.watchAccountChannel(watchCtx)
		}()

		ticker := time.NewTicker(s.tickEvery())
		defer ticker.Stop()
		// 两条链路各退各的（R7/R16）：下行不通不该连带把本机路径上报也拖慢，反之亦然。
		var syncOff, reportOff backoff
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if syncOff.due() {
					if err := s.SyncOnce(ctx); err != nil {
						syncOff.fail()
						logger.Ctx(ctx).Debug("sync_svc.Start: poll failed, backing off",
							zap.Int("failStreak", syncOff.streak), zap.Error(err))
					} else {
						syncOff.succeed()
					}
				}
				if reportOff.due() {
					if err := s.reportLocalPathsOnce(ctx); err != nil {
						reportOff.fail()
						logger.Ctx(ctx).Debug("sync_svc.Start: local path report failed, backing off",
							zap.Int("failStreak", reportOff.streak), zap.Error(err))
					} else {
						reportOff.succeed()
					}
				}
			}
		}
	}()
}

// tickEvery 是轮询周期。零值（直接构造的引擎，包括测试替身）按生产的 30 秒算。
func (s *service) tickEvery() time.Duration {
	if s.pollEvery > 0 {
		return s.pollEvery
	}
	return PollInterval
}

// maxBackoffTicks 是退让的上限，按轮询周期计：30s × 60 = 30 分钟。有上限是必须的
// ——退到「永不重试」等于把 R7 的「联网后补齐」变成「联网后也不补」。
const maxBackoffTicks = 60

// backoff 落实「同步失败按退避重试」（R7、R16）。轮询的**周期不变**，退让体现在
// 「失败后跳过几个周期」上：连续失败一次跳 1 个、两次跳 2 个、三次跳 4 个……封顶
// 30 分钟，任何一次成功立即复位。
//
// 编辑当场触发的那一次上行（R3）不看它：用户刚做的改动该立刻试一次，退让只约束
// 后台轮询。
type backoff struct {
	// streak 连续失败次数。
	streak int
	// pending 还要跳过几个周期。
	pending int
}

// due 报告这一个周期该不该跑，并消耗掉一次退让额度。
func (b *backoff) due() bool {
	if b.pending > 0 {
		b.pending--
		return false
	}
	return true
}

func (b *backoff) fail() {
	b.streak++
	skip := 1 << min(b.streak-1, 30)
	b.pending = min(skip, maxBackoffTicks)
}

func (b *backoff) succeed() { b.streak, b.pending = 0, 0 }

// Stop 停掉轮询与实时通道，并**等它们真的退出**才返回。
func (s *service) Stop() {
	s.mu.Lock()
	done := s.doneCh
	if s.stopCh != nil {
		close(s.stopCh)
		s.stopCh, s.doneCh = nil, nil
	}
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}

// SyncOnce 跑一次完整来回。上行在前：本端刚做的改动先出去，再拉别人的回来，
// 少一个轮询周期的往返。
func (s *service) SyncOnce(ctx context.Context) error {
	started := time.Now()
	s.syncing.Lock()
	defer s.syncing.Unlock()

	accountID, deviceID, fingerprint, ok := s.account(ctx)
	if !ok || s.getTransport() == nil {
		return nil
	}
	if err := s.claimAnonymousQueue(ctx, accountID); err != nil {
		s.setLastErr(err)
		return err
	}
	if err := s.claimUnowned(ctx, accountID); err != nil {
		s.setLastErr(err)
		return err
	}
	if err := s.flush(ctx, accountID, fingerprint); err != nil {
		s.setLastErr(err)
		return err
	}
	if err := s.pull(ctx, accountID); err != nil {
		// server 不认识本端的游标：它的历史被重建过（或换了一套自建服务端）。这不是
		// 一次可重试的失败——同一个游标下一轮还是死的——而是要求本端重建整份历史，
		// 并把 server 不认识的本地行重新上行（rebase.go）。重推排在 rebase 之后而不是
		// 交给下一个 30 秒周期：这条路径本来就是从「静默失联」里爬出来，没有理由再等。
		if !errors.Is(err, syncwire.ErrCursorUnknown) {
			s.setLastErr(err)
			return err
		}
		if err := s.rebase(ctx, accountID); err != nil {
			s.setLastErr(err)
			return err
		}
		if err := s.flush(ctx, accountID, fingerprint); err != nil {
			s.setLastErr(err)
			return err
		}
	}
	s.setLastErr(nil)
	logger.Ctx(ctx).Debug("sync_svc.SyncOnce: completed",
		zap.Int64("accountId", accountID), zap.Int64("deviceId", deviceID),
		zap.Duration("duration", time.Since(started)))
	return nil
}

// Active 报告同步引擎是否已装配。域服务用它避开「只为拿同步标识而多查一次库」的
// 开销——单机构建与单元测试里这些查询一次都不该发生。
func Active() bool { return defaultSvc != nil }
