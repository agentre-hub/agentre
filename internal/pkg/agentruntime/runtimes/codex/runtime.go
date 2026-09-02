// Package codex 是 OpenAI Codex CLI 的 agent runtime,emit sealed agentruntime.Event。
// 本包 init() 时把 *Runtime 注册到 agentruntime.RuntimeFor。
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/pkg/codex"
)

var defaultRuntime = NewWithPool(agentruntime.DefaultCLISessionPool())

func init() {
	agentruntime.RegisterRuntime(agent_backend_entity.TypeCodex, defaultRuntime)
}

// codexActive 一个 chat session 当前的 codex stream 状态。
//   - stream:turn/steer 入口(*codex.Stream 实现)
//   - interrupter:turn/interrupt 入口
//   - userInput:request_user_input 反向投回入口
//   - approval:requestApproval 反向投回入口
//   - pending:本 turn 已发出但还没被 EventUserMessage echo 回来的 steer text
//     (codex 协议 fire-and-forget,本地做 FIFO 配对)
//   - askWaiters:request_user_input 阻塞中的 waiter
//   - permWaiters:requestApproval 阻塞中的 waiter
//   - out:Run() 期间登记的事件出口,SubmitAnswer 完成后用它 emit UserAskResolved
type codexActive struct {
	mu          sync.Mutex
	stream      cxSteerStream
	interrupter cxInterruptable
	userInput   cxUserInputStream
	approval    cxApprovalStream
	pending     []agentruntime.ConsumedSteer
	askWaiters  map[string]codexAskWaiter
	permWaiters map[string]codexPermWaiter
	pool        *agentruntime.CLISessionPool
	poolKey     string
	outMu       sync.Mutex
	out         chan<- agentruntime.Event
	// turnToken 本会话当前活跃轮的 per-turn token(决策 1):每轮 Run 入口递增,值随
	// RunResult 暴露给 chat_svc。Abort(turnToken!=0) 只在该 token 仍是当前活跃轮时
	// 才中断,否则 stale no-op。codex 只有用户轮,上报类型恒为 userTurn。
	turnToken atomic.Uint64
}

type codexAskWaiter struct {
	questions []agentruntime.AskQuestion
}

// codexPermWaiter 记录审批 waiter 重建卡片所需的载荷:PendingWaiters(R7)要把
// 它还原成与实时 ToolPermissionRequest 帧同形的内容,所以 requestID 之外还要
// 留住 tool_name 与原始 input 字节。
type codexPermWaiter struct {
	toolName string
	rawInput json.RawMessage
}

// Runtime codex runtime 实现。
type Runtime struct {
	mu     sync.Mutex
	active map[int64]*codexActive
	pool   *agentruntime.CLISessionPool
}

// launchIdentity 拼出 codex 的启动身份:ReasoningEffort(-c model_reasoning_effort=
// 覆盖项)、--model(解析出的 ModelID)、稳定 ModelKey 与 effectiveProviderKey
// (model_provider/base_url 的 -c 覆盖项)都绑定在 Client 创建时,
// 而 app-server 进程会被池跨轮复用 —— 任一变化都必须驱逐重开,否则这一轮复用的是拿旧
// 参数起来的进程:换了供应商仍打旧的、换了模型 RunResult.Model 仍是旧模型、改了力度仍
// 用旧力度跑(spec 2026-09-01「三后端下发档位的收敛」)。ModelKey
// 单列一项,因为两行不同的稳定模型可以解析到同一个上游 ModelID。
//
// 比对与「未记录即已变」的判定都交给 CLISessionPool.GetWithIdentity,身份随条目消失
// —— 此前这里是一张旁路表,池自行淘汰条目时不回调本包,只能靠 512 条 FIFO 上限兜底。
// 分隔符用 \x00:这些字段都是标识串,不会含 NUL。
func launchIdentity(req agentruntime.RunRequest) string {
	return strings.Join([]string{
		req.Backend.ReasoningEffort,
		codexEffectiveModel(req),
		codexEffectiveModelKey(req),
		req.EffectiveProviderKey(),
	}, "\x00")
}

func New() *Runtime {
	return NewWithPool(agentruntime.NewCLISessionPool(agentruntime.DefaultCLISessionIdleCap))
}

func NewWithPool(pool *agentruntime.CLISessionPool) *Runtime {
	if pool == nil {
		pool = agentruntime.NewCLISessionPool(agentruntime.DefaultCLISessionIdleCap)
	}
	return &Runtime{
		active: map[int64]*codexActive{},
		pool:   pool,
	}
}

