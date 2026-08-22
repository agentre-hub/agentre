package piagent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/piagent/mcpbridge"
	pkgpi "github.com/agentre-ai/agentre/pkg/piagent"
)

var defaultRuntime = New()

func init() {
	agentruntime.RegisterRuntime(agent_backend_entity.TypePiAgent, defaultRuntime)
}

type activeSession struct {
	mu             sync.Mutex
	stream         steerStream
	interrupter    interruptable
	pending        []agentruntime.ConsumedSteer
	abortRequested bool
	// turnToken 本会话当前活跃轮的 per-turn token(决策 1):每轮入口递增,值随
	// RunResult 暴露给 chat_svc。Abort(turnToken!=0) 只在该 token 仍是当前活跃轮时
	// 才中断,否则 stale no-op。piagent 只有用户轮,被中断轮类型恒为 userTurn。
	turnToken atomic.Uint64
}

type Runtime struct {
	mu       sync.Mutex
	active   map[int64]*activeSession
	prepared map[int64]*preparedRun
}

type PreparedRun interface {
	Start(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error)
	Close(context.Context) error
}

type RunPreparer interface {
	PrepareRun(context.Context, agentruntime.RunRequest) (PreparedRun, error)
}

// PreparedRunIdentity is the optional pre-prompt identity boundary implemented
// by Pi prepared runs. Callers can persist the validated native session before
// Start transmits the prompt without widening the generic runtime contract.
type PreparedRunIdentity interface {
	PreparedRun
	ProviderSessionID() string
}

type preparedRun struct {
	runtime           *Runtime
	req               agentruntime.RunRequest
	sess              sessionHandle
	prepared          preparedTurnStream
	cwd               string
	modelID           string
	providerSessionID string

	startMu sync.Mutex
	started bool
	closed  bool
	close   sync.Once
}

func New() *Runtime {
	return &Runtime{
		active:   map[int64]*activeSession{},
		prepared: map[int64]*preparedRun{},
	}
}

func (r *Runtime) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Set: map[capability.Capability]bool{
			capability.CapSteer:               true,
			capability.CapAbort:               true,
			capability.CapImageInput:          true,
			capability.CapCompact:             true,
			capability.CapReportContextWindow: true,
			capability.CapForkSession:         true,
			capability.CapMCPTools:            true,
		},
	}
}

func (r *Runtime) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	prepared, err := r.PrepareRun(ctx, req)
	if err != nil {
		return nil, nil, err
	}
	return prepared.Start(ctx)
}

func (r *Runtime) PrepareRun(ctx context.Context, req agentruntime.RunRequest) (PreparedRun, error) {
	if req.Backend == nil {
		return nil, fmt.Errorf("agentruntime/runtimes/piagent: nil backend")
	}
	cwd := req.Cwd
	if cwd == "" {
		var err error
		cwd, err = agentruntime.ResolveAgentCwd(req.AgentID, req.AgentSyncID)
		if err != nil {
			return nil, err
		}
	}
	env, err := BuildPiAgentEnv(req.Backend)
	if err != nil {
		logger.Ctx(ctx).Error("piagent runtime: BuildPiAgentEnv failed", zap.Int64("sessionID", req.SessionID), zap.Error(err))
		return nil, err
	}
	// 绑定供应商：APIKey 空视为配置错误（消息只含 provider key，不含密钥）；
	// 否则把 AGENTRE_PI_API_KEY_* 注入本次子进程 env（密钥永不落盘）。
	// Effective 在 CLI 登录态也会以 Mode=native 的非 nil 配置下发，因此必须按
	// ProviderKey 判断是否真的绑定了 Agentre Provider，不能把非 nil 等同于已绑定。
	if effective := req.Effective; effective != nil {
		if providerKey := strings.TrimSpace(effective.ProviderKey); providerKey != "" {
			if strings.TrimSpace(effective.APIKey) == "" {
				return nil, fmt.Errorf("piagent runtime: provider %q has empty APIKey", providerKey)
			}
			env = agentruntime.BuildPiAgentProviderEnv(env, effective)
		}
	}
	sess, err := sessionFactory(req, env, cwd)
	if err != nil {
		logger.Ctx(ctx).Error("piagent runtime: session factory failed", zap.Int64("sessionID", req.SessionID), zap.String("cwd", cwd), providerKeyField(req), zap.Error(err))
		return nil, err
	}
	modelID := piResultModelPlaceholder(req)
	prepared := &preparedRun{
		runtime:           r,
		req:               req,
		sess:              sess,
		cwd:               cwd,
		modelID:           modelID,
		providerSessionID: strings.TrimSpace(sess.ID()),
	}
	r.registerPrepared(req.SessionID, prepared)
	if !req.Compact {
		if preparer, ok := sess.(turnStreamPreparer); ok {
			preparedStream, err := preparer.PrepareStreamTurn(
				ctx,
				req.UserText,
				req.CollaborationMode,
				extractImages(req.UserBlocks),
				turnSpec{forkAnchor: req.ForkAnchor},
			)
			if err != nil {
				_ = prepared.Close(context.Background())
				return nil, mapSessionError(err)
			}
			prepared.prepared = preparedStream
			prepared.providerSessionID = strings.TrimSpace(preparedStream.SessionID())
			if prepared.providerSessionID == "" {
				_ = prepared.Close(context.Background())
				return nil, errors.New("piagent runtime: prepared stream returned empty provider session id")
			}
		}
	}
	return prepared, nil
}

