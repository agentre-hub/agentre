// Package builtin 是 in-process agent runtime,通过 cago agents/app/coding 跑工具
// 循环,emit sealed agentruntime.Event。本包 init() 时把 *Runtime 注册到
// agentruntime.RuntimeFor。
package builtin

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/app/coding"
	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentprovider"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

var defaultRuntime = New()

func init() {
	agentruntime.RegisterRuntime(agent_backend_entity.TypeBuiltin, defaultRuntime)
}

// builtinEffectiveModel 统一模型解析规则(builtin 版):#26 会话级模型覆盖已移除,
// effectiveModel 取执行侧解析结果(EffectiveLLMConfig v1 seam)的 ModelID。builtin
// 强制要求绑 provider (Run 里 nil 检查),且 backend 无默认模型字段(DefaultModel
// 仅 claudecode 用)。
func builtinEffectiveModel(req agentruntime.RunRequest) string {
	return req.EffectiveModelID()
}

// builtinProviderBuilder 是 agentprovider.Build 的间接引用;测试时换成 fake provider,
// 避免真打 LLM 网络。
var builtinProviderBuilder = func(p *llm_provider_entity.LLMProvider) (provider.Provider, error) {
	return agentprovider.Build(p)
}

// SetBuiltinProviderBuilderForTest 仅供单测用。
func SetBuiltinProviderBuilderForTest(b func(*llm_provider_entity.LLMProvider) (provider.Provider, error)) {
	builtinProviderBuilder = b
}

// ResetBuiltinProviderBuilderForTest 恢复默认。
func ResetBuiltinProviderBuilderForTest() {
	builtinProviderBuilder = func(p *llm_provider_entity.LLMProvider) (provider.Provider, error) {
		return agentprovider.Build(p)
	}
}

// builtinSteerable 是 *agent.Runner 的窄接口,测试可注入 fake。
type builtinSteerable interface {
	Steer(ctx context.Context, text string, opts ...agent.SteerOption) error
	RemovePendingSteer(id string) bool
	ClearPendingSteers() []string
}

type builtinActive struct {
	runner builtinSteerable
	// cancel 取消本轮 turn 的 ctx。Run 用 context.WithCancel 派生 turnCtx 给
	// runner.Send,cago 的 LLM 调用监听 ctx 退出。Abort 通过它解锁阻塞读。
	cancel context.CancelFunc
	// turnToken 本会话当前活跃轮的 per-turn token(决策 1):每轮 Run 入口递增,值随
	// RunResult 暴露给 chat_svc。Abort(turnToken!=0) 只在该 token 仍是当前活跃轮时
	// 才中断,否则 stale no-op。builtin 每会话同时至多一轮、只有用户轮。
	turnToken atomic.Uint64
}

// Runtime in-process cago runtime 实现。
type Runtime struct {
	mu     sync.Mutex
	active map[int64]*builtinActive
}

// New 构造一个新 Runtime。
func New() *Runtime {
	return &Runtime{active: map[int64]*builtinActive{}}
}

// Capabilities 返回 builtin runtime 的能力矩阵。
//
// 见 runtime_test.go 的 TestBuiltinCapabilities 注释:steer / cancel / abort
// 是 in-process 单 provider 模式天生支持的,其它 cap 都不参与。
func (r *Runtime) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Set: map[capability.Capability]bool{
			capability.CapSteer:       true,
			capability.CapCancelSteer: true,
			capability.CapAbort:       true,
			capability.CapImageInput:  true,
			// 力度直接交给 cago runner 的模型参数(见 Run 里的 ReasoningEffort 分支)。
			capability.CapReasoningEffort: true,
		},
	}
}

// Steer 把 (queuedID, text) 投递给 cago agent.Runner,由 cago 在下个安全点
// (tool 完成 / 一轮 LLM 文本结束)注入。语义同顶层 builtin.go.Steer。
func (r *Runtime) Steer(ctx context.Context, sessionID int64, queuedID, text string) error {
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil || a.runner == nil {
		return agentruntime.ErrNoActiveTurn
	}
	if err := a.runner.Steer(ctx, text, agent.WithSteerID(queuedID)); err != nil {
		if errors.Is(err, agent.ErrSteerNoActiveTurn) {
			return agentruntime.ErrNoActiveTurn
		}
		return err
	}
	return nil
}

