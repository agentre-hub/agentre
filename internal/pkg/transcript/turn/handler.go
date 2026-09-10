package turn

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/turnstats"
)

// Handler 处理一种 agentruntime.Event 类型。
//
// 参数:
//   - ctx:turn 的 context;handler 决定何时用 context.WithoutCancel 持久化(spec §1.4)
//   - ev:具体 Event 实例,handler 内做类型断言
//   - acc:累积器(text/thinking/blocks + mutateIndex)
//   - emit:Wails event 推送适配器
//   - view:canonical 投影(blocks → ChatBlock DTO)
//   - turnCtx:本轮 turn 上下文(assistantMsg / session / stream),持久化和 emit 需要
//
// Handler 不直接依赖 chat_repo;持久化由 turnCtx 携带的接口或 handler 内通过 ctx
// 值注入(具体接线见 chat_svc/dispatcher_adapters.go newTurnContext,以及
// chat_svc/turn_run.go 与 autonomous_turn_run.go 的 consumeEvents)。
type Handler interface {
	Apply(ctx context.Context, ev agentruntime.Event, acc *Accumulator, emit Emitter, view View, turnCtx *TurnContext) error
}

// Emitter 抽象 chat_svc.emitter,handler 不直接依赖具体 emit 路径,便于单测。
//
// stream 是 Wails event 名(turnCtx.Stream 即可,handler 通常透传);event 是
// 任意 JSON-marshallable payload。
type Emitter interface {
	Emit(ctx context.Context, stream string, event any)
}

// View 提供 canonical 投影 + ChatBlock 构造能力;具体实现在 chat_svc/view 包。
// dispatcher 这一层只看接口,避免循环依赖。
type View interface {
	// ProjectCanonical 把 agentruntime.Event(目前主要是 ToolCall.Canonical)
	// 投影成 wire DTO 的 kind + payload;handler 拼 emit 时调。
	ProjectCanonical(ev agentruntime.Event) (kind string, payload any)
}

// TurnContext 本轮 turn 的 mutable 上下文,handler 通过这个写 assistantMsg 字段、
// 决定 stream name、必要时回写 session 字段。
//
// 字段为 any 是为了避免 turn 子包反向依赖 chat_entity / chat_repo;chat_svc 层
// 把具体类型填进去,handler 内按需类型断言。
type TurnContext struct {
	AssistantMsg any // *chat_entity.ChatMessage
	Session      any // *chat_entity.ChatSession
	Stream       string

	// BackendType 是当前 turn 跑的 runtime 类型("claudecode" / "codex" / "builtin"
	// 等,字符串值与 agent_backend_entity.BackendType 一致)。handler 装配
	// canonical.Actions 时按这个分支(plan_update 的 Codex 路径要装 [execute,
	// refine],Claude 路径要 nil)。chat_svc.newTurnContext 注入。
	BackendType string

	// LaunchPermissionMode 是 session.PermissionModeAtLaunch 快照(claudecode 专用)。
	// ExitPlanMode 审批卡的 actions 列表按这个分支:bypass launch → 第一项给 bypass,
	// 否则给 acceptEdits。handler 不需要再 reach session 实体。
	LaunchPermissionMode string

	// LastPlanWriteContent 本轮 turn 内最近一次 Write 到 *.claude/plans/*.md 的
	// content。claudecode v2.1.x 起 ExitPlanMode 的 input 是 {}(plan 文本通过
	// 先前的 Write 工具写到 ~/.claude/plans/<slug>.md),buildToolPermissionCanonical
	// 在 input["plan"] 为空时回退到这个字段。per-turn 单 goroutine 写入,无锁。
	LastPlanWriteContent string

	// SessionUpdater 提供持久化能力;avoid import cycle 用 any。具体 method set 由
	// chat_svc 注入。
	SessionUpdater SessionUpdater

	// SessionTransitioner 切换 session waiting / running 状态。UserAsk /
	// ToolPermission Request handler 调 MarkWaiting;Resolved handler 调 MarkRunning。
	// chat_svc 在 newTurnContext 时注入。
	SessionTransitioner SessionTransitioner
	Waits               *WaitTracker

	// SubagentFlipper 把一个「不属于本轮」的后台任务终态,落到它派遣卡真正所在的那条
	// 更早的消息上。nil 时 handler 退回静默忽略。chat_svc 在 newTurnContext 注入。
	SubagentFlipper SubagentFlipper

	// resumedSubagents 记「本轮的新 tool call → 它认领到的、更早那条消息里那张派遣卡
	// 的 tool call」。跨轮恢复(SendMessage 沿用同一个 task_id 换新 tool_use_id)时由
	// SubagentStartedHandler 写入,后续 done 帧据此把终态翻在原卡上。
	// per-turn 单 goroutine 写入,无锁。
	resumedSubagents map[string]string

	// 本轮计时。口径(整轮耗时减工具空档,排队计入)与字段说明在 turnstats.Clock 上;
	// 「哪条事件动哪一下表」在 Dispatcher.Apply。嵌入而不是各存一份:agentred 的
	// fanout 要在没有 chat_svc 的前提下算出同一份数,两边共用这一只表。
	turnstats.Clock
}

