package agentruntime

//go:generate mockgen -source runner.go -destination mock_agentruntime/mock_runner.go

import (
	"context"
	"encoding/json"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

// EventKind 统一事件离散类型。chat_svc 按这个枚举做 switch。
type EventKind string

const (
	EventTextDelta     EventKind = "text_delta"
	EventThinkingDelta EventKind = "thinking_delta"
	// EventOutputActivity 模型开始产出一个输出块(含工具入参这类不可见输出)。
	// 纯计时信号,只用于记首 token,见 event.go 的 OutputActivity。
	EventOutputActivity EventKind = "output_activity"
	EventToolUseStart   EventKind = "tool_use_start"
	EventToolUseEnd     EventKind = "tool_use_end"
	EventToolResult     EventKind = "tool_result"
	EventSteerConsumed  EventKind = "steer_consumed"
	// subagent 生命周期（claudecode 与 Pi runtime 产生；codex 不发）。
	// Pi 的 stateful drain 在这里承载 parallel/chain 的 mode + runs 全量快照；
	// claudecode 的 legacy 单运行生产者可继续不填 Runs。
	EventSubagentStarted  EventKind = "subagent_started"
	EventSubagentProgress EventKind = "subagent_progress"
	EventSubagentDone     EventKind = "subagent_done"
	// EventSubagentModel claudecode 从 subagent 内部帧解析出的实际模型（R2：实际执行
	// 覆盖调用意图）。独立事件类型，刻意不复用 EventSubagentProgress ——
	// SubagentProgressHandler 对 ToolUses/TotalTokens/LastToolName 是无条件赋值，混进
	// 一个只带模型的 SubagentInfo 会把已累计的进度清零（R4）。chat_svc 把它并入对应
	// 派遣的 subagent 累计态，只更新模型字段。first-wins（R3，同一子代理只认第一次）
	// 由 chat_svc 累计态负责；claudecode 侧每次遇到都如实产出，不做去重。
	EventSubagentModel EventKind = "subagent_model"
	// EventAskUserQuestion backend 检测到 ask_user_question 类型的工具调用
	// 时 emit（Claude Code 的内置 AskUserQuestion、Codex / 内置 Agent
	// 后续注册的同语义 function tool 都翻译到这里）。service 接住后 push
	// 给前端渲染卡片；用户答完后通过 AskAnswerSink 反向投回给具体 backend，
	// 由 backend 各自实现"同 turn 注入答案"的协议细节。
	EventAskUserQuestion EventKind = "ask_user_question"
	// EventAskUserQuestionAnswered backend 完成 SubmitAnswer 反向投回后 emit。
	// 用于把 Answered/Skipped/Answers 状态写回 acc 里那条 AskUserQuestionBlock,
	// 这样 turn finalize 时 SetBlocks 落盘的 chat_messages.blocks 就是终态;
	// service 同时 forward 一条 StreamAskUserQuestion 给前端做兜底 merge。
	EventAskUserQuestionAnswered EventKind = "ask_user_question_answered"
	// EventPlanUpdated carries Codex collaboration plan updates. It is separate
	// from EventToolUseStart because the UI needs a visible assistant plan even
	// when Codex does not emit normal text in plan mode.
	EventPlanUpdated EventKind = "plan_updated"
	// EventToolPermissionRequest backend 收到 can_use_tool（除 AskUserQuestion 以外）
	// 时 emit。service 推前端渲染审批卡，用户决定 allow/deny 后通过
	// ToolPermissionSink 反向投回 backend。前端可以读 Input 自己 pretty-print。
	EventToolPermissionRequest EventKind = "tool_permission_request"
	// EventToolPermissionResolved backend 完成 SubmitToolPermission 反向投回后 emit。
	// service 把终态 patch 回 acc 里那条 ToolPermissionRequestBlock，并 forward
	// 一条 StreamToolPermissionResolved 给前端做兜底 merge。
	EventToolPermissionResolved EventKind = "tool_permission_resolved"
	// EventExecApprovalRequested / Resolved model the OpenClaw Gateway exec
	// approval lifecycle. Approval resolution is not tool execution completion.
	EventExecApprovalRequested EventKind = "exec_approval_requested"
	EventExecApprovalResolved  EventKind = "exec_approval_resolved"
	// EventPermissionModeChanged claudecode CLI 通报自身 permission mode 已变更
	// （被动 ExitPlanMode 流程 / 主动 set_permission_mode 回执）。
	// chat_svc 接住后落 chat_sessions.permission_mode 并推 StreamSessionStatus patch。
	EventPermissionModeChanged EventKind = "permission_mode_changed"
	EventRetry                 EventKind = "retry"
	// EventUsage backend 在 turn 中途上报「本次 API call 之后模型当前看到的输入大小」。
	// claudecode 每个主 agent assistant 帧、codex 的 token_count notification 各产一条。
	// chat_svc 接住后更新 assistantMsg 的 token 列 + 落库 + emit StreamUsage 给前端，
	// 让 Composer 进度条在 turn 内随工具循环阶梯式刷新。Usage 字段必填，其它字段留空。
	EventUsage           EventKind = "usage"
	EventCompactBoundary EventKind = "compact_boundary"
	// EventRuntimeStatus backend 通报会话级运行状态字符串 (compacting / requesting ...)。
	// 与 EventPermissionModeChanged 同源帧但承载独立信号 —— chat_svc 推一条 stream
	// event,前端在 Composer / typing indicator 处显示 "正在压缩上下文…" 等过渡 chip。
	EventRuntimeStatus EventKind = "runtime_status"
	EventError         EventKind = "error"
	EventDone          EventKind = "done"
	// EventUserMessage (R18):daemon 在「开新一轮」事件流开头注入的发起方标记(见
	// event.go 的 UserMessageEvent)。它是 wire 事件流的一部分,走既有的
	// runtime.event 通知 / journal / 补齐,不需要额外的通知通道。
	EventUserMessage EventKind = "user_message"
	// EventUnrecognizedBlock 携带一条发送方投射不出来的转录块,原样。
	//
	// 它存在是为了让「我读不懂这一块」本身过得了 wire,而不是被悄悄丢掉:接收方
	// 可能是认得这个 block_type 的新版本;就算不认得,摆一块不透明的内容也好过
	// 转录里凭空少一段(R8)。
	//
	// 只有历史合成会产出它 —— 实时事件流里的一切本来就是密封事件。
	EventUnrecognizedBlock EventKind = "unrecognized_block"
)

// ToolUseEvent EventToolUseStart / End 携带。Input 是原始 JSON；chat_svc 自己 unmarshal 到 map。
//
// ParentToolCallID：当前 tool_use 是 subagent 内部调用时指向外层 Agent.tool_use_id；
// SubagentRunID：同一外层 parallel/chain 中稳定的输入槽 ID。主 agent 自己的工具留空。
// 注:JSON wire 字段仍叫 parentToolUseId（来自 Anthropic CLI 协议），仅 Go field 重命名。
//
// Subagent：仅外层 Agent / Task 父调用上填，透传 claudecode.SubagentMeta 元数据。
type ToolUseEvent struct {
	ID               string
	Name             string
	Input            []byte
	ParentToolCallID string
	SubagentRunID    string
	Subagent         *SubagentInfo
}

// ToolResultEvent EventToolResult 携带。
//
// ResultMeta 透传 backend 在 tool_result 旁吐的工具结构化元数据（claudecode 走 CLI
// 顶层 tool_use_result；codex 当前不发）。原始 JSON,留给 chat_svc 落 ChatBlock,
// 前端按工具语义 Unmarshal。无 meta 的工具留 nil。
type ToolResultEvent struct {
	ToolCallID       string
	Content          string
	IsError          bool
	ParentToolCallID string
	SubagentRunID    string
	ResultMeta       []byte
}

// SubagentInfo 是 runtime 层的 backend-neutral subagent 快照，由 EventSubagent*
// 事件以及外层 Agent 工具的 ToolUseEvent 携带。legacy 字段镜像
// claudecode.SubagentMeta；Mode/Runs 承载 Pi 的 normalized 单/并行/链式运行模型。
// Runs 是全量快照：消费者在非 nil 时整片替换，nil 表示 legacy 事件省略、不得清空
// 已有 runs。Status 是所有 runs 的 aggregate 状态，单个 run 状态保存在 Runs[i]。
type SubagentInfo struct {
	TaskID          string        `json:"taskId,omitempty"`
	SubagentType    string        `json:"subagentType,omitempty"`
	Kind            string        `json:"kind,omitempty"` // local_bash | local_agent（区分后台 bash 与 subagent；空=未知/旧帧）
	TaskDescription string        `json:"taskDescription,omitempty"`
	Prompt          string        `json:"prompt,omitempty"`
	LastToolName    string        `json:"lastToolName,omitempty"`
	ToolUses        int           `json:"toolUses,omitempty"`
	TotalTokens     int           `json:"totalTokens,omitempty"`
	DurationMs      int           `json:"durationMs,omitempty"`
	Status          string        `json:"status,omitempty"` // aggregate: waiting | running | completed | partial | failed | canceled | skipped | unknown
	Mode            string        `json:"mode,omitempty"`
	Runs            []SubagentRun `json:"runs,omitempty"`
	// Summary 是该 task 收尾时生产者给出的整体结论:claudecode 的
	// task_notification.summary(成功时是子代理交回的报告全文,失败时是中断原因,
	// 如 "Agent terminated early due to an API error: ...")。Runs 为空的 legacy
	// 生产者只有这一处承载结论。
	Summary string `json:"summary,omitempty"`
}

// SubagentRun is one normalized child execution scoped by the outer tool call.
// Legacy Claude producers may leave SubagentInfo.Runs empty.
type SubagentRun struct {
	ID             string `json:"id"`
	Index          int    `json:"index"`
	Agent          string `json:"agent,omitempty"`
	Profile        string `json:"profile,omitempty"`
	AgentSource    string `json:"agentSource,omitempty"`
	Task           string `json:"task"`
	RequestedModel string `json:"requestedModel,omitempty"`
	Model          string `json:"model,omitempty"`
	Status         string `json:"status"`
	LastToolName   string `json:"lastToolName,omitempty"`
	ToolUses       int    `json:"toolUses,omitempty"`
	Summary        string `json:"summary,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
}

// ConsumedSteer is a queued mid-turn user message that the backend has now
// incorporated into the active conversation.
//
// R17: daemon 在收到这条 steer 的 Steer RPC 时,把**提交方**对端设备指纹 / 设备名盖到
// SourcePeer / SourceName 上,随 SteerConsumed 密封事件一路带到桌面端。桌面端拿
// SourcePeer 与本机指纹比对,相等就不渲染来源标识(本机不带);SourceName 是渲染
// 「来自 <设备名>」的文案(空 = 无可用名字,前端回退到指纹/通用文案)。
// 本机 builtin 后端产生的 steer 没有这两个字段(空),呈现与今天完全一致。
type ConsumedSteer struct {
	QueuedID string
	Text     string
	// 新增字段走 lowerCamelCase(现有 QueuedID/Text 无 tag、线上是 PascalCase,不能改:
	// 老桌面端按字段名解,改名会让老客户端把 QueuedID/Text 解成空)。
	SourcePeer string `json:"sourcePeer,omitempty"`
	SourceName string `json:"sourceName,omitempty"`
}

// RetryEvent is a non-terminal backend retry notification. It is surfaced to
// the UI while the turn keeps running.
type RetryEvent struct {
	Message           string
	AdditionalDetails string
	Attempt           int
	MaxAttempts       int
}

// RuntimeEvent 统一流事件。
type RuntimeEvent struct {
	Kind       EventKind
	Text       string
	ToolUse    *ToolUseEvent
	ToolResult *ToolResultEvent
	Steers     []ConsumedSteer
	Retry      *RetryEvent
	Err        error

	// EventSubagent* 携带：Subagent 元数据；外层 Agent.tool_use_id 放在 ToolCallID。
	Subagent   *SubagentInfo
	ToolCallID string

	// EventAskUserQuestion 携带：解析后的 question 列表 + backend 提供的
	// requestID（claudecode 走 control_request.request_id；codex 走
	// item/tool/requestUserInput 的 JSON-RPC request id）。
	AskUserQuestion *AskUserQuestionEvent

	// EventPlanUpdated 携带：Codex app-server 上报的当前计划步骤。
	Plan []PlanStep
	// PlanText 携带 Codex app-server 以 plan item 形式输出的完整 Markdown plan。
	// 新版 app-server 在 plan mode 下发 item/plan/delta + item/completed{type:"plan"}，
	// 不再一定发 turn/plan/updated。
	PlanText string

	// EventToolPermissionRequest / EventToolPermissionResolved 携带。
	ToolPermission *ToolPermissionEvent

	// EventPermissionModeChanged 携带：CLI 通报切换后的新 mode 值。
	// 合法值同 pkg/claudecode PermissionMode 白名单（default/plan/acceptEdits/bypassPermissions）。
	PermissionMode string

	// EventUsage 携带：本次 API call 之后模型当前看到的输入大小（per-call usage）。
	// 非 nil 才有效；chat_svc 据此 patch assistantMsg.PromptTokens 等列并 emit StreamUsage。
	Usage *provider.Usage

	// TotalInputTokens EventUsage 携带:由 runtime translator 按 family 已聚合的
	// 总输入(Anthropic = prompt + cached + cacheCreation;OpenAI = prompt)。
	// 新 sealed Event 必填;老 RuntimeEvent 路径靠 chat.go 自己做家族数学兜底。
	TotalInputTokens int
}

// ToolPermissionEvent EventToolPermission{Request,Resolved} 携带。
//
// RequestID 是 backend 私有的回写句柄（claudecode = control_request.request_id），
// service 在调 ToolPermissionSink.SubmitToolPermission 时按它定位 waiter。
//
// Resolved 字段仅 EventToolPermissionResolved 时填：SubmitToolPermission 完成
// 反向投回后 emit 一帧带这些字段，让 service 把终态 patch 回 acc 里那条
// ToolPermissionRequestBlock，确保 turn finalize 时落盘正确。
type ToolPermissionEvent struct {
	RequestID string
	// ToolCallID 关联到 assistant 流里的 tool_use 块；race 时（control_request 比
	// tool_use 先到）允许为空。claudecode emitter 不填(走 RequestID 单 key 索引);
	// 新 sealed Event 直填。
	ToolCallID string
	ToolName   string
	Input      []byte // 原 control_request.input 透传字节；前端自己 JSON.parse
	// Resolved 状态：
	Resolved    bool
	Allowed     bool
	AlwaysAllow bool // 是否勾选了 "Allow for this session"
	// DenyReason 仅 Resolved=true && Allowed=false 时有意义,记录用户在审批卡上
	// 输入的拒绝理由(claudecode 把它塞进 PermissionResult.Message 回灌给 LLM)。
	// 老 RuntimeEvent 路径不回填(deny reason 直接走 SubmitToolPermission 入参);
	// 新 sealed Event 携带 reason 字段。
	DenyReason string
}

// ToolPermissionSink 由具体 backend runner 在 Run 启动时把 session-scoped 句柄
// 注入到 service；service 拿到前端 AnswerToolPermission 请求后调
// SubmitToolPermission 把决策投回该 session 当前阻塞的 control 协议。
//
// 各 backend 实现细节：
//   - claudecode：构造 PermissionResult 写一帧 control_response 到子进程 stdin；
//     allow 时 UpdatedInput 用原 input 解析的 map；alwaysAllow=true 时附加
//     updatedPermissions=[{type:"addRules", rules:[{toolName}], behavior:"allow",
//     destination:"session"}]，由 CLI SDK 自己维护后续 allow rules。
//
// denyReason 仅 allow=false 时生效：写入 PermissionResult.Message，CLI 把它
// 作为 tool_result 回灌给 LLM 让 AI 拿到具体反馈。空字符串走默认文案。
// allow=true 时忽略。
//
// 当前只有 claudecode 走这条；其它 backend 还没有等价工具审批协议，留接口扩展。
type ToolPermissionSink interface {
	SubmitToolPermission(ctx context.Context, sessionID int64, requestID string, allow, alwaysAllowSession bool, denyReason string) error
}

// AskUserQuestionEvent EventAskUserQuestion 携带。
//
// RequestID 是 backend 私有的回写句柄（claudecode = control_request.request_id），
// service 在调 AskAnswerSink.SubmitAnswer 时按它定位 waiter。
//
// ToolCallID 关联到 assistant 流里的 tool_use 块；race 时（control_request 比
// tool_use 先到）允许为空，前端 merge 时按 RequestID 占位。
type AskUserQuestionEvent struct {
	RequestID        string
	ToolCallID       string
	ParentToolCallID string
	Questions        []AskQuestion
	// Answered / Skipped / Answers 仅 EventAskUserQuestionAnswered 时填:
	// SubmitAnswer 完成后 emit 一帧带这些字段,让 service 层把终态 patch 回
	// acc 里那条 AskUserQuestionBlock,确保 turn finalize 时落盘正确。
	Answered bool
	Skipped  bool
	Answers  []AskAnswer
}

// AskAnswerSink 由具体 backend runner 在 Run 启动时把 session-scoped
// 句柄注入到 service；service 拿到前端 AnswerUserQuestion 请求后
// 调 SubmitAnswer 把答案投回该 session 当前阻塞的 control 协议。
//
// 各 backend 实现细节：
//   - claudecode：写一帧 control_response 到子进程 stdin，updatedInput.answers
//     按 question 文本聚合 csv labels（OtherAnswerLabel 已替换为 OtherText）
//   - codex：响应 app-server 的 item/tool/requestUserInput JSON-RPC 请求，
//     answers 按 Codex question id 聚合
//   - 内置 Agent：直接走 in-process chan
//
// skipped == true 表示用户跳过：各 backend 应回 deny 让 LLM 优雅看到拒答信号，
// 而不是 allow 一个空 map（会让 turn 静默挂死，见 hapi gotcha #4）。
type AskAnswerSink interface {
	SubmitAnswer(ctx context.Context, sessionID int64, requestID string, questions []AskQuestion, answers []AskAnswer, skipped bool) error
}

// PendingToolPermission is one still-blocked (non-AskUserQuestion) tool
// approval waiter, carrying the same fields ToolPermissionRequest raised it
// with the first time (RequestID / ToolName / Input) — enough for a
// reconnecting client to rebuild the approval card without a second,
// parallel payload shape.
//
// Input is json.RawMessage, not []byte, for the same reason
// ToolPermissionRequest.Input is: encoding/json renders []byte as base64,
// so the reconnect payload would carry the tool input in a different shape
// than the live event frame and the client would need two parsers for one
// card.
type PendingToolPermission struct {
	RequestID string
	ToolName  string
	Input     json.RawMessage
}

// PendingAskUserQuestion is one still-blocked AskUserQuestion waiter,
// carrying the same fields AskUserQuestionEvent raised it with the first
// time (RequestID / Questions) — enough to rebuild the question card.
type PendingAskUserQuestion struct {
	RequestID string
	Questions []AskQuestion
}

// WaiterSnapshot is the full set of a session's currently-blocked waiters,
// returned by WaiterLister.PendingWaiters. Either slice may be empty; a
// session with nothing pending returns the zero value.
type WaiterSnapshot struct {
	ToolPermissions  []PendingToolPermission
	AskUserQuestions []PendingAskUserQuestion
}

// WaiterLister is implemented by backend runtimes whose control protocol can
// enumerate its own currently-blocked waiters — the read half of
// ToolPermissionSink / AskAnswerSink's submit-by-requestID write half.
//
// Declared as its own narrow interface (ISP) rather than a third method
// bolted onto ToolPermissionSink / AskAnswerSink: the common caller only
// ever wants to answer one already-known requestID and should not have to
// also satisfy an enumeration method to do that, and a backend with no
// approval protocol at all should not have to stub one out just to keep
// implementing the sinks it does support.
//
// 实现者是有审批协议的那些 backend —— 今天是 claudecode（runtimes/claudecode/
// control.go）与 codex（runtimes/codex/runtime.go）两家。没有审批协议的 backend
// 不实现它；consumer 对它们 type-assert 失败后回落空 WaiterSnapshot，不是接口
// 本身返回错误（R7）。
//
// No error return: PendingWaiters is a synchronous snapshot read of
// in-memory state (mirrors SteerDrainer.DrainPending), not an I/O call.
// sessionID unknown to this runner (never spawned / already evicted) yields
// the zero-value WaiterSnapshot, same as "nothing pending right now" — R9
// forbids any waiter timeout, so there is no separate "expired" case to
// report either.
type WaiterLister interface {
	PendingWaiters(ctx context.Context, sessionID int64) WaiterSnapshot
}

type PlanStep struct {
	Step   string
	Status string
}

// HistoryMessage builtin 用，CLI runner 忽略（它们的 history 在 cliagent Session 内）。
type HistoryMessage struct {
	Role   string
	Blocks []blocks.ContentBlock
}

// MCPServerSpec 一个注入给 runtime 的 MCP tool server(http transport)。
type MCPServerSpec struct {
	Name    string            // server 名; claude tool 暴露为 mcp__<Name>__<tool>
	URL     string            // http MCP endpoint(如 http://127.0.0.1:<port>/mcp/group/)
	Headers map[string]string // 鉴权/scope header(如 {"Authorization":"Bearer <token>"})
	Tools   []string          // 允许的 tool 名(进 --allowedTools 为 mcp__<Name>__<tool>); 按角色裁剪
}

// RunRequest 一次 Send 的入参。
type RunRequest struct {
	Backend  *agent_backend_entity.AgentBackend
	Provider *llm_provider_entity.LLMProvider // 可为 nil（CLI 后端走自身 login）
	// Effective 是本轮执行侧唯一解析结果（EffectiveLLMConfig v1 seam）。
	// chat_svc 通过 ResolveTarget 解析产生；runtime 读取它决定实际 ModelID，
	// 不再各自从 Provider 旧单模型字段取值。native（无供应商）时为 nil 或
	// Mode=native 的配置。
	Effective *EffectiveLLMConfig
	AgentID   int64 // Agent 工作目录 key：<AppDataDir>/agents/<agentID>
	// SessionID 是这一轮在**本进程内**的会话身份：runner 的会话表（子进程缓存 /
	// waiter / 自主续轮通道）全按它索引，控制类接口（Steerer / Aborter /
	// ToolPermissionSink / WaiterLister …）收到的 sessionID 也是它。
	//
	// 桌面端进程里它就是 chat_sessions.ID（还兼作 provider session resume /
	// builtin conv id 的 key）。agentred 里**不是**：那里同时服务多台设备，而会话 id
	// 是各设备本地自增的、必然重号，daemon 因此在调 runner 前把对端指纹揉进这个值
	// （见 internal/daemon/handlers.runtimeSessionID）。runner 只需把它当成一个不透明的
	// 进程内唯一键，不要反解成 chat_sessions.ID、也不要拿它去查库。
	SessionID int64
	// Cwd 非空时直接用作 runner 工作目录；为空时各 runner 回退到 AgentCwd(AgentID)
	// 兜底（保留老的 Agent 级目录行为）。chat_svc 在拼 RunRequest 时调
	// project_svc.ResolveSessionCwd 解析 project 维度的 cwd 注入此字段，避免
	// agentruntime 反向依赖 project_svc。
	Cwd string
	// Title 是该会话此刻的标题（R7）。桌面端每轮携带当前值，远端 daemon 幂等覆盖；
	// 本地 runner 不用它。改名后最多滞后一轮生效。
	Title string
	// AgentSyncID 是该会话所属 Agent 的账号级同步标识（块 1 决策 3 的 ULID，不是本地
	// 自增 agent_id）。远端 daemon 落库并在会话列表里回传（R5 / R7）；本地 runner 不用它。
	AgentSyncID string
	// ProjectSyncID 是该会话所属项目的账号级同步标识。只有远端 runtime 用得上：它
	// 随一轮过线，让对端把项目记进自己的会话行（本地 runtime 的会话行就在本机，
	// 项目归属查得到，不需要携带）。空串 = 自由会话或项目还没认领标识。
	ProjectSyncID     string
	SystemPrompt      string
	ProviderSessionID string // 空 = 新建；非空 = resume
	// FreshSession 声明这一轮**必须起全新会话**(挂账修复 2026-08-11):即使远端 daemon
	// 在自家落库里存了这条会话的 provider_session_id,也不许续。决策 8 之后「空
	// ProviderSessionID」被重载成「用落库那份续话」,而 regenerate 无锚点 / provider 会话
	// 失效恢复这两条路径的空字段本意是「全新」—— 两者在 wire 上不可区分,daemon 拿旧 id
	// 顶掉就失效。chat_svc 在本地 sess.ProviderSessionID 为空(即没有可续的原生会话)时置
	// true;本地 runner 忽略它,只有远端 daemon 的续话逻辑读它。
	FreshSession bool
	UserText     string
	UserBlocks   []blocks.ContentBlock // 非空时为本轮用户输入权威 blocks；UserText 仅保留文本索引/兼容
	History      []HistoryMessage      // 仅 builtin
	GatewayURL   string                // 关联 provider 的 CLI 后端要；builtin 不用
	GatewayToken string                // 同上，一次性 token
	Compact      bool                  // Codex 原生 compact turn；不创建普通 user prompt

	// MCPServers 非空 = 给本轮 CLI 注入额外 MCP tool server。仅声明 CapMCPTools
	// 的 runtime(claudecode/codex)消费; 其它 runtime 忽略。org/subagent/hook 等工具经此注入。
	MCPServers []MCPServerSpec

	// EnabledPlugins 非空 = 给本轮 CLI 注入 plugin/skill-pack 覆盖(仅 agent 的显式
	// 覆盖:强制开=true / 强制关=false;未列出的 plugin 沿用全局 CLI 配置=继承)。
	// 仅声明 CapSkills 的 runtime 消费:claudecode 渲进 --settings,codex 渲进 --config;
	// 其它 runtime 忽略。
	EnabledPlugins map[string]bool

	// ForkAnchor 非空时 = "重新生成"路径：runner 应当把 provider 会话从 ForkAnchor
	// 之后的所有内容丢弃，再以 UserText 重发一次。
	//
	//   - claudecode: anchor 是 JSONL 里 user msg 的 parentUuid（来自上一轮 RunResult.UserAnchor）。
	//     runner 用 `--resume <ProviderSessionID> --resume-session-at <ForkAnchor> --fork-session`
	//     一次性完成 rewind + 新 turn；返回新的 ProviderSessionID（fork 出来的 sid）。
	//   - codex: anchor 是十进制 numTurns；runner 先调用 `thread/rollback`，
	//     再 resume 同一个 thread 发起新 turn。
	//   - builtin: 不使用 ForkAnchor；chat_svc 截 DB 后从 history 重建。
	ForkAnchor string

	// PermissionMode 仅 claudecode runner 在 spawn 新 CLI 子进程时透传给
	// --permission-mode；命中 LRU cache 的复用路径不重传（运行时切换走
	// PermissionModeSetter 接口的 control_request）。空串 = 不附 flag，让
	// pkg/claudecode args.go 的默认值 (acceptEdits) 生效。chat_svc.runTurn 从
	// chat_sessions.permission_mode 取值填入。
	PermissionMode string

	// CollaborationMode 仅 codex runner 使用，传给 app-server turn/start 的
	// collaborationMode.mode。合法值 default / plan；空串 = 不发字段，保留旧行为。
	CollaborationMode string

	// LLMProviderKey 是 desktop 端关联的 provider stable key（UUID）。
	// 远端 backend 场景下透传给 daemon，daemon 用 ProviderLookup.FindByKey
	// 解出本机 state 里的 provider，不需要 desktop 每个 turn 传 APIKey。
	// 本地 backend 场景下此字段不使用。
	LLMProviderKey string
}

// EffectiveProviderKey 返回本轮 Provider 的 key：无 effective provider（CLI 自身
// 登录态）返回空串,否则返回解析结果的 ProviderKey。
//
// 这是 chat_svc.providerKeyOf(prov) 在 runtime 层的同源取值 —— turn 入口已把
// 「会话 provider_key > agent 绑定」的解析结果（含缺失/停用回退）装进 req.Effective,
// claudecode / codex 的 CLI env/config 网关门控与 evict 比对键（spec 2026-08-10
// 决策 4/6）都应读这同一个值,不各自解析一遍导致漂移。
func (req RunRequest) EffectiveProviderKey() string {
	if req.Effective != nil {
		return req.Effective.ProviderKey
	}
	if req.Provider != nil {
		return req.Provider.ProviderKey
	}
	return ""
}

// EffectiveModelID 返回本轮解析出的实际模型 id（provider-default 每轮解析当前默认）；
// native（无供应商）时返回空串。runtime 用它替代旧 req.Provider.Model。
func (req RunRequest) EffectiveModelID() string {
	if req.Effective == nil {
		return ""
	}
	return req.Effective.ModelID
}

// RunResult 由 runner 在事件流 close 后填充；chat_svc 在 drain 完 channel 后读。
type RunResult struct {
	ProviderSessionID string
	Usage             *provider.Usage
	StopErr           error

	// TurnToken 是这一轮的 per-turn token(本会话内的递增序号,0 表示 runner 未
	// 生成)。Abort 携带它做精确寻址:非 0 时仅当该轮仍是当前活跃轮才中断,否则
	// stale no-op(决策 1)。claudecode / codex / builtin / piagent / openclaw
	// 在每轮入口递增一次;remote 侧由 daemon 生成后经 wire 透传回来。
	TurnToken uint64

	// UserAnchor 是 provider 端对本轮 user prompt 的稳定标识，用于将来
	// 「重新生成 assistant N」时反向定位到 user N 之前的会话点。
	//
	// 仅 claudecode 当前会填（读 ~/.claude/projects/<slug>/<sid>.jsonl 的 message uuid）；
	// codex / builtin 留空 —— codex 的 thread/rollback 数量由 chat_svc 按 user 消息数计算；
	// builtin 不存在 provider 端 session，重生由 chat_messages 自己截断。
	UserAnchor string

	// Model 本轮实际调用的模型 id：
	//   - builtin：解析出的 ModelID（chat_svc 已写过 message.Model，runner 不必再填）；
	//   - claudecode：从 system.init.model 抓，CLI login / 显式 --model 两条路径都走得通；
	//   - codex：取启动时的 spec.model，CLI 自身 login 时回退 Agentre 的默认模型。
	// 空字符串表示 runner 没办法可靠探到模型，调用方按自己的回退策略处理。
	Model string

	// ContextWindow 是 runtime 上报的模型上下文窗口大小（tokens）：
	//   - codex：从 thread/tokenUsage/updated 通知的 modelContextWindow 字段抓（部分版本 codex
	//     app-server 会推这个值）；
	//   - piagent：用 Pi RPC get_state.model.contextWindow 启动，再由
	//     get_session_stats.contextUsage.contextWindow 校正，避免自定义 provider
	//     复用公共模型名时误套 llmcatalog 元数据；
	//   - claudecode：通过 ContextWindowUpdated 事件实时上报，RunResult 通常留 0；
	//   - builtin：不报。
	// 0 表示 runner 没探到，chat_svc 用解析出的 ContextWindow > cago catalog 兜底。
	// 非 0 时是这一层最权威的优先级（用户实际跑出来的窗口）。
	ContextWindow int

	// LaunchPermissionMode 是 runtime spawn CLI 子进程时实际下发的
	// --permission-mode 值（claudecode 专用,其它 runtime 留空）。runner 在
	// Run() 返回前同步填好,chat_svc 拿到后写入 session.PermissionModeAtLaunch。
	//
	// 历史:旧实现 runtime 直接 chat_repo.Session().UpdatePermissionModeAtLaunch,
	// 在 agentred daemon(不 bootstrap chat_repo)里触发 nil panic。把状态回吐
	// 给 chat_svc 持久化,避免 runtime 层反向依赖 repository。
	LaunchPermissionMode string

	// ProviderFallbackKey 是决策 9 的回退信号(仅远端 remote runtime 会填):daemon 按
	// wire 的 effectiveProviderKey 自解失败(会话 provider_key 缺失/非 active)时回退
	// agent 绑定执行,并把被回退的 key 经 ack 透到这里。chat_svc 据此追加一条持久
	// notice(与本地 Q3 一致)。空串 = 未回退。
	ProviderFallbackKey string
}

// Steerer is implemented by Runtimes that support mid-turn message
// injection. Steer is called by chat_svc.Enqueue when the user sends a
// follow-up message while a turn is in flight. queuedID is generated by
// chat_svc per Enqueue call and is the handle used by SteerCanceler to
// withdraw a specific queued message before it's consumed.
type Steerer interface {
	Steer(ctx context.Context, sessionID int64, queuedID, text string) error
}

// SteerCanceler is implemented by BackendRunners that allow withdrawing
// a previously enqueued Steer message before the runner has consumed it
// (i.e. the AI hasn't yet seen it). Defined as a separate interface so a
// runner can support Steer but not Cancel — codex is the canonical example:
// its turn/steer RPC is fire-and-forget with no withdraw verb.
//
// queuedID matches the ID passed to Steer.
//
//   - empty queuedID = clear all pending entries for sessionID.
//   - returns the list of IDs actually removed in FIFO order (empty slice
//     when queuedID was non-empty but no entry matched).
//   - ErrSteerNotFound when queuedID is non-empty and not in the queue
//     (already consumed by the runtime or never existed).
type SteerCanceler interface {
	CancelSteer(ctx context.Context, sessionID int64, queuedID string) ([]string, error)
}

// SteerDrainer is implemented by BackendRunners that keep an in-memory queue
// of mid-turn steer messages waiting to be consumed by the model. After a turn
// ends, chat_svc calls DrainPending to take any leftovers and replay them as
// the user prompt of an auto-started next turn — this replaces the old
// claudecode Stop-hook trick (which used decision=block to keep the model
// iterating, but surfaced as a red "Stop hook error" banner in Claude's TUI
// and dropped messages that arrived during the hook's own execution).
//
// Returns nil/empty when there is nothing pending or no active session for
// sessionID. Implementations must clear their internal queue as part of
// returning the slice — Drain semantics, not Peek.
//
// **Side effect (race-fix)**: when the returned slice is non-empty, the
// implementation MUST atomically mark the session as "still in turn" again,
// so user Steer calls arriving between this DrainPending and the next
// runner.Run still succeed (otherwise they fall through to Send → lock held
// → ChatSendInFlight and the message is dropped). Caller MUST follow up with
// another Run for the merged text — failing to do so leaves the session
// stuck in active state until LRU evict / next manual Send.
//
// codex deliberately does NOT implement SteerDrainer: its turn/steer RPC is
// fire-and-forget, so there is no agentre-side queue to drain.
type SteerDrainer interface {
	DrainPending(ctx context.Context, sessionID int64) []ConsumedSteer
}

// AbortOutcome 描述一次 Abort 的结果:被中断的那一轮的类型。
//
// TurnKind == TurnKindNone 只随 ErrNoActiveTurn / stale no-op 返回(没有实际中断任何轮);
// 成功中断时带具体类型。
type AbortOutcome struct {
	TurnKind TurnKind
}

// TurnKind 标识被中断的那一轮的类型。
type TurnKind string

const (
	// TurnKindNone 没有中断任何轮(无活跃轮 / stale token no-op)。
	TurnKindNone TurnKind = "none"
	// TurnKindUser 被中断的是用户轮(Run)。
	TurnKindUser TurnKind = "userTurn"
	// TurnKindAutonomous 被中断的是自主续轮(AutonomousTurn)。
	TurnKindAutonomous TurnKind = "autonomous"
	// TurnKindSubagentActivity 被中断的是后台 subagent 活动轮(SubagentActivity)。
	TurnKindSubagentActivity TurnKind = "subagentActivity"
)

// Aborter is implemented by BackendRunners that support stopping the in-flight
// turn (user clicks "停止"). chat_svc.Stop calls Abort to unblock the runner's
// blocking I/O — claudecode writes a control_request{interrupt} frame, codex
// sends turn/interrupt RPC, builtin cancels turnCtx. Implementations MUST be
// idempotent and MUST be safe to call concurrently with the runner's own
// drain goroutine.
//
// turnToken 是调用方想中断的那一轮的 per-turn token(随 RunResult /
// AutonomousTurn / SubagentActivity 暴露):
//   - 0 = 中断该会话当前活跃的那一轮(等价于旧行为,用户点停止的语义不变);
//   - 非 0 = 仅当该轮仍是当前活跃轮时才中断,否则视为 stale、返回 (AbortOutcome{}, nil)
//     no-op,不触碰任何其它轮 —— 迟到的带 token 中断不会杀掉新起的轮。
//
// 返回 AbortOutcome 携带被中断轮的类型(TurnKindUser / TurnKindAutonomous /
// TurnKindSubagentActivity;无轮可中断或 stale no-op 时 TurnKindNone)。
//
// Returns ErrNoActiveTurn when there is no in-flight turn for sessionID
// (already finished, never started, or unknown to this runner). chat_svc
// translates that to code.ChatStopNoActive.
//
// Defined as a separate interface (like SteerCanceler) so a backend can be
// added without abort support if needed; all three current runners implement it.
type Aborter interface {
	Abort(ctx context.Context, sessionID int64, turnToken uint64) (AbortOutcome, error)
}

// BackgroundTaskStopper is implemented by BackendRunners that support stopping a
// single background task (run_in_background Bash / subagent) by CLI task_id —
// used by the "停止" button on a background-task panel row / AgentSpawn card.
// Distinct from Aborter: a background task outlives the turn that spawned it, so
// implementations MUST NOT require an in-flight turn; they only need the still-
// alive backend session. claudecode writes a control_request{stop_task}. The CLI
// then emits a background task_notification (status canceled/failed) that flips
// the subagent_state block to a terminal state through the existing autonomous-
// turn path; chat_svc additionally flips it to "canceled" on success (idempotent).
//
// Returns ErrNoActiveTurn when the session is unknown to this runner (subprocess
// evicted / never spawned) — chat_svc treats that as "task already gone".
// Implementations MUST be idempotent and safe to call concurrently with the
// runner's own drain goroutine.
//
// Defined as a separate interface (like Aborter) so a backend can be added
// without stop-task support; only claudecode implements it today (codex/builtin
// have no background-task concept), which auto-hides the button via capability.
type BackgroundTaskStopper interface {
	StopBackgroundTask(ctx context.Context, sessionID int64, taskID string) error
}

// PermissionModeSetter 由支持运行时切换 permission mode 的 runner 实现。
// 当前只有 claudecode runner 实现；codex / builtin 不参与权限门概念。
//
// 调用语义：mode 取 {default, acceptEdits, plan, bypassPermissions}，对 sessionID
// 对应的常驻 CLI 子进程下发 control_request{set_permission_mode}。CLI 在两个
// Turn 之间应用，后续 turn 立即生效。
//
// 返回错误：
//   - ErrNoActiveTurn：sessionID 在 runner 的 LRU 缓存里找不到（还没起过 turn
//     或子进程已被 evict）。chat_svc 翻译给上层。
//   - 其它 error：mode 非法 / I/O 失败 / CLI 拒绝。
type PermissionModeSetter interface {
	SetPermissionMode(ctx context.Context, sessionID int64, mode string) error
}

// AutonomousTurnSource 由「会自发产生 turn」的 runtime 实现 —— 当前仅 claudecode:
// CLI 在 run_in_background Bash 任务完成后,自主注入 <task-notification> 并跑完整
// 一轮(列目录之类)。这是唯一的「前向」子接口(backend→host),区别于 Steerer /
// Aborter / ToolPermissionSink 等全是「反向」(host→backend)的控制通道。
//
// chat_svc 每会话订阅一次:AutonomousTurns(sessionID) 返回一个 channel,每当 backend
// 自主起一轮就吐一个 AutonomousTurn。返回的 channel 在该会话子进程退出 / evict 时
// close;sessionID 未知(未 spawn 或已 evict)时返回一个立即 close 的 channel。
//
// 调用约束:同一 sessionID 的 AutonomousTurns 事件流必须与 Run 的事件流**串行
// drain**(consumer 侧保证)—— 二者复用同一个 backend session 的翻译/control 状态,
// 不允许并发。chat_svc 用会话级 turn 锁满足这一点。
type AutonomousTurnSource interface {
	AutonomousTurns(sessionID int64) <-chan AutonomousTurn
}

// AutonomousTurn 是一轮 backend 自发的 turn:事件流 + 异步填充的 RunResult。
// Events 与 Run 的事件流同形;消费方 drain 完 Events(close)后才可读 Result。
type AutonomousTurn struct {
	Events  <-chan Event
	Result  *RunResult
	Trigger string // "background_task"
	// TurnToken 是本自主轮的 per-turn token,供 Abort 精确寻址(决策 1)。
	TurnToken uint64
	// CompletedTask 镜像 claudecode.CompletedBackgroundTask:触发本自主轮的后台命令身份。
	CompletedTask *CompletedBackgroundTask
}

// CompletedBackgroundTask 见 AutonomousTurn.CompletedTask。
type CompletedBackgroundTask struct {
	ToolCallID string
	TaskID     string
	Status     string // "completed" | "failed"; 空 → 视为 completed（见 pkg/claudecode.CompletedBackgroundTask）
	Summary    string // CLI task_notification.summary 透传；空 = CLI 没下发或 remote 路径暂不携带
}

// SubagentActivitySource 由能在轮间(空闲)产生「后台 subagent 内部活动流」的 runtime 实现
// —— 当前仅 claudecode。事件流与 Run 同形,ToolCallID 是发起该 subagent 的 Agent 工具 tool_use_id。
type SubagentActivitySource interface {
	SubagentActivity(sessionID int64) <-chan SubagentActivity
}

// SubagentActivity 镜像 claudecode.SubagentActivity:一轮后台 subagent 内部活动的事件流。
type SubagentActivity struct {
	ToolCallID string
	Events     <-chan Event
	// TurnToken 是本活动轮的 per-turn token,供 Abort 精确寻址(决策 1)。
	TurnToken uint64
}

// Errors live in errors.go; registry / RuntimeFor / RegisterRuntime / SwapRuntimeForTest live in registry.go.