// Abort 中止当前正在跑的 turn。语义同顶层 builtin.go.Abort:
//  1. ClearPendingSteers 把还没被 cago 消费的 steer chip 清空;
//  2. cancel turnCtx —— cago Runner.Send 监听 ctx,events channel 关闭,drain
//     goroutine 退出。
//
// turnToken 语义(决策 1):0 = 中断当前活跃轮;非 0 = 仅当该轮仍是当前活跃轮才中断,
// 否则 stale no-op。builtin 只有用户轮,被中断轮类型恒为 userTurn。
//
// 幂等;并发安全。
func (r *Runtime) Abort(_ context.Context, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	if turnToken != 0 && a.turnToken.Load() != turnToken {
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, nil
	}
	if a.runner != nil {
		_ = a.runner.ClearPendingSteers()
	}
	if a.cancel != nil {
		a.cancel()
	}
	return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}, nil
}

// CancelSteer 撤回尚未被消费的 steer 条目。语义同顶层 builtin.go.CancelSteer:
//   - queuedID == "":清空,返回被清的 ID 列表
//   - queuedID 非空:不在队列里返 ErrSteerNotFound
func (r *Runtime) CancelSteer(_ context.Context, sessionID int64, queuedID string) ([]string, error) {
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil || a.runner == nil {
		return nil, agentruntime.ErrNoActiveTurn
	}
	if queuedID == "" {
		return a.runner.ClearPendingSteers(), nil
	}
	if a.runner.RemovePendingSteer(queuedID) {
		return []string{queuedID}, nil
	}
	return nil, agentruntime.ErrSteerNotFound
}

func (r *Runtime) register(sessionID int64, a *builtinActive) {
	if sessionID <= 0 {
		return
	}
	r.mu.Lock()
	r.active[sessionID] = a
	r.mu.Unlock()
}

func (r *Runtime) unregister(sessionID int64) {
	if sessionID <= 0 {
		return
	}
	r.mu.Lock()
	delete(r.active, sessionID)
	r.mu.Unlock()
}