// Capabilities 返回 codex runtime 的能力矩阵。
//
// 与 claudecode 的差异:
//   - CapCancelSteer = false(codex turn/steer fire-and-forget,无 withdraw verb)
//   - CapDrainSteer = false(无 hook 队列)
//   - CapToolPermission = true(codex app-server requestApproval 协议)
//   - CapForkSession = true(走 thread/rollback)
//   - CapReportContextWindow = true(thread/tokenUsage/updated 推 modelContextWindow)
//   - PermissionModeMeta:仅 default / plan;**禁运行时切换**(running/waiting 禁切)
func (r *Runtime) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Set: map[capability.Capability]bool{
			capability.CapSteer:               true,
			capability.CapAbort:               true,
			capability.CapImageInput:          true,
			capability.CapSetPermission:       true,
			capability.CapAnswerUserAsk:       true,
			capability.CapToolPermission:      true,
			capability.CapForkSession:         true,
			capability.CapReportContextWindow: true,
			capability.CapCompact:             true,
			capability.CapGoal:                true,
			capability.CapMCPTools:            true,
			capability.CapSkills:              true,
		},
		PermissionModeMeta: capability.PermissionModeMeta{
			AllowedModes:         []string{"default", "plan"},
			DefaultMode:          "default",
			SwitchableDuringTurn: false,
			Order:                []string{"default", "plan"},
			// codex 协议要求 launch 时显式 collaboration mode,chat_svc 必须落非空。
			LaunchDefaultMode: "default",
		},
	}
}

func (r *Runtime) GetGoal(ctx context.Context, req agentruntime.GoalRequest) (*agentruntime.Goal, error) {
	sess, err := r.goalSession(ctx, req, true)
	if err != nil {
		return nil, err
	}
	defer r.releaseGoalSession(req.SessionID)
	goal, err := sess.GetGoal(ctx)
	if err != nil {
		return nil, err
	}
	return goalFromCodex(goal), nil
}

func (r *Runtime) SetGoal(ctx context.Context, req agentruntime.GoalRequest) (*agentruntime.Goal, error) {
	sess, err := r.goalSession(ctx, req, false)
	if err != nil {
		return nil, err
	}
	defer r.releaseGoalSession(req.SessionID)
	update := codex.GoalUpdate{
		Objective:   req.Objective,
		TokenBudget: req.TokenBudget,
	}
	if req.Status != nil {
		status := codex.GoalStatus(*req.Status)
		update.Status = &status
	}
	goal, err := sess.SetGoal(ctx, update)
	if err != nil {
		return nil, err
	}
	return goalFromCodex(goal), nil
}

func (r *Runtime) ClearGoal(ctx context.Context, req agentruntime.GoalRequest) (bool, error) {
	sess, err := r.goalSession(ctx, req, true)
	if err != nil {
		return false, err
	}
	defer r.releaseGoalSession(req.SessionID)
	return sess.ClearGoal(ctx)
}

func (r *Runtime) releaseGoalSession(sessionID int64) {
	if sessionID <= 0 {
		return
	}
	r.mu.Lock()
	active := r.active[sessionID] != nil
	r.mu.Unlock()
	if active {
		return
	}
	r.pool.MarkIdle(sessionKey(sessionID))
}

func (r *Runtime) goalSession(ctx context.Context, req agentruntime.GoalRequest, requireProviderSession bool) (cxSessionHandle, error) {
	if req.SessionID <= 0 {
		return nil, fmt.Errorf("agentruntime/runtimes/codex: invalid sessionID %d", req.SessionID)
	}
	if requireProviderSession && strings.TrimSpace(req.ProviderSessionID) == "" {
		return nil, fmt.Errorf("agentruntime/runtimes/codex: missing provider session id for goal")
	}
	cwd := req.Cwd
	if cwd == "" {
		var err error
		cwd, err = agentruntime.AgentCwd(req.AgentID)
		if err != nil {
			logger.Ctx(ctx).Error("codex runtime: AgentCwd resolve failed for goal",
				zap.Int64("sessionID", req.SessionID),
				zap.Int64("agentID", req.AgentID), zap.Error(err))
			return nil, err
		}
	}
	runReq := agentruntime.RunRequest{
		Backend:           req.Backend,
		Provider:          req.Provider,
		Effective:         req.Effective,
		AgentID:           req.AgentID,
		SessionID:         req.SessionID,
		Cwd:               cwd,
		ProviderSessionID: req.ProviderSessionID,
		GatewayURL:        req.GatewayURL,
		GatewayToken:      req.GatewayToken,
	}
	env, err := BuildCodexEnv(runReq.Backend, gatewayDeps(runReq))
	if err != nil {
		return nil, err
	}
	return r.acquireSession(runReq, env, cwd)
}

