// Package wire 定义 agentre ↔ agentred RPC 协议的参数 / 结果 / 通知帧。
// daemon handlers 和 client *remote.Runtime 共享同一组类型,避免两边手抄 JSON shape 时漂移。
//
// 命名约定:
//   - 所有 RPC 方法都在 "runtime.*" 命名空间下,与 agentruntime.Runtime + 子接口一一对应。
//   - 字段名一律 lowerCamelCase。
//   - 错误码 -32010..-32014 是 agentruntime 标准 sentinel 的稳定 wire 值;
//     ToJSONRPCError / FromJSONRPCError 双向翻译,让 errors.Is(err, agentruntime.ErrXxx)
//     在客户端继续工作。
package wire

import (
	"encoding/json"
	"errors"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-ai/agentre/internal/pkg/jsonrpc"
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
	MethodSubmitAnswer         = "runtime.submitAnswer"
	MethodSubmitToolPermission = "runtime.submitToolPermission"
	MethodGetGoal              = "runtime.goal.get"
	MethodSetGoal              = "runtime.goal.set"
	MethodClearGoal            = "runtime.goal.clear"

	// 断连重连的补齐族。客户端重连后的三步是 list → attach → pull(→ pendingWaiters),
	// 每一步都限定在调用方自己的对端范围内(R16),对端身份取自那条连接的鉴权状态,
	// 不由参数携带 —— 参数里的对端标识等于让任何已配对设备点名读别人的会话。
	//
	// 老版本 daemon 不认识这四个方法,会回 method-not-found;客户端据此判定该 daemon
	// 不支持本规格并回落到「断连即终止」(R18),所以它们必须是**新增**方法而不是给
	// 既有方法加参数。
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
	// 它同样是**新增**方法而不是给既有方法加参数:老版本的执行端不认识它,会回
	// method-not-found,调用方据此判定「这台机器这辈子都答不了」并如实降级(判据见
	// ErrSessionDeleteUnsupported),而不是把一条永远失败的删除重试到天荒地老。
	MethodSessionDelete = "runtime.session.delete"

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
)

// ToJSONRPCError 把 agentruntime 的 sentinel 包成 *jsonrpc.Error,daemon 端返回。
// 非 sentinel 错误返 nil,调用方应自己包装(ErrInternal 之类)。
func ToJSONRPCError(err error) *jsonrpc.Error {
	if code, ok := CodeForSentinel(err); ok {
		return &jsonrpc.Error{Code: code, Message: err.Error()}
	}
	return nil
}

// FromJSONRPCError 反向把 *jsonrpc.Error 翻成对应的 agentruntime sentinel。
// 未知 code 返原 err。
func FromJSONRPCError(err error) error {
	var rpcErr *jsonrpc.Error
	if !errors.As(err, &rpcErr) {
		return err
	}
	if sent := SentinelFromCode(rpcErr.Code); sent != nil {
		return sent
	}
	return err
}

// SentinelFromCode 把 wire error code 直接翻成 agentruntime sentinel,无匹配
// 返 nil。客户端只拿到 (code, message) 二元组(走 RunResultDoneFrame 而非
// *jsonrpc.Error)时调它,免去人工合成 *jsonrpc.Error 再走 FromJSONRPCError
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
// 返 (0, false)。ToJSONRPCError 的核心,也方便 daemon 端调用方按需自己组帧。
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

// ErrSessionDeleteUnsupported 是 MethodSessionDelete 回 method-not-found 时的判据:
// 对面的执行端太老,它这辈子都不认识这个方法。
//
// 它必须与「这一次没删成」分开(与 remote.ErrCatchUpUnsupported 同形):后者等机器
// 回来再删一遍就成了,而前者重试多少次都是同一个结果 —— 分不开,server 那条删除待办
// 就会对着一台永远答不了的机器重放到天荒地老。
var ErrSessionDeleteUnsupported = errors.New("agentruntime/runtimes/remote/wire: peer does not support session delete")

