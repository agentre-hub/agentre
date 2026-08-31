// internal/service/chat_svc/types.go
package chat_svc

import (
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/view"
)

// Chat Wails 事件名形如 "chat:event:<sessionID>:<assistantMessageID>"。
// 前端用 EventsOn 注册该名字接收 chunk / done / error。
const StreamEventPrefix = "chat:event"

// AutonomousEventPrefix 是会话级旁路事件名前缀,形如 "chat:autonomous:<sessionID>"。
// 见 AutonomousStreamName / StreamAutonomousStarted。
const AutonomousEventPrefix = "chat:autonomous"

// ChatStreamEventKind 是 Wails 事件 payload 里的 kind 枚举。
type ChatStreamEventKind string

const (
	StreamChunk    ChatStreamEventKind = "chunk"
	StreamThinking ChatStreamEventKind = "thinking"
	// StreamOutputActivity「模型开始产出一个输出块」的纯计时信号,不带任何载荷。
	// 前端的 live「首 token」是自己按流事件算的,靠这条在没有可见正文的那些跳里
	// 记表,与后端 turn/timing.go 保持同口径(sess-3241)。
	StreamOutputActivity   ChatStreamEventKind = "output_activity"
	StreamToolUse          ChatStreamEventKind = "tool_use"
	StreamToolResult       ChatStreamEventKind = "tool_result"
	StreamSteerConsumed    ChatStreamEventKind = "steer_consumed"
	StreamSubagentStarted  ChatStreamEventKind = "subagent_started"
	StreamSubagentProgress ChatStreamEventKind = "subagent_progress"
	StreamSubagentDone     ChatStreamEventKind = "subagent_done"
	// StreamSubagentModel claudecode 从 subagent 内部 assistant 帧解析出实际模型时 emit
	// (R2)。独立 kind,只带 ToolCallID + Model 两个字段——不复用 StreamSubagentProgress
	// 的整份 Subagent 快照,避免与已累计的 toolUses/totalTokens/status 混在一起经前端
	// 浅合并时把已有状态覆盖掉(R4)。
	StreamSubagentModel ChatStreamEventKind = "subagent_model"
	StreamRetry         ChatStreamEventKind = "retry"
	StreamMessageEnd    ChatStreamEventKind = "message_end"
	StreamDone          ChatStreamEventKind = "done"
	StreamError         ChatStreamEventKind = "error"
	StreamClosed        ChatStreamEventKind = "closed"
	// StreamAborted 用户点「停止」中断本轮 turn 时 emit。语义上是 Done 的兄弟：
	// 流以正常方式结束（partial 内容保留 + agentStatus=idle），但前端要渲染成
	// 「已停止」标签而不是 error 红字。Message 字段携带最终的 assistant 消息状态
	// （包含 abort 之前已经流出的 blocks）。
	StreamAborted ChatStreamEventKind = "aborted"
	// StreamAskUserQuestion backend 检测到 AskUserQuestion 类工具调用时 emit。
	// 前端渲染交互卡片，用户答完后调 AnswerUserQuestion 回灌。Answered=true 的
	// 事件代表"已回答"态切换（无需重新建 block，按 RequestID 找到既有 block 更新）。
	StreamAskUserQuestion ChatStreamEventKind = "ask_user_question"
	// StreamPlanUpdate backend 收到 runtime plan delta/update_plan 时 emit。
	// 前端把它落为 type:"plan" + canonical.plan.update live block,作为底部
	// TaskProgressBar 的数据源;若 canonical.plan.update.actions 非空,同一个
	// type:"plan" block 会复用 PlanCard 作为下一步操作入口。tool_use 形式的
	// plan.update 仍按普通 tool card 展示。
	StreamPlanUpdate ChatStreamEventKind = "plan_update"
	// StreamToolPermissionRequest backend 收到非 AskUserQuestion 类 can_use_tool
	// 时 emit。前端渲染审批卡片，用户决策后调 AnswerToolPermission 回灌。
	// Resolved=true 的事件代表"已审批"态切换（按 RequestID 找到既有 block 更新）。
	StreamToolPermissionRequest ChatStreamEventKind = "tool_permission_request"
	// StreamExecApproval carries OpenClaw Gateway approval requested/resolved
	// cards. A resolved card is not an exec/tool completion event.
	StreamExecApproval ChatStreamEventKind = "exec_approval"
	// StreamSessionStatus 推送 session 级 status patch（agentStatus + needsAttention）。
	// 用于 turn 进行中遇到 ask / 审批等待时把 toolbar 翻成橙色 WAITING，应答后翻回
	// RUNNING。前端按 stream name 已知 sessionId，patch 体只带新状态。
	StreamSessionStatus ChatStreamEventKind = "session_status"
	// StreamUsage 在 turn 内每次模型内部 API call 边界推一条，携带当前 assistant
	// 消息「本次 API call 之后看到的输入大小」（per-call usage）。前端 Composer
	// 进度条据此阶梯式刷新「已用上下文」，不必等 StreamDone 才更新。
	// chat_svc 同时把 token 列写回 chat_messages 行（context.WithoutCancel 抗
	// abort），让刷新页面也能看到中间态。
	StreamUsage ChatStreamEventKind = "usage"
	// StreamCompactBoundary backend 收到 runtime CompactBoundary 时 emit
	// (claudecode system.compact_boundary;manual / auto 同等)。前端据此在 transcript
	// 内嵌"上下文已压缩"分隔卡片,并默认折叠最后一个 compact_boundary 之前的全部消息
	// (DB 保留,展开可见)。同时 chat_svc 在当前 assistant message blocks 末尾追加
	// CompactBoundaryBlock 持久化,LoadSession 重放可重建 UI。
	StreamCompactBoundary ChatStreamEventKind = "compact_boundary"
	// StreamRuntimeStatus runtime 中间状态通知（如 compacting）。
	// 前端 chat-streams-host 据此切 typing indicator 样式。
	StreamRuntimeStatus ChatStreamEventKind = "runtime_status"
	// StreamAutonomousStarted 经会话级流 AutonomousStreamName(sessionID) 推送：CLI 在
	// run_in_background 任务完成后**自主**跑的一轮(无用户输入)被捕获时,通知前端有一条
	// 非用户发起的 assistant 轮开始。携带 AssistantMessage(前端据此插入新行)+ Stream
	// (该自主轮的 per-turn 事件名,前端 openStream 后续 chunk/done 走它实时渲染)。
	// Trigger="background_task"。前端渲染 AutoTriggerBanner +「自动」badge。
	StreamAutonomousStarted ChatStreamEventKind = "autonomous_started"
	// StreamSubagentActivityStarted:后台 subagent 在空闲态开始产出内部活动。前端据此对
	// 发起消息(LaunchMessageID)重开 per-turn 流(Stream),把活动块嵌套渲染回 AgentSpawnCard。
	// 与 StreamAutonomousStarted 不同:不插入新 assistant 行(发起消息已存在)。
	StreamSubagentActivityStarted ChatStreamEventKind = "subagent_activity_started"
	// StreamAutonomousFinished 是自主轮 / 后台 subagent 活动轮收尾时,在**会话级**流
	// AutonomousStreamName(sessionID) 上补发的终态兜底。
	//
	// 为什么需要它:这两类"非用户发起"的轮子,前端拿到 per-turn 流名的唯一入口是
	// StreamAutonomousStarted / StreamSubagentActivityStarted —— 前端收到后才 openStream,
	// ChatStreamsHost 也要等下一次 render 才 EventsOn 订阅该 per-turn 流。而后端可能在这个
	// "openStream→EventsOn" 窗口内就把 per-turn StreamDone/StreamClosed 发完了(典型:零子块
	// 的活动轮 started→done 背靠背)。fire-and-forget 事件对迟到的订阅者不重放 → 前端漏掉
	// 终态 → LiveStream 永远留在 store → streaming 卡死(输入框被逼走 Enqueue 发不出、
	// 自主轮那条空 assistant 行也不再 reload 回填内容)。用户轮没这个病:Send 的 RPC 响应
	// 同步给出流名,订阅早于任何帧。
	//
	// AutonomousStreamName 这条会话级流由 ChatPanel 挂载即订阅、常驻,先于任何 bypass 轮,
	// 不存在 subscribe-after-emit race。收尾在此补一发,前端据 LaunchMessageID 兜底
	// finishStream(幂等:per-turn 已收到 done 时该流已不在,直接 no-op)。
	StreamAutonomousFinished ChatStreamEventKind = "autonomous_finished"

	// StreamConnectionState 会话与执行它那台远端 daemon 之间的**通道**状态
	// (connected / reconnecting / lost)。它是运行态之上的一层修饰,不是第五种
	// AgentStatus —— 会话在重连期间仍然是「运行中」,只是通道断了。走会话级的
	// ConnStateStreamName 流,不走 per-turn 流(断连时那条流恰好没人收得到)。
	StreamConnectionState ChatStreamEventKind = "connection_state"
)