func goalFromCodex(goal *codex.Goal) *agentruntime.Goal {
	if goal == nil {
		return nil
	}
	return &agentruntime.Goal{
		ThreadID:        goal.ThreadID,
		Objective:       goal.Objective,
		Status:          string(goal.Status),
		TokenBudget:     goal.TokenBudget,
		TokensUsed:      goal.TokensUsed,
		TimeUsedSeconds: goal.TimeUsedSeconds,
		CreatedAt:       goal.CreatedAt,
		UpdatedAt:       goal.UpdatedAt,
	}
}

func (r *Runtime) register(sessionID int64, a *codexActive) error {
	if sessionID <= 0 {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active[sessionID] != nil {
		return fmt.Errorf("agentruntime/runtimes/codex: session %d already has an active turn", sessionID)
	}
	r.active[sessionID] = a
	return nil
}

func (r *Runtime) unregister(sessionID int64, expected *codexActive) {
	if sessionID <= 0 {
		return
	}
	r.mu.Lock()
	if r.active[sessionID] == expected {
		delete(r.active, sessionID)
	}
	r.mu.Unlock()
}

// sessionKey 把 chat session ID 翻成池键。形状由 agentruntime 统一决定(见
// SessionPoolKey):池是进程级单例,两个 CLI 后端共用同一个实例。
func sessionKey(id int64) string {
	return agentruntime.SessionPoolKey(agent_backend_entity.TypeCodex, id)
}

// Run 启动一轮 codex CLI 发送。语义同顶层 codex.go.Run,emit 类型从
// RuntimeEvent 改为 sealed agentruntime.Event。
func (r *Runtime) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	if req.Backend == nil {
		return nil, nil, fmt.Errorf("agentruntime/runtimes/codex: nil backend")
	}
	cwd := req.Cwd
	if cwd == "" {
		var err error
		cwd, err = agentruntime.ResolveAgentCwd(req.AgentID, req.AgentSyncID)
		if err != nil {
			logger.Ctx(ctx).Error("codex runtime: agent cwd resolve failed",
				zap.Int64("sessionID", req.SessionID),
				zap.Int64("agentID", req.AgentID), zap.Error(err))
			return nil, nil, err
		}
	}
	env, err := BuildCodexEnv(req.Backend, gatewayDeps(req))
	if err != nil {
		logger.Ctx(ctx).Error("codex runtime: BuildCodexEnv failed",
			zap.Int64("sessionID", req.SessionID), zap.Error(err))
		return nil, nil, err
	}
	active := &codexActive{}
	if err := r.register(req.SessionID, active); err != nil {
		return nil, nil, err
	}
	releaseClaim := true
	defer func() {
		if releaseClaim {
			r.unregister(req.SessionID, active)
		}
	}()

	sess, err := r.acquireSession(req, env, cwd)
	if err != nil {
		logger.Ctx(ctx).Error("codex runtime: session factory failed",
			zap.Int64("sessionID", req.SessionID),
			zap.String("cwd", cwd), zap.Error(err))
		return nil, nil, err
	}

	if strings.TrimSpace(req.ForkAnchor) != "" {
		if _, err := sess.RewindTo(ctx, req.ForkAnchor); err != nil {
			closeEphemeralSession(req, sess)
			logger.Ctx(ctx).Error("codex runtime: RewindTo failed",
				zap.Int64("sessionID", req.SessionID),
				zap.String("forkAnchor", req.ForkAnchor), zap.Error(err))
			return nil, nil, err
		}
	}

	var stream cxStream
	var cleanupInputs func()
	if req.Compact {
		stream, err = sess.Compact(ctx)
	} else if len(req.UserBlocks) > 0 {
		inputs, cleanup, ierr := userInputsFromBlocks(req.UserBlocks)
		if ierr != nil {
			closeEphemeralSession(req, sess)
			return nil, nil, ierr
		}
		cleanupInputs = cleanup
		stream, err = sess.StreamInput(ctx, inputs, req.CollaborationMode)
	} else {
		stream, err = sess.Stream(ctx, req.UserText, req.CollaborationMode)
	}
	if err != nil {
		if requiresEphemeralSession(req) {
			closeEphemeralSession(req, sess)
		} else if req.SessionID > 0 {
			r.pool.Remove(sessionKey(req.SessionID))
		}
		logger.Ctx(ctx).Error("codex.Runtime: sessionRun failed",
			zap.Int64("sessionId", req.SessionID),
			zap.Bool("compact", req.Compact),
			zap.String("collaborationMode", req.CollaborationMode), zap.Error(err))
		return nil, nil, err
	}
	logger.Ctx(ctx).Info("codex.Runtime: turn started",
		zap.Int64("sessionId", req.SessionID),
		zap.String("providerSessionId", sess.ID()),
		zap.String("collaborationMode", req.CollaborationMode))

	key := ""
	if req.SessionID > 0 && !requiresEphemeralSession(req) {
		key = sessionKey(req.SessionID)
	}
	active.mu.Lock()
	active.stream = sess.ActiveStream()
	active.interrupter = sess.ActiveInterruptor()
	active.pool = r.pool
	active.poolKey = key
	if st, ok := stream.(cxSteerStream); ok {
		active.stream = st
	}
	if intr, ok := stream.(cxInterruptable); ok {
		active.interrupter = intr
	}
	if ui, ok := stream.(cxUserInputStream); ok {
		active.userInput = ui
	}
	if ap, ok := stream.(cxApprovalStream); ok {
		active.approval = ap
	}
	active.mu.Unlock()
	out := make(chan agentruntime.Event, 32)
	active.setOut(out)

	// RunResult.Model 上报线程实际模型(sess.Model()),而非启动请求模型:codex 的
	// thread/resume 返回线程当前 model。绑 provider 时解析出的 ModelID 经 --model 生效后
	// sess.Model() 即实际运行模型;无 provider 时两者同值,不回归。
	modelID := strings.TrimSpace(sess.Model())
	if modelID == "" {
		// app-server 没在 thread start/resume 结果里带 model 时 sess.Model() 为空 ——
		// 此时「观测不到」不等于「跑的是 defaultModelID」:直接落死常量会把一个从没跑过
		// 的 model id 写进 assistantMsg.Model。回落到本轮请求的 effectiveModel
		// (解析出的 ModelID),观测不到就按「请求值已生效」处理。
		modelID = codexEffectiveModel(req)
	}
	if modelID == "" {
		modelID = defaultModelID
	}
	result := &agentruntime.RunResult{ProviderSessionID: sess.ID(), Model: modelID, TurnToken: active.turnToken.Add(1)}

	go func() {
		defer close(out)
		if key == "" {
			defer func() { _ = sess.Close(context.Background()) }()
		}
		if cleanupInputs != nil {
			defer cleanupInputs()
		}
		defer r.unregister(req.SessionID, active)
		defer active.setOut(nil)
		drainStream(stream, out, result, active, req.CollaborationMode)
		active.clearWaiters()
		if sid := stream.SessionID(); sid != "" {
			result.ProviderSessionID = sid
		}
		if key != "" {
			if codexStreamReusable(stream, result.StopErr) {
				r.pool.MarkIdle(key)
			} else {
				r.pool.Remove(key)
			}
		}
	}()
	releaseClaim = false
	return out, result, nil
}