// SessionDeleteCallError 把一次 MethodSessionDelete 调用的错误分类:JSON-RPC 标准的
// -32601 翻成 ErrSessionDeleteUnsupported,其余原样交回(包括 nil)。
func SessionDeleteCallError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *jsonrpc.Error
	if errors.As(err, &rpcErr) && rpcErr.Code == jsonrpc.ErrMethodNotFound.Code {
		return ErrSessionDeleteUnsupported
	}
	return err
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

// OK 大部分 mutating 方法不需要返回值,统一返这个空 struct 让 JSON-RPC 框架知道
// 是「成功无 payload」。
type OK struct{}

// PeerSessionControlResult reports that another endpoint won the race to answer
// a pending ask or tool permission. Older peers return an empty object, whose
// zero value preserves the original successful outcome.
type PeerSessionControlResult struct {
	AlreadyHandled bool `json:"alreadyHandled,omitempty"`
}

type GoalParams struct {
	SessionID         int64           `json:"sessionId"`
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
	Backend   json.RawMessage `json:"backend"`
	AgentID   int64           `json:"agentId"`
	SessionID int64           `json:"sessionId"`
	// PeerFingerprint 点名这一轮要落在**哪个对端**名下的那条会话上(R9)。会话键是
	// (发起端指纹, 会话 id),而清单(SessionSummary.PeerFingerprint)是客户端学 origin
	// 的唯一来源;省略 = 调用方自己的对端,与控制族(attach / pull / abort / submit)
	// 同一条 ResolveSessionPeer 约定 —— 点名别人的 origin 是账号级能力,配对身份点名
	// 一律被拒。不点名地给别人的会话开新一轮,会在调用方名下另建一条同号会话:上下文
	// (决策 8 的 provider_session_id)续不上,事件也落到另一个 journal 分区,发起端与
	// 它的其余订阅者一条都收不到(R6 / R18 的前提)。
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	Cwd             string `json:"cwd,omitempty"`
	// Title 是该会话此刻的标题(R7)。桌面端每轮携带当前值,daemon 幂等覆盖;老桌面端
	// 不传时保持空串。改名后最多滞后一轮生效。
	Title string `json:"title,omitempty"`
	// AgentSyncID 是该会话所属 Agent 的账号级同步标识(块 1,决策 3 的 ULID,不是本地
	// 自增 agent_id)。会话列表按它解析 Agent 名与头像(R5)。
	AgentSyncID       string `json:"agentSyncId,omitempty"`
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
	Body    []byte              `json:"body,omitempty"`    // JSON-RPC body
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
	SessionID            int64  `json:"sessionId"`
	ProviderSessionID    string `json:"providerSessionId,omitempty"`
	LaunchPermissionMode string `json:"launchPermissionMode,omitempty"`
	ProviderFallbackKey  string `json:"providerFallbackKey,omitempty"`
}

// SteerParams 等同 agentruntime.Steerer.Steer 的入参。
type SteerParams struct {
	SessionID       int64  `json:"sessionId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	QueuedID        string `json:"queuedId,omitempty"`
	Text            string `json:"text"`
}

// CancelSteerParams 等同 agentruntime.SteerCanceler.CancelSteer 的入参。
type CancelSteerParams struct {
	SessionID       int64  `json:"sessionId"`
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
	SessionID       int64  `json:"sessionId"`
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
	SessionID       int64  `json:"sessionId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	TurnToken       uint64 `json:"turnToken,omitempty"`
}

// AbortResult 是 MethodAbort 的应答,携带被中断轮的类型(agentruntime.AbortOutcome.TurnKind)。
type AbortResult struct {
	TurnKind agentruntime.TurnKind `json:"turnKind"`
}

// StopBackgroundTaskParams 等同 agentruntime.BackgroundTaskStopper.StopBackgroundTask 的入参。
type StopBackgroundTaskParams struct {
	SessionID       int64  `json:"sessionId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	TaskID          string `json:"taskId"`
}

// SetPermissionModeParams 等同 agentruntime.PermissionModeSetter.SetPermissionMode 的入参。
type SetPermissionModeParams struct {
	SessionID       int64  `json:"sessionId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	Mode            string `json:"mode"`
}

