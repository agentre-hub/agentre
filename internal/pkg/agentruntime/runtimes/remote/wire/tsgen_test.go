// tsgen_test.go 把 wire.go 生成成浏览器侧的 TypeScript 编解码。
//
// 为什么要生成:wire 协议此前有两份实现 —— Go 侧的 wire.go 与浏览器侧一份逐字段
// 手抄的 wire.ts。手抄本没有任何机械保证覆盖了 Go 会发出的全部字段:codec 刻意
// 保留未知字段(为兼容老版本 agentred,设计本身正确),所以 Go 新增字段后往返测试
// 照样绿,新字段被当未知键原样带过,在 TS 侧静默消失。让 wire.go 成为唯一真理、
// TS 侧单向生成,手写副本归零,就没有东西可漂。
//
// 为什么用反射而不是 AST 类型解析:JSON 字段名、omitempty、指针可选性的实际行为
// 由 encoding/json 决定,反射拿到的就是运行时真实语义;而 reflect.Type.PkgPath()
// 正好给出「只追 wire 包内的类型」这条边界判定。AST 只用来取两样反射拿不到的东西:
// 文档注释,以及「本包声明了哪些导出结构 / 常量」(完整性守卫的依据)。
//
// 几个测试各司其职(与 golden_test.go 同一套形状):
//
//   - TestTSGenCoversWirePackage —— 完整性守卫:AST 扫出的导出结构 / 常量集合必须
//     与生成器的清单一致。新增一个 wire 结构却忘了登记,这里变红。
//   - TestTSGenCoversEventKinds —— 同上,守 agentruntime 的 EventKind 词表。
//   - TestTSGenCoversBlockTypes —— 守块类型词表。这一条与上面两条**机制不同**:
//     块类型是运行时注册表,不是编译期常量表,理由见该测试的注释。
//   - TestTSGenCoversChatBlockTypes —— 守**视图**块类型词表(chat_svc.ChatBlock.Type)。
//     它与上面那张块类型表**不是同一张**:一个是持久化判别值,一个是 backend → 前端
//     的视图判别值,投影在两者之间改名 / 折叠 / 丢弃。机制第三种:词表在源码里,
//     但生成器 import 不进去(import cycle),只能 AST 读,理由见该测试的注释。
//   - TestGeneratedTSFresh —— 新鲜度守卫,总是运行:把产物生成到临时目录,与包里
//     已提交的 *.gen.ts 比文件集 + 逐字节内容。改了 wire 结构却忘了重新生成,这里变红。
//   - TestWriteTSCodec —— 重新生成这个动作本身,带 WIRE_TS_WRITE=1 才执行,
//     让 `go test ./...` 不写盘。
//
// 重新生成的命令:
//
//	WIRE_TS_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteTSCodec
//
// 格式化在生成器内部完成:产物直接就是 Prettier(printWidth 80,本仓默认配置)的
// 形态。这不是洁癖 —— 新鲜度守卫比的是「重新生成的字节 vs 已提交的字节」,格式化
// 若是生成之后的一道外部工序,守卫就会因为格式差异永久误报红。
package wire

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	// 块类型词表是**运行时**注册表:判别值只有在注册包的 init() 跑过之后才存在。
	// 这个 blank import 就是把本仓的注册包链接进测试二进制、让 init() 真的执行 ——
	// 生成器随后问 blocks.RegisteredTypes() 拿到的才是完整答案。理由与代价见
	// blockTypeVocabulary 与 TestTSGenCoversBlockTypes。
	_ "github.com/agentre-ai/agentre/internal/service/chat_svc/blocks"
)

// tsGenRel 生成产物在本仓里的位置(相对仓库根)—— @agentre-ai/agentre-wire 的 src/。
const tsGenRel = "frontend/packages/agentre-wire/src"

// agentruntimeRel 是 agentruntime 包在本仓里的位置(相对仓库根)。
// 只为 EventKind 词表而定 —— 破例的理由见 tsEventKindDecls。
const agentruntimeRel = "internal/pkg/agentruntime"

// tsRegenCmd 产物过期时重新生成的确切命令,原样出现在守卫的失败信息里。
const tsRegenCmd = "WIRE_TS_WRITE=1 go test ./internal/pkg/agentruntime/runtimes/remote/wire/ -run TestWriteTSCodec"

// tsPrintWidth 是 Prettier 的 printWidth 默认值。本仓没有 .prettierrc,
// eslint-plugin-prettier 因此按 Prettier 默认配置校验格式。
const tsPrintWidth = 80

// tsRuntimeModule 手写运行时的模块路径(生成产物从它取校验助手)。
const tsRuntimeModule = "./runtime"

// ── 生成器清单 ──────────────────────────────────────────────────────────────

// tsRootTypes 是要生成的全部 wire 帧类型,按 wire.go 里的声明序排列 ——
// 产物因此能与 wire.go 并排对读。清单的完整性由 TestTSGenCoversWirePackage
// 机械保证,不靠人记得回来加一行。
func tsRootTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(ModelSummary{}),
		reflect.TypeOf(ProviderSummary{}),
		reflect.TypeOf(OK{}),
		reflect.TypeOf(PeerSessionControlResult{}),
		reflect.TypeOf(GoalParams{}),
		reflect.TypeOf(GoalResult{}),
		reflect.TypeOf(GoalClearResult{}),
		reflect.TypeOf(CapabilitiesParams{}),
		reflect.TypeOf(CapabilitiesResult{}),
		reflect.TypeOf(HistoryMessageWire{}),
		reflect.TypeOf(RunParams{}),
		reflect.TypeOf(MCPProxyRequest{}),
		reflect.TypeOf(MCPProxyResponse{}),
		reflect.TypeOf(RunAck{}),
		reflect.TypeOf(SteerParams{}),
		reflect.TypeOf(CancelSteerParams{}),
		reflect.TypeOf(CancelSteerResult{}),
		reflect.TypeOf(DrainParams{}),
		reflect.TypeOf(DrainResult{}),
		reflect.TypeOf(AbortParams{}),
		reflect.TypeOf(AbortResult{}),
		reflect.TypeOf(StopBackgroundTaskParams{}),
		reflect.TypeOf(SetPermissionModeParams{}),
		reflect.TypeOf(SubmitAnswerParams{}),
		reflect.TypeOf(SubmitToolPermissionParams{}),
		reflect.TypeOf(SessionSummary{}),
		reflect.TypeOf(SessionListResult{}),
		reflect.TypeOf(SessionPullParams{}),
		reflect.TypeOf(JournaledNotification{}),
		reflect.TypeOf(SessionPullResult{}),
		reflect.TypeOf(SessionPendingWaitersParams{}),
		reflect.TypeOf(SessionPendingWaitersResult{}),
		reflect.TypeOf(SessionAttachParams{}),
		reflect.TypeOf(SessionAttachResult{}),
		reflect.TypeOf(SessionDeleteParams{}),
		reflect.TypeOf(SessionDeleteResult{}),
		reflect.TypeOf(SkillAuthorization{}),
		reflect.TypeOf(SkillCatalogParams{}),
		reflect.TypeOf(SkillPackSummary{}),
		reflect.TypeOf(SkillCatalogResult{}),
		reflect.TypeOf(EventFrame{}),
		reflect.TypeOf(RunResultDoneFrame{}),
		reflect.TypeOf(AutonomousTurnStartedFrame{}),
		reflect.TypeOf(UsageWire{}),
	}
}

// tsConstDecl 一条要导出到 TS 的常量:名字 + 直接引用的 Go 常量值。
// 值由 Go 常量本身提供,所以改 Go 的值就会改产物,新鲜度守卫据此变红。
type tsConstDecl struct {
	name  string
	value any
}

// tsConstDecls 是要生成的全部 wire 常量。完整性同样由 TestTSGenCoversWirePackage
// 机械保证。
func tsConstDecls() []tsConstDecl {
	return []tsConstDecl{
		{"MethodCapabilities", MethodCapabilities},
		{"MethodRun", MethodRun},
		{"MethodSteer", MethodSteer},
		{"MethodCancelSteer", MethodCancelSteer},
		{"MethodDrainPending", MethodDrainPending},
		{"MethodAbort", MethodAbort},
		{"MethodStopBackgroundTask", MethodStopBackgroundTask},
		{"MethodSetPermissionMode", MethodSetPermissionMode},
		{"MethodSubmitAnswer", MethodSubmitAnswer},
		{"MethodSubmitToolPermission", MethodSubmitToolPermission},
		{"MethodGetGoal", MethodGetGoal},
		{"MethodSetGoal", MethodSetGoal},
		{"MethodClearGoal", MethodClearGoal},
		{"MethodSessionList", MethodSessionList},
		{"MethodSessionPull", MethodSessionPull},
		{"MethodSessionPendingWaiters", MethodSessionPendingWaiters},
		{"MethodSessionAttach", MethodSessionAttach},
		{"MethodSessionDelete", MethodSessionDelete},
		{"MethodSkillsCatalog", MethodSkillsCatalog},
		{"NotifyEvent", NotifyEvent},
		{"NotifyRunResultDone", NotifyRunResultDone},
		{"MethodMCPProxy", MethodMCPProxy},
		{"NotifyAutonomousTurnStarted", NotifyAutonomousTurnStarted},
		{"NotifyAutonomousTurnEvent", NotifyAutonomousTurnEvent},
		{"NotifyAutonomousTurnDone", NotifyAutonomousTurnDone},
		{"ErrCodeNoActiveTurn", ErrCodeNoActiveTurn},
		{"ErrCodeSteerNotFound", ErrCodeSteerNotFound},
		{"ErrCodeUnsupported", ErrCodeUnsupported},
		{"ErrCodeAborted", ErrCodeAborted},
		{"ErrCodeSessionNotFound", ErrCodeSessionNotFound},
		{"ErrCodePeerExecutionUnavailable", ErrCodePeerExecutionUnavailable},
		{"CapLLMModelTargetV1", CapLLMModelTargetV1},
		{"SessionLifecycleRunning", SessionLifecycleRunning},
		{"SessionLifecycleIdle", SessionLifecycleIdle},
		{"SessionLifecycleInterrupted", SessionLifecycleInterrupted},
		{"DefaultSessionPullLimit", DefaultSessionPullLimit},
		{"MaxSessionPullLimit", MaxSessionPullLimit},
		{"SkillDiscoveryOK", SkillDiscoveryOK},
		{"SkillDiscoveryUnavailable", SkillDiscoveryUnavailable},
		{"SkillDiscoveryUnsupported", SkillDiscoveryUnsupported},
	}
}