func (p *preparedRun) ProviderSessionID() string {
	if p == nil {
		return ""
	}
	return p.providerSessionID
}

func (p *preparedRun) Start(ctx context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	p.startMu.Lock()
	if p.closed {
		p.startMu.Unlock()
		return nil, nil, errors.New("piagent runtime: prepared run closed")
	}
	if p.started {
		p.startMu.Unlock()
		return nil, nil, errors.New("piagent runtime: prepared run already started")
	}
	p.started = true
	p.startMu.Unlock()

	var (
		s   stream
		err error
	)
	switch {
	case p.req.Compact:
		s, err = p.sess.Compact(ctx)
	case p.prepared != nil:
		s, err = p.prepared.Start(ctx)
	default:
		s, err = p.sess.StreamTurn(
			ctx,
			p.req.UserText,
			p.req.CollaborationMode,
			extractImages(p.req.UserBlocks),
			turnSpec{forkAnchor: p.req.ForkAnchor},
		)
	}
	if err != nil {
		_ = p.Close(context.Background())
		return nil, nil, mapSessionError(err)
	}
	active := &activeSession{stream: p.sess.ActiveStream(), interrupter: p.sess.ActiveInterruptor()}
	p.runtime.register(p.req.SessionID, active)

	out := make(chan agentruntime.Event, 32)
	providerSessionID := p.providerSessionID
	if providerSessionID == "" {
		providerSessionID = strings.TrimSpace(p.sess.ID())
	}
	result := &agentruntime.RunResult{ProviderSessionID: providerSessionID, Model: p.modelID, TurnToken: active.turnToken.Add(1)}
	logFields := make([]zap.Field, 0, 7)
	logFields = append(logFields,
		zap.Int64("sessionID", p.req.SessionID),
		zap.Int64("agentID", p.req.AgentID),
		zap.String("cwd", p.cwd),
		zap.String("providerSessionID", result.ProviderSessionID),
		zap.String("model", result.Model),
		zap.Bool("compact", p.req.Compact),
	)
	logFields = append(logFields, providerKeyField(p.req))
	logger.Ctx(ctx).Info("piagent runtime: turn starting", logFields...)

	go func() {
		defer close(out)
		defer p.runtime.unregister(p.req.SessionID, active)
		defer func() { _ = p.Close(context.Background()) }()
		drainStream(ctx, p.req, p.cwd, s, out, result, active)
	}()
	return out, result, nil
}

func (p *preparedRun) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.startMu.Lock()
	p.closed = true
	p.startMu.Unlock()
	var closeErr error
	p.close.Do(func() {
		if p.prepared != nil {
			closeErr = p.prepared.Close(ctx)
		}
		if err := p.sess.Close(ctx); closeErr == nil && err != nil {
			closeErr = err
		}
		ownsPreparedRegistration := p.runtime.unregisterPrepared(p.req.SessionID, p)
		if ownsPreparedRegistration && len(p.req.MCPServers) > 0 {
			if err := mcpbridge.RemoveConfig(p.req.SessionID); closeErr == nil && err != nil {
				closeErr = err
			}
		}
	})
	return closeErr
}

func (r *Runtime) Abort(ctx context.Context, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil || a.interrupter == nil {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	if turnToken != 0 && a.turnToken.Load() != turnToken {
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, nil
	}
	a.setAbortRequested(true)
	if err := a.interrupter.Interrupt(ctx); err != nil {
		a.setAbortRequested(false)
		return agentruntime.AbortOutcome{}, err
	}
	return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}, nil
}

func (r *Runtime) Steer(ctx context.Context, sessionID int64, queuedID string, text string) error {
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil || a.stream == nil {
		return agentruntime.ErrNoActiveTurn
	}
	a.addPending(queuedID, text)
	if err := a.stream.Steer(ctx, text); err != nil {
		a.removePending(queuedID)
		return err
	}
	return nil
}

