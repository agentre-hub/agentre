// Package wire 定义 agentre ↔ agentred RPC 协议的参数 / 结果 / 通知帧。
// daemon handlers 和 client *remote.Runtime 共享同一组类型,避免两边手抄 Protobuf shape 时漂移。
//
// 命名约定:
//   - 与 agentruntime.Runtime + 子接口一一对应的方法都在 "runtime.*" 命名空间下。
//     不属于「跑一轮」的方法另开自己的命名空间(如 "skills.*" 是这台机器上装了
//     什么技能包,与任何一轮执行无关),免得 runtime.* 变成一个什么都往里塞的箩筐。
//   - 字段名一律 lowerCamelCase。
//   - 错误码 -32010..-32014 是 agentruntime 标准 sentinel 的稳定 wire 值;
//     ToRPCError / FromRPCError 双向翻译,让 errors.Is(err, agentruntime.ErrXxx)
//     在客户端继续工作。
package wire

import (
	"encoding/json"
	"errors"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
)

// ── RPC method names ────────────────────────────────────────────────────────

// Method 常量是 daemon registry.Register 与客户端 c.Call 的唯一来源。
const (
	MethodCapabilities         = "runtime.capabilities"
	MethodRun                  = "runtime.run"
	MethodSteer                = "runtime.steer"
	MethodCancelSteer          = "runtime.cancelSteer"
	MethodDrainPending         = "runtime.drainPending"
	MethodAbort                = "runtime.abort"
	MethodStopBackgroundTask   = "runtime.stopBackgroundTask"
	MethodSetPermissionMode    = "runtime.setPermissionMode"
	MethodSetModelTarget       = "runtime.setModelTarget"
	MethodSubmitAnswer         = "runtime.submitAnswer"
	MethodSubmitToolPermission = "runtime.submitToolPermission"
	MethodGetGoal              = "runtime.goal.get"
	MethodSetGoal              = "runtime.goal.set"
	MethodClearGoal            = "runtime.goal.clear"

	// 断连重连的补齐族。客户端重连后的三步是 list → attach → pull(→ pendingWaiters),
	// 每一步都限定在调用方自己的对端范围内(R16),对端身份取自那条连接的鉴权状态,
	// 不由参数携带 —— 参数里的对端标识等于让任何已配对设备点名读别人的会话。
	MethodSessionList           = "runtime.session.list"
	MethodSessionPull           = "runtime.session.pull"
	MethodSessionPendingWaiters = "runtime.session.pendingWaiters"
	// MethodSessionAttach 是**显式接管**:客户端声明「这条会话此后由我消费」,daemon
	// 受理后才把该会话的通知推送目标改到这条连接上。
	//
	// 它必须独立存在,不能并进 list / pull:list 只是看一眼有哪些会话(看一眼不该改变
	// 任何东西),pull 是只读补齐(它在补齐**完成前**就改推送目标才对,不然补齐期间的
	// 实时通知会只落库不推送)。今天 daemon 侧的认领是隐式的 —— 任何被受理的 runtime.*
	// 都会把流指向发起它的那条连接,哪怕那条连接根本不打算消费它。补齐族不走这条隐式
	// 路径,所以重连的客户端需要一个不含副作用的入口明说这件事。
	MethodSessionAttach = "runtime.session.attach"

	// MethodSessionDelete 删掉这一端上的那条会话:agentred 上是会话行与它的整段通知
	// 日志,桌面端上是**那台电脑自己那条对话本体**。两种端一视同仁地受理它 —— 会话
	// 在哪台机器上执行,删除就在哪台机器上生效。
	//
	MethodSessionDelete = "runtime.session.delete"

	// MethodSkillsCatalog 列出**这台机器上**某一档执行目标的技能目录:已装包(含全局
	// 启用态)并上 agentre 的推荐包,逐行标注这一档授权了没有。它替掉浏览器控制台里
	// 「手打 skill id」——浏览器此前没有任何办法知道那台机器上到底装了什么。
	//
	// 它**不在** runtime.* 下:技能装在机器上,与任何一轮执行无关,问它不需要、也不该
	// 需要一条会话。
	//
	// 授权集由**调用方随请求带上**(SkillCatalogParams.Authorized),而不是执行端自己去
	// 查:执行目标与它的技能授权(R15e「一档一块」)存在组织架构库里,agentred 上没有
	// 那个库 —— 让它猜等于让它拿别的档、或者干脆拿空授权来答。谁掌握那一档的授权谁
	// 就得说出来,这样「一档一块」在协议上就是显式的,不靠两边默契。
	MethodSkillsCatalog = "skills.catalog"

	// MethodProjectSetLocalPath / MethodProjectClearLocalPath 配置**这台机器上**某个
	// 项目的本机路径（规格 agentre-server 2026-08-21「桌面端的项目路径也能从 web 配」）。
	//
	// 它们同样**不在** runtime.* 下,理由与 skills.catalog 同一条:项目落在机器上,
	// 与任何一轮执行无关。
	//
	// **为什么必须由这台机器自己写**:桌面端的本机路径不参与同步,只按 30 秒内容指纹
	// 单向上报给 server(整份快照替换)。server 往那份快照里直写一行,这台机器下一次
	// 上报就把它冲掉——所以浏览器要改它,只能经中转喊到这里来。agentred 不同,它的
	// 路径是账号级同步对象,server 直写即可,那条路不经过这两个方法。
	//
	// 项目按**同步标识**指代,不按本地自增 id:后者是各端私有的,而载荷里不出现任何
	// 一端的本地 id 是同步协议本来就写死的边界(见 internal/pkg/syncwire 包注释)。
	MethodProjectSetLocalPath   = "project.setLocalPath"
	MethodProjectClearLocalPath = "project.clearLocalPath"

	// daemon → client 通知。
	NotifyEvent         = "runtime.event"
	NotifyRunResultDone = "runtime.runResultDone"

	// MethodMCPProxy 是 daemon → client 的反向请求(request/response):daemon 上的 CLI
	// 子进程访问内置工具 MCP(org/subagent/group/workflow)时,这些 /mcp/* handler 的真身
	// 在 desktop。daemon 把 CLI 打到本地的 HTTP 请求原样隧道回 desktop 执行,应答原路返回,
	// 修「remote 不支持内置工具(URL 是 desktop 的 127.0.0.1)」。鉴权靠 Header 里 desktop
	// 轮起手时签的 MCP token(随 RunRequest.MCPServers 下发),在 desktop 侧校验。
	MethodMCPProxy = "runtime.mcpProxy"

	// 自主续轮(AutonomousTurnSource):backend 自发跑的一轮,daemon 转发给 client。
	// 一轮 = Started → Event* → Done(同一 sessionID,串行,无重叠);Event 复用
	// EventFrame、Done 复用 RunResultDoneFrame,只是走各自的 notify 方法区分归属
	// (普通 Run 流 vs 自主续轮流),sessionID 仍负责会话路由。
	NotifyAutonomousTurnStarted = "runtime.autonomousTurn.started"
	NotifyAutonomousTurnEvent   = "runtime.autonomousTurn.event"
	NotifyAutonomousTurnDone    = "runtime.autonomousTurn.done"
)

