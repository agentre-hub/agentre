// Package remote 是 agentre 桌面端连远端 agentred daemon 的 agent runtime
// 客户端。daemon 端跑真正的 claudecode / codex / builtin runtime,本包通过
// WebSocket + JSON-RPC(runtime.* 命名空间)把整个 agentruntime.Runtime
// 接口 + 7 个可选子接口透明代理过去:
//
//   - Run / Steer / CancelSteer / DrainPending / Abort / SetPermissionMode /
//     SubmitAnswer / SubmitToolPermission → existing runtime.* RPC forms
//   - Pi PrepareRun → phased runtime.run registration / preparation / Start
//   - daemon → client 反向 push 用两条 notification:
//     runtime.event(每个 sealed Event 一条)+ runtime.runResultDone(终态)
//
// chat_svc 拿到 *Runtime 后只用接口方法,看不到本地 / 远端区别。
//
// 协议层 sentinel 错误(ErrNoActiveTurn / ErrSteerNotFound / ErrUnsupported /
// ErrAborted)通过 wire.FromJSONRPCError 反向 rehydrate,让 errors.Is 跨进程
// 继续工作。
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/capability"
	piagentrt "github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// remoteSession 一个远端 daemon 上跑的 chat session 在本地的镜像。sessionID
// 是 client/daemon 共享的 int64(daemon 侧不再分配额外的 string sid),所以一个 map 就够。
type remoteSession struct {
	id          int64
	backendType agent_backend_entity.BackendType
	events      chan agentruntime.Event
	result      *agentruntime.RunResult
	ctx         context.Context
	cancel      context.CancelFunc
	// startSeq 是**开轮那一刻** daemon 通知日志里这条会话的高水位:本轮自己的通知
	// 必然都比它新。它是这一轮在 seq 时间线上的位置 —— 而 runtime.run 这条 RPC 本身
	// 不在那条时间线上(RunAck 不带 seq,日志载荷里也没有轮次身份),少了它就分不出
	// 补齐回放上来的一条终态帧到底是谁的。见 handleRunResultDone 与 turnStartFloor。
	//
	// 0 表示未知(没装重连端口 / 老 daemon / 这一次没读到),此时守卫退化成今天的行为。
	// 发布进 r.sessions 之前赋值,之后不再改写;读方一律在 r.mu 下。
	startSeq int64

	mu                sync.Mutex
	providerSessionID string
	started           bool
	closed            bool
	registrationDone  chan struct{}
	registrationOnce  sync.Once
	abortOnce         sync.Once
	abortErr          error
	abortOutcome      agentruntime.AbortOutcome
}

// Runtime 包装 DaemonClientPort 把 chat session 委托给远端 daemon。生命周期:
//   - New(client) 立即向 client 注册两条 server-push handler
//   - Run() 调 runtime.run 注册 session,后续 runtime.event / runtime.runResultDone
//     按 sessionID 路由；Pi PrepareRun 复用同一方法分三阶段完成
//   - Prefetch(ctx, backendType) 主动拉一次 daemon 的 capability 矩阵缓存到本地,
//     之后 Capabilities() 同步返(chat_svc UI gating 依赖它是同步的)
type Runtime struct {
	client agentruntime.DaemonClientPort

	mu              sync.RWMutex
	sessions        map[int64]*remoteSession
	generationGates map[int64]*generationGate
	caps            map[agent_backend_entity.BackendType]capability.Capabilities
	// autoSessions 是「自主续轮」(AutonomousTurnSource)的会话级镜像,**独立于**
	// per-Run 的 sessions(后者在 runResultDone 时删除,而自主续轮发生在 Run 收尾
	// *之后*)。按 sessionID 持久(跨 turn / 子进程 evict 复用),conn close 时统一拆。
	// 见 autoturn.go。
	autoSessions    map[int64]*autoSession
	piGenerationSeq atomic.Uint64
	// tracked 是 App 启动后按 exec_device_id 找回来、要为之补齐的会话(见 catchup.go)。
	// 它们在本进程内**没有**在飞的一轮 —— 那正是重启后的常态 —— 但断连后仍要重连补齐,
	// 所以必须与 sessions / autoSessions 一起进补齐范围。受 mu 保护。
	tracked map[int64]struct{}

	// ── 断连重连(reconnect.go)──
	reconnect     ReconnectPort
	connObserver  ConnStateObserver
	durabilityObs DurabilityObserver
	cursorPort    agentruntime.SessionCursorPort
	backoff       []time.Duration
	cursorFlush   time.Duration

	// connMu 只保护「当前这条连接」相关的三个字段:client / daemonFP / 能力探测
	// 结果。它与 mu 分开,是因为重连期间要在不持有会话表锁的前提下换连接。
	connMu     sync.Mutex
	daemonFP   string
	durability durabilityState
	// connGen 是「第几条连接」的代号,adoptConn 每换一条连接 +1。开轮位置的探测结果
	// 按它作废(见 turnStartFloor):同一条连接上探一次就够,换了连接必须重探。
	connGen int64

	// sessionState 是每条会话的补齐状态(游标 + 补洞串行化)。
	// 与 sessions 分开:sessions 只活在一轮之内,游标要跨轮存活。
	//
	// 条目**故意不回收**,failSession / failAllSessions 都不删。游标是这里唯一的热副本,
	// 而落库是防抖的(见 recordCursor):failSession 只收尾一条会话、并不 flushCursors
	// (failAllSessions 才会),此刻删掉条目就把最后 cursorFlush 窗口里那截游标推进丢了
	// —— 同一条会话下一轮起来时按库里那个旧游标补洞,已经交付过的通知会被再重放一遍,
	// 直接破「无重复」这条硬不变量。空间是有界的:一个 *Runtime 按设备缓存,它的条目数
	// 至多等于这台 daemon 上被本进程碰过的远端会话数,而最后一条会话引用释放时整个
	// Runtime 连同这张表一起被丢掉(见 chat_svc.releaseRemoteRuntime)。
	stateMu      sync.Mutex
	sessionState map[int64]*sessionSync

	// 游标落库的防抖攒批,见 recordCursor。cursorFlushMu 把「取走脏表 + 落库」串起来,
	// 让并发的两次 flush 不会把更早的那份快照后写进库(见 flushCursors)。
	cursorMu      sync.Mutex
	cursorDirty   map[int64]int64
	cursorTimer   *time.Timer
	cursorFlushMu sync.Mutex

	// originMu / origins 记录每条会话的发起对端 —— sessionSummaries 从清单里学到的
	// SessionSummary.PeerFingerprint(R12 桌面侧)。已认领 daemon 上同账号客户端要操作
	// 别的对端发起的会话,attach / pull / pendingWaiters / 控制请求必须把 origin 原样
	// 带过去;空 = 自己对端(未认领 daemon 恒空),省略该字段即向后兼容。
	originMu sync.Mutex
	origins  map[int64]string

	stopOnce sync.Once
	stopped  chan struct{}
}

type generationGate struct {
	mu   sync.Mutex
	refs int
}

// notifyHandler 是一条 daemon → client 通知的处理函数。
type notifyHandler func(*Runtime, context.Context, json.RawMessage) (any, error)