func (r *Runtime) acquireSession(req agentruntime.RunRequest, env map[string]string, cwd string) (cxSessionHandle, error) {
	if requiresEphemeralSession(req) {
		return cxSessionFactory(req, env, cwd)
	}
	identity := launchIdentity(req)
	if req.SessionID > 0 {
		key := sessionKey(req.SessionID)
		// 启动身份未变才复用池内 app-server;变了(含池里那条从没记过身份)由池当场
		// 驱逐,见 CLISessionPool.GetWithIdentity。
		if v, ok := r.pool.GetWithIdentity(key, identity); ok {
			r.pool.MarkActive(key)
			return v.(cxSessionHandle), nil
		}
	}
	sess, err := cxSessionFactory(req, env, cwd)
	if err != nil {
		return nil, err
	}
	if req.SessionID > 0 {
		key := sessionKey(req.SessionID)
		r.pool.PutWithIdentity(key, identity, sess)
		r.pool.MarkActive(key)
	}
	return sess, nil
}

func closeEphemeralSession(req agentruntime.RunRequest, sess cxSessionHandle) {
	if !requiresEphemeralSession(req) || sess == nil {
		return
	}
	_ = sess.Close(context.Background())
}

func requiresEphemeralSession(req agentruntime.RunRequest) bool {
	return len(req.MCPServers) > 0 || len(req.EnabledPlugins) > 0
}