// eventKindTypeName 是词表常量在 agentruntime 里的类型名 —— AST 据此把 EventKind
// 常量从该包的其它常量里筛出来。
const eventKindTypeName = "EventKind"

// tsEventKindDecl 一条 EventKind 词表项。value 的类型就是 agentruntime.EventKind,
// 所以往清单里塞一个不是 EventKind 的东西直接编译不过。
type tsEventKindDecl struct {
	name  string
	value agentruntime.EventKind
}

// tsEventKindDecls 是要导出到 TS 的 agentruntime.EventKind 词表。
//
// **这是「只追 wire 包内类型」那条边界唯一的、刻意的例外。** 破例的理由要成立到
// 这个程度才配:
//
//   - EventFrame.Event 在 Go 侧是 json.RawMessage —— 载荷对 wire 完全不透明,
//     生成器对它只能给出 unknown。整条事件流里**唯一有类型意义的东西就是那个
//     kind 判别值**,而它恰恰是 agentre ↔ agentred 之间的契约本身。词表留在包外,
//     等于契约里唯一可校验的那一格没有任何机械保证。
//   - agentruntime 是 wire 包的**直接依赖**(wire.go 已经在用它的 TurnKind /
//     MCPServerSpec / Goal),不是第三方模块,追它不打穿分层。
//   - EventKind 是一张编译期常量表,AST 能完整枚举 —— 这是完整性守卫
//     (TestTSGenCoversEventKinds)成立的前提。换成运行时注册表填充的词表
//     (如 blocks.StoredBlock.Type)就没有这个前提,不能照搬这套机制。
//
// 例外**到此为止**:这不是「凡是 agentruntime 的东西都生成」的先例。别的
// agentruntime 类型仍然一律 unknown —— 它们大多没有 JSON tag,追进去等于凭空
// 发明一份新契约(理由见 isWireStruct)。
//
// 清单的完整性由 TestTSGenCoversEventKinds 机械保证,不靠人记得回来加一行。
// 排列顺序跟着 runner.go 的声明序走(产物因此能与 runner.go 并排对读),
// event_wire.go 补登的那条排在最后。
func tsEventKindDecls() []tsEventKindDecl {
	return []tsEventKindDecl{
		{"EventTextDelta", agentruntime.EventTextDelta},
		{"EventThinkingDelta", agentruntime.EventThinkingDelta},
		{"EventToolUseStart", agentruntime.EventToolUseStart},
		{"EventToolUseEnd", agentruntime.EventToolUseEnd},
		{"EventToolResult", agentruntime.EventToolResult},
		{"EventSteerConsumed", agentruntime.EventSteerConsumed},
		{"EventSubagentStarted", agentruntime.EventSubagentStarted},
		{"EventSubagentProgress", agentruntime.EventSubagentProgress},
		{"EventSubagentDone", agentruntime.EventSubagentDone},
		{"EventSubagentModel", agentruntime.EventSubagentModel},
		{"EventAskUserQuestion", agentruntime.EventAskUserQuestion},
		{"EventAskUserQuestionAnswered", agentruntime.EventAskUserQuestionAnswered},
		{"EventPlanUpdated", agentruntime.EventPlanUpdated},
		{"EventToolPermissionRequest", agentruntime.EventToolPermissionRequest},
		{"EventToolPermissionResolved", agentruntime.EventToolPermissionResolved},
		{"EventExecApprovalRequested", agentruntime.EventExecApprovalRequested},
		{"EventExecApprovalResolved", agentruntime.EventExecApprovalResolved},
		{"EventPermissionModeChanged", agentruntime.EventPermissionModeChanged},
		{"EventRetry", agentruntime.EventRetry},
		{"EventUsage", agentruntime.EventUsage},
		{"EventCompactBoundary", agentruntime.EventCompactBoundary},
		{"EventRuntimeStatus", agentruntime.EventRuntimeStatus},
		{"EventError", agentruntime.EventError},
		{"EventDone", agentruntime.EventDone},
		{"EventUserMessage", agentruntime.EventUserMessage},
		{"EventContextWindowUpdated", agentruntime.EventContextWindowUpdated},
	}
}

// ── 块类型词表清单 ─────────────────────────────────────────────────────────

// blocksPkgPath 是块注册表所在的包。既是运行时枚举的入口,也是 AST 扫描认出
// 「这个文件里有没有注册点」的依据。
const blocksPkgPath = "github.com/cago-frame/agents/agent/blocks"

// blockTypeCycleBound 是本仓声明、但运行时枚举**够不着**的块类型判别值。
//
// 目前只有一条:chat_svc.PlanBlock 的 "plan"。它注册在 chat_svc 包本身(而不是
// chat_svc/blocks 子包),而 chat_svc 传递依赖 wire 包,所以本包的包内测试
// blank import 它会直接 import cycle —— 换句话说,这个 init() 在本测试二进制里
// **永远不可能**跑起来。词表里那一格因此由 AST 扫描(scanBlockTypeRegistrations)
// 补齐,而不是靠人记得手抄。
//
// 这不是一张豁免清单,而是一条被钉住的事实:TestTSGenCoversBlockTypes 断言
// 「AST 扫到的减去运行时枚举到的」正好等于它。再有一个块类型落进够不着的包,
// 或者 PlanBlock 挪进 chat_svc/blocks 之后这一格空了,守卫都会变红,逼人回来
// 更新这段话。
var blockTypeCycleBound = []string{"plan"}