// New 构造一个 remote.Runtime,并把 runtime.event / runtime.runResultDone
// 两个 server-push handler 注册到 client。调用方负责管理 client 的生命周期(通常
// 是 Pool.Lease)。
//
// 额外起一个 goroutine 监 client.Closed():daemon 进程崩溃 / 网络断 / TLS 失
// 败等情况下,在飞的 run session 永远等不到 runResultDone,events channel 不
// 关 → chat_svc.runTurn 卡在 `for ev := range events`,前端会话一直停在「生
// 成中」。
//
// 装了 WithReconnect 时,断连**不**终结会话:会话转入重连态,退避重连并按游标
// 补齐(见 reconnect.go)。没装重连端口、或对面 daemon 不认补齐族 RPC 时,才
// 回落到给所有 live session 注入 ErrDaemonDisconnected 并 close events,
// chat_svc 走 StreamError 解锁前端。
func New(c agentruntime.DaemonClientPort, opts ...Option) *Runtime {
	r := &Runtime{
		client:          c,
		sessions:        map[int64]*remoteSession{},
		generationGates: map[int64]*generationGate{},
		caps:            map[agent_backend_entity.BackendType]capability.Capabilities{},
		autoSessions:    map[int64]*autoSession{},
		sessionState:    map[int64]*sessionSync{},
		origins:         map[int64]string{},
		connGen:         1, // 0 留给 sessionSync 的零值:那表示「这条会话还没探过」
		backoff:         defaultReconnectBackoff,
		cursorFlush:     defaultCursorFlushInterval,
		stopped:         make(chan struct{}),
	}
	for _, o := range opts {
		o(r)
	}
	r.registerHandlers(c)
	if closed := c.Closed(); closed != nil {
		go r.watchClose(closed)
	}
	return r
}

// registerHandlers 把五类通知 + MCP 反向隧道挂到一条连接上。重连换连接后要原样再挂
// 一遍 —— 新连接自带一张空的 handler 表。
//
// 五类通知统一经 dispatchNotification 入口,补齐重放走的也是它:实时与补齐因此共用
// 同一套 handler,R5 的等价性是结构性成立的,不只是被测试覆盖。
func (r *Runtime) registerHandlers(c agentruntime.DaemonClientPort) {
	for method, h := range notifyHandlers {
		m, fn := method, h
		c.Handle(m, func(ctx context.Context, raw json.RawMessage) (any, error) {
			return r.dispatchNotification(ctx, m, fn, raw)
		})
	}
	// daemon 上的 CLI 子进程访问内置工具 MCP(org/subagent/group/workflow)时,经此反向
	// 请求隧道回 desktop 执行(真 /mcp/* handler 在 desktop)。见 mcpproxy.go。
	c.Handle(wire.MethodMCPProxy, r.handleMCPProxy)
}

// notifyHandlers 是 daemon → client 五类通知的方法名 → handler 映射。补齐重放按
// method 找回同一个 handler,所以这张表必须是**唯一**的注册来源。
var notifyHandlers = map[string]notifyHandler{
	wire.NotifyEvent:                 (*Runtime).handleEvent,
	wire.NotifyRunResultDone:         (*Runtime).handleRunResultDone,
	wire.NotifyAutonomousTurnStarted: (*Runtime).handleAutonomousTurnStarted,
	wire.NotifyAutonomousTurnEvent:   (*Runtime).handleAutonomousTurnEvent,
	wire.NotifyAutonomousTurnDone:    (*Runtime).handleAutonomousTurnDone,
}

// ErrDaemonDisconnected 当远端 daemon 连接断开(进程崩 / 网络断 / 主动 Close)
// 时,remote.Runtime 注入到在飞 session 的 StopErr。chat_svc 拿到后映射为
// StreamError,前端就能解锁「生成中」并显示一条提示。
var ErrDaemonDisconnected = errors.New("agentruntime/runtimes/remote: daemon connection closed")

// errEventBufferOverflow 是 bounded event buffer 溢出时注入到 StopErr 的脱敏
// 错误:消息固定,不含任何 prompt / event payload。notify handler(read loop
// 直接调用)做 non-blocking send,消费方跟不上时取消精确 generation 而不是
// 阻塞 WebSocket 读循环。
var errEventBufferOverflow = errors.New("remote runtime: event delivery exceeded bounded buffer")

// eventChannelBound 是每个远端 generation 的有界事件缓冲。read loop 上的
// handleEvent 用 non-blocking send,缓冲满即取消精确 generation。该值兼顾
// 短突发(避免误取消正常 turn)和溢出保护(消费方卡死时尽快释放资源)。
const eventChannelBound = 128

const generationControlTimeout = 5 * time.Second

// ErrRunInterrupted 这一轮在远端**被打断**了:daemon 重启后按 R10 把非终态会话标成
// 中断态(接管回 ErrNoActiveTurn / ErrSessionNotFound),或那台 daemon 的实例标识对不上
// 导致游标失效(R12 判「按已中断处理」)。连接本身是好的,只是这一轮接不回去了。
//
// 它必须与 ErrDaemonDisconnected 分开:R15 规定中断沿用既有的 error 态、**由消息文案
// 区分其与真实错误**,而上层能拿到的唯一依据就是 StopErr。折成同一个哨兵,「被打断」
// 与「连不上了」就是同一句话,用户分不出发生了什么。
var ErrRunInterrupted = errors.New("agentruntime/runtimes/remote: run interrupted by daemon restart")

// Close 关掉与 daemon 的 client 连接,并停掉重连状态机。
func (r *Runtime) Close() error {
	r.stopOnce.Do(func() { close(r.stopped) })
	r.flushCursors()
	c := r.conn()
	if c == nil {
		return nil
	}
	return c.Close()
}

// ── Capabilities ───────────────────────────────────────────────────────────

// Prefetch 主动拉一次 daemon 端 backendType 对应 runtime 的 capability 矩阵
// 并缓存,后续 Capabilities() 同步返。chat_svc.borrowRemoteRuntime 在 Pool
// borrow 完成后调一次,避免 turn 启动时再走异步 RPC。
//
// 已缓存的 backendType 重复调直接 noop。
func (r *Runtime) Prefetch(ctx context.Context, bt agent_backend_entity.BackendType) error {
	r.mu.RLock()
	_, ok := r.caps[bt]
	r.mu.RUnlock()
	if ok {
		return nil
	}
	var res wire.CapabilitiesResult
	if err := r.conn().Call(ctx, wire.MethodCapabilities, wire.CapabilitiesParams{
		BackendType: string(bt),
	}, &res); err != nil {
		return wire.FromJSONRPCError(err)
	}
	r.mu.Lock()
	r.caps[bt] = res.Capabilities
	r.mu.Unlock()
	return nil
}

// Capabilities 返回最近一次 Prefetch 的结果(任意 backendType 第一个命中的);
// 没 Prefetch 时返默认占位矩阵让 UI gating 不挂死。
//
// 一台远端 daemon 通常只跑一种 backend type(claudecode 或 codex),所以单值
// 返回足够;真要同 device 多 backend,chat_svc 拿到 runtime 后立即 Prefetch
// 当前 turn 的 backendType,再调 Capabilities() 命中刚写的 cache。
func (r *Runtime) Capabilities() capability.Capabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, c := range r.caps {
		return c
	}
	return defaultCapsBeforePrefetch
}