func (r *Runtime) CloseSession(_ context.Context, sessionID int64) {
	if sessionID <= 0 {
		return
	}
	r.pool.Remove(sessionKey(sessionID))
}

func (r *Runtime) CloseAllSessions(_ context.Context) {
	r.pool.RemoveAll()
}

// Abort 软中断当前 turn。语义同顶层 codex.go.Abort。
// turnToken 语义(决策 1):0 = 中断当前活跃轮;非 0 = 仅当该轮仍是当前活跃轮才中断,
// 否则 stale no-op。codex 每会话同时至多一轮、只有用户轮,故被中断轮类型恒为 userTurn。
func (r *Runtime) Abort(ctx context.Context, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	if turnToken != 0 && a.turnToken.Load() != turnToken {
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, nil
	}
	a.mu.Lock()
	a.pending = nil
	intr := a.interrupter
	a.mu.Unlock()
	if intr == nil {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	if err := intr.Interrupt(ctx); err != nil {
		return agentruntime.AbortOutcome{}, err
	}
	return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}, nil
}

// Steer 把 text dispatch 给 active codex.Stream(turn/steer JSON-RPC)。
// queuedID 仅作本地配对用 —— codex 协议 fire-and-forget。
func (r *Runtime) Steer(ctx context.Context, sessionID int64, queuedID string, text string) error {
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil {
		return agentruntime.ErrNoActiveTurn
	}
	a.mu.Lock()
	stream := a.stream
	a.mu.Unlock()
	if stream == nil {
		return agentruntime.ErrNoActiveTurn
	}
	a.addPendingSteer(queuedID, text)
	if err := stream.Steer(ctx, text); err != nil {
		a.removePendingSteer(queuedID)
		if errors.Is(err, codex.ErrNoActiveTurn) {
			return agentruntime.ErrNoActiveTurn
		}
		return err
	}
	return nil
}

// SubmitAnswer 把前端提交的 request_user_input 答案反向投回 codex app-server。
// 语义同顶层 codex.go.SubmitAnswer:skipped → 空 answers map(让 LLM 看到拒答);
// 非 skipped → buildUserInputAnswers 拼 codex 期望的 map[questionID][]string。
func (r *Runtime) SubmitAnswer(ctx context.Context, sessionID int64, requestID string, questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer, skipped bool) error {
	if sessionID <= 0 {
		return fmt.Errorf("agentruntime/runtimes/codex: invalid sessionID %d", sessionID)
	}
	if strings.TrimSpace(requestID) == "" {
		return errors.New("agentruntime/runtimes/codex: empty requestID")
	}
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil {
		return agentruntime.ErrNoActiveTurn
	}
	a.mu.Lock()
	userInput := a.userInput
	a.mu.Unlock()
	if userInput == nil {
		return agentruntime.ErrNoActiveTurn
	}
	waiter := a.askWaiter(requestID)
	if waiter == nil {
		return fmt.Errorf("agentruntime/runtimes/codex: no waiting request_user_input for requestID %s: %w", requestID, agentruntime.ErrWaiterNotFound)
	}
	if len(questions) > 0 && len(questions) != len(waiter.questions) {
		return fmt.Errorf("agentruntime/runtimes/codex: client supplied %d questions but waiter recorded %d", len(questions), len(waiter.questions))
	}
	if skipped {
		return a.submitResolved(
			func() error { return userInput.SubmitUserInput(ctx, requestID, map[string][]string{}) },
			agentruntime.UserAskResolved{RequestID: requestID, Skipped: true},
			func() { a.removeAskWaiter(requestID) },
		)
	}
	payload, err := buildUserInputAnswers(waiter.questions, answers)
	if err != nil {
		return err
	}
	return a.submitResolved(
		func() error { return userInput.SubmitUserInput(ctx, requestID, payload) },
		agentruntime.UserAskResolved{RequestID: requestID, Answers: answers},
		func() { a.removeAskWaiter(requestID) },
	)
}