// blockTypeVocabulary 求块类型判别值的完整词表(去重 + 字典序)。
//
// 两个来源合起来才完整,少哪个都留洞:
//
//   - blocks.RegisteredTypes() —— 运行时注册表。唯一能看见**第三方**词表
//     (cago 的 text / image / tool_use …)的途径:那些 init() 在依赖模块里,
//     本仓的 AST 扫不到。
//   - scanBlockTypeRegistrations() —— AST 扫本仓的注册点。唯一能看见
//     **没链接进来**的本仓块类型的途径(见 blockTypeCycleBound)。
//
// 词表本身不再手抄一份清单 —— 这与 tsRootTypes / tsConstDecls / tsEventKindDecls
// 刻意不同。那三份的值必须由 Go 侧的具名标识符提供(TS 常量名要跟着 Go 名走),
// 手抄清单加上 AST 完整性守卫是当时唯一的形态;而块类型的 TS 常量名是从判别值
// 本身推出来的(tsBlockTypeConstName),清单里没有任何一格需要人来填,凡是能被
// 手抄错的东西都不存在。
func blockTypeVocabulary(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	for _, v := range blocks.RegisteredTypes() {
		seen[v] = true
	}
	for _, v := range scanBlockTypeRegistrations(t) {
		seen[v] = true
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		require.Regexp(t, `^[a-z][a-z0-9]*(_[a-z0-9]+)*$`, v,
			"块类型判别值 %q 不是 lower_snake_case,tsBlockTypeConstName 推不出合法的 TS 标识符", v)
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// tsBlockTypeConstName 把判别值推成 TS 常量名:tool_use → BlockTypeToolUse。
// 加 BlockType 前缀是因为产物与 event-kinds.gen.ts 一起从包根导出,
// 裸的 ToolUse / Text 撞名的概率太高。
func tsBlockTypeConstName(v string) string {
	var b strings.Builder
	b.WriteString("BlockType")
	for _, part := range strings.Split(v, "_") {
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// ── 视图块类型词表清单 ─────────────────────────────────────────────────────

// chatSvcRel 是 chat_svc 包在本仓里的位置(相对仓库根)。视图词表的真理源。
const chatSvcRel = "internal/service/chat_svc"

// chatBlockTypeName 是视图 DTO 的结构名 —— AST 据此认出「往 ChatBlock.Type
// 里写值」的构造点。
const chatBlockTypeName = "ChatBlock"

// chatBlockTypeField 是视图 DTO 上承载判别值的字段名。
const chatBlockTypeField = "Type"

// chatBlockTypeAnchors 是词表里必然存在的几格。扫描器本身坏掉(比如
// ChatBlock 改了名、构造点换了形态)时会扫出空集或残集,产物随之静默缩水;
// 有了这条锚,那种情况直接变红而不是悄悄过。
var chatBlockTypeAnchors = []string{"text", "tool_use", "tool_result", "unknown"}

// chatBlockTypeDecl 一条视图词表项:Go 常量名 + 值 + 文档注释。
// 三样都由 AST 从 chat_svc 源码里读出,生成器里没有任何一格需要人填。
type chatBlockTypeDecl struct {
	name  string
	value string
	doc   string
}

// chatBlockTypeVocabulary 求视图词表 —— ChatBlock.Type 的全部取值。
//
// **为什么只能走 AST,不能像 tsConstDecls 那样直接引用 Go 常量:** 本文件是
// wire 包的包内测试,而 chat_svc 传递依赖 wire 包 —— import 它直接是 import
// cycle。这与 blockTypeCycleBound 记的是同一条约束,只是那里够不着一格,
// 这里够不着整张表。
//
// 也因此这份词表与 tsRootTypes / tsConstDecls / tsEventKindDecls 形态不同:
// 那三份在生成器里各有一张手抄清单(靠 AST 完整性守卫兜底),这一份连清单
// 都没有 —— 成员由「哪些常量真的被写进了 ChatBlock.Type」反查得出,凡是能
// 被手抄错的东西都不存在。词表与构造点是否自洽由 TestTSGenCoversChatBlockTypes
// 机械保证。
//
// 排列顺序跟着 chat_block_type.go 里 const 块的声明序走,产物因此能与它并排对读。
func chatBlockTypeVocabulary(t *testing.T) []chatBlockTypeDecl {
	t.Helper()
	consts, groups := parseChatBlockTypeConsts(t)
	used := scanChatBlockTypeUses(t)

	group := ""
	for _, name := range used {
		owner, ok := consts[name]
		require.True(t, ok,
			"%s 里有构造点把 %s 写进 %s.%s,但它不是本包里一个 `Name = \"字面量\"` 形态的常量,"+
				"生成器取不到值", chatSvcRel, name, chatBlockTypeName, chatBlockTypeField)
		if group == "" {
			group = owner
			continue
		}
		require.Equal(t, group, owner,
			"视图词表被拆到了多个 const 块(%s 与 %s)。整张表必须留在一个块里,"+
				"否则「这张表有哪些格」就没有一个可读的答案", group, owner)
	}
	require.NotEmpty(t, group, "没在 %s 扫到任何 ChatBlock{%s: …} 构造点,扫描逻辑本身坏了",
		chatSvcRel, chatBlockTypeField)
	return groups[group]
}

// ── Prettier 形态的行渲染 ───────────────────────────────────────────────────

// tsWidth 按 Prettier 的字符宽度算行宽:东亚宽字符占 2 列(生成的错误信息里
// 有中文,按 1 列算会算漏、按 Prettier 的规则才对得上)。
func tsWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// isWideRune 覆盖 Unicode East Asian Wide / Fullwidth 的常用区段。
func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0x303E, // CJK Radicals … CJK Symbols
		r >= 0x3041 && r <= 0x33FF, // Hiragana … CJK Compatibility
		r >= 0x3400 && r <= 0x4DBF, // CJK Ext A
		r >= 0x4E00 && r <= 0x9FFF, // CJK Unified
		r >= 0xA000 && r <= 0xA4CF, // Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul Syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK Compatibility Ideographs
		r >= 0xFE30 && r <= 0xFE6F, // CJK Compatibility Forms
		r >= 0xFF00 && r <= 0xFF60, // Fullwidth Forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
}

// tsIndent 缩进串。
func tsIndent(n int) string { return strings.Repeat(" ", n) }

// tsCall 按 Prettier 的规则渲染一次调用表达式语句:整行放得下就一行,放不下就把
// 实参逐个换行并补尾随逗号。这正是 Prettier 对「实参里没有可 hug 的函数体」的调用
// 采取的展开形态。
func tsCall(indent int, prefix, callee string, args []string, suffix string) []string {
	pad := tsIndent(indent)
	one := pad + prefix + callee + "(" + strings.Join(args, ", ") + ")" + suffix
	if tsWidth(one) <= tsPrintWidth {
		return []string{one}
	}
	inner := tsIndent(indent + 2)
	out := []string{pad + prefix + callee + "("}
	for _, a := range args {
		out = append(out, inner+a+",")
	}
	return append(out, pad+")"+suffix)
}

// tsFuncHeader 渲染函数签名:放不下就把形参换行(Prettier 对单形参函数的展开形态)。
func tsFuncHeader(name, param, ret string) []string {
	one := "export function " + name + "(" + param + "): " + ret + " {"
	if tsWidth(one) <= tsPrintWidth {
		return []string{one}
	}
	return []string{
		"export function " + name + "(",
		"  " + param + ",",
		"): " + ret + " {",
	}
}

// tsDocComment 把 Go 文档注释渲染成 JSDoc。Prettier 不重排注释内容,所以这里
// 输出什么就是什么;`*/` 会被拆开,免得提前闭合注释。
func tsDocComment(indent int, text string) []string {
	text = strings.ReplaceAll(strings.TrimRight(text, "\n"), "*/", "* /")
	if text == "" {
		return nil
	}
	pad := tsIndent(indent)
	lines := strings.Split(text, "\n")
	if len(lines) == 1 {
		return []string{pad + "/** " + lines[0] + " */"}
	}
	out := []string{pad + "/**"}
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if l == "" {
			out = append(out, pad+" *")
			continue
		}
		out = append(out, pad+" * "+l)
	}
	return append(out, pad+" */")
}

// ── Go → TS 类型映射 ────────────────────────────────────────────────────────

// tsField 一个字段映射到 TS 后的全部信息。
type tsField struct {
	jsonName string
	tsType   string
	optional bool
	callee   string   // 校验助手名;空 = 该字段无法校验(unknown)
	args     []string // 校验调用的实参
}

// rawMessageType 用来把 json.RawMessage 与普通 []byte 区分开:前者是原样透传的
// 任意 JSON(TS 侧 unknown),后者被 encoding/json 编成 base64 字符串。
var rawMessageType = reflect.TypeOf(json.RawMessage(nil))

// wirePkgPath 本包的导入路径 —— 「只追包内类型」这条边界的判定依据。
func wirePkgPath() string { return reflect.TypeOf(OK{}).PkgPath() }

// isWireStruct 判断类型是否是本包声明的结构。包外的结构一律 unknown:
// 它们大多没有 JSON tag(按 Go 字段名裸序列化,如 agentruntime.MCPServerSpec),
// TS 侧从未为它们建过类型,追进去等于凭空发明一份新契约。
func isWireStruct(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.PkgPath() == wirePkgPath()
}

// tsTypeOnly 只求 TS 类型文本(用于数组元素 / map 值这类没有独立校验位置的地方)。
func tsTypeOnly(t reflect.Type) string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == rawMessageType {
		return "unknown"
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return "string" // []byte 被 encoding/json 编成 base64 字符串
		}
		return tsTypeOnly(t.Elem()) + "[]"
	case reflect.Map:
		return "Record<string, " + tsTypeOnly(t.Elem()) + ">"
	case reflect.Struct:
		if isWireStruct(t) {
			return t.Name()
		}
		return "unknown"
	default:
		return "unknown"
	}
}

// tsFieldSpec 把一个结构字段映射成 TS 声明 + 校验调用。
// owner 只用来拼错误信息里的路径(与手写 codec 同形:`wire: Type.field …`)。
func tsFieldSpec(owner string, f reflect.StructField) (tsField, bool) {
	tag := f.Tag.Get("json")
	if tag == "-" || f.PkgPath != "" {
		return tsField{}, false
	}
	name, opts, _ := strings.Cut(tag, ",")
	if name == "" {
		name = f.Name
	}
	out := tsField{
		jsonName: name,
		optional: strings.Contains(","+opts+",", ",omitempty,"),
	}
	path := strconv.Quote(owner + "." + name)
	ref := "o." + name

	t := f.Type
	nullable := false
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
		nullable = isWireStruct(t)
	}

	switch {
	case t == rawMessageType:
		out.tsType = "unknown" // 原样透传的 JSON,任何值都合法,无从校验
	case t.Kind() == reflect.String:
		out.tsType, out.callee, out.args = "string", pick(out.optional, "optStr", "reqStr"), []string{ref, path}
	case t.Kind() == reflect.Bool:
		out.tsType, out.callee, out.args = "boolean", pick(out.optional, "optBool", "reqBool"), []string{ref, path}
	case isNumberKind(t.Kind()):
		out.tsType, out.callee, out.args = "number", pick(out.optional, "optNum", "reqNum"), []string{ref, path}
	case (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) && t.Elem().Kind() == reflect.Uint8:
		// []byte:encoding/json 编成 base64 字符串。
		out.tsType, out.callee, out.args = "string", pick(out.optional, "optStr", "reqStr"), []string{ref, path}
	case t.Kind() == reflect.Slice || t.Kind() == reflect.Array:
		elem := t.Elem()
		if elem.Kind() == reflect.Pointer {
			elem = elem.Elem()
		}
		if isWireStruct(elem) {
			out.tsType = elem.Name() + "[]"
			out.callee = pick(out.optional, "optArrOf", "reqArrOf")
			out.args = []string{ref, path, "decode" + elem.Name()}
			break
		}
		out.tsType = tsTypeOnly(t)
		out.callee, out.args = pick(out.optional, "optArr", "reqArr"), []string{ref, path}
	case t.Kind() == reflect.Map:
		out.tsType = tsTypeOnly(t)
		out.callee, out.args = pick(out.optional, "optObj", "reqObj"), []string{ref, path}
	case isWireStruct(t):
		out.tsType = t.Name()
		if nullable {
			// Go 指针字段:配 omitempty 时 nil 直接省键,但对端显式送 null 也不该
			// 让整帧解码失败 —— 保留手写 codec 的这份宽容。
			out.tsType += " | null"
			out.callee, out.args = "optOf", []string{ref, "decode" + t.Name()}
			break
		}
		out.callee, out.args = "decode"+t.Name(), []string{ref}
	default:
		out.tsType = "unknown"
	}
	return out, true
}

func pick(cond bool, yes, no string) string {
	if cond {
		return yes
	}
	return no
}

func isNumberKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// ── AST:文档注释 + 本包声明了什么 ──────────────────────────────────────────

// wireDecls 是 AST 从本包源码里读出来的东西:反射拿不到的文档注释,以及
// 「本包到底声明了哪些导出结构 / 常量」—— 完整性守卫的依据。
type wireDecls struct {
	structNames []string
	structDocs  map[string]string
	fieldDocs   map[string]map[string]string
	constNames  []string
	constDocs   map[string]string
}

// parseGoPackage 解析一个目录下的非测试 Go 源码,按文件名序把每份 AST 交给 visit。
// 按文件名排序是为了让产物顺序确定。
func parseGoPackage(t *testing.T, dir string, visit func(*ast.File)) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "读 Go 包目录 %s", dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)

	fset := token.NewFileSet()
	for _, n := range names {
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.ParseComments)
		require.NoError(t, err, "解析 Go 源码 %s", n)
		visit(f)
	}
}

// parseWireDecls 解析本包的非测试源码。
func parseWireDecls(t *testing.T) wireDecls {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)

	out := wireDecls{
		structDocs: map[string]string{},
		fieldDocs:  map[string]map[string]string{},
		constDocs:  map[string]string{},
	}
	parseGoPackage(t, dir, func(f *ast.File) { collectFileDecls(f, &out) })
	return out
}

func collectFileDecls(file *ast.File, out *wireDecls) {
	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok {
			continue
		}
		switch gd.Tok {
		case token.TYPE:
			collectTypeDecls(gd, out)
		case token.CONST:
			collectConstDecls(gd, out)
		}
	}
}

