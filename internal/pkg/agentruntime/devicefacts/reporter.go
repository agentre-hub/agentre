package devicefacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// ChangeCheckInterval 是「本机探测结果变了没有」的检查周期。每次检查都要起几个
// `--version` 与技能包列举子进程,所以它比本机路径的 30 秒长得多;连上中继与用户
// 点刷新都不等它。
const ChangeCheckInterval = 5 * time.Minute

// Reporter 记着这台设备最近一次的情况,并决定什么时候交给 server。三个触发点:
//
//   - Connected:连上账号中继(首次与每次重连)—— 重探并整份上报;
//   - CheckForChange:周期检查 —— 重探,内容与上次**成功上报**的不同才报;
//   - Refresh:用户点「刷新」—— 立即重探并上报,不看内容变没变。
//
// 每次上报都是整份情况(不做增量):server 只留最后一份,整份替换没有合并语义,一次
// 失败的上报也不会让 server 停在半新半旧的状态。上报失败(server 还没有这个端点、
// 够不着、未登录)不推进「上次成功上报」的指纹,下一次检查自然重试。
type Reporter struct {
	collect func(ctx context.Context) (syncwire.DeviceFactsReport, error)
	upload  func(ctx context.Context, report *syncwire.DeviceFactsReport) error
	// canUpload 说「此刻报得了」(登录了账号);nil = 总是报得了。
	canUpload func() bool

	// probeMu 串行化探测与上报:刷新与周期检查同时到达时只跑一次子进程风暴的
	// 一份,且后者看得到前者推进的指纹。
	probeMu sync.Mutex

	mu         sync.Mutex
	latest     syncwire.DeviceFactsReport
	hasLatest  bool
	uploadedFP string
}

// Option 定制 NewReporter。
type Option func(*Reporter)

// WithUploadGate 给出「此刻报不报得了」(登录了账号才报得了)。门关着时周期检查连探测
// 都不做 —— 没人收的探测是白起一批子进程;刷新照样重探(界面要看),只是不上报。
func WithUploadGate(canUpload func() bool) Option {
	return func(r *Reporter) { r.canUpload = canUpload }
}

// NewReporter 用宿主的采集与上报口构造。upload 为 nil 表示这个宿主不向 server 上报
// (只供本机与直连读取)。
func NewReporter(
	collect func(ctx context.Context) (syncwire.DeviceFactsReport, error),
	upload func(ctx context.Context, report *syncwire.DeviceFactsReport) error,
	opts ...Option,
) *Reporter {
	r := &Reporter{collect: collect, upload: upload}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Latest 交回最近一次采到的情况;从没采过时 ok 为 false。
func (r *Reporter) Latest() (syncwire.DeviceFactsReport, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.latest, r.hasLatest
}

// Current 交回最近一次的情况;从没采过就当场采一次(不上报)。
func (r *Reporter) Current(ctx context.Context) (syncwire.DeviceFactsReport, error) {
	if report, ok := r.Latest(); ok {
		return report, nil
	}
	r.probeMu.Lock()
	defer r.probeMu.Unlock()
	if report, ok := r.Latest(); ok {
		return report, nil
	}
	return r.probe(ctx)
}

// Connected 见类型注释:连上中继时整份上报。
func (r *Reporter) Connected(ctx context.Context) error {
	_, err := r.probeAndReport(ctx, "connected", true)
	return err
}

// CheckForChange 见类型注释:只在内容变了时上报。
func (r *Reporter) CheckForChange(ctx context.Context) error {
	if !r.uploadable() {
		return nil
	}
	_, err := r.probeAndReport(ctx, "change", false)
	return err
}

// Refresh 见类型注释:立即重探并上报,交回这份新情况。只有探测失败才报错:上报失败
// 不影响界面显示刚探到的结果(失败已记日志,下一次检查变化时重试)。
func (r *Reporter) Refresh(ctx context.Context) (syncwire.DeviceFactsReport, error) {
	report, err := r.probeAndReport(ctx, "refresh", true)
	var upload *uploadError
	if errors.As(err, &upload) {
		return report, nil
	}
	return report, err
}

// Run 按 every 周期检查变化,直到 ctx 结束。
func (r *Reporter) Run(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = r.CheckForChange(ctx) // 失败已在 probeAndReport 记过,下一拍重试
		}
	}
}

func (r *Reporter) probe(ctx context.Context) (syncwire.DeviceFactsReport, error) {
	report, err := r.collect(ctx)
	if err != nil {
		logger.Ctx(ctx).Warn("devicefacts.Reporter: collect failed, keeping previous facts", zap.Error(err))
		return syncwire.DeviceFactsReport{}, err
	}
	r.mu.Lock()
	r.latest, r.hasLatest = report, true
	r.mu.Unlock()
	return report, nil
}

func (r *Reporter) probeAndReport(ctx context.Context, trigger string, always bool) (syncwire.DeviceFactsReport, error) {
	r.probeMu.Lock()
	defer r.probeMu.Unlock()

	report, err := r.probe(ctx)
	if err != nil {
		return syncwire.DeviceFactsReport{}, err
	}
	if !r.uploadable() {
		return report, nil
	}
	fp := contentFingerprint(report)
	r.mu.Lock()
	unchanged := fp == r.uploadedFP
	r.mu.Unlock()
	if unchanged && !always {
		return report, nil
	}
	if err := r.upload(ctx, &report); err != nil {
		// 不是故障:server 可能还没有这个端点、此刻够不着或本机未登录,下一次触发重试。
		logger.Ctx(ctx).Debug("devicefacts.Reporter: report failed, will retry on next trigger",
			zap.String("trigger", trigger), zap.Error(err))
		return report, &uploadError{err: err}
	}
	r.mu.Lock()
	r.uploadedFP = fp
	r.mu.Unlock()
	logger.Ctx(ctx).Debug("devicefacts.Reporter: reported",
		zap.String("trigger", trigger), zap.Int("runtimeCount", len(report.Runtimes)))
	return report, nil
}

func (r *Reporter) uploadable() bool {
	return r.upload != nil && (r.canUpload == nil || r.canUpload())
}

// uploadError 标出「采到了,只是没交上去」,Refresh 据此与探测失败分开。
type uploadError struct{ err error }

func (e *uploadError) Error() string { return "report device facts: " + e.err.Error() }
func (e *uploadError) Unwrap() error { return e.err }

// contentFingerprint 是情况内容的指纹:采集时刻不算内容,时刻变了而探测结果没变,
// 不该白发一次请求。
func contentFingerprint(report syncwire.DeviceFactsReport) string {
	report.CollectedAt = 0
	body, _ := json.Marshal(report)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
