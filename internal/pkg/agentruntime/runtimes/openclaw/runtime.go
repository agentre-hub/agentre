// Package openclaw implements the AgentRE runtime backed exclusively by the
// OpenClaw Gateway WebSocket RPC protocol. It never invokes OpenClaw through
// ACP, HTTP, CLI, or an embedded process.
package openclaw

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

type ConfigResolver interface {
	ResolveOpenClawConfig(ctx context.Context, backendID int64) (openclawgateway.Config, error)
}

type ConfigResolverFunc func(context.Context, int64) (openclawgateway.Config, error)

func (f ConfigResolverFunc) ResolveOpenClawConfig(ctx context.Context, backendID int64) (openclawgateway.Config, error) {
	return f(ctx, backendID)
}

type Runtime struct {
	resolverMu sync.RWMutex
	resolver   ConfigResolver

	mu     sync.RWMutex
	active map[int64]*activeTurn
	// turnSeq 会话无关的轮计数器:每轮 Run 入口递增一次,赋给 activeTurn.turnToken,
	// 值随 RunResult 暴露给 chat_svc。openclaw 每会话同时至多一轮(register 会拒绝
	// 重入),token 用于区分「调用方想中断的那一轮」与「当前活跃轮」:旧轮已结束、
	// 新轮已起时带旧 token 的 abort 是 stale no-op(决策 1)。
	turnSeq atomic.Uint64
}

var defaultRuntime = New(nil)

func init() {
	agentruntime.RegisterRuntime(agent_backend_entity.TypeOpenClaw, defaultRuntime)
}

func RegisterConfigResolver(resolver ConfigResolver) {
	defaultRuntime.SetConfigResolver(resolver)
}

func New(resolver ConfigResolver) *Runtime {
	return &Runtime{resolver: resolver, active: make(map[int64]*activeTurn)}
}

func (r *Runtime) SetConfigResolver(resolver ConfigResolver) {
	r.resolverMu.Lock()
	r.resolver = resolver
	r.resolverMu.Unlock()
}

func (r *Runtime) Capabilities() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{
		capability.CapAbort:        true,
		capability.CapExecApproval: true,
	}}
}