func collectTypeDecls(gd *ast.GenDecl, out *wireDecls) {
	for _, s := range gd.Specs {
		ts, ok := s.(*ast.TypeSpec)
		if !ok || !ts.Name.IsExported() {
			continue
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			continue
		}
		name := ts.Name.Name
		out.structNames = append(out.structNames, name)
		out.structDocs[name] = docText(ts.Doc, gd.Doc, len(gd.Specs))
		fields := map[string]string{}
		for _, f := range st.Fields.List {
			doc := docText(f.Doc, nil, 0)
			if doc == "" && f.Comment != nil {
				doc = f.Comment.Text()
			}
			for _, id := range f.Names {
				fields[id.Name] = strings.TrimRight(doc, "\n")
			}
		}
		out.fieldDocs[name] = fields
	}
}

func collectConstDecls(gd *ast.GenDecl, out *wireDecls) {
	for _, s := range gd.Specs {
		vs, ok := s.(*ast.ValueSpec)
		if !ok {
			continue
		}
		doc := docText(vs.Doc, gd.Doc, len(gd.Specs))
		if doc == "" && vs.Comment != nil {
			doc = strings.TrimRight(vs.Comment.Text(), "\n")
		}
		for _, id := range vs.Names {
			if !id.IsExported() {
				continue
			}
			out.constNames = append(out.constNames, id.Name)
			out.constDocs[id.Name] = doc
		}
	}
}

// eventKindDecls 是 AST 从 agentruntime 包源码里读出的 EventKind 词表:
// 声明序的常量名 + 文档注释。词表分散在该包的多个文件里(runner.go 是主表,
// event_wire.go 另有补登的 discriminator),所以扫的是整个包目录而不是某一个文件。
type eventKindDecls struct {
	names []string
	docs  map[string]string
}

// parseEventKindDecls 从 agentruntime 包源码里读出全部 EventKind 常量。
func parseEventKindDecls(t *testing.T) eventKindDecls {
	t.Helper()
	out := eventKindDecls{docs: map[string]string{}}
	dir := filepath.Join(repoRoot(t), agentruntimeRel)
	parseGoPackage(t, dir, func(f *ast.File) { collectEventKindDecls(f, &out) })
	return out
}

// collectEventKindDecls 挑出 const 声明里类型为 EventKind 的项。
func collectEventKindDecls(file *ast.File, out *eventKindDecls) {
	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		// isKind 在一个 const 组里逐行推进:显式写了类型的行直接判定,省略类型的
		// 行沿用组内上一行(Go 的隐式重复)。省略类型却自带值的行严格说是 untyped
		// string,但它在每一个需要 EventKind 的调用点都照样能用 —— 对词表而言是
		// 同一样东西,一并收进来才不会留下缺口。
		isKind := false
		for _, s := range gd.Specs {
			vs, ok := s.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type != nil {
				id, ok := vs.Type.(*ast.Ident)
				isKind = ok && id.Name == eventKindTypeName
			}
			if !isKind {
				continue
			}
			doc := docText(vs.Doc, gd.Doc, len(gd.Specs))
			if doc == "" && vs.Comment != nil {
				doc = strings.TrimRight(vs.Comment.Text(), "\n")
			}
			for _, id := range vs.Names {
				if !id.IsExported() {
					continue
				}
				out.names = append(out.names, id.Name)
				out.docs[id.Name] = doc
			}
		}
	}
}

// ── AST:本仓的块类型注册点 ────────────────────────────────────────────────

// scanBlockTypeRegistrations 扫本仓源码,返回全部块类型的判别值(未排序、可能重复)。
//
// 扫的是**注册动作**而不是「实现了 ContentBlock 的类型」:没注册进注册表的类型
// 根本无法跨进程往返,不属于词表。两种注册形态都认:
//
//	blocks.RegisterFactory[XxxBlock]()   判别值取 XxxBlock 的 Type() 返回字面量
//	blocks.Register("xxx", factory)      判别值就是第一个实参的字面量
//
// 认得出注册点的前提是文件 import 了块注册表包 —— 先只解析 import 段(便宜),
// 没有这个 import 的文件直接跳过,不做完整解析。裸叫 Register( 的地方本仓多得是
// (jsonrpc registry、wails binding …),必须靠 import 的本地名限定,否则全是误报。
func scanBlockTypeRegistrations(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)

	// dir → 该目录里被 RegisterFactory 点名的类型名。
	pending := map[string][]string{}
	var values []string

	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if skipScanDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		imports, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("解析 import 段 %s: %w", path, err)
		}
		local, ok := blocksImportName(imports)
		if !ok {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("解析 %s: %w", path, err)
		}
		dir := filepath.Dir(path)
		types, direct := collectBlockRegistrations(t, path, local, file)
		pending[dir] = append(pending[dir], types...)
		values = append(values, direct...)
		return nil
	})
	require.NoError(t, err, "遍历仓库源码")

	dirs := make([]string, 0, len(pending))
	for dir := range pending {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		discs := blockTypeDiscriminators(t, dir)
		for _, name := range pending[dir] {
			v, ok := discs[name]
			require.True(t, ok,
				"%s 注册了 %s,却在同一个包里找不到它的 Type() string 返回字面量", dir, name)
			values = append(values, v)
		}
	}
	return values
}

// skipScanDir 是扫描要跳过的目录:版本库元数据、前端与其依赖、构建产物。
// 它们要么没有 Go 源码,要么(node_modules)藏着与本仓无关的 Go 包。
func skipScanDir(name string) bool {
	switch name {
	case ".git", "node_modules", "frontend", "build", "dist":
		return true
	}
	return false
}

// blocksImportName 返回块注册表包在这个文件里的本地名。
func blocksImportName(file *ast.File) (string, bool) {
	for _, imp := range file.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || p != blocksPkgPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, imp.Name.Name != "_"
		}
		return "blocks", true
	}
	return "", false
}

// collectBlockRegistrations 从一份 AST 里挑出注册调用:
// RegisterFactory 返回被点名的类型名,Register 直接返回判别值字面量。
func collectBlockRegistrations(t *testing.T, path, local string, file *ast.File) (types, values []string) {
	t.Helper()
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.IndexExpr: // blocks.RegisterFactory[XxxBlock]()
			if !isBlocksSelector(fn.X, local, "RegisterFactory") {
				return true
			}
			id, ok := fn.Index.(*ast.Ident)
			require.True(t, ok,
				"%s:RegisterFactory 的类型实参不是本包的具名类型,扫描器无从取判别值", path)
			types = append(types, id.Name)
		case *ast.SelectorExpr: // blocks.Register("xxx", factory)
			if !isBlocksSelector(fn, local, "Register") {
				return true
			}
			require.NotEmpty(t, call.Args, "%s:Register 没有实参", path)
			lit, ok := call.Args[0].(*ast.BasicLit)
			require.True(t, ok && lit.Kind == token.STRING,
				"%s:Register 的判别值不是字符串字面量,扫描器无从取值", path)
			v, err := strconv.Unquote(lit.Value)
			require.NoError(t, err, "%s:Register 的判别值解不出来", path)
			values = append(values, v)
		}
		return true
	})
	return types, values
}

// isBlocksSelector 判断表达式是不是「块注册表包的某个函数」。
func isBlocksSelector(e ast.Expr, local, fn string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != fn {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == local
}

// blockTypeDiscriminators 读一个包里全部 `func (X) Type() string { return "…" }`,
// 返回类型名 → 判别值。ContentBlock 的判别值只能这么写(接口要求 Type() string),
// 拼接 / 常量引用一律取不到,取不到就在调用方 require 变红,不会静默漏一格。
func blockTypeDiscriminators(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	parseGoPackage(t, dir, func(f *ast.File) {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Name.Name != "Type" || fd.Recv == nil || len(fd.Recv.List) != 1 {
				continue
			}
			recv, ok := receiverTypeName(fd.Recv.List[0].Type)
			if !ok {
				continue
			}
			if v, ok := singleStringReturn(fd); ok {
				out[recv] = v
			}
		}
	})
	return out
}

// receiverTypeName 取接收者的类型名(值接收者与指针接收者同等对待)。
func receiverTypeName(e ast.Expr) (string, bool) {
	if star, ok := e.(*ast.StarExpr); ok {
		e = star.X
	}
	id, ok := e.(*ast.Ident)
	if !ok {
		return "", false
	}
	return id.Name, true
}

// singleStringReturn 取「函数体只有一句 return "字面量"」的那个字面量。
func singleStringReturn(fd *ast.FuncDecl) (string, bool) {
	if fd.Body == nil || len(fd.Body.List) != 1 {
		return "", false
	}
	ret, ok := fd.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return "", false
	}
	lit, ok := ret.Results[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	return v, err == nil
}

// ── AST:本仓的视图块类型词表 ──────────────────────────────────────────────

// parseChatBlockTypeConsts 读出 chat_svc 包里全部 `Name = "字面量"` 形态的常量,
// 按声明它们的 const 块分组;返回 (常量名 → 组键, 组键 → 组内声明序的词条)。
//
// 只认**无类型**字符串常量:ChatBlock.Type 的 Go 类型是 string(必须保持,否则
// wails 生成的 models.ts 会跟着变),词表常量因此只能是无类型的才赋得进去。带类型
// 的常量(如 ChatStreamEventKind 那一组)天然赋不进 Type,自然也不可能是词表成员。
//
// 组键取该块里第一个常量的名字 —— 不需要位置信息就能稳定区分两个 const 块,
// 失败信息里也读得懂。
func parseChatBlockTypeConsts(t *testing.T) (owner map[string]string, groups map[string][]chatBlockTypeDecl) {
	t.Helper()
	owner = map[string]string{}
	groups = map[string][]chatBlockTypeDecl{}
	parseGoPackage(t, filepath.Join(repoRoot(t), chatSvcRel), func(f *ast.File) {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			key := ""
			for _, s := range gd.Specs {
				name, decl, ok := untypedStringConst(gd, s)
				if !ok {
					continue
				}
				if key == "" {
					key = name
				}
				owner[name] = key
				groups[key] = append(groups[key], decl)
			}
		}
	})
	return owner, groups
}

