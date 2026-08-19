// Package remote — reconnect.go 是桌面端的断连重连状态机。
//
// 连接关闭 → 会话转入重连态(会话不关闭、事件 channel 不关闭)→ 退避重连 → 连上后
// 校验 daemon 实例标识 → 补齐三步(attach → pull → pendingWaiters)→ 回到实时。
// 重连尝试超上限、daemon 不支持补齐族 RPC、或游标失效时,才走今天的老路:注入
// ErrDaemonDisconnected 并 close events。
//
// 补齐的通知**喂回与实时同一套 handler**(notifyHandlers 是唯一注册来源),所以
// 「补齐后的序列与全程不断连时逐条相等」是结构性成立的,不只是被测试覆盖。
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-ai/agentre/internal/pkg/jsonrpc"
)

// ConnState 是一条远端会话此刻的**通道**状态。它是运行态之上的一层修饰,不是第五种
// 运行状态 —— 会话在重连期间仍然是「运行中」,只是通道断了。
type ConnState string

const (
	// ConnStateConnected 实时推送可用(含补齐完成后回到实时)。
	ConnStateConnected ConnState = "connected"
	// ConnStateReconnecting 通道断了,远端仍在跑,客户端正在退避重连。
	ConnStateReconnecting ConnState = "reconnecting"
	// ConnStateLost 重连放弃(超上限 / daemon 不支持补齐 / 游标失效),会话已按
	// 今天的语义收尾(注入 ErrDaemonDisconnected 并 close events)。
	ConnStateLost ConnState = "lost"
)

// SessionConnState 是一次连接态变化的完整描述。上层(chat_svc)据此向前端播一条
// 会话级连接态事件 —— 连接态不进 AgentStatus,那个联合类型仍然只有四个取值。
type SessionConnState struct {
	State ConnState
	// Replayed 本次补齐重放的通知条数,仅 ConnStateConnected 有意义。
	Replayed int
	// PendingDecisions 补齐后该会话仍在阻塞的待决策条数,仅 ConnStateConnected 有意义。
	PendingDecisions int
}

// ReconnectPort 重新拨通**同一台** daemon,返回一条已鉴权的新连接与该 daemon 的实例
// 标识("sha256:<hex>" 指纹形,与 chat_sessions.exec_daemon_fingerprint 同形)。
//
// 按 DIP 声明在消费方:internal/pkg 是叶子层,拨号要用的设备行 / keychain / 连接池
// 都在 service 层,实现由 chat_svc 在构造 Runtime 时注入。
type ReconnectPort interface {
	Reconnect(ctx context.Context) (agentruntime.DaemonClientPort, string, error)
}

// ReconnectFunc 是 ReconnectPort 的函数式实现。
type ReconnectFunc func(ctx context.Context) (agentruntime.DaemonClientPort, string, error)

// Reconnect 实现 ReconnectPort。
func (f ReconnectFunc) Reconnect(ctx context.Context) (agentruntime.DaemonClientPort, string, error) {
	return f(ctx)
}

// ConnStateObserver 接收会话级连接态变化。
type ConnStateObserver interface {
	OnSessionConnState(sessionID int64, st SessionConnState)
}

// ConnStateFunc 是 ConnStateObserver 的函数式实现。
type ConnStateFunc func(sessionID int64, st SessionConnState)

// OnSessionConnState 实现 ConnStateObserver。
func (f ConnStateFunc) OnSessionConnState(sessionID int64, st SessionConnState) { f(sessionID, st) }

// Option 是 New 的可选配置。
type Option func(*Runtime)

// WithReconnect 注入重连端口。**没有它就没有重连**:断连一律回落到今天的
// 「注入 ErrDaemonDisconnected 并 close events」。
func WithReconnect(p ReconnectPort) Option { return func(r *Runtime) { r.reconnect = p } }

// WithDaemonFingerprint 声明当前这条连接背后的 daemon 实例标识(指纹形)。
func WithDaemonFingerprint(fp string) Option { return func(r *Runtime) { r.daemonFP = fp } }

// WithConnStateObserver 注入连接态观察者。
func WithConnStateObserver(o ConnStateObserver) Option {
	return func(r *Runtime) { r.connObserver = o }
}

// DurabilityObserver 接收 R18 的能力探测结论:对面这台 daemon 认不认补齐族 RPC。
//
// 探测结果只活在 *remote.Runtime 里,而 R18 要求「配对设备状态里说明该 daemon 版本
// 过旧」—— 设备行在 service 层,internal/pkg 是叶子层够不着它。按 DIP 在消费方声明
// 这个窄端口,由构造 Runtime 的一方(chat_svc)注入实现,结论才出得去。
type DurabilityObserver interface {
	OnDaemonDurability(supported bool)
}

// DurabilityFunc 是 DurabilityObserver 的函数式实现。
type DurabilityFunc func(supported bool)

// OnDaemonDurability 实现 DurabilityObserver。
func (f DurabilityFunc) OnDaemonDurability(supported bool) { f(supported) }

// WithDurabilityObserver 注入能力探测的观察者。
func WithDurabilityObserver(o DurabilityObserver) Option {
	return func(r *Runtime) { r.durabilityObs = o }
}

// WithSessionCursor 覆盖游标端口(默认取 agentruntime.SessionCursor())。
func WithSessionCursor(p agentruntime.SessionCursorPort) Option {
	return func(r *Runtime) { r.cursorPort = p }
}

// WithReconnectBackoff 覆盖退避重连的节奏表;档数即最大尝试次数。
func WithReconnectBackoff(schedule []time.Duration) Option {
	return func(r *Runtime) { r.backoff = schedule }
}

// WithCursorFlushInterval 覆盖游标落库的防抖间隔;<=0 表示同步落库(测试用)。
func WithCursorFlushInterval(d time.Duration) Option {
	return func(r *Runtime) { r.cursorFlush = d }
}

// defaultReconnectBackoff 退避重连的节奏表:总跨度约 1 分钟,足够跨过一次 Wi-Fi
// 漫游 / VPN 重拨,又不会让一台真的关机了的 daemon 把会话无限期吊着。
var defaultReconnectBackoff = []time.Duration{
	time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second,
	15 * time.Second, 15 * time.Second, 15 * time.Second,
}

// defaultCursorFlushInterval 游标落库的防抖间隔。SaveCursor 一次一个事务且会 bump
// updatetime,而它落在通知热路径上(每条通知一次)—— 按间隔合并写。
const defaultCursorFlushInterval = 2 * time.Second

// durabilityState 是「这台 daemon 认不认补齐族 RPC」的三态探测结果(R18)。
// 探测失败(网络抖动之类)保持 unknown:一次拨不通不该把持久化关掉。
type durabilityState int

const (
	durabilityUnknown durabilityState = iota
	durabilitySupported
	durabilityUnsupported
)

// ErrReconnectAbandoned 让重连端口宣告「不必再试了」:设备已被解除配对、凭据已撤销、
// 连接池已关停 —— 这类失败重试多少次都是同一个结果,继续按退避表试只会把会话在
// 「重连中」多吊几十秒。返回它(或 wrap 它)会立刻走「注入 ErrDaemonDisconnected」。
var ErrReconnectAbandoned = errors.New("agentruntime/runtimes/remote: reconnect abandoned")