func (r *Runtime) SubmitToolPermission(ctx context.Context, sessionID int64, requestID string, allow, alwaysAllowSession bool, _ string) error {
	if sessionID <= 0 {
		return fmt.Errorf("agentruntime/runtimes/codex: invalid sessionID %d", sessionID)
	}
	if strings.TrimSpace(requestID) == "" {
		return errors.New("agentruntime/runtimes/codex: empty requestID")
	}
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil {
		return agentruntime.ErrNoActiveTurn
	}
	a.mu.Lock()
	approval := a.approval
	a.mu.Unlock()
	if approval == nil {
		return agentruntime.ErrNoActiveTurn
	}
	if !a.hasPermWaiter(requestID) {
		return fmt.Errorf("agentruntime/runtimes/codex: no waiting approval for requestID %s: %w", requestID, agentruntime.ErrWaiterNotFound)
	}
	return a.submitResolved(
		func() error { return approval.SubmitApproval(ctx, requestID, allow, alwaysAllowSession) },
		agentruntime.ToolPermissionResolved{
			RequestID:   requestID,
			Allowed:     allow,
			AlwaysAllow: alwaysAllowSession,
		},
		func() { a.removePermWaiter(requestID) },
	)
}

// PendingWaiters 实现 agentruntime.WaiterLister(R7):codex 的 app-server 协议
// 同时有 requestApproval 与 request_user_input 两类阻塞点,断连期间产生的待决策
// 要能被重连的客户端枚举出来重建审批/提问卡,而不只是按已知 requestID 回答。
// sessionID 不在 active 表里(未起轮 / 已结束)返回零值快照 —— 与「此刻没有待
// 决策」同义,不是错误。
func (r *Runtime) PendingWaiters(_ context.Context, sessionID int64) agentruntime.WaiterSnapshot {
	if sessionID <= 0 {
		return agentruntime.WaiterSnapshot{}
	}
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil {
		return agentruntime.WaiterSnapshot{}
	}
	return a.pendingWaiters()
}

// pendingWaiters 在 a.mu 下快照两张 waiter 表。切片内顺序不定(map 迭代),需要
// 稳定顺序的调用方自己按 RequestID 排序。
func (a *codexActive) pendingWaiters() agentruntime.WaiterSnapshot {
	var snap agentruntime.WaiterSnapshot
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.permWaiters) > 0 {
		snap.ToolPermissions = make([]agentruntime.PendingToolPermission, 0, len(a.permWaiters))
		for reqID, w := range a.permWaiters {
			snap.ToolPermissions = append(snap.ToolPermissions, agentruntime.PendingToolPermission{
				RequestID: reqID,
				ToolName:  w.toolName,
				Input:     append(json.RawMessage(nil), w.rawInput...),
			})
		}
	}
	if len(a.askWaiters) > 0 {
		snap.AskUserQuestions = make([]agentruntime.PendingAskUserQuestion, 0, len(a.askWaiters))
		for reqID, w := range a.askWaiters {
			snap.AskUserQuestions = append(snap.AskUserQuestions, agentruntime.PendingAskUserQuestion{
				RequestID: reqID,
				Questions: append([]agentruntime.AskQuestion(nil), w.questions...),
			})
		}
	}
	return snap
}

// drainStream 与顶层 drainCodexStream 同构,emit 类型升级到 sealed Event。
func drainStream(stream cxStream, out chan<- agentruntime.Event, result *agentruntime.RunResult, active *codexActive, collaborationMode string) {
	for stream.Next() {
		ev := stream.Event()
		if ev.Kind == codex.EventRequestResolved && ev.RequestResolved != nil {
			if active != nil {
				active.resolveServerRequest(*ev.RequestResolved)
			}
			continue
		}
		if result.StopErr != nil && codexEventShowsProgressAfterError(ev.Kind) {
			result.StopErr = nil
		}
		if ev.Kind == codex.EventUserMessage {
			// codex 把 user message echo 回来 —— 对照 pending steer FIFO,
			// 命中就 emit SteerConsumed,让 chat_svc 把对应 queued 状态推进到 consumed。
			if active != nil {
				if steer, ok := active.consumePendingSteer(ev.Text); ok {
					out <- agentruntime.SteerConsumed{Steers: []agentruntime.ConsumedSteer{steer}}
				}
			}
			continue
		}
		if ev.ContextWindow > result.ContextWindow {
			result.ContextWindow = ev.ContextWindow
			out <- agentruntime.ContextWindowUpdated{Tokens: ev.ContextWindow}
		}
		translated, usage, stopErr := translate(ev)
		for _, t := range translated {
			t = attachPlanModeActions(t, collaborationMode)
			// UserAskRequest 同时登记 askWaiter,等 SubmitAnswer 反向唤醒。
			if uar, ok := t.(agentruntime.UserAskRequest); ok && active != nil {
				active.registerAskWaiter(uar.RequestID, uar.Questions)
			}
			if tpr, ok := t.(agentruntime.ToolPermissionRequest); ok && active != nil {
				active.registerPermWaiter(tpr.RequestID, tpr.ToolName, tpr.Input)
			}
			out <- t
		}
		if usage != nil {
			result.Usage = usage
		}
		if stopErr != nil {
			result.StopErr = stopErr
		}
	}
}