func (r *Runtime) register(sessionID int64, a *activeSession) {
	if sessionID <= 0 {
		return
	}
	r.mu.Lock()
	r.active[sessionID] = a
	r.mu.Unlock()
}

func (r *Runtime) unregister(sessionID int64, owner *activeSession) {
	if sessionID <= 0 || owner == nil {
		return
	}
	r.mu.Lock()
	if r.active[sessionID] == owner {
		delete(r.active, sessionID)
	}
	r.mu.Unlock()
}

// CloseSession 放掉某条会话此刻在飞的那一轮的 RPC 进程。会话被删除时由释放广播调到
// (agentruntime.CloseSessionEverywhere):会话都没了,这个进程再也不会有人用。
func (r *Runtime) CloseSession(ctx context.Context, sessionID int64) {
	if sessionID <= 0 {
		return
	}
	r.mu.Lock()
	owner := r.prepared[sessionID]
	r.mu.Unlock()
	if owner == nil {
		return
	}
	if err := owner.Close(ctx); err != nil {
		logger.Ctx(ctx).Warn("piagent runtime: close session failed",
			zap.Int64("sessionID", sessionID), zap.Error(err))
	}
}

// CloseAllSessions 收掉此刻在飞的每一轮的 RPC 进程,宿主关机时调。
//
// pi 的进程不进 CLISessionPool(每轮一个,轮末就关),所以宿主那两条只扫池的收尾路径
// 都够不着它:确认退出时正在跑的一轮,收尾靠的是 Start 那个 goroutine 里的 defer
// Close —— 宿主进程先它一步退出,而 pi 自带进程组、不会被连坐,留下的就是孤儿。
func (r *Runtime) CloseAllSessions(ctx context.Context) {
	r.mu.Lock()
	owners := make([]*preparedRun, 0, len(r.prepared))
	for _, owner := range r.prepared {
		owners = append(owners, owner)
	}
	r.mu.Unlock()
	for _, owner := range owners {
		if err := owner.Close(ctx); err != nil {
			logger.Ctx(ctx).Warn("piagent runtime: close session failed on shutdown", zap.Error(err))
		}
	}
}

func (r *Runtime) registerPrepared(sessionID int64, owner *preparedRun) {
	if sessionID <= 0 || owner == nil {
		return
	}
	r.mu.Lock()
	r.prepared[sessionID] = owner
	r.mu.Unlock()
}

func (r *Runtime) unregisterPrepared(sessionID int64, owner *preparedRun) bool {
	if sessionID <= 0 || owner == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prepared[sessionID] != owner {
		return false
	}
	delete(r.prepared, sessionID)
	return true
}

func (a *activeSession) setAbortRequested(requested bool) {
	a.mu.Lock()
	a.abortRequested = requested
	a.mu.Unlock()
}

func (a *activeSession) wasAbortRequested() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.abortRequested
}

func (a *activeSession) addPending(id, text string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pending = append(a.pending, agentruntime.ConsumedSteer{QueuedID: id, Text: text})
}

func (a *activeSession) removePending(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := a.pending[:0]
	for _, it := range a.pending {
		if it.QueuedID != id {
			out = append(out, it)
		}
	}
	a.pending = out
}

// consumePendingSteer 按 FIFO 找第一条文本匹配的 pending steer，命中即移除并返回。
// 只有 Pi 真正把 steer 注入对话（回显成 EventUserMessage）时才调用，避免助手输出
// 文字恰好等于 steer 文本造成误判。
func (a *activeSession) consumePendingSteer(text string) (agentruntime.ConsumedSteer, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, it := range a.pending {
		if it.Text == text {
			a.pending = append(a.pending[:i], a.pending[i+1:]...)
			return it, true
		}
	}
	return agentruntime.ConsumedSteer{}, false
}