// ErrCatchUpUnsupported 补齐族 RPC 回 method-not-found —— 对面是老 daemon(R18)。
//
// 它是导出的:CatchUpSessions 的调用方(chat_svc)必须把它与「这一次没问到」分开 ——
// 后者等设备回来再问一遍就有答案,而这台 daemon 这辈子都答不了,那些会话只能当场按
// R18 的回落收尾(老 daemon 上断连即结束该轮)。
var ErrCatchUpUnsupported = errors.New("agentruntime/runtimes/remote: daemon does not support session catch-up")

// errSessionUnrecoverable 这条会话接不回去了(daemon 侧已中断 / 不存在 / 游标失效),
// 但连接本身是好的:只收尾这一条会话,别的会话照常补齐。
var errSessionUnrecoverable = errors.New("agentruntime/runtimes/remote: session cannot be resumed")

// sessionSync 是一条会话的补齐状态。cursor 跨轮存活(sessions 只活在一轮之内),
// mu 串行化「实时投递」与「补齐重放」——两条路径喂的是同一套 handler,不串起来
// 会把顺序搅乱,而顺序正是本规格的硬不变量。
//
// 纪律:持 mu 期间**绝不**发 RPC。通知是在读循环里同步分发的(rpc/conn.go:118),
// 持锁调 Call 会让应答帧永远读不回来。
type sessionSync struct {
	mu     sync.Mutex
	cursor int64
	loaded bool
	// filling 补洞在飞。同一会话任一时刻至多一次拉取。
	filling bool
	// floorSeq / floorGen 是「这条会话的日志高水位在**哪一代连接**上探到过、探到多少」
	// (见 turnStartFloor)。按连接代作废:换了连接就可能错过了 daemon 在断连期间新增
	// 的行,那时候的旧值会偏小。
	floorSeq int64
	floorGen int64
}

// floorOnConn 交出这条会话在 gen 这代连接上已知的日志高水位:探到过的那个值与游标取
// 较大者 —— 游标只由 daemon 真的发过的 seq 推进,它本身就是一份「日志至少长到这里」的
// 证据,而且比探测那一刻更新。本代还没探过时 ok 为 false。
func (s *sessionSync) floorOnConn(gen int64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.floorGen != gen {
		return 0, false
	}
	return max(s.floorSeq, s.cursor), true
}

// rememberFloor 记下这一代连接上探到的高水位,并交出与游标取较大者后的开轮位置。
func (s *sessionSync) rememberFloor(gen, seq int64) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.floorGen, s.floorSeq = gen, seq
	return max(seq, s.cursor)
}

// cursorNow 读一眼当前游标。
func (s *sessionSync) cursorNow() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor
}

// stateFor 取(或建)某会话的补齐状态,**不**碰游标端口。
func (r *Runtime) stateFor(sid int64) *sessionSync {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	ss, ok := r.sessionState[sid]
	if !ok {
		ss = &sessionSync{}
		r.sessionState[sid] = ss
	}
	return ss
}

// syncFor 取(或建)某会话的补齐状态,并保证游标已从端口读过一次。
func (r *Runtime) syncFor(ctx context.Context, sid int64) *sessionSync {
	ss := r.stateFor(sid)
	r.ensureCursorLoaded(ctx, sid, ss)
	return ss
}

// ensureCursorLoaded 惰性把持久化游标读进内存。读不到(会话不是远端跑的 / 实例标识
// 对不上 / 端口没接)时游标留在 0 —— 后续第一条实时帧就会看成跳号并整段补齐,
// 这正是想要的兜底。
func (r *Runtime) ensureCursorLoaded(ctx context.Context, sid int64, ss *sessionSync) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.loaded {
		return
	}
	ss.loaded = true
	port := r.cursor()
	if port == nil {
		return
	}
	seq, ok, err := port.LoadCursor(ctx, sid, r.fingerprint())
	if err != nil {
		logger.Ctx(ctx).Warn("remote runtime: load session cursor failed",
			zap.Int64("sid", sid), zap.Error(err))
		return
	}
	if ok && seq > ss.cursor {
		ss.cursor = seq
	}
}

// ── 通知分发(实时 + 补齐共用)────────────────────────────────────────────────

// dispatchNotification 是五类通知的统一入口。R6 的三条规则都在这里:
//   - seq == 游标 + 1  → 消费并推进游标
//   - seq >  游标 + 1  → **不消费**,改从游标发起一次增量拉取,拉平后再继续
//   - seq <= 游标      → 丢弃(重复投递)
//
// seq == 0 表示对面是不盖 seq 的老 daemon(帧上是 omitempty 的可选追加字段),
// 此时没有游标可言,直接消费,行为与今天完全一致。
func (r *Runtime) dispatchNotification(
	ctx context.Context,
	method string,
	h notifyHandler,
	raw json.RawMessage,
) (any, error) {
	var head struct {
		SessionID int64 `json:"sessionId"`
		Seq       int64 `json:"seq"`
	}
	if err := json.Unmarshal(raw, &head); err != nil || head.Seq == 0 {
		return h(r, ctx, raw)
	}
	ss := r.syncFor(ctx, head.SessionID)

	ss.mu.Lock()
	switch {
	case head.Seq <= ss.cursor:
		ss.mu.Unlock()
		logger.Ctx(ctx).Debug("remote runtime: duplicate notification dropped",
			zap.Int64("sid", head.SessionID), zap.Int64("seq", head.Seq),
			zap.String("method", method))
		return nil, nil
	case head.Seq > ss.cursor+1:
		cursor := ss.cursor
		ss.mu.Unlock()
		// 只在真的起了一次拉取时打一行:一个洞后面跟着的每一条实时帧都会走到这里,
		// 按帧打就是在通知循环里打日志(observability.md:「不要在循环内打日志」)。
		if r.scheduleGapFill(head.SessionID, ss) {
			logger.Ctx(ctx).Warn("remote runtime: notification seq gap, pulling",
				zap.Int64("sid", head.SessionID), zap.Int64("cursor", cursor),
				zap.Int64("seq", head.Seq), zap.String("method", method))
		}
		return nil, nil
	}
	res, err := h(r, ctx, raw)
	ss.cursor = head.Seq
	// 待落库值与内存游标在同一把 ss.mu 之下更新,见 recordCursor。
	r.recordCursor(head.SessionID, head.Seq)
	ss.mu.Unlock()
	// cursorFlush<=0 是同步落库模式(测试用)。轮末那一条则不能压在防抖窗口里:用户
	// 拿到答案随手关窗(2 秒内进程就没了),库里的游标会停在这一轮的中段,下一轮的第
	// 一条实时帧对着它判成跳号,把上一轮的尾巴整段重放进新的一轮 —— 硬不变量的
	// 「无重复」当场破掉。
	if r.cursorFlush <= 0 || terminalNotifyMethods[method] {
		r.flushCursors()
	}
	return res, err
}

