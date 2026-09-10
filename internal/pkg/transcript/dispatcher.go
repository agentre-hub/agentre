package transcript

import (
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

// Adapters 是宿主注入的那几件事：把 handler 算出来的东西写到宿主自己的存储上。
// 每一格都可以是 nil —— handler 在 Writer 为 nil 时只 emit、不落库，累积出来的块
// 不受影响。
//
// 为什么它们不共用：写入的对象是宿主自己的会话行与消息行（桌面端 chat_sessions /
// agentred 的握手建行），列与生命周期都不同。共用的是「哪种事件落哪种块」，也就是
// 下面那张注册表。
type Adapters struct {
	// Usage per-call token 用量写入（桌面端只写 token 那几列，导入路径只 patch 内存实体）。
	Usage handlers.UsageWriter
	// Error 错误文本写入。
	Error handlers.ErrorWriter
	// ContextWindow 上下文窗口占用写入（写会话行，不是消息行）。
	ContextWindow handlers.ContextWindowWriter
	// PermissionMode 权限模式读写（写会话行）。
	PermissionMode handlers.PermissionModeWriter
	// Plan 计划卡落块 —— 块类型归宿主（桌面端是 chat_svc.PlanBlock）。
	Plan handlers.PlanWriter
	// Compact 压缩边界要读的消息定位信息（ID / Seq）。
	Compact handlers.CompactInspector
}

// NewTurnDispatcher 构造一轮 turn 的事件 dispatcher，注册全部 handler。
//
// **这是「哪种事件落哪种块」的唯一一张表。** 从前它在 chat_svc 与 chat_import_svc
// 各有一份，两份只在注入的适配器上不同 —— 于是块类型每演进一次要同步两处，而漏同步
// 的表现是转录静默少一张卡，编译期没有任何东西会报错。现在差异全部收在 Adapters 里。
//
// 未注册的事件由 turn.Dispatcher 默默丢弃（forward-compat）。SteerConsumed 与
// 桌面端的 ErrorEvent 拦截属于宿主的轮次控制，不在这张表上。
func NewTurnDispatcher(adapters Adapters) *turn.Dispatcher {
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
	d.Register((*agentruntime.PermissionModeChanged)(nil), handlers.PermissionModeChangedHandler{Writer: adapters.PermissionMode})
	d.Register((*agentruntime.UsageUpdate)(nil), handlers.UsageUpdateHandler{Writer: adapters.Usage})
	d.Register((*agentruntime.ContextWindowUpdated)(nil), handlers.ContextWindowUpdatedHandler{Writer: adapters.ContextWindow})
	d.Register((*agentruntime.Retry)(nil), handlers.RetryHandler{})
	d.Register((*agentruntime.ErrorEvent)(nil), handlers.ErrorHandler{Writer: adapters.Error})
	d.Register((*agentruntime.Done)(nil), handlers.DoneHandler{})
	d.Register((*agentruntime.PlanUpdated)(nil), handlers.PlanUpdatedHandler{Writer: adapters.Plan})
	d.Register((*agentruntime.CompactBoundary)(nil), handlers.CompactBoundaryHandler{Inspector: adapters.Compact})
	d.Register((*agentruntime.RuntimeStatus)(nil), handlers.RuntimeStatusHandler{})
	return d
}