func drainStream(ctx context.Context, req agentruntime.RunRequest, _ string, s stream, out chan<- agentruntime.Event, result *agentruntime.RunResult, active *activeSession) {
	var usage *provider.Usage
	var stopErr error
	trackers := make(map[string]*subagentTracker)
	for s.Next() {
		raw := s.Event()
		if raw.Kind == pkgpi.EventUserMessage {
			// Pi 把 steer 注入回显成 user message；对照 pending FIFO 命中即 consumed。
			if active != nil {
				if steer, ok := active.consumePendingSteer(raw.Text); ok {
					out <- agentruntime.SteerConsumed{Steers: []agentruntime.ConsumedSteer{steer}}
				}
			}
			continue
		}
		contextWindowChanged := raw.ContextWindow > 0 && raw.ContextWindow != result.ContextWindow
		if contextWindowChanged {
			// Usage snapshots also carry the authoritative Pi window so it survives
			// a missing/failed round-end stats refresh and is persisted by chat_svc.
			result.ContextWindow = raw.ContextWindow
		}
		if raw.Kind == pkgpi.EventContextWindow && !contextWindowChanged {
			// Context window 未变化时不重复向前端 emit patch。
			raw.ContextWindow = 0
		}
		if raw.Kind == pkgpi.EventDone {
			// pkg/piagent 用 EventDone 标记底层流终止；runtime 在 loop 结束后统一
			// emit agentruntime.Done，避免向 chat_svc 重复发送 message_end。
			continue
		}
		if handleSubagentToolEvent(raw, out, trackers) {
			continue
		}
		if raw.Model != "" {
			// Pi 在 usage 帧上报真实模型 id；piagent 不绑 provider，靠这里把模型回
			// 吐给 chat_svc（result.Model → assistantMsg.Model）。上下文窗口只采用
			// Pi RPC get_state / get_session_stats 返回值，避免自定义 provider 复用
			// 公共模型名时被 Agentre catalog 的同名模型元数据错误覆盖。
			// 绑 provider 时上报值带 "agentre-<key>/" 前缀，剥掉后再吐给 chat_svc，
			// 让 transcript 的 model 字段是面向用户的原始模型 id（见 piUserModelID）。
			result.Model = piUserModelID(req, raw.Model)
		}
		events, u, err := translate(raw)
		for _, ev := range events {
			out <- ev
		}
		if u != nil {
			usage = u
		}
		if err != nil {
			stopErr = err
		}
	}
	if anchorStream, ok := s.(userAnchorStream); ok {
		result.UserAnchor = anchorStream.UserAnchor()
	}
	if err := s.Err(); err != nil && stopErr == nil {
		stopErr = err
	}
	if active != nil && active.wasAbortRequested() {
		stopErr = agentruntime.ErrAborted
	}
	if usage != nil {
		result.Usage = usage
	}
	if stopErr != nil {
		stopErr = mapSessionError(stopErr)
		result.StopErr = stopErr
		if errors.Is(stopErr, agentruntime.ErrAborted) {
			finalizeAbortedSubagents(out, trackers)
		} else {
			finalizeIncompleteSubagents(out, trackers, true)
		}
		logPiFailureDiagnostics(ctx, req, s)
		logger.Ctx(ctx).Warn("piagent.drainStream: turn failed", piTurnLogFields(req, result, stopErr)...)
		out <- agentruntime.ErrorEvent{Err: stopErr}
		return
	}
	finalizeIncompleteSubagents(out, trackers, false)
	logger.Ctx(ctx).Info("piagent.drainStream: turn done", piTurnLogFields(req, result, nil)...)
	out <- agentruntime.Done{}
}

func finalizeAbortedSubagents(out chan<- agentruntime.Event, trackers map[string]*subagentTracker) {
	finalizeTrackedSubagents(out, trackers, func(tracker *subagentTracker) bool {
		return tracker.abort()
	})
}

func finalizeIncompleteSubagents(out chan<- agentruntime.Event, trackers map[string]*subagentTracker, turnFailed bool) {
	finalizeTrackedSubagents(out, trackers, func(tracker *subagentTracker) bool {
		return tracker.finishIncomplete(turnFailed)
	})
}

func finalizeTrackedSubagents(out chan<- agentruntime.Event, trackers map[string]*subagentTracker, finalize func(*subagentTracker) bool) {
	toolCallIDs := make([]string, 0, len(trackers))
	for toolCallID := range trackers {
		toolCallIDs = append(toolCallIDs, toolCallID)
	}
	sort.Strings(toolCallIDs)
	for _, toolCallID := range toolCallIDs {
		tracker := trackers[toolCallID]
		if finalize(tracker) {
			out <- agentruntime.SubagentProgress{ToolCallID: toolCallID, Info: tracker.info()}
		}
		out <- agentruntime.SubagentDone{ToolCallID: toolCallID, Info: tracker.info()}
		delete(trackers, toolCallID)
	}
}