// terminalNotifyMethods 是「一轮到此为止」的两类通知。它们之后不再有同轮通知,
// 所以此刻的游标就是这一轮的终值,值得一次同步落库(每轮一次,不在热路径上)。
var terminalNotifyMethods = map[string]bool{
	wire.NotifyRunResultDone:      true,
	wire.NotifyAutonomousTurnDone: true,
}

// scheduleGapFill 起一次补洞拉取。必须异步:通知在读循环里同步分发,就地发 RPC
// 会让应答帧永远读不回来(rpc/conn.go:118 的注释写明了这条纪律)。
//
// 同一会话任一时刻至多一次拉取;返回值告诉调用方本次是否真的起了一次(调用方据此
// 决定要不要打日志 —— 一个洞后面的每一条实时帧都会走到这里)。
func (r *Runtime) scheduleGapFill(sid int64, ss *sessionSync) bool {
	ss.mu.Lock()
	if ss.filling {
		ss.mu.Unlock()
		return false
	}
	ss.filling = true
	ss.mu.Unlock()
	go func() {
		defer func() {
			ss.mu.Lock()
			ss.filling = false
			ss.mu.Unlock()
		}()
		ctx := context.Background()
		if _, err := r.pullUntilCaughtUp(ctx, sid, ss); err != nil {
			logger.Default().Warn("remote runtime: gap fill failed",
				zap.Int64("sid", sid), zap.Error(err))
		}
	}()
	return true
}

// pullUntilCaughtUp 按游标翻页拉取并重放,直到 daemon 说没有更多。返回重放条数。
//
// 每页拉完才重放,重放期间**不持** ss.mu(dispatchNotification 自己按帧取):
// 实时帧与重放帧因此可以任意交错而不出错 —— 顺序由 seq 闸门保证,不靠一把大锁。
func (r *Runtime) pullUntilCaughtUp(ctx context.Context, sid int64, ss *sessionSync) (int, error) {
	replayed := 0
	for {
		before := ss.cursorNow()

		// 发 RPC 时绝不持 ss.mu,见 sessionSync 的纪律注释。
		var res wire.SessionPullResult
		if err := r.conn().Call(ctx, wire.MethodSessionPull, wire.SessionPullParams{
			SessionID: sid, Cursor: before, PeerFingerprint: r.originFor(sid),
		}, &res); err != nil {
			return replayed, wire.FromJSONRPCError(err)
		}
		// 复位必须在重放**之前**:这一页的第一条要么正是游标+1,要么就落在被回收掉的
		// 那段之后 —— 后者不先复位,它当场被判成跳号丢弃,这一页一条也交付不出去。
		r.dropCursorBelowOldest(ctx, sid, ss, res.OldestSeq)
		for _, n := range res.Notifications {
			delivered, err := r.replay(ctx, sid, ss, n)
			if err != nil {
				return replayed, err
			}
			if delivered {
				replayed++
			}
		}
		// 这一页有没有真的消费掉:游标停在页尾那一条上就是消费掉了。停在它之前只有一种
		// 可能 —— 复位把游标抬到了这一页某些行之上(daemon 读下界与读行之间跑了一轮留存
		// 回收,下界因此偏低而行更靠后),那些行在闸门里被判成跳号丢掉了。此时还得接着
		// 拉:不拉就带着 oldest-1 的游标收工,存活的那截要等下一条实时帧再触发一次补洞
		// 才补得上,而终态会话根本不会再有实时帧。
		pageConsumed := ss.cursorNow() >= res.Cursor
		// 空页、daemon 说没有更多且这一页也消费完了、或游标根本没前进(载荷坏了 / daemon
		// 一直交同一页)都停。最后一条同时是防死循环的闸:每一圈都必须把游标推前。
		if len(res.Notifications) == 0 || (!res.HasMore && pageConsumed) || ss.cursorNow() <= before {
			return replayed, nil
		}
	}
}

// replay 把日志里的一行喂回**实时同一个入口**(dispatchNotification),因此重放帧
// 与实时帧受同一套 seq 规则约束:重复的被丢、跳号的触发补洞。
//
// 日志里的 payload **不含 seq**(seq 是日志行自己的列),直接喂进去每一帧都解出
// seq=0,闸门会当成「老 daemon 不盖 seq」放行却不推进游标 —— 补齐看似成功,游标
// 却原地不动,下一条实时帧立刻判成跳号,把刚补的这段再重放一遍(重复投递)。
// 所以必须按 method 解成对应的帧、盖上 seq、再重新序列化。
func (r *Runtime) replay(ctx context.Context, sid int64, ss *sessionSync, n wire.JournaledNotification) (bool, error) {
	h, ok := notifyHandlers[n.Method]
	if !ok {
		// 未知 method:新版 daemon 加了第六类通知而本客户端还不认识。跳过而不是
		// 整段补齐失败 —— 停在这里会让后面所有已知通知也丢掉。
		logger.Ctx(ctx).Warn("remote runtime: replay skipped unknown notification method",
			zap.String("method", n.Method), zap.Int64("seq", n.Seq))
		r.skipSeq(sid, ss, n.Seq)
		return false, nil
	}
	raw, err := stampSeq(n.Method, n.Params, n.Seq)
	if err != nil {
		return false, fmt.Errorf("stamp seq on %s: %w", n.Method, err)
	}
	if _, err := r.dispatchNotification(ctx, n.Method, h, raw); err != nil {
		return false, err
	}
	return true, nil
}

// skipSeq 让一条交付不出去的通知照样占掉它那一格游标,规则与 dispatchNotification
// 的闸门一致(只有 seq == 游标 + 1 才推进)。
//
// 不推进的后果不是「少一条」而是「一条也不剩」:游标停在洞前,后面每一条已知通知
// 都会被判成跳号丢弃,而补洞拉取又原样把这条认不得的通知再拉回来 —— 会话就此卡死
// 在拉取死循环里,连终态帧都收不到。
func (r *Runtime) skipSeq(sid int64, ss *sessionSync, seq int64) {
	ss.mu.Lock()
	if seq != ss.cursor+1 {
		ss.mu.Unlock()
		return
	}
	ss.cursor = seq
	r.recordCursor(sid, seq)
	ss.mu.Unlock()
	if r.cursorFlush <= 0 { // 同步落库模式(测试用)
		r.flushCursors()
	}
}

// stampSeq 按 method 把日志载荷解成对应的帧、盖上 seq、再重新序列化。
func stampSeq(method string, params json.RawMessage, seq int64) (json.RawMessage, error) {
	stamp, ok := seqStampers[method]
	if !ok {
		return nil, fmt.Errorf("no frame mapping for %s", method)
	}
	return stamp(params, seq)
}

// seqStampers 是 method → 帧 的映射。wire 包只定义帧类型,不知道哪个 method 对应
// 哪个帧,所以映射建在使用方。它必须与 notifyHandlers 同集合(见 guard 测试)。
var seqStampers = map[string]func(json.RawMessage, int64) (json.RawMessage, error){
	wire.NotifyEvent:                 stampEventFrame,
	wire.NotifyAutonomousTurnEvent:   stampEventFrame,
	wire.NotifyRunResultDone:         stampDoneFrame,
	wire.NotifyAutonomousTurnDone:    stampDoneFrame,
	wire.NotifyAutonomousTurnStarted: stampStartedFrame,
}

