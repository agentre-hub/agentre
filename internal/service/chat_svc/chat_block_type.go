package chat_svc

// 视图词表:ChatBlock.Type 的全部取值。
//
// **它与 blocks.StoredBlock.Type(注册表词表)不是同一张表。** 两者此前一直被
// 混为一谈,实际当场就对不上:
//
//   - 注册表词表是持久化 / 跨进程往返的判别值,由块注册表拥有,生成产物是
//     @agentre-hub/agentre-wire 的 block-types.gen.ts。
//   - 视图词表是 backend → 前端这一跳的判别值,由本文件拥有。toChatMessage
//     的投影正是两者之间的翻译:重命名(user_ask → ask_user_question、
//     tool_permission → tool_permission_request)、多对一折叠(nested_tool_use
//     与 tool_use 都落成 tool_use)、以及整类丢弃(subagent_state 合进外层
//     tool_use 块的 .Subagent,permission_mode_change 直接 skip)。
//
// 两张表同名的那几格(text / thinking / plan / exec_approval …)是投影恰好没改名,
// 不是同一个真理 —— 改其中一张不会、也不该牵动另一张。
//
// 为什么提成具名常量:这些值是 backend 与两个前端(桌面 frontend/src、
// agentre-server 的 web 控制台)之间的契约,此前散在投影各处当裸字面量,没有任何
// 东西能回答「Go 这边到底发得出哪些 type」。生成器
// (internal/pkg/agentruntime/runtimes/remote/wire/tsgen_test.go)据此单向生成
// chat-block-types.gen.ts,并由 TestTSGenCoversChatBlockTypes 钉住「每个
// ChatBlock{Type: …} 构造点都引用本表的常量,而不是裸字面量」—— 新增一个视图
// 块类型不再可能悄无声息。
//
// 常量刻意是**无类型**字符串常量:ChatBlock.Type 的 Go 类型必须保持 string,
// 换成具名类型会连带改掉 wails 生成的 frontend/wailsjs/go/models.ts。
const (
	// ChatBlockTypeText 普通文本段。
	ChatBlockTypeText = "text"
	// ChatBlockTypeThinking 思考过程文本段。
	ChatBlockTypeThinking = "thinking"
	// ChatBlockTypeImage 图片块,载荷在 .Image。
	ChatBlockTypeImage = "image"
	// ChatBlockTypeNotice 系统提示条(供应商回退 / 切换),载荷在 .Level 与
	// .ProviderKey 等字段。
	ChatBlockTypeNotice = "notice"
	// ChatBlockTypeToolUse 工具调用卡。内层(subagent 里的)嵌套调用也折叠成它,
	// 靠 .ParentToolCallID 区分。
	ChatBlockTypeToolUse = "tool_use"
	// ChatBlockTypeToolResult 工具结果。同样折叠了内层嵌套结果。
	ChatBlockTypeToolResult = "tool_result"
	// ChatBlockTypeCompactBoundary 上下文压缩分隔卡,载荷在 .Compact。
	ChatBlockTypeCompactBoundary = "compact_boundary"
	// ChatBlockTypeAskUserQuestion 交互提问卡,载荷在 .AskUserQuestion。
	// 注册表那边叫 user_ask —— 投影在这里改了名。
	ChatBlockTypeAskUserQuestion = "ask_user_question"
	// ChatBlockTypeToolPermissionRequest 工具审批卡,载荷在 .ToolPermission。
	// 注册表那边叫 tool_permission —— 投影在这里改了名。
	ChatBlockTypeToolPermissionRequest = "tool_permission_request"
	// ChatBlockTypeExecApproval OpenClaw Gateway 的 exec 审批卡,载荷在 .ExecApproval。
	ChatBlockTypeExecApproval = "exec_approval"
	// ChatBlockTypeToolApproval agent 内置工具(org / hook 等)写操作审批卡,
	// 载荷在 .ToolApproval。
	ChatBlockTypeToolApproval = "tool_approval"
	// ChatBlockTypePlan 计划卡,文本在 .Text、结构化步骤在 .Canonical。
	ChatBlockTypePlan = "plan"
	// ChatBlockTypeUnknown 兜底:投影认不出的持久化块类型,原判别值放在
	// .Raw["kind"] 里带给前端。**Go 侧的降级形态**,与前端自产的降级形态
	// (peer-transcript 的 raw)分属两个所有者,别混。
	ChatBlockTypeUnknown = "unknown"
)