func codexEventShowsProgressAfterError(kind codex.EventKind) bool {
	switch kind {
	case codex.EventTextDelta,
		codex.EventThinkingDelta,
		codex.EventPreToolUse,
		codex.EventPostToolUse,
		codex.EventUserMessage,
		codex.EventRequestUserInput,
		codex.EventApprovalRequest,
		codex.EventPlanUpdated,
		codex.EventRetry,
		codex.EventCompactBoundary:
		return true
	default:
		return false
	}
}

func (a *codexActive) registerPermWaiter(requestID, toolName string, rawInput json.RawMessage) {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.permWaiters == nil {
		a.permWaiters = map[string]codexPermWaiter{}
	}
	a.permWaiters[requestID] = codexPermWaiter{
		toolName: toolName,
		rawInput: append(json.RawMessage(nil), rawInput...),
	}
	if a.pool != nil && a.poolKey != "" {
		a.pool.MarkWaiting(a.poolKey)
	}
}

func (a *codexActive) hasPermWaiter(requestID string) bool {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.permWaiters[requestID]
	return ok
}

func (a *codexActive) removePermWaiter(requestID string) {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	a.mu.Lock()
	delete(a.permWaiters, requestID)
	waiting := len(a.permWaiters) > 0 || len(a.askWaiters) > 0
	a.mu.Unlock()
	if !waiting && a.pool != nil && a.poolKey != "" {
		a.pool.MarkActive(a.poolKey)
	}
}

func (a *codexActive) addPendingSteer(queuedID, text string) {
	if a == nil || queuedID == "" {
		return
	}
	a.mu.Lock()
	a.pending = append(a.pending, agentruntime.ConsumedSteer{QueuedID: queuedID, Text: text})
	a.mu.Unlock()
}

func (a *codexActive) removePendingSteer(queuedID string) {
	if a == nil || queuedID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, p := range a.pending {
		if p.QueuedID == queuedID {
			a.pending = append(a.pending[:i], a.pending[i+1:]...)
			return
		}
	}
}