// untypedStringConst 把一条 `Name = "字面量"` 的 ValueSpec 读成词条;形态不符返回 false。
func untypedStringConst(gd *ast.GenDecl, s ast.Spec) (string, chatBlockTypeDecl, bool) {
	vs, ok := s.(*ast.ValueSpec)
	if !ok || vs.Type != nil || len(vs.Names) != 1 || len(vs.Values) != 1 {
		return "", chatBlockTypeDecl{}, false
	}
	lit, ok := vs.Values[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", chatBlockTypeDecl{}, false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", chatBlockTypeDecl{}, false
	}
	doc := docText(vs.Doc, gd.Doc, len(gd.Specs))
	if doc == "" && vs.Comment != nil {
		doc = strings.TrimRight(vs.Comment.Text(), "\n")
	}
	name := vs.Names[0].Name
	return name, chatBlockTypeDecl{name: name, value: v, doc: doc}, true
}

// scanChatBlockTypeUses 扫 chat_svc 包,返回全部 ChatBlock{Type: …} 构造点写进去的
// 常量名(按出现序,可能重复)。
//
// 扫的是**构造动作**而不是常量声明 —— 与 scanBlockTypeRegistrations 同一条思路:
// 声明了却没人往 ChatBlock 里写的常量不是词表成员,而写进去了却没有常量的值更是
// 本轮要消灭的那种漂移。两条要求一并在这里钉死:
//
//   - 每个 ChatBlock 复合字面量都必须显式给 Type —— 漏了会发出 type:"" 的块,
//     消费方的 switch 静默落进 default;
//   - Type 的值必须是标识符,不能是裸字符串字面量。
//
// 只扫 chat_svc 包本身(parseGoPackage 不进子目录):子包里不再有同名类型,
// chat_svc.ChatBlock 是这份词表唯一的真理来源。
func scanChatBlockTypeUses(t *testing.T) []string {
	t.Helper()
	var used []string
	parseGoPackage(t, filepath.Join(repoRoot(t), chatSvcRel), func(f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if arr, ok := cl.Type.(*ast.ArrayType); ok {
				if id, ok := arr.Elt.(*ast.Ident); ok && id.Name == chatBlockTypeName {
					require.Empty(t, cl.Elts,
						"%s 里出现了带元素的 []%s{…} 字面量:元素类型是隐式的,扫描器读不到它的 %s,"+
							"请改成逐个 %s{…} 构造", chatSvcRel, chatBlockTypeName, chatBlockTypeField, chatBlockTypeName)
				}
				return true
			}
			id, ok := cl.Type.(*ast.Ident)
			if !ok || id.Name != chatBlockTypeName {
				return true
			}
			used = append(used, chatBlockTypeValue(t, cl))
			return true
		})
	})
	return used
}

// chatBlockTypeValue 取一个 ChatBlock 复合字面量里 Type 字段写入的常量名。
func chatBlockTypeValue(t *testing.T, cl *ast.CompositeLit) string {
	t.Helper()
	for _, e := range cl.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != chatBlockTypeField {
			continue
		}
		if id, ok := kv.Value.(*ast.Ident); ok {
			return id.Name
		}
		lit, isLit := kv.Value.(*ast.BasicLit)
		require.False(t, isLit && lit.Kind == token.STRING,
			"%s.%s 被赋了裸字符串字面量 %s —— 视图词表的每一格都必须是 chat_block_type.go 里的具名常量,"+
				"否则它对生成器不存在、对两个前端也只是又一处手抄。改完重新生成:\n\t%s",
			chatBlockTypeName, chatBlockTypeField, lit.Value, tsRegenCmd)
		require.FailNow(t,
			"生成器读不出词表成员",
			"%s.%s 被赋了一个既不是具名常量也不是字面量的表达式;词表必须是可穷举的",
			chatBlockTypeName, chatBlockTypeField)
		return ""
	}
	require.FailNow(t, "构造点漏了判别值",
		"%s 里有一处 %s{…} 没有显式给 %s,它会发出 type:\"\" 的块,消费方的 switch 静默落进 default",
		chatSvcRel, chatBlockTypeName, chatBlockTypeField)
	return ""
}

// docText 取 spec 自己的注释;没有且整个 GenDecl 只声明一项时退回块注释。
func docText(own, block *ast.CommentGroup, blockSpecs int) string {
	if own != nil {
		return strings.TrimRight(own.Text(), "\n")
	}
	if block != nil && blockSpecs == 1 {
		return strings.TrimRight(block.Text(), "\n")
	}
	return ""
}

// ── 渲染 ────────────────────────────────────────────────────────────────────

// tsGenHeader 每个产物的文件头:出处 + 禁止手改 + 重新生成命令 + 边界与格式约定。
//
// truth 是这份产物的真理源,boundary 是它与「只追 wire 包内类型」那条边界的关系 ——
// 两份 wire 产物(codec / constants)守着边界,另外三份各自越界,理由写在自己的头里:
// event-kinds.gen.ts 与 block-types.gen.ts 追的是 wire 上唯一有类型意义的判别值
// (见 tsEventKindBoundary / tsBlockTypeBoundary);chat-block-types.gen.ts 性质不同,
// 它讲的根本不是 wire(见 tsChatBlockTypeBoundary)。
func tsGenHeader(what string, truth, boundary []string) []string {
	out := []string{
		"/**",
		" * " + what,
		" *",
		" * 本文件由 Go 生成器产出,**不要手改** —— 手改会被下一次重新生成覆盖,",
		" * 而且 TestGeneratedTSFresh 会立刻变红。",
		" *",
	}
	out = append(out, truth...)
	out = append(out,
		" * 生成器:  internal/pkg/agentruntime/runtimes/remote/wire/tsgen_test.go",
		" * 重新生成:",
		" *",
		" *   "+tsRegenCmd,
		" *",
	)
	out = append(out, boundary...)
	return append(out,
		" *",
		" * 格式:生成器直接输出 Prettier(printWidth 80,本仓默认配置)的形态,",
		" * 与手写代码同一套 ESLint 规则,没有整文件豁免。格式化是产物的一部分 ——",
		" * 若放到生成之后当外部工序,「重新生成 → 逐字节比对」的守卫会永久误报。",
		" */",
	)
}

// tsWireTruth / tsWireBoundary 两份 wire 产物(codec / constants)共用的头部段落。
func tsWireTruth() []string {
	return []string{" * 真理源:  internal/pkg/agentruntime/runtimes/remote/wire/wire.go"}
}

func tsWireBoundary() []string {
	return []string{
		" * 边界:wire 包**之外**的类型一律映射成 unknown。它们大多没有 JSON tag",
		" * (按 Go 字段名裸序列化,如 agentruntime.MCPServerSpec 的 Name/URL),",
		" * TS 侧从未为它们建过类型;追进去等于凭空发明一份新契约。",
	}
}

// tsEventKindTruth / tsEventKindBoundary 是词表产物的头部段落。边界那段把破例的
// 理由写在产物里 —— 读到这个文件的人第一眼就该知道它为什么可以越界,以及例外
// 到哪里为止(完整论证见 tsEventKindDecls)。
func tsEventKindTruth() []string {
	return []string{
		" * 真理源:  internal/pkg/agentruntime 的 EventKind 常量",
		" *          (runner.go 是主表,event_wire.go 另有补登的 discriminator)",
	}
}

func tsEventKindBoundary() []string {
	return []string{
		" * 边界例外:codec / constants 两份产物守的规矩是「wire 包之外的类型一律",
		" * unknown」,这一份与 block-types.gen.ts 是仅有的两处刻意例外",
		" * (chat-block-types.gen.ts 不在此列 —— 它讲的根本不是 wire)。理由:",
		" *",
		" * EventFrame.event 在 Go 侧是 json.RawMessage —— 载荷对 wire 完全不透明,",
		" * 生成器对它只能给出 unknown。整条事件流里唯一有类型意义的东西就是这个",
		" * kind 判别值,而它恰恰是 agentre ↔ agentred 的契约本身;把词表留在生成",
		" * 范围之外,等于契约里唯一可校验的那一格没有任何机械保证。agentruntime",
		" * 又是 wire 包的直接依赖(wire.go 已在用它的 TurnKind / MCPServerSpec),",
		" * 追它不打穿分层。",
		" *",
		" * 例外到此为止:这不是「凡是 agentruntime 的东西都生成」的先例。",
	}
}

// tsEventKindUnionDoc 是词表联合类型的 JSDoc —— 解释它为什么存在,而不是复述定义。
const tsEventKindUnionDoc = `全部 kind 的联合类型(= Go 的 agentruntime.EventKind)。

消费方把手上的 kind 收窄成这个类型之后,在 switch 的 default 分支写一句
const _: never = kind,「Go 新增了一个 kind」就成了消费方的编译期错误。载荷
本身是 json.RawMessage、无从校验,kind 是这条链路上唯一能被类型系统接住的东西。`

// renderTSEventKinds 渲染 EventKind 词表产物:逐条常量 + 一个联合类型。
func renderTSEventKinds(decls eventKindDecls) string {
	kinds := tsEventKindDecls()
	lines := tsGenHeader(
		"agentruntime.EventKind 词表:EventFrame.event 载荷里 kind 判别值的全部取值。",
		tsEventKindTruth(),
		tsEventKindBoundary(),
	)
	for _, c := range kinds {
		lines = append(lines, "")
		lines = append(lines, tsDocComment(0, decls.docs[c.name])...)
		lines = append(lines, "export const "+c.name+" = "+strconv.Quote(string(c.value))+";")
	}

	lines = append(lines, "")
	lines = append(lines, tsDocComment(0, tsEventKindUnionDoc)...)
	// Prettier 对放不下一行的联合类型的形态:`=` 后换行,每支一行、前置 `|`。
	lines = append(lines, "export type EventKind =")
	for i, c := range kinds {
		tail := ""
		if i == len(kinds)-1 {
			tail = ";"
		}
		lines = append(lines, "  | typeof "+c.name+tail)
	}
	return strings.Join(lines, "\n") + "\n"
}