// ── Error codes ─────────────────────────────────────────────────────────────

const (
	ErrCodeNoActiveTurn    = -32010
	ErrCodeSteerNotFound   = -32011
	ErrCodeUnsupported     = -32012
	ErrCodeAborted         = -32013
	ErrCodeSessionNotFound = -32014
	// ErrCodePeerExecutionUnavailable:会话钉住的执行目标(agentred)当前不可用。
	//
	// 与上面五个不同,它**不进 SentinelFromCode**:那张表翻的是 daemon 回给
	// remote 客户端的 agentruntime sentinel,而这一条是桌面端 peer 回给浏览器的,
	// 两条链路不同。放这里的唯一理由是让它跟着 wire 一起生成给 TS ——
	// 浏览器要靠它把「执行目标不可用」(停用写入 + 状态横幅)与普通拒绝区分开,
	// 此前是在 agentre-server 里手抄的一个魔数,改了这边不会有任何地方变红。
	//
	// 应答里同时带类型化 data(accepted / historyAvailable / executionUnavailable)。
	ErrCodePeerExecutionUnavailable = -32015

	// project.* 的三个码。段位刻意避开已经用掉的 -32030..-32035(remotefs)与
	// -32040..-32042(workspacefs):同一条连接上跑着好几个方法族,码段重叠会让
	// 客户端把别人的失败认成自己的。
	//
	// ErrCodeProjectNotSynced:这台机器上没有这个同步标识的项目。它与「写失败了」
	// **必须分得开**——项目可以先在 web 上建出来,那一刻目标机器可能还没拉到这一行,
	// 等一会儿就好;折进通用失败会让用户去查权限和磁盘。
	ErrCodeProjectNotSynced    = -32050
	ErrCodeProjectInvalidPath  = -32051
	ErrCodeProjectPathNotFound = -32052
)

// ToRPCError 把 agentruntime 的 sentinel 包成 *rpcerror.Error,daemon 端返回。
// 非 sentinel 错误返 nil,调用方应自己包装(ErrInternal 之类)。
func ToRPCError(err error) *rpcerror.Error {
	if code, ok := CodeForSentinel(err); ok {
		return &rpcerror.Error{Code: int32(code), Message: err.Error()}
	}
	return nil
}

// FromRPCError 反向把 *rpcerror.Error 翻成对应的 agentruntime sentinel。
// 未知 code 返原 err。
func FromRPCError(err error) error {
	var rpcErr *rpcerror.Error
	if !errors.As(err, &rpcErr) {
		return err
	}
	if sent := SentinelFromCode(int(rpcErr.Code)); sent != nil {
		return sent
	}
	return err
}

// SentinelFromCode 把 wire error code 直接翻成 agentruntime sentinel,无匹配
// 返 nil。客户端只拿到 (code, message) 二元组(走 RunResultDoneFrame 而非
// *rpcerror.Error)时调它,免去人工合成 *rpcerror.Error 再走 FromRPCError
// 的绕远路 —— 这也是 runtimes/remote 包能彻底不依赖 daemon/rpc 的关键。
func SentinelFromCode(code int) error {
	switch code {
	case ErrCodeNoActiveTurn:
		return agentruntime.ErrNoActiveTurn
	case ErrCodeSteerNotFound:
		return agentruntime.ErrSteerNotFound
	case ErrCodeUnsupported:
		return agentruntime.ErrUnsupported
	case ErrCodeAborted:
		return agentruntime.ErrAborted
	case ErrCodeSessionNotFound:
		return agentruntime.ErrSessionNotFound
	}
	return nil
}

// CodeForSentinel 把 agentruntime sentinel 翻成 wire error code;非 sentinel
// 返 (0, false)。ToRPCError 的核心,也方便 daemon 端调用方按需自己组帧。
func CodeForSentinel(err error) (int, bool) {
	switch {
	case errors.Is(err, agentruntime.ErrNoActiveTurn):
		return ErrCodeNoActiveTurn, true
	case errors.Is(err, agentruntime.ErrSteerNotFound):
		return ErrCodeSteerNotFound, true
	case errors.Is(err, agentruntime.ErrUnsupported):
		return ErrCodeUnsupported, true
	case errors.Is(err, agentruntime.ErrAborted):
		return ErrCodeAborted, true
	case errors.Is(err, agentruntime.ErrSessionNotFound):
		return ErrCodeSessionNotFound, true
	}
	return 0, false
}

// ── RPC types ───────────────────────────────────────────────────────────────

// CapLLMModelTargetV1 是 daemon 在 health.ping 里公布的能力位：本 daemon 支持
// 按 ModelKey 解析 fixed-model（决策 11）。桌面端据此在 Picker 里禁用不支持
// fixed-model 的旧 daemon 上的固定模型选项 —— 旧 daemon 即使收到 ModelKey 也会
// 静默按 provider-default 执行，正是规格禁止的降级，所以必须先查能力位再允许选择。
const CapLLMModelTargetV1 = "llm-model-target-v1"

// HasCapability 判断 capability 列表是否包含指定能力位。
func HasCapability(caps []string, name string) bool {
	for _, c := range caps {
		if c == name {
			return true
		}
	}
	return false
}

// ModelSummary describes a single model configured for a daemon provider.
// Non-sensitive：只含稳定 key / 实际 model id / 展示名 / 启用态，绝不含凭证。
type ModelSummary struct {
	Key     string `json:"key"`
	ModelID string `json:"modelId"`
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled"`
}

// ProviderSummary describes a single LLM provider configured in the daemon
// state. Returned by health.ping so desktop watcher can render sync status.
// 只含非敏感字段：Provider/Model 的稳定 key + 展示名 + 实际 model id；
// APIKey / BaseURL 永不进这条目录（凭证执行侧本地化）。
type ProviderSummary struct {
	Key             string         `json:"key"`
	Name            string         `json:"name"`
	Type            string         `json:"type"`
	DefaultModelKey string         `json:"defaultModelKey,omitempty"`
	Models          []ModelSummary `json:"models,omitempty"`
}

// OK 大部分 mutating 方法不需要返回值,统一返这个空 struct 表示成功。
// 是「成功无 payload」。
type OK struct{}

// PeerSessionControlResult reports that another endpoint won the race to answer
// a pending ask or tool permission. Older peers return an empty object, whose
// zero value preserves the original successful outcome.
type PeerSessionControlResult struct {
	AlreadyHandled bool `json:"alreadyHandled,omitempty"`
}