func (a *codexActive) consumePendingSteer(text string) (agentruntime.ConsumedSteer, bool) {
	if a == nil {
		return agentruntime.ConsumedSteer{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.pending) == 0 {
		return agentruntime.ConsumedSteer{}, false
	}
	next := a.pending[0]
	if next.Text == "" {
		if strings.TrimSpace(text) == "" {
			return agentruntime.ConsumedSteer{}, false
		}
		next.Text = text
	} else if strings.TrimSpace(text) != next.Text {
		return agentruntime.ConsumedSteer{}, false
	}
	a.pending = a.pending[1:]
	return next, true
}

func (a *codexActive) registerAskWaiter(requestID string, questions []agentruntime.AskQuestion) {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.askWaiters == nil {
		a.askWaiters = map[string]codexAskWaiter{}
	}
	a.askWaiters[requestID] = codexAskWaiter{questions: append([]agentruntime.AskQuestion(nil), questions...)}
	if a.pool != nil && a.poolKey != "" {
		a.pool.MarkWaiting(a.poolKey)
	}
}

func (a *codexActive) askWaiter(requestID string) *codexAskWaiter {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	w, ok := a.askWaiters[requestID]
	if !ok {
		return nil
	}
	return &w
}

func (a *codexActive) removeAskWaiter(requestID string) {
	if a == nil || strings.TrimSpace(requestID) == "" {
		return
	}
	a.mu.Lock()
	delete(a.askWaiters, requestID)
	waiting := len(a.askWaiters) > 0 || len(a.permWaiters) > 0
	a.mu.Unlock()
	if !waiting && a.pool != nil && a.poolKey != "" {
		a.pool.MarkActive(a.poolKey)
	}
}

func (a *codexActive) setOut(out chan<- agentruntime.Event) {
	if a == nil {
		return
	}
	a.outMu.Lock()
	a.out = out
	a.outMu.Unlock()
}

// emitUserAskResolved serializes terminal request events with setOut(nil), so
// an accepted response cannot be silently dropped or race a channel close.
func emitUserAskResolved(a *codexActive, requestID string, skipped bool, answers []agentruntime.AskAnswer) error {
	ev := agentruntime.UserAskResolved{
		RequestID: requestID,
		Skipped:   skipped,
		Answers:   answers,
	}
	return a.emitResolved(ev)
}

func emitToolPermissionResolved(a *codexActive, requestID string, allowed, alwaysAllow bool, denyReason ...string) error {
	ev := agentruntime.ToolPermissionResolved{
		RequestID:   requestID,
		Allowed:     allowed,
		AlwaysAllow: alwaysAllow,
	}
	if len(denyReason) > 0 {
		ev.DenyReason = denyReason[0]
	}
	return a.emitResolved(ev)
}

func (a *codexActive) emitResolved(ev agentruntime.Event) error {
	if a == nil {
		return agentruntime.ErrNoActiveTurn
	}
	a.outMu.Lock()
	defer a.outMu.Unlock()
	if a.out == nil {
		return agentruntime.ErrNoActiveTurn
	}
	a.out <- ev
	return nil
}

func (a *codexActive) submitResolved(submit func() error, ev agentruntime.Event, onSuccess func()) error {
	if a == nil {
		return agentruntime.ErrNoActiveTurn
	}
	a.outMu.Lock()
	defer a.outMu.Unlock()
	if a.out == nil {
		return agentruntime.ErrNoActiveTurn
	}
	if err := submit(); err != nil {
		return err
	}
	if onSuccess != nil {
		onSuccess()
	}
	a.out <- ev
	return nil
}

func (a *codexActive) resolveServerRequest(resolved codex.RequestResolvedEvent) {
	switch resolved.Kind {
	case codex.RequestKindUserInput:
		if a.askWaiter(resolved.RequestID) == nil {
			return
		}
		a.removeAskWaiter(resolved.RequestID)
		_ = emitUserAskResolved(a, resolved.RequestID, true, nil)
	case codex.RequestKindApproval:
		if !a.hasPermWaiter(resolved.RequestID) {
			return
		}
		a.removePermWaiter(resolved.RequestID)
		_ = emitToolPermissionResolved(a, resolved.RequestID, false, false, "approval request resolved by Codex app-server without a decision")
	}
}

func (a *codexActive) clearWaiters() {
	if a == nil {
		return
	}
	a.mu.Lock()
	clear(a.askWaiters)
	clear(a.permWaiters)
	a.pending = nil
	a.mu.Unlock()
}

func codexStreamReusable(stream cxStream, stopErr error) bool {
	if reusable, ok := stream.(interface{ Reusable() bool }); ok && !reusable.Reusable() {
		return false
	}
	if errors.Is(stopErr, codex.ErrProcessDead) || errors.Is(stopErr, codex.ErrProtocol) {
		return false
	}
	var exitErr *codex.ExitError
	return !errors.As(stopErr, &exitErr)
}

// buildUserInputAnswers 把前端 AskAnswer 列表拼成 codex 期望的
// map[questionID][]string。镜像顶层 codex.go.buildCodexUserInputAnswers。
func buildUserInputAnswers(questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer) (map[string][]string, error) {
	if len(answers) == 0 {
		return nil, errors.New("agentruntime/runtimes/codex: empty answers")
	}
	result := make(map[string][]string, len(answers))
	for _, ans := range answers {
		if ans.QuestionIndex < 0 || ans.QuestionIndex >= len(questions) {
			return nil, fmt.Errorf("agentruntime/runtimes/codex: answer question index %d out of range (have %d questions)", ans.QuestionIndex, len(questions))
		}
		if len(ans.Labels) == 0 {
			return nil, fmt.Errorf("agentruntime/runtimes/codex: question %d has no selected labels", ans.QuestionIndex)
		}
		q := questions[ans.QuestionIndex]
		if strings.TrimSpace(q.ID) == "" {
			return nil, fmt.Errorf("agentruntime/runtimes/codex: question %d missing codex id", ans.QuestionIndex)
		}
		seen := make(map[string]struct{}, len(ans.Labels))
		values := make([]string, 0, len(ans.Labels))
		for _, label := range ans.Labels {
			value := label
			if label == agentruntime.OtherAnswerLabel {
				if strings.TrimSpace(ans.OtherText) == "" {
					return nil, fmt.Errorf("agentruntime/runtimes/codex: question %d picked %q with empty OtherText", ans.QuestionIndex, agentruntime.OtherAnswerLabel)
				}
				value = ans.OtherText
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			values = append(values, value)
		}
		result[q.ID] = values
	}
	return result, nil
}
