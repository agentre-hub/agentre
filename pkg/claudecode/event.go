package claudecode

import (
	"encoding/json"

	"github.com/cago-frame/agents/provider"
)

// EventKind claudecode Stream 暴露的事件离散类型。
//
// 子集：仅包含 agentre 当前消费的事件。upstream cago 还有 EventMessageEnd /
// EventSessionStart / EventPermissionRequest 等，我们用不到就不发。
type EventKind string

const (
	EventTextDelta     EventKind = "text_delta"
	EventThinkingDelta EventKind = "thinking_delta"
	EventPreToolUse    EventKind = "pre_tool_use"
	EventPostToolUse   EventKind = "post_tool_use"
	// subagent (Agent / Task) 生命周期信号，CLI 以 system.subtype 形态发出。
	// 把它们抬到一等事件方便上层维护"派子任务卡"的进度态。
	EventTaskStarted      EventKind = "task_started"
	EventTaskProgress     EventKind = "task_progress"
	EventTaskNotification EventKind = "task_notification"
	// EventRetry 来自 CLI 的 system.api_retry 帧：Anthropic SDK 命中可重试错误（429/5xx 等）后
	// CLI 把每一次后退-重试都包成一条协议帧推到 stdout（同 turn 内非终态），让上层有机会渲染
	// "正在重试 N/M" 卡片，对齐 Codex 的 RetryNoticeCard 行为。命中 max_retries 后 CLI 走
	// result.error 路径，与本事件正交。
	EventRetry EventKind = "retry"
	// EventUsage 在每个**主 agent** assistant 帧到达时 emit 一条，携带该帧的
	// per-call usage —— 等同于「这次内部 API call 之后模型当前看到的输入大小」。
	// turn 内会发多条（每个工具循环一条），让上层（chat_svc → 前端 Composer
	// 进度条）能跟着工具调用阶梯式刷新「已用上下文」，不必等 EventDone 才更新。
	//
	// 严格只对 parent_tool_use_id == "" 的主 agent 帧 emit；subagent 内部帧的
	// usage 代表独立 Anthropic 会话的输入量，混进来会让主进度条骤降。
	//
	// 老 CLI 不在 assistant 帧上挂 usage 时不发本事件，回退到「仅 EventDone」旧行为。
	EventUsage EventKind = "usage"
	EventDone  EventKind = "done"
	EventError EventKind = "error"
	// EventControlRequest 来自 Claude Code stdout 的 control_request 帧（subtype:
	// "can_use_tool"），由 --permission-prompt-tool stdio 启用。host 必须用
	// Session.RespondToControl 在 stdinMu 保护下回一帧 control_response 才能让 CLI
	// 继续推进。
	EventControlRequest EventKind = "control_request"
	// EventPermissionModeChanged 来自 CLI 的 system{subtype:"status",permissionMode:...}
	// 帧。两种触发场景：
	//   - 主动：host 调 Session.SetPermissionMode 之后 CLI 回这一帧（同 control_response 之后）
	//   - 被动：AI 在 plan mode 下调用 ExitPlanMode 工具被批准后，CLI 自动切到 default/acceptEdits
	// 两种场景的帧结构一致，host 一律通过该事件感知最终 mode。Event.PermissionMode 携带新值。
	EventPermissionModeChanged EventKind = "permission_mode_changed"
	// EventCompactBoundary 来自 CLI 的 system{subtype:"compact_boundary"} 帧。
	// 两种触发场景：
	//   - 用户主动 /compact（compact_metadata.trigger == "manual"）
	//   - CLI 自身阈值触发的自动压缩（trigger == "auto"）
	// 帧到达之后 CLI 继续 resume 同一 session，但 LLM 只看得到压缩后的摘要——原始
	// 历史不再喂给模型。上层据此打边界，UI 折叠旧消息避免"以为 AI 还记得"的认知错位。
	EventCompactBoundary EventKind = "compact_boundary"
	// EventStatus 来自 CLI 的 system{subtype:"status",status:<非空>} 帧 ——
	// 与 EventPermissionModeChanged 同源 (都解 status 帧),但承载的是 CLI 的会话级
	// 运行状态字符串 (已观察值:"requesting" / "compacting";其它由 CLI 未来引入)。
	//
	// 典型场景:用户调 /compact 或 CLI 达到自动压缩阈值时,CLI 立刻推一帧
	// status:"compacting" (整个总结期间持续生效),压缩完成后推 status:null 收尾,
	// 紧接着才发 compact_boundary 帧。我们刻意只 emit 非空状态;清理信号留给 compact_boundary
	// / done / error 路径 —— 这样无法识别的未来 status:null 帧仍能保持前向兼容地被静默忽略。
	EventStatus EventKind = "status"
	// EventInit 来自 CLI 的 system{subtype:"init"} 帧。仅在该帧带 model 字段时 emit
	// (老 CLI 不报 model → 不发,上层走 EventDone.Model 兜底)。携带 SessionID + Model,
	// 让 agentruntime/claudecode translator 在 turn 开始时就能查 cago llmcatalog
	// 兜底 context window 大小,而不是等 EventDone 才知道窗口——前端进度条因此能在
	// turn 内实时显示用量占比,不必"等一轮跑完才出条"。
	EventInit EventKind = "init"
	// EventSubagentModel 来自 subagent 内部 assistant 帧（parent_tool_use_id 非空）
	// 携带的 message.model 字段。「实际执行」胜过「调用意图」（R2）——上游用它覆盖
	// canonical 投影阶段记录的入参别名（如 "haiku"）。仅当 parent_tool_use_id 与
	// message.model 都非空才 emit；主 agent 自己的帧（parent_tool_use_id 为空）即便
	// 带 model 也不产出，避免污染 EventInit/EventDone 的既有 Model 语义（R5）。
	// 老 CLI 不发 message.model 时同样不产出，走降级路径不阻断。
	//
	// 同一子代理只认第一个实际模型（R3 first-wins）由上游累计态负责去重；本层每次
	// 遇到都如实产出一条，不做去重判断。
	EventSubagentModel EventKind = "subagent_model"
	// EventContentBlockStart 来自 stream_event 的 Anthropic SSE content_block_start
	// 帧:模型开始产出这一条 assistant message 的某个输出块。纯计时信号 —— 不带
	// 内容,上层只拿它记「首 token」(TTFT)。
	//
	// 为什么需要它:text_delta / thinking_delta 只覆盖**看得见**的输出。一跳「一句话
	// 不吐、直接甩工具调用」时,工具入参走 input_json_delta,而那条按设计不流(工具
	// 调用整块由 merged assistant 帧产出,见 partial.go),于是整跳没有任何事件能证明
	// 模型已经开口 —— 首 token 一路推迟到模型终于说正文的那一刻(sess-3241:190s 的
	// 一轮报出 166s 的首 token)。content_block_start 对**所有**块类型都发,是这个
	// 口径下唯一完整的开表点。
	//
	// 与 usage / 正文增量同规矩:parent_tool_use_id 非空的子代理内部帧不产出。
	EventContentBlockStart EventKind = "content_block_start"
)