type GoalParams struct {
	ConversationID    string          `json:"conversationId"`
	PeerFingerprint   string          `json:"peerFingerprint,omitempty"`
	AgentID           int64           `json:"agentId,omitempty"`
	ProviderSessionID string          `json:"providerSessionId"`
	Backend           json.RawMessage `json:"backend,omitempty"`
	Cwd               string          `json:"cwd,omitempty"`
	Objective         *string         `json:"objective,omitempty"`
	Status            *string         `json:"status,omitempty"`
	TokenBudget       *int            `json:"tokenBudget,omitempty"`
	// LLMProviderKey / LLMModelKey 与 RunParams 同形（决策 11）：goal 与 turn 共用
	// 同一个 CLI 会话池，两边解析不一致会让启动期比对键反复翻转。daemon 按这组
	// key 从自家目录解析，wire 永不携带 APIKey / BaseURL / Provider 行正文。
	LLMProviderKey string `json:"llmProviderKey,omitempty"`
	LLMModelKey    string `json:"llmModelKey,omitempty"`
}

type GoalResult struct {
	Goal *agentruntime.Goal `json:"goal,omitempty"`
}

type GoalClearResult struct {
	Cleared bool `json:"cleared"`
}

// CapabilitiesParams 按 BackendType 查 daemon 端 runtime 的能力矩阵。
type CapabilitiesParams struct {
	BackendType string `json:"backendType"`
}

// CapabilitiesResult 直接透传 capability.Capabilities — 含 Set 映射 + PermissionModeMeta。
type CapabilitiesResult struct {
	Capabilities capability.Capabilities `json:"capabilities"`
}

// HistoryMessageWire 是 agentruntime.HistoryMessage 的 wire 镜像。blocks 字段
// 走 blocks.StoredBlock(已经是 discriminated envelope)。
type HistoryMessageWire struct {
	Role   string               `json:"role"`
	Blocks []blocks.StoredBlock `json:"blocks,omitempty"`
}

// RunParams 是 runtime.run 的请求体。镜像 agentruntime.RunRequest 跨进程需要的
// 字段子集。Backend 用 json.RawMessage 透传,避免 wire 层硬依赖 entity 内部结构。
//
// 故意没有 Provider / GatewayURL / GatewayToken:
//   - Provider 含明文 APIKey,desktop 不该每个 turn 越线漂移到远端 daemon;
//   - GatewayURL/Token 来自 desktop 本机 127.0.0.1,在 daemon 主机上根本拨不到。
//
// daemon 端 handlers/runtime.go 在 Run 入口处自己用 ProviderLookup + 自家
// Gateway 解出这三者,desktop 端 chat_svc.runTurn 检 be.IsRemote() 后也不再填。
type RunParams struct {
	Backend        json.RawMessage `json:"backend"`
	AgentID        int64           `json:"agentId"`
	ConversationID string          `json:"conversationId"`
	// PeerFingerprint 点名这一轮要落在**哪个对端**名下的那条会话上(R9)。会话键是
	// (发起端指纹, 会话 id),而清单(SessionSummary.PeerFingerprint)是客户端学 origin
	// 的唯一来源;省略 = 调用方自己的对端,与控制族(attach / pull / abort / submit)
	// 同一条 ResolveSessionPeer 约定 —— 点名别人的 origin 是账号级能力,配对身份点名
	// 一律被拒。不点名地给别人的会话开新一轮,会在调用方名下另建一条同号会话:上下文
	// (决策 8 的 provider_session_id)续不上,事件也落到另一个 journal 分区,发起端与
	// 它的其余订阅者一条都收不到(R6 / R18 的前提)。
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	Cwd             string `json:"cwd,omitempty"`
	// Title 是该会话此刻的标题(R7)。桌面端每轮携带当前值,daemon 幂等覆盖;调用方
	// 不带这一格时保持空串。改名后最多滞后一轮生效。
	Title string `json:"title,omitempty"`
	// AgentSyncID 是该会话所属 Agent 的账号级同步标识(块 1,决策 3 的 ULID,不是本地
	// 自增 agent_id)。会话列表按它解析 Agent 名与头像(R5)。
	AgentSyncID string `json:"agentSyncId,omitempty"`
	// ProjectSyncID 是该会话所属项目的账号级同步标识。它与 AgentSyncID 同批携带、
	// 由 daemon 幂等覆盖。
	//
	// 之所以要发起端报而不是让服务端从 cwd 推:日活跃统计按项目分组,而那条通道只
	// 上行计数、不上行任何路径 —— 推不出来。空串 = 这一轮不属于任何项目(或调用方
	// 是老版本),不是「未知待推导」。
	ProjectSyncID     string `json:"projectSyncId,omitempty"`
	SystemPrompt      string `json:"systemPrompt,omitempty"`
	ProviderSessionID string `json:"providerSessionId,omitempty"`
	// FreshSession 声明这一轮**必须起全新会话**:即使 daemon 在自家落库里存了这条会话
	// 的 provider_session_id,也不许续(挂账修复,2026-08-11)。决策 8 之后「空
	// ProviderSessionID」的语义被重载成「用落库那份续话」,而 regenerate 与 provider 会话
	// 失效恢复这两条路径的空字段本意是「全新」—— 两者在 wire 上不可区分,daemon 拿旧 id
	// 顶掉:regenerate 退化成续旧上下文、gone 恢复永远撞同一个失效 id。本字段是那个
	// 「全新」意图的显式出口。三种取值:ProviderSessionID 非空 = resume;空 +
	// FreshSession=false(缺省)= 决策 8 续话(daemon 续落库那份);空 + FreshSession=true
	// = 全新,忽略落库。浏览器续话不置它;桌面端在本地 sess.ProviderSessionID 为空时置它。
	FreshSession      bool                 `json:"freshSession,omitempty"`
	UserText          string               `json:"userText,omitempty"`
	UserBlocks        []blocks.StoredBlock `json:"userBlocks,omitempty"`
	History           []HistoryMessageWire `json:"history,omitempty"`
	Compact           bool                 `json:"compact,omitempty"`
	ForkAnchor        string               `json:"forkAnchor,omitempty"`
	PermissionMode    string               `json:"permissionMode,omitempty"`
	CollaborationMode string               `json:"collaborationMode,omitempty"`
	// MCPServers 注入给 runtime 的 MCP tool server（org/subagent/hook 工具等）。漏传会让
	// 远程后端的 launch-time MCP 注入失效，故必须随 wire 过线。
	MCPServers []agentruntime.MCPServerSpec `json:"mcpServers,omitempty"`
	// EnabledPlugins 注入给 runtime 的 per-agent plugin/skill-pack 覆盖。漏传会让
	// 远程 CapSkills 后端展示可配置但实际继承全局配置。
	EnabledPlugins map[string]bool `json:"enabledPlugins,omitempty"`
	// LLMProviderKey 是 desktop 端关联的 provider stable key（UUID）。
	// daemon 用它做 ProviderLookup（FindByKey），不需要 desktop 越线传 APIKey。
	// 决策 9 后它携带 effectiveProviderKey（会话 provider_key 优先），daemon 自解。
	LLMProviderKey string `json:"llmProviderKey,omitempty"`
	// LLMModelKey 是执行侧目标的稳定 ModelKey（决策 11）：空 = provider-default
	// （daemon 解析该 Provider 当前默认模型），非空 = fixed-model（daemon 精确解析
	// 该模型，缺失/停用/旧 daemon 一律严格拒绝，绝不静默降级为默认模型）。
	LLMModelKey string `json:"llmModelKey,omitempty"`
	// SourceDevice / SourceDeviceName 是「开新一轮」发起方的设备身份（R18/R19）。
	// 浏览器**每轮**随 runtime.run 声明自己的设备指纹与显示名（如「Chrome · macOS」）：
	// 握手（auth.account）只带指纹、不带显示名，而 R19 要的是「人能认出的名字」，所以
	// 名字走这里而不是握手。daemon 据此在事件流开头注入一条 user_message 标记，扇出给
	// 同一条会话的其余订阅者，让桌面端把这一轮落成一行带来源标识的用户消息。桌面端自己
	// 发消息不传这两个字段 → 不注入、消息不带来源标识，单端界面零变化（R17 既有承诺不变）。
	SourceDevice     string `json:"sourceDevice,omitempty"`
	SourceDeviceName string `json:"sourceDeviceName,omitempty"`
}