// AliasSubagentToolCall 记下「新 tool call 其实是那张更早的卡」。
func (tc *TurnContext) AliasSubagentToolCall(from, to string) {
	if tc == nil || from == "" || to == "" {
		return
	}
	if tc.resumedSubagents == nil {
		tc.resumedSubagents = map[string]string{}
	}
	tc.resumedSubagents[from] = to
}

// ResolveSubagentToolCall 把 tool call 翻成它真正该落在的那张卡;没别名就原样返回。
func (tc *TurnContext) ResolveSubagentToolCall(toolCallID string) string {
	if tc == nil {
		return toolCallID
	}
	if to, ok := tc.resumedSubagents[toolCallID]; ok {
		return to
	}
	return toolCallID
}

// SessionUpdater handler 在 PermissionModeChanged / ContextWindowUpdated 等场景下
// 写 session 字段走这条。
type SessionUpdater interface {
	Update(ctx context.Context, sess any) error
}

// SubagentFlipper 跨消息定向翻转一个后台任务派遣卡的终态。
//
// 为什么需要它:后台任务(run_in_background 的 bash / subagent)的完成通知是**跨轮**
// 到达的 —— 派遣它的那条消息早已收尾落库,它的 subagent_state 块过不了当前轮的
// accumulator,turn.Mutate 必然落空。完成通知在会话空闲时到达会另起一条自主续轮
// (那条路自己带跨消息翻转);但它同样可能在**别人的轮**进行中到达,那时 CLI 把这一帧
// 并进当前活跃轮,handler 是该终态唯一的落点。
type SubagentFlipper interface {
	// FlipSubagentStatus 把该 tool call 的派遣卡改成 status;summary 非空时一并写入
	// (CLI task_notification.summary —— 成功时是子代理交回的报告,失败时是中断原因)。
	FlipSubagentStatus(ctx context.Context, toolCallID, status, summary string) error
	// ResumeSubagentByTaskID 按 CLI task_id 把更早那条消息里的派遣卡推回运行态,交回
	// 它所在的 tool_call_id;找不到交回空串。
	//
	// 恢复(SendMessage)沿用同一个 task_id、换一个 tool_use_id。恢复若发生在后一轮,
	// 原卡早已落库,同轮那条 adoptResumedSubagent 的通路够不着它 —— 不接这一条,恢复段
	// 就会另起一块挂在 SendMessage 那次调用上,而它不是 agent.spawn 工具名,那块永远没有
	// 卡片渲染。
	ResumeSubagentByTaskID(ctx context.Context, taskID, status string) (string, error)
}

// SessionTransitioner 切 session 状态 — UserAskRequest/ToolPermissionRequest
// 进入 waiting,Resolved 出 waiting。chat_svc 现有 markSessionWaiting/Running
// 实现该接口。
type SessionTransitioner interface {
	MarkWaiting(ctx context.Context, sess any, stream string)
	MarkRunning(ctx context.Context, sess any, stream string)
}