// tsBlockTypeTruth / tsBlockTypeBoundary 是块类型词表产物的头部段落。
func tsBlockTypeTruth() []string {
	return []string{
		" * 真理源:  " + blocksPkgPath + " 的块注册表",
		" *          (运行时枚举 + 本仓注册点的 AST 扫描,两者取并集)",
	}
}

func tsBlockTypeBoundary() []string {
	return []string{
		" * 边界例外:codec / constants 两份产物守的规矩是「wire 包之外的类型一律",
		" * unknown」,这一份与 event-kinds.gen.ts 是仅有的两处刻意例外",
		" * (chat-block-types.gen.ts 不在此列 —— 它讲的根本不是 wire)。理由:",
		" *",
		" * StoredBlock 在 Go 侧是 {type, data},data 是 json.RawMessage —— 载荷对",
		" * wire 完全不透明,生成器对它只能给出 unknown。HistoryMessageWire.blocks",
		" * 与 RunParams.userBlocks 这两条链路上唯一有类型意义的东西就是 type 判别值,",
		" * 而块注册表包正是 wire.go 的直接依赖(那两个字段的元素类型就是它的",
		" * StoredBlock),追它不打穿分层。",
		" *",
		" * 与 event-kinds.gen.ts 的一处不同:那张表是编译期常量,AST 扫源码就能穷举;",
		" * 这一份是运行时注册表,判别值散在各类型的 Type() 方法里、由各包的 init()",
		" * 填进注册表。完整性守卫因此形态不同,见生成器里的 TestTSGenCoversBlockTypes。",
	}
}

// tsBlockTypeUnionDoc 是块类型联合类型的 JSDoc —— 解释它为什么存在,而不是复述定义。
const tsBlockTypeUnionDoc = `全部块类型判别值的联合类型(= Go 的 blocks.StoredBlock.type 取值域)。

消费方把手上的 type 收窄成这个类型之后,在 switch 的 default 分支写一句
const _: never = type,「上游新增了一个块类型」就成了消费方的编译期错误。块的
data 是 json.RawMessage、无从校验,type 是这条链路上唯一能被类型系统接住的东西。`

// renderTSBlockTypes 渲染块类型词表产物:逐条常量 + 一个联合类型。
//
// 刻意不带逐条 JSDoc:运行时枚举只给得出判别值本身,给不出声明处的文档注释,
// 而第三方(cago)那批块类型的注释更是本仓 AST 够不着的。一半有一半没有会让人
// 误以为「没注释的那些是本仓的」,不如一条都不带。
func renderTSBlockTypes(vocab []string) string {
	lines := tsGenHeader(
		"块类型词表:blocks.StoredBlock 的 type 判别值的全部取值。",
		tsBlockTypeTruth(), tsBlockTypeBoundary(),
	)
	for _, v := range vocab {
		lines = append(lines, "")
		lines = append(lines, "export const "+tsBlockTypeConstName(v)+" = "+strconv.Quote(v)+";")
	}

	lines = append(lines, "")
	lines = append(lines, tsDocComment(0, tsBlockTypeUnionDoc)...)
	// Prettier 对放不下一行的联合类型的形态:`=` 后换行,每支一行、前置 `|`。
	lines = append(lines, "export type BlockType =")
	for i, v := range vocab {
		tail := ""
		if i == len(vocab)-1 {
			tail = ";"
		}
		lines = append(lines, "  | typeof "+tsBlockTypeConstName(v)+tail)
	}
	return strings.Join(lines, "\n") + "\n"
}

// tsChatBlockTypeTruth / tsChatBlockTypeBoundary 是视图块类型词表产物的头部段落。
//
// 边界那段是这份产物**最要紧**的内容 —— 前三份产物讲的都是「为什么可以越界去追
// 一个 wire 包外的类型」,这一份要讲的是另一件事:它压根不在 wire 上,以及真理
// 边界为什么画在「Go 发得出什么」而不是「前端能收到什么」。
func tsChatBlockTypeTruth() []string {
	return []string{
		" * 真理源:  internal/service/chat_svc 的 chat_block_type.go",
		" *          (成员由 AST 反查 ChatBlock{Type: …} 构造点得出,",
		" *           生成器里没有任何手抄清单)",
	}
}

func tsChatBlockTypeBoundary() []string {
	return []string{
		" * 边界:这是第三份越出 wire 包的产物,而且与前两份**性质不同**,值得先看清:",
		" *",
		" * event-kinds / block-types 越界的理由是「它就在 wire 上,而且是那条链路上",
		" * 唯一有类型意义的东西」。这一份不是 —— ChatBlock 根本不在 agentre ↔ agentred",
		" * 的 wire 上,它是 backend → 前端这一跳(桌面走 wails binding、web 控制台走",
		" * HTTP)的视图 DTO。它放在这个包里只有一个理由:这里是本仓 Go → TS 单向生成",
		" * 唯一的那道缝,而这份词表同样有两个前端在手抄。**别把它读成 wire 协议的一部分。**",
		" *",
		" * 与 block-types.gen.ts 不是同一张表(两份都导出,别混用):那份是",
		" * blocks.StoredBlock.type,持久化 / 跨进程的判别值;这份是 ChatBlock.type,",
		" * 视图判别值。chat_svc 的投影正是两者之间的翻译 —— 重命名(user_ask →",
		" * ask_user_question、tool_permission → tool_permission_request)、多对一折叠",
		" * (nested_tool_use 与 tool_use 都落成 tool_use)、整类丢弃(subagent_state",
		" * 合进外层 tool_use 块,permission_mode_change 直接 skip)。同名的那几格",
		" * (text / thinking / plan …)是投影恰好没改名,不是同一个真理。",
		" *",
		" * 真理边界画在「Go 发得出什么」:本词表 = chat_svc 的投影能写进 ChatBlock.type",
		" * 的全部取值,一格不多、一格不少。前端的 TranscriptBlock.type 今天比它多一格",
		" * \"raw\" —— 那是 peer-transcript.ts 自产的降级形态(认不出的 peer 事件帧原样",
		" * JSON 塞进去),Go 从不发它,它的真理本来就该留在产它的那一侧。所以消费方收窄时",
		" * **不要**直接拿 ChatBlockType 当 TranscriptBlock.type 的类型,而应写成",
		" * ChatBlockType | <前端自产的那几格>,让每一格的真理留在产它的那一侧。",
		" *",
		" * 一个容易踩的坑:\"unknown\" 在本词表里 —— 它是 **Go 侧**的降级形态(投影认不出",
		" * 的持久化块,原判别值放在 .raw.kind)。它与前端自产的 \"raw\" 长得像、所有者不同,",
		" * 别合并成一格。",
	}
}

// tsChatBlockTypeUnionDoc 是视图词表联合类型的 JSDoc —— 解释它为什么存在,而不是复述定义。
const tsChatBlockTypeUnionDoc = `全部视图块类型判别值的联合类型(= Go 的 chat_svc.ChatBlock.type 取值域)。

消费方把手上的 type 收窄成这个类型之后,在 switch 的 default 分支写一句
const _: never = type,「Go 的投影新增了一个视图块类型」就成了消费方的编译期
错误。今天后端加一格,桌面端与 web 控制台的 switch 都不会有任何信号。

注意它**不等于**前端的 TranscriptBlock.type:后者还多一格前端自产的 "raw"
(peer-transcript.ts)。收窄时写 ChatBlockType | typeof … 组合,别直接替换。`

// renderTSChatBlockTypes 渲染视图块类型词表产物:逐条常量(带 Go 文档注释)+ 一个联合类型。
//
// 与 block-types.gen.ts 的一处不同:那份的判别值来自运行时注册表,拿不到声明处的
// 文档注释,索性一条都不带;这一份是 AST 读源码,每一格的注释都在,照搬过来。
func renderTSChatBlockTypes(vocab []chatBlockTypeDecl) string {
	lines := tsGenHeader(
		"视图块类型词表:chat_svc.ChatBlock 的 type 判别值的全部取值。",
		tsChatBlockTypeTruth(), tsChatBlockTypeBoundary(),
	)
	for _, c := range vocab {
		lines = append(lines, "")
		lines = append(lines, tsDocComment(0, c.doc)...)
		lines = append(lines, "export const "+c.name+" = "+strconv.Quote(c.value)+";")
	}

	lines = append(lines, "")
	lines = append(lines, tsDocComment(0, tsChatBlockTypeUnionDoc)...)
	// Prettier 对放不下一行的联合类型的形态:`=` 后换行,每支一行、前置 `|`。
	lines = append(lines, "export type ChatBlockType =")
	for i, c := range vocab {
		tail := ""
		if i == len(vocab)-1 {
			tail = ";"
		}
		lines = append(lines, "  | typeof "+c.name+tail)
	}
	return strings.Join(lines, "\n") + "\n"
}

// renderTSConstants 渲染常量产物。
func renderTSConstants(decls wireDecls) string {
	lines := tsGenHeader("wire 协议常量:RPC 方法名 / 通知名 / 错误码 / 会话生命周期 / 拉取上限。",
		tsWireTruth(), tsWireBoundary())
	for _, c := range tsConstDecls() {
		lines = append(lines, "")
		lines = append(lines, tsDocComment(0, decls.constDocs[c.name])...)
		lines = append(lines, "export const "+c.name+" = "+tsLiteral(c.value)+";")
	}
	return strings.Join(lines, "\n") + "\n"
}

func tsLiteral(v any) string {
	switch x := v.(type) {
	case string:
		return strconv.Quote(x)
	case int:
		return strconv.Itoa(x)
	default:
		panic(fmt.Sprintf("tsgen: 不支持的常量类型 %T", v))
	}
}