func stampEventFrame(params json.RawMessage, seq int64) (json.RawMessage, error) {
	var f wire.EventFrame
	if err := json.Unmarshal(params, &f); err != nil {
		return nil, err
	}
	f.SetSeq(seq)
	return json.Marshal(f)
}

func stampDoneFrame(params json.RawMessage, seq int64) (json.RawMessage, error) {
	var f wire.RunResultDoneFrame
	if err := json.Unmarshal(params, &f); err != nil {
		return nil, err
	}
	f.SetSeq(seq)
	return json.Marshal(f)
}

func stampStartedFrame(params json.RawMessage, seq int64) (json.RawMessage, error) {
	var f wire.AutonomousTurnStartedFrame
	if err := json.Unmarshal(params, &f); err != nil {
		return nil, err
	}
	f.SetSeq(seq)
	return json.Marshal(f)
}

// ── 重连状态机 ──────────────────────────────────────────────────────────────

// watchClose 是连接生命周期的主循环:一条连接死了就退避重连、补齐、接着监听新连接。
// 只有「不具备重连能力」或「重连彻底失败」才走 failAllSessions —— 今天的那条老路。
func (r *Runtime) watchClose(closed <-chan struct{}) {
	for {
		select {
		case <-closed:
		case <-r.stopped:
			return
		}
		select {
		case <-r.stopped:
			return
		default:
		}
		if !r.canReconnect() || !r.hasLiveRun() {
			r.failAllSessions()
			return
		}
		r.enterReconnecting()
		next, ok := r.reconnectAndCatchUp()
		if !ok {
			r.failAllSessions()
			return
		}
		closed = next
	}
}

// canReconnect 有没有重连的资格:装了重连端口,且这台 daemon 没被证伪过(R18)。
func (r *Runtime) canReconnect() bool {
	if r.reconnect == nil {
		return false
	}
	r.connMu.Lock()
	defer r.connMu.Unlock()
	return r.durability != durabilityUnsupported
}

// hasLiveRun 有没有在飞的一轮 —— per-Run 的那一轮,或某条会话上正在流的自主续轮。
// 没有就不值得重连:连接池在 refcount 归零后会主动 idle 回收连接,那时重连只会再借
// 一条谁也不会归还的连接,把套接字永久钉住。
//
// 自主续轮必须算进来:r.sessions 在 runResultDone 时就清空了,而 CLI 发起的自主轮
// (后台任务续轮)整段都发生在那之后 —— 只看 r.sessions 会让这类断连直接走
// failAllSessions → closeAllAutoSessions,在流中途 close(cur.events) 且 StopErr
// 仍是 nil,chat_svc 于是把一条**被截断的**助手消息当作正常完成的轮次持久化。
// 这也正是 liveSessionIDs 为 enterReconnecting / catchUpAll 并上 autoSessions 的理由。
//
// 但看的必须是「有没有在飞的那一轮」(a.cur)而不是「有没有条目」:autoSessions 的
// 条目在订阅(AutonomousTurns)时创建、直到 conn close 才删,按条目设闸会让运行时在
// 空闲之后永远重连下去。
//
// 快照后再取 a.mu:持 r.mu 期间去拿 a.mu,会与「持 a.mu 投递事件」的通知路径叠成
// 一条不必要的等待链(autoturn.go 的既有纪律是先在 r.mu 下快照,再逐个上 a.mu)。
func (r *Runtime) hasLiveRun() bool {
	r.mu.RLock()
	// tracked 是 App 启动时按 exec_device_id 找回来、正在补齐的那些会话:它们在本
	// 进程里没有轮次,却恰恰是本轮要接住的对象 —— 不算进来,重启后断一次线就再也
	// 接不回去了(watchClose 会走 failAllSessions 并退出循环)。
	if len(r.sessions) > 0 || len(r.tracked) > 0 {
		r.mu.RUnlock()
		return true
	}
	autos := make([]*autoSession, 0, len(r.autoSessions))
	for _, a := range r.autoSessions {
		autos = append(autos, a)
	}
	r.mu.RUnlock()
	for _, a := range autos {
		a.mu.Lock()
		live := a.cur != nil
		a.mu.Unlock()
		if live {
			return true
		}
	}
	return false
}

// reconnectAndCatchUp 按退避表重连并补齐。成功返回新连接的 Closed() 通道。
func (r *Runtime) reconnectAndCatchUp() (<-chan struct{}, bool) {
	ctx := context.Background()
	for _, delay := range r.backoff {
		select {
		case <-time.After(delay):
		case <-r.stopped:
			return nil, false
		}
		cli, fp, err := r.reconnect.Reconnect(ctx)
		if errors.Is(err, ErrReconnectAbandoned) {
			logger.Default().Warn("remote runtime: reconnect abandoned by port", zap.Error(err))
			return nil, false
		}
		if err != nil || cli == nil {
			logger.Default().Warn("remote runtime: reconnect attempt failed", zap.Error(err))
			continue
		}
		r.adoptConn(cli, fp)
		if err := r.catchUpAll(ctx); err != nil {
			if errors.Is(err, ErrCatchUpUnsupported) {
				logger.Default().Warn("remote runtime: daemon has no catch-up RPCs, ending turn on disconnect")
				return nil, false
			}
			logger.Default().Warn("remote runtime: catch-up failed, retrying", zap.Error(err))
			continue
		}
		closed := cli.Closed()
		if closed == nil {
			closed = make(chan struct{})
		}
		return closed, true
	}
	logger.Default().Warn("remote runtime: reconnect attempts exhausted",
		zap.Int("attempts", len(r.backoff)))
	return nil, false
}

// adoptConn 换上新连接:五类通知 + MCP 隧道的 handler 要在新连接上原样再挂一遍
// (新连接自带一张空的 handler 表),能力探测结果按代重置。
func (r *Runtime) adoptConn(cli agentruntime.DaemonClientPort, fp string) {
	r.connMu.Lock()
	r.client = cli
	if fp != "" {
		r.daemonFP = fp
	}
	r.durability = durabilityUnknown
	// 连接代 +1:开轮位置的那份已知(sessionSync.floorSeq)只在探到它的那条连接上作数
	// —— 断连期间 daemon 可能又落了几行,旧值会偏小(见 turnStartFloor)。
	r.connGen++
	r.connMu.Unlock()
	r.registerHandlers(cli)
}

// catchUpAll 对每条本地还认得的会话跑一遍补齐三步:在飞的一轮、自主续轮镜像,以及
// App 启动时按 exec_device_id 找回来、本进程内并没有轮次在跑的那些(见 catchup.go)。
func (r *Runtime) catchUpAll(ctx context.Context) error {
	return r.catchUpEach(ctx, r.catchUpSessionIDs())
}

