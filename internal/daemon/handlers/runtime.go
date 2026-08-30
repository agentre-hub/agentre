// Package handlers — runtime.go implements the runtime.* RPC surface,
// a 1:1 transparent proxy of agentruntime.Runtime + its 7 optional control
// sub-interfaces (Steerer / SteerCanceler / SteerDrainer / Aborter /
// PermissionModeSetter / AskAnswerSink / ToolPermissionSink). Each RPC
// method either delegates straight to the backend runtime or returns the
// agentruntime sentinel that the wire codec maps to the client.
//
// 单连接寿命内会有多个 sessionID（每个 chat session 一个）。非 Pi runtime.run
// 直接启动 fanout；Pi 复用同一方法依次注册 generation、PrepareRun、Start。启动后
// fanout 把 backend events 推到 runtime.event notification，channel close 后再发
// runtime.runResultDone 终态帧;之后 session 从 sessions
// map 摘除,gateway token revoke。所有控制方法（Steer / Abort / ...）按
// sessionID 查 backendType,再 type-assert backend runtime 拿对应的子接口,
// 没实现就返 ErrUnsupported,session 不在就返 ErrNoActiveTurn —— 两者都被
// 协议适配层翻译成稳定的类型化 RPC error code 跨进程传递。
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/connection"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	piagentrt "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
)

// RuntimeDeps are the explicit constructor inputs for RuntimeHandlers. All
// fields are required except RuntimeFor (defaults to agentruntime.RuntimeFor).
type RuntimeDeps struct {
	// NotifyFor 解析「此刻活着的、属于该对端的」推送端口,没有则返回 nil。它是函数而
	// 不是一个 NotifierPort,因为 RuntimeHandlers 是 per-connection 构造的,而 fanout /
	// forwardAutonomousTurn 的 goroutine 会活过那条连接:静态捕获的端口在客户端重连后
	// 仍指向已死的旧连接,通知就再也发不出去了(见 daemon.bindConn 注释)。
	NotifyFor func(peerFingerprint string) NotifierPort
	// Journal 是通知日志,Daemon 级(每个 daemon 一份),不随连接生灭。
	Journal JournalPort
	// Sessions 是会话生命周期的写入口,同样 Daemon 级。它落的那一行是重连客户端
	// 拿会话清单的唯一来源,也是 daemon 启动时把非终态会话标成中断(R10)的对象。
	Sessions SessionLifecyclePort
	// SessionQuery 是同一批会话行的读出口(Daemon 级)。提交决策解不出会话时,它是
	// 「这条会话是真的结束了,还是只是不在**这个** handler 手里」的判别依据 ——
	// 内存会话表答不了,因为 registry 是全局一份、每条新连接都会用一张空表覆盖它
	// (见 idempotentSubmitResult)。留空即无判别依据,一律按 R8 折成成功。
	SessionQuery SessionQueryPort
	Gateway      GatewayPort
	Lookup       LLMProviderLookupPort
	RuntimeFor   func(agent_backend_entity.BackendType) agentruntime.Runtime
	// CLIPathForBackend resolves the claimed daemon's in-memory per-device
	// overlay by account backend SyncID. false preserves paired-desktop behavior
	// before any account snapshot exists; true with an empty path means PATH.
	CLIPathForBackend func(backendSyncID string) (cliPath string, authoritative bool)
	// ClaimedAccountID returns the daemon account authorized to target a
	// non-caller origin peer in control requests.
	ClaimedAccountID func() string
	// SteerSource 是「queuedID → 提交方对端」的映射(R17),Daemon 级共享(见
	// SteerSourcePort 注释)。nil 时 NewRuntimeHandlers 兜成 no-op,单测/旧调用不炸。
	SteerSource SteerSourcePort
	// GenerationRegistry 是 Daemon 级的 generation 属主表(见 sessions.Registry)。
	// RuntimeHandlers 是 per-connection 构造的,而一条会话上在飞的 generation 要跨
	// 连接排他:重连必须等旧属主释放,迟到的旧清理也顶不掉重连。
	GenerationRegistry RuntimeGenerationRegistry
}

// RuntimeGenerationRegistry owns the cross-connection reservation for a Pi
// generation. The concrete in-memory sessions registry implements it without
// adding a wire field or persistent state.
type RuntimeGenerationRegistry interface {
	ClaimConnection(connection connection.Conn, sessionID int64, generation string) bool
	ReleaseConnection(connection connection.Conn, sessionID int64, generation string) bool
}

// RuntimeHandlers groups the runtime.* RPC handlers and owns the
// per-connection session map so control RPCs can resolve sessionID → backend.
//
// Lock invariant: h.mu is the only lock guarding h.sessions and every mutable
// runtimeSession lifecycle field. Code holding h.mu must never wait on owner
// state, a channel, the generation registry, notifier delivery, or an external
// runtime Close/Abort call. Snapshots and state claims happen under h.mu; all
// potentially blocking work happens after it is released.
type RuntimeHandlers struct {
	deps RuntimeDeps

	mu sync.RWMutex
	// sessions 的键是**按对端隔离过的 backend 会话键**(runtimeSessionID),不是客户端
	// 报的会话 id:同一台 daemon 服务着多个对端,按裸 id 存放会让同号会话互相顶掉。
	sessions map[int64]*runtimeSession
	closed   bool

	cleanupOnce sync.Once
	cleanupDone chan struct{}
	cleanupErr  error
	// runtimeFor mirrors deps.RuntimeFor but is swappable at runtime via
	// SwapRuntimeFor (used by tests that need to flip the runtime registry
	// after a session is already live).
	runtimeFor func(agent_backend_entity.BackendType) agentruntime.Runtime
	// sessionTokens 是每个 session 的常驻 gateway token 缓存(与桌面共用
	// agentruntime.SessionTokenCache:签一次 / 改道 / 撤销只有那一份实现)。
	// 该 token 在 spawn 时烤进 daemon spawn 的 claude 子进程 env,子进程跨轮复用时
	// env 不重建 —— 所以 token 必须签成永久(ttl=0)、跨轮稳定、且 **不在轮末撤销**。
	// 旧实现每轮签 time.Hour token 并在 fanout 轮末撤销,而子进程手里还是首轮那个
	// (已撤销)token → 第二轮起 PostToolUse hook 撞 401、SteerInbox drain 不到。
	// daemon 侧没有 session 关闭钩子(故不调用 Revoke),token 随 daemon 进程退出释放
	// (内存级、有界)。
	sessionTokens *agentruntime.SessionTokenCache
	// autoSubs 防同一 session 重复起「自主续轮转发」goroutine(每会话一个)。
	// goroutine 在真实 runtime 的 AutonomousTurns(sid) channel close(子进程 evict)时
	// 退出并清这条,下次 Run 复用 / 重 spawn 时再起。
	autoSubs sync.Map // sessionID(int64) → struct{}
}

type runtimeSession struct {
	backendType agent_backend_entity.BackendType
	ctx         context.Context
	cancel      context.CancelFunc
	connection  connection.Conn
	// adopted 标记这是 Adopt 放进来的**占位行**:它只为了让重连后的这条连接解得出
	// 会话(见 Adopt),背后并没有一轮在跑。它必须与真正的 generation 属主区分开 ——
	// 否则 Pi 那道「一条会话同时只有一个 generation」的闸门会把占位行当成在跑的一轮,
	// 重连之后再也开不出新一轮。
	adopted bool

	prepared          piagentrt.PreparedRun
	providerSessionID string
	generationToken   string
	preparing         bool
	starting          bool
	started           bool
	cancelRequested   bool
	disconnected      bool
	terminalClaimed   bool
	finalizing        bool
	aborting          bool
	terminalDone      chan struct{}
	terminalOnce      sync.Once
	finalErr          error
}

const (
	runtimeConnectionCleanupTimeout = 2 * time.Second
	runtimePiTerminalWaitTimeout    = 2 * time.Second
)

// GatewayPort 必须满足共享令牌缓存的路由端口 —— 编译期钉死,避免端口方法漂移后
// 才在运行期发现会话 token 签不出来。
var _ agentruntime.SessionTokenRouter = (GatewayPort)(nil)

// NewRuntimeHandlers wires the dependencies and prepares the session map.
func NewRuntimeHandlers(deps RuntimeDeps) *RuntimeHandlers {
	if deps.RuntimeFor == nil {
		deps.RuntimeFor = agentruntime.RuntimeFor
	}
	if deps.SteerSource == nil {
		deps.SteerSource = noopSteerSource{}
	}
	h := &RuntimeHandlers{
		deps:        deps,
		sessions:    map[int64]*runtimeSession{},
		runtimeFor:  deps.RuntimeFor,
		cleanupDone: make(chan struct{}),
	}
	h.sessionTokens = agentruntime.NewSessionTokenCache(
		"handlers.ensureSessionToken",
		func() agentruntime.SessionTokenRouter {
			if h.deps.Gateway == nil {
				return nil
			}
			return h.deps.Gateway
		},
	)
	return h
}

// noopSteerSource 是未注入 SteerSourcePort 时的空实现:单测 / 旧调用不记录任何来源,
// 被消费的 steer 保持 SourcePeer/SourceName 为空(本机路径,与今天行为一致)。
type noopSteerSource struct{}

func (noopSteerSource) Record(string, SteerSourceEntry)         {}
func (noopSteerSource) Consume(string) (SteerSourceEntry, bool) { return SteerSourceEntry{}, false }
func (noopSteerSource) Forget(string)                           {}

// Adopt 让这条连接的 handler 认下一条**别处发起的**会话:它此后能像自己起的那样解出
// 会话的 backend,控制 RPC(steer / abort / submitAnswer / submitToolPermission …)因此
// 继续可用。
//
// 它存在是因为 RuntimeHandlers 是 per-connection 构造的:客户端重连后拿到的是一个内存
// 会话表为空的新 handler,断连期间产生的待决策**答不回去** —— submitToolPermission 解不出
// 会话,再被 R8 的幂等折成「成功」,于是 waiter 没人回答、客户端以为答过了,叠加 R9 的
// 不设过期就是永久挂死。只由显式接管(runtime.session.attach)调用,且只在 daemon 已经
// 确认该会话属于调用方之后 —— 认下一条不属于自己的会话等于跨对端夺取控制权。
//
// 认下的是**这个对端**的那条会话:内存会话表与 backend 一样按隔离后的会话键存放,
// 否则同号会话会在这张表里互相顶掉(见 runtimeSessionID)。
func (h *RuntimeHandlers) Adopt(ctx context.Context, sessionID int64, backendType agent_backend_entity.BackendType) {
	h.AdoptForPeer(peerFingerprint(ctx), sessionID, backendType)
}

// AdoptForPeer remembers a session under its persisted origin after an
// authorized account-level attach.
func (h *RuntimeHandlers) AdoptForPeer(peer string, sessionID int64, backendType agent_backend_entity.BackendType) {
	if sessionID == 0 || backendType == "" {
		return
	}
	h.register(runtimeSessionID(peer, sessionID), &runtimeSession{backendType: backendType, adopted: true})
}