func (r *Runtime) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	if req.Backend == nil || !req.Backend.IsOpenClaw() {
		return nil, nil, fmt.Errorf("openclaw runtime: OpenClaw backend is required")
	}
	// 本地 runtime 只收得到本机网关的档：指向本机指纹的档（R13 认领后本地 backend 的
	// DeviceID == 本机指纹）在这里不是「远端」，而另一台机器的 OpenClaw 档在
	// resolveAgentBackend 就已拒绝、也到不了本地 runtime。是否拿得到远端 secret 由
	// config resolver 定夺（远端档的 resolver 会回 ErrOpenClawRemoteSecretUnavailable），
	// 这里不再用 DeviceID 非空当「远端」拦——那会把本机档误杀。
	if req.SessionID <= 0 || req.Backend.ID <= 0 {
		return nil, nil, fmt.Errorf("openclaw runtime: backend and chat session IDs are required")
	}
	r.resolverMu.RLock()
	resolver := r.resolver
	r.resolverMu.RUnlock()
	if resolver == nil {
		return nil, nil, fmt.Errorf("openclaw runtime: config resolver is unavailable")
	}
	config, err := resolver.ResolveOpenClawConfig(ctx, req.Backend.ID)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(config.URL) == "" {
		config.URL = req.Backend.OpenClawGatewayURL
	}
	client, err := openclawgateway.NewClient(config)
	if err != nil {
		return nil, nil, err
	}
	hello, err := client.Start(ctx)
	if err != nil {
		client.Close()
		if ctx.Err() != nil {
			// 用户在握手期间点了停止 —— 是中止不是故障。
			return nil, nil, agentruntime.ErrAborted
		}
		return nil, nil, err
	}
	if err := openclawgateway.ValidateRuntimeFeatures(hello); err != nil {
		client.Close()
		return nil, nil, err
	}
	modelOverride := ""
	if hasGrantedScope(hello.Auth.Scopes, "operator.admin") {
		modelOverride = strings.TrimSpace(req.Backend.OpenClawDefaultModel)
	}
	// 开轮对账是尽力而为:待审批本来也会通过 exec.approval.requested 事件到达,而
	// exec.approval.list 在真实网关上只对创建该审批的连接/管理员可见,拿不到属常态。
	// 早期版本把这里当致命错误,结果用户在这一次 RPC 往返期间点停止 → ctx cancel →
	// 整轮以「故障」收场,会话卡死在 running。ctx 被取消要明确回 ErrAborted。
	initialApprovals, listErr := listExecApprovals(ctx, client)
	if listErr != nil {
		if ctx.Err() != nil {
			client.Close()
			return nil, nil, agentruntime.ErrAborted
		}
		initialApprovals = nil
	}
	// Start publishes the initial ready snapshot. Drain it here so every value
	// consumed by activeTurn means a reconnect and therefore requires state
	// reconciliation before accepting more events.
	select {
	case <-client.Ready():
	default:
	}

	sessionKey := strings.TrimSpace(req.ProviderSessionID)
	if sessionKey == "" {
		sessionKey = fmt.Sprintf("agentre:%d:%d", req.Backend.ID, req.SessionID)
	}
	runID := uuid.NewString()
	agentID := strings.TrimSpace(req.Backend.OpenClawAgentID)
	// 开轮默认走 chat.send:网关只在这条路径上把「发起该轮的设备」记成审批 reviewer
	// (server 侧 ApprovalReviewerDeviceId),agent 方法不绑 —— 用 agent 起轮，本设备
	// 就看不到自己这一轮触发的 exec 审批,工具调用会一直挂着等一个永远看不见的决策。
	// 唯一的例外是 admin 下发 model override:chat.send 没有 model 参数,而 admin
	// 连接本来就能看见全部审批,不依赖 reviewer 绑定。
	// chat.send 起的轮次不会广播 agent/chat 事件 —— 网关只发给
	// sessions.messages.subscribe 的订阅者。订阅不了就只能退回 agent(它广播),
	// 宁可丢掉审批可见性也不能让整轮没有流式输出。
	canSubscribe := slices.Contains(hello.Features.Methods, sessionSubscribeMethod)
	useChatSend := modelOverride == "" &&
		slices.Contains(hello.Features.Methods, chatSendMethod) && canSubscribe
	sessionKeyCanonical := strings.TrimSpace(req.ProviderSessionID) != ""
	if useChatSend {
		canonicalKey, subscribeErr := subscribeSessionMessages(ctx, client, sessionKey, agentID)
		if subscribeErr != nil {
			client.Close()
			if ctx.Err() != nil {
				return nil, nil, agentruntime.ErrAborted
			}
			return nil, nil, fmt.Errorf("openclaw runtime: subscribe session messages: %w", subscribeErr)
		}
		if canonicalKey != "" {
			sessionKey = canonicalKey
			sessionKeyCanonical = true
		}
	}
	turnMethod := "chat.send"
	var turnParams any = struct {
		Message        string `json:"message"`
		AgentID        string `json:"agentId,omitempty"`
		SessionKey     string `json:"sessionKey"`
		Deliver        bool   `json:"deliver"`
		IdempotencyKey string `json:"idempotencyKey"`
	}{
		Message:        req.UserText,
		AgentID:        agentID,
		SessionKey:     sessionKey,
		Deliver:        false,
		IdempotencyKey: runID,
	}
	if !useChatSend {
		turnMethod = "agent"
		turnParams = struct {
			Message        string `json:"message"`
			AgentID        string `json:"agentId,omitempty"`
			Model          string `json:"model,omitempty"`
			SessionKey     string `json:"sessionKey"`
			Deliver        bool   `json:"deliver"`
			IdempotencyKey string `json:"idempotencyKey"`
		}{
			Message:        req.UserText,
			AgentID:        agentID,
			Model:          modelOverride,
			SessionKey:     sessionKey,
			Deliver:        false,
			IdempotencyKey: runID,
		}
	}
	var ack struct {
		RunID      string `json:"runId"`
		Status     string `json:"status"`
		SessionKey string `json:"sessionKey"`
	}
	callErr := client.Call(ctx, turnMethod, turnParams, &ack)
	if callErr == nil && strings.TrimSpace(ack.RunID) != "" {
		runID = strings.TrimSpace(ack.RunID)
	}
	// chat.send 只回 {runId,status};规范化后的 key 来自订阅应答,agent 则回在 ack 里。
	if callErr == nil && strings.TrimSpace(ack.SessionKey) != "" {
		sessionKey = strings.TrimSpace(ack.SessionKey)
		sessionKeyCanonical = true
	}
	if callErr != nil && !errors.Is(callErr, openclawgateway.ErrDisconnected) &&
		!errors.Is(callErr, context.DeadlineExceeded) {
		client.Close()
		if ctx.Err() != nil {
			return nil, nil, agentruntime.ErrAborted
		}
		return nil, nil, callErr
	}

	// 开轮前记下会话记录的 endedAt 作基线 —— 收轮补 usage 时据此判断记录是否已刷新。
	var usageBaselineEndedAt int64
	if slices.Contains(hello.Features.Methods, sessionDescribeMethod) {
		if record := describeSession(ctx, client, sessionKey); record != nil {
			usageBaselineEndedAt = record.EndedAt
		}
	}

	result := &agentruntime.RunResult{
		ProviderSessionID: sessionKey,
		Model:             modelOverride,
	}
	active := &activeTurn{
		runtime:              r,
		ctx:                  ctx,
		client:               client,
		sessionID:            req.SessionID,
		sessionKey:           sessionKey,
		runID:                runID,
		out:                  make(chan agentruntime.Event, 64),
		result:               result,
		agentID:              agentID,
		sessionKeyCanonical:  sessionKeyCanonical,
		subscribeMessages:    useChatSend,
		usageBaselineEndedAt: usageBaselineEndedAt,
		sessionDescribe:      slices.Contains(hello.Features.Methods, sessionDescribeMethod),
		approvals:            make(map[string]*approvalState),
		initialApprovals:     initialApprovals,
		turnToken:            r.turnSeq.Add(1),
	}
	result.TurnToken = active.turnToken
	if !r.register(active) {
		client.Close()
		return nil, nil, fmt.Errorf("openclaw runtime: session already has an active turn")
	}
	go active.consume()
	return active.out, result, nil
}