// defaultCapsBeforePrefetch 占位矩阵 —— Prefetch 之前 UI 才不会一片灰。
// claudecode 是 daemon 最常见的 backend,所以默认对齐它已知能力子集。
var defaultCapsBeforePrefetch = capability.Capabilities{
	Set: map[capability.Capability]bool{
		capability.CapSteer:              true,
		capability.CapAbort:              true,
		capability.CapStopBackgroundTask: true,
		capability.CapAnswerUserAsk:      true,
		capability.CapToolPermission:     true,
		capability.CapSkills:             true,
	},
	PermissionModeMeta: capability.PermissionModeMeta{
		AllowedModes:         []string{"default", "acceptEdits", "plan", "bypassPermissions"},
		DefaultMode:          "acceptEdits",
		Order:                []string{"default", "acceptEdits", "plan", "bypassPermissions"},
		SwitchableDuringTurn: false,
	},
}

// ── Run ─────────────────────────────────────────────────────────────────────

// Run 在远端 daemon 上启动一轮 chat session;本地返回 sealed Event 流 +
// 一个会异步被填充的 *RunResult。channel close 之后调用方才能读 RunResult,
// 这一契约由 daemon 的 runtime.runResultDone 通知保证:终态帧到达时先填
// result,再 close channel。
func (r *Runtime) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	if req.Backend != nil && req.Backend.IsPiAgent() {
		prepared, err := r.PrepareRun(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		return prepared.Start(ctx)
	}
	return r.runDirect(ctx, req)
}

// PrepareRun mirrors Pi's existing local pre-prompt boundary without adding a
// wire method or field. The first runtime.run request prepares/forks on
// agentred and returns RunAck.ProviderSessionID. Start sends the same request a
// second time with that provider identity, which agentred uses to start the
// exact prepared generation only after chat_svc has durably activated it.
func (r *Runtime) PrepareRun(ctx context.Context, req agentruntime.RunRequest) (piagentrt.PreparedRun, error) {
	if req.Backend == nil || !req.Backend.IsPiAgent() {
		return nil, errors.New("remote runtime: prepared runs are only supported for Pi Agent")
	}
	params, err := buildRunParams(req)
	if err != nil {
		return nil, err
	}
	// Pi does not consume PermissionMode. Reuse that existing request field as
	// an opaque per-runtime generation owner, and strip it on agentred before
	// constructing the real Pi RunRequest. This avoids a new wire field while
	// letting delayed preparation/start requests fail closed against a retry.
	params.PermissionMode = "remote-pi-generation-" + strconv.FormatUint(r.piGenerationSeq.Add(1), 10)
	generationCtx, cancel := context.WithCancel(ctx)
	sess := &remoteSession{
		id:               req.SessionID,
		backendType:      agent_backend_entity.TypePiAgent,
		events:           make(chan agentruntime.Event, eventChannelBound),
		result:           &agentruntime.RunResult{},
		ctx:              generationCtx,
		cancel:           cancel,
		registrationDone: make(chan struct{}),
	}
	releaseGenerationGate := r.acquireGenerationGate(req.SessionID)
	r.mu.Lock()
	if _, exists := r.sessions[req.SessionID]; exists {
		r.mu.Unlock()
		releaseGenerationGate()
		cancel()
		return nil, errors.New("remote runtime: session already has an active generation")
	}
	r.sessions[req.SessionID] = sess
	r.mu.Unlock()

	registrationCtx, stopRegistration := context.WithTimeout(context.WithoutCancel(ctx), generationControlTimeout)
	var registrationAck wire.RunAck
	registrationErr := r.conn().Call(registrationCtx, wire.MethodRun, params, &registrationAck)
	stopRegistration()
	sess.registrationOnce.Do(func() { close(sess.registrationDone) })
	releaseGenerationGate()
	if registrationErr != nil {
		_, _ = r.abortGeneration(ctx, sess, 0)
		return nil, wire.FromJSONRPCError(registrationErr)
	}
	if registrationAck.SessionID != req.SessionID {
		_, _ = r.abortGeneration(ctx, sess, 0)
		return nil, fmt.Errorf("remote runtime: Pi registration returned session %d for %d", registrationAck.SessionID, req.SessionID)
	}
	if err := generationCtx.Err(); err != nil {
		_, _ = r.abortGeneration(ctx, sess, 0)
		return nil, err
	}

	var ack wire.RunAck
	if err := r.conn().Call(generationCtx, wire.MethodRun, params, &ack); err != nil {
		_, _ = r.abortGeneration(ctx, sess, 0)
		logger.Ctx(ctx).Warn("remote runtime: Pi preparation RPC failed",
			zap.Int64("sessionId", req.SessionID),
			zap.String("errorType", fmt.Sprintf("%T", err)))
		return nil, wire.FromJSONRPCError(err)
	}
	if ack.SessionID != req.SessionID {
		_, _ = r.abortGeneration(ctx, sess, 0)
		return nil, fmt.Errorf("remote runtime: Pi preparation returned session %d for %d", ack.SessionID, req.SessionID)
	}
	providerSessionID := strings.TrimSpace(ack.ProviderSessionID)
	if providerSessionID == "" {
		_, _ = r.abortGeneration(ctx, sess, 0)
		return nil, errors.New("remote runtime: Pi preparation returned empty provider session id")
	}
	sess.mu.Lock()
	sess.providerSessionID = providerSessionID
	sess.result.ProviderSessionID = providerSessionID
	sess.result.LaunchPermissionMode = ack.LaunchPermissionMode
	sess.mu.Unlock()
	return &remotePreparedRun{runtime: r, session: sess, params: params}, nil
}

type remotePreparedRun struct {
	runtime *Runtime
	session *remoteSession
	params  wire.RunParams

	mu      sync.Mutex
	started bool
	closed  bool
}

func (p *remotePreparedRun) ProviderSessionID() string {
	if p == nil || p.session == nil {
		return ""
	}
	p.session.mu.Lock()
	defer p.session.mu.Unlock()
	return p.session.providerSessionID
}

func (p *remotePreparedRun) Start(ctx context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	if p == nil || p.runtime == nil || p.session == nil {
		return nil, nil, errors.New("remote runtime: nil prepared Pi generation")
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, nil, errors.New("remote runtime: prepared Pi generation closed")
	}
	if p.started {
		p.mu.Unlock()
		return nil, nil, errors.New("remote runtime: prepared Pi generation already started")
	}
	p.started = true
	p.mu.Unlock()

	p.params.ProviderSessionID = p.ProviderSessionID()
	p.session.mu.Lock()
	p.session.started = true
	p.session.mu.Unlock()
	startCtx, cancelStart := context.WithCancel(p.session.ctx)
	stopCallerCancel := context.AfterFunc(ctx, cancelStart)
	defer func() {
		stopCallerCancel()
		cancelStart()
	}()
	var ack wire.RunAck
	if err := p.runtime.conn().Call(startCtx, wire.MethodRun, p.params, &ack); err != nil {
		_, _ = p.runtime.abortGeneration(ctx, p.session, 0)
		return nil, nil, wire.FromJSONRPCError(err)
	}
	if ack.SessionID != p.session.id || strings.TrimSpace(ack.ProviderSessionID) != p.ProviderSessionID() {
		_, _ = p.runtime.abortGeneration(ctx, p.session, 0)
		return nil, nil, errors.New("remote runtime: Pi start acknowledged a different prepared generation")
	}
	p.session.mu.Lock()
	p.session.result.LaunchPermissionMode = ack.LaunchPermissionMode
	p.session.result.ProviderFallbackKey = ack.ProviderFallbackKey
	p.session.mu.Unlock()
	logger.Ctx(ctx).Info("remote runtime: Pi generation started",
		zap.Int64("sessionId", ack.SessionID),
		zap.String("providerSessionId", ack.ProviderSessionID))
	return p.session.events, p.session.result, nil
}

