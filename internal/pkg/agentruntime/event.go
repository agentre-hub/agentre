package agentruntime

import (
	"context"
	"encoding/json"

	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

// Event 是 sealed interface,所有 typed event case 必须实现 isEvent()。
// chat_svc 用 type switch 处理;不再有"Kind discriminator + 15 个可选字段"的胖 struct。
//
// 旧 RuntimeEvent 仍在 runner.go 保留(daemon wire format + 旧 fixture 模板用);
// 新代码统一通过 Event 直流。
type Event interface {
	isEvent()
}

// TextDelta 流式纯文本片段。
type TextDelta struct{ Text string }

// ThinkingDelta 流式思考片段(Anthropic 协议把它放在 turn 开头并保住 signature)。
type ThinkingDelta struct{ Text string }

// OutputActivity「模型开始产出一个输出块」的纯计时信号,不带内容。turn 内可以来
// 多条(每个输出块一条),chat_svc 只拿它记首 token(TTFT),不进 accumulator、不落库、
// 不推 UI 内容。
//
// 存在理由:TextDelta / ThinkingDelta 只覆盖**看得见**的输出。一跳「一句话不吐、
// 直接甩工具调用」时,模型早就在产出 token(工具入参),但没有任何可见增量事件 ——
// 首 token 于是一路推迟到模型终于说正文那一刻(sess-3241:190s 的一轮报出 166s
// 的首 token,前面 23 跳工具全程无人记表)。
//
// 当前唯一生产者是 claudecode(SSE content_block_start,见 pkg/claudecode 的
// EventContentBlockStart);codex / piagent 没有等价帧,由 ToolCall 兜底记表 ——
// 精度略差(merged 帧要等整块生成完才到),但同样不会再漏整跳。
type OutputActivity struct{}

// ToolCall 携带原始工具名 + input;Canonical 在 translator 识别成功时填,nil 表示
// 非 canonical (走 raw tool_use 路径)。同 ToolCallID 多次 emit 视为增量更新
// (canonical 增量),accumulator 用 mutateIndex 覆盖。subagent 子调用同时携带外层
// ParentToolCallID 与可选稳定 SubagentRunID；缺失 run ID 仍须保留为父调用 fallback
// step，两者都为空才表示主 agent 自己的工具。Input 仅跨 runtime wire、UI block 与
// blocks_json 边界流转，禁止作为 operational log 字段记录。
type ToolCall struct {
	ID               string
	Name             string
	Input            json.RawMessage
	Canonical        canonical.CanonicalTool
	ParentToolCallID string
	SubagentRunID    string
}

// ToolResult 工具调用结果。Meta 携带 backend 在 tool_result 旁吐的结构化元数据
// (claudecode 走 CLI 顶层 tool_use_result;codex 当前不发),原始 JSON 字节;
// chat_svc 落 ChatBlock,前端按工具语义 Unmarshal。无 meta 留 nil。
//
// ParentToolCallID:当前 tool_result 属于 subagent 内部工具时指向外层 Agent.tool_use_id;
// SubagentRunID 再区分同一外层 parallel/chain 的输入槽；缺失时保持为空并由 UI fallback
// 分组，不能丢弃或猜测归属。Content/Meta 只供 wire、UI/persistence 与正常模型工具语义
// 使用，不得复制到 operational logs。
type ToolResult struct {
	ToolCallID       string
	Content          string
	IsError          bool
	ParentToolCallID string
	SubagentRunID    string
	Meta             json.RawMessage
}

// SteerConsumed mid-turn 用户消息被 backend 注入到当前 turn(claudecode user 块 /
// codex turn/steer)。Steers 是 FIFO 顺序的批次,chat_svc 据此把对应 queued
// chat_message 状态推进到 consumed。
type SteerConsumed struct{ Steers []ConsumedSteer }

// UserAskRequest backend 检测到 AskUserQuestion 控制请求时 emit。
// ToolCallID race 时(control_request 比 tool_use 先到)允许为空,前端按 RequestID merge。
// ParentToolCallID:subagent 内部 AskUserQuestion 时指向外层 Agent.tool_use_id。
type UserAskRequest struct {
	RequestID        string
	ToolCallID       string
	ParentToolCallID string
	Questions        []AskQuestion
}

// UserAskResolved backend 完成 SubmitAnswer 反向投回后 emit。Skipped=true 表示用户跳过。
// ParentToolCallID 与对应 UserAskRequest 一致,便于前端 merge 后仍落在子卡上。
type UserAskResolved struct {
	RequestID        string
	ParentToolCallID string
	Answers          []AskAnswer
	Skipped          bool
}

// ToolPermissionRequest backend 收到 can_use_tool(除 AskUserQuestion 以外)时 emit。
type ToolPermissionRequest struct {
	RequestID  string
	ToolCallID string
	ToolName   string
	Input      json.RawMessage
}

// ToolPermissionResolved backend 完成 SubmitToolPermission 反向投回后 emit。
type ToolPermissionResolved struct {
	RequestID   string
	Allowed     bool
	AlwaysAllow bool
	DenyReason  string
}

// ExecApprovalRequested is the safe, presentation-oriented subset of an
// OpenClaw exec.approval.requested event. The Gateway's systemRunPlan is never
// copied into a resolve request; AgentRE returns only ID + Decision.
type ExecApprovalRequested struct {
	ID               string
	CommandText      string
	CommandPreview   string
	AllowedDecisions []string
	Host             string
	NodeID           string
	AgentID          string
	SessionKey       string
	CreatedAtMs      int64
	ExpiresAtMs      int64
}

// ExecApprovalResolved is an approval terminal state, distinct from the
// lifecycle of the command/tool it authorized. Status is resolved or expired.
type ExecApprovalResolved struct {
	ID           string
	Status       string
	Decision     string
	ResolvedBy   string
	ResolvedAtMs int64
}

// PermissionModeChanged CLI 通报自身 permission_mode 已变更。
type PermissionModeChanged struct{ Mode string }

// SubagentStarted / Progress / Done 是 backend-neutral subagent 生命周期。ToolCallID
// 指向外层 Task / Agent 工具调用；Info 可携带 legacy 单运行元数据，或 Pi runtime
// 维护的 mode + runs 全量快照。Info 中的 task/summary/error 等内容只进入 runtime
// wire 与 UI/persistence 边界，不得序列化进 operational logs。
type SubagentStarted struct {
	ToolCallID string
	Info       SubagentInfo
}
type SubagentProgress struct {
	ToolCallID string
	Info       SubagentInfo
}
type SubagentDone struct {
	ToolCallID string
	Info       SubagentInfo
}

// SubagentModel 携带 subagent 内部帧解析出的实际模型（R2）。ToolCallID 指向外层
// Agent/Task 工具的调用 id，与 SubagentStarted/Progress/Done 的 ToolCallID 同一
// 命名空间。独立事件类型，不复用 SubagentProgress——那条事件的消费方对
// ToolUses/TotalTokens/LastToolName 是无条件赋值，塞一个只带模型的 SubagentInfo
// 会把已累计的进度清零；模型改走本事件，只更新模型字段，不清空既有累计态（R4）。
type SubagentModel struct {
	ToolCallID string
	Model      string
}

// Retry 非终止 backend 重试通知。
type Retry struct {
	Message string
	Details string
	Attempt int
	Max     int
}

// UsageUpdate per-API-call usage 上报。TotalInputTokens 由各 runtime translator
// 按 family 聚合(Anthropic = prompt + cached + cacheCreation;OpenAI = prompt),
// 供 chat_svc 直接 patch assistantMsg 与 emit StreamUsage,前端不再做家族判断。
// ContextWindow 可选；runtime 已探到时与 usage 同帧携带，避免独立窗口事件在
// 前端订阅建立前丢失后留下「有 usage、无分母」的状态。
type UsageUpdate struct {
	Usage            *provider.Usage
	TotalInputTokens int
	ContextWindow    int
}

// ContextWindowUpdated runtime 探到模型实际可用窗口大小变化时 emit。
// Codex 读 app-server modelContextWindow；Claude Code 用模型 id 查 llmcatalog；
// Pi Agent 用 Pi RPC get_state.model.contextWindow 启动，并由
// get_session_stats.contextUsage.contextWindow 校正，避免自定义 provider 复用公共
// 模型名时误套 catalog 元数据。Tokens=0 视为"未探到"。
type ContextWindowUpdated struct{ Tokens int }

// PlanUpdated runtime 上报的计划更新(claudecode TodoWrite / codex update_plan +
// plan delta)。Plan 携带 canonical 化的步骤 + 完整 Markdown,chat_svc 走 canonical
// 投影到 PlanBlock / PlanUpdateCard,不必再各自适配 wire 格式。
type PlanUpdated struct{ Plan canonical.PlanUpdate }

// CompactBoundary runtime 上报的会话上下文压缩边界(claudecode 的
// system{subtype:"compact_boundary"} 帧;codex 的 contextCompaction item /
// thread/compacted notification)。chat_svc 据此持久化一条
// role=system 的边界 message + emit StreamCompactBoundary,前端折叠旧上下文。
//
// 任一字段为零值表示 CLI 没下发对应 compact_metadata 字段,
// 前端按零值退化展示(不显示数字 / trigger label / 耗时)。
type CompactBoundary struct {
	PreTokens  int
	PostTokens int    // 压缩后保留的 token 数 (摘要 + 必要历史)
	Trigger    string // "auto" | "manual"
	DurationMs int    // 压缩耗时,毫秒
}

// RuntimeStatus runtime 上报的会话级运行状态字符串。当前 claudecode 用它表达
// "compacting" / "requesting" 等带状态的过渡阶段 —— /compact 启动到 compact_boundary
// 之间这段时间持续生效,chat_svc 据此推送 stream event,前端 Composer 替换 typing
// indicator 为 "正在压缩上下文…" chip。
//
// 空 Status 视为"清理/重置"信号,runtime translator 自己决定是否 emit;chat_svc 只
// 关心最后一次非空 Status 和后续 done/error/compact_boundary 之间的窗口。
type RuntimeStatus struct {
	Status string
}

// UserMessageEvent (R18):一轮 **由某位带设备身份的发起方**在空闲会话上「开新一轮」时,
// daemon 在事件流开头注入的标记,携带该轮的发起方用户文本与设备身份(指纹 + 显示名)。
// 它是 daemon 到桌面端转录的唯一事实来源:桌面端据它在转录里落成一行用户消息 + 来源
// 标识(「来自 <设备名>」),并据它区分「浏览器发起的一轮」与「自主续轮」(纯 assistant,
// 没有这个标记)。本机(桌面端)发起的轮不携带 SourceDevice,daemon 不注入 —— 单端界面
// 零变化。Text 是发起方的用户文本;SourceDeviceName 缺失时保持空,前端回退到指纹。
type UserMessageEvent struct {
	Text             string
	SourceDevice     string
	SourceDeviceName string
}

// UnrecognizedBlock 一条发送方投射不出来的转录块,原样往下送(见
// EventUnrecognizedBlock)。BlockType 是存储层记的块类型,Data 是它的载荷字节 ——
// 两者都不解释、不重新序列化:发送方读不懂的东西,只有原样传下去才有被读懂的机会。
type UnrecognizedBlock struct {
	BlockType string
	Data      json.RawMessage
}

// Done turn 正常结束。
type Done struct{}

// ErrorEvent turn 因错误中止;Err 携带原因。
type ErrorEvent struct{ Err error }

func (TextDelta) isEvent()              {}
func (ThinkingDelta) isEvent()          {}
func (OutputActivity) isEvent()         {}
func (ToolCall) isEvent()               {}
func (ToolResult) isEvent()             {}
func (SteerConsumed) isEvent()          {}
func (UserAskRequest) isEvent()         {}
func (UserAskResolved) isEvent()        {}
func (ToolPermissionRequest) isEvent()  {}
func (ToolPermissionResolved) isEvent() {}
func (ExecApprovalRequested) isEvent()  {}
func (ExecApprovalResolved) isEvent()   {}
func (PermissionModeChanged) isEvent()  {}
func (SubagentStarted) isEvent()        {}
func (SubagentProgress) isEvent()       {}
func (SubagentDone) isEvent()           {}
func (SubagentModel) isEvent()          {}
func (Retry) isEvent()                  {}
func (UsageUpdate) isEvent()            {}
func (ContextWindowUpdated) isEvent()   {}
func (PlanUpdated) isEvent()            {}
func (CompactBoundary) isEvent()        {}
func (RuntimeStatus) isEvent()          {}
func (UserMessageEvent) isEvent()       {}
func (UnrecognizedBlock) isEvent()      {}
func (Done) isEvent()                   {}
func (ErrorEvent) isEvent()             {}

type ExecApprovalResolution struct {
	Status   string
	Decision string
}

// ExecApprovalSink resolves a pending Gateway exec approval for the active
// AgentRE chat session. Implementations must validate Decision against the
// request's allowedDecisions before producing any side effect.
type ExecApprovalSink interface {
	ResolveExecApproval(ctx context.Context, sessionID int64, approvalID, decision string) (ExecApprovalResolution, error)
}