// ChatStreamEvent 是 EventsEmit 出去的统一 payload。
type ChatStreamEvent struct {
	Kind    ChatStreamEventKind `json:"kind"`
	Delta   string              `json:"delta,omitempty"`
	Message *ChatMessage        `json:"message,omitempty"`
	Error   string              `json:"error,omitempty"`

	// steer_consumed 事件填充：queuedIds 用于前端清 queue chip；
	// previousAssistantMessage / userMessages / assistantMessage 用于把当前
	// assistant 段收口、插入正式 user 段，并把后续 live stream 切到新的 assistant。
	QueuedIDs                []string      `json:"queuedIds,omitempty"`
	PreviousAssistantMessage *ChatMessage  `json:"previousAssistantMessage,omitempty"`
	UserMessages             []ChatMessage `json:"userMessages,omitempty"`
	AssistantMessage         *ChatMessage  `json:"assistantMessage,omitempty"`

	// tool_use 事件填充（StreamToolUse）。
	ToolCallID string         `json:"toolUseId,omitempty"`
	ToolName   string         `json:"toolName,omitempty"`
	ToolInput  map[string]any `json:"toolInput,omitempty"`
	// Canonical 是前端消费的统一工具识别投影 — runtime translator 算出来后,
	// handler emit、dispatcher_emitter 转 wire CanonicalDTO。前端按
	// CanonicalDTO.kind 分发到 canonical-tool/<kind>/card.tsx;不识别走 RawToolCard。
	Canonical *view.CanonicalDTO `json:"canonical,omitempty"`

	// tool_result 事件填充（StreamToolResult）。
	ToolResult string `json:"toolResult,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
	// ToolResultMeta backend 透传过来的工具结构化元数据（claudecode CLI 顶层
	// tool_use_result;codex 当前不发）。前端按工具语义解码,典型用例是 TaskCreate
	// 用它把系统分配的 task id 喂给前端做 task-progress 关联。无 meta 时留 nil。
	ToolResultMeta map[string]any `json:"toolResultMeta,omitempty"`

	// StreamConnectionState 事件填充。ConnectionState 是通道状态取值;
	// CaughtUpCount / PendingDecisions 只在补齐落定(connected)那一发有意义,
	// 分别是本次补齐重放了多少条通知、补完后还有多少个待决策没回答。
	ConnectionState  string `json:"connectionState,omitempty"`
	CaughtUpCount    int    `json:"caughtUpCount,omitempty"`
	PendingDecisions int    `json:"pendingDecisions,omitempty"`

	// subagent 内部产生的 tool_use / tool_result 在这里附上外层 Agent.tool_use_id；
	// 主 agent 自己的工具留空。前端据此把子 block 从主 transcript 移走，挂到父卡。
	ParentToolCallID string `json:"parentToolUseId,omitempty"`
	// SubagentRunID 在同一父调用的 normalized parallel/chain runs 间分组；
	// 缺失时前端保留为父卡 fallback step，不得丢弃或猜测归属。
	SubagentRunID string `json:"subagentRunId,omitempty"`

	// StreamSubagent* 事件填充：外层 Agent.tool_use_id + 元数据快照。
	// 前端按 ToolCallID 找到对应的 ChatBlock 并 merge Subagent 字段。
	Subagent *ChatBlockSubagent `json:"subagent,omitempty"`

	// StreamSubagentModel 事件填充：ToolCallID(复用上方字段)关联到对应派遣，Model 是
	// 子代理内部帧解析出的实际模型(R2 覆盖 R1 的入参别名)。只带这一个字段，不复用
	// 上面的 Subagent 全量快照 —— SubagentStateBlock.Status 的 JSON 标签没有
	// omitempty，若把整个快照甩给前端的浅合并，会把已有状态覆盖成空串。
	Model string `json:"model,omitempty"`

	// StreamAskUserQuestion 事件填充：交互问题载荷或答完后的状态切换。
	AskUserQuestion *ChatBlockAskUserQuestion `json:"askUserQuestion,omitempty"`

	// StreamToolPermissionRequest 事件填充：审批载荷或审批后的状态切换。
	ToolPermission *ChatBlockToolPermission `json:"toolPermission,omitempty"`
	ExecApproval   *ChatBlockExecApproval   `json:"execApproval,omitempty"`

	// StreamRetry 事件填充：后端/上游的非终态重试通知。本轮 turn 继续运行。
	RetryAttempt     int    `json:"retryAttempt,omitempty"`
	RetryMaxAttempts int    `json:"retryMaxAttempts,omitempty"`
	RetryMessage     string `json:"retryMessage,omitempty"`
	RetryDetails     string `json:"retryDetails,omitempty"`
	RetryAt          int64  `json:"retryAt,omitempty"`

	// StreamSessionStatus 事件填充：session 级 status patch。
	SessionStatus *ChatSessionStatusPatch `json:"sessionStatus,omitempty"`

	// StreamUsage 事件填充：当前 assistant 的 per-call token 快照。
	Usage *ChatStreamUsage `json:"usage,omitempty"`

	// StreamCompactBoundary 事件填充:压缩边界元数据 + 落库的 assistantMessageId
	// (前端按 messageId + boundary 在 blocks 中的位置切分折叠)。
	Compact *ChatCompactBoundary `json:"compact,omitempty"`

	// StreamRuntimeStatus 事件填充：runtime 中间状态快照。
	RuntimeStatus *ChatRuntimeStatus `json:"runtimeStatus,omitempty"`

	// StreamAutonomousStarted 事件填充：Stream 是该自主轮的 per-turn 事件名(前端
	// openStream 订阅它接后续 chunk/done);Trigger 是触发来源("background_task")。
	// AssistantMessage 复用上面的字段携带要插入的新 assistant 行。
	Stream  string `json:"stream,omitempty"`
	Trigger string `json:"trigger,omitempty"`

	// StreamSubagentActivityStarted 事件填充：LaunchMessageID 是后台 subagent 所属的
	// 发起消息 ID,前端据此定位 AgentSpawnCard 并重开 per-turn 流(Stream 字段)。
	// ToolCallID 复用上方字段,标识具体的 subagent tool_use block。
	// StreamAutonomousFinished 复用 LaunchMessageID 携带该收尾的 assistant / 发起消息
	// ID,前端据此定位并兜底 finishStream 那条 per-turn 流。
	LaunchMessageID int64 `json:"launchMessageId,omitempty"`

	// StreamAutonomousStarted 时,若该自主轮由后台命令完成触发,带上完成任务身份,
	// 前端据此把对应 subagent_state(上一条消息里)即时翻成 completed/failed。
	CompletedTask *CompletedTaskRef `json:"completedTask,omitempty"`
}

// CompletedTaskRef 标识触发本自主轮的后台命令身份。镜像 agentruntime.CompletedBackgroundTask
// 中前端需要的字段:ToolCallID 关联到上一条消息里的 subagent_state 块,Status 指明
// 该块要翻成的终态,Summary 是 CLI 下发的完成摘要文本（如退出码说明）。
type CompletedTaskRef struct {
	ToolCallID string `json:"toolUseId"`
	Status     string `json:"status"`            // completed | failed
	Summary    string `json:"summary,omitempty"` // CLI task_notification.summary
}

// ChatCompactBoundary 是 StreamCompactBoundary 事件的 payload。MessageID 是 boundary
// 所挂的 assistant message ID(同 turn 内自然 = TurnContext.assistantMsg.ID);Seq 该消息
// 在会话内的顺序号(前端按 Seq + 这个 blockIndex 定位边界);PreTokens / Trigger 透传
// CLI compact_metadata 字段(零值表示 CLI 没下发);At 是落库的 unix 毫秒,跟 block 同值。
type ChatCompactBoundary struct {
	MessageID int64  `json:"messageId"`
	Seq       int    `json:"seq"`
	PreTokens int    `json:"preTokens,omitempty"`
	Trigger   string `json:"trigger,omitempty"` // "auto" | "manual"
	At        int64  `json:"at"`
}

// ChatRuntimeStatus 是 StreamRuntimeStatus 事件的 payload。
// Status 是 runtime 上报的中间状态字符串（如 "compacting" / "requesting"）。
// Compacting 为 true 时前端把 typing indicator 切换为压缩动画。
type ChatRuntimeStatus struct {
	Status     string `json:"status"`
	Compacting bool   `json:"compacting,omitempty"`
}

// ChatStreamUsage 是 StreamUsage 事件 payload。字段与 ChatMessage 上的 token 列同名；
// runtime translator 已按 provider 家族算好 TotalInputTokens，前端直接使用。
// ContextWindow 可与 token 快照同帧到达，供 Composer 原子更新分子与分母。
type ChatStreamUsage struct {
	MessageID           int64 `json:"messageId,omitempty"`
	PromptTokens        int   `json:"promptTokens,omitempty"`
	CompletionTokens    int   `json:"completionTokens,omitempty"`
	CachedTokens        int   `json:"cachedTokens,omitempty"`
	CacheCreationTokens int   `json:"cacheCreationTokens,omitempty"`
	ReasoningTokens     int   `json:"reasoningTokens,omitempty"`
	// TotalInputTokens runtime translator 按 family 聚合的本次 API call 输入大小。
	// 前端不再做 family 判断,直接读这个值显示 "已用上下文"。
	TotalInputTokens int `json:"totalInputTokens,omitempty"`
	// ContextWindow 与 usage 同帧携带的模型窗口分母。0 表示 runtime 尚未探到；
	// 非零时前端与 liveUsage 原子写入，避免独立事件的订阅竞态。
	ContextWindow int `json:"contextWindow,omitempty"`
}

// ChatSessionStatusPatch 是 StreamSessionStatus 事件的 payload。
// AgentStatus 总是带最新值（idle/running/waiting/error），NeedsAttention 也总是带；
// 前端按字段直接覆盖 ChatSessionDetail 即可，不需要再做 diff。
type ChatSessionStatusPatch struct {
	AgentStatus    string `json:"agentStatus"`
	NeedsAttention bool   `json:"needsAttention"`
	// BgRunning 会话是否有后台 subagent 在跑(run_in_background)。总是带最新值；
	// 独立于 AgentStatus——后台 subagent 期间 AgentStatus 仍为 idle。见 bg_running.go。
	BgRunning bool `json:"bgRunning"`
	// PermissionMode 可选：只在 CLI 通报 permission mode 变更时填（被动 ExitPlanMode 流程）。
	// 缺省（omitempty）时前端不动 ChatSessionDetail.permissionMode；带值时直接覆盖。
	PermissionMode string `json:"permissionMode,omitempty"`
	// ContextWindow 可选:runtime 探到模型实际窗口大小时填(codex modelContextWindow)。
	// 前端按非零值覆盖 ChatSessionDetail.contextWindow;0 = 没探到,保留旧值。
	ContextWindow int `json:"contextWindow,omitempty"`
}

// ChatBlock 是 backend → 前端的简化投影：把 cago/agents StoredBlock 拍平。
// 已支持的 Type：text / thinking / tool_use / tool_result / notice / ask_user_question / unknown（兜底）。
type ChatBlock struct {
	Type  string `json:"type"`
	Text  string `json:"text,omitempty"`  // text / thinking / tool_result / notice 文本
	Level string `json:"level,omitempty"` // notice 级别

	// notice 块专用:供应商回退提示的会话所选 provider_key（spec 2026-08-09 决策 8）,
	// 或供应商切换提示切换后的会话级 key（2026-08-10 决策 9,空串 = 改回跟随 agent 绑定）。
	// 持久化时编码进 cago blocks.NoticeBlock.Text 的小 JSON,投影(noticeBlockToChatBlock)
	// 解回这里;非结构化旧 notice 无此字段,前端回退到 Text 原样渲染。
	ProviderKey string `json:"providerKey,omitempty"`
	// ProviderName 是 ProviderKey 对应供应商的展示名(2026-08-10 显示缺陷修复决策 1/2):
	// 后端产出 notice 时按当前解析到的供应商实体填入,查不到(供应商已删)时留空。前端
	// 渲染时优先用它,空则回退到 ProviderKey —— transcript 要读得懂"改用了哪个供应商"
	// 而不是一串 UUID。
	ProviderName string `json:"providerName,omitempty"`
	// ModelKey 是切换 notice 里切换后的会话级 ModelKey（2026-08-11 决策 1）：固定模型
	// 切换时非空，provider-default / 跟随 agent 绑定恒为空。
	ModelKey string `json:"modelKey,omitempty"`
	// ModelName 是 ModelKey 对应模型的展示名（后端产出 notice 时已解析到的实体），查不
	// 到时留空；前端渲染时优先用它，空则回退到 ModelKey。
	ModelName string `json:"modelName,omitempty"`
	// NoticeKind 区分 notice 的来源:""=供应商回退提示(含全部旧数据),"switch"=用户
	// 切换了会话供应商。前端据它选 t() 文案 —— 切回「跟随 agent 绑定」时 ProviderKey
	// 为空,只有这个字段能把它与「无结构化负载的旧 notice」区分开。
	NoticeKind string          `json:"noticeKind,omitempty"`
	Image      *ChatBlockImage `json:"image,omitempty"`

	// tool_use:
	ToolCallID string         `json:"toolUseId,omitempty"`
	ToolName   string         `json:"toolName,omitempty"`
	ToolInput  map[string]any `json:"toolInput,omitempty"`

	// tool_result:
	IsError bool `json:"isError,omitempty"`
	// ToolResultMeta backend 在 tool_result 旁吐的工具结构化元数据(claudecode CLI
	// 顶层 tool_use_result;codex 当前不发)。Claude Code 的 TaskCreate 走这条通道
	// 把系统分配的 task id 喂给前端 —— CLI 不在 tool input 里回 id。前端 task-progress
	// 派生层据此把 TaskCreate ↔ TaskUpdate 关联起来。普通工具帧没有 meta 时留 nil。
	ToolResultMeta map[string]any `json:"toolResultMeta,omitempty"`

	// 当前 block 是 subagent 内部产生时，指向外层 Agent.tool_use_id；
	// 主 agent 自己的 block 留空。前端按它把子 block 归集到父 SubagentInvocationCard。
	ParentToolCallID string `json:"parentToolUseId,omitempty"`
	// SubagentRunID 可选；空值表示 runtime/remote peer 未提供，仍作为父卡 fallback step 下行。
	SubagentRunID string `json:"subagentRunId,omitempty"`

	// 仅外层 Agent / Task 工具的 tool_use block 上填，缓存 subagent 元数据快照
	// （subagent_type / 累计 token / last_tool_name / status 等）。
	Subagent *ChatBlockSubagent `json:"subagent,omitempty"`

	// ask_user_question block 专用：交互问题与答题状态。
	AskUserQuestion *ChatBlockAskUserQuestion `json:"askUserQuestion,omitempty"`

	// tool_permission_request block 专用：工具审批载荷与决策状态。
	ToolPermission *ChatBlockToolPermission `json:"toolPermission,omitempty"`

	// exec_approval block 专用：OpenClaw Gateway exec 审批生命周期。
	ExecApproval *ChatBlockExecApproval `json:"execApproval,omitempty"`

	// tool_approval block 专用：agent 内置工具(org / hook 等)写操作审批卡。
	ToolApproval *ChatBlockToolApproval `json:"toolApproval,omitempty"`

	// Canonical 是 runtime translator 算出的统一工具识别投影 — wire 形态由
	// chat_svc/view/CanonicalDTO 提供。前端按 kind 分发到 canonical-tool/<kind>/card.tsx。
	// Live emit 路径:dispatcher_emitter 从 handler m["canonical"] 转;
	// Replay 路径:view/project.go 重建 block 时按 runtime translator 重算。
	Canonical *view.CanonicalDTO `json:"canonical,omitempty"`

	// Compact 仅 type="compact_boundary" 时填:压缩边界元数据(pre_tokens / trigger / at)。
	// 前端按 trigger 区分文案、按 at 显示时间,按"最后一条 compact_boundary 之前"切分折叠。
	Compact *ChatBlockCompactBoundary `json:"compact,omitempty"`

	Raw map[string]any `json:"raw,omitempty"` // unknown 兜底
}

type ChatBlockImage struct {
	Name      string `json:"name,omitempty"`
	MediaType string `json:"mediaType"`
	DataURL   string `json:"dataUrl"`
}

// ChatBlockCompactBoundary 是 type=compact_boundary block 的 wire payload,
// 镜像 blocks.CompactBoundaryBlock 三个字段。
type ChatBlockCompactBoundary struct {
	PreTokens int    `json:"preTokens,omitempty"`
	Trigger   string `json:"trigger,omitempty"`
	At        int64  `json:"at"`
}

// ChatBlockAskUserQuestion 是前端渲染 AskUserQuestion 卡片需要的全部状态。
//
// RequestID 来自 control_request.request_id，是前端答题后回传 AnswerUserQuestion
// 的句柄。ToolCallID 关联到同 turn 内 assistant 帧里的 tool_use 块；race 情况下
// （control_request 比 tool_use 先到）可能为空，前端按 RequestID 占位、等 tool_use
// 帧到了 merge。
//
// Answered + Answers + Skipped 在用户提交后更新；持久化到 chat_messages.blocks
// 让历史回放也能看到"已选 X / 用户跳过"。
type ChatBlockAskUserQuestion struct {
	RequestID string                  `json:"requestId"`
	Questions []blocks.AskQuestionDTO `json:"questions"`
	Answered  bool                    `json:"answered,omitempty"`
	Answers   []blocks.AskAnswerDTO   `json:"answers,omitempty"`
	Skipped   bool                    `json:"skipped,omitempty"`
	Expired   bool                    `json:"expired,omitempty"`
}

// ChatBlockToolPermission 是前端渲染工具审批卡片需要的全部状态。
//
// RequestID 来自 control_request.request_id，是前端审批后回传 AnswerToolPermission
// 的句柄。ToolInput 是 control_request.input 解析后的对象（前端按 ToolName 自行
// pretty-print，比如 Bash 突出 command 字段）。
//
// Resolved + Allowed + AlwaysAllow 在用户审批后更新；持久化到 chat_messages.blocks
// 让历史回放也能看到"已允许 / 已拒绝"。
type ChatBlockToolPermission struct {
	RequestID   string         `json:"requestId"`
	ToolName    string         `json:"toolName"`
	ToolInput   map[string]any `json:"toolInput"`
	Resolved    bool           `json:"resolved,omitempty"`
	Allowed     bool           `json:"allowed,omitempty"`
	AlwaysAllow bool           `json:"alwaysAllow,omitempty"`
}

type ChatBlockExecApproval struct {
	ID               string   `json:"id"`
	CommandText      string   `json:"commandText"`
	CommandPreview   string   `json:"commandPreview,omitempty"`
	AllowedDecisions []string `json:"allowedDecisions,omitempty"`
	Host             string   `json:"host,omitempty"`
	NodeID           string   `json:"nodeId,omitempty"`
	AgentID          string   `json:"agentId,omitempty"`
	Status           string   `json:"status"`
	Decision         string   `json:"decision,omitempty"`
	ResolvedBy       string   `json:"resolvedBy,omitempty"`
	CreatedAtMs      int64    `json:"createdAtMs,omitempty"`
	ExpiresAtMs      int64    `json:"expiresAtMs,omitempty"`
	ResolvedAtMs     int64    `json:"resolvedAtMs,omitempty"`
}

// ChatBlockToolApproval agent 内置工具(org / hook 等)写操作审批卡的前端投影。
// ToolKey 标识来源工具,前端据此选标题/文案与 approved 后处理。
type ChatBlockToolApproval struct {
	ToolKey   string         `json:"toolKey"`
	RequestID string         `json:"requestId"`
	ToolName  string         `json:"toolName"`
	ToolInput map[string]any `json:"toolInput,omitempty"`
	Status    string         `json:"status"`
	Result    string         `json:"result,omitempty"`
}

// ChatBlockSubagent 是 claudecode.SubagentMeta / agentruntime.SubagentInfo 在前端投影里的镜像。
//
// task_started 给到完整 prompt / subagent_type；task_progress 阶段性带 last_tool_name + cumulative usage；
// task_notification 给 status + 最终 usage。所有字段对老数据自动为零值，向前兼容。
type ChatBlockSubagent struct {
	TaskID          string                     `json:"taskId,omitempty"`
	Kind            string                     `json:"kind,omitempty"` // local_bash | local_agent（区分后台 bash 与 subagent；空=未知/旧帧）
	SubagentType    string                     `json:"subagentType,omitempty"`
	TaskDescription string                     `json:"taskDescription,omitempty"`
	Prompt          string                     `json:"prompt,omitempty"`
	LastToolName    string                     `json:"lastToolName,omitempty"`
	ToolUses        int                        `json:"toolUses,omitempty"`
	TotalTokens     int                        `json:"totalTokens,omitempty"`
	DurationMs      int                        `json:"durationMs,omitempty"`
	Status          string                     `json:"status,omitempty"`  // waiting | running | completed | failed | canceled | skipped | unknown
	Summary         string                     `json:"summary,omitempty"` // CLI task_notification.summary（如退出码说明）
	Mode            string                     `json:"mode,omitempty"`
	Runs            []agentruntime.SubagentRun `json:"runs,omitempty"`
	// Resumes 是每一次「中断后被重开」时的既有终态,镜像
	// blocks.SubagentStateBlock.Resumes；长度即恢复次数,空=从未中断过。
	Resumes []blocks.SubagentInterruption `json:"resumes,omitempty"`
	// Model 是子代理内部 assistant 帧解析出的实际模型(R2),first-wins(R3)。镜像
	// blocks.SubagentStateBlock.Model；空值表示尚未有内部帧到达 / 老会话数据。
	Model string `json:"model,omitempty"`
}

type ChatMessage struct {
	ID                  int64       `json:"id"`
	SessionID           int64       `json:"sessionId"`
	Role                string      `json:"role"`
	Blocks              []ChatBlock `json:"blocks"`
	Model               string      `json:"model"`
	PromptTokens        int         `json:"promptTokens"`
	CompletionTokens    int         `json:"completionTokens"`
	CachedTokens        int         `json:"cachedTokens"`
	CacheCreationTokens int         `json:"cacheCreationTokens"`
	ReasoningTokens     int         `json:"reasoningTokens"`
	// TotalInputTokens runtime translator 按 family 聚合好的本次 API call 输入大小。
	// 前端 Composer 进度条「已用上下文」按此读,不再做 backend-family-specific 加法。
	TotalInputTokens int     `json:"totalInputTokens"`
	DurationMs       int     `json:"durationMs"`
	FirstTokenMs     int     `json:"firstTokenMs,omitempty"`
	TokensPerSec     float64 `json:"tokensPerSec,omitempty"`
	ErrorText        string  `json:"errorText"`
	Seq              int     `json:"seq"`
	Createtime       int64   `json:"createtime"`
	// SourceDevice 是 R17 的「来源设备标识」:非本机发出的用户消息才带(为空=本机/未知)。
	// 携带的是提交方设备指纹;前端拿它与本机指纹比对,相等就不渲染来源标识(本机不带,
	// 单客户端界面零变化)。
	SourceDevice string `json:"sourceDevice,omitempty"`
	// SourceDeviceName 是对应设备的显示名(auth.pair 时上报),渲染「来自 <设备名>」;
	// 空 = 无可用名字,前端回退到来源指纹/通用文案。
	SourceDeviceName string `json:"sourceDeviceName,omitempty"`
	// BlocksLoaded 说明 Blocks 是不是这条消息的**全部**正文。
	//
	// 读路径是「元数据全量 + 块按需取」(决策 6):LoadSession 只给最近一个窗口的完整
	// 正文,更早的消息先只给元数据(BlocksLoaded=false,Blocks 空或只含派生视图点名的
	// 那几类块),前端向上滚动时再经 LoadMessageBlocks 取回。转录只渲染
	// BlocksLoaded=true 的消息 —— 把半份正文当整份渲染,用户看到的就是缺了工具结果的
	// 假转录。
	BlocksLoaded bool `json:"blocksLoaded"`
}

type ChatSessionLite struct {
	ID int64 `json:"id"`
	// AgentID / ProjectID 是会话的两个归属维度（ProjectID = 0 即未挂项目）。
	//
	// 单一会话索引按其中一维分组时，行首要放**另一维**（决策 4：按项目分组时行首是
	// agent 头像，按 Agent 分组时是项目色文件夹字形；决策 5 的时间轴两维都给）。
	// 此前 Lite 两个都不带 —— 项目归属只有 ChatSessionDetail 有，也就是「这条会话被
	// 打开过」之后才知道，侧栏拿不到。
	AgentID        int64  `json:"agentId"`
	ProjectID      int64  `json:"projectId"`
	Title          string `json:"title"`
	Status         string `json:"status"`
	NeedsAttention bool   `json:"needsAttention"`
	BgRunning      bool   `json:"bgRunning"`
	LastMessageAt  int64  `json:"lastMessageAt"`
	// LastReadAt 由 chat_svc.MarkSessionRead 推进；前端 sidebar 折叠态 attention bubble 用
	// LastMessageAt > LastReadAt 判定「未读」。
	LastReadAt int64 `json:"lastReadAt"`
}

type ChatSessionDetail struct {
	ID                 int64  `json:"id"`
	AgentID            int64  `json:"agentId"`
	AgentName          string `json:"agentName"`
	AgentColor         string `json:"agentColor"`
	AgentIcon          string `json:"agentIcon"`
	AgentAvatarDataURL string `json:"agentAvatarDataUrl"`
	// BackendType 是 agent 绑定的 backend 类型（builtin/claudecode/codex），
	// 前端用来决定「复制启动命令」等仅对 CLI 后端有效的菜单项是否可见。
	BackendType string `json:"backendType"`
	// LLMProviderType 是 backend 绑定的主 LLM provider 类型（anthropic / openai-chat /
	// openai-response）。前端用它和 BackendType 一起判定 Usage 字段语义：Anthropic 系
	// 的 PromptTokens 只含未缓存输入，要叠加 CachedTokens + CacheCreationTokens 才是
	// 总上下文；OpenAI 系的 PromptTokens 已是总数。空串表示后端未绑定 provider（CLI 登录态）。
	//
	// 按 **effective provider**（会话 provider_key > agent 绑定，spec 2026-08-10
	// 「有效供应商解析（唯一口径）」）解析，不再单看 agent 绑定 —— 否则会话换了个不同
	// 类型 / 不同上下文窗口的供应商后，用量条与上下文占比会按另一个供应商算错。
	LLMProviderType string `json:"llmProviderType"`
	// ProviderKey 是这条会话自己选的 LLM 供应商 key；空串 = 跟随 agent 绑定。
	// AgentProviderKey 是该会话所用那一档 backend 绑定的 key；两者都空 = CLI 登录态。
	// 前端 composer 的供应商 pill 用这两个值渲染标签（已选 → 供应商名；未选 → agent
	// 绑定供应商名；皆无 → 「选择供应商」占位）。
	ProviderKey      string `json:"providerKey"`
	AgentProviderKey string `json:"agentProviderKey"`
	// ModelKey 是这条会话自己选的 ModelKey（spec 2026-08-11 决策 1）：与 ProviderKey 组合
	// 成会话 ModelTarget（双空 = inherit-agent；providerKey 非空 + 空 = provider-default；
	// 双非空 = fixed-model）。AgentModelKey 是该会话所用那一档 backend 绑定的固定模型
	// key（未绑固定模型时为空）。前端 composer 的供应商 pill 用这四个值水合当前选择并
	// 渲染「跟随绑定」标签。
	ModelKey      string `json:"modelKey"`
	AgentModelKey string `json:"agentModelKey"`
	Title         string `json:"title"`
	AgentStatus   string `json:"agentStatus"`
	// ActiveStream 仅在 LoadSession 时填:该会话有正在跑的 turn 时,给出其 per-turn
	// wails 事件名("chat:event:<sessionID>:<assistantMessageID>"),让中途打开本会话的
	// 前端 openStream 重挂到实时流。子 agent 调用轮 / 自主轮等"非前端发起"的 turn 没有 Send
	// 响应入口,只能靠这个字段重挂。无活跃 turn 时为空(omitempty),前端不重挂。
	ActiveStream string `json:"activeStream,omitempty"`
	// ConnectionState 是本机与执行该会话那台远端 daemon 之间的**通道**状态
	// (connected / reconnecting / lost),不是第五个 AgentStatus —— 断连期间远端仍在跑,
	// 会话照旧是运行中。整页重载会清空前端的连接态 store,而 ActiveStream 仍非空
	// (断连不再终结会话),不随本响应同步带回它,重连的整个退避窗口里用户看到的都是
	// 普通打字指示器。补发一次事件不行:前端在本响应**之后**才订阅 chat:conn:<sid>。
	ConnectionState string `json:"connectionState"`
	// NeedsAttention 是由 AgentStatus=="waiting" 派生的兼容字段，不单独持久化。
	// 前端 toolbar 同时叠 displayStatus 兜底：即便 session_status stream 事件丢失，
	// LoadSession 拉到这个字段为 true 也能把状态翻成橙色 WAITING。
	NeedsAttention bool `json:"needsAttention"`
	// BgRunning 会话是否有后台 subagent 在跑；LoadSession 时从内存 bgRunning map 填充，
	// 与 session_status 事件同源，让页面重载后状态不丢。
	BgRunning     bool  `json:"bgRunning"`
	LastMessageAt int64 `json:"lastMessageAt"`
	LastReadAt    int64 `json:"lastReadAt"`
	Createtime    int64 `json:"createtime"`
	// ContextWindow 当前 agent 绑定 backend 的主 LLM provider 的上下文窗口（token 数）。
	// 解析顺序：provider.ContextWindow > 0 → 直接用；否则 cago 内置 catalog 兜底；都没有 → 0
	// （前端约定 0 时不展示上下文用量条）。
	ContextWindow int `json:"contextWindow"`
	// PermissionMode 是 CLI 后端会话当前模式：claudecode 使用 permission mode，
	// codex 使用 default / plan collaboration mode 子集。空串是历史兼容值；
	// 前端按 backend normalize 成对应默认值显示。builtin 不使用。
	PermissionMode string `json:"permissionMode"`
	// PermissionModeAtLaunch 是 claudecode CLI 子进程 spawn 时下发的 mode 快照；
	// runtime Shift+Tab 切换不动它。前端 pill 用它判定 bypass 选项是否还可点。
	// 空串表示「还没 spawn 过 / 老会话」。
	PermissionModeAtLaunch string `json:"permissionModeAtLaunch"`
	// 远端 device 归属 + 远端 cwd, 给前端 chat header 渲染"远端运行"小字使用。
	// 空 DeviceID = 本机 —— 含 R13 认领后 DeviceID 是本机指纹的档,它们在这里一律
	// 收敛成空串(remote_device_svc.ExternalDeviceID),因为本机永远不在配对表里,
	// 照远端解析会渲染成一台没名字的离线机器。非空 = 真正的另一台机器(规范指纹,
	// 或历史 paired_agentred.id 字符串)。
	// DeviceName 来自 paired_agentreds.display_name;Online 由 LastSeenAt 推算;
	// 本机档两者都留零值(前端按空 DeviceID 走本机分支,不读它们)。
	// Cwd 是该 session 真正的工作目录:本地 = project.path (或 AgentCwd 兜底);
	// 远端 = project_locations.path (resolveSessionCwd 已经做完路由)。
	DeviceID   string `json:"deviceID"`
	DeviceName string `json:"deviceName"`
	Online     bool   `json:"online"`
	Cwd        string `json:"cwd"`
	// ProjectID = 0 表示自由会话；> 0 时受 project_svc 管控。
	// 前端 ChatPanel 用它派生 breadcrumb 路径。
	ProjectID int64 `json:"projectId,omitempty"`
	// ExecTargetCount 是这个会话所属 Agent 的有序执行目标列表长度（R15）。前端聊天头
	// 用它放宽 chip 显示守卫：多档 Agent 的会话总是显示机器 chip（含本机），单档维持
	// 今天「只有远端会话才显示」的行为（R20）。0/1 与「未绑执行目标列表」（老 Agent）
	// 在这里不做区分——两者都不该显示 chip。
	ExecTargetCount int `json:"execTargetCount"`
	// CwdUnavailableReason 在 Cwd 为空时给出结构化原因（R10）：Wails 边界只过
	// Error() 字符串，没有结构化通道，前端因此没法从一个失败的 RPC 反推出"是本机
	// 未配置路径,还是远端没配,还是自由会话压根没有 cwd"——这个字段把
	// resolveSessionCwd 的错误分类透出来，供会话文件面板据此展示 R10 专用的
	// 空态文案而不是一律笼统的"没有工作目录"。取值：""(Cwd 非空，或没有可归类的
	// 原因) / "local-path-missing"（本机未配置路径，ProjectLocalPathMissing）/
	// "location-missing"（远端机器未配置路径，ProjectLocationMissing）。
	CwdUnavailableReason string `json:"cwdUnavailableReason,omitempty"`
}

// BlockReason 是不可对话 Agent 的结构化原因枚举；空串 = 可对话（与 Chattable=true 一致）。
// 由 ListAgents 在与 Chattable/ChattableHint 相同的判定点位设置，取值见下。
// 前端（spec docs/specs/2026-08-08-setup-guidance.md 决策表）按它映射引导文案与
// 主按钮跳转；ChattableHint 保留为兜底展示字段。
type BlockReason string

const (
	// BlockReasonNoBackend 该 Agent 没绑后端（含 CEO）。
	BlockReasonNoBackend BlockReason = "no-backend"
	// BlockReasonBackendRequiresProvider 后端需要但找不到绑定的 LLM 供应商。
	BlockReasonBackendRequiresProvider BlockReason = "backend-requires-provider"
	// BlockReasonProviderInactive 后端绑的供应商存在但未激活/缺 Key。
	BlockReasonProviderInactive BlockReason = "provider-inactive"
	// BlockReasonRemoteProviderMissing 远端 agentred 未配置该供应商。
	BlockReasonRemoteProviderMissing BlockReason = "remote-provider-missing"
	// BlockReasonGatewayNotRunning 本地网关未启动，CLI 后端暂不可用。
	BlockReasonGatewayNotRunning BlockReason = "gateway-not-running"
	// BlockReasonRemoteOpenClawUnavailable 远端 OpenClaw 暂不可用。
	BlockReasonRemoteOpenClawUnavailable BlockReason = "remote-openclaw-unavailable"
	// BlockReasonUnknownBackend 未知 Agent 后端类型。
	BlockReasonUnknownBackend BlockReason = "unknown-backend"

	// 以下三个是 R15 执行目标挑选专用的原因，与上面几个「backend 自身不可用」的判据
	// 正交：它们描述的是这一档所在的机器 / 项目路径，不是 backend 配置本身。

	// BlockReasonExecTargetUnpaired 本机没有配对这一档指向的那台 agentred（R2b：判据
	// 是本地配对表里有没有这一行，不是有没有配对令牌）。
	BlockReasonExecTargetUnpaired BlockReason = "exec-target-unpaired"
	// BlockReasonExecTargetOffline 已配对，但该 agentred 当前不在线。
	BlockReasonExecTargetOffline BlockReason = "exec-target-offline"
	// BlockReasonExecTargetDesktopNotRunning 目标是一台具名桌面端，且它的 Agentre App
	// 没有运行（R2：与「机器离线」是两种说法——一个是开应用，一个是开机）。
	BlockReasonExecTargetDesktopNotRunning BlockReason = "exec-target-desktop-not-running"
	// BlockReasonExecTargetProjectPathMissing 会话绑定了项目，但这一档所在的机器上
	// 没有配置这个项目的路径（决策 34）。不绑项目的会话不受这一项约束。
	BlockReasonExecTargetProjectPathMissing BlockReason = "exec-target-project-path-missing"
)

type ChatAgentItem struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	AvatarColor   string `json:"avatarColor"`
	AvatarIcon    string `json:"avatarIcon"`
	AvatarDataURL string `json:"avatarDataUrl"`
	BackendType   string `json:"backendType"`
	// DefaultPermissionMode 是 claudecode 后端管理员预设的 spawn 时 mode；其它后端
	// （codex / builtin）一律留空。前端新会话场景下用它作为 pill 起手值兜底，并把
	// 同值随 SendChatMessage.permissionMode 透回，让 chat_svc.createPermissionMode
	// 的「raw 非空就直接用」分支照样落到管理员预设上。
	DefaultPermissionMode string `json:"defaultPermissionMode"`
	// LLMProviderKey 是 backend 绑定的 provider key；空串 = 未绑（CLI 登录态）。
	// 前端无会话 composer ModelPill 用它判定新会话已绑/未绑：非空 → 走 /v1/models 列表；
	// 空 → 弹层内自由输入模型 id。
	LLMProviderKey string `json:"llmProviderKey"`
	Chattable      bool   `json:"chattable"`
	Pinned         bool   `json:"pinned"`
	ChattableHint  string `json:"chattableHint"`
	// BlockReason 是结构化不可对话原因；空串 = 可对话。取值见 BlockReason 常量，
	// 由 ListAgents 与 Chattable 同一判定点位设置，保持二者一致。
	BlockReason BlockReason `json:"blockReason"`
	// HasBackendTarget 表示 Agent 的执行目标列表里仍有后端引用。目标所指后端被
	// 软删除后 BlockReason 仍是 no-backend，但设置导航不应把这个不可见的残留
	// 关联当成一个可操作的“尚未配置”缺口。
	HasBackendTarget  bool              `json:"hasBackendTarget"`
	ActiveCount       int               `json:"activeCount"`
	RecentCount       int               `json:"recentCount"`
	TotalSessions     int64             `json:"totalSessions"`
	SessionIDs        []int64           `json:"sessionIds"`
	Sessions          []ChatSessionLite `json:"sessions"`
	AttentionSessions []ChatSessionLite `json:"attentionSessions"`

	// 远端 device 归属 — 给前端 DeviceTag 渲染本地/远端 chip 用。口径与
	// ChatSessionDetail 一致：空 DeviceID = 本机（含 R13 认领后带本机指纹的档，经
	// remote_device_svc.ExternalDeviceID 收敛）；非空 = 真正的另一台机器。
	// DeviceName 来自 paired_agentreds.display_name；Online 由 LastSeenAt 推算。
	DeviceID   string `json:"deviceID"`
	DeviceName string `json:"deviceName"`
	Online     bool   `json:"online"`
}

// ChatSessionGitState 是一次 git 状态快照。Branch 为空 + NotARepo=true 意味着 cwd
// 不在 git 仓库内, 前端把整个 chip 区折叠掉。HasUpstream=false 时 Ahead/Behind 不渲染。
type ChatSessionGitState struct {
	Branch      string `json:"branch"`
	Worktree    string `json:"worktree"`
	Dirty       int    `json:"dirty"`
	Ahead       int    `json:"ahead"`
	Behind      int    `json:"behind"`
	HasUpstream bool   `json:"hasUpstream"`
	NotARepo    bool   `json:"notARepo"`
	UpdatedAt   int64  `json:"updatedAt"`
}

type GetSessionGitStateRequest struct {
	SessionID int64 `json:"sessionId"`
}

type GetSessionGitStateResponse struct {
	State ChatSessionGitState `json:"state"`
}

// ── Request / Response shapes ────────────────────────────────────────────────

type ListAgentsRequest struct{}
type ListAgentsResponse struct {
	Agents []ChatAgentItem `json:"agents"`
}

type LoadSessionRequest struct {
	SessionID int64 `json:"sessionId"`
}
type LoadSessionResponse struct {
	Session  ChatSessionDetail `json:"session"`
	Messages []ChatMessage     `json:"messages"`
}

// LoadMessageBlocksRequest 是「向上滚动继续取更早的正文」的入参。
// BeforeSeq 是前端手上最早那条**已取到正文**的消息的 seq,取的是它之前的一段;
// Limit<=0 时用 TranscriptBlockWindow。
type LoadMessageBlocksRequest struct {
	SessionID int64 `json:"sessionId"`
	BeforeSeq int   `json:"beforeSeq"`
	Limit     int   `json:"limit"`
}

// LoadMessageBlocksResponse 里的消息一律 BlocksLoaded=true;HasMore 说明这一段之前
// 还有没有更早的消息可取。
type LoadMessageBlocksResponse struct {
	Messages []ChatMessage `json:"messages"`
	HasMore  bool          `json:"hasMore"`
}

// LoadSessionBlocksByTypeRequest 是派生视图(后台任务面板 / 大纲 / 变更)的取数入参:
// 点名它需要的**投影后**块类型,后端按块表的 type 列点查整条会话的这几类块。
// Types 为空是错误 —— 那等于把整条转录再要一遍。
type LoadSessionBlocksByTypeRequest struct {
	SessionID int64    `json:"sessionId"`
	Types     []string `json:"types"`
}

// LoadSessionBlocksByTypeResponse 覆盖整条会话的全部消息(元数据全量),每条只带点名
// 类型的块,因此 BlocksLoaded 一律为 false:这是派生视图的取数,不是转录正文。
type LoadSessionBlocksByTypeResponse struct {
	Messages []ChatMessage `json:"messages"`
}

// LocalCommandScope 是本地命令历史与命令执行共享的稳定设备/cwd 作用域。
// DeviceID 为空表示本机；Cwd 为空表示目标设备上的默认 Agent 工作目录。
type LocalCommandScope struct {
	DeviceID string `json:"deviceId"`
	Cwd      string `json:"cwd"`
}

// ResolveLocalCommandScopeRequest 接受且只接受一种目标：已有 SessionID，或尚未
// 持久化的 AgentID + ProjectID（ProjectID=0 表示自由会话）。
type ResolveLocalCommandScopeRequest struct {
	SessionID int64 `json:"sessionId"`
	AgentID   int64 `json:"agentId"`
	ProjectID int64 `json:"projectId"`
}

// ListAgentSessionsRequest 给「查看全部 N 个会话」popover 翻页拉数据用。
// Limit==0 时服务侧用默认页大小 20；上限 100。
type ListAgentSessionsRequest struct {
	AgentID int64 `json:"agentId"`
	Offset  int   `json:"offset"`
	Limit   int   `json:"limit"`
}
type ListAgentSessionsResponse struct {
	Sessions []ChatSessionLite `json:"sessions"`
	Total    int64             `json:"total"`
	HasMore  bool              `json:"hasMore"`
}

// SessionIndexScope 是单一会话索引的查询范围。
//
// 刻意用具名字符串而不是「projectID = -1 表示不限」这类哨兵：哨兵在 Wails 生成的
// TS 签名里读不出含义，调用方迟早传错一个 0 进来。
type SessionIndexScope = string

const (
	// SessionScopeRecent 全部会话按最近活动排序 —— 索引的「按时间」档。
	// 它跨 agent、跨项目，是唯一能给出「全局最近」的查询：按 agent 的变体各自只看
	// 一个 agent，并起来只是一个窗口。
	SessionScopeRecent SessionIndexScope = "recent"
	// SessionScopeFree 仅未挂项目（project_id = 0）的会话 —— 索引的「随手对话」组。
	// 自由会话此前没有任何列表接口能拿到：ListSessions 被挡在 projectID > 0，
	// 而 0 本来就不是一个项目。
	SessionScopeFree SessionIndexScope = "free"
	// SessionScopeProject 某个项目下的会话 —— 索引的项目组。
	//
	// 它与 free 只差一个 project_id，走同一条查询是有意的：索引三个轴拿到的是**同一种
	// 载荷**（ChatSessionLite，带 agent / 项目 / bgRunning / 已读），前端一处投影就够。
	// 旧的 ProjectListSessions 返回的是另一个形状（无 bgRunning、无 project_id），
	// 正是「同一条会话在两个页面显示不一样」的根。
	SessionScopeProject SessionIndexScope = "project"
	// SessionScopeMachine 跑在某一台机器上的会话 —— 索引的「按机器」轴那一组
	// （docs/specs/2026-08-21-index-glyph-and-machine-axis.md）。
	//
	// 分组这一维是 chat_entity.Session.ExecDeviceID，而**不是** ChatSessionLite 上那个
	// 从 backend 推出来的 DeviceID：前者是会话表上的一列（取数时就分得开），后者索引
	// 这条路根本没填。DeviceID = 0 是本机，是一台正当的机器。
	SessionScopeMachine SessionIndexScope = "machine"
	// SessionScopeAgent 某个 agent 名下的会话 —— 索引「按 agent」轴那一组。
	//
	// 不搜索时那条轴的会话由 ListAgents 顺带给出（每个 agent 前 5 条），不必为了摆一屏
	// 多发 N 个 RPC；**搜索时**那个前 5 条的窗口就不够了，得按 agent 各查一遍全量，
	// 这个 scope 就是为此存在的。AgentID 必须 > 0：0 不是「不限 agent」，recent 才是。
	SessionScopeAgent SessionIndexScope = "agent"
)

// ListIndexSessionsRequest 单一会话索引的分页查询。
// Limit==0 时服务侧用默认页大小 20；上限 100（与 ListAgentSessions 同一口径）。
type ListIndexSessionsRequest struct {
	Scope SessionIndexScope `json:"scope"`
	// ProjectID 仅在 Scope=project 时有意义，且必须 > 0：0 走 Scope=free。
	ProjectID int64 `json:"projectId"`
	// DeviceID 仅在 Scope=machine 时有意义。**0 合法**（本机），负数才是漏传 ——
	// 与 ProjectID 的判据差这一格，因为 0 在那边有专门的 scope，在这边是一台机器。
	DeviceID int64 `json:"deviceId"`
	// AgentID 仅在 Scope=agent 时有意义，且必须 > 0。
	AgentID int64 `json:"agentId"`
	Offset  int   `json:"offset"`
	Limit   int   `json:"limit"`
	// Keyword 是索引搜索框里那个词，与 Scope 正交：命中口径（会话标题 / agent 名 /
	// 项目名）由 repo 的 SessionIndexFilter 统一定义，每条轴都能叠。
	//
	// 它必须走取数而不是留给前端过滤：前端手上只有首屏那一页（项目组 5 条 / 时间轴
	// 30 条），在那上面做匹配等于「只搜得到最近几条」。带上它之后 Total / HasMore
	// 也一并是过滤后的口径，组头计数与翻页因此不会和列表打架。
	Keyword string `json:"keyword"`
}

type ListIndexSessionsResponse struct {
	Sessions []ChatSessionLite `json:"sessions"`
	Total    int64             `json:"total"`
	HasMore  bool              `json:"hasMore"`
}

type SessionPurpose string

const (
	// SessionPurposeSubagentCall 子 agent 调用的一次性隔离会话(每次新建, 不复用)。
	// 值与落库的 chat_entity.SessionPurposeSubagent 同源, 防两处字面量漂移。
	SessionPurposeSubagentCall SessionPurpose = SessionPurpose(chat_entity.SessionPurposeSubagent)
	// SessionPurposeUserChat 普通用户会话(每次新建)。供 ! 命令在「新会话占位态」先坐实一个
	// 真实会话用 —— 与子会话不同, 落库 Purpose 留空, 出现在侧栏、可继续对话。这是请求层的
	// 派发键, 不与某个 chat_entity.SessionPurpose 同源(普通会话本就是空 Purpose)。
	SessionPurposeUserChat SessionPurpose = "user_chat"
)

type EnsureSessionRequest struct {
	Purpose   SessionPurpose
	AgentID   int64
	ProjectID int64
	Title     string
}

type EnsureSessionResponse struct {
	SessionID int64
	Created   bool
}

type SendRequest struct {
	SessionID int64       `json:"sessionId"` // 0 = 新建
	AgentID   int64       `json:"agentId"`
	Text      string      `json:"text"`
	Images    []SendImage `json:"images,omitempty"`
	// 新建会话路径（SessionID=0）专用：把会话挂到指定项目。
	// 已存在的会话不应再传 ProjectID —— Send 会忽略它，project 在 Create 时定型。
	ProjectID int64 `json:"projectId,omitempty"`
	// PermissionMode 是 CLI 后端会话启动模式：
	//   - claudecode: default / acceptEdits / plan / bypassPermissions
	//   - codex: default / plan
	// 空串表示不改已有会话；新建 codex 会话空串按 default 落库。
	PermissionMode string `json:"permissionMode,omitempty"`
	// ExecTargetOverride 是 R15a 的手动指定：仅新建会话时生效（与 ProviderKey 同一条
	// 规则），值是用户在空会话态「改选」浮层里选中的 agentBackendID。0 = 不指定，按
	// R15 顺序自动挑第一个可用的档。非零时必须在该 Agent 的执行目标列表里且此刻可用，
	// 否则整个 Send 失败（不静默回落自动挑选）——拒绝指定一个不可用的档。已有会话
	// （SessionID>0）忽略这个字段：会话早已按 R15b 钉在它落到的那一档上，不可能再改。
	ExecTargetOverride int64 `json:"execTargetOverride,omitempty"`
	// ProviderKey 仅新建会话（SessionID=0）生效：所选 LLM ModelTarget 的 ProviderKey，
	// 随首条消息与 Session 一同 Create 落库（spec 决策 2）。空串 = 跟随 agent 绑定
	// （inherit-agent）。已有会话在这里忽略——改 target 走 SetChatSessionModelTarget
	// （2026-08-10 决策 1 / 2026-08-11 决策 1）。非空时校验：供应商必须存在、IsActive 且
	// 与后端 kind 兼容（ProviderTypeMatch），否则 Send 报错。
	ProviderKey string `json:"providerKey,omitempty"`
	// ModelKey 仅新建会话（SessionID=0）生效：所选 ModelTarget 的 ModelKey。
	//   - 空 = provider-default（providerKey 非空时）或 inherit-agent（providerKey 空）；
	//   - 非空 = fixed-model（必须存在、启用且归属所选 Provider）。
	// 与 ProviderKey 一并随 Session 落库；已有会话忽略。
	ModelKey string `json:"modelKey,omitempty"`
	// EmitTurnStartedBypass 表示本轮由"非查看者"发起(子 agent 调用经 subagent_svc
	// 阻塞起轮),需经会话级旁路 chat:autonomous:<sessionId> 把 per-turn 流名推给该会话
	// 已打开(可能在后台)的 ChatPanel, 让它翻 running + openStream —— 否则只有发起者
	// (前端 Send 响应)能拿到流名。
	// 前端 Send 默认 false: 发起者自己已从响应拿到流名, 重复推会双开流。子 agent 调用用; 普通会话空。
	EmitTurnStartedBypass bool `json:"-"`
	// peerSource is populated only by the account-peer adapter. Keeping it out
	// of Wails JSON prevents a local caller from forging a source pill.
	peerSource peerMessageSource
	// conversationID 只由对端适配器填(R17:浏览器把新对话派到这台桌面端上跑,
	// 号是浏览器铸的)。非空时新建的会话行就用这个身份落库 —— 本机另铸一个会让
	// 同一条对话在两侧有两个身份,对端此后再也 attach 不上它。同样不进 Wails
	// JSON:本地调用方不能指定一条对话的身份。已存在的会话忽略这个字段。
	conversationID string
}
type SendImage struct {
	Name    string `json:"name,omitempty"`
	DataURL string `json:"dataUrl"`
}

// ── 拖拽图片读取 (ReadDroppedImages) ─────────────────────────────────────────

const (
	DroppedImageKindImage = "image"
	DroppedImageKindPath  = "path"
)

type ReadDroppedImagesRequest struct {
	Paths []string `json:"paths"`
}

type ReadDroppedImagesResponse struct {
	Items []DroppedImageItem `json:"items"`
}

// DroppedImageItem 是单个拖入路径的归类结果。
//   - Kind=="image": 可作图片附件,Name/MediaType/DataURL 给出。
//   - Kind=="path":  降级为纯路径(目录/超限/类型不符/读失败),调用方应把 Path 当文本插入。
type DroppedImageItem struct {
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	Name      string `json:"name,omitempty"`
	MediaType string `json:"mediaType,omitempty"`
	DataURL   string `json:"dataUrl,omitempty"` // 空 for Kind==path
}

type SendResponse struct {
	SessionID          int64  `json:"sessionId"`
	UserMessageID      int64  `json:"userMessageId"`
	AssistantMessageID int64  `json:"assistantMessageId"`
	Stream             string `json:"stream"`
}

type CompactRequest struct {
	SessionID int64 `json:"sessionId"`
}

type CompactResponse struct {
	SessionID          int64  `json:"sessionId"`
	AssistantMessageID int64  `json:"assistantMessageId"`
	Stream             string `json:"stream"`
}

type ChatGoal struct {
	ThreadID        string `json:"threadId"`
	Objective       string `json:"objective"`
	Status          string `json:"status"`
	TokenBudget     *int   `json:"tokenBudget,omitempty"`
	TokensUsed      int    `json:"tokensUsed"`
	TimeUsedSeconds int    `json:"timeUsedSeconds"`
	CreatedAt       int64  `json:"createdAt"`
	UpdatedAt       int64  `json:"updatedAt"`
}

type GoalRequest struct {
	SessionID int64 `json:"sessionId"`
}

type SetGoalRequest struct {
	SessionID   int64   `json:"sessionId"`
	Objective   *string `json:"objective,omitempty"`
	Status      *string `json:"status,omitempty"`
	TokenBudget *int    `json:"tokenBudget,omitempty"`
}

type StartGoalRequest struct {
	AgentID        int64   `json:"agentId"`
	ProjectID      int64   `json:"projectId,omitempty"`
	PermissionMode string  `json:"permissionMode,omitempty"`
	Objective      *string `json:"objective,omitempty"`
	Status         *string `json:"status,omitempty"`
	TokenBudget    *int    `json:"tokenBudget,omitempty"`
}

type StartGoalResponse struct {
	SessionID int64     `json:"sessionId"`
	Goal      *ChatGoal `json:"goal,omitempty"`
}

type ClearGoalRequest struct {
	SessionID int64 `json:"sessionId"`
}

type GoalResponse struct {
	Goal *ChatGoal `json:"goal,omitempty"`
}

type ClearGoalResponse struct {
	Cleared bool `json:"cleared"`
}

type RenameRequest struct {
	SessionID int64  `json:"sessionId"`
	Title     string `json:"title"`
}
type RenameResponse struct{}

type DeleteRequest struct {
	SessionID int64 `json:"sessionId"`
}
type DeleteResponse struct{}

// MarkSessionReadRequest 把 last_read_at 推进到至少 Timestamp (unix ms)。
// Timestamp <= 0 时服务侧改用当前时间。语义单调：repo 层只在新 ts 严格大于旧值时落库。
type MarkSessionReadRequest struct {
	SessionID int64 `json:"sessionId"`
	Timestamp int64 `json:"timestamp"`
}
type MarkSessionReadResponse struct{}

// RegenerateRequest 触发"从指定 assistant 消息重新生成"：
//   - 截掉对应 user 消息（含）开始的所有 chat_messages
//   - 用同一段 user 文本重新走一遍 turn
//
// builtin 通过截 DB + history 重建生效；claudecode 走 provider_anchor fork；
// codex 按目标 user 到末尾的 user 消息数执行 thread/rollback。
type RegenerateRequest struct {
	SessionID      int64  `json:"sessionId"`
	MessageID      int64  `json:"messageId"` // 目标 assistant 消息 id
	PermissionMode string `json:"permissionMode,omitempty"`
}

// EnqueueRequest 是 AI 还在回答时用户发的「下一条」消息。service 把它
// 注入到当前正在跑的 turn（claudecode 走 SteerInbox + PostToolUse hook —
// 没被 hook 拉走的残留在 turn 结束时由 runTurn DrainPending 收尾自动起新一轮；
// codex 走 turn/steer RPC）。不会落 chat_messages 表 —— 语义上是"下次 AI
// 看到的提示"，不是历史消息。
type EnqueueRequest struct {
	SessionID int64  `json:"sessionId"`
	Text      string `json:"text"`
	// peerSource is private to the authenticated peer adapter; local enqueue
	// calls retain their current source-free behavior.
	peerSource peerMessageSource
}

// EnqueueResponse 把刚入队消息的稳定 ID 回传给前端。前端按它显示 chip 并
// 用作后续 CancelQueued 的 handle。Cancellable=false 表示当前后端（codex）
// 一发即不可撤，前端把对应 chip 上的 X 替换为锁图标。
type EnqueueResponse struct {
	SessionID   int64  `json:"sessionId"`
	Queued      bool   `json:"queued"`
	QueuedID    string `json:"queuedId"`
	Cancellable bool   `json:"cancellable"`
}

// CancelQueuedRequest 撤回排队消息。QueuedID 为空 = 清空当前会话的整条队列。
type CancelQueuedRequest struct {
	SessionID int64  `json:"sessionId"`
	QueuedID  string `json:"queuedId"`
}

// CancelQueuedResponse 返回实际被撤回的 queued ID 列表（FIFO）。前端用它
// 同步前端的 queue state。
type CancelQueuedResponse struct {
	Removed []string `json:"removed"`
}

// StopRequest 用户点「停止」中断当前 turn。SessionID 标识哪个会话；当前会话
// 必须正在跑（agentStatus=running/waiting），否则返回 ChatStopNoActive。
type StopRequest struct {
	SessionID int64 `json:"sessionId"`
}

// StopResponse Stopped=true 表示 abort 路径已经触发（不代表 turn 此刻已完全结束
// —— 异步 cleanup 在 runTurn goroutine 完成）。前端按 StreamAborted 事件翻 UI。
type StopResponse struct {
	Stopped bool `json:"stopped"`
}

// StopBackgroundTaskRequest 用户点某条后台任务 / 子 agent 的「停止」。ToolCallID 是发起它
// 的 tool_use_id（前后端统一 join key）；chat_svc 据此从持久化 subagent_state 块读出 CLI
// task_id 再下发 stop_task —— 停的是这一个后台任务，不是整个 turn。
type StopBackgroundTaskRequest struct {
	SessionID  int64  `json:"sessionId"`
	ToolCallID string `json:"toolUseId"`
}

// StopBackgroundTaskResponse Stopped=true 表示 stop_task 已下发（或任务已是终态 / 已被
// evict，按幂等成功处理）。前端乐观把该行翻「已停止」并 reload 对齐 DB。
type StopBackgroundTaskResponse struct {
	Stopped bool `json:"stopped"`
}

// SetPermissionModeRequest 用户切换 CLI 会话模式。
// claudecode 可取 {default, acceptEdits, plan, bypassPermissions}；
// codex 可取 {default, plan}。
//
// 持久化语义：mode 总是写入 chat_sessions.permission_mode 后再尝试下发到
// 当前活跃 CLI 子进程。如果 CLI 还没起 / 已被 LRU evict，runtime 下发会被
// 跳过（不报错），下一次 spawn CLI 时会读 DB 用 --permission-mode 启动；
// 因此前端切 pill **不需要**先发一条消息把进程拉起来。
type SetPermissionModeRequest struct {
	SessionID int64  `json:"sessionId"`
	Mode      string `json:"mode"`
}

// SetPermissionModeResponse Applied=true 表示请求已被后端接受（DB 已落）。
// runtime 是否已即时下发到活跃 CLI 由后端 best-effort，CLI 不在时下次 spawn
// 自然生效。前端不需要区分这两种情形。
type SetPermissionModeResponse struct {
	Applied bool   `json:"applied"`
	Mode    string `json:"mode"`
}

// SetChatSessionModelTargetRequest 切换已有会话的 LLM ModelTarget（spec 2026-08-11
// 决策 1）。ProviderKey 空 = 改回「跟随 agent 绑定」（inherit-agent，CLI 后端即回到自身
// 登录态）；ProviderKey 非空 + ModelKey 空 = provider-default（每轮解析该 Provider 当前
// 默认）；两者都非空 = fixed-model。
type SetChatSessionModelTargetRequest struct {
	SessionID   int64  `json:"sessionId"`
	ProviderKey string `json:"providerKey"`
	ModelKey    string `json:"modelKey"`
}

// SetChatSessionModelTargetResponse 回传落库后的会话级 target 与该会话所用那一档 backend
// 的绑定 target，前端据此立刻更新 pill 标签，不必再拉一次 LoadSession（乐观 UI 对账）。
// 新 target 自下一轮生效：正在进行的轮不受影响（决策 8）。
//
// persisted（ProviderKey / ModelKey）是刚写入 chat_sessions 的两列；agent 绑定
// （AgentProviderKey / AgentModelKey）供「跟随 agent 绑定」回落标签渲染。effective 目标
// = persisted 非空取 persisted，否则取 agent 绑定。
type SetChatSessionModelTargetResponse struct {
	ProviderKey      string `json:"providerKey"`
	ModelKey         string `json:"modelKey"`
	AgentProviderKey string `json:"agentProviderKey"`
	AgentModelKey    string `json:"agentModelKey"`
}

// LaunchCommandRequest / Response 用于「复制启动命令」菜单：
// 把当前 session 关联的 CLI 后端配置拼成可在终端粘贴运行的命令。
// Token 字段固定为占位符 <TOKEN>，不发放实际 token；用户自行替换。
type LaunchCommandRequest struct {
	SessionID int64 `json:"sessionId"`
}

type LaunchCommandResponse struct {
	Command     string `json:"command"`
	BackendType string `json:"backendType"`
}

// EditRequest 触发"编辑历史 user 消息并重跑"：
//   - 截掉目标 user 消息（含）开始的所有 chat_messages
//   - 用新文本 Text 走一遍 turn
//
// 跟 Regenerate 共用 fork 路径：claudecode 走 provider_anchor fork；
// codex 走 thread/rollback；builtin 通过截 DB + history 重建生效。
type EditRequest struct {
	SessionID      int64  `json:"sessionId"`
	MessageID      int64  `json:"messageId"` // 目标 user 消息 id
	Text           string `json:"text"`      // 新的 user 文本
	PermissionMode string `json:"permissionMode,omitempty"`
}
