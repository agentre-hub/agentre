package chat_svc

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/handlers"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// newPackageDispatcher 构造 chat_svc 的 dispatcher 并注册全部 handler,
// 并把 chat_svc 适配器(usage/error/context_window/permission_mode/plan)注入到对应 handler。
// 每个 chatSvc 实例调一次,svc-bound 让适配器能持 *chatSvc 引用。
//
// SteerConsumed 与 ErrorEvent 不经 dispatcher —— 由 chat.go runTurn 的 switch
// 提前拦截(turn-segmentation / streamStopErr 紧耦合 local state)。
func newPackageDispatcher(svc *chatSvc) *turn.Dispatcher {
	usageH, errH, cwH, pmH, planH, compactH := buildHandlersWithAdapters(svc)
	d := turn.NewDispatcher()
	d.Register((*agentruntime.TextDelta)(nil), handlers.TextDeltaHandler{})
	d.Register((*agentruntime.ThinkingDelta)(nil), handlers.ThinkingDeltaHandler{})
	d.Register((*agentruntime.OutputActivity)(nil), handlers.OutputActivityHandler{})
	d.Register((*agentruntime.ToolCall)(nil), handlers.ToolCallHandler{})
	d.Register((*agentruntime.ToolResult)(nil), handlers.ToolResultHandler{})
	d.Register((*agentruntime.UserAskRequest)(nil), handlers.UserAskRequestHandler{})
	d.Register((*agentruntime.UserAskResolved)(nil), handlers.UserAskResolvedHandler{})
	d.Register((*agentruntime.ToolPermissionRequest)(nil), handlers.ToolPermissionRequestHandler{})
	d.Register((*agentruntime.ToolPermissionResolved)(nil), handlers.ToolPermissionResolvedHandler{})
	d.Register((*agentruntime.ExecApprovalRequested)(nil), handlers.ExecApprovalRequestedHandler{})
	d.Register((*agentruntime.ExecApprovalResolved)(nil), handlers.ExecApprovalResolvedHandler{})
	d.Register((*agentruntime.SubagentStarted)(nil), handlers.SubagentStartedHandler{})
	d.Register((*agentruntime.SubagentProgress)(nil), handlers.SubagentProgressHandler{})
	d.Register((*agentruntime.SubagentDone)(nil), handlers.SubagentDoneHandler{})
	d.Register((*agentruntime.SubagentModel)(nil), handlers.SubagentModelHandler{})
	d.Register((*agentruntime.PermissionModeChanged)(nil), pmH)
	d.Register((*agentruntime.UsageUpdate)(nil), usageH)
	d.Register((*agentruntime.ContextWindowUpdated)(nil), cwH)
	d.Register((*agentruntime.Retry)(nil), handlers.RetryHandler{})
	d.Register((*agentruntime.ErrorEvent)(nil), errH)
	d.Register((*agentruntime.Done)(nil), handlers.DoneHandler{})
	d.Register((*agentruntime.PlanUpdated)(nil), planH)
	d.Register((*agentruntime.CompactBoundary)(nil), compactH)
	d.Register((*agentruntime.RuntimeStatus)(nil), handlers.RuntimeStatusHandler{})
	return d
}

// packageDispatcher 用零值 svc(nil)注册;运行时调用方应当用 newPackageDispatcher(svc)
// 拿到 svc-bound 实例(Steer/Usage 等才能落库)。本变量留作脚手架 + 单测用。
var packageDispatcher = newPackageDispatcher(nil)