// renderTSCodec 渲染帧类型 + 编解码产物。
func renderTSCodec(t *testing.T, decls wireDecls) string {
	t.Helper()

	roots := tsRootTypes()
	known := map[string]bool{}
	for _, rt := range roots {
		known[rt.Name()] = true
	}

	used := map[string]bool{}
	body := make([]string, 0, 1024)
	for _, rt := range roots {
		name := rt.Name()
		fields := make([]tsField, 0, rt.NumField())
		for i := 0; i < rt.NumField(); i++ {
			f := rt.Field(i)
			require.False(t, f.Anonymous,
				"wire.%s 有嵌入字段 %s,生成器不支持(encoding/json 会把它拍平);请改成具名字段", name, f.Name)
			spec, ok := tsFieldSpec(name, f)
			if !ok {
				continue
			}
			require.True(t, refOK(spec, known),
				"wire.%s.%s 引用了未登记的包内类型;把它加进 tsRootTypes()", name, f.Name)
			fields = append(fields, spec)
			if spec.callee != "" {
				used[spec.callee] = true
			}
		}
		body = append(body, "")
		body = append(body, renderTSInterface(name, decls, fields)...)
		body = append(body, "")
		body = append(body, renderTSDecoder(name, fields)...)
		body = append(body, "")
		body = append(body, renderTSEncoder(name)...)
	}

	lines := tsGenHeader("wire 协议帧类型与编解码:与 wire.go 的 JSON tag 逐字段同构。",
		tsWireTruth(), tsWireBoundary())
	lines = append(lines, "")
	lines = append(lines, renderTSImport(used)...)
	return strings.Join(append(lines, body...), "\n") + "\n"
}

// refOK 校验字段引用到的包内类型确实在清单里(否则产物会引用一个不存在的 decodeX)。
func refOK(f tsField, known map[string]bool) bool {
	base := strings.TrimSuffix(strings.TrimSuffix(f.tsType, " | null"), "[]")
	switch base {
	case "string", "number", "boolean", "unknown":
		return true
	}
	if strings.HasPrefix(base, "Record<") {
		return true
	}
	return known[base]
}

// renderTSImport 渲染从手写运行时取校验助手的 import —— 只列真正用到的名字,
// 免得撞上 @typescript-eslint/no-unused-vars。
func renderTSImport(used map[string]bool) []string {
	extra := make([]string, 0, len(used))
	for n := range used {
		if strings.HasPrefix(n, "decode") {
			continue // 包内类型的解码函数在本文件里,不用 import
		}
		extra = append(extra, n)
	}
	sort.Strings(extra)
	names := make([]string, 0, 3+len(extra))
	names = append(names, "type WireObject", "decodeWire", "encodeWire")
	names = append(names, extra...)

	one := "import { " + strings.Join(names, ", ") + " } from " + strconv.Quote(tsRuntimeModule) + ";"
	if tsWidth(one) <= tsPrintWidth {
		return []string{one}
	}
	out := []string{"import {"}
	for _, n := range names {
		out = append(out, "  "+n+",")
	}
	return append(out, "} from "+strconv.Quote(tsRuntimeModule)+";")
}

func renderTSInterface(name string, decls wireDecls, fields []tsField) []string {
	out := tsDocComment(0, decls.structDocs[name])
	// 空结构(如 wire.OK)在 TS 里就是「只剩未知字段」,直接等价于 WireObject ——
	// 写成空 interface 会撞 @typescript-eslint/no-empty-object-type。
	if len(fields) == 0 {
		return append(out, "export type "+name+" = WireObject;")
	}
	out = append(out, "export interface "+name+" extends WireObject {")
	goFields := decls.fieldDocs[name]
	for i, f := range fields {
		doc := tsDocComment(2, goFieldDoc(goFields, f))
		if len(doc) > 0 && i > 0 {
			out = append(out, "")
		}
		out = append(out, doc...)
		mark := ""
		if f.optional {
			mark = "?"
		}
		out = append(out, "  "+tsMemberName(f.jsonName)+mark+": "+f.tsType+";")
	}
	return append(out, "}")
}

// goFieldDoc 按 JSON 名反查 Go 字段的文档注释。tsField 只留 JSON 名,所以这里
// 用大小写不敏感的匹配把它对回 Go 字段名(wire 的 tag 就是字段名的 lowerCamel)。
func goFieldDoc(docs map[string]string, f tsField) string {
	for goName, doc := range docs {
		if strings.EqualFold(goName, f.jsonName) {
			return doc
		}
	}
	return ""
}

// tsMemberName 非标识符形态的键要加引号(wire 目前全是 lowerCamelCase)。
func tsMemberName(n string) string {
	for i, r := range n {
		ok := r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9')
		if !ok {
			return strconv.Quote(n)
		}
	}
	return n
}

// renderTSDecoder 渲染一个帧类型的解码函数。
//
// 形态跟着 Prettier 走:`decodeWire<T>(v, "T", (o) => {…})` 的实参里有个可以
// hug 的箭头函数,所以只要头一行放得下,Prettier 就保持「贴住」的写法;放不下才
// 把三个实参逐个换行(此时函数体缩进从 4 变成 6)。
func renderTSDecoder(name string, fields []tsField) []string {
	checked := make([]tsField, 0, len(fields))
	for _, f := range fields {
		if f.callee != "" {
			checked = append(checked, f)
		}
	}

	out := tsFuncHeader("decode"+name, "v: unknown", name)
	quoted := strconv.Quote(name)

	// 没有可校验的字段:coerce 是空箭头(形参会被 no-unused-vars 抓,所以不写 o)。
	if len(checked) == 0 {
		one := fmt.Sprintf("  return decodeWire<%s>(v, %s, () => {});", name, quoted)
		if tsWidth(one) <= tsPrintWidth {
			return append(out, one, "}")
		}
		out = append(out, "  return decodeWire<"+name+">(", "    v,", "    "+quoted+",",
			"    () => {},", "  );")
		return append(out, "}")
	}

	head := fmt.Sprintf("  return decodeWire<%s>(v, %s, (o) => {", name, quoted)
	if tsWidth(head) <= tsPrintWidth {
		out = append(out, head)
		out = append(out, renderTSCoerce(4, checked)...)
		return append(out, "  });", "}")
	}
	out = append(out, "  return decodeWire<"+name+">(", "    v,", "    "+quoted+",", "    (o) => {")
	out = append(out, renderTSCoerce(6, checked)...)
	return append(out, "    },", "  );", "}")
}

// renderTSCoerce 按目标缩进渲染逐字段校验语句(是否换行取决于最终缩进,
// 所以必须在这里、按最终缩进渲染)。
func renderTSCoerce(indent int, checked []tsField) []string {
	out := make([]string, 0, len(checked))
	for _, f := range checked {
		out = append(out, tsCall(indent, "o."+f.jsonName+" = ", f.callee, f.args, ";")...)
	}
	return out
}

func renderTSEncoder(name string) []string {
	out := tsFuncHeader("encode"+name, "v: "+name, "string")
	return append(out, "  return encodeWire(v);", "}")
}

// ── 产物 + 守卫 ─────────────────────────────────────────────────────────────

// tsSource 一份生成产物:文件名 + 内容。
type tsSource struct {
	name    string
	content string
}

func buildTSSources(t *testing.T) []tsSource {
	t.Helper()
	decls := parseWireDecls(t)
	return []tsSource{
		{name: "constants.gen.ts", content: renderTSConstants(decls)},
		{name: "codec.gen.ts", content: renderTSCodec(t, decls)},
		{name: "event-kinds.gen.ts", content: renderTSEventKinds(parseEventKindDecls(t))},
		{name: "block-types.gen.ts", content: renderTSBlockTypes(blockTypeVocabulary(t))},
		{name: "chat-block-types.gen.ts", content: renderTSChatBlockTypes(chatBlockTypeVocabulary(t))},
	}
}

func writeTSSources(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755), "创建产物目录")
	for _, s := range buildTSSources(t) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, s.name), []byte(s.content), 0o644), "写产物 %s", s.name)
	}
}

// listGenNames 列出目录里全部 *.gen.ts 产物文件名(已排序)。
func listGenNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err, "读产物目录 %s", dir)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".gen.ts") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// TestTSGenCoversWirePackage 完整性守卫:生成器的清单必须覆盖本包声明的全部
// 导出结构与导出常量。反射只认得被点名的类型,所以「新加一个 wire 结构」这条
// 最常见的漂移路径必须由 AST 来堵 —— 否则新结构悄悄不进产物,而新鲜度守卫
// (比的是「生成器此刻会写出什么」)对它一无所知,照样绿。
func TestTSGenCoversWirePackage(t *testing.T) {
	decls := parseWireDecls(t)

	listed := make([]string, 0, len(tsRootTypes()))
	for _, rt := range tsRootTypes() {
		listed = append(listed, rt.Name())
	}
	require.ElementsMatch(t, decls.structNames, listed,
		"wire 包的导出结构与 tsRootTypes() 不一致,补齐清单后重新生成:\n\t%s", tsRegenCmd)

	consts := make([]string, 0, len(tsConstDecls()))
	for _, c := range tsConstDecls() {
		consts = append(consts, c.name)
	}
	require.ElementsMatch(t, decls.constNames, consts,
		"wire 包的导出常量与 tsConstDecls() 不一致,补齐清单后重新生成:\n\t%s", tsRegenCmd)
}

// TestTSGenCoversEventKinds 完整性守卫:agentruntime 声明的 EventKind 常量必须
// 全部在 tsEventKindDecls() 里。
//
// 这条守卫就是把词表纳入生成的全部意义所在。此前 Go 侧加一个 kind,浏览器侧
// **没有任何信号** —— EventFrame.Event 是 json.RawMessage,往返测试照样绿,消费方
// 那份手抄的 kind 常量该不该多一条,从来没有人被问到过。有了它,Go 侧加常量这里
// 直接变红,「这个 kind 该不该有真正的渲染」才成为一个必须有人回答的问题。
//
// 扫的是整个 agentruntime 包目录而不是 runner.go 一个文件:词表本来就分散在
// 多个文件里(event_wire.go 补登过 EventContextWindowUpdated),盯死单个文件
// 等于给漂移留一扇后门。
func TestTSGenCoversEventKinds(t *testing.T) {
	decls := parseEventKindDecls(t)
	require.NotEmpty(t, decls.names, "没从 %s 扫到任何 EventKind 常量,扫描逻辑本身坏了", agentruntimeRel)

	listed := make([]string, 0, len(tsEventKindDecls()))
	for _, c := range tsEventKindDecls() {
		listed = append(listed, c.name)
	}
	require.ElementsMatch(t, decls.names, listed,
		"agentruntime 的 EventKind 常量与 tsEventKindDecls() 不一致,补齐清单后重新生成:\n\t%s", tsRegenCmd)
}