// catchUpEach 逐条补齐,并按错误种类分流:整条连接不支持就整体放弃,单条会话接不
// 回去只收尾它。
func (r *Runtime) catchUpEach(ctx context.Context, sids []int64) error {
	for _, sid := range sids {
		err := r.catchUpSession(ctx, sid)
		switch {
		case err == nil:
		case errors.Is(err, ErrCatchUpUnsupported):
			return err
		case errors.Is(err, errSessionUnrecoverable):
			// 只这一条接不回去:按今天的语义收尾它,别的会话照常补齐。
			logger.Ctx(ctx).Warn("remote runtime: session cannot be resumed, ending it",
				zap.Int64("sid", sid))
			r.failSession(sid)
		default:
			return err
		}
	}
	return nil
}

// catchUpSession 是补齐三步。顺序是硬的:
//  0. 钉游标 —— 校验实例标识并读回游标,**必须在 attach 之前**(见 pinCursorFor:
//     接管一受理,daemon 就把实时帧推到这条连接上并并发推进游标,越界守卫要比对的
//     是接管那一刻的值)。这一步只读桌面库、不发 RPC,期间 daemon 落下的通知照常
//     进日志,随后的 pull 一条不落地取回来;
//  1. attach —— 认领实时推送流 **与** 控制面(少了它,补齐完仍收不到实时通知,
//     提交决策还会被 daemon 的幂等折叠吞掉,会话永久挂起);
//  2. 按游标翻页拉取并重放;
//  3. pendingWaiters —— 断连前就已阻塞、宣告事件早在游标之前的那些待决策,
//     只能靠这一步找回来。
func (r *Runtime) catchUpSession(ctx context.Context, sid int64) error {
	ss, pinned, err := r.pinCursorFor(ctx, sid)
	if err != nil {
		return err
	}
	highWater, err := r.attachSession(ctx, sid)
	if err != nil {
		return err
	}
	r.dropCursorAboveHighWater(ctx, sid, ss, pinned, highWater)
	replayed, err := r.pullUntilCaughtUp(ctx, sid, ss)
	if err != nil {
		return err
	}
	pend, err := r.PendingWaiters(ctx, sid)
	if err != nil {
		// 待决策查询失败不推翻已经补齐的转录:重放已经落定,卡片最差是少一张,
		// 用户仍可看到全部历史。
		logger.Ctx(ctx).Warn("remote runtime: pending waiters query failed",
			zap.Int64("sid", sid), zap.Error(err))
	}
	pending := len(pend.ToolPermissions) + len(pend.AskUserQuestions)
	logger.Ctx(ctx).Info("remote runtime: session caught up",
		zap.Int64("sid", sid), zap.Int("replayed", replayed),
		zap.Int("pendingDecisions", pending))
	r.setSessionConnState(sid, SessionConnState{
		State: ConnStateConnected, Replayed: replayed, PendingDecisions: pending,
	})
	return nil
}

// attachSession 显式接管:声明「这条会话此后由我消费」。可反复发起 —— 桌面端同时
// 握着 2-3 条同指纹连接(连接池 / 心跳 / 刷新探测),每条新连接都会把 runtime.*
// 重新注册进 daemon 的共享 registry 并带一张空的会话表,把上一次接管静默还原。
//
// 返回接管时 daemon 交回的高水位(该会话通知日志里此刻的 MAX(seq)),补齐据它校验
// 本地游标有没有越界(见 dropCursorAboveHighWater)。失败时返 0。
func (r *Runtime) attachSession(ctx context.Context, sid int64) (int64, error) {
	var att wire.SessionAttachResult
	err := r.conn().Call(ctx, wire.MethodSessionAttach, wire.SessionAttachParams{
		SessionID: sid, PeerFingerprint: r.originFor(sid),
	}, &att)
	if err == nil {
		r.setDurability(durabilitySupported)
		return att.LatestSeq, nil
	}
	if isMethodNotFound(err) {
		r.setDurability(durabilityUnsupported)
		return 0, ErrCatchUpUnsupported
	}
	r.setDurability(durabilitySupported)
	sentinel := wire.FromJSONRPCError(err)
	if errors.Is(sentinel, agentruntime.ErrSessionNotFound) || errors.Is(sentinel, agentruntime.ErrNoActiveTurn) {
		// daemon 侧已把它标成中断态(进程重启),或它根本不在这台 daemon 上。
		return 0, errSessionUnrecoverable
	}
	return 0, sentinel
}

// pinCursorFor 在**接管之前**把这条会话的游标钉住:校验实例标识、读回持久化游标、与
// 内存里的取较大者,交回钉住的那个值(越界守卫唯一该比对的东西)。
//
// 为什么非在接管之前不可:daemon 在 Attach 受理之后**立刻**把本连接认领为该会话的推送
// 目标(daemon.go:739),此后的实时帧由读循环消费并推进 ss.cursor —— 一条已经完全补齐、
// 仍在流式输出的会话(游标 == 高水位)重连时,认领后紧跟着的那一条就会把游标推过高水位。
// 越界守卫若比对这个被并发推进过的值,就把一次**正常的实时推进**判成日志回退:整本重放、
// 用户正在看的回答被再追加一遍、库里还落一个 0 让下次启动照做一遍。接管之前 daemon 还没
// 认领本连接,这条会话一条实时帧也进不来,钉在这里的值没人能并发推进它。
//
// 两种失败要分开:实例标识对不上是 R12 的**判定**(errSessionUnrecoverable,这条
// 会话就此收尾);读不出来只是这一次没读到(库正忙、事务冲突),交出普通错误让整轮
// 补齐按退避表重来 —— 把它也算作失效,等于一次 sqlite busy 就杀掉一条远端还在跑的
// 会话,正是 R4 要消灭的行为。
//
// 内存游标与库里的取较大者:库里的那份是防抖落库的,进程内已经消费到更远是常事,
// 用旧值会把已经交付过的通知再重放一遍 —— 那是重复,同样破坏硬不变量。
func (r *Runtime) pinCursorFor(ctx context.Context, sid int64) (*sessionSync, int64, error) {
	r.stateMu.Lock()
	ss, ok := r.sessionState[sid]
	if !ok {
		ss = &sessionSync{}
		r.sessionState[sid] = ss
	}
	r.stateMu.Unlock()

	port := r.cursor()
	if port == nil {
		ss.mu.Lock()
		ss.loaded = true
		pinned := ss.cursor
		ss.mu.Unlock()
		return ss, pinned, nil
	}
	seq, valid, err := port.LoadCursor(ctx, sid, r.fingerprint())
	if err != nil {
		logger.Ctx(ctx).Warn("remote runtime: load session cursor failed on reconnect",
			zap.Int64("sid", sid), zap.Error(err))
		return nil, 0, fmt.Errorf("load session cursor: %w", err)
	}
	if !valid {
		// R12:实例标识对不上(daemon 重装 / 换机 / 数据目录被清)。记录的游标指向
		// 另一条通知日志,拿它去拉会静默跳过整段转录,只能按已中断处理。
		return nil, 0, errSessionUnrecoverable
	}
	ss.mu.Lock()
	ss.loaded = true
	if seq > ss.cursor {
		ss.cursor = seq
	}
	pinned := ss.cursor
	ss.mu.Unlock()
	return ss, pinned, nil
}