// SwapRuntimeFor replaces the runtime lookup at runtime — test seam only.
// Production code should construct a new RuntimeHandlers instead of mutating.
func (h *RuntimeHandlers) SwapRuntimeFor(fn func(agent_backend_entity.BackendType) agentruntime.Runtime) {
	h.mu.Lock()
	h.runtimeFor = fn
	h.mu.Unlock()
}

// Close cancels and closes every Pi generation owned by this connection's
// handler. Cleanup starts once, is internally bounded, and callers may impose a
// tighter wait through ctx. Non-Pi runtime sessions keep their existing
// lifecycle behavior.
func (h *RuntimeHandlers) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	h.cleanupOnce.Do(func() {
		go func() {
			h.cleanupErr = h.cleanupConnectionPiGenerations()
			close(h.cleanupDone)
		}()
	})
	select {
	case <-h.cleanupDone:
		return h.cleanupErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

type runtimeSessionCleanup struct {
	sessionID int64
	owner     *runtimeSession
	finalize  bool
}

func (h *RuntimeHandlers) cleanupConnectionPiGenerations() error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), runtimeConnectionCleanupTimeout)
	defer cancel()

	h.mu.Lock()
	h.closed = true
	owned := make([]runtimeSessionCleanup, 0, len(h.sessions))
	for sessionID, owner := range h.sessions {
		if owner.backendType != agent_backend_entity.TypePiAgent || owner.ctx == nil {
			continue
		}
		owner.disconnected = true
		owner.cancelRequested = true
		owned = append(owned, runtimeSessionCleanup{
			sessionID: sessionID,
			owner:     owner,
			finalize:  !owner.preparing && !owner.starting && !owner.terminalClaimed && !owner.finalizing,
		})
	}
	h.mu.Unlock()

	for _, item := range owned {
		if item.owner.cancel != nil {
			item.owner.cancel()
		}
		if item.finalize {
			go func(current runtimeSessionCleanup) {
				_ = h.finalizePiGeneration(cleanupCtx, current.sessionID, current.owner)
			}(item)
		}
	}

	var cleanupErr error
	for _, item := range owned {
		select {
		case <-item.owner.terminalDone:
			cleanupErr = errors.Join(cleanupErr, h.piFinalError(item.owner))
		case <-cleanupCtx.Done():
			return errors.Join(cleanupErr, cleanupCtx.Err())
		}
	}
	return cleanupErr
}

// ── Capabilities ────────────────────────────────────────────────────────────

func (h *RuntimeHandlers) Capabilities(_ context.Context, p wire.CapabilitiesParams) (wire.CapabilitiesResult, error) {
	rt := h.lookupRuntimeByType(agent_backend_entity.BackendType(p.BackendType))
	if rt == nil {
		return wire.CapabilitiesResult{}, fmt.Errorf("no runtime registered for backend type %q", p.BackendType)
	}
	return wire.CapabilitiesResult{Capabilities: rt.Capabilities()}, nil
}

// ── Run ─────────────────────────────────────────────────────────────────────

func (h *RuntimeHandlers) Run(ctx context.Context, p wire.RunParams) (wire.RunAck, error) {
	var be agent_backend_entity.AgentBackend
	if err := json.Unmarshal(p.Backend, &be); err != nil {
		return wire.RunAck{}, fmt.Errorf("parse backend: %w", err)
	}
	bt := agent_backend_entity.BackendType(be.Type)
	if bt == agent_backend_entity.TypeBuiltin {
		return wire.RunAck{}, errors.New("builtin backend not supported in agentred")
	}
	if bt == agent_backend_entity.TypeOpenClaw {
		return wire.RunAck{}, errors.New("openclaw backend not supported in agentred: remote secret enrollment is unavailable")
	}
	if h.deps.CLIPathForBackend != nil {
		if cliPath, authoritative := h.deps.CLIPathForBackend(be.SyncID); authoritative {
			// Account snapshots own the execution-side per-device overlay. An
			// absent overlay authoritatively means PATH, so it also clears a
			// desktop machine's absolute path from the wire payload.
			be.CLIPath = cliPath
		}
	}

	rt := h.lookupRuntimeByType(bt)
	if rt == nil {
		return wire.RunAck{}, fmt.Errorf("backend %q not registered", be.Type)
	}
	// 通知出口必须在这里建:对端指纹只有请求 ctx 上有,而 fanout / forwardAutonomousTurn
	// 跑在脱离 ctx 的 goroutine 里(见 sessionEmitter 注释)。它同时定下这一轮的 backend
	// 会话键 em.rid —— 交给 backend 的一律是这个按对端隔离的键,不是客户端报的裸 id。
	//
	// R9:一轮可以由**别的同账号对端**在这条会话上开起来(浏览器给桌面端发起的会话发
	// 新消息)。会话归属因此由点名的 origin 决定,而不是调用方自己的指纹 —— 与控制族
	// 走同一条 ResolveSessionPeer 约定:省略 = 调用方自己的对端,点名别人是账号级能力
	// (配对身份点名一律 ErrUnauthorized)。
	runPeer, err := ResolveSessionPeer(ctx, p.PeerFingerprint, h.deps.ClaimedAccountID)
	if err != nil {
		return wire.RunAck{}, err
	}
	em := h.newEmitterFor(ctx, p.SessionID, runPeer)

	// R18:「开新一轮」的发起方标记。浏览器在空闲会话上发消息时随 runtime.run 声明自己的
	// 设备身份(SourceDevice 非空),daemon 据此在事件流开头注入一条 user_message 事件,
	// 扇出给同一条会话的其余订阅者 —— 桌面端据此把这一轮落成一行带来源标识的用户消息。
	// 桌面端自己发消息不带 SourceDevice(单端零变化),不注入,事件流与今天逐帧一致。
	userMsg := userMessageFor(p)

	var (
		piPreparer piagentrt.RunPreparer
		piOwner    *runtimeSession
	)
	if be.IsPiAgent() {
		if preparer, ok := rt.(piagentrt.RunPreparer); ok {
			piPreparer = preparer
			piOwner = h.lookupSession(em.rid)
			// 占位行不是一轮:重连接管只是把这条会话认到这条连接名下,新一轮照常从
			// 注册 generation 开始(注册时把占位行顶掉)。
			if piOwner == nil || piOwner.adopted {
				return h.registerPiGeneration(ctx, em, p, &be)
			}
			ownsGeneration := piOwner.generationToken == strings.TrimSpace(p.PermissionMode)
			ownsConnection := piOwner.connection == nil || piOwner.connection == connection.FromContext(ctx)
			if !ownsGeneration || !ownsConnection {
				return wire.RunAck{}, errors.New("runtime.run: stale Pi generation request")
			}
		}
	}

	// Provider / Gateway 由 daemon 自家解 —— wire 已不再携带客户端版本:
	//   - APIKey 由 daemon 本机 state 读取,不让 desktop 每个 turn 越线漂移;
	//   - GatewayURL 是 daemon 本机 127.0.0.1:<port>,对 daemon spawn 的 CLI 子
	//     进程可达;desktop 本机 URL 在 daemon 上拨不到。
	//
	// 会话级目标(决策 9/11):wire 带 effectiveProviderKey + LLMModelKey(会话
	// provider_key/model_key 优先),daemon 按它们从自家目录自解。provider-default
	// 的 Provider 缺失保留 #39 回退 agent 绑定;fixed-model 缺失/停用/Provider 缺失
	// 一律严格阻止,绝不静默降级为默认模型(决策 7)。
	provider, effective, providerFallbackKey, err := h.resolveTarget(ctx, p.LLMProviderKey, p.LLMModelKey, &be)
	if err != nil {
		return wire.RunAck{}, err
	}

	var gatewayURL, gatewayToken string
	if provider != nil {
		var terr error
		// 会话级常驻 token:首轮签、后续轮复用同一个,**不在轮末撤销**(见
		// sessionTokens 注释)。decode/Run 失败也不撤销 —— token 留着给下一轮重试复用,
		// 没用上的也只是随 daemon 退出释放(有界)。
		// 按解析出来的 provider 路由,而不是 be.LLMProviderKey:桌面端中途换了供应商时,
		// 下一轮 wire 带的是新的 effective key,这条常驻 token 的上游要跟着变(决策 3/12)。
		gatewayURL, gatewayToken, terr = h.ensureSessionToken(ctx, em.rid, &be, provider.ProviderKey, effective.ModelKey)
		if terr != nil {
			return wire.RunAck{}, terr
		}
	}

	history, err := decodeHistory(p.History)
	if err != nil {
		return wire.RunAck{}, fmt.Errorf("decode history: %w", err)
	}
	userBlocks, err := decodeUserBlocks(p.UserBlocks)
	if err != nil {
		return wire.RunAck{}, fmt.Errorf("decode user blocks: %w", err)
	}

	req := agentruntime.RunRequest{
		Backend:           &be,
		Provider:          provider,
		Effective:         effective,
		AgentID:           p.AgentID,
		AgentSyncID:       p.AgentSyncID,
		SessionID:         em.rid,
		Cwd:               p.Cwd,
		SystemPrompt:      p.SystemPrompt,
		ProviderSessionID: p.ProviderSessionID,
		UserText:          p.UserText,
		UserBlocks:        userBlocks,
		History:           history,
		Compact:           p.Compact,
		GatewayURL:        gatewayURL,
		GatewayToken:      gatewayToken,
		ForkAnchor:        p.ForkAnchor,
		PermissionMode:    p.PermissionMode,
		CollaborationMode: p.CollaborationMode,
		// 内置工具 MCP server 的 URL 是 desktop 的 127.0.0.1(在 daemon 主机拨不到),
		// 改写成 daemon 本机 gateway base → CLI 打到本地 /mcp/ 隧道入口,再反向请求回
		// desktop 执行。Headers(desktop 签的 token)/ Tools / Name 原样保留。
		MCPServers: rewriteMCPServersForDaemon(
			p.MCPServers,
			func() string { return daemonGatewayBase(h.deps.Gateway) },
			em.peer,
			em.sid,
		),
		EnabledPlugins: p.EnabledPlugins,
	}
	if piPreparer != nil {
		// PermissionMode carries only the remote transport generation owner for
		// Pi. Pi has no permission-mode capability, so never pass it to runtime.
		req.PermissionMode = ""
	}

	if piPreparer != nil {
		h.mu.RLock()
		prepared := h.sessions[em.rid] == piOwner && piOwner.prepared != nil
		h.mu.RUnlock()
		if prepared {
			return h.startPreparedPi(em, p, &be, piOwner, bt, providerFallbackKey)
		}
		return h.preparePi(em, p, &be, piOwner, piPreparer, req)
	}

	// 续话(决策 8):provider_session_id 已由上一轮在这台 daemon 上落库,调用方不再需要
	// 提供。这里只改直连(非 Pi)路径 —— Pi 的 prepare/start 由已准备的身份自己带,不受
	// 影响;调用方显式提供的值(重开 fork 等)优先于落库的那份。查不出行或读失败时保持
	// 空,让 runtime 自己新建会话,不拿一个读不出来的库去换续话。
	// 挂账修复(2026-08-11):FreshSession=true 声明这一轮**必须全新**(regenerate 无锚点
	// 时 / provider 会话失效恢复),即使落库有旧 id 也不许续 —— 否则这两条路径的空字段
	// 被重载成「续话」,regenerate 退化成续旧上下文、gone 恢复永远撞同一个失效 id。
	if !p.FreshSession && strings.TrimSpace(req.ProviderSessionID) == "" && h.deps.SessionQuery != nil {
		if row, err := h.deps.SessionQuery.Find(ctx, em.peer, em.peerSessionID); err == nil && row != nil && row.ProviderSessionID != "" {
			req.ProviderSessionID = row.ProviderSessionID
		}
	}

	// Protobuf 请求在 RunAck 写出后就会取消 ctx；真实 CLI 的 assistant/usage
	// 事件在 ACK 之后才持续到达，不能把一轮 turn 的寿命绑在这次请求上。
	// 保留账号、连接与日志等 value，由 runtime 的结果/Abort/daemon shutdown 收尾。
	runCtx := context.WithoutCancel(ctx)
	events, result, err := rt.Run(runCtx, req)
	if err != nil {
		return wire.RunAck{}, err
	}
	owner := &runtimeSession{backendType: bt}
	h.register(em.rid, owner)
	ack := wire.RunAck{SessionID: p.SessionID}
	if providerFallbackKey != "" {
		ack.ProviderFallbackKey = providerFallbackKey
	}
	if result != nil {
		ack.LaunchPermissionMode = result.LaunchPermissionMode
		if be.IsPiAgent() {
			ack.ProviderSessionID = result.ProviderSessionID
		}
	}
	// backendKey 是这条会话在 backend 那边的键(按对端隔离):claudecode / codex 的日志
	// 里报的 sessionID 是它,这一行是把两边对上号的唯一地方。
	logger.Ctx(ctx).Info("handlers.RuntimeHandlers.Run: session started",
		zap.Int64("sessionId", p.SessionID),
		zap.Int64("runtimeSessionId", em.rid),
		zap.String("backendType", be.Type),
		zap.Int64("agentId", p.AgentID),
		zap.String("peerFingerprint", runPeer),
		zap.Int("userTextBytes", len(p.UserText)))
	h.startSession(em, p, bt, providerSessionIDOf(result))
	go h.fanout(em, owner, events, result, userMsg) //nolint:gosec // G118: turn fanout outlives the Run RPC and owns terminal cleanup.
	// 真实 runtime 若支持自主续轮(claudecode),起每会话一个转发 goroutine 把
	// AutonomousTurns(sid) 推到 client。session 已 spawn,此刻订阅才拿得到 channel。
	if src, ok := rt.(agentruntime.AutonomousTurnSource); ok {
		h.startAutonomousFanout(em, src)
	}
	return ack, nil
}