// ToolEvent 在 EventPreToolUse / EventPostToolUse 上携带。
//
//   - PreToolUse:  ID / Name / Input 填，Response / Err 留空。
//   - PostToolUse: ID / Name / Response 填，Err 在工具失败时填。
//
// Response 是 tool_result.content 解码后的纯文本：Anthropic 协议里它可能是 JSON 字符串
// 或 content-block 数组（每块带 type/text），decoder 统一拍平成 Go string。换行等转义
// 序列已还原成真实字符，调用方拿到即可直接展示。
//
// Subagent 仅在 Agent / Task 父调用的 PreToolUse 或 3 类 EventTask* 事件上填，
// 透传 CLI 的 system.task_* 元数据；普通工具留空。
type ToolEvent struct {
	ID       string
	Name     string
	Input    json.RawMessage // 原始 JSON；调用方按需 Unmarshal 到 map
	Response string
	Err      error
	Subagent *SubagentMeta

	// ResultMeta 仅 PostToolUse 填，承载 CLI 在 user 帧顶层 (跟 message 同级)
	// 吐的 tool_use_result 元数据原始 JSON。典型用例：
	//   - TaskCreate result_meta = {"task":{"id":"1","subject":"..."}}
	//     —— CLI 不在 tool input 里返回新建任务的 id,只在这份 meta 里有；
	//     前端按它把 TaskCreate.toolUseId ↔ TaskUpdate.taskId 建映射。
	//   - TaskUpdate result_meta = {"success":..,"taskId":"1",
	//     "statusChange":{"from":"pending","to":"in_progress"},"updatedFields":[...]}
	// 普通工具帧没有这个字段时留 nil（不要填空 JSON），上层按 nil 判断"无 meta"。
	ResultMeta json.RawMessage
}

// SubagentMeta CLI 通过 system.task_started / task_progress / task_notification
// 三类 system 帧周期性下发的子智能体运行元数据。字段命名贴近原始协议，便于追源。
//
// 各字段的来源帧：
//
//	TaskID          ← 三类 system 帧共通字段 task_id
//	SubagentType    ← 三类共通字段 subagent_type
//	TaskType        ← 三类共通字段 task_type（"local_bash" / "local_agent"）
//	TaskDescription ← task_started.description（任务名）/ task_progress.description（实时摘要）
//	Prompt          ← 仅 task_started.prompt（任务说明，长文本）
//	LastToolName    ← task_progress.last_tool_name
//	ToolUses        ← task_progress.usage.tool_uses / task_notification.usage.tool_uses
//	TotalTokens     ← usage.total_tokens
//	DurationMs      ← usage.duration_ms
//	Status          ← task_notification.status（"completed" / "failed"）
type SubagentMeta struct {
	TaskID          string
	SubagentType    string
	TaskType        string // ← task_started/progress/notification.task_type（local_bash / local_agent）
	TaskDescription string
	Prompt          string
	LastToolName    string
	ToolUses        int
	TotalTokens     int
	DurationMs      int
	Status          string
}