// MCPProxyRequest 是 daemon→desktop 隧道里一次 MCP HTTP 请求的封装。daemon 把 CLI 子进程
// 打到 daemon 本地 /mcp/* 的请求原样装包发回 desktop;desktop 用本机真 gateway 重放。
// MCP-over-HTTP 是纯请求/应答(server→client SSE 被 handler 405 掉),所以单帧封装即可。
type MCPProxyRequest struct {
	Path    string              `json:"path"`              // 如 /mcp/org/
	Method  string              `json:"method"`            // HTTP 方法(POST/GET/...)
	Headers map[string][]string `json:"headers,omitempty"` // 含 Authorization: Bearer <token>
	Body    []byte              `json:"body,omitempty"`    // MCP JSON-RPC HTTP body
}

// MCPProxyResponse 是 desktop 重放 MCPProxyRequest 后的 HTTP 应答封装。
type MCPProxyResponse struct {
	Status  int                 `json:"status"`
	Headers map[string][]string `json:"headers,omitempty"`
	Body    []byte              `json:"body,omitempty"`
}

// RunAck 是 runtime.run 的同步返回,只回 echo 客户端传的 sessionID 供它确认。
// 真实 events 走 NotifyEvent 异步推;终态 RunResult 走 NotifyRunResultDone。
//
// ProviderSessionID 是 runtime 在事件流开始前已经确认的 provider 原生
// Session 身份（当前由 Pi Agent 使用），让 desktop 可在流结束前持久化。
//
// LaunchPermissionMode 是 daemon 端 runtime spawn CLI 子进程实际下发的
// --permission-mode 值(claudecode 专用)。同步随 ack 回来,客户端立即写入
// RunResult.LaunchPermissionMode,让 chat_svc 在主进程侧持久化到
// session.PermissionModeAtLaunch。空串 = runtime 未指定(其它 backend / 复
// 用现有 CLI 进程)。
//
// ProviderFallbackKey 是决策 9 的回退信号:wire 带 effectiveProviderKey(会话
// provider_key 优先),daemon 自解时该 key 缺失/非 active → 回退 agent 绑定(或 CLI
// 登录态)执行,并把被回退的 key 放进此字段回传。非空时桌面端据此追加一条持久
// notice(与本地 Q3 一致);空串 = 未回退。
type RunAck struct {
	ConversationID       string `json:"conversationId"`
	ProviderSessionID    string `json:"providerSessionId,omitempty"`
	LaunchPermissionMode string `json:"launchPermissionMode,omitempty"`
	ProviderFallbackKey  string `json:"providerFallbackKey,omitempty"`
}

// SteerParams 等同 agentruntime.Steerer.Steer 的入参。
type SteerParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	QueuedID        string `json:"queuedId,omitempty"`
	Text            string `json:"text"`
}

// CancelSteerParams 等同 agentruntime.SteerCanceler.CancelSteer 的入参。
type CancelSteerParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	QueuedID        string `json:"queuedId,omitempty"`
}

// CancelSteerResult 返已撤销的 queuedID 列表(空 queuedID 表示「清空所有未消费」,
// daemon 据此返若干 id)。
type CancelSteerResult struct {
	Removed []string `json:"removed,omitempty"`
}

// DrainParams 等同 agentruntime.SteerDrainer.DrainPending 的入参。
type DrainParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
}

// DrainResult 返本轮 daemon 已 ack 但 hook 没拉走的 mid-turn steer 列表,
// chat_svc 拿来 emit StreamSteerConsumed + persistAutoContinueTurn。
type DrainResult struct {
	Steers []agentruntime.ConsumedSteer `json:"steers,omitempty"`
}

// AbortParams 等同 agentruntime.Aborter.Abort 的入参。
// TurnToken 语义同 agentruntime:0 = 中断当前活跃轮;非 0 = 仅当该轮仍是当前活跃轮才中断。
type AbortParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	TurnToken       uint64 `json:"turnToken,omitempty"`
}

// AbortResult 是 MethodAbort 的应答,携带被中断轮的类型(agentruntime.AbortOutcome.TurnKind)。
type AbortResult struct {
	TurnKind agentruntime.TurnKind `json:"turnKind"`
}

// StopBackgroundTaskParams 等同 agentruntime.BackgroundTaskStopper.StopBackgroundTask 的入参。
type StopBackgroundTaskParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	TaskID          string `json:"taskId"`
}

// SetPermissionModeParams 等同 agentruntime.PermissionModeSetter.SetPermissionMode 的入参。
type SetPermissionModeParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	Mode            string `json:"mode"`
}

// SetModelTargetParams 改这条会话钉的 LLM ModelTarget,语义等同桌面端的
// chat_svc.SetChatSessionModelTarget:
//   - ProviderKey 空 + ModelKey 空 = 改回「跟随 Agent 绑定」(CLI 后端即回到自身
//     登录态)。这是一个**要写下去的值**,不是「不改」—— 用户从固定模型改回跟随
//     绑定时不清空,就等于这次改动没发生;
//   - ProviderKey 非空 + ModelKey 空 = 该供应商当前的默认模型;
//   - 两者都非空 = 固定模型。
//
// 新目标自**下一轮**生效,正在跑的那一轮不受影响。会话不存在时报错而不是折成
// 成功:那会让调用方以为下一轮会用新模型,而实际上一行都没写。
type SetModelTargetParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	ProviderKey     string `json:"providerKey,omitempty"`
	ModelKey        string `json:"modelKey,omitempty"`
}