func (h *RuntimeHandlers) registerPiGeneration(
	ctx context.Context,
	em *sessionEmitter,
	p wire.RunParams,
	be *agent_backend_entity.AgentBackend,
) (wire.RunAck, error) {
	generationToken := strings.TrimSpace(p.PermissionMode)
	if generationToken == "" {
		return wire.RunAck{}, errors.New("runtime.run: Pi generation owner is empty")
	}
	// The generation outlives this RPC response. Protobuf request contexts are
	// canceled as soon as the response frame is written, so retain values while
	// giving the generation its own lifecycle.
	generationCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	owner := &runtimeSession{
		backendType:     agent_backend_entity.TypePiAgent,
		ctx:             generationCtx,
		cancel:          cancel,
		connection:      connection.FromContext(ctx),
		generationToken: generationToken,
		terminalDone:    make(chan struct{}),
	}
	if !h.registerPiIfAbsent(em.rid, owner) {
		cancel()
		return wire.RunAck{}, errors.New("runtime.run: session already has an active generation")
	}
	logger.Ctx(ctx).Debug("handlers.RuntimeHandlers.Run: Pi generation registered",
		zap.Int64("sessionId", p.SessionID),
		zap.String("backendType", be.Type),
		zap.String("peerFingerprint", em.peer))
	return wire.RunAck{SessionID: p.SessionID}, nil
}

func (h *RuntimeHandlers) preparePi(
	em *sessionEmitter,
	p wire.RunParams,
	be *agent_backend_entity.AgentBackend,
	owner *runtimeSession,
	preparer piagentrt.RunPreparer,
	req agentruntime.RunRequest,
) (wire.RunAck, error) {
	h.mu.Lock()
	if h.sessions[em.rid] != owner || owner.backendType != agent_backend_entity.TypePiAgent ||
		owner.cancelRequested || owner.preparing || owner.starting || owner.started ||
		owner.prepared != nil || owner.finalizing {
		h.mu.Unlock()
		return wire.RunAck{}, errors.New("runtime.run: Pi preparation does not own the registered generation")
	}
	owner.preparing = true
	generationCtx := owner.ctx
	h.mu.Unlock()

	prepared, err := preparer.PrepareRun(generationCtx, req)
	if err != nil {
		h.mu.Lock()
		owner.preparing = false
		h.mu.Unlock()
		cleanupErr := h.finalizePiGeneration(context.Background(), em.rid, owner)
		return wire.RunAck{}, errors.Join(err, cleanupErr)
	}
	identity, hasIdentity := prepared.(piagentrt.PreparedRunIdentity)
	providerSessionID := ""
	if hasIdentity {
		providerSessionID = strings.TrimSpace(identity.ProviderSessionID())
	}

	h.mu.Lock()
	owner.preparing = false
	owner.prepared = prepared
	owner.providerSessionID = providerSessionID
	canceled := h.sessions[em.rid] != owner || owner.cancelRequested || owner.disconnected ||
		owner.finalizing || owner.ctx.Err() != nil
	h.mu.Unlock()

	var prepareErr error
	switch {
	case !hasIdentity:
		prepareErr = errors.New("runtime.run: prepared Pi generation has no pre-prompt identity")
	case providerSessionID == "":
		prepareErr = errors.New("runtime.run: prepared Pi generation returned empty provider session id")
	case canceled:
		prepareErr = context.Canceled
	}
	if prepareErr != nil {
		cleanupErr := h.finalizePiGeneration(context.Background(), em.rid, owner)
		return wire.RunAck{}, errors.Join(prepareErr, cleanupErr)
	}
	logger.Ctx(em.ctx).Debug("handlers.RuntimeHandlers.Run: Pi generation prepared",
		zap.Int64("sessionId", p.SessionID),
		zap.String("backendType", be.Type),
		zap.String("providerSessionId", providerSessionID),
		zap.String("peerFingerprint", em.peer))
	return wire.RunAck{SessionID: p.SessionID, ProviderSessionID: providerSessionID}, nil
}

func (h *RuntimeHandlers) startPreparedPi(
	em *sessionEmitter,
	p wire.RunParams,
	be *agent_backend_entity.AgentBackend,
	owner *runtimeSession,
	bt agent_backend_entity.BackendType,
	providerFallbackKey string,
) (wire.RunAck, error) {
	h.mu.Lock()
	providerSessionID := owner.providerSessionID
	prepared := owner.prepared
	if h.sessions[em.rid] != owner || owner.backendType != agent_backend_entity.TypePiAgent ||
		owner.cancelRequested || owner.starting || owner.started || owner.finalizing || prepared == nil ||
		strings.TrimSpace(p.ProviderSessionID) != providerSessionID {
		h.mu.Unlock()
		return wire.RunAck{}, errors.New("runtime.run: Pi start does not own the prepared generation")
	}
	owner.starting = true
	h.mu.Unlock()

	events, result, err := prepared.Start(owner.ctx)
	h.mu.Lock()
	owner.starting = false
	canceled := h.sessions[em.rid] != owner || owner.cancelRequested || owner.disconnected ||
		owner.finalizing || owner.ctx.Err() != nil
	if err == nil && !canceled {
		owner.started = true
	}
	h.mu.Unlock()
	if err != nil {
		cleanupErr := h.finalizePiGeneration(context.Background(), em.rid, owner)
		if cleanupErr != nil {
			logger.Ctx(em.ctx).Warn("handlers.RuntimeHandlers.Run: generation close failed",
				zap.Int64("sessionId", p.SessionID),
				zap.String("backendType", be.Type),
				zap.String("peerFingerprint", em.peer),
				zap.String("errorType", fmt.Sprintf("%T", cleanupErr)),
				zap.Error(cleanupErr))
		}
		return wire.RunAck{}, errors.Join(err, cleanupErr)
	}
	if canceled {
		if events != nil {
			go drainRuntimeEvents(events)
		}
		cleanupErr := h.finalizePiGeneration(context.Background(), em.rid, owner)
		return wire.RunAck{}, errors.Join(context.Canceled, cleanupErr)
	}
	ack := wire.RunAck{SessionID: p.SessionID, ProviderSessionID: providerSessionID}
	if providerFallbackKey != "" {
		ack.ProviderFallbackKey = providerFallbackKey
	}
	if result != nil {
		ack.LaunchPermissionMode = result.LaunchPermissionMode
		if strings.TrimSpace(result.ProviderSessionID) != "" {
			ack.ProviderSessionID = strings.TrimSpace(result.ProviderSessionID)
		}
	}
	logger.Ctx(em.ctx).Info("handlers.RuntimeHandlers.Run: Pi generation started",
		zap.Int64("sessionId", p.SessionID),
		zap.Int64("runtimeSessionId", em.rid),
		zap.String("backendType", be.Type),
		zap.String("peerFingerprint", em.peer))
	h.startSession(em, p, bt, providerSessionID)
	go h.fanout(em, owner, events, result, userMessageFor(p))
	return ack, nil
}

func drainRuntimeEvents(events <-chan agentruntime.Event) {
	for range events { //nolint:revive // draining an abandoned generation's events
	}
}

// ── 会话生命周期(running → idle → running,重启 → interrupted)─────────────
//
// 落的那一行是重连客户端拿会话清单的唯一来源,也是 R10 启动清扫的对象。写失败只记
// 日志、不影响这一轮执行:一轮已经在跑了,拿它陪葬换不回任何东西,代价是这条会话在
// 清单里缺席直到下一轮重新建行。

// startSession 在一轮起手时建行并置 running。providerSessionID 是 daemon 这一轮从
// result 收回的 provider 原生会话身份(决策 8):首轮新建后落库,后续轮续用;空串 =
// runtime 还没给出身份(这一轮就是新建)。标题与 Agent 同步标识(R7)随 p 携带、幂等覆盖。
func (h *RuntimeHandlers) startSession(em *sessionEmitter, p wire.RunParams, bt agent_backend_entity.BackendType, providerSessionID string) {
	err := h.deps.Sessions.Start(em.ctx, SessionRecord{
		PeerFingerprint:   em.peer,
		PeerSessionID:     em.peerSessionID,
		AgentID:           p.AgentID,
		Cwd:               p.Cwd,
		BackendType:       string(bt),
		LifecycleState:    wire.SessionLifecycleRunning,
		Title:             p.Title,
		AgentSyncID:       p.AgentSyncID,
		ProjectSyncID:     p.ProjectSyncID,
		ProviderSessionID: providerSessionID,
	})
	if err != nil {
		logger.Ctx(em.ctx).Error("handlers.RuntimeHandlers.startSession: record session failed",
			zap.Int64("sessionId", em.sid),
			zap.String("peerFingerprint", em.peer),
			zap.String("backendType", string(bt)),
			zap.Int64("agentId", p.AgentID),
			zap.Error(err))
	}
}