// TestTSGenCoversBlockTypes 完整性守卫:块类型词表。
//
// **与上面两条守卫机制不同**,这是这份词表最值得写清楚的一点:
//
//   - wire 结构与 EventKind 是**编译期**的东西 —— 一组类型声明、一张 const 表。
//     源码里躺着一份可穷举的清单,守卫因此是「AST 读源码 vs 读生成器清单」二者
//     相比,不需要把任何代码跑起来。
//   - 块类型是**运行时**的 —— 判别值散在各类型的 Type() 方法里,进不进词表取决于
//     某个包的 init() 有没有执行 RegisterFactory。源码里没有一张表可读,唯一的
//     真理是进程起来之后注册表里到底有什么。所以本文件 blank import 了注册包、
//     让 init() 真的跑,再问 blocks.RegisteredTypes()。
//
// 运行时枚举换来的是 AST 拿不到的东西:第三方(cago)自带的那批块类型。本仓的
// AST 永远扫不到依赖模块里的 init(),而它们照样会出现在 wire 上。
//
// 代价是一条 AST 式守卫没有的软肋:**没链接进测试二进制的包等于不存在**。
// chat_svc 正好撞上它(见 blockTypeCycleBound),所以这里同时留了一条 AST 扫描
// 兜底,并把「AST 扫到、运行时枚举漏掉」的差集钉死 —— 差集变化必须有人回答。
func TestTSGenCoversBlockTypes(t *testing.T) {
	registered := blocks.RegisteredTypes()
	require.NotEmpty(t, registered, "块注册表是空的,blank import 或访问器本身坏了")
	// 第三方词表还在 —— 这一格只有运行时枚举给得出,掉了说明 blank import
	// 被人删了或依赖降级了。
	require.Subset(t, registered, []string{"text", "image", "tool_use", "tool_result", "thinking"},
		"cago 自带的块类型不在注册表里,运行时枚举这条路断了")

	scanned := scanBlockTypeRegistrations(t)
	require.NotEmpty(t, scanned, "AST 没在本仓扫到任何块类型注册点,扫描逻辑本身坏了")

	vocab := blockTypeVocabulary(t)
	require.Subset(t, vocab, registered, "运行时注册表里有词表漏掉的判别值")
	require.Subset(t, vocab, scanned, "本仓注册点里有词表漏掉的判别值")

	known := map[string]bool{}
	for _, v := range registered {
		known[v] = true
	}
	missed := make([]string, 0, len(scanned))
	for _, v := range scanned {
		if !known[v] {
			missed = append(missed, v)
		}
	}
	sort.Strings(missed)
	require.Equal(t, blockTypeCycleBound, dedupe(missed),
		"「本仓声明、但运行时枚举够不着」的块类型集合变了。\n"+
			"多出来的:多半是新块类型注册在了一个 blank import 不进来的包里 —— 优先把它\n"+
			"挪进 chat_svc/blocks(那里 import 得进来),挪不动再更新 blockTypeCycleBound;\n"+
			"少掉的:某个原本够不着的块类型现在够得着了,回去把 blockTypeCycleBound 那段\n"+
			"注释一起改掉。改完重新生成:\n\t%s", tsRegenCmd)
}

// TestTSGenCoversChatBlockTypes 完整性守卫:视图块类型词表。
//
// 这条守卫与前三条的机制又不同,原因值得写清楚:
//
//   - wire 结构 / EventKind 的守卫是「AST 读源码 vs 生成器里的手抄清单」二者相比 ——
//     漏登记一格就变红。
//   - 块类型的守卫要把进程跑起来问运行时注册表,因为源码里根本没有一张可读的表。
//   - 视图词表两样都不是:源码里有表(chat_block_type.go 的 const 块),但生成器
//     **够不着**它 —— 本文件是 wire 包的包内测试,而 chat_svc 传递依赖 wire,
//     import 它直接 import cycle(与 blockTypeCycleBound 同一条约束)。所以词表
//     只能由 AST 读出,生成器里连清单都没有,也就没有「漏登记」这种失败模式。
//
// 那这条守卫守的是什么?**构造点与词表的自洽**,一共三件事:
//
//  1. 每个 ChatBlock{…} 都显式给了 Type —— 漏了会发出 type:"" 的块;
//  2. 每个 Type 写的都是具名常量而不是裸字面量 —— 裸字面量对生成器不存在,
//     等于又开一处手抄;
//  3. 词表那个 const 块与构造点实际用到的常量集合**完全相等** —— 多一格是死词条
//     (产物里凭空多一个前端永远收不到的值),少一格根本进不来。
//
// 于是「Go 侧新增一个视图块类型」这条路上没有静默出口:写裸字面量 → 第 2 条红;
// 加常量并用上 → 词表变了 → TestGeneratedTSFresh 红;加了常量却没用 → 第 3 条红。
func TestTSGenCoversChatBlockTypes(t *testing.T) {
	consts, _ := parseChatBlockTypeConsts(t)
	require.NotEmpty(t, consts, "没从 %s 扫到任何无类型字符串常量,扫描逻辑本身坏了", chatSvcRel)

	// 第 1、2 条在扫描时就地 require;这里拿到的是干净的使用集。
	used := scanChatBlockTypeUses(t)
	require.NotEmpty(t, used, "没在 %s 扫到任何 %s{%s: …} 构造点,扫描逻辑本身坏了",
		chatSvcRel, chatBlockTypeName, chatBlockTypeField)

	vocab := chatBlockTypeVocabulary(t)
	declared := make([]string, 0, len(vocab))
	values := make([]string, 0, len(vocab))
	seen := map[string]string{}
	for _, c := range vocab {
		declared = append(declared, c.name)
		values = append(values, c.value)
		require.NotEmpty(t, c.value, "词表常量 %s 的值是空串,它会发出一个 type:\"\" 的块", c.name)
		prev, dup := seen[c.value]
		require.False(t, dup, "词表里 %s 与 %s 的值都是 %q,联合类型会出现重复分支;"+
			"同一个判别值只该有一个名字", prev, c.name, c.value)
		seen[c.value] = c.name
	}

	sortedUsed := append([]string(nil), used...)
	sort.Strings(sortedUsed)
	require.ElementsMatch(t, declared, dedupe(sortedUsed),
		"视图词表的 const 块与 %s{%s: …} 构造点实际用到的常量对不上。\n"+
			"多出来的:死词条 —— 产物里会凭空多一个前端永远收不到的值,删掉它或者把它用上;\n"+
			"少掉的:某个常量声明在了别的 const 块里 —— 整张表必须留在一个块里。\n"+
			"改完重新生成:\n\t%s", chatBlockTypeName, chatBlockTypeField, tsRegenCmd)

	// 扫描器坏掉(ChatBlock 改名、构造点换形态)会扫出空集或残集,而空集在上面
	// 每一条断言里都是"通过"。这条锚把那种静默缩水挡住。
	require.Subset(t, values, chatBlockTypeAnchors,
		"视图词表里少了必然存在的几格 %v,多半是扫描器本身坏了而不是词表真的变了", chatBlockTypeAnchors)
}

// dedupe 去重(输入需已排序)。
func dedupe(in []string) []string {
	out := make([]string, 0, len(in))
	for i, v := range in {
		if i == 0 || in[i-1] != v {
			out = append(out, v)
		}
	}
	return out
}

// TestGeneratedTSFresh 新鲜度守卫:已提交的 *.gen.ts 必须就是生成器此刻会写出的
// 东西。给 wire 结构加个字段却忘了重新生成,这里变红 —— 与黄金样本那条守卫
// (TestGoldenFixturesFresh)同一条纪律,只是守的是 TS 产物而不是 JSON 样本。
func TestGeneratedTSFresh(t *testing.T) {
	committed := filepath.Join(repoRoot(t), tsGenRel)
	fresh := t.TempDir()
	writeTSSources(t, fresh)

	require.Equal(t, listGenNames(t, fresh), listGenNames(t, committed),
		"%s 的产物文件集与生成器不一致,重新生成:\n\t%s", tsGenRel, tsRegenCmd)

	for _, s := range buildTSSources(t) {
		t.Run(s.name, func(t *testing.T) {
			// G304:路径由 repoRoot + 本文件里的常量目录 + 本文件里写死的文件名拼出,
			// 没有外部输入参与,且只在测试里读本仓已提交的产物。
			got, err := os.ReadFile(filepath.Join(committed, s.name)) //nolint:gosec // 见上
			require.NoError(t, err, "读已提交的产物 %s", s.name)
			if diff := firstLineDiff(s.content, string(got)); diff != "" {
				// 不用 require.Equal:产物上千行,整份 diff 会淹掉真正的信息。
				// 这里只报第一处差异 + 重新生成的命令。
				require.FailNow(t, "产物已过期",
					"%s 已过期(wire 改了但没重新生成)。\n%s\n重新生成:\n\t%s", s.name, diff, tsRegenCmd)
			}
		})
	}
}

// firstLineDiff 报出两份文本的第一处行级差异(带行号与上下文);相同则返空串。
func firstLineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		lw, lg := lineAt(w, i), lineAt(g, i)
		if lw == lg {
			continue
		}
		return fmt.Sprintf("第 %d 行起不一致:\n\t生成器会写出: %q\n\t已提交的是:   %q\n\t(共 %d 行 vs %d 行)",
			i+1, lw, lg, len(w), len(g))
	}
	return ""
}

func lineAt(lines []string, i int) string {
	if i < len(lines) {
		return lines[i]
	}
	return "<文件到此结束>"
}

// TestWriteTSCodec 重新生成 TS 产物,写进本仓的 agentre-wire 包。
// 写盘是显式动作:不带 WIRE_TS_WRITE=1 直接跳过,`go test ./...` 因此不写盘。
func TestWriteTSCodec(t *testing.T) {
	if os.Getenv("WIRE_TS_WRITE") != "1" {
		t.Skip("设 WIRE_TS_WRITE=1 重新生成 TS 产物")
	}
	dir := filepath.Join(repoRoot(t), tsGenRel)
	writeTSSources(t, dir)
	t.Logf("TS 产物已写入 %s", dir)
}