// Run in-process 跑一轮 cago agent;emit 新 sealed agentruntime.Event。
//
// 与现有顶层 builtin.go.Run 平行,唯一差异是事件类型:
//   - 用 translate() 把 cago agent.Event 翻成 0/1 个 sealed Event;
//   - 同安全点连续到达的多帧 SteerConsumed 在 Run() 层合并(per Part 0 §1.10),
//     保持单批 emit 的 wire 行为(避免下游被迫处理多条窄帧)。
//
// chat history 由 chat_svc 透传,cago Session 无持久化,所以每轮 LoadConversation
// 重建。
func (r *Runtime) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	if req.Backend == nil {
		return nil, nil, fmt.Errorf("agentruntime/runtimes/builtin: nil backend")
	}
	if req.Provider == nil {
		return nil, nil, fmt.Errorf("agentruntime/runtimes/builtin: nil provider")
	}
	p, err := builtinProviderBuilder(req.Provider)
	if err != nil {
		logger.Ctx(ctx).Error("builtin runtime: provider builder failed",
			zap.Int64("sessionID", req.SessionID),
			zap.String("providerType", req.Provider.Type),
			zap.String("model", req.EffectiveModelID()), zap.Error(err))
		return nil, nil, err
	}

	cwd := req.Cwd
	if cwd == "" {
		cwd, err = agentruntime.ResolveAgentCwd(req.AgentID, req.AgentSyncID)
		if err != nil {
			logger.Ctx(ctx).Error("builtin runtime: agent cwd resolve failed",
				zap.Int64("sessionID", req.SessionID),
				zap.Int64("agentID", req.AgentID), zap.Error(err))
			return nil, nil, err
		}
	}

	opts := []coding.Option{}
	if sys := strings.TrimSpace(req.SystemPrompt); sys != "" {
		opts = append(opts, coding.AppendSystem(sys))
	}
	if model := builtinEffectiveModel(req); model != "" {
		opts = append(opts, coding.WithModel(model))
	}
	if eff := req.Backend.ReasoningEffort; eff != "" {
		opts = append(opts, coding.WithThinking(&provider.ThinkingConfig{
			Effort: provider.ThinkingEffort(eff),
		}))
	}
	sysApp, err := coding.New(ctx, p, cwd, opts...)
	if err != nil {
		logger.Ctx(ctx).Error("builtin runtime: coding.New failed",
			zap.Int64("sessionID", req.SessionID),
			zap.String("cwd", cwd), zap.Error(err))
		return nil, nil, err
	}

	history := make([]agent.Message, 0, len(req.History))
	for _, h := range req.History {
		history = append(history, agent.Message{Role: agent.Role(h.Role), Content: h.Blocks})
	}
	convID := req.ProviderSessionID
	if convID == "" {
		convID = fmt.Sprintf("builtin-%d", req.SessionID)
	}
	conv := agent.LoadConversation(convID, history)
	runner, err := sysApp.Agent().TryRunner(conv)
	if err != nil {
		logger.Ctx(ctx).Error("builtin runtime: TryRunner failed",
			zap.Int64("sessionID", req.SessionID),
			zap.String("convID", convID), zap.Error(err))
		_ = sysApp.Close(ctx)
		return nil, nil, err
	}

	turnCtx, cancelTurn := context.WithCancel(ctx)
	var events iter.Seq[agent.Event]
	var sendErr error
	if len(req.UserBlocks) > 0 {
		events, sendErr = runner.Send(turnCtx, "", agent.WithBlocks(req.UserBlocks...))
	} else {
		events, sendErr = runner.Send(turnCtx, req.UserText)
	}
	if sendErr != nil {
		logger.Ctx(ctx).Error("builtin runtime: runner.Send failed",
			zap.Int64("sessionID", req.SessionID), zap.Error(sendErr))
		cancelTurn()
		_ = runner.Close()
		_ = sysApp.Close(ctx)
		return nil, nil, sendErr
	}
	logger.Ctx(ctx).Info("builtin.Runtime: turn started",
		zap.Int64("sessionId", req.SessionID),
		zap.String("convID", convID),
		zap.Int("historyLen", len(history)))

	ba := &builtinActive{runner: runner, cancel: cancelTurn}
	r.register(req.SessionID, ba)

	out := make(chan agentruntime.Event, 32)
	result := &agentruntime.RunResult{
		ProviderSessionID: convID,
		Model:             builtinEffectiveModel(req),
		TurnToken:         ba.turnToken.Add(1),
	}
	go func() {
		defer close(out)
		defer r.unregister(req.SessionID)
		defer cancelTurn()
		defer func() { _ = runner.Close() }()
		defer func() { _ = sysApp.Close(ctx) }()

		// steerBatch 收集同安全点的连续 SteerConsumed,直到下一个非 steer 事件
		// 到来时一次性 flush 成单帧 emit —— 与顶层 builtin.go 230-272 行为一致。
		var steerBatch []agentruntime.ConsumedSteer
		flushSteers := func() {
			if len(steerBatch) == 0 {
				return
			}
			out <- agentruntime.SteerConsumed{Steers: steerBatch}
			steerBatch = nil
		}

		for ev := range events {
			translated := translate(ev)
			for _, t := range translated {
				if sc, ok := t.(agentruntime.SteerConsumed); ok {
					steerBatch = append(steerBatch, sc.Steers...)
					continue
				}
				flushSteers()
				out <- t
			}
			// 终态写回 *RunResult:Usage 在 EventTurnEnd 携带;StopErr 在
			// EventError(ErrorEvent 已经通过 translate 下发,这里只补 RunResult)。
			switch ev.Kind {
			case agent.EventTurnEnd:
				if ev.Usage != nil {
					result.Usage = ev.Usage
				}
			case agent.EventError:
				if ev.Error != nil {
					result.StopErr = ev.Error
				}
			}
		}
		flushSteers()
		logger.Ctx(ctx).Info("builtin.Runtime: turn completed",
			zap.Int64("sessionId", req.SessionID),
			zap.String("providerSessionId", result.ProviderSessionID),
			zap.String("model", result.Model),
			zap.Bool("usageAvailable", result.Usage != nil),
		)
	}()
	return out, result, nil
}