func (p *remotePreparedRun) Close(ctx context.Context) error {
	if p == nil || p.runtime == nil || p.session == nil {
		return nil
	}
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	_, err := p.runtime.abortGeneration(ctx, p.session, 0)
	return err
}

func (r *Runtime) runDirect(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	params, err := buildRunParams(req)
	if err != nil {
		return nil, nil, err
	}
	generationCtx, cancel := context.WithCancel(ctx)
	// 开轮前读一眼日志高水位(顺带完成 R18 的能力探测),把这一轮钉在 seq 时间线上:
	// 不比它新的终态帧都属于已经结束的轮次。见 turnStartFloor。
	floor := r.turnStartFloor(ctx, req.SessionID)
	sess := &remoteSession{
		id:          req.SessionID,
		backendType: agent_backend_entity.BackendType(req.Backend.Type),
		events:      make(chan agentruntime.Event, eventChannelBound),
		result:      &agentruntime.RunResult{},
		ctx:         generationCtx,
		cancel:      cancel,
		started:     true,
		startSeq:    floor,
	}
	r.mu.Lock()
	r.sessions[req.SessionID] = sess
	r.mu.Unlock()

	var ack wire.RunAck
	if err := r.conn().Call(generationCtx, wire.MethodRun, params, &ack); err != nil {
		r.finishSession(sess, nil)
		fields := make([]zap.Field, 0, 3)
		fields = append(fields, zap.Int64("requestedSid", req.SessionID))
		fields = append(fields, remoteErrorLogFields(err)...)
		logger.Ctx(ctx).Error("remote.Run: RPC failed", fields...)
		return nil, nil, wire.FromJSONRPCError(err)
	}

	sess.mu.Lock()
	sess.id = ack.SessionID
	sess.providerSessionID = strings.TrimSpace(ack.ProviderSessionID)
	sess.result.ProviderSessionID = ack.ProviderSessionID
	sess.result.LaunchPermissionMode = ack.LaunchPermissionMode
	sess.result.ProviderFallbackKey = ack.ProviderFallbackKey
	sess.mu.Unlock()
	if ack.SessionID != req.SessionID {
		r.mu.Lock()
		if r.sessions[req.SessionID] == sess {
			delete(r.sessions, req.SessionID)
			r.sessions[ack.SessionID] = sess
		}
		r.mu.Unlock()
	}
	logger.Ctx(ctx).Info("remote runtime: session started",
		zap.Int64("sid", ack.SessionID),
		zap.String("backend", req.Backend.Type))
	return sess.events, sess.result, nil
}

// buildRunParams 序列化 agentruntime.RunRequest 成 wire.RunParams。Backend
// 走 json.RawMessage 透传(避免 wire 硬依赖 entity 内部结构),History 通过
// blocks.EncodeAll 转成 StoredBlock 形式。
//
// 故意不发 req.Provider / GatewayURL / GatewayToken —— 见 wire.RunParams 注释:
// daemon 端在 handlers/runtime.go 里自家 ProviderLookup + 自家 Gateway 解出来,
// desktop 那份是本机 127.0.0.1 + 含 APIKey 的明文,跨进程发过去既不可达也不安全。
func buildRunParams(req agentruntime.RunRequest) (wire.RunParams, error) {
	backendJSON, err := json.Marshal(req.Backend)
	if err != nil {
		return wire.RunParams{}, fmt.Errorf("marshal backend: %w", err)
	}
	history, err := encodeHistory(req.History)
	if err != nil {
		return wire.RunParams{}, err
	}
	userBlocks, err := blocks.EncodeAll(req.UserBlocks)
	if err != nil {
		return wire.RunParams{}, fmt.Errorf("encode user blocks: %w", err)
	}
	return wire.RunParams{
		Backend:           backendJSON,
		AgentID:           req.AgentID,
		SessionID:         req.SessionID,
		Cwd:               req.Cwd,
		Title:             req.Title,
		AgentSyncID:       req.AgentSyncID,
		SystemPrompt:      req.SystemPrompt,
		ProviderSessionID: req.ProviderSessionID,
		FreshSession:      req.FreshSession,
		UserText:          req.UserText,
		UserBlocks:        userBlocks,
		History:           history,
		Compact:           req.Compact,
		ForkAnchor:        req.ForkAnchor,
		PermissionMode:    req.PermissionMode,
		CollaborationMode: req.CollaborationMode,
		MCPServers:        req.MCPServers,
		EnabledPlugins:    req.EnabledPlugins,
		LLMProviderKey:    req.LLMProviderKey,
		LLMModelKey:       remoteModelKey(req),
	}, nil
}

// remoteModelKey 返回本轮远端执行目标的稳定 ModelKey（决策 11）：直接透传执行侧
// 解析结果（EffectiveLLMConfig v1 seam）的 ModelKey，绝不自行派生 backend 主绑定的
// 固定模型。
//
// 会话是否钉住 provider 只有桌面端知道：chat_svc 的 remoteKeysOnlyEffective 已把
// 「会话钉了 provider → 用会话 ModelKey（空 = provider-default）；未钉 → 用 backend
// 固定模型」这一口径解析好。空 ModelKey 是**有意义的 provider-default**，不是「未
// 提供」——若在这里回落到 backend 固定模型，会话钉 provider-default 就会被 backend
// 固定模型带偏（spec 决策 1），与本地路径 sessionModelKeyFor 的语义分叉。仅当执行侧
// 结果完全缺失（防御）时才回落 backend 绑定。
func remoteModelKey(req agentruntime.RunRequest) string {
	if req.Effective != nil {
		return strings.TrimSpace(req.Effective.ModelKey)
	}
	if req.Backend != nil {
		return strings.TrimSpace(req.Backend.LLMModelKey)
	}
	return ""
}

func encodeHistory(in []agentruntime.HistoryMessage) ([]wire.HistoryMessageWire, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]wire.HistoryMessageWire, 0, len(in))
	for _, m := range in {
		sbs, err := blocks.EncodeAll(m.Blocks)
		if err != nil {
			return nil, fmt.Errorf("encode history blocks: %w", err)
		}
		out = append(out, wire.HistoryMessageWire{Role: m.Role, Blocks: sbs})
	}
	return out, nil
}

func usageFromWire(u *wire.UsageWire) *provider.Usage {
	if u == nil {
		return nil
	}
	return &provider.Usage{
		PromptTokens:        u.PromptTokens,
		CompletionTokens:    u.CompletionTokens,
		ReasoningTokens:     u.ReasoningTokens,
		CachedTokens:        u.CachedTokens,
		CacheCreationTokens: u.CacheCreationTokens,
		TotalTokens:         u.TotalTokens,
	}
}