// dropCursorAboveHighWater 拿接管交回的高水位校验游标。游标只可能来自 daemon 发过的
// seq,正常永远不会越过高水位;一旦越过,说明那台 daemon 的通知日志退了 —— agentred.db
// 被恢复 / 截断而 state.json(连同 TOFU 指纹)还在,R12 的指纹校验因此照常放行。
//
// 比对的是 pinned —— pinCursorFor 在接管之前钉住的那个快照,**不是**此刻的 ss.cursor:
// 接管一受理,daemon 就开始把实时帧推到这条连接上,读循环对每条顺序帧都推进 ss.cursor,
// 拿它去比等于把「日志正常地长出了下一条」判成「日志退了」。
//
// 不管的后果不是丢几条:此后每一条实时帧都满足 seq <= 游标,在 dispatchNotification
// 的第一条规则里被静默丢弃,会话没有跳号、没有错误、Debug 以上没有任何日志地冻住。
// 所以把游标归零、从头补齐,并留一条 Warn 让人看得见。
//
// 库里那份也要一并作废,否则下一次进程启动会把越界的老值原样读回来,同一个冻结重演
// 一遍。记(在 ss.mu 之下)+ 立刻 flush:防抖表里可能正压着一条更大的推进,只作废内存
// 那份会让它随后落库,把刚作废掉的值又写回去(见 recordCursor / flushCursors)。
func (r *Runtime) dropCursorAboveHighWater(ctx context.Context, sid int64, ss *sessionSync, pinned, highWater int64) {
	if pinned <= highWater {
		return
	}
	ss.mu.Lock()
	ss.cursor = 0
	r.recordCursor(sid, 0)
	ss.mu.Unlock()
	logger.Ctx(ctx).Warn("remote runtime: cursor beyond daemon high-water, restarting catch-up from scratch",
		zap.Int64("sid", sid), zap.Int64("cursor", pinned), zap.Int64("latestSeq", highWater))
	r.flushCursors()
}

// dropCursorBelowOldest 把落在「已经不存在的那一段」里的游标抬到现存最老的一行之前。
//
// agentred 不再回收通知日志(规格 2026-08-18 决策 8),但它的库可能被从外部恢复或截断,
// 老前缀因此仍可能消失。落后不止一格的客户端,它的游标正指向那段已经不存在的区间:
// 拉回来的每一页第一条都比 游标+1 大,在 dispatchNotification 的第二条规则里被判成跳号
// 丢弃并触发补洞拉取,而补洞原样拉回同一页 —— 游标永远推不动,此后连实时通知也全被当成
// 跳号,会话没有错误、没有跳号地冻住(与 dropCursorAboveHighWater 处理的越界冻结同类)。
//
// 那截尾巴是真的没有了,拿不回来。所以按 daemon 交回的下界复位,从现存最老的一行接着补,
// 并留一条 Warn 让「这段转录已经不在 daemon 那边了」在日志里看得见。
//
// 只在游标真的落在下界之下时动它:正常补齐里 oldest == 游标+1(甚至更小),一动就是把
// 已经交付过的通知再重放一遍。oldest 为 0 表示 daemon 没报(老 daemon / 读不出来),
// 一律不动。
func (r *Runtime) dropCursorBelowOldest(ctx context.Context, sid int64, ss *sessionSync, oldest int64) {
	if oldest <= 0 {
		return
	}
	ss.mu.Lock()
	if ss.cursor >= oldest-1 {
		ss.mu.Unlock()
		return
	}
	stale := ss.cursor
	ss.cursor = oldest - 1
	r.recordCursor(sid, oldest-1)
	ss.mu.Unlock()
	logger.Ctx(ctx).Warn("remote runtime: cursor below daemon's oldest surviving seq, that range was reclaimed",
		zap.Int64("sid", sid), zap.Int64("cursor", stale), zap.Int64("oldestSeq", oldest))
	r.flushCursors()
}

// ── 会话收尾 ────────────────────────────────────────────────────────────────

// failAllSessions 是今天的那条老路:给每条在飞会话注入 ErrDaemonDisconnected、
// close events,并拆掉自主续轮镜像。只在「没有重连资格」或「重连彻底失败」时走。
func (r *Runtime) failAllSessions() {
	r.mu.Lock()
	live := make([]*remoteSession, 0, len(r.sessions))
	liveSids := make([]int64, 0, len(r.sessions))
	for sid, sess := range r.sessions {
		live = append(live, sess)
		liveSids = append(liveSids, sid)
		delete(r.sessions, sid)
	}
	r.mu.Unlock()
	// 关键失败模式:daemon 进程崩 / 网络断 / 重连超上限,客户端单方面感知到通道死了,
	// 给在飞 session 注 ErrDaemonDisconnected 解锁前端「生成中」。同步落一条 Warn
	// 让运维事后能在日志里看到"哪几个 session 是被断连兜底关掉的"。
	if len(live) > 0 {
		// goroutine 无请求 ctx,按 observability.md 用 logger.Default()。
		logger.Default().Warn("remote runtime: giving up on daemon, injecting StopErr to live sessions",
			zap.Int("liveCount", len(live)),
			zap.Int64s("sids", liveSids))
	} else {
		logger.Default().Debug("remote runtime: daemon disconnected, no live sessions")
	}
	for i, sess := range live {
		closeSessionWithErr(sess, ErrDaemonDisconnected)
		r.setSessionConnState(liveSids[i], SessionConnState{State: ConnStateLost})
	}
	// 自主续轮镜像也随之拆掉:close 每个 out → chat_svc 的 watcher 退出;
	// 在飞的那轮 events 也 close 并带上同一个终止理由,driveAutonomousTurn 才能把它
	// 落成终态而不是「正常跑完」。见 autoturn.go。
	r.closeAllAutoSessions(ErrDaemonDisconnected)
	r.flushCursors()
}

// failSession 只收尾一条会话(它接不回去了,但连接是好的)。
//
// 终止理由是 ErrRunInterrupted 而不是 ErrDaemonDisconnected:走到这里意味着 daemon
// 重启后把它标成了中断态(R10)、它不在这台 daemon 上、或实例标识对不上导致游标失效
// (R12 判「按已中断处理」)—— 连接明明是通的,说「连不上了」是错的。R15 要求这两种
// 情形由消息文案区分,而文案的唯一依据就是这里交出去的哨兵。
func (r *Runtime) failSession(sid int64) {
	r.mu.Lock()
	sess := r.sessions[sid]
	delete(r.sessions, sid)
	auto := r.autoSessions[sid]
	delete(r.autoSessions, sid)
	// 这条会话在这台 daemon 上到此为止:摘掉补齐登记,否则此后每次重连都会为一条
	// 已经收尾的会话再 attach 一次,而每次都注定失败。
	delete(r.tracked, sid)
	r.mu.Unlock()
	if sess != nil {
		closeSessionWithErr(sess, ErrRunInterrupted)
	}
	if auto != nil {
		closeAutoSession(auto, ErrRunInterrupted)
	}
	r.setSessionConnState(sid, SessionConnState{State: ConnStateLost})
}