// SubmitAnswerParams 等同 agentruntime.AskAnswerSink.SubmitAnswer 的入参。
type SubmitAnswerParams struct {
	SessionID       int64                      `json:"sessionId"`
	PeerFingerprint string                     `json:"peerFingerprint,omitempty"`
	RequestID       string                     `json:"requestId"`
	Questions       []agentruntime.AskQuestion `json:"questions,omitempty"`
	Answers         []agentruntime.AskAnswer   `json:"answers,omitempty"`
	Skipped         bool                       `json:"skipped,omitempty"`
}

// SubmitToolPermissionParams 等同 agentruntime.ToolPermissionSink.SubmitToolPermission 的入参。
type SubmitToolPermissionParams struct {
	SessionID          int64  `json:"sessionId"`
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
	SessionID       int64  `json:"sessionId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	AgentID         int64  `json:"agentId,omitempty"`
	// Title / AgentSyncID / ProviderSessionID 是 R7 + 决策 8 的新列:会话标题、所属
	// Agent 的账号级同步标识、以及续话要用的 provider 原生会话身份。老会话缺这些
	// 字段时如实留空(空串,不猜、不填占位名)。
	Title             string `json:"title,omitempty"`
	AgentSyncID       string `json:"agentSyncId,omitempty"`
	ProviderSessionID string `json:"providerSessionId,omitempty"`
	Cwd               string `json:"cwd,omitempty"`
	BackendType       string `json:"backendType,omitempty"`
	LifecycleState    string `json:"lifecycleState"`
	WaitingForInput   bool   `json:"waitingForInput,omitempty"`
	LatestSeq         int64  `json:"latestSeq"`
	// UpdatedAt 是这条会话最后一次活动的时刻(Unix 毫秒),取自 daemon_sessions 的
	// updated_at —— 每轮起手幂等覆盖时一并推进。会话清单要显示「最后活动时间」
	// (R5),而它的唯一真相源在执行端这台机器上。没记过活动时间的老会话报 0,由
	// 客户端如实表达为「未知」而不是猜一个时刻。
	UpdatedAt int64 `json:"updatedAt,omitempty"`
}

// SessionListResult 是 MethodSessionList 的应答:这台 daemon 上的会话。调用方自己的
// 对端永远在范围内;daemon 已认领账号时 ListAll 会把全部对端的会话一并列出(账号可见
// 性,见 handlers/session_catchup.go 的 List),范围不再只有「调用这条连接的对端」。
type SessionListResult struct {
	Sessions []SessionSummary `json:"sessions"`
	// SupportsSessionMetadata 声明这台 daemon 落库并回传 R7 的标题 / Agent 同步标识
	// 与决策 8 的 provider_session_id。**未升级的 agentred 不认识这个字段**,它的应答
	// 里解出来恒为 false —— 这是客户端区分「这台机器上的老会话」与「这台机器本身没
	// 升级」的唯一信号。后者上头,会话只能按 R5 退化显示、发新消息也续不上上下文,
	// 客户端必须如实说明该机器需要升级而不是静默失败(规格「安全、隐私与兼容性」)。
	SupportsSessionMetadata bool `json:"supportsSessionMetadata,omitempty"`
}

// SessionPullParams 是 MethodSessionPull 的请求:给定会话与起始游标,取其后的通知。
// Cursor 是**已经收到的**最后一个 seq(独占),所以首次补齐传 0。
type SessionPullParams struct {
	SessionID       int64  `json:"sessionId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
	Cursor          int64  `json:"cursor"`
	Limit           int    `json:"limit,omitempty"`
}