// providerSessionIDOf 取这一轮 result 里 runtime 确认的 provider 原生会话身份;result
// 为 nil(直连路径 run 失败前)时回空串。
func providerSessionIDOf(result *agentruntime.RunResult) string {
	if result == nil {
		return ""
	}
	return strings.TrimSpace(result.ProviderSessionID)
}

// runningSession 把会话推回 running(自主续轮开始)。
func (h *RuntimeHandlers) runningSession(em *sessionEmitter) {
	if err := h.deps.Sessions.Running(em.ctx, em.peer, em.peerSessionID); err != nil {
		logger.Ctx(em.ctx).Error("handlers.RuntimeHandlers.runningSession: mark session running failed",
			zap.Int64("sessionId", em.sid),
			zap.String("peerFingerprint", em.peer),
			zap.Error(err))
	}
}

// finishSession 把会话落回 idle(一轮结束,等下一轮)。
func (h *RuntimeHandlers) finishSession(em *sessionEmitter) {
	if err := h.deps.Sessions.Finish(em.ctx, em.peer, em.peerSessionID); err != nil {
		logger.Ctx(em.ctx).Error("handlers.RuntimeHandlers.finishSession: mark session idle failed",
			zap.Int64("sessionId", em.sid),
			zap.String("peerFingerprint", em.peer),
			zap.Error(err))
	}
}

// userMessageFor 从 RunParams 推出「开新一轮」的发起方标记(R18):发起方声明了设备身份
// (SourceDevice 非空)且有用户文本时返回标记,否则 nil。桌面端自己发消息不传 SourceDevice,
// 返回 nil 即事件流与今天逐帧一致。
func userMessageFor(p wire.RunParams) *agentruntime.UserMessageEvent {
	if text := strings.TrimSpace(p.UserText); text != "" && p.SourceDevice != "" {
		return &agentruntime.UserMessageEvent{
			Text:             text,
			SourceDevice:     p.SourceDevice,
			SourceDeviceName: p.SourceDeviceName,
		}
	}
	return nil
}

// emitPrelude 把发起方标记(UserMessageEvent)按与事件流同一条纪律发出:marshal →
// 判 generation 是否仍归本属主(stale 丢弃,与循环里一致)→ em.emit 落库 + 推送。
// 返回是否真的作为一条事件发出。
func (h *RuntimeHandlers) emitPrelude(em *sessionEmitter, owner *runtimeSession, rid int64, prelude *agentruntime.UserMessageEvent) bool {
	current := h.isCurrent(rid, owner)
	if owner.backendType == agent_backend_entity.TypePiAgent && owner.ctx != nil {
		current = h.canDeliverPiEvent(rid, owner)
	}
	if !current {
		logger.Ctx(em.ctx).Debug("handlers.RuntimeHandlers.emitPrelude: stale prelude dropped",
			zap.Int64("sessionId", em.sid),
			zap.String("peerFingerprint", em.peer))
		return false
	}
	return em.emit(wire.NotifyEvent, &wire.EventFrame{
		SessionID: em.sid,
		Event:     *prelude,
	})
}

// fanout 把 backend events channel 抽干推到 runtime.event,channel close 后再发
// runtime.runResultDone 终态帧。日志按事件 kind 计数,turn 结束时打一条汇总,
// 排查 stuck-turn / 漏事件时方便对账 client 端实际收到几条。
func (h *RuntimeHandlers) fanout(em *sessionEmitter, owner *runtimeSession, ch <-chan agentruntime.Event, result *agentruntime.RunResult, prelude *agentruntime.UserMessageEvent) {
	startedAt := time.Now()
	sid, rid := em.sid, em.rid
	count := 0
	kindHist := map[string]int{}
	// R18:把发起方标记作为**第一条**事件注入,保证订阅者先把这一轮的用户消息落成转录行,
	// 再接收后端真正的事件。
	if prelude != nil {
		if h.emitPrelude(em, owner, rid, prelude) {
			count++
			kindHist["UserMessage"]++
		}
	}
	for ev := range ch {
		// R17:SteerConsumed 里的每条 steer 都带着它的提交方来源 —— 实时消费路径
		// 在这里把 Steer RPC 时记下的对端盖回去(轮末残留的走 DrainPending 同表消费)。
		// 盖在**密封事件内部**:远端 runtime 把 EventFrame 原样传递、会丢外层字段。
		if sc, ok := ev.(agentruntime.SteerConsumed); ok {
			ev = stampSteerSources(h.deps.SteerSource, sc)
		}
		count++
		kind := reflect.TypeOf(ev).Name()
		kindHist[kind]++
		current := h.isCurrent(rid, owner)
		if owner.backendType == agent_backend_entity.TypePiAgent && owner.ctx != nil {
			current = h.canDeliverPiEvent(rid, owner)
		}
		if !current {
			logger.Ctx(em.ctx).Debug("handlers.RuntimeHandlers.fanout: stale generation dropped",
				zap.Int64("sessionId", sid),
				zap.String("peerFingerprint", em.peer),
				zap.Int("eventNumber", count),
				zap.String("eventKind", kind))
			continue
		}
		if em.emit(wire.NotifyEvent, &wire.EventFrame{
			SessionID: sid,
			Event:     ev,
		}) && !isNoisyEventKind(kind) {
			// text/thinking/usage 频率极高,kindHist 汇总即可,不逐条 log。
			logger.Ctx(em.ctx).Debug("handlers.RuntimeHandlers.fanout: event delivered",
				zap.Int64("sessionId", sid),
				zap.String("peerFingerprint", em.peer),
				zap.Int("eventNumber", count),
				zap.String("eventKind", kind))
		}
	}
	frame := runResultToFrame(sid, result)
	if owner.backendType != agent_backend_entity.TypePiAgent || owner.ctx == nil {
		// 生命周期落回 idle 必须在终态帧**之前**:终态帧是客户端得知这一轮结束的那一刻,
		// 它随后立刻查清单时必须已经看到 idle,而不是一个正在收尾的 running。
		//
		// 也必须在摘表**之前**:反过来的话,两者之间(Finish 是一次同步的 SQLite 写,与流式
		// 落库抢锁时能拖到几十毫秒以上)落进来的决策提交会看到「内存表里没有 + 行还在跑」,
		// 被 idempotentSubmitResult 判成真错误,给用户一个假失败。这个顺序下它解得出会话、
		// 照旧走到 backend,由「waiter 已经不在了」按 R8 折成成功。
		h.finishSession(em)
		// 只清 active-turn 记录;**不撤销 gateway token** —— token 是会话级常驻,
		// 跨轮复用,寿命跟随子进程(见 sessionTokens 注释),轮末撤销会让下一轮复用
		// 的子进程手里 token 失效。
		removed := h.unregister(rid, owner)
		em.emit(wire.NotifyRunResultDone, &frame)
		h.logFanoutSummary(em, owner, startedAt, removed, count, kindHist, frame)
		return
	}

	// Pi terminal delivery is claimed by the exact in-memory owner while it is
	// still registered. Abort may request cancellation before this claim, but an
	// acknowledged turn still owns its one terminal result. Disconnect and stale
	// owners cannot claim delivery. Finalization runs only after the terminal
	// frame is emitted, so Abort cannot permit a retry before it is settled.
	current := h.claimPiTerminal(rid, owner)
	if current {
		h.finishSession(em)
		em.emit(wire.NotifyRunResultDone, &frame)
		if cleanupErr := h.finalizePiGeneration(context.Background(), rid, owner); cleanupErr != nil {
			logger.Ctx(em.ctx).Error("handlers.RuntimeHandlers.fanout: generation finalization failed",
				zap.Int64("sessionId", sid),
				zap.String("peerFingerprint", em.peer),
				zap.String("errorType", fmt.Sprintf("%T", cleanupErr)),
				zap.Error(cleanupErr))
		}
	}
	h.logFanoutSummary(em, owner, startedAt, current, count, kindHist, frame)
}

func (h *RuntimeHandlers) logFanoutSummary(em *sessionEmitter, owner *runtimeSession, startedAt time.Time, current bool, count int, kindHist map[string]int, frame wire.RunResultDoneFrame) {
	logger.Ctx(em.ctx).Info("handlers.RuntimeHandlers.fanout: session ended",
		zap.Int64("sessionId", em.sid),
		zap.Int64("runtimeSessionId", em.rid),
		zap.String("peerFingerprint", em.peer),
		zap.String("backendType", string(owner.backendType)),
		zap.Bool("currentGeneration", current),
		zap.Int("totalEvents", count),
		zap.Any("eventKinds", kindHist),
		zap.Bool("hasStopError", frame.StopErrMsg != ""),
		zap.Int("stopErrorBytes", len(frame.StopErrMsg)),
		zap.Int("stopErrorCode", frame.StopErrCode),
		zap.Duration("duration", time.Since(startedAt)))
}

// stampSteerSources 把 Steer RPC 时记下的提交方来源盖回被消费的 steer 上(R17)。
// 每条 steer 按 QueuedID 取映射(取走即删);查不到的保持空(本机/未知)。返回新事件,
// 不改 backend 侧 slice。
func stampSteerSources(src SteerSourcePort, sc agentruntime.SteerConsumed) agentruntime.SteerConsumed {
	if len(sc.Steers) == 0 || src == nil {
		return sc
	}
	out := make([]agentruntime.ConsumedSteer, len(sc.Steers))
	copied := false
	for i, st := range sc.Steers {
		if entry, ok := src.Consume(st.QueuedID); ok && (entry.Peer != "" || entry.Name != "") {
			if !copied {
				copy(out, sc.Steers)
				copied = true
			}
			out[i].SourcePeer = entry.Peer
			out[i].SourceName = entry.Name
		}
	}
	if !copied {
		return sc
	}
	sc.Steers = out
	return sc
}

// startAutonomousFanout 每会话起一个 goroutine,把真实 runtime 的自主续轮转发到
// client(每轮:Started → Event* → Done)。去重防重复订阅;AutonomousTurns(sid)
// channel close(子进程 evict)时 goroutine 退出并清去重位。
func (h *RuntimeHandlers) startAutonomousFanout(em *sessionEmitter, src agentruntime.AutonomousTurnSource) {
	// 订阅的是 backend 那边的会话键(按对端隔离);日志里报的仍是客户端自己那个 id。
	rid := em.rid
	if _, loaded := h.autoSubs.LoadOrStore(rid, struct{}{}); loaded {
		return
	}
	go func() {
		defer h.autoSubs.Delete(rid)
		for at := range src.AutonomousTurns(rid) {
			h.forwardAutonomousTurn(em, at)
		}
		log.Printf("runtime.autonomousTurn: source closed sid=%d", em.sid)
	}()
}