// SubmitAnswerParams 等同 agentruntime.AskAnswerSink.SubmitAnswer 的入参。
type SubmitAnswerParams struct {
	ConversationID  string                     `json:"conversationId"`
	PeerFingerprint string                     `json:"peerFingerprint,omitempty"`
	RequestID       string                     `json:"requestId"`
	Questions       []agentruntime.AskQuestion `json:"questions,omitempty"`
	Answers         []agentruntime.AskAnswer   `json:"answers,omitempty"`
	Skipped         bool                       `json:"skipped,omitempty"`
}

// SubmitToolPermissionParams 等同 agentruntime.ToolPermissionSink.SubmitToolPermission 的入参。
type SubmitToolPermissionParams struct {
	ConversationID     string `json:"conversationId"`
	PeerFingerprint    string `json:"peerFingerprint,omitempty"`
	RequestID          string `json:"requestId"`
	Allow              bool   `json:"allow"`
	AlwaysAllowSession bool   `json:"alwaysAllowSession,omitempty"`
	DenyReason         string `json:"denyReason,omitempty"`
}

// ── 断连重连的补齐族 ────────────────────────────────────────────────────────

// 会话在 daemon 上的生命周期取值:running(一轮执行中)→ idle(轮结束,等下一轮)
// → 可再次 running;任一状态遇 daemon 重启 → interrupted。
//
// interrupted 是这条链的终点:那一轮的子进程随上一个 daemon 进程消亡了,会话的历史
// 仍可拉取,但接不回实时流(MethodSessionAttach 拒绝它),对它提交决策按 R8 返回成功
// 且无副作用。
//
// 「正在等待输入」**不在**这条链上:它是 running 之上的一层实时叠加,由 daemon 在
// 应答时现算(SessionSummary.WaitingForInput),永不落库 —— 落库的等待标志会活过
// daemon 重启,变成一个没人能回答的问题(R11)。
const (
	SessionLifecycleRunning     = "running"
	SessionLifecycleIdle        = "idle"
	SessionLifecycleInterrupted = "interrupted"
)

// 单次增量拉取的条数:客户端不指定时用 Default,指定值超过 Max 时按 Max 截断。上限
// 是硬的 —— 一条跑了很久的会话日志有几万行,一次全塞进一帧 WS 会把连接顶爆,而客户端
// 靠 SessionPullResult.HasMore 翻页本来就能拉平。
const (
	DefaultSessionPullLimit = 200
	MaxSessionPullLimit     = 1000
)

// SessionSummary 是会话清单里的一条:标识 + 生命周期状态 + 是否正在等待输入 + 最新 seq。
//
// LatestSeq 取自 daemon 通知日志里该会话的 MAX(seq)(唯一真相源),客户端拿它与自己
// 存的游标一比就知道断连期间落下了多少条。
type SessionSummary struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	AgentID         int64  `json:"agentId,omitempty"`
	// Title / AgentSyncID / ProviderSessionID 是 R7 + 决策 8 的新列:会话标题、所属
	// Agent 的账号级同步标识、以及续话要用的 provider 原生会话身份。三者每轮由调用
	// 方携带、幂等覆盖,所以还没跑过第一轮的会话这几格就是空的(标题由首条消息派生)。
	// 缺这些字段时如实留空(空串,不猜、不填占位名)。
	Title             string `json:"title,omitempty"`
	AgentSyncID       string `json:"agentSyncId,omitempty"`
	ProviderSessionID string `json:"providerSessionId,omitempty"`
	Cwd               string `json:"cwd,omitempty"`
	// ProjectSyncID 是这条会话所属项目的**账号级同步标识**,由**桌面端**交出。
	//
	// 这一维在两种执行端上不是同一件事:agentred 的会话有一个落库的 cwd,账号那边
	// 拿 (指纹, cwd) 去比它给每台机器配的项目路径就判得出归属;桌面端没有「这条会话
	// 的 cwd」这种东西 —— 工作目录是每轮按项目本机路径现算的 —— 而且它的本机路径
	// 不流动、只存在账号的上报组里,压根不在那份名单中。两头都对不上,于是桌面端的
	// 每一条对话在账号侧都只能落进「随手对话」。真正流动的事实是项目同步标识本身,
	// 所以它自己说出来。
	//
	// 交出的是同步标识而不是本地自增主键:那是账号里跨机通用的那个名字。项目还没
	// 认领同步标识时(未登录期间建的行,R12a 之前)如实留空 —— 拿本地主键凑一个,
	// 账号那边会照它建出一个永远配不上真项目的组。自由会话同样留空。
	ProjectSyncID   string `json:"projectSyncId,omitempty"`
	BackendType     string `json:"backendType,omitempty"`
	LifecycleState  string `json:"lifecycleState"`
	WaitingForInput bool   `json:"waitingForInput,omitempty"`
	LatestSeq       int64  `json:"latestSeq"`
	// LastMessageAt 是这条会话最后一次活动的时刻(Unix 毫秒),取自 daemon_sessions 的
	// last_message_at —— 每轮起手幂等覆盖时一并推进。会话清单要显示「最后活动时间」
	// (R5),而它的唯一真相源在执行端这台机器上。还没记过活动时间的会话报 0,由
	// 客户端如实表达为「未知」而不是猜一个时刻。
	LastMessageAt int64 `json:"lastMessageAt,omitempty"`
	// ProviderKey / ModelKey 是这条会话**自己**钉的 LLM ModelTarget,与桌面端
	// chat_sessions.provider_key / model_key 逐字同义(chat_entity/session.go):
	//   - 两者皆空 = 跟随 Agent 绑定(inherit-agent),每轮从 agent 的后端绑定解析;
	//   - ProviderKey 非空 + ModelKey 空 = 该供应商当前的默认模型;
	//   - 两者都非空 = 固定模型。
	//
	// 空**是一个有含义的取值**:它表示这条对话跟随 Agent 绑定。
	ProviderKey string `json:"providerKey,omitempty"`
	ModelKey    string `json:"modelKey,omitempty"`
}

// SessionListResult 是 MethodSessionList 的应答:这台 daemon 上的会话。调用方自己的
// 对端永远在范围内;daemon 已认领账号时 ListAll 会把全部对端的会话一并列出(账号可见
// 性,见 handlers/session_catchup.go 的 List),范围不再只有「调用这条连接的对端」。
type SessionListResult struct {
	Sessions []SessionSummary `json:"sessions"`
}

// SessionPullParams 是 MethodSessionPull 的请求:给定会话与起始游标,取其后的通知。
// Cursor 是**已经收到的**最后一个 seq(独占),所以首次补齐传 0。
type SessionPullParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	Cursor          int64  `json:"cursor"`
	Limit           int    `json:"limit,omitempty"`
}