// JournaledNotification 是日志里的一行:那条本该发出的通知的原样 (method, params)。
//
// Params **不含 seq** —— 落库时 seq 还没盖上去,它是日志行自己的列。补齐的客户端必须
// 按 method 把 Params 解成对应的帧、把这里的 Seq 盖上去、再喂进与实时同一套 handler,
// 否则每一帧都解出 seq=0,会被「不大于游标就丢弃」的规则整段吞掉(R6)。
type JournaledNotification struct {
	Seq    int64           `json:"seq"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
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
	SessionID       int64  `json:"sessionId"`
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
	SessionID       int64  `json:"sessionId"`
	PeerFingerprint string `json:"peerFingerprint,omitempty"`
}

// SessionAttachResult 交回客户端接着补齐需要的东西:会话此刻的生命周期状态、backend
// 类型,以及此刻的最新 seq(高水位)。
//
// 接管成功后该会话的实时通知就推给这条连接;客户端随后按自己的游标 pull 到拉平即可,
// 接管与读高水位之间落库的那几条会在同一轮 pull 里被带出来。
type SessionAttachResult struct {
	SessionID      int64  `json:"sessionId"`
	BackendType    string `json:"backendType,omitempty"`
	LifecycleState string `json:"lifecycleState"`
	LatestSeq      int64  `json:"latestSeq"`
}

// SessionDeleteParams 是 MethodSessionDelete 的请求。PeerFingerprint 的语义与补齐族
// 完全一致:省略 = 调用方自己的对端,点名别人是账号级能力(见 handlers.ResolveSessionPeer)。
// 这是本 wire 上第一个破坏性方法,越界的代价不再是「读到了不该读的」而是「删掉了
// 别人的对话」,所以它绝不能自成一套宽松的范围规则。
type SessionDeleteParams struct {
	SessionID       int64  `json:"sessionId"`
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

// ── Notification frames ─────────────────────────────────────────────────────

// Seq 字段的共同约定(EventFrame / RunResultDoneFrame / AutonomousTurnStartedFrame):
// 它是这条通知在 daemon 通知日志里的序号,同一会话内从 1 起单调递增、无洞。daemon 先
// 落库拿到 seq 再推送,所以每条推出去的帧都带着它(R6);客户端据此判断跳号并按游标补齐。
//
// 它是**可选的追加字段**:老版本桌面端不认识 "seq",JSON 解码时直接忽略,行为与今天
// 完全一致。因此新增它不需要版本协商,也永远不能变成必填。
//
// 日志里存的 payload 是**不含 seq** 的帧原样 —— seq 是日志行自己的列,实时推送与重连
// 补齐都在发送时才把行上的 seq 盖到帧上,两条路径因此投递同一份字节 + 同一个 seq。

// EventFrame wraps a single agentruntime.Event for delivery over NotifyEvent.
// SessionID is transport metadata so the receiving end can route by session;
// Event payload is the JSON output of one of the 19 sealed Event types
// (see internal/pkg/agentruntime/event_wire.go for the marshaling rules).
type EventFrame struct {
	SessionID int64           `json:"sessionId"`
	Event     json.RawMessage `json:"event"`
	Seq       int64           `json:"seq,omitempty"`
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
	SessionID         int64      `json:"sessionId"`
	ProviderSessionID string     `json:"providerSessionId,omitempty"`
	Usage             *UsageWire `json:"usage,omitempty"`
	UserAnchor        string     `json:"userAnchor,omitempty"`
	Model             string     `json:"model,omitempty"`
	ContextWindow     int        `json:"contextWindow,omitempty"`
	TurnToken         uint64     `json:"turnToken,omitempty"`
	StopErrMsg        string     `json:"stopErrMsg,omitempty"`
	StopErrCode       int        `json:"stopErrCode,omitempty"`
	Seq               int64      `json:"seq,omitempty"`
}

// SetSeq 盖上该帧在通知日志里的序号(见 EventFrame.SetSeq)。
func (f *RunResultDoneFrame) SetSeq(seq int64) { f.Seq = seq }

// AutonomousTurnStartedFrame 在一轮自主续轮开始时由 daemon 发一次。客户端据此
// 新建一个 agentruntime.AutonomousTurn 推给 AutonomousTurns() 的消费方,并把随后
// 的 NotifyAutonomousTurnEvent(EventFrame)路由进它的 Events,直到 NotifyAutonomousTurnDone
// (RunResultDoneFrame)填回该轮 RunResult 并 close。
type AutonomousTurnStartedFrame struct {
	SessionID int64  `json:"sessionId"`
	Trigger   string `json:"trigger,omitempty"`
	TurnToken uint64 `json:"turnToken,omitempty"`
	Seq       int64  `json:"seq,omitempty"`
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