// ── server-push handlers ───────────────────────────────────────────────────

func remoteEventLogFields(raw json.RawMessage) []zap.Field {
	fields := []zap.Field{zap.Int("eventBytes", len(raw))}
	var head struct {
		Kind agentruntime.EventKind `json:"kind"`
	}
	if err := json.Unmarshal(raw, &head); err == nil {
		if eventKind, ok := safeRemoteEventKind(head.Kind); ok {
			fields = append(fields, zap.String("eventKind", eventKind))
		}
	}
	return fields
}

func safeRemoteEventKind(kind agentruntime.EventKind) (string, bool) {
	switch kind {
	case agentruntime.EventTextDelta,
		agentruntime.EventThinkingDelta,
		agentruntime.EventToolUseStart,
		agentruntime.EventToolUseEnd,
		agentruntime.EventToolResult,
		agentruntime.EventSteerConsumed,
		agentruntime.EventSubagentStarted,
		agentruntime.EventSubagentProgress,
		agentruntime.EventSubagentDone,
		agentruntime.EventSubagentModel,
		agentruntime.EventAskUserQuestion,
		agentruntime.EventAskUserQuestionAnswered,
		agentruntime.EventPlanUpdated,
		agentruntime.EventToolPermissionRequest,
		agentruntime.EventToolPermissionResolved,
		agentruntime.EventPermissionModeChanged,
		agentruntime.EventRetry,
		agentruntime.EventUsage,
		agentruntime.EventCompactBoundary,
		agentruntime.EventRuntimeStatus,
		agentruntime.EventContextWindowUpdated,
		agentruntime.EventError,
		agentruntime.EventDone:
		return string(kind), true
	default:
		return "", false
	}
}

func remoteErrorLogFields(err error) []zap.Field {
	if err == nil {
		return nil
	}
	return []zap.Field{
		zap.String("errorClass", fmt.Sprintf("%T", err)),
		zap.Int("errorBytes", len(err.Error())),
	}
}

func (r *Runtime) handleEvent(ctx context.Context, raw json.RawMessage) (any, error) {
	var frame wire.EventFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		fields := make([]zap.Field, 0, 3)
		fields = append(fields, zap.Int("rawBytes", len(raw)))
		fields = append(fields, remoteErrorLogFields(err)...)
		logger.Ctx(ctx).Warn("remote.handleEvent: event frame unmarshal failed", fields...)
		return nil, nil
	}
	r.mu.RLock()
	sess := r.sessions[frame.SessionID]
	knownSids := make([]int64, 0, len(r.sessions))
	for k := range r.sessions {
		knownSids = append(knownSids, k)
	}
	r.mu.RUnlock()
	if sess == nil && frame.Seq <= 0 {
		// Legacy/live frames without a replay sequence cannot belong to a catch-up
		// turn. Drop them before decoding, while logging only bounded metadata from
		// the untrusted payload.
		fields := make([]zap.Field, 0, 4)
		fields = append(fields,
			zap.Int64("frameSid", frame.SessionID),
			zap.Int64s("knownSids", knownSids),
		)
		fields = append(fields, remoteEventLogFields(frame.Event)...)
		logger.Ctx(ctx).Warn("remote.handleEvent: event for unknown session dropped", fields...)
		return nil, nil
	}
	ev, err := agentruntime.UnmarshalEvent(frame.Event)
	if err != nil {
		fields := make([]zap.Field, 0, 5)
		fields = append(fields, zap.Int64("sid", frame.SessionID))
		fields = append(fields, remoteEventLogFields(frame.Event)...)
		fields = append(fields, remoteErrorLogFields(err)...)
		logger.Ctx(ctx).Warn("remote.handleEvent: event decode failed", fields...)
		return nil, nil
	}
	if sess != nil && frame.Seq > 0 && frame.Seq <= sess.startSeq {
		// 补齐回放上来的、属于**已结束轮次**的事件:它在开轮之前就落库了。放进当前
		// 这一轮的 events,上一轮的 TextDelta 会一字不差地追加到用户刚发出的那条消息
		// 的回答里 —— 用户看到的是一段答非所问的历史。它归补齐轮。
		logger.Ctx(ctx).Debug("remote runtime: event from an ended turn — routed to catch-up",
			zap.Int64("sid", frame.SessionID), zap.Int64("seq", frame.Seq),
			zap.Int64("turnStartSeq", sess.startSeq))
		sess = nil
	}
	if sess == nil {
		// 带 seq 的事件必定来自认得补齐族的 daemon:它要么是重放上来的历史,要么是
		// 一条本进程还没有轮次去接的实时通知(App 刚重启)。两种都是用户没见过的
		// 转录内容,交给补齐轮落成一轮,而不是像老 daemon 那样丢掉。
		if frame.Seq > 0 && r.deliverToCatchUpTurn(ctx, frame.SessionID, ev) {
			return nil, nil
		}
		fields := make([]zap.Field, 0, 4)
		fields = append(fields,
			zap.Int64("frameSid", frame.SessionID),
			zap.Int64s("knownSids", knownSids),
		)
		fields = append(fields, remoteEventLogFields(frame.Event)...)
		logger.Ctx(ctx).Warn("remote.handleEvent: event for unknown session dropped", fields...)
		return nil, nil
	}
	sess.mu.Lock()
	if !sess.started {
		sess.mu.Unlock()
		logger.Ctx(ctx).Warn("remote.handleEvent: event before prepared generation start dropped",
			zap.Int64("sid", frame.SessionID),
			zap.String("eventType", fmt.Sprintf("%T", ev)))
		return nil, nil
	}
	if sess.closed {
		sess.mu.Unlock()
		logger.Ctx(ctx).Warn("remote.handleEvent: event after session close dropped",
			zap.Int64("sid", frame.SessionID),
			zap.String("eventType", fmt.Sprintf("%T", ev)))
		return nil, nil
	}
	// non-blocking send: read loop (conn.Serve) dispatches notifications
	// synchronously, so blocking here would stall the entire WebSocket read
	// loop including Start acknowledgement. A burst that exceeds the bounded
	// buffer finalizes the exact generation instead of waiting.
	select {
	case sess.events <- ev:
		sess.mu.Unlock()
		logger.Ctx(ctx).Debug("remote.handleEvent: event delivered",
			zap.Int64("sid", frame.SessionID),
			zap.String("eventType", fmt.Sprintf("%T", ev)))
		return nil, nil
	default:
	}
	// Bounded buffer overflow — finalize this exact generation under sess.mu
	// (same invariant as finishSession: closed flag gates the close).
	if sess.result != nil && sess.result.StopErr == nil {
		sess.result.StopErr = errEventBufferOverflow
	}
	sess.closed = true
	close(sess.events)
	if sess.cancel != nil {
		sess.cancel()
	}
	sess.mu.Unlock()
	overflowFields := make([]zap.Field, 0, 3)
	overflowFields = append(overflowFields, zap.Int64("sid", frame.SessionID))
	overflowFields = append(overflowFields, remoteEventLogFields(frame.Event)...)
	logger.Ctx(ctx).Warn("remote.handleEvent: event delivery exceeded bounded buffer", overflowFields...)
	// Remove from the session map so subsequent events for this generation are
	// dropped as unknown, and trigger the daemon-side Abort asynchronously so
	// the read loop is never blocked on an RPC.
	r.mu.Lock()
	if r.sessions[sess.id] == sess {
		delete(r.sessions, sess.id)
	}
	r.mu.Unlock()
	//nolint:gosec // G118: intentional — the abort must reach agentred even if
	// the desktop connection (whose ctx backs the read loop) is closing, so
	// the orphaned daemon generation is cleaned up. Bounded by
	// generationControlTimeout inside abortOverflowedGeneration.
	go r.abortOverflowedGeneration(sess)
	return nil, nil
}