// CompactEvent 镜像 system.compact_boundary 帧 compact_metadata 子对象的字段。
//
//	PreTokens  ← compact_metadata.pre_tokens   （压缩前上下文 token 数）
//	PostTokens ← compact_metadata.post_tokens  （压缩后保留的 token 数,带摘要的总量）
//	Trigger    ← compact_metadata.trigger      （"auto" | "manual"）
//	DurationMs ← compact_metadata.duration_ms  （本次压缩耗时,毫秒）
//
// 字段缺失时各保持零值；上层 UI 拿到零值仅退化为不显示数字 / trigger label，
// 不影响"打边界"主流程。
type CompactEvent struct {
	PreTokens  int
	PostTokens int
	Trigger    string
	DurationMs int
}

// RetryEvent 镜像 system.api_retry 帧的字段。命名贴近原始协议，便于追源。
//
//	Attempt     ← attempt（1-indexed 当前重试序号：CLI 在第 N 次发请求时报 N）
//	MaxAttempts ← max_retries（SDK 配置的重试上限；命中后 CLI 改走 result/error 路径）
//	DelayMs     ← retry_delay_ms（下次重试前的 jittered 等待，浮点 ms）
//	ErrorStatus ← error_status（HTTP status，例如 529）
//	ErrorCode   ← error（例如 "rate_limit"）
type RetryEvent struct {
	Attempt     int
	MaxAttempts int
	DelayMs     float64
	ErrorStatus int
	ErrorCode   string
}

// Event 是 Stream 暴露的事件值。值类型，调用方拷贝安全。
type Event struct {
	Kind EventKind

	// 所有 frame 都附带：每帧来时填写当时的 session_id（首帧 system.init 才有
	// 权威值，后续每帧重复填便于消费方就近读取）。
	SessionID string

	// EventTextDelta / EventThinkingDelta 文本增量。
	Text string

	// EventPreToolUse / EventPostToolUse 工具元数据；
	// EventTask* 子智能体生命周期信号时，Tool.ID == 外层 Agent.tool_use_id，
	// Tool.Subagent 填充对应字段，Input/Response 留空。
	Tool *ToolEvent

	// EventDone 时填写最终 usage。
	Usage provider.Usage

	// EventInit / EventDone 时填写本轮 CLI 实际使用的模型 id（来自 system.init 帧）。
	// 上游用它在不显式 --model 时回查模型 metadata（如 context window）。
	// 老版本 CLI 不发 system.init.model → 留空，调用方需自己回退。
	//
	// EventSubagentModel 时复用本字段填写 subagent 内部帧 message.model 的值（与
	// 上面两个 Kind 的取值来源、语义都不同，互不干扰）。
	Model string

	// ContextWindow 与 codex.Event.ContextWindow 字段对称。Claude Code SDK 不在 usage
	// 里报上下文窗口，所以这里始终为 0 —— 调用方（agentruntime / chat_svc）必须用
	// model 名查 cago catalog 兜底。保留此字段是为了让 agentruntime adapter 在两个
	// 后端之间不做 backend 分支判断，统一读 ev.ContextWindow。
	ContextWindow int

	// EventRetry 携带：CLI system.api_retry 帧的结构化字段。
	Retry *RetryEvent

	// EventCompactBoundary 携带：CLI system.compact_boundary 帧的压缩元数据。
	Compact *CompactEvent

	// EventError 携带终止错误。
	Err error

	// ParentToolUseID 当前事件属于某个 Agent / Task subagent 内部时，指向外层 Agent
	// 的 tool_use_id；主 agent 自己的事件留空。
	//
	// 上层据此把子事件归集到对应的 Agent 卡，避免内部 Bash/Read/Grep 被渲染成同级兄弟卡。
	// 注意：EventTask* 系统帧本身不算 "子事件"，留空。
	ParentToolUseID string

	// EventControlRequest 携带：claude 端发起的工具调用许可请求。host 必须用
	// Session.RespondToControl 回写决定。
	ControlRequest *ControlRequestEvent

	// EventPermissionModeChanged 携带：CLI system{subtype:"status"} 帧上的
	// permissionMode 字段值（"default"/"acceptEdits"/"plan"/"bypassPermissions"）。
	PermissionMode string

	// EventStatus 携带:CLI system{subtype:"status"} 帧上的 status 字段值。
	// 已知值 "requesting" / "compacting";其他由 CLI 未来引入。上层透传给 UI,
	// 不在底层做语义映射。
	Status string
}