func hasGrantedScope(scopes []string, required string) bool {
	for _, scope := range scopes {
		if scope == required {
			return true
		}
	}
	return false
}

func (r *Runtime) ResolveExecApproval(ctx context.Context, sessionID int64, approvalID, decision string) (agentruntime.ExecApprovalResolution, error) {
	r.mu.RLock()
	active := r.active[sessionID]
	r.mu.RUnlock()
	if active == nil {
		return agentruntime.ExecApprovalResolution{}, agentruntime.ErrNoActiveTurn
	}
	return active.resolveApproval(ctx, approvalID, decision)
}

func (r *Runtime) Abort(ctx context.Context, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	r.mu.RLock()
	active := r.active[sessionID]
	r.mu.RUnlock()
	if active == nil {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	if turnToken != 0 && active.turnToken != turnToken {
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, nil
	}
	if err := active.abort(ctx); err != nil {
		return agentruntime.AbortOutcome{}, err
	}
	return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}, nil
}

func (r *Runtime) register(active *activeTurn) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active[active.sessionID] != nil {
		return false
	}
	r.active[active.sessionID] = active
	return true
}

func (r *Runtime) unregister(active *activeTurn) {
	r.mu.Lock()
	if r.active[active.sessionID] == active {
		delete(r.active, active.sessionID)
	}
	r.mu.Unlock()
}