func (r *Runtime) handleRunResultDone(ctx context.Context, raw json.RawMessage) (any, error) {
	var frame wire.RunResultDoneFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		fields := make([]zap.Field, 0, 3)
		fields = append(fields, zap.Int("rawBytes", len(raw)))
		fields = append(fields, remoteErrorLogFields(err)...)
		logger.Ctx(ctx).Warn("remote.handleRunResultDone: frame unmarshal failed", fields...)
		return nil, nil
	}
	r.mu.RLock()
	sess, ok := r.sessions[frame.SessionID]
	r.mu.RUnlock()
	if ok && frame.Seq > 0 && frame.Seq <= sess.startSeq {
		// 补齐回放上来的、属于**已结束轮次**的终态帧:它在开轮之前就落库了,不可能是
		// 这一轮的结果。放它进去会删掉会话表里当前这一轮、用旧结果覆盖它的 RunResult、
		// 并 close 掉它的 events —— 用户刚发出的消息瞬间「结束」并带着上一轮的答案,
		// 其后的实时帧则全部走 handleEvent 的「未知会话」被丢弃。
		//
		// 它与同一轮回放上来的事件走同一个去处:补齐轮。事件在 handleEvent 里按同一条
		// 判据分流,这一帧则是那一轮的收尾 —— 两者合起来,回放的每一轮都完整落成一张
		// 自己的卡片,而不是揉进用户刚发起的这一轮里。
		logger.Ctx(ctx).Warn("remote runtime: runResultDone from an ended turn — routed to catch-up",
			zap.Int64("sid", frame.SessionID),
			zap.Int64("seq", frame.Seq),
			zap.Int64("turnStartSeq", sess.startSeq))
		// 它是**那一轮**的收尾:补齐轮攒的正是那一轮的内容,到此为止。
		r.closeCatchUpTurn(ctx, frame)
		return nil, nil
	}
	if !ok {
		logger.Ctx(ctx).Info("remote.handleRunResultDone: no live generation for terminal frame",
			zap.Int64("sid", frame.SessionID),
			zap.Int("stopErrCode", frame.StopErrCode))
		// 本进程没有这一轮(App 重启后补齐回放的整轮就长这样):补齐轮攒到这里为止。
		r.closeCatchUpTurn(ctx, frame)
		return nil, nil
	}
	// The provider session identity only discriminates stale generations for Pi
	// prepared runs, where the pre-prompt fork fixes it before Start. Direct runs
	// (e.g. a claudecode fork on Regenerate) legitimately change the provider
	// session during the turn, so comparing it would drop the real terminal frame
	// and leave the events channel open forever; the map-identity check below
	// already guards against a replaced generation for those runs.
	sess.mu.Lock()
	piGeneration := sess.backendType == agent_backend_entity.TypePiAgent
	expectedProviderSessionID := sess.providerSessionID
	sess.mu.Unlock()
	if piGeneration && expectedProviderSessionID != "" && frame.ProviderSessionID != "" &&
		frame.ProviderSessionID != expectedProviderSessionID {
		logger.Ctx(ctx).Warn("remote.handleRunResultDone: stale generation dropped",
			zap.Int64("sid", frame.SessionID),
			zap.Int("stopErrCode", frame.StopErrCode))
		return nil, nil
	}
	r.mu.Lock()
	if r.sessions[frame.SessionID] != sess {
		r.mu.Unlock()
		logger.Ctx(ctx).Warn("remote.handleRunResultDone: replaced generation dropped",
			zap.Int64("sid", frame.SessionID),
			zap.Int("stopErrCode", frame.StopErrCode))
		return nil, nil
	}
	delete(r.sessions, frame.SessionID)
	r.mu.Unlock()

	sess.mu.Lock()
	if sess.cancel != nil {
		sess.cancel()
	}
	sess.result.ProviderSessionID = frame.ProviderSessionID
	sess.result.UserAnchor = frame.UserAnchor
	sess.result.Model = frame.Model
	sess.result.ContextWindow = frame.ContextWindow
	sess.result.TurnToken = frame.TurnToken
	if frame.Usage != nil {
		// provider.Usage 没 JSON tag,wire 端用 UsageWire 中转,这里 1:1 拷回。
		sess.result.Usage = usageFromWire(frame.Usage)
	}
	sess.result.StopErr = stopErrFromFrame(frame)
	if !sess.closed {
		sess.closed = true
		close(sess.events)
	}
	sess.mu.Unlock()
	logger.Ctx(ctx).Info("remote.handleRunResultDone: session ended",
		zap.Int64("sid", frame.SessionID),
		zap.Bool("hasStopErr", frame.StopErrMsg != ""),
		zap.Int("stopErrBytes", len(frame.StopErrMsg)),
		zap.Int("stopErrCode", frame.StopErrCode))
	return nil, nil
}

func stopErrFromFrame(f wire.RunResultDoneFrame) error {
	if f.StopErrCode == 0 && f.StopErrMsg == "" {
		return nil
	}
	if sent := wire.SentinelFromCode(f.StopErrCode); sent != nil {
		return sent
	}
	return errors.New(f.StopErrMsg)
}

// ── control RPCs ────────────────────────────────────────────────────────────

func (r *Runtime) Steer(ctx context.Context, sessionID int64, queuedID, text string) error {
	if !r.hasSession(sessionID) {
		return agentruntime.ErrNoActiveTurn
	}
	return r.callSession(ctx, sessionID, wire.MethodSteer, wire.SteerParams{
		SessionID: sessionID, PeerFingerprint: r.originFor(sessionID),
		QueuedID: queuedID, Text: text,
	}, &wire.OK{})
}

func (r *Runtime) CancelSteer(ctx context.Context, sessionID int64, queuedID string) ([]string, error) {
	if !r.hasSession(sessionID) {
		return nil, agentruntime.ErrNoActiveTurn
	}
	var res wire.CancelSteerResult
	if err := r.callSession(ctx, sessionID, wire.MethodCancelSteer, wire.CancelSteerParams{
		SessionID: sessionID, PeerFingerprint: r.originFor(sessionID), QueuedID: queuedID,
	}, &res); err != nil {
		return nil, err
	}
	return res.Removed, nil
}

func (r *Runtime) DrainPending(ctx context.Context, sessionID int64) []agentruntime.ConsumedSteer {
	if !r.hasSession(sessionID) {
		return nil
	}
	var res wire.DrainResult
	if err := r.callSession(ctx, sessionID, wire.MethodDrainPending, wire.DrainParams{
		SessionID: sessionID, PeerFingerprint: r.originFor(sessionID),
	}, &res); err != nil {
		return nil
	}
	return res.Steers
}