// forwardAutonomousTurn 转发一轮自主续轮:先 Started,再逐事件 Event,最后 Done
// 带 RunResult(复用 runResultToFrame)。语义同 fanout,但走 autonomousTurn.* 方法。
//
// 生命周期同 fanout 一样两端推进:自主续轮同样是「一轮执行中」,不这么做的话一条正在
// 产出事件的会话会在清单里显示成闲置。
func (h *RuntimeHandlers) forwardAutonomousTurn(em *sessionEmitter, at agentruntime.AutonomousTurn) {
	sid := em.sid
	h.runningSession(em)
	em.emit(wire.NotifyAutonomousTurnStarted, &wire.AutonomousTurnStartedFrame{
		SessionID: sid,
		Trigger:   at.Trigger,
		TurnToken: at.TurnToken,
	})
	count := 0
	for ev := range at.Events {
		count++
		em.emit(wire.NotifyAutonomousTurnEvent, &wire.EventFrame{
			SessionID: sid,
			Event:     ev,
		})
	}
	frame := runResultToFrame(sid, at.Result)
	h.finishSession(em) // 同 fanout:先落回 idle,再发终态帧
	em.emit(wire.NotifyAutonomousTurnDone, &frame)
	log.Printf("runtime.autonomousTurn: forwarded sid=%d trigger=%s events=%d hasStopErr=%t stopErrBytes=%d stopErrCode=%d",
		sid, at.Trigger, count, frame.StopErrMsg != "", len(frame.StopErrMsg), frame.StopErrCode)
}

// ── 会话通知出口(先落库,后推送)────────────────────────────────────────────

// seqFrame 是能被盖上 seq 的通知帧。wire 的三个通知帧(EventFrame /
// RunResultDoneFrame / AutonomousTurnStartedFrame)的指针都满足它。
// 按 ISP 在消费方声明,wire 那边只留三个 SetSeq 方法。
type seqFrame interface {
	SetSeq(seq int64)
}

// sessionEmitter 是某个 (对端, 会话) 的通知出口:一条通知先落进 daemon 的通知日志拿到
// seq,落库成功后才盖上 seq 推给此刻活着的那条连接。
//
// 它按**会话**构造(而不是按连接)有两个原因:
//   - 对端指纹只在 runtime.run 的请求 ctx 上拿得到,而 fanout / forwardAutonomousTurn
//     跑在脱离请求 ctx 的 goroutine 里,只能在 run 期间捕获后随会话带下去;
//   - RuntimeHandlers 自己是 per-connection 构造的,把推送目标静态捕获进来会让客户端
//     重连之后的通知一直发往那条死连接(见 daemon.bindConn 注释),所以推送目标每次
//     发送时才解析。
type sessionEmitter struct {
	// ctx 派生自 runtime.run 的请求 ctx 但去掉了取消:落库要活过发起它的那次请求
	// (fanout 的寿命是整轮执行),但 ctx 上的值(daemon 自己的 db 句柄)必须留着。
	ctx           context.Context
	journal       JournalPort
	notifyFor     func(peerFingerprint string) NotifierPort
	peer          string
	peerSessionID string
	// sid 是客户端自己那个会话 id:落库的会话身份、以及推给客户端的每一帧带的都是它。
	sid int64
	// rid 是同一条会话在 backend runtime 那边的会话键(按对端隔离,见
	// runtimeSessionID)。fanout / 自主续轮订阅一律用它,它永远不过线。
	rid int64
}

// newEmitterFor 在 runtime.run 处理期间构造会话通知出口,并据会话归属定下这一轮的
// backend 会话键。归属由调用方给定 —— runtime.run 用它把一轮落在**点名的 origin**
// 名下(R9),而不是调用方自己名下那条同号会话;省略 origin 时调用方传
// peerFingerprint(ctx),即「调用方自己的对端」。
func (h *RuntimeHandlers) newEmitterFor(ctx context.Context, sid int64, peer string) *sessionEmitter {
	return &sessionEmitter{
		ctx:           context.WithoutCancel(ctx),
		journal:       h.deps.Journal,
		notifyFor:     h.deps.NotifyFor,
		peer:          peer,
		peerSessionID: strconv.FormatInt(sid, 10),
		sid:           sid,
		rid:           runtimeSessionID(peer, sid),
	}
}

// emit 先落库、后推送一条会话通知,返回是否真的推出去了(只给调用方决定要不要打
// 成功日志)。三条硬规则:
//   - 落库成功之后才推,推出去的帧带着库分配的 seq(R1 / R6);
//   - 落库失败:不推、seq 不推进,记 error 日志 —— 日志里因此不会出现空洞,客户端拉到
//     的连续 seq 就是完整序列(R3);
//   - 推送失败:通知已经落库、seq 已经推进,记一条日志就继续下一条,不回滚不重试(R2)。
func (e *sessionEmitter) emit(method string, frame seqFrame) bool {
	notification, err := protowire.WireNotificationToProto(method, frame)
	if err != nil {
		logger.Ctx(e.ctx).Error("handlers.sessionEmitter.emit: protobuf conversion failed",
			zap.Int64("sessionId", e.sid),
			zap.String("peerFingerprint", e.peer),
			zap.String("notificationMethod", method),
			zap.Error(err))
		return false
	}
	if e.journal == nil {
		// 没接日志就没有「事实」可言,只能连推送一起停:宁可整条出口静默失败被一眼看见,
		// 也不能一边推一边丢事实(那样断连补齐会缺条,而没人会发现)。
		logger.Ctx(e.ctx).Error("handlers.sessionEmitter.emit: journal not wired",
			zap.Int64("sessionId", e.sid),
			zap.String("peerFingerprint", e.peer),
			zap.String("notificationMethod", method))
		return false
	}
	protowire.SetNotificationSeq(notification, 0)
	encoded, err := protowire.EncodeNotification(notification)
	if err != nil {
		logger.Ctx(e.ctx).Error("handlers.sessionEmitter.emit: protobuf encode failed",
			zap.Int64("sessionId", e.sid),
			zap.String("peerFingerprint", e.peer),
			zap.String("notificationMethod", method),
			zap.Error(err))
		return false
	}
	seq, err := e.journal.Append(e.ctx, e.peer, e.peerSessionID, encoded)
	if err != nil {
		logger.Ctx(e.ctx).Error("handlers.sessionEmitter.emit: journal append failed",
			zap.Int64("sessionId", e.sid),
			zap.String("peerFingerprint", e.peer),
			zap.String("notificationMethod", method),
			zap.Int("payloadBytes", len(encoded)),
			zap.Error(err))
		return false
	}
	frame.SetSeq(seq)
	// 推出去的是**刚才落库的那一条消息本身**,只多盖一个 seq —— 不重新转换一遍(转换要
	// 对密封事件做一次 JSON 解码并重建整棵消息树,而这里跑的是每一个 token)。落库的
	// 字节里 seq 仍是 0:它是日志行自己的属性,断连补齐时由行的 seq 列重新盖上。
	protowire.SetNotificationSeq(notification, seq)
	n := e.pushTarget()
	if n == nil {
		// 对端不在线:通知已经落库,等它重连后按游标补齐。
		logger.Ctx(e.ctx).Debug("handlers.sessionEmitter.emit: notification journaled without live peer",
			zap.Int64("sessionId", e.sid),
			zap.String("peerFingerprint", e.peer),
			zap.String("notificationMethod", method),
			zap.Int64("seq", seq),
			zap.Int("payloadBytes", len(encoded)))
		return false
	}
	if err := n.Notify(notification); err != nil {
		logger.Ctx(e.ctx).Warn("handlers.sessionEmitter.emit: notification push failed after journaling",
			zap.Int64("sessionId", e.sid),
			zap.String("peerFingerprint", e.peer),
			zap.String("notificationMethod", method),
			zap.Int64("seq", seq),
			zap.Int("payloadBytes", len(encoded)),
			zap.Error(err))
		return false
	}
	if method != wire.NotifyEvent && method != wire.NotifyAutonomousTurnEvent {
		logger.Ctx(e.ctx).Debug("handlers.sessionEmitter.emit: notification journaled and pushed",
			zap.Int64("sessionId", e.sid),
			zap.String("peerFingerprint", e.peer),
			zap.String("notificationMethod", method),
			zap.Int64("seq", seq),
			zap.Int("payloadBytes", len(encoded)))
	}
	return true
}

// pushTarget 解析此刻的推送目标;对端不在线返回 nil。
func (e *sessionEmitter) pushTarget() NotifierPort {
	if e.notifyFor == nil {
		return nil
	}
	return e.notifyFor(e.peer)
}

// peerFingerprint 取发起这轮的对端设备指纹 —— 会话身份的前半段(R16)。它只在请求
// ctx 上有(auth.pair / auth.connect 成功后写进 rpcerror.AuthState),所以必须在 runtime.run
// 处理期间取,fanout 的 goroutine 里已经拿不到连接了。
func peerFingerprint(ctx context.Context) string {
	if c := connection.FromContext(ctx); c != nil {
		return c.Auth().DeviceFingerprint
	}
	return ""
}

// peerName 取发起这条 RPC 的对端设备名(auth.pair 时上报;auth.account 路径为空)。
// 与 peerFingerprint 一样只在请求 ctx 上有,必须在 RPC 处理期间取。
func peerName(ctx context.Context) string {
	if c := connection.FromContext(ctx); c != nil {
		return c.Auth().DeviceName
	}
	return ""
}

// ── backend 会话键(按对端隔离)──────────────────────────────────────────────

// runtimeSessionID 把「客户端报的会话 id」翻成本 daemon 进程内唯一的 backend 会话键。
//
// 会话 id 是各客户端本地自增的主键:两台设备各自的 42 号会话是两条毫不相干的会话。而
// backend runtime 的会话表是**进程内一份、只按这个数字索引**的(claudecode 的
// sessionKey(id)、codex 的 r.active[sessionID]),把裸 id 交给它,两个对端的同号会话就
// 并成了一条 —— 待决策是同一批,子进程也是同一个。表现出来就是一条跨对端的信息泄漏:
// 一台设备读得到另一台的 requestID / 工具名 / 完整工具入参,还能照着那个 requestID 替
// 对方提交审批。
//
// 所以 daemon 在**调用 backend 的那一刻**把对端指纹揉进会话键。翻译只发生在这一处:
// 落库的会话身份仍是 (对端指纹, 客户端会话 id),推给客户端的每一帧带的也仍是客户端
// 自己那个 id —— 客户端不知道、也不需要知道这层翻译,协议一字未改。桌面端进程内不做这层
// 翻译:那里只有一个「对端」(本机用户),会话 id 本来就唯一。
//
// 两种情况原样返回:没有对端(未鉴权的直连单测)时没有可隔离的第二方;非正数会话 id
// 要留给 backend 自己那条 "invalid sessionID" 校验去拒。
func runtimeSessionID(peer string, sessionID int64) int64 {
	if peer == "" || sessionID <= 0 {
		return sessionID
	}
	sum := fnv.New64a()
	_, _ = sum.Write([]byte(peer))
	_, _ = sum.Write([]byte{0})
	_, _ = sum.Write([]byte(strconv.FormatInt(sessionID, 10)))
	// 右移一位清掉符号位:backend 一律拒绝非正数会话 id。0 让位给 1 —— 会话键只要求
	// 唯一,不要求可逆。
	if v := int64(sum.Sum64() >> 1); v > 0 {
		return v
	}
	return 1
}

// isNoisyEventKind 标记单 turn 内可能上百次出现的事件类型,逐条 log 会刷屏。
// 它们仍计入 fanout 汇总(kindHist),只是不展开。
func isNoisyEventKind(kind string) bool {
	switch kind {
	case "TextDelta", "ThinkingDelta", "OutputActivity", "UsageUpdate", "ContextWindowUpdated":
		return true
	}
	return false
}

