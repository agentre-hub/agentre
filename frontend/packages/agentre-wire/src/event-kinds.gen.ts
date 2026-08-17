/**
 * agentruntime.EventKind 词表:EventFrame.event 载荷里 kind 判别值的全部取值。
 *
 * 本文件由 Go 生成器产出,**不要手改** —— 手改会被下一次重新生成覆盖,
 * 而且 TestGeneratedTSFresh 会立刻变红。
 *
 * 真理源:  internal/pkg/agentruntime 的 EventKind 常量
 *          (runner.go 是主表,event_wire.go 另有补登的 discriminator)
 * 生成器:  internal/pkg/agentruntime/runtimes/remote/wire/tsgen_test.go
 * 重新生成:
 *
 *   WIRE_TS_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteTSCodec
 *
 * 边界例外:codec / constants 两份产物守的规矩是「wire 包之外的类型一律
 * unknown」,这一份与 block-types.gen.ts 是仅有的两处刻意例外
 * (chat-block-types.gen.ts 不在此列 —— 它讲的根本不是 wire)。理由:
 *
 * EventFrame.event 在 Go 侧是 json.RawMessage —— 载荷对 wire 完全不透明,
 * 生成器对它只能给出 unknown。整条事件流里唯一有类型意义的东西就是这个
 * kind 判别值,而它恰恰是 agentre ↔ agentred 的契约本身;把词表留在生成
 * 范围之外,等于契约里唯一可校验的那一格没有任何机械保证。agentruntime
 * 又是 wire 包的直接依赖(wire.go 已在用它的 TurnKind / MCPServerSpec),
 * 追它不打穿分层。
 *
 * 例外到此为止:这不是「凡是 agentruntime 的东西都生成」的先例。
 *
 * 格式:生成器直接输出 Prettier(printWidth 80,本仓默认配置)的形态,
 * 与手写代码同一套 ESLint 规则,没有整文件豁免。格式化是产物的一部分 ——
 * 若放到生成之后当外部工序,「重新生成 → 逐字节比对」的守卫会永久误报。
 */

export const EventTextDelta = "text_delta";

export const EventThinkingDelta = "thinking_delta";

export const EventToolUseStart = "tool_use_start";

export const EventToolUseEnd = "tool_use_end";

export const EventToolResult = "tool_result";

export const EventSteerConsumed = "steer_consumed";

/**
 * subagent 生命周期（claudecode 与 Pi runtime 产生；codex 不发）。
 * Pi 的 stateful drain 在这里承载 parallel/chain 的 mode + runs 全量快照；
 * claudecode 的 legacy 单运行生产者可继续不填 Runs。
 */
export const EventSubagentStarted = "subagent_started";

export const EventSubagentProgress = "subagent_progress";

export const EventSubagentDone = "subagent_done";

/**
 * EventSubagentModel claudecode 从 subagent 内部帧解析出的实际模型（R2：实际执行
 * 覆盖调用意图）。独立事件类型，刻意不复用 EventSubagentProgress ——
 * SubagentProgressHandler 对 ToolUses/TotalTokens/LastToolName 是无条件赋值，混进
 * 一个只带模型的 SubagentInfo 会把已累计的进度清零（R4）。chat_svc 把它并入对应
 * 派遣的 subagent 累计态，只更新模型字段。first-wins（R3，同一子代理只认第一次）
 * 由 chat_svc 累计态负责；claudecode 侧每次遇到都如实产出，不做去重。
 */
export const EventSubagentModel = "subagent_model";

/**
 * EventAskUserQuestion backend 检测到 ask_user_question 类型的工具调用
 * 时 emit（Claude Code 的内置 AskUserQuestion、Codex / 内置 Agent
 * 后续注册的同语义 function tool 都翻译到这里）。service 接住后 push
 * 给前端渲染卡片；用户答完后通过 AskAnswerSink 反向投回给具体 backend，
 * 由 backend 各自实现"同 turn 注入答案"的协议细节。
 */
export const EventAskUserQuestion = "ask_user_question";

/**
 * EventAskUserQuestionAnswered backend 完成 SubmitAnswer 反向投回后 emit。
 * 用于把 Answered/Skipped/Answers 状态写回 acc 里那条 AskUserQuestionBlock,
 * 这样 turn finalize 时 SetBlocks 落盘的 chat_messages.blocks 就是终态;
 * service 同时 forward 一条 StreamAskUserQuestion 给前端做兜底 merge。
 */
export const EventAskUserQuestionAnswered = "ask_user_question_answered";