func (r *Runtime) Abort(ctx context.Context, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	r.mu.RLock()
	sess := r.sessions[sessionID]
	r.mu.RUnlock()
	// 本进程在跑 Pi generation:走本地收尾路径(main 的 Pi abort)。
	if sess != nil && sess.backendType == agent_backend_entity.TypePiAgent {
		return r.abortGeneration(ctx, sess, turnToken)
	}
	// 其余会话(含补齐进 tracked / autoSessions 的 R12 远端会话):用 HEAD 的
	// hasSession 判定 + origin 路由,避免把无本地 Run 的会话拦成 ErrNoActiveTurn。
	if !r.hasSession(sessionID) {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	var res wire.AbortResult
	if err := r.callSession(ctx, sessionID, wire.MethodAbort, wire.AbortParams{
		SessionID: sessionID, PeerFingerprint: r.originFor(sessionID), TurnToken: turnToken,
	}, &res); err != nil {
		return agentruntime.AbortOutcome{}, err
	}
	return agentruntime.AbortOutcome{TurnKind: res.TurnKind}, nil
}

func (r *Runtime) StopBackgroundTask(ctx context.Context, sessionID int64, taskID string) error {
	if !r.hasSession(sessionID) {
		return agentruntime.ErrNoActiveTurn
	}
	return r.callSession(ctx, sessionID, wire.MethodStopBackgroundTask, wire.StopBackgroundTaskParams{
		SessionID: sessionID, PeerFingerprint: r.originFor(sessionID),
		TaskID: taskID,
	}, &wire.OK{})
}

func (r *Runtime) SetPermissionMode(ctx context.Context, sessionID int64, mode string) error {
	if !r.hasSession(sessionID) {
		return agentruntime.ErrNoActiveTurn
	}
	return r.callSession(ctx, sessionID, wire.MethodSetPermissionMode, wire.SetPermissionModeParams{
		SessionID: sessionID, PeerFingerprint: r.originFor(sessionID), Mode: mode,
	}, &wire.OK{})
}

func (r *Runtime) SubmitAnswer(ctx context.Context, sessionID int64, requestID string, questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer, skipped bool) error {
	if !r.hasSession(sessionID) {
		return agentruntime.ErrNoActiveTurn
	}
	var res wire.PeerSessionControlResult
	if err := r.callSession(ctx, sessionID, wire.MethodSubmitAnswer, wire.SubmitAnswerParams{
		SessionID: sessionID, PeerFingerprint: r.originFor(sessionID), RequestID: requestID,
		Questions: questions, Answers: answers, Skipped: skipped,
	}, &res); err != nil {
		return err
	}
	return controlResultError(res)
}

func (r *Runtime) SubmitToolPermission(ctx context.Context, sessionID int64, requestID string, allow, alwaysAllowSession bool, denyReason string) error {
	if !r.hasSession(sessionID) {
		return agentruntime.ErrNoActiveTurn
	}
	var res wire.PeerSessionControlResult
	if err := r.callSession(ctx, sessionID, wire.MethodSubmitToolPermission, wire.SubmitToolPermissionParams{
		SessionID: sessionID, PeerFingerprint: r.originFor(sessionID), RequestID: requestID,
		Allow: allow, AlwaysAllowSession: alwaysAllowSession, DenyReason: denyReason,
	}, &res); err != nil {
		return err
	}
	return controlResultError(res)
}

// PendingWaiters 交出那台 daemon 上这条会话此刻仍在阻塞的全部待决策 —— 与
// SubmitAnswer / SubmitToolPermission 这两个写侧同源的读侧。
//
// 它刻意**不**实现 agentruntime.WaiterLister,尽管名字与语义都对得上:那个接口没有
// 错误返回,契约明写 PendingWaiters 是一次进程内内存快照读(mirrors
// SteerDrainer.DrainPending)、不是 I/O 调用。远端这一路偏偏是一次 RPC —— 会阻塞、
// 会失败,而在没有错误返回的形状里,失败只能被降级成零值快照;零值快照的语义又恰好是
// 「确实没有待决策」。于是一次瞬时网络故障就把一条正卡在审批上的会话画成空闲:用户
// 看不到卡片,也无从知道自己漏了什么。所以远端走自己的形状,把错误如实交给调用方,
// 由它决定是报错还是回退(LSP:实现不得悄悄违背接口契约)。
//
// 不像别的控制类调用那样先过 hasSession:这是只读快照,daemon 对不属于调用方 / 它不
// 认识的会话本就回空列表而不是报错(R7),本地再加一道「不认识就当空」只会多造一个
// 说不清是「真没有」还是「本机没跟上」的静默分支。
func (r *Runtime) PendingWaiters(ctx context.Context, sessionID int64) (wire.SessionPendingWaitersResult, error) {
	var res wire.SessionPendingWaitersResult
	if err := r.callSentinel(ctx, wire.MethodSessionPendingWaiters, wire.SessionPendingWaitersParams{
		SessionID: sessionID, PeerFingerprint: r.originFor(sessionID),
	}, &res); err != nil {
		return wire.SessionPendingWaitersResult{}, err
	}
	return res, nil
}

func controlResultError(res wire.PeerSessionControlResult) error {
	if res.AlreadyHandled {
		return agentruntime.ErrWaiterNotFound
	}
	return nil
}

func (r *Runtime) GetGoal(ctx context.Context, req agentruntime.GoalRequest) (*agentruntime.Goal, error) {
	var res wire.GoalResult
	params, err := r.goalParams(req)
	if err != nil {
		return nil, err
	}
	if err := r.callSentinel(ctx, wire.MethodGetGoal, params, &res); err != nil {
		return nil, err
	}
	return res.Goal, nil
}

func (r *Runtime) SetGoal(ctx context.Context, req agentruntime.GoalRequest) (*agentruntime.Goal, error) {
	var res wire.GoalResult
	params, err := r.goalParams(req)
	if err != nil {
		return nil, err
	}
	if err := r.callSentinel(ctx, wire.MethodSetGoal, params, &res); err != nil {
		return nil, err
	}
	return res.Goal, nil
}

func (r *Runtime) ClearGoal(ctx context.Context, req agentruntime.GoalRequest) (bool, error) {
	var res wire.GoalClearResult
	params, err := r.goalParams(req)
	if err != nil {
		return false, err
	}
	if err := r.callSentinel(ctx, wire.MethodClearGoal, params, &res); err != nil {
		return false, err
	}
	return res.Cleared, nil
}

func (r *Runtime) goalParams(req agentruntime.GoalRequest) (wire.GoalParams, error) {
	var backendJSON json.RawMessage
	if req.Backend != nil {
		raw, err := json.Marshal(req.Backend)
		if err != nil {
			return wire.GoalParams{}, fmt.Errorf("marshal backend: %w", err)
		}
		backendJSON = raw
	}
	return wire.GoalParams{
		SessionID:         req.SessionID,
		PeerFingerprint:   r.originFor(req.SessionID),
		AgentID:           req.AgentID,
		ProviderSessionID: req.ProviderSessionID,
		Backend:           backendJSON,
		Cwd:               req.Cwd,
		Objective:         req.Objective,
		Status:            req.Status,
		TokenBudget:       req.TokenBudget,
		LLMProviderKey:    remoteGoalProviderKey(req),
		LLMModelKey:       remoteGoalModelKey(req),
	}, nil
}

// remoteGoalProviderKey 返回 goal 的执行侧 ProviderKey（决策 11）：优先取执行侧
// 解析结果，缺省回落 backend 主绑定 —— 与 Run 的 effectiveProviderKey 同口径，
// 保证 goal 与 turn 共用同一个 CLI 会话池时启动期比对键不翻转。
func remoteGoalProviderKey(req agentruntime.GoalRequest) string {
	if req.Effective != nil {
		if pk := strings.TrimSpace(req.Effective.ProviderKey); pk != "" {
			return pk
		}
	}
	if req.Backend != nil {
		return strings.TrimSpace(req.Backend.LLMProviderKey)
	}
	return ""
}

// remoteGoalModelKey 返回 goal 的执行侧 ModelKey（决策 11），语义同 remoteModelKey：
// 直接透传执行侧结果，空 ModelKey（provider-default）不被 backend 固定模型带偏。
func remoteGoalModelKey(req agentruntime.GoalRequest) string {
	if req.Effective != nil {
		return strings.TrimSpace(req.Effective.ModelKey)
	}
	if req.Backend != nil {
		return strings.TrimSpace(req.Backend.LLMModelKey)
	}
	return ""
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (r *Runtime) abortGeneration(ctx context.Context, sess *remoteSession, turnToken uint64) (agentruntime.AbortOutcome, error) {
	if sess == nil {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	r.mu.RLock()
	current := r.sessions[sess.id] == sess
	r.mu.RUnlock()
	if !current {
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, nil
	}

	// Cancellation is owner-local and must reach preparation immediately. Only
	// the SessionID-only daemon Abort call waits behind the generation gate.
	sess.mu.Lock()
	if sess.cancel != nil {
		sess.cancel()
	}
	registrationDone := sess.registrationDone
	sess.mu.Unlock()
	if registrationDone != nil {
		<-registrationDone
	}

	releaseGenerationGate := r.acquireGenerationGate(sess.id)
	defer releaseGenerationGate()
	r.mu.RLock()
	current = r.sessions[sess.id] == sess
	r.mu.RUnlock()
	if !current {
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, nil
	}
	sess.abortOnce.Do(func() {
		base := ctx
		if base == nil {
			base = context.Background()
		}
		abortCtx, cancel := context.WithTimeout(context.WithoutCancel(base), generationControlTimeout)
		defer cancel()
		var res wire.AbortResult
		err := r.callSentinel(abortCtx, wire.MethodAbort, wire.AbortParams{SessionID: sess.id, TurnToken: turnToken}, &res)
		if errors.Is(err, agentruntime.ErrNoActiveTurn) {
			err = nil
		}
		sess.abortErr = err
		sess.abortOutcome = agentruntime.AbortOutcome{TurnKind: res.TurnKind}
		r.finishSession(sess, agentruntime.ErrAborted)
	})
	return sess.abortOutcome, sess.abortErr
}

// abortOverflowedGeneration cancels the daemon-side generation after a bounded
// buffer overflow. It runs in its own goroutine so the read loop that detected
// the overflow is never blocked on an RPC. The session has already been
// finalized (StopErr set, events closed, removed from the session map) by the
// caller; this call only ensures the daemon stops producing events for the
// abandoned generation.
func (r *Runtime) abortOverflowedGeneration(sess *remoteSession) {
	abortCtx, cancel := context.WithTimeout(context.Background(), generationControlTimeout)
	defer cancel()
	err := r.callSentinel(abortCtx, wire.MethodAbort, wire.AbortParams{SessionID: sess.id}, &wire.AbortResult{})
	if errors.Is(err, agentruntime.ErrNoActiveTurn) {
		err = nil
	}
	sess.mu.Lock()
	sess.abortErr = err
	sess.mu.Unlock()
}

func (r *Runtime) finishSession(sess *remoteSession, stopErr error) {
	if sess == nil {
		return
	}
	r.mu.Lock()
	if r.sessions[sess.id] == sess {
		delete(r.sessions, sess.id)
	}
	r.mu.Unlock()
	sess.mu.Lock()
	if sess.cancel != nil {
		sess.cancel()
	}
	if stopErr != nil && sess.result != nil && sess.result.StopErr == nil {
		sess.result.StopErr = stopErr
	}
	if !sess.closed {
		sess.closed = true
		close(sess.events)
	}
	sess.mu.Unlock()
}

func (r *Runtime) acquireGenerationGate(sid int64) func() {
	r.mu.Lock()
	gate := r.generationGates[sid]
	if gate == nil {
		gate = &generationGate{}
		r.generationGates[sid] = gate
	}
	gate.refs++
	r.mu.Unlock()

	gate.mu.Lock()
	return func() {
		gate.mu.Unlock()
		r.mu.Lock()
		gate.refs--
		if gate.refs == 0 && r.generationGates[sid] == gate {
			delete(r.generationGates, sid)
		}
		r.mu.Unlock()
	}
}

func (r *Runtime) hasSession(sid int64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.sessions[sid]
	if ok {
		return true
	}
	// 已认领 daemon 上同账号客户端要操作别的对端发起的会话(R12):那条会话没有本地
	// Run,是经补齐被接管进 tracked / autoSessions 的。不把它们算进来,控制请求就会
	// 被 ErrNoActiveTurn 拦在本地 —— 用户能看到那条会话却发不了指令。
	if _, ok = r.autoSessions[sid]; ok {
		return true
	}
	_, ok = r.tracked[sid]
	return ok
}

func (r *Runtime) callSentinel(ctx context.Context, method string, params, result any) error {
	if err := r.conn().Call(ctx, method, params, result); err != nil {
		return wire.FromJSONRPCError(err)
	}
	return nil
}

// callSession 是带会话身份的控制类调用。ErrNoActiveTurn 时**重新接管一次再重试**。
//
// 为什么必须重试:daemon 的 runtime.* 是 per-connection 注册进共享 registry 的,
// 每条新连接都会带着一张空的会话表把它们重新注册一遍,把上一次 runtime.session.attach
// 的接管静默还原。而桌面端同时握着 2-3 条同指纹连接(连接池 / 设备心跳 / 刷新探测),
// 只要其中任意一条重连过,接管就没了 —— 此后提交决策会被 daemon 的幂等折叠(R8)
// 折成 OK,会话永久停在等待输入上。所以接管必须是可反复发起的。
func (r *Runtime) callSession(ctx context.Context, sessionID int64, method string, params, result any) error {
	err := r.callSentinel(ctx, method, params, result)
	// canReconnect 同时管住两件事:没装重连端口的调用方(老接线、单测)一律走今天
	// 的路径,不会凭空多出一次 attach;已被证伪的老 daemon 也不再白试。
	if err == nil || !errors.Is(err, agentruntime.ErrNoActiveTurn) || !r.canReconnect() {
		return err
	}
	if _, aerr := r.attachSession(ctx, sessionID); aerr != nil {
		logger.Ctx(ctx).Warn("remote runtime: re-attach before retry failed",
			zap.Int64("sid", sessionID), zap.String("method", method), zap.Error(aerr))
		return err
	}
	logger.Ctx(ctx).Info("remote runtime: re-attached session, retrying control call",
		zap.Int64("sid", sessionID), zap.String("method", method))
	return r.callSentinel(ctx, method, params, result)
}