func runResultToFrame(sid int64, r *agentruntime.RunResult) wire.RunResultDoneFrame {
	if r == nil {
		return wire.RunResultDoneFrame{SessionID: sid}
	}
	f := wire.RunResultDoneFrame{
		SessionID:         sid,
		ProviderSessionID: r.ProviderSessionID,
		UserAnchor:        r.UserAnchor,
		Model:             r.Model,
		ContextWindow:     r.ContextWindow,
		TurnToken:         r.TurnToken,
	}
	if r.Usage != nil {
		f.Usage = &wire.UsageWire{
			PromptTokens:        r.Usage.PromptTokens,
			CompletionTokens:    r.Usage.CompletionTokens,
			ReasoningTokens:     r.Usage.ReasoningTokens,
			CachedTokens:        r.Usage.CachedTokens,
			CacheCreationTokens: r.Usage.CacheCreationTokens,
			TotalTokens:         r.Usage.TotalTokens,
		}
	}
	if r.StopErr != nil {
		f.StopErrMsg = r.StopErr.Error()
		if rpcErr := wire.ToRPCError(r.StopErr); rpcErr != nil {
			f.StopErrCode = int(rpcErr.Code)
		}
	}
	return f
}

// resolveSessionCapability 解出该会话的 backend 能力,并**一并交回要用来调用它的那个
// 会话键**(按对端隔离,见 runtimeSessionID)。两样东西一起返回是有意的:控制 RPC 全都
// 「先解会话,再调 backend」,分两次各取一次就有机会解的是隔离键、调的却是客户端裸 id。
func resolveSessionCapability[T any](ctx context.Context, h *RuntimeHandlers, sessionID int64, originPeer string) (T, int64, error) {
	var zero T
	peer, err := ResolveSessionPeer(ctx, originPeer, h.deps.ClaimedAccountID)
	if err != nil {
		return zero, 0, err
	}
	rid := runtimeSessionID(peer, sessionID)
	rt, err := h.resolveSession(rid)
	if err != nil {
		return zero, rid, err
	}
	capability, ok := any(rt).(T)
	if !ok {
		return zero, rid, agentruntime.ErrUnsupported
	}
	return capability, rid, nil
}

// ── Control RPCs (Steer / CancelSteer / DrainPending / Abort / SetPM /
//                  SubmitAnswer / SubmitToolPermission) ─────────────────────

func (h *RuntimeHandlers) Steer(ctx context.Context, p wire.SteerParams) (wire.OK, error) {
	s, rid, err := resolveSessionCapability[agentruntime.Steerer](ctx, h, p.SessionID, p.PeerFingerprint)
	if err != nil {
		return wire.OK{}, err
	}
	if err := s.Steer(ctx, rid, p.QueuedID, p.Text); err != nil {
		return wire.OK{}, err
	}
	// R17:记下这条 steer 的**提交方**(调用连接自己的对端 —— 他端接管别人的会话时,
	// 提交方 ≠ 会话发起方,而来源标识要标的是「谁发的」)。等 backend 把这条 steer
	// 消费掉、SteerConsumed 事件经 fanout 流出时,盖回 ConsumedSteer.SourcePeer。
	if p.QueuedID != "" {
		h.deps.SteerSource.Record(p.QueuedID, SteerSourceEntry{
			Peer: peerFingerprint(ctx),
			Name: peerName(ctx),
		})
	}
	return wire.OK{}, nil
}

func (h *RuntimeHandlers) CancelSteer(ctx context.Context, p wire.CancelSteerParams) (wire.CancelSteerResult, error) {
	c, rid, err := resolveSessionCapability[agentruntime.SteerCanceler](ctx, h, p.SessionID, p.PeerFingerprint)
	if err != nil {
		return wire.CancelSteerResult{}, err
	}
	removed, err := c.CancelSteer(ctx, rid, p.QueuedID)
	if err != nil {
		return wire.CancelSteerResult{}, err
	}
	// 被撤回的 steer 不会再被消费,清掉它的来源映射避免无界增长。
	h.deps.SteerSource.Forget(p.QueuedID)
	for _, id := range removed {
		h.deps.SteerSource.Forget(id)
	}
	return wire.CancelSteerResult{Removed: removed}, nil
}

func (h *RuntimeHandlers) DrainPending(ctx context.Context, p wire.DrainParams) (wire.DrainResult, error) {
	d, rid, err := resolveSessionCapability[agentruntime.SteerDrainer](ctx, h, p.SessionID, p.PeerFingerprint)
	if err != nil {
		return wire.DrainResult{}, err
	}
	steers := d.DrainPending(ctx, rid)
	// R17:轮末残留的 pending steer 同样带来源 —— 它们和实时消费的 SteerConsumed 走
	// 同一个 SteerInbox,QueuedID 对得上同一张映射表。
	for i := range steers {
		if entry, ok := h.deps.SteerSource.Consume(steers[i].QueuedID); ok {
			steers[i].SourcePeer = entry.Peer
			steers[i].SourceName = entry.Name
		}
	}
	return wire.DrainResult{Steers: steers}, nil
}

func (h *RuntimeHandlers) Abort(ctx context.Context, p wire.AbortParams) (wire.AbortResult, error) {
	// 会话键按对端隔离,而这里要处理的可能是他端(账号)接管后的会话:先解析
	// 提交方对端(省略 = 调用方自己),再按隔离后的键查本 handler 的内存会话表。
	peer, err := ResolveSessionPeer(ctx, p.PeerFingerprint, h.deps.ClaimedAccountID)
	if err != nil {
		return wire.AbortResult{}, err
	}
	rid := runtimeSessionID(peer, p.SessionID)
	h.mu.Lock()
	owner := h.sessions[rid]
	if owner == nil {
		h.mu.Unlock()
		return wire.AbortResult{}, agentruntime.ErrNoActiveTurn
	}
	if owner.backendType == agent_backend_entity.TypePiAgent && owner.ctx != nil {
		if owner.terminalClaimed || owner.finalizing || owner.aborting {
			h.mu.Unlock()
			return wire.AbortResult{}, h.waitPiFinalization(ctx, owner)
		}
		owner.cancelRequested = true
		owner.aborting = true
		preparing := owner.preparing
		starting := owner.starting
		accepted := owner.started
		h.mu.Unlock()

		if owner.cancel != nil {
			owner.cancel()
		}
		if preparing || starting {
			return wire.AbortResult{}, h.waitPiFinalization(ctx, owner)
		}
		if !accepted {
			return wire.AbortResult{}, h.finalizePiGeneration(ctx, rid, owner)
		}

		// An acknowledged prompt owns one terminal settlement. Runtime Abort is
		// invoked without h.mu; fanout may claim delivery concurrently and, if it
		// does, this RPC waits only for the bounded exact-owner finalization.
		var abortErr error
		if aborter, ok := h.lookupRuntimeByType(owner.backendType).(agentruntime.Aborter); ok {
			_, abortErr = aborter.Abort(ctx, rid, p.TurnToken)
			if errors.Is(abortErr, agentruntime.ErrNoActiveTurn) {
				abortErr = nil
			}
		}
		if abortErr != nil {
			cleanupErr := h.finalizePiGeneration(ctx, rid, owner)
			return wire.AbortResult{}, errors.Join(abortErr, cleanupErr)
		}
		waitErr := h.waitPiFinalization(ctx, owner)
		if waitErr == nil {
			return wire.AbortResult{}, nil
		}
		h.mu.RLock()
		terminalOwned := owner.terminalClaimed || owner.finalizing
		h.mu.RUnlock()
		if terminalOwned {
			return wire.AbortResult{}, waitErr
		}
		cleanupErr := h.finalizePiGeneration(ctx, rid, owner)
		return wire.AbortResult{}, errors.Join(waitErr, cleanupErr)
	}
	h.mu.Unlock()
	a, rid, err := resolveSessionCapability[agentruntime.Aborter](ctx, h, p.SessionID, p.PeerFingerprint)
	if err != nil {
		return wire.AbortResult{}, err
	}
	outcome, err := a.Abort(ctx, rid, p.TurnToken)
	if err != nil {
		return wire.AbortResult{}, err
	}
	return wire.AbortResult{TurnKind: outcome.TurnKind}, nil
}

func (h *RuntimeHandlers) StopBackgroundTask(ctx context.Context, p wire.StopBackgroundTaskParams) (wire.OK, error) {
	s, rid, err := resolveSessionCapability[agentruntime.BackgroundTaskStopper](ctx, h, p.SessionID, p.PeerFingerprint)
	if err != nil {
		return wire.OK{}, err
	}
	if err := s.StopBackgroundTask(ctx, rid, p.TaskID); err != nil {
		return wire.OK{}, err
	}
	return wire.OK{}, nil
}

func (h *RuntimeHandlers) SetPermissionMode(ctx context.Context, p wire.SetPermissionModeParams) (wire.OK, error) {
	m, rid, err := resolveSessionCapability[agentruntime.PermissionModeSetter](ctx, h, p.SessionID, p.PeerFingerprint)
	if err != nil {
		return wire.OK{}, err
	}
	if err := m.SetPermissionMode(ctx, rid, p.Mode); err != nil {
		return wire.OK{}, err
	}
	return wire.OK{}, nil
}

func (h *RuntimeHandlers) SubmitAnswer(ctx context.Context, p wire.SubmitAnswerParams) (wire.OK, error) {
	s, rid, err := resolveSessionCapability[agentruntime.AskAnswerSink](ctx, h, p.SessionID, p.PeerFingerprint)
	if err != nil {
		return h.idempotentSubmitResult(ctx, p.SessionID, p.PeerFingerprint, err)
	}
	return h.idempotentSubmitResult(ctx, p.SessionID, p.PeerFingerprint,
		s.SubmitAnswer(ctx, rid, p.RequestID, p.Questions, p.Answers, p.Skipped))
}

func (h *RuntimeHandlers) SubmitToolPermission(ctx context.Context, p wire.SubmitToolPermissionParams) (wire.OK, error) {
	s, rid, err := resolveSessionCapability[agentruntime.ToolPermissionSink](ctx, h, p.SessionID, p.PeerFingerprint)
	if err != nil {
		return h.idempotentSubmitResult(ctx, p.SessionID, p.PeerFingerprint, err)
	}
	return h.idempotentSubmitResult(ctx, p.SessionID, p.PeerFingerprint,
		s.SubmitToolPermission(ctx, rid, p.RequestID, p.Allow, p.AlwaysAllowSession, p.DenyReason))
}