func handleSubagentToolEvent(raw pkgpi.Event, out chan<- agentruntime.Event, trackers map[string]*subagentTracker) bool {
	switch raw.Kind {
	case pkgpi.EventPreToolUse:
		tracker, spawn := defaultSubagentSelector.selectCandidate(raw.Tool.Name, raw.Tool.ID, raw.Tool.Input)
		call := agentruntime.ToolCall{
			ID: raw.Tool.ID, Name: raw.Tool.Name, Input: raw.Tool.Input,
			Canonical: recognizeCanonical(raw.Tool.Name, raw.Tool.Input),
		}
		if tracker != nil {
			call.Canonical = *spawn
			trackers[raw.Tool.ID] = tracker
		}
		out <- call
		if tracker != nil {
			out <- agentruntime.SubagentStarted{ToolCallID: raw.Tool.ID, Info: tracker.info()}
		}
		return true
	case pkgpi.EventToolUseUpdate:
		tracker := trackers[raw.Tool.ID]
		if tracker == nil {
			return true
		}
		events, changed := tracker.consumeUpdate(raw.Tool.PartialResult)
		for _, event := range events {
			out <- event
		}
		if changed {
			out <- agentruntime.SubagentProgress{ToolCallID: raw.Tool.ID, Info: tracker.info()}
		}
		return true
	case pkgpi.EventPostToolUse:
		tracker := trackers[raw.Tool.ID]
		if tracker == nil {
			return false
		}
		events, changed := tracker.consumeFinal(raw.Tool.Details, raw.Tool.IsError, raw.Tool.Content)
		for _, event := range events {
			out <- event
		}
		if changed {
			out <- agentruntime.SubagentProgress{ToolCallID: raw.Tool.ID, Info: tracker.info()}
		}
		out <- agentruntime.SubagentDone{ToolCallID: raw.Tool.ID, Info: tracker.info()}
		delete(trackers, raw.Tool.ID)
		out <- agentruntime.ToolResult{ToolCallID: raw.Tool.ID, Content: raw.Tool.Content, IsError: raw.Tool.IsError}
		return true
	default:
		return false
	}
}

func mapSessionError(err error) error {
	if err == nil || !errors.Is(err, pkgpi.ErrSessionNotFound) {
		return err
	}
	return fmt.Errorf("%w: %w", agentruntime.ErrSessionNotFound, err)
}

type diagnosticsStream interface {
	Diagnostics() pkgpi.StreamDiagnostics
}

func logPiFailureDiagnostics(ctx context.Context, req agentruntime.RunRequest, s stream) {
	ds, ok := s.(diagnosticsStream)
	if !ok {
		return
	}
	d := ds.Diagnostics()
	if d.FinalErrorEventType == "" && d.FinalErrorStopReason == "" && d.FinalErrorFrame == "" {
		return
	}
	fields := []zap.Field{
		zap.Int64("sessionID", req.SessionID),
		zap.Int64("agentID", req.AgentID),
		zap.Bool("compact", req.Compact),
	}
	if d.FinalErrorEventType != "" {
		fields = append(fields, zap.String("piEventType", d.FinalErrorEventType))
	}
	if d.FinalErrorStopReason != "" {
		fields = append(fields, zap.String("piStopReason", d.FinalErrorStopReason))
	}
	// pkg/piagent 已把最终错误帧脱敏,这里只报体量,不复制内容。
	if d.FinalErrorFrame != "" {
		fields = append(fields, zap.Int("piFinalErrorFrameBytes", len(d.FinalErrorFrame)))
	}
	logger.Ctx(ctx).Debug("piagent.logPiFailureDiagnostics: turn failed diagnostics", fields...)
}

func providerKeyField(req agentruntime.RunRequest) zap.Field {
	if req.Effective != nil {
		return zap.String("providerKey", req.Effective.ProviderKey)
	}
	return zap.Skip()
}

func piTurnLogFields(req agentruntime.RunRequest, result *agentruntime.RunResult, err error) []zap.Field {
	fields := []zap.Field{
		zap.Int64("sessionID", req.SessionID),
		zap.Int64("agentID", req.AgentID),
		zap.Bool("compact", req.Compact),
	}
	fields = append(fields, providerKeyField(req))
	if result != nil {
		fields = append(fields,
			zap.String("providerSessionID", result.ProviderSessionID),
			zap.Int("contextWindow", result.ContextWindow),
		)
		if result.Usage != nil {
			fields = append(fields,
				zap.Int("promptTokens", result.Usage.PromptTokens),
				zap.Int("completionTokens", result.Usage.CompletionTokens),
				zap.Int("cachedTokens", result.Usage.CachedTokens),
				zap.Int("cacheCreationTokens", result.Usage.CacheCreationTokens),
				zap.Int("totalInputTokens", result.Usage.PromptTokens+result.Usage.CachedTokens+result.Usage.CacheCreationTokens),
			)
		}
	}
	if err != nil {
		fields = append(fields,
			zap.String("errorClass", fmt.Sprintf("%T", err)),
			zap.Int("errorBytes", len(err.Error())),
		)
	}
	return fields
}