/**
 * EventPlanUpdated carries Codex collaboration plan updates. It is separate
 * from EventToolUseStart because the UI needs a visible assistant plan even
 * when Codex does not emit normal text in plan mode.
 */
export const EventPlanUpdated = "plan_updated";

/**
 * EventToolPermissionRequest backend 收到 can_use_tool（除 AskUserQuestion 以外）
 * 时 emit。service 推前端渲染审批卡，用户决定 allow/deny 后通过
 * ToolPermissionSink 反向投回 backend。前端可以读 Input 自己 pretty-print。
 */
export const EventToolPermissionRequest = "tool_permission_request";

/**
 * EventToolPermissionResolved backend 完成 SubmitToolPermission 反向投回后 emit。
 * service 把终态 patch 回 acc 里那条 ToolPermissionRequestBlock，并 forward
 * 一条 StreamToolPermissionResolved 给前端做兜底 merge。
 */
export const EventToolPermissionResolved = "tool_permission_resolved";

/**
 * EventExecApprovalRequested / Resolved model the OpenClaw Gateway exec
 * approval lifecycle. Approval resolution is not tool execution completion.
 */
export const EventExecApprovalRequested = "exec_approval_requested";

export const EventExecApprovalResolved = "exec_approval_resolved";

/**
 * EventPermissionModeChanged claudecode CLI 通报自身 permission mode 已变更
 * （被动 ExitPlanMode 流程 / 主动 set_permission_mode 回执）。
 * chat_svc 接住后落 chat_sessions.permission_mode 并推 StreamSessionStatus patch。
 */
export const EventPermissionModeChanged = "permission_mode_changed";

export const EventRetry = "retry";

/**
 * EventUsage backend 在 turn 中途上报「本次 API call 之后模型当前看到的输入大小」。
 * claudecode 每个主 agent assistant 帧、codex 的 token_count notification 各产一条。
 * chat_svc 接住后更新 assistantMsg 的 token 列 + 落库 + emit StreamUsage 给前端，
 * 让 Composer 进度条在 turn 内随工具循环阶梯式刷新。Usage 字段必填，其它字段留空。
 */
export const EventUsage = "usage";

export const EventCompactBoundary = "compact_boundary";

/**
 * EventRuntimeStatus backend 通报会话级运行状态字符串 (compacting / requesting ...)。
 * 与 EventPermissionModeChanged 同源帧但承载独立信号 —— chat_svc 推一条 stream
 * event,前端在 Composer / typing indicator 处显示 "正在压缩上下文…" 等过渡 chip。
 */
export const EventRuntimeStatus = "runtime_status";

export const EventError = "error";

export const EventDone = "done";

/**
 * EventUserMessage (R18):daemon 在「开新一轮」事件流开头注入的发起方标记(见
 * event.go 的 UserMessageEvent)。它是 wire 事件流的一部分,走既有的
 * runtime.event 通知 / journal / 补齐,不需要额外的通知通道。
 */
export const EventUserMessage = "user_message";

/**
 * EventContextWindowUpdated 给 ContextWindowUpdated 事件做 wire discriminator。
 * 老 RuntimeEvent 时 ContextWindow 走 RunResult,因此 runner.go 的 EventKind 列表里
 * 没这个常量,这里补上。
 */
export const EventContextWindowUpdated = "context_window_updated";

/**
 * 全部 kind 的联合类型(= Go 的 agentruntime.EventKind)。
 *
 * 消费方把手上的 kind 收窄成这个类型之后,在 switch 的 default 分支写一句
 * const _: never = kind,「Go 新增了一个 kind」就成了消费方的编译期错误。载荷
 * 本身是 json.RawMessage、无从校验,kind 是这条链路上唯一能被类型系统接住的东西。
 */
export type EventKind =
  | typeof EventTextDelta
  | typeof EventThinkingDelta
  | typeof EventToolUseStart
  | typeof EventToolUseEnd
  | typeof EventToolResult
  | typeof EventSteerConsumed
  | typeof EventSubagentStarted
  | typeof EventSubagentProgress
  | typeof EventSubagentDone
  | typeof EventSubagentModel
  | typeof EventAskUserQuestion
  | typeof EventAskUserQuestionAnswered
  | typeof EventPlanUpdated
  | typeof EventToolPermissionRequest
  | typeof EventToolPermissionResolved
  | typeof EventExecApprovalRequested
  | typeof EventExecApprovalResolved
  | typeof EventPermissionModeChanged
  | typeof EventRetry
  | typeof EventUsage
  | typeof EventCompactBoundary
  | typeof EventRuntimeStatus
  | typeof EventError
  | typeof EventDone
  | typeof EventUserMessage
  | typeof EventContextWindowUpdated;