// idempotentSubmitResult folds "waiter no longer exists" errors into a
// success response (R8): the same requestID submitted twice (the second
// take-and-delete misses → ErrWaiterNotFound) and a session that is no
// longer live on this daemon (never started here, already ended, or marked
// interrupted after a daemon restart → ErrNoActiveTurn) both collapse to
// "nothing left to do here", not an error. A reconnected client cannot tell
// whether its previous submission already arrived, so surfacing an error
// here would make it misreport failure to the user. Every other error
// (capability genuinely unsupported, invalid input, a real I/O failure
// talking to the backend) is returned unchanged.
//
// errBackendUnwired 是刻意的例外:一台 backend 没接线的 daemon 会把**每一次**提交
// 都报成 OK,而没有任何 waiter 被回答 —— 叠加 R9 的不设过期,会话就此永久挂死,客户端
// 与运维两边都看不到任何异常。它虽然也是「这条会话此刻没有活的一轮」,但成因是接线
// 故障而不是 R8 描述的那两种正常情况。
//
// ErrNoActiveTurn 同理不能无条件折叠:registry 是全局一份,每条新接入的连接都把 13 个
// runtime.* 重新 Register 一遍(覆盖),所以只要桌面端的心跳 / 刷新那几条连接里任意一条
// 在会话开跑之后接入,提交就落到一个**从没拥有过这条会话**的 RuntimeHandlers 上、解不出
// 会话。它和「轮次真的结束了」共用同一个 sentinel,却是两件事:后者按 R8 折成成功,前者
// 必须如实报错,桌面端 callSession 才会重挂一次再重试(不报错 = 客户端把「已送达」报给
// 前端,而没有任何 waiter 被回答,叠加 R9 的不设过期 = 永久挂死)。
//
// 判别依据是 daemon 自己的会话生命周期行(sessionRunningHere):它是 Daemon 级的、
// 不随连接生灭,正好答得了内存会话表答不了的那个问题。
func (h *RuntimeHandlers) idempotentSubmitResult(ctx context.Context, sid int64, originPeer string, err error) (wire.OK, error) {
	if err == nil {
		return wire.OK{}, nil
	}
	if errors.Is(err, errBackendUnwired) {
		return wire.OK{}, err
	}
	if errors.Is(err, agentruntime.ErrWaiterNotFound) {
		return wire.OK{}, nil
	}
	if errors.Is(err, agentruntime.ErrNoActiveTurn) {
		if h.sessionRunningHere(ctx, sid, originPeer) {
			return wire.OK{}, err
		}
		return wire.OK{}, nil
	}
	return wire.OK{}, err
}

// sessionRunningHere 回答「这条会话此刻在本 daemon 上是不是还在跑一轮」。
//
// 只认 running:idle(那一轮已经结束)、interrupted(子进程随上一个 daemon 进程消亡,
// R10)、以及查无此行(从没在这台 daemon 上跑过,或属于别的对端 —— 会话 id 是各客户端
// 本地自增的、必然重号,所以查询一律带对端指纹,R16)都是 R8 说的「没什么可做的了」。
//
// 无判别依据时(没接查询出口 / 读不出来)一律回 false:只有能**证明**会话仍在跑时才
// 把错误抛给客户端,证不了就维持 R8 的幂等,不拿一个读不出来的库去换用户面前一个假失败。
func (h *RuntimeHandlers) sessionRunningHere(ctx context.Context, sid int64, originPeer string) bool {
	if h.deps.SessionQuery == nil {
		return false
	}
	peer, err := ResolveSessionPeer(ctx, originPeer, h.deps.ClaimedAccountID)
	if err != nil {
		return false
	}
	row, err := h.deps.SessionQuery.Find(ctx, peer, strconv.FormatInt(sid, 10))
	if err != nil {
		log.Printf("runtime.submit: read session lifecycle failed sid=%d peer=%q err=%v", sid, peer, err)
		return false
	}
	return row != nil && row.LifecycleState == wire.SessionLifecycleRunning
}

func (h *RuntimeHandlers) GetGoal(ctx context.Context, p wire.GoalParams) (wire.GoalResult, error) {
	g, req, release, err := h.resolveGoalController(ctx, p)
	if err != nil {
		return wire.GoalResult{}, err
	}
	defer release()
	goal, err := g.GetGoal(ctx, req)
	if err != nil {
		return wire.GoalResult{}, err
	}
	return wire.GoalResult{Goal: goal}, nil
}

func (h *RuntimeHandlers) SetGoal(ctx context.Context, p wire.GoalParams) (wire.GoalResult, error) {
	g, req, release, err := h.resolveGoalController(ctx, p)
	if err != nil {
		return wire.GoalResult{}, err
	}
	defer release()
	goal, err := g.SetGoal(ctx, req)
	if err != nil {
		return wire.GoalResult{}, err
	}
	return wire.GoalResult{Goal: goal}, nil
}

func (h *RuntimeHandlers) ClearGoal(ctx context.Context, p wire.GoalParams) (wire.GoalClearResult, error) {
	g, req, release, err := h.resolveGoalController(ctx, p)
	if err != nil {
		return wire.GoalClearResult{}, err
	}
	defer release()
	cleared, err := g.ClearGoal(ctx, req)
	if err != nil {
		return wire.GoalClearResult{}, err
	}
	return wire.GoalClearResult{Cleared: cleared}, nil
}

func (h *RuntimeHandlers) resolveGoalController(ctx context.Context, p wire.GoalParams) (agentruntime.GoalController, agentruntime.GoalRequest, func(), error) {
	req, err := goalRequestFromWire(p)
	if err != nil {
		return nil, agentruntime.GoalRequest{}, func() {}, err
	}
	// goal 也按会话键落到 backend 的会话表上(codex 的 goalSession 走的正是
	// r.active[sessionID] / sessionKey(sessionID)),所以同样要按对端隔离。
	peer, err := ResolveSessionPeer(ctx, p.PeerFingerprint, h.deps.ClaimedAccountID)
	if err != nil {
		return nil, agentruntime.GoalRequest{}, func() {}, err
	}
	req.SessionID = runtimeSessionID(peer, req.SessionID)
	if req.Backend != nil {
		release, err := h.hydrateGoalTarget(ctx, &req, p.LLMProviderKey, p.LLMModelKey)
		if err != nil {
			return nil, agentruntime.GoalRequest{}, func() {}, err
		}
		rt := h.lookupRuntimeByType(agent_backend_entity.BackendType(req.Backend.Type))
		if rt == nil {
			release()
			return nil, agentruntime.GoalRequest{}, func() {}, agentruntime.ErrNoActiveTurn
		}
		g, ok := rt.(agentruntime.GoalController)
		if !ok {
			release()
			return nil, agentruntime.GoalRequest{}, func() {}, agentruntime.ErrUnsupported
		}
		return g, req, release, nil
	}
	g, _, err := resolveSessionCapability[agentruntime.GoalController](ctx, h, p.SessionID, p.PeerFingerprint)
	if err != nil {
		return nil, agentruntime.GoalRequest{}, func() {}, err
	}
	return g, req, func() {}, nil
}

// hydrateGoalTarget 按 wire 的 effective target（决策 11：GoalParams 携带
// ProviderKey+ModelKey，与 Run 同形）从 daemon 自家目录解析 provider + 执行侧配置，
// 并签 gateway token。与 Run 走同一 resolveTarget：goal 与 turn 共用同一个 CLI
// 会话池，两边解析不一致会让启动期比对键反复翻转。
func (h *RuntimeHandlers) hydrateGoalTarget(
	ctx context.Context, req *agentruntime.GoalRequest, wireProviderKey, wireModelKey string,
) (func(), error) {
	release := func() {}
	if req.Backend == nil {
		return release, nil
	}
	provider, effective, _, err := h.resolveTarget(ctx, wireProviderKey, wireModelKey, req.Backend)
	if err != nil {
		return release, err
	}
	req.Provider = provider
	req.Effective = effective
	if req.Provider != nil && h.deps.Gateway != nil {
		req.GatewayURL = h.deps.Gateway.URL()
		if req.GatewayURL != "" {
			tok, err := h.deps.Gateway.IssueToken(ctx, req.Backend, time.Hour)
			if err != nil {
				return release, fmt.Errorf("gateway token: %w", err)
			}
			req.GatewayToken = tok
			release = func() { h.deps.Gateway.RevokeToken(tok) }
		}
	}
	return release, nil
}

func goalRequestFromWire(p wire.GoalParams) (agentruntime.GoalRequest, error) {
	var be *agent_backend_entity.AgentBackend
	if len(p.Backend) > 0 {
		var parsed agent_backend_entity.AgentBackend
		if err := json.Unmarshal(p.Backend, &parsed); err != nil {
			return agentruntime.GoalRequest{}, fmt.Errorf("parse backend: %w", err)
		}
		be = &parsed
	}
	return agentruntime.GoalRequest{
		SessionID:         p.SessionID,
		AgentID:           p.AgentID,
		ProviderSessionID: p.ProviderSessionID,
		Backend:           be,
		Cwd:               p.Cwd,
		Objective:         p.Objective,
		Status:            p.Status,
		TokenBudget:       p.TokenBudget,
	}, nil
}

// ── helpers ─────────────────────────────────────────────────────────────────

func (h *RuntimeHandlers) lookupRuntimeByType(bt agent_backend_entity.BackendType) agentruntime.Runtime {
	h.mu.RLock()
	fn := h.runtimeFor
	h.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(bt)
}

// errBackendUnwired 标记**接线故障**:会话在本 handler 上是活的,但它的 backend 在这台
// daemon 上根本解不出 runtime(注册表没接上 / 该类型没注册)。
//
// 它包住 ErrNoActiveTurn,所以过线错误码不变(-32012 之外的那 7 个控制 RPC 在同一条
// resolveSession 上仍拿到 ErrCodeNoActiveTurn,桌面端的 errors.Is 一字未改);daemon
// 内部则据此把它与「会话不在 / 已结束」区分开 —— 后者按 R8 折成成功,前者绝不能
// (见 idempotentSubmitResult)。
var errBackendUnwired = fmt.Errorf("daemon: backend runtime not registered: %w", agentruntime.ErrNoActiveTurn)

func (h *RuntimeHandlers) resolveSession(sid int64) (agentruntime.Runtime, error) {
	h.mu.RLock()
	row, ok := h.sessions[sid]
	fn := h.runtimeFor
	h.mu.RUnlock()
	if !ok || row == nil {
		return nil, agentruntime.ErrNoActiveTurn
	}
	if fn == nil {
		return nil, errBackendUnwired
	}
	rt := fn(row.backendType)
	if rt == nil {
		return nil, errBackendUnwired
	}
	return rt, nil
}

