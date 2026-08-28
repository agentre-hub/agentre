/**
 * 视图块类型词表:chat_svc.ChatBlock 的 type 判别值的全部取值。
 *
 * 本文件由 Go 生成器产出,**不要手改** —— 手改会被下一次重新生成覆盖,
 * 而且 TestGeneratedTSFresh 会立刻变红。
 *
 * 真理源:  internal/service/chat_svc 的 chat_block_type.go
 *          (成员由 AST 反查 ChatBlock{Type: …} 构造点得出,
 *           生成器里没有任何手抄清单)
 * 生成器:  internal/pkg/agentruntime/runtimes/remote/wire/tsgen_test.go
 * 重新生成:
 *
 *   WIRE_TS_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteTSCodec
 *
 * 边界:这是第三份越出 wire 包的产物,而且与前两份**性质不同**,值得先看清:
 *
 * event-kinds / block-types 越界的理由是「它就在 wire 上,而且是那条链路上
 * 唯一有类型意义的东西」。这一份不是 —— ChatBlock 根本不在 agentre ↔ agentred
 * 的 wire 上,它是 backend → 前端这一跳(桌面走 wails binding、web 控制台走
 * HTTP)的视图 DTO。它放在这个包里只有一个理由:这里是本仓 Go → TS 单向生成
 * 唯一的那道缝,而这份词表同样有两个前端在手抄。**别把它读成 wire 协议的一部分。**
 *
 * 与 block-types.gen.ts 不是同一张表(两份都导出,别混用):那份是
 * blocks.StoredBlock.type,持久化 / 跨进程的判别值;这份是 ChatBlock.type,
 * 视图判别值。chat_svc 的投影正是两者之间的翻译 —— 重命名(user_ask →
 * ask_user_question、tool_permission → tool_permission_request)、多对一折叠
 * (nested_tool_use 与 tool_use 都落成 tool_use)、整类丢弃(subagent_state
 * 合进外层 tool_use 块,permission_mode_change 直接 skip)。同名的那几格
 * (text / thinking / plan …)是投影恰好没改名,不是同一个真理。
 *
 * 真理边界画在「Go 发得出什么」:本词表 = chat_svc 的投影能写进 ChatBlock.type
 * 的全部取值,一格不多、一格不少。前端的 TranscriptBlock.type 今天比它多一格
 * "raw" —— 那是 peer-transcript.ts 自产的降级形态(认不出的 peer 事件帧原样
 * JSON 塞进去),Go 从不发它,它的真理本来就该留在产它的那一侧。所以消费方收窄时
 * **不要**直接拿 ChatBlockType 当 TranscriptBlock.type 的类型,而应写成
 * ChatBlockType | <前端自产的那几格>,让每一格的真理留在产它的那一侧。
 *
 * 一个容易踩的坑:"unknown" 在本词表里 —— 它是 **Go 侧**的降级形态(投影认不出
 * 的持久化块,原判别值放在 .raw.kind)。它与前端自产的 "raw" 长得像、所有者不同,
 * 别合并成一格。
 *
 * 格式:生成器直接输出 Prettier(printWidth 80,本仓默认配置)的形态,
 * 与手写代码同一套 ESLint 规则,没有整文件豁免。格式化是产物的一部分 ——
 * 若放到生成之后当外部工序,「重新生成 → 逐字节比对」的守卫会永久误报。
 */

/** ChatBlockTypeText 普通文本段。 */
export const ChatBlockTypeText = "text";

/** ChatBlockTypeThinking 思考过程文本段。 */
export const ChatBlockTypeThinking = "thinking";

/** ChatBlockTypeImage 图片块,载荷在 .Image。 */
export const ChatBlockTypeImage = "image";

/**
 * ChatBlockTypeNotice 系统提示条(供应商回退 / 切换),载荷在 .Level 与
 * .ProviderKey 等字段。
 */
export const ChatBlockTypeNotice = "notice";

/**
 * ChatBlockTypeToolUse 工具调用卡。内层(subagent 里的)嵌套调用也折叠成它,
 * 靠 .ParentToolCallID 区分。
 */
export const ChatBlockTypeToolUse = "tool_use";

/** ChatBlockTypeToolResult 工具结果。同样折叠了内层嵌套结果。 */
export const ChatBlockTypeToolResult = "tool_result";

/** ChatBlockTypeCompactBoundary 上下文压缩分隔卡,载荷在 .Compact。 */
export const ChatBlockTypeCompactBoundary = "compact_boundary";

/**
 * ChatBlockTypeAskUserQuestion 交互提问卡,载荷在 .AskUserQuestion。
 * 注册表那边叫 user_ask —— 投影在这里改了名。
 */
export const ChatBlockTypeAskUserQuestion = "ask_user_question";

/**
 * ChatBlockTypeToolPermissionRequest 工具审批卡,载荷在 .ToolPermission。
 * 注册表那边叫 tool_permission —— 投影在这里改了名。
 */
export const ChatBlockTypeToolPermissionRequest = "tool_permission_request";

/** ChatBlockTypeExecApproval OpenClaw Gateway 的 exec 审批卡,载荷在 .ExecApproval。 */
export const ChatBlockTypeExecApproval = "exec_approval";

/**
 * ChatBlockTypeToolApproval agent 内置工具(org / hook 等)写操作审批卡,
 * 载荷在 .ToolApproval。
 */
export const ChatBlockTypeToolApproval = "tool_approval";

/** ChatBlockTypePlan 计划卡,文本在 .Text、结构化步骤在 .Canonical。 */
export const ChatBlockTypePlan = "plan";

/**
 * ChatBlockTypeUnknown 兜底:投影认不出的持久化块类型,原判别值放在
 * .Raw["kind"] 里带给前端。**Go 侧的降级形态**,与前端自产的降级形态
 * (peer-transcript 的 raw)分属两个所有者,别混。
 */
export const ChatBlockTypeUnknown = "unknown";

/**
 * 全部视图块类型判别值的联合类型(= Go 的 chat_svc.ChatBlock.type 取值域)。
 *
 * 消费方把手上的 type 收窄成这个类型之后,在 switch 的 default 分支写一句
 * const _: never = type,「Go 的投影新增了一个视图块类型」就成了消费方的编译期
 * 错误。今天后端加一格,桌面端与 web 控制台的 switch 都不会有任何信号。
 *
 * 注意它**不等于**前端的 TranscriptBlock.type:后者还多一格前端自产的 "raw"
 * (peer-transcript.ts)。收窄时写 ChatBlockType | typeof … 组合,别直接替换。
 */
export type ChatBlockType =
  | typeof ChatBlockTypeText
  | typeof ChatBlockTypeThinking
  | typeof ChatBlockTypeImage
  | typeof ChatBlockTypeNotice
  | typeof ChatBlockTypeToolUse
  | typeof ChatBlockTypeToolResult
  | typeof ChatBlockTypeCompactBoundary
  | typeof ChatBlockTypeAskUserQuestion
  | typeof ChatBlockTypeToolPermissionRequest
  | typeof ChatBlockTypeExecApproval
  | typeof ChatBlockTypeToolApproval
  | typeof ChatBlockTypePlan
  | typeof ChatBlockTypeUnknown;