// JournaledNotification 是日志里的一行:那条本该发出的通知的原样 (method, params)。
//
// Params **不含 seq** —— 落库时 seq 还没盖上去,它是日志行自己的列。补齐的客户端必须
// 按 method 把 Params 解成对应的帧、把这里的 Seq 盖上去、再喂进与实时同一套 handler,
// 否则每一帧都解出 seq=0,会被「不大于游标就丢弃」的规则整段吞掉(R6)。
// Params 装的是**帧本身**(EventFrame / RunResultDoneFrame /
// AutonomousTurnStartedFrame 之一),不是它的 JSON 字节。这一页补齐从头到尾在
// Protobuf 与密封值之间走,中间摆一个 json.RawMessage 只会让每一行在服务端与
// 客户端各自多走一轮 marshal→unmarshal —— 而日志行本身早就是 Protobuf 字节
// (见 protowire.EncodeNotification),那次 JSON 连存储格式都不是。
//
// json tag 在这里不驱动序列化(下面的 MarshalJSON / UnmarshalJSON 才是),但必须
// 与 journaledNotificationWire 一字不差:TS 编解码生成器读的是 tag,读不到自定义
// marshaler。TestJournaledNotificationWireTagsMatchMarshaler 守住这一致性。
type JournaledNotification struct {
	Seq    int64  `json:"seq"`
	Method string `json:"method"`
	Params any    `json:"params"`
	// Createtime 是这一帧在**原点**发生的时刻(Unix 毫秒),取自日志行自己的列。
	// 0 = 那一端还没升级到会报它,读者据此退回自己的收帧时刻,而不是把 0 当 1970。
	//
	// **没有 omitempty**:这一族结构的 json tag 就是 TS 编解码生成器读的那份契约
	// (TestJournaledNotificationWireTagsMatchMarshaler 守着「tag 列表 == 实际发出的
	// 键」)。省掉零值会让「报了 0」与「这一版根本没有这个字段」在线上长得一模一样。
	Createtime int64 `json:"createtime"`
}

// journaledNotificationWire 是它真正的线上形态。Params 按 Method 决定解成哪个帧,
// 所以两个方向都得自己接管。
type journaledNotificationWire struct {
	Seq        int64           `json:"seq"`
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params"`
	Createtime int64           `json:"createtime"`
}

func (n JournaledNotification) MarshalJSON() ([]byte, error) {
	raw := json.RawMessage("null")
	if n.Params != nil {
		encoded, err := json.Marshal(n.Params)
		if err != nil {
			return nil, err
		}
		raw = encoded
	}
	return json.Marshal(journaledNotificationWire{Seq: n.Seq, Method: n.Method, Params: raw, Createtime: n.Createtime})
}

func (n *JournaledNotification) UnmarshalJSON(data []byte) error {
	var w journaledNotificationWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	n.Seq = w.Seq
	n.Method = w.Method
	n.Createtime = w.Createtime
	n.Params = nil
	if len(w.Params) == 0 || string(w.Params) == "null" {
		return nil
	}
	frame, err := DecodeNotificationParams(w.Method, w.Params)
	if err != nil {
		return err
	}
	n.Params = frame
	return nil
}

// DecodeNotificationParams 按 method 把一段 params 解成对应的帧。
//
// 认不出的 method **不是错误**:新版 daemon 可能加了第六类通知,而整段补齐不该
// 因此失败(补齐停在这里会让它后面每一条已知通知也一起丢掉)。这种情况返回 nil
// 帧,由调用方按「跳过这一格游标」处理。
func DecodeNotificationParams(method string, params json.RawMessage) (any, error) {
	switch method {
	case NotifyEvent, NotifyAutonomousTurnEvent:
		frame := &EventFrame{}
		if err := json.Unmarshal(params, frame); err != nil {
			return nil, err
		}
		return frame, nil
	case NotifyRunResultDone, NotifyAutonomousTurnDone:
		frame := &RunResultDoneFrame{}
		if err := json.Unmarshal(params, frame); err != nil {
			return nil, err
		}
		return frame, nil
	case NotifyAutonomousTurnStarted:
		frame := &AutonomousTurnStartedFrame{}
		if err := json.Unmarshal(params, frame); err != nil {
			return nil, err
		}
		return frame, nil
	default:
		return nil, nil
	}
}

// SessionPullResult 是一页补齐:按 seq 升序的通知、翻页用的新游标、以及是否还有更多。
// Cursor 在空页上**保持不变**(不回退到 0),否则客户端会把整段日志重放一遍。
//
// OldestSeq 是该会话此刻**现存最老的那一行**的 seq(一条日志都没有时为 0)。它存在的
// 唯一理由是日志的老前缀可能不在了 —— agentred 自己已经不回收(规格 2026-08-18
// 决策 8),但库可能被从外部恢复或截断:
// 客户端的游标会落在那段已经不存在的区间里,补洞拉取因此
// 永远拉不到 游标+1 那一条 —— 每一页的第一条都被判成跳号丢弃,游标原地不动,此后连
// 实时通知也全被当成跳号,会话没有错误、没有跳号地冻住(与 8496c291 修的越界冻结同类)。
// 客户端据它把游标复位到 OldestSeq-1(那截尾巴是真的没有了),照 dropCursorAboveHighWater
// 的样子留一条 Warn,然后从现存最老的一行接着补。
type SessionPullResult struct {
	Notifications []JournaledNotification `json:"notifications,omitempty"`
	Cursor        int64                   `json:"cursor"`
	HasMore       bool                    `json:"hasMore"`
	OldestSeq     int64                   `json:"oldestSeq,omitempty"`
}

// SessionPendingWaitersParams 是 MethodSessionPendingWaiters 的请求。
type SessionPendingWaitersParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
}

// SessionPendingWaitersResult 是某会话此刻仍在阻塞的全部待决策,载荷足以重建审批 /
// 提问卡片。两个列表都可能为空:未实现审批协议的 backend、以及不属于调用方的会话,
// 都回空列表而不是报错(R7)。
type SessionPendingWaitersResult struct {
	ToolPermissions  []agentruntime.PendingToolPermission  `json:"toolPermissions,omitempty"`
	AskUserQuestions []agentruntime.PendingAskUserQuestion `json:"askUserQuestions,omitempty"`
}

// SessionAttachParams 是 MethodSessionAttach 的请求。
type SessionAttachParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
}

// SessionAttachResult 交回客户端接着补齐需要的东西:会话此刻的生命周期状态、backend
// 类型,以及此刻的最新 seq(高水位)。
//
// 接管成功后该会话的实时通知就推给这条连接;客户端随后按自己的游标 pull 到拉平即可,
// 接管与读高水位之间落库的那几条会在同一轮 pull 里被带出来。
type SessionAttachResult struct {
	ConversationID string `json:"conversationId"`
	BackendType    string `json:"backendType,omitempty"`
	LifecycleState string `json:"lifecycleState"`
	LatestSeq      int64  `json:"latestSeq"`
}