// resolveTarget 解析本轮执行目标（provider + 执行侧配置），决策 9/11。
//
// wireProviderKey / wireModelKey 是桌面端透传的 effective target（会话
// provider_key/model_key 优先，未钉时回落 backend 绑定）。daemon 按它们从自家
// 目录自解：
//   - provider-default（modelKey 空）：Provider 缺失/非 active → 回退 agent 绑定
//     （或 CLI 登录态）执行并回传 providerFallbackKey 信号；
//   - fixed-model（modelKey 非空）：Provider 或 Model 缺失/停用/不支持 → 严格阻止，
//     绝不静默降级为默认模型（决策 7/11）。
//
// 返回的 EffectiveLLMConfig 只在 provider 解析成功时非 nil；native（CLI 登录态）时
// 为 nil，runtime 回落 CLI 自身模型。
func (h *RuntimeHandlers) resolveTarget(
	ctx context.Context, wireProviderKey, wireModelKey string, be *agent_backend_entity.AgentBackend,
) (*llm_provider_entity.LLMProvider, *agentruntime.EffectiveLLMConfig, string, error) {
	effectiveKey := strings.TrimSpace(wireProviderKey)
	if effectiveKey == "" && be != nil {
		effectiveKey = be.LLMProviderKey
	}
	// wire 的 model key 是桌面端按 sessionModelKeyFor 解析好的结果：会话钉了 provider 时
	// 用会话 ModelKey（空 = provider-default），未钉（inherit-agent）时桌面端已回落
	// backend 固定模型透传过来。会话是否钉住只有桌面端知道，daemon 不得再自行派生
	// be.LLMModelKey —— 否则会话钉 provider-default（同 backend 绑定同家）会被 backend
	// 固定模型带偏成 fixed-model，与本地路径 sessionModelKeyFor 的语义分叉（spec 决策 1）。
	// 会话 provider 缺失回退 agent 绑定的情形在下方分支显式用 be.LLMModelKey。
	modelKey := strings.TrimSpace(wireModelKey)
	if effectiveKey == "" || h.deps.Lookup == nil {
		return nil, nil, "", nil
	}
	pv, err := h.deps.Lookup.FindByKey(ctx, effectiveKey)
	if err == nil && pv != nil && pv.IsActive() {
		eff, merr := h.resolveEffectiveModel(ctx, pv, modelKey)
		if merr != nil {
			return nil, nil, "", merr
		}
		return pv, eff, "", nil
	}
	if modelKey != "" {
		// fixed-model：Provider 缺失/非 active → 严格阻止，不降级、不回退。
		return nil, nil, "", &rpcerror.Error{
			Code:    rpcerror.ErrProviderMissing.Code,
			Message: fmt.Sprintf("LLM provider %q not configured on remote daemon: %v", effectiveKey, err),
		}
	}
	// provider-default：会话 key 缺失/非 active → 回退 agent 绑定（或 CLI 登录态）。
	providerFallbackKey := effectiveKey
	if be != nil && be.LLMProviderKey != "" && effectiveKey != be.LLMProviderKey {
		bpv, berr := h.deps.Lookup.FindByKey(ctx, be.LLMProviderKey)
		if berr != nil {
			return nil, nil, "", &rpcerror.Error{
				Code:    rpcerror.ErrProviderMissing.Code,
				Message: fmt.Sprintf("LLM provider %q not configured on remote daemon: %v", be.LLMProviderKey, berr),
			}
		}
		eff, merr := h.resolveEffectiveModel(ctx, bpv, be.LLMModelKey)
		if merr != nil {
			return nil, nil, "", merr
		}
		return bpv, eff, providerFallbackKey, nil
	}
	// effective key 就是 agent 绑定（或未绑定 CLI 登录态）：缺失直接报 ErrProviderMissing
	//（桌面端 remoteProviderKnownMissing 会先拦已知缺失的 agent 绑定）；未绑定则回落 CLI 登录态。
	if be == nil || be.LLMProviderKey == "" {
		return nil, nil, providerFallbackKey, nil
	}
	return nil, nil, "", &rpcerror.Error{
		Code:    rpcerror.ErrProviderMissing.Code,
		Message: fmt.Sprintf("LLM provider %q not configured on remote daemon: %v", effectiveKey, err),
	}
}

// resolveEffectiveModel 在已解析的 provider 之上解析执行侧配置（EffectiveLLMConfig
// v1 seam）：provider-default 取当前默认模型，fixed-model 取指定模型。模型缺失/停用
// 由 Lookup.ResolveModel 报错，这里原样透出（调用方据此严格阻止本轮）。
//
// 装配本身走共享构造口 agentruntime.NewEffectiveLLMConfig —— 桌面与 daemon 同一套输入
// 必须得到逐字段相同的配置，daemon 自己手写那份曾漏填 ContextWindow / MaxOutput。
func (h *RuntimeHandlers) resolveEffectiveModel(
	ctx context.Context, provider *llm_provider_entity.LLMProvider, modelKey string,
) (*agentruntime.EffectiveLLMConfig, error) {
	eff, err := h.deps.Lookup.ResolveModel(ctx, provider.ProviderKey, modelKey)
	if err != nil {
		return nil, fmt.Errorf("resolve model for provider %q: %w", provider.ProviderKey, err)
	}
	return agentruntime.NewEffectiveLLMConfig(agentruntime.EffectiveLLMConfigInput{
		ProviderKey:      provider.ProviderKey,
		ProviderType:     provider.Type,
		ProviderName:     provider.Name,
		TargetModelKey:   modelKey,
		ResolvedModelKey: eff.ModelKey,
		ResolvedModelID:  eff.ModelID,
		ContextWindow:    eff.ContextWindow,
		MaxOutput:        eff.MaxOutput,
		BaseURL:          provider.BaseURL,
		APIKey:           provider.APIKey,
		HasAPIKey:        provider.APIKey != "",
	}), nil
}

// ensureSessionToken 返回某 session 的 gateway URL + 常驻 token:首轮签一个永久
// (ttl=0)token 并缓存,后续轮复用同一个。该 token 在 spawn 时烤进 claude 子进程
// env,子进程跨轮复用时 env 不重建,所以必须整段会话稳定且永不过期 —— 否则下一轮
// 复用的子进程手里的 token 失效,PostToolUse hook 撞 401、SteerInbox drain 不到。
// Gateway 不可用 / URL 为空时返回空串,调用方按"不签"处理。
//
// providerKey 是本轮解析出来的供应商(wire 的 effectiveProviderKey 自解、必要时已回退):
// 首轮按它签发,之后每轮把既有 token 的路由目标对齐到它 —— 桌面端换供应商后,同一个
// token 字符串继续有效,只是上游变了(决策 3/12)。签一次 / 改道 / 撤销的实现在
// agentruntime.SessionTokenCache,与桌面共用;这里只保留 daemon 自己的可用性判据
// (URL 为空 = 不签)。
func (h *RuntimeHandlers) ensureSessionToken(
	ctx context.Context, sid int64, be *agent_backend_entity.AgentBackend, providerKey, modelKey string,
) (string, string, error) {
	if h.deps.Gateway == nil {
		return "", "", nil
	}
	url := h.deps.Gateway.URL()
	if url == "" {
		return "", "", nil
	}
	tok, err := h.sessionTokens.EnsureToken(ctx, sid, be, providerKey, modelKey)
	if err != nil {
		return "", "", err
	}
	return url, tok, nil
}

func (h *RuntimeHandlers) lookupSession(sid int64) *runtimeSession {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[sid]
}

func (h *RuntimeHandlers) isCurrent(sid int64, owner *runtimeSession) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[sid] == owner
}

func (h *RuntimeHandlers) register(sid int64, row *runtimeSession) {
	h.mu.Lock()
	h.sessions[sid] = row
	h.mu.Unlock()
}

func (h *RuntimeHandlers) registerPiIfAbsent(sid int64, row *runtimeSession) bool {
	claimed := false
	if h.deps.GenerationRegistry != nil {
		claimed = h.deps.GenerationRegistry.ClaimConnection(row.connection, sid, row.generationToken)
		if !claimed {
			return false
		}
	}

	h.mu.Lock()
	// 挡的是「真的有一轮在跑」;Adopt 留下的占位行背后没有任何一轮,直接顶掉它 ——
	// 顶掉之后会话依旧解得出(resolveSession 只看 backendType),而且解出来的是这一轮
	// 真正的属主。
	existing := h.sessions[sid]
	if h.closed || (existing != nil && !existing.adopted) {
		h.mu.Unlock()
		if claimed {
			h.releaseGeneration(sid, row)
		}
		return false
	}
	h.sessions[sid] = row
	h.mu.Unlock()
	return true
}

func (h *RuntimeHandlers) unregister(sid int64, owner *runtimeSession) bool {
	h.mu.Lock()
	current := h.sessions[sid] == owner
	if current {
		delete(h.sessions, sid)
	}
	h.mu.Unlock()
	// 释放**无条件**发生:内存表里的这一格可能已经被别人换掉(例如重连后的
	// runtime.session.attach 会 Adopt 同一条会话),但 generation 属主表里那份
	// 预约仍然记在这个 owner 名下。ReleaseRuntimeGeneration 按 (连接, token)
	// 精确匹配,重复调用与非属主调用都是 no-op。
	h.releaseGeneration(sid, owner)
	return current
}

func (h *RuntimeHandlers) releaseGeneration(sid int64, owner *runtimeSession) {
	if owner == nil || h.deps.GenerationRegistry == nil {
		return
	}
	h.deps.GenerationRegistry.ReleaseConnection(owner.connection, sid, owner.generationToken)
}

func (h *RuntimeHandlers) canDeliverPiEvent(sid int64, owner *runtimeSession) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sessions[sid] == owner && !owner.disconnected && !owner.finalizing
}

func (h *RuntimeHandlers) claimPiTerminal(sid int64, owner *runtimeSession) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessions[sid] != owner || !owner.started || owner.disconnected ||
		owner.finalizing || owner.terminalClaimed {
		return false
	}
	owner.terminalClaimed = true
	return true
}

func (h *RuntimeHandlers) finalizePiGeneration(ctx context.Context, sid int64, owner *runtimeSession) error {
	if owner == nil {
		return nil
	}
	h.mu.Lock()
	if owner.finalizing {
		h.mu.Unlock()
		return h.waitPiFinalization(ctx, owner)
	}
	owner.finalizing = true
	prepared := owner.prepared
	h.mu.Unlock()

	if owner.cancel != nil {
		owner.cancel()
	}
	var closeErr error
	if prepared != nil {
		closeBase := ctx
		if closeBase == nil || closeBase.Err() != nil {
			closeBase = context.Background()
		}
		closeCtx, cancel := context.WithTimeout(closeBase, runtimeConnectionCleanupTimeout)
		closeErr = prepared.Close(closeCtx)
		cancel()
	}
	h.unregister(sid, owner)
	h.mu.Lock()
	owner.finalErr = closeErr
	h.mu.Unlock()
	owner.signalTerminal()
	return closeErr
}

func (h *RuntimeHandlers) waitPiFinalization(ctx context.Context, owner *runtimeSession) error {
	if owner == nil || owner.terminalDone == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(runtimePiTerminalWaitTimeout)
	defer timer.Stop()
	select {
	case <-owner.terminalDone:
		return h.piFinalError(owner)
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return context.DeadlineExceeded
	}
}

func (h *RuntimeHandlers) piFinalError(owner *runtimeSession) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return owner.finalErr
}

func (s *runtimeSession) signalTerminal() {
	if s == nil || s.terminalDone == nil {
		return
	}
	s.terminalOnce.Do(func() { close(s.terminalDone) })
}

// decodeHistory turns wire HistoryMessage frames back into the agentruntime
// HistoryMessage shape (typed blocks via blocks.DecodeAll).
func decodeHistory(in []wire.HistoryMessageWire) ([]agentruntime.HistoryMessage, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make([]agentruntime.HistoryMessage, 0, len(in))
	for _, m := range in {
		bs, err := blocks.DecodeAll(m.Blocks)
		if err != nil {
			return nil, err
		}
		out = append(out, agentruntime.HistoryMessage{Role: m.Role, Blocks: bs})
	}
	return out, nil
}

func decodeUserBlocks(in []blocks.StoredBlock) ([]blocks.ContentBlock, error) {
	if len(in) == 0 {
		return nil, nil
	}
	return blocks.DecodeAll(in)
}