// closeSessionWithErr 收尾一条在飞会话:把终止理由写进 RunResult.StopErr 再 close
// events。cause 决定上层给出哪一句终态文案,已经有理由(终态帧到过)时不覆盖。
func closeSessionWithErr(sess *remoteSession, cause error) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	// generation ctx 与会话同生共死:即便这一轮已经收过尾,也要让还在飞的
	// 准备 / 启动 RPC 立刻返回,不留下没人接的远端 generation。
	if sess.cancel != nil {
		sess.cancel()
	}
	if sess.closed {
		return
	}
	sess.closed = true
	if sess.result != nil && sess.result.StopErr == nil {
		sess.result.StopErr = cause
	}
	close(sess.events)
}

// ── 连接态 ──────────────────────────────────────────────────────────────────

// enterReconnecting 把所有还活着的会话翻成重连态并播给观察者。
func (r *Runtime) enterReconnecting() {
	sids := r.liveSessionIDs()
	logger.Default().Warn("remote runtime: daemon connection dropped, reconnecting",
		zap.Int64s("sids", sids))
	r.flushCursors()
	for _, sid := range sids {
		r.setSessionConnState(sid, SessionConnState{State: ConnStateReconnecting})
	}
}

// setSessionConnState 播一次连接态变化。Runtime **不**自己留一份可查询的副本:
// 唯一的消费方 chat_svc 维护它自己的缓存,因为那份还要按「本进程里这条会话有没有在飞的
// turn」过滤(activeCancels),而 Runtime 这一侧看不到那个集合 —— 留第二份只会变成一个
// 谁都不该读、读了还会答错的真相源。
func (r *Runtime) setSessionConnState(sid int64, st SessionConnState) {
	if r.connObserver != nil {
		r.connObserver.OnSessionConnState(sid, st)
	}
}

// liveSessionIDs 本地还认得的会话:在飞的一轮 + 自主续轮镜像。
func (r *Runtime) liveSessionIDs() []int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessionIDsLocked(false)
}

// catchUpSessionIDs 补齐要覆盖的会话:liveSessionIDs 再并上「本进程没有轮次在跑、
// 但 App 启动时按 exec_device_id 找回来」的那些(见 CatchUpSessions)。
//
// 两者必须分开:liveSessionIDs 还负责决定连接态播给谁,而只有真的有活信号要修饰的
// 会话才该收到重连态。补齐的范围比它宽 —— 一条会话在 daemon 上跑着、在本进程里
// 一轮都没起,正是本轮要修的那个场景。
func (r *Runtime) catchUpSessionIDs() []int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessionIDsLocked(true)
}

// sessionIDsLocked 调用方必须持 r.mu(读或写)。
func (r *Runtime) sessionIDsLocked(withTracked bool) []int64 {
	seen := make(map[int64]struct{}, len(r.sessions)+len(r.autoSessions)+len(r.tracked))
	out := make([]int64, 0, len(seen))
	add := func(sid int64) {
		if _, dup := seen[sid]; dup {
			return
		}
		seen[sid] = struct{}{}
		out = append(out, sid)
	}
	for sid := range r.sessions {
		add(sid)
	}
	for sid := range r.autoSessions {
		add(sid)
	}
	if withTracked {
		for sid := range r.tracked {
			add(sid)
		}
	}
	return out
}

// ── 开轮定位 + 能力探测(R18)───────────────────────────────────────────────

// turnStartFloor 在开轮前读一眼这条会话此刻在 daemon 通知日志里的高水位,交给
// remoteSession.startSeq 当作**本轮**在 seq 时间线上的位置:凡是不比它新的通知,
// 都是上一轮(或更早)留下的。
//
// 为什么非问一次不可:一轮的开始是 runtime.run 这条 RPC,而它不在通知的 seq 时间线上
// —— RunAck 不带 seq,日志载荷里也没有轮次身份(seq 是日志行自己的列)。客户端因此没有
// 别的办法把「我刚起的这一轮」与「日志里还没读到的那些旧通知」排出先后,而补齐回放的
// 区间里恰恰可能整段夹着上一轮的终态帧(见 handleRunResultDone)。
//
// 顺带完成 R18 的能力探测:runtime.session.list 规格明写无副作用,是补齐族里唯一能拿来
// 探测与读高水位的入口(attach 会改推送目标、pull 会推游标)。探测放在开轮前而不是断连
// 时,是因为断连时连接已经死了,那时候探不出任何东西 —— 会话会被吊在退避重连里,而对
// 老 daemon 正确的行为是立刻按今天的语义收尾。
//
// 读不到就返 0(老 daemon / 这一次没拨通):守卫退化成今天的行为,绝不会因为读不到就把
// 一轮吊死 —— 高水位只可能偏小,永远不会把本轮自己的终态帧挡在外面。没装重连端口时连
// 调用都不发:那些调用方没有重连,凭空给每一轮加一次 RPC 只是纯开销。
//
// 一条会话在**同一代连接**上只问一次,之后由游标接着跟(见 floorOnConn):清单在 daemon
// 侧是一次 LatestSeqByPeer 的 GROUP BY 加上对该对端**每条**会话的 PendingWaiters 探测,
// 成本随这个对端历史上跑过的会话数增长,而这里唯一消费的只是这条会话的 LatestSeq。
// 探过之后游标追得上高水位:本连接是该会话的推送属主,daemon 新增的每一行都推给它,
// 推不动的那一刻这条连接就死了 —— 而换代重连(adoptConn)会把这份已知作废、重新探。
func (r *Runtime) turnStartFloor(ctx context.Context, sid int64) int64 {
	if r.reconnect == nil {
		return 0
	}
	r.connMu.Lock()
	st, gen := r.durability, r.connGen
	r.connMu.Unlock()
	if st == durabilityUnsupported {
		return 0
	}
	// 只取状态、不惰性读库:这里要的是「本连接上探过没有」,而游标该由谁读、什么时候读
	// 自有它的路径(第一条通知 / 补齐前的 pinCursorFor)。
	ss := r.stateFor(sid)
	if floor, known := ss.floorOnConn(gen); known {
		return floor
	}
	summaries, err := r.sessionSummaries(ctx)
	if err != nil {
		if errors.Is(err, ErrCatchUpUnsupported) {
			logger.Ctx(ctx).Warn("remote runtime: daemon predates session durability, disconnect will end the turn")
			return 0
		}
		// 网络抖动之类:留在 unknown,下一轮再探。一次拨不通不该把持久化关掉。
		logger.Ctx(ctx).Debug("remote runtime: session list before turn inconclusive", zap.Error(err))
		return 0
	}
	// 清单里没有这条会话:它在这台 daemon 上还一条通知都没发过,高水位就是 0。
	return ss.rememberFloor(gen, summaries[sid].LatestSeq)
}