// SessionDeleteParams 是 MethodSessionDelete 的请求。PeerFingerprint 的语义与补齐族
// 完全一致:省略 = 调用方自己的对端,点名别人是账号级能力(见 handlers.ResolveSessionPeer)。
// 这是本 wire 上第一个破坏性方法,越界的代价不再是「读到了不该读的」而是「删掉了
// 别人的对话」,所以它绝不能自成一套宽松的范围规则。
type SessionDeleteParams struct {
	ConversationID  string `json:"conversationId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
}

// SessionDeleteResult 交回删除的**后置条件**:应答返回时,这一端已经没有这条会话了。
//
// 它有意不是「删了几行」:删除必须幂等 —— server 那份先删、执行端离线时留一条待办,
// 待办会重放,而且上一次可能删到一半(会话行没了、日志还剩着)。已经不在的会话回
// Deleted=false 会让调用方把它当成没删干净并永远重放下去,回错误更糟。两种端存的
// 东西也不一样(agentred 是会话行 + 日志,桌面端是 chat_sessions 与它的消息),
// 只有后置条件才是两边都答得准的同一件事。
type SessionDeleteResult struct {
	Deleted bool `json:"deleted"`
}

// ── 技能目录 ────────────────────────────────────────────────────────────────

// 发现的结果判别值(SkillCatalogResult.Discovery)。**空目录必须自带理由**:
// 「这台机器上真的一个包都没有」与「压根没问出来」对用户是两回事 —— 前者该请他去
// 装包,后者该告诉他这台机器现在答不了、已授权的仍然可以移除。此前 desktop 侧的
// 远端发现器把拨号失败软降级成空列表(agent_backend_svc.RemoteSkillDiscoverer),
// 界面上因此看不出区别;这条 wire 不重复那个错误。
const (
	// SkillDiscoveryOK 目录是问出来的,可以照它增删。
	SkillDiscoveryOK = "ok"
	// SkillDiscoveryUnavailable 这台机器此刻答不出:CLI 找不到、枚举失败。
	// 目录为空**不代表**没有包,调用方不得据此认为可添加集是空的。
	SkillDiscoveryUnavailable = "unavailable"
	// SkillDiscoveryUnsupported 这个 backend 类型没有技能这一说(builtin / piagent /
	// openclaw 都不声明 CapSkills)。与 unavailable 不同,它是**稳定**的答案:再问一次、
	// 等机器空闲了再问,结果都一样。
	SkillDiscoveryUnsupported = "unsupported"
)

// SkillAuthorization 是这一档执行目标上的一条技能授权(桌面端 agent_exec_targets
// 那一行的 skills_json 里的一项,字段名逐字相同,好让调用方原样搬运)。
type SkillAuthorization struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// SkillCatalogParams 是 MethodSkillsCatalog 的请求。
//
// 请求里没有 agentId / execTargetId:执行端上没有组织架构库,那两个号码在它这里
// 什么都指不到。要答的那一档由**调用方**限定 —— 它连的这台机器 + 它带上来的这份
// 授权集,合起来就是「一档」。
type SkillCatalogParams struct {
	// BackendType 决定用哪个发现器、以及推荐包那半边取哪一张表。
	BackendType string `json:"backendType"`
	// Authorized 是这一档已经授权的包(可为空 = 一个都没授权)。它只用来给目录的每
	// 一行盖上 Enabled,不会被写到任何地方 —— 执行端不持有授权,只是照着标注。
	Authorized []SkillAuthorization `json:"authorized,omitempty"`
	// CLIPath 一般留空,由执行端自己解析本机 CLI 路径(调用方不知道对面的 claude 在哪)。
	CLIPath string `json:"cliPath,omitempty"`
}

// SkillPackSummary 是目录里的一行 —— 恰好是画一行要读的那几格(桌面端
// skillPacksToCatalog → CapabilityPicker 的 CatalogItem)。
//
// 它刻意**不是** skill_svc.SkillPackDTO 的照搬:source / recommended /
// effectiveEnabled 都是桌面端内部口径,浏览器一格也没读,搬过来只会变成两份要同步
// 的真相。
type SkillPackSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Description 是包的一句话说明。
	Description string `json:"description,omitempty"`
	// Skills 是包内的 skill 名 —— 界面用它给出条数、展开时列出内容。
	Skills []string `json:"skills,omitempty"`
	// Installed 这台机器上装了没有。没装的行只能看不能授权(要先去装),这是分组
	// 「可安装 / 可启用 / 已继承」的第一根轴。
	Installed bool `json:"installed,omitempty"`
	// Enabled 这一档显式授权了没有(= 请求里 Authorized 带的那份)。
	Enabled bool `json:"enabled,omitempty"`
	// GloballyEnabled CLI 全局启用态(claude plugin list --json 的 enabled)。三态
	// 「继承全局 / 强制开 / 强制关」里的「继承」指的就是它。
	GloballyEnabled bool `json:"globallyEnabled,omitempty"`
}

// SkillCatalogResult 是 MethodSkillsCatalog 的应答。
//
// Discovery **没有 omitempty**:它必须每次都在字节流里。可选字段缺席时解出零值,
// 而这里的零值是空串 —— 调用方就得替它猜一个含义,猜错的方向恰恰是最危险的那个
// (把「问不出来」当成「没有包」)。
type SkillCatalogResult struct {
	Packs     []SkillPackSummary `json:"packs"`
	Discovery string             `json:"discovery"`
}

// ── Notification frames ─────────────────────────────────────────────────────

// Seq 字段的共同约定(EventFrame / RunResultDoneFrame / AutonomousTurnStartedFrame):
// 它是这条通知在 daemon 通知日志里的序号,同一会话内从 1 起单调递增、无洞。daemon 先
// 落库拿到 seq 再推送,所以每条推出去的帧都带着它(R6);客户端据此判断跳号并按游标补齐。
//
// 它在 wire 上是 omitempty 的:不带日志序号的帧就不带这一格。日志里的序号从 1 起,
// 所以收到 seq 为 0 读作「这条帧没有序号」,而不是「序号是 0」。
//
// 日志里存的 payload 是**不含 seq** 的帧原样 —— seq 是日志行自己的列,实时推送与重连
// 补齐都在发送时才把行上的 seq 盖到帧上,两条路径因此投递同一份字节 + 同一个 seq。

// EventFrame wraps a single agentruntime.Event for delivery over NotifyEvent.
// ConversationID is transport metadata so the receiving end can route by
// conversation.
//
// Event 是**密封事件本身**,不是它的 JSON 字节。这条帧在进程内只被 protowire 读,
// 而 protowire 要的就是 Event —— 中间摆一个 json.RawMessage 的后果是每帧在两端
// 各自多走一轮 Event → JSON → Event(生产者 marshal、协议层 unmarshal 再 marshal
// 成 proto,接收端反过来再来一遍),而这条链路上根本没有第二种载荷形态需要它当
// 通用容器。
//
// 线上形态一个字节都没变:下面的 MarshalJSON / UnmarshalJSON 仍旧落
// {"conversationId":…,"event":{"kind":…},"seq":…},由各 Event 自己的 MarshalJSON 与
// agentruntime.UnmarshalEvent 负责 —— 通知日志里的旧行、旧版本对端、黄金样本
// 都照常读得出来。
// json tag 在这里**不驱动序列化**(下面的 MarshalJSON / UnmarshalJSON 才是),
// 但必须与 eventFrameWire 一字不差:TS 编解码生成器读的是 tag,读不到自定义
// marshaler。两处一旦分家,生成出来的 decodeEventFrame 会去找 `ConversationID` 这样
// 根本不存在的键 —— 编译期无声,浏览器侧全线解码失败。
// TestEventFrameWireTagsMatchMarshaler 守住这一致性。
type EventFrame struct {
	ConversationID string             `json:"conversationId"`
	Event          agentruntime.Event `json:"event"`
	Seq            int64              `json:"seq,omitempty"`
}

// eventFrameWire 是 EventFrame 真正的线上形态。单独一个类型而不是直接用上面那组
// tag:Event 是 interface,encoding/json 解不进去,两个方向都得自己接管。
type eventFrameWire struct {
	ConversationID string          `json:"conversationId"`
	Event          json.RawMessage `json:"event"`
	Seq            int64           `json:"seq,omitempty"`
}

func (f EventFrame) MarshalJSON() ([]byte, error) {
	// 空事件也要落成 "event":null 而不是省略 —— 老形态里 Event 是必填字段,
	// 省掉它会让旧解码方读到一个没有 event 的帧。
	raw := json.RawMessage("null")
	if f.Event != nil {
		encoded, err := json.Marshal(f.Event)
		if err != nil {
			return nil, err
		}
		raw = encoded
	}
	return json.Marshal(eventFrameWire{ConversationID: f.ConversationID, Event: raw, Seq: f.Seq})
}

func (f *EventFrame) UnmarshalJSON(data []byte) error {
	var w eventFrameWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	f.ConversationID = w.ConversationID
	f.Seq = w.Seq
	f.Event = nil
	if len(w.Event) == 0 || string(w.Event) == "null" {
		return nil
	}
	event, err := agentruntime.UnmarshalEvent(w.Event)
	if err != nil {
		return err
	}
	f.Event = event
	return nil
}

// SetSeq 盖上该帧在通知日志里的序号。指针接收者:发送方先 marshal 出不含 seq 的
// payload 落库,再把库分配到的 seq 写回帧本身,然后才推送。
func (f *EventFrame) SetSeq(seq int64) { f.Seq = seq }

// RunResultDoneFrame 在 daemon 端 events channel close 之后发一次,带完整 RunResult。
// 客户端拿到后填回 *remote.Runtime 持有的 *RunResult 指针,然后才 close 客户端的
// events channel,匹配 chat_svc 的契约(chat.go:1683-1722 在 channel close 后才读 result)。
//
// StopErrMsg / StopErrCode 用来在客户端把 RunResult.StopErr 重新 hydrate 成正确的
// sentinel(ErrAborted 等)。StopErrCode = 0 表示无 sentinel,StopErrMsg 仅作显示;
// = -32013 表示 ErrAborted;等等。
type RunResultDoneFrame struct {
	ConversationID    string     `json:"conversationId"`
	ProviderSessionID string     `json:"providerSessionId,omitempty"`
	Usage             *UsageWire `json:"usage,omitempty"`
	UserAnchor        string     `json:"userAnchor,omitempty"`
	Model             string     `json:"model,omitempty"`
	ContextWindow     int        `json:"contextWindow,omitempty"`
	TurnToken         uint64     `json:"turnToken,omitempty"`
	StopErrMsg        string     `json:"stopErrMsg,omitempty"`
	StopErrCode       int        `json:"stopErrCode,omitempty"`
	Seq               int64      `json:"seq,omitempty"`

	// 本轮的计时,由 daemon 就着它自己扇出的那条事件流量出来(口径与映射见
	// internal/pkg/turnstats)。按帧重建转录的消费方(浏览器控制台 / peer 视图)
	// 没有第二个来源:桌面端本机会话上那三个数是 chat_svc 在 runtime 之上算完落
	// 自己库的,过不了 wire。
	//
	// DurationMs 是墙上时间、**含**工具空档;TokensPerSec 的分母只数生成段。
	DurationMs   int     `json:"durationMs,omitempty"`
	FirstTokenMs int     `json:"firstTokenMs,omitempty"`
	TokensPerSec float64 `json:"tokensPerSec,omitempty"`
}

// SetSeq 盖上该帧在通知日志里的序号(见 EventFrame.SetSeq)。
func (f *RunResultDoneFrame) SetSeq(seq int64) { f.Seq = seq }

// AutonomousTurnStartedFrame 在一轮自主续轮开始时由 daemon 发一次。客户端据此
// 新建一个 agentruntime.AutonomousTurn 推给 AutonomousTurns() 的消费方,并把随后
// 的 NotifyAutonomousTurnEvent(EventFrame)路由进它的 Events,直到 NotifyAutonomousTurnDone
// (RunResultDoneFrame)填回该轮 RunResult 并 close。
type AutonomousTurnStartedFrame struct {
	ConversationID string `json:"conversationId"`
	Trigger        string `json:"trigger,omitempty"`
	TurnToken      uint64 `json:"turnToken,omitempty"`
	Seq            int64  `json:"seq,omitempty"`
}

// SetSeq 盖上该帧在通知日志里的序号(见 EventFrame.SetSeq)。
func (f *AutonomousTurnStartedFrame) SetSeq(seq int64) { f.Seq = seq }

// UsageWire mirrors provider.Usage with stable lowerCamelCase tags. provider.Usage
// has no JSON tags so we wrap it for wire stability(同 event_wire.go 里同名 helper)。
type UsageWire struct {
	PromptTokens        int `json:"promptTokens"`
	CompletionTokens    int `json:"completionTokens"`
	ReasoningTokens     int `json:"reasoningTokens"`
	CachedTokens        int `json:"cachedTokens"`
	CacheCreationTokens int `json:"cacheCreationTokens"`
	TotalTokens         int `json:"totalTokens"`
}

// ── project.* 本机路径 ──────────────────────────────────────────────────────

// ProjectSetLocalPathParams 指定某个项目在**这台机器上**的本机路径。
type ProjectSetLocalPathParams struct {
	ProjectSyncID string `json:"projectSyncId"`
	Path          string `json:"path"`
}

// ProjectClearLocalPathParams 把某个项目在这台机器上打回「本机未配置路径」。
//
// **机器上的目录一个字节都不动**,去掉的只是「这个项目在本机落在哪」这条记录。
type ProjectClearLocalPathParams struct {
	ProjectSyncID string `json:"projectSyncId"`
}

// ProjectLocalPathResult 是两个写方法共同的应答:生效之后的状态。
//
// 带回路径正文是刻意的:上报是 30 秒轮询,浏览器重新去 server 拉只会拿到旧快照。
// 调用方据此就地更新那一行,不必等下一轮。
type ProjectLocalPathResult struct {
	// Path 是生效后的本机路径;清除之后为空。
	Path string `json:"path"`
	// Configured 为假即这个项目在这台机器上处于「本机未配置路径」。
	Configured bool `json:"configured"`
}