// sessionSummaries 是补齐三步的第一步:问一眼这个对端在这台 daemon 上有哪些会话、
// 各自的生命周期状态、是否正在等输入、日志高水位到哪。规格明写它无副作用,所以它
// 同时充当 R18 的能力探测入口(attach 会改推送目标、pull 会推游标,都不能拿来探)。
//
// 老 daemon 回 method-not-found → ErrCatchUpUnsupported,并就地记下证伪结果。
func (r *Runtime) sessionSummaries(ctx context.Context) (map[int64]wire.SessionSummary, error) {
	var res wire.SessionListResult
	if err := r.conn().Call(ctx, wire.MethodSessionList, struct{}{}, &res); err != nil {
		if isMethodNotFound(err) {
			r.setDurability(durabilityUnsupported)
			return nil, ErrCatchUpUnsupported
		}
		return nil, wire.FromJSONRPCError(err)
	}
	r.setDurability(durabilitySupported)
	out := make(map[int64]wire.SessionSummary, len(res.Sessions))
	for _, s := range res.Sessions {
		// 账号级清单(R12)里两个对端各有一条**同号**会话是常态而非例外:会话 id 是各
		// 客户端本地自增的主键。这张表按裸 id 索引,所以同号时必须让**自己**那条(daemon
		// 在清单里把它的 origin 留空)胜出,别的对端那条不得覆盖它 —— R12 放宽的是可见性
		// 的过滤条件,「会话主键结构不变」,主键仍是 (对端指纹, 会话 id) 两段。
		//
		// 覆盖了会同时坏两处:turnStartFloor 把别人的高水位当成自己会话的下限,自己此后
		// 每一条通知都低于下限被判成重复丢弃(会话不报错地冻住);补齐三步则会带着别人的
		// origin 去 attach / pull,把别人的通知日志重放进自己的转录。
		if _, dup := out[s.SessionID]; dup && s.PeerFingerprint != "" {
			continue
		}
		out[s.SessionID] = s
		r.rememberOrigin(s.SessionID, s.PeerFingerprint)
	}
	return out, nil
}

// rememberOrigin 记下清单里学到的会话发起对端(R12 桌面侧)。下游的 attach / pull /
// pendingWaiters / 控制请求都要按它把 PeerFingerprint 原样带过去,daemon 据此解析到
// 发起对端;记到空值 = 未认领 daemon / 自己对端,请求省略该字段(向后兼容)。
func (r *Runtime) rememberOrigin(sid int64, fp string) {
	r.originMu.Lock()
	r.origins[sid] = fp
	r.originMu.Unlock()
}

// originFor 交出这条会话学到的发起对端;没学过(本地 Run 起的会话、清单还没含它)返
// 空串,调用方据此省略 PeerFingerprint —— 空 origin 在 daemon 侧解析为调用方自己对端,
// 正好是自己发的会话,天然向后兼容。
func (r *Runtime) originFor(sid int64) string {
	r.originMu.Lock()
	defer r.originMu.Unlock()
	return r.origins[sid]
}

// setDurability 记下能力探测的结论,并在结论**翻转**时播报给观察者(R18)。
//
// 只播翻转:探测落在每一轮开轮前的热路径上,结论不变时重复播报只会让上层一遍遍重写
// 同一条设备状态。unknown 不播 —— 它是「还没探」而不是结论(adoptConn 按连接代重置
// 它),把它当结论播会在每次重连时先把设备面板上那条说明撤下来又装回去。
//
// 播报在锁外:观察者是上层注入的,持着 connMu 回调它等于把 service 层的锁序拽进
// 这条连接的临界区。
func (r *Runtime) setDurability(st durabilityState) {
	r.connMu.Lock()
	prev := r.durability
	r.durability = st
	obs := r.durabilityObs
	r.connMu.Unlock()
	if obs == nil || st == durabilityUnknown || st == prev {
		return
	}
	obs.OnDaemonDurability(st == durabilitySupported)
}

// isMethodNotFound 判定 daemon 回的是 JSON-RPC 标准的 -32601。
func isMethodNotFound(err error) bool {
	var rpcErr *jsonrpc.Error
	return errors.As(err, &rpcErr) && rpcErr.Code == jsonrpc.ErrMethodNotFound.Code
}

// ── 游标落库(防抖)─────────────────────────────────────────────────────────

// recordCursor 记下待落库的游标。SaveCursor 一次一个事务且 bump updatetime,而这里是
// 每条通知都会走的热路径 —— 只记不写,真正落库交给 flushCursors 按 cursorFlush 合并。
//
// 纪律:**调用方必须持 ss.mu**。待落库值与内存游标必须在同一把锁下更新,否则「顺序帧
// 推进」与「守卫作废」各自记各自的,谁后落库由调度说了算 —— 库里那份就会与内存对不上
// (作废归零之后又被一条更早的推进覆盖回去,下次启动照着它把整段转录再重放一遍)。
//
// 也因此这里覆盖而不是取较大者:两条作废路径本来就是要把一个更小的值写下去,取较大者
// 会让它永远写不进库;定序由 ss.mu 给出 —— 最后记下的那个,就是最后写进内存游标的那个。
func (r *Runtime) recordCursor(sid, seq int64) {
	if r.cursor() == nil {
		return
	}
	r.cursorMu.Lock()
	if r.cursorDirty == nil {
		r.cursorDirty = map[int64]int64{}
	}
	r.cursorDirty[sid] = seq
	if r.cursorFlush > 0 && r.cursorTimer == nil {
		r.cursorTimer = time.AfterFunc(r.cursorFlush, r.flushCursors)
	}
	r.cursorMu.Unlock()
}

// flushCursors 把攒下的游标一次性落库。断连、放弃、Close、游标作废时都会主动调一次。
//
// 取走脏表与真正落库合在 cursorFlushMu 之下:两次并发 flush 各自取走一份快照后,落库
// 先后由调度决定,后写的那次可能拿的是更早取走的那份 —— 库里于是停在一个已经被覆盖掉
// 的值上。串起来之后「最后一次落库写的就是最后记下的那个值」才成立。
//
// 写库在 cursorMu 之外:它是一次事务,压在攒批锁里会把持 ss.mu 的通知热路径按在库上。
func (r *Runtime) flushCursors() {
	r.cursorFlushMu.Lock()
	defer r.cursorFlushMu.Unlock()
	r.cursorMu.Lock()
	if r.cursorTimer != nil {
		r.cursorTimer.Stop()
		r.cursorTimer = nil
	}
	dirty := r.cursorDirty
	r.cursorDirty = nil
	r.cursorMu.Unlock()
	for sid, seq := range dirty {
		r.saveCursor(sid, seq)
	}
}

func (r *Runtime) saveCursor(sid, seq int64) {
	port := r.cursor()
	if port == nil {
		return
	}
	ctx := context.Background()
	if err := port.SaveCursor(ctx, sid, r.fingerprint(), seq); err != nil {
		logger.Default().Warn("remote runtime: save session cursor failed",
			zap.Int64("sid", sid), zap.Int64("seq", seq), zap.Error(err))
	}
}

// cursor 取生效的游标端口:构造时注入的优先,否则取进程级注册的那个。
func (r *Runtime) cursor() agentruntime.SessionCursorPort {
	if r.cursorPort != nil {
		return r.cursorPort
	}
	return agentruntime.SessionCursor()
}

// fingerprint 当前连上的 daemon 实例标识。
func (r *Runtime) fingerprint() string {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	return r.daemonFP
}

// conn 当前这条连接。重连会换掉它,所以每次用都要重新取,不能捕获。
func (r *Runtime) conn() agentruntime.DaemonClientPort {
	r.connMu.Lock()
	defer r.connMu.Unlock()
	return r.client
}
