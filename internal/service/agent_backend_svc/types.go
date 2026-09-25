// Package agent_backend_svc 暴露 Agent 后端的应用服务接口与请求/响应类型。
//
// 类型定义直接被 Wails 绑定层引用，会被 wails dev / wails build 提取为 TypeScript
// 类型暴露给前端，因此字段名要稳定、json tag 要明确。
package agent_backend_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"

	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// RouteTarget 是 Claude Tier Route 的结构化目标（spec 决策 14）。
// 与 agent_backend_entity.ModelRouteTarget 同形；前端只读类型化 target，不读原始 JSON。
// ModelKey 空 = provider-default；alias 缺失 = inherit-main。
type RouteTarget struct {
	ProviderKey string `json:"providerKey"`
	ModelKey    string `json:"modelKey"`
}

// BackendItem 单条 Agent 后端配置（已 join LLM Provider 摘要）。
type BackendItem struct {
	ID                int64  `json:"id"`
	SyncID            string `json:"syncId"`
	Type              string `json:"type"`
	Name              string `json:"name"`
	LLMProviderKey    string `json:"llmProviderKey"`
	LLMProviderName   string `json:"llmProviderName"`
	LLMProviderType   string `json:"llmProviderType"`
	LLMProviderModel  string `json:"llmProviderModel"`
	LLMProviderActive bool   `json:"llmProviderActive"`
	// LLMModelKey 主绑定目标的稳定 ModelKey（空 = provider-default）。
	LLMModelKey string `json:"llmModelKey"`
	// ModelRoutes 类型化的 Claude Tier Route target（key = OPUS/SONNET/HAIKU）。
	ModelRoutes     map[string]RouteTarget `json:"modelRoutes"`
	Sandbox         string                 `json:"sandbox"`
	Approval        string                 `json:"approval"`
	EnvJSON         string                 `json:"envJson"`
	ReasoningEffort string                 `json:"reasoningEffort"`
	// DefaultPermissionMode 仅 claudecode 使用；新会话起手 mode；
	// '' / default / acceptEdits / plan / bypassPermissions。
	DefaultPermissionMode string `json:"defaultPermissionMode"`
	// DefaultModel 仅 claudecode 使用；spawn claude 子进程下发的 --model 值。
	// 走 CLI 登录态（未绑 provider）时填自定义模型（如 claude-fable-5）；空 = CLI 默认。
	DefaultModel string `json:"defaultModel"`
	// OpenClaw fields are non-sensitive Gateway configuration. Authentication
	// tokens are deliberately absent from every Wails DTO.
	OpenClawGatewayURL   string `json:"openClawGatewayUrl"`
	OpenClawAgentID      string `json:"openClawAgentId"`
	OpenClawDefaultModel string `json:"openClawDefaultModel"`
	OpenClawSessionMode  string `json:"openClawSessionMode"`
	// HermesURL 仅 hermes 使用（entity 侧落在 config_json，不走迁移）：一个已在运行的
	// `hermes serve` 的地址，规范形式 http(s)://host:port。非敏感配置，可随 DTO 出入。
	HermesURL string `json:"hermesUrl"`
	// HermesAuthProvider 仅 hermes 使用：gated serve 的认证 provider 名（如 basic）。
	// 非敏感展示字段；密码与 refresh token 绝不出现在任何 DTO 里。
	HermesAuthProvider string `json:"hermesAuthProvider"`
	// HermesUserID 仅 hermes 使用：登录成功后的用户标识，用于界面「已登录为 xxx」。
	HermesUserID string `json:"hermesUserId"`
	// ACPCommand / ACPArgs 仅 acp 使用：非敏感的可执行文件与附加 argv（语义同
	// CreateBackendRequest）。
	ACPCommand string   `json:"acpCommand"`
	ACPArgs    []string `json:"acpArgs"`
	HasToken   bool     `json:"hasToken"`
	// DeviceID 是目标机器的 canonical fingerprint。当前安装自己的 fingerprint 表示
	// 本机，跨机展示/编辑必须保留原值；只有调用本地 daemon RPC 时才翻译成
	// paired row ID。
	DeviceID devicefp.Carrier `json:"deviceId"`
	// DeviceName 关联目标设备的显示名；无法在本机设备目录解析时可能为空。
	DeviceName string `json:"deviceName"`
	// Online 关联远端设备当前是否在线；DeviceID 为空时为 false。
	Online bool `json:"online"`
	// AgentCount 引用该 backend 的 active Agent 数；List 时由 svc 注入。
	AgentCount int64 `json:"agentCount"`
	Createtime int64 `json:"createtime"`
	Updatetime int64 `json:"updatetime"`
}

// ListBackendsRequest 入参占位。
type ListBackendsRequest struct{}

// ListBackendsResponse 列出全部启用的后端。
type ListBackendsResponse struct {
	Items []*BackendItem `json:"items"`
}

// CreateBackendRequest 新建后端。不同 Type 的字段约束由 agent_backend_entity.BackendKind 校验。
//
// **没有 cliPath**：可执行文件路径是每（后端, 设备）一行的覆盖，走 SetCLIOverlay
// 那个端口写，不随身份一起编码——身份写一次就顺手把路径写回去，会撤回另一台设备
// 在这期间对那一行的改动（见 agent_backend.go 里 ListCLIOverlays 一族的注释）。
type CreateBackendRequest struct {
	Type                  string                 `json:"type" binding:"required"`
	Name                  string                 `json:"name" binding:"required"`
	LLMProviderKey        string                 `json:"llmProviderKey"`
	LLMModelKey           string                 `json:"llmModelKey"`
	ModelRoutes           map[string]RouteTarget `json:"modelRoutes"`
	Sandbox               string                 `json:"sandbox"`
	Approval              string                 `json:"approval"`
	EnvJSON               string                 `json:"envJson"`
	ReasoningEffort       string                 `json:"reasoningEffort"`
	DefaultPermissionMode string                 `json:"defaultPermissionMode"`
	DefaultModel          string                 `json:"defaultModel"`
	OpenClawGatewayURL    string                 `json:"openClawGatewayUrl"`
	OpenClawAgentID       string                 `json:"openClawAgentId"`
	OpenClawDefaultModel  string                 `json:"openClawDefaultModel"`
	OpenClawSessionMode   string                 `json:"openClawSessionMode"`
	HermesURL             string                 `json:"hermesUrl"`
	HermesAuthProvider    string                 `json:"hermesAuthProvider"`
	HermesUserID          string                 `json:"hermesUserId"`
	// ACPCommand / ACPArgs 仅 acp 使用：ACP Agent 可执行文件与附加 argv，随身份
	// 落 config_json 并随后端同步；没有每设备覆盖（acp 不是「已知 CLI」）。
	ACPCommand string   `json:"acpCommand"`
	ACPArgs    []string `json:"acpArgs"`
	DeviceID   string   `json:"deviceId"`
}

// CreateBackendResponse 返回创建后的实体。
type CreateBackendResponse struct {
	Item *BackendItem `json:"item"`
}

// UpdateBackendRequest 更新后端。Type 不可变。
type UpdateBackendRequest struct {
	ID                    int64                  `json:"id" binding:"required"`
	Name                  string                 `json:"name" binding:"required"`
	LLMProviderKey        string                 `json:"llmProviderKey"`
	LLMModelKey           string                 `json:"llmModelKey"`
	ModelRoutes           map[string]RouteTarget `json:"modelRoutes"`
	Sandbox               string                 `json:"sandbox"`
	Approval              string                 `json:"approval"`
	EnvJSON               string                 `json:"envJson"`
	ReasoningEffort       string                 `json:"reasoningEffort"`
	DefaultPermissionMode string                 `json:"defaultPermissionMode"`
	DefaultModel          string                 `json:"defaultModel"`
	OpenClawGatewayURL    string                 `json:"openClawGatewayUrl"`
	OpenClawAgentID       string                 `json:"openClawAgentId"`
	OpenClawDefaultModel  string                 `json:"openClawDefaultModel"`
	OpenClawSessionMode   string                 `json:"openClawSessionMode"`
	HermesURL             string                 `json:"hermesUrl"`
	HermesAuthProvider    string                 `json:"hermesAuthProvider"`
	HermesUserID          string                 `json:"hermesUserId"`
	// ACPCommand / ACPArgs 仅 acp 使用；语义同 CreateBackendRequest。
	ACPCommand string   `json:"acpCommand"`
	ACPArgs    []string `json:"acpArgs"`
	DeviceID   string   `json:"deviceId"`
}

// UpdateBackendResponse 返回更新后的实体。
type UpdateBackendResponse struct {
	Item *BackendItem `json:"item"`
}

// DeleteBackendRequest 软删除后端。
type DeleteBackendRequest struct {
	ID int64 `json:"id" binding:"required"`
}

// DeleteBackendResponse 占位返回。
type DeleteBackendResponse struct{}

// TestBackendRequest 请求一次连通性自检。
//
// ID > 0  → 用已保存的 backend 记录作底；UseDraft=true 时再用 draft 字段覆盖。
// ID == 0 → 全部字段从 draft 来,适用于"还没保存就先试"。
//
// RequestID 由前端生成（uuid），用于在测试还在跑时通过 CancelTest 主动中断。
// 留空 → 不可中断（兼容旧路径 / 自动化调用）。
type TestBackendRequest struct {
	ID                    int64                  `json:"id"`
	UseDraft              bool                   `json:"useDraft"`
	Type                  string                 `json:"type"`
	Name                  string                 `json:"name"`
	LLMProviderKey        string                 `json:"llmProviderKey"`
	LLMModelKey           string                 `json:"llmModelKey"`
	CLIPath               string                 `json:"cliPath"`
	ModelRoutes           map[string]RouteTarget `json:"modelRoutes"`
	Sandbox               string                 `json:"sandbox"`
	Approval              string                 `json:"approval"`
	EnvJSON               string                 `json:"envJson"`
	ReasoningEffort       string                 `json:"reasoningEffort"`
	DefaultPermissionMode string                 `json:"defaultPermissionMode"`
	DefaultModel          string                 `json:"defaultModel"`
	OpenClawGatewayURL    string                 `json:"openClawGatewayUrl"`
	OpenClawAgentID       string                 `json:"openClawAgentId"`
	OpenClawDefaultModel  string                 `json:"openClawDefaultModel"`
	OpenClawSessionMode   string                 `json:"openClawSessionMode"`
	HermesURL             string                 `json:"hermesUrl"`
	HermesAuthProvider    string                 `json:"hermesAuthProvider"`
	HermesUserID          string                 `json:"hermesUserId"`
	// DeviceID 是草稿上选中的绑定设备(canonical fingerprint)。Hermes / OpenClaw 的
	// 凭据只在绑定设备上,所以「还没保存就先试」也要说清在哪台机器上试。留空表示
	// 沿用保存行的绑定(ID>0)或本机(草稿)。
	DeviceID  string `json:"deviceId"`
	RequestID string `json:"requestId"`
}

// TestBackendResponse 返回测试结果。
//
// Message 在 OK=true 时是模型回复文本,OK=false 时是人话错误。
type TestBackendResponse struct {
	OK             bool                  `json:"ok"`
	Code           string                `json:"code"`
	Message        string                `json:"message"`
	LatencyMs      int64                 `json:"latencyMs"`
	GatewayVersion string                `json:"gatewayVersion"`
	Protocol       int                   `json:"protocol"`
	GrantedScopes  []string              `json:"grantedScopes"`
	Methods        []string              `json:"methods"`
	Events         []string              `json:"events"`
	OpenClawAgents []OpenClawAgentOption `json:"openClawAgents"`
	OpenClawModels []OpenClawModelOption `json:"openClawModels"`
}

type OpenClawAgentOption struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	PrimaryModel string   `json:"primaryModel"`
	Fallbacks    []string `json:"fallbacks"`
	Default      bool     `json:"default"`
}

type OpenClawModelOption struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	Available bool   `json:"available"`
}

// ---- Hermes gated serve 登录 ----

// HermesAuthProviderItem 是 GET /api/auth/providers 的一条 provider。
// SupportsPassword=false 的 provider（OAuth）在当前阶段不可用。
type HermesAuthProviderItem struct {
	Name             string `json:"name"`
	DisplayName      string `json:"displayName"`
	SupportsPassword bool   `json:"supportsPassword"`
}

// ListHermesAuthProvidersRequest 查询一个 hermes serve 支持的认证 provider。
// URL 可以是还没保存的地址;DeviceID 指定去哪台机器上读(空 = 本机)。
type ListHermesAuthProvidersRequest struct {
	URL      string `json:"url" binding:"required"`
	DeviceID string `json:"deviceId"`
}

type ListHermesAuthProvidersResponse struct {
	Providers []HermesAuthProviderItem `json:"providers"`
}

// LoginHermesRequest 跑一次原生 PKCE 登录。后端可能还没保存：凭据按 URL 派生存 keychain。
// Password 只在这一次调用里存在，绝不落库。
type LoginHermesRequest struct {
	URL      string `json:"url" binding:"required"`
	Provider string `json:"provider"`
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	// DeviceID 是要登录的那台设备(空 = 本机)。凭据只落在它上面,换绑不迁移。
	DeviceID string `json:"deviceId"`
}

// LoginHermesResponse 只回非敏感展示字段；refresh token 留在 keychain。
type LoginHermesResponse struct {
	Provider string `json:"provider"`
	UserID   string `json:"userId"`
}

// LogoutHermesRequest 退出登录。ID>0 时清后端 config_json 里的两个展示字段；
// URL 用于还没保存的草稿。
type LogoutHermesRequest struct {
	ID  int64  `json:"id"`
	URL string `json:"url"`
	// DeviceID 只在草稿(ID==0)时使用;ID>0 时以保存行上的绑定设备为准。
	DeviceID string `json:"deviceId"`
}

type LogoutHermesResponse struct{}

// BackendCredentialStatusRequest 查询一个后端在它绑定设备上的凭据状态。
// OpenClaw 按 SyncID 问,Hermes 按 URL 问;DeviceID 空 = 本机。
type BackendCredentialStatusRequest struct {
	Type      string `json:"type" binding:"required"`
	SyncID    string `json:"syncId"`
	HermesURL string `json:"hermesUrl"`
	DeviceID  string `json:"deviceId"`
}

// BackendCredentialStatusResponse 只说「存没存 / 登录成谁」。登录有没有过期要等
// 测试连接或发起对话才知道,凭据本身永远不出绑定设备。
type BackendCredentialStatusResponse struct {
	OpenClawTokenSaved bool   `json:"openClawTokenSaved"`
	HermesLoggedIn     bool   `json:"hermesLoggedIn"`
	HermesProvider     string `json:"hermesProvider"`
	HermesUserID       string `json:"hermesUserId"`
}

// CancelTestBackendRequest 中断一个还在跑的 Test。
//
// RequestID 必须与发起 Test 时 TestBackendRequest.RequestID 一致；
// 未知 ID 返回 Canceled=false（不视为错误，避免前端竞态导致刷红）。
type CancelTestBackendRequest struct {
	RequestID string `json:"requestId" binding:"required"`
}

// CancelTestBackendResponse 返回是否真的命中了在跑的请求。
type CancelTestBackendResponse struct {
	Canceled bool `json:"canceled"`
}

type CLIOverlayItem struct {
	BackendSyncID string           `json:"backendSyncId"`
	Fingerprint   devicefp.Carrier `json:"fingerprint"`
	Status        string           `json:"status"`
}

type ListCLIOverlaysRequest struct{}
type ListCLIOverlaysResponse struct {
	Items []*CLIOverlayItem `json:"items"`
}

// GetCLIOverlayRequest reads the per-device CLI override for BackendSyncID on
// DeviceID's row. DeviceID empty means the local installation's own
// fingerprint (normalizeDeviceID's fallback), keeping the pre-device-aware
// call shape working unchanged.
type GetCLIOverlayRequest struct {
	BackendSyncID string `json:"backendSyncId" binding:"required"`
	DeviceID      string `json:"deviceId"`
}

type GetCLIOverlayResponse struct {
	CLIPath string `json:"cliPath"`
	Status  string `json:"status"`
}

// SetCLIOverlayRequest updates the per-device CLI override for
// BackendSyncID on DeviceID's row. DeviceID empty means the local
// installation's own fingerprint (normalizeDeviceID's fallback).
type SetCLIOverlayRequest struct {
	BackendSyncID string `json:"backendSyncId" binding:"required"`
	DeviceID      string `json:"deviceId"`
	CLIPath       string `json:"cliPath"`
}

type SetCLIOverlayResponse struct {
	CLIPath string `json:"cliPath"`
	Status  string `json:"status"`
}

// ResolveCLIPathRequest 探测前端选定 CLI 后端类型可用的 binary 绝对路径。
//
// Type 必填，仅接受 "claudecode" / "codex"；其它值返回 AgentBackendInvalidType。
//
// DeviceID 路由 CLI 探测的目标机：
//   - 空串或本安装的 canonical fingerprint → 本地，主进程直接扫本机 $PATH；
//   - 其它 canonical fingerprint → 在本地派发边界解析成 paired row ID，再拨该
//     device 的 daemon cli.resolvePath RPC。
type ResolveCLIPathRequest struct {
	Type     string `json:"type" binding:"required"`
	DeviceID string `json:"deviceId"`
}

// ResolveCLIPathResponse 返回 exec.LookPath 命中的绝对路径。
//
// Found=false 时 Path 为空，表示 $PATH 里未挂到对应可执行文件；前端应回退到
// 让用户手填。已注释字段不会被前端写回 backend 表，仅作为编辑器自动填充建议。
type ResolveCLIPathResponse struct {
	Path  string `json:"path"`
	Found bool   `json:"found"`
}

// ScanResultItem 一次扫描并尝试创建的结果。
type ScanResultItem struct {
	Type      string `json:"type"`                // "claudecode" / "codex" / "piagent"
	Name      string `json:"name"`                // 自动生成的名称
	CLIPath   string `json:"cliPath"`             // 命中的 binary 绝对路径
	Found     bool   `json:"found"`               // 是否在 PATH 中找到了 binary
	Created   bool   `json:"created"`             // 是否成功创建
	Skipped   bool   `json:"skipped"`             // 是否因重名跳过
	BackendID int64  `json:"backendId,omitempty"` // 创建成功后的 ID
	Error     string `json:"error,omitempty"`     // 失败的人话原因
}

// ScanAndCreateAgentBackendsRequest 入参占位。
type ScanAndCreateAgentBackendsRequest struct{}

// ScanAndCreateAgentBackendsResponse 报告扫描与自动创建结果。
type ScanAndCreateAgentBackendsResponse struct {
	Results []*ScanResultItem `json:"results"`
}

// ReclaimTombstonedBackendsRequest 入参占位。
type ReclaimTombstonedBackendsRequest struct{}

// ReclaimTombstonedBackendsResponse 报告一次墓碑回收的结果(决策 24)。
type ReclaimTombstonedBackendsResponse struct {
	// ReclaimedIDs 被物理删除的墓碑 id(墓碑 AND 无引用 AND 超过保留期)。
	ReclaimedIDs []int64 `json:"reclaimedIds"`
	// KeptReferencedIDs 早过了保留期、但仍被至少一条会话/执行目标引用而保留的
	// 墓碑 id —— 这些是 SurveyDanglingBackendReferences 会报出的那一半。
	KeptReferencedIDs []int64 `json:"keptReferencedIds"`
}

// DanglingBackendReference 描述一条指向非 ACTIVE 后端的引用(决策 24)。巡检只
// 报出,不改写 —— Kind 标出引用来自哪张表,RefID 是那一行自己的 id。
type DanglingBackendReference struct {
	Kind      string `json:"kind"` // "session" | "exec_target"
	RefID     int64  `json:"refId"`
	BackendID int64  `json:"backendId"`
}

// SurveyDanglingBackendReferencesRequest 入参占位。
type SurveyDanglingBackendReferencesRequest struct{}

// SurveyDanglingBackendReferencesResponse 报告全部悬空引用,供人工排查(决策 24
// 明确拒绝"顺手改写")。
type SurveyDanglingBackendReferencesResponse struct {
	Dangling []DanglingBackendReference `json:"dangling"`
}

//go:generate mockgen -source types.go -destination mock_prober_test.go -package agent_backend_svc -mock_names Prober=mockProber

// ProbeDeps 由 svc.Test 装配后传给 Prober。
//
//   - 对 builtin 的 codingProber 全部留空（不依赖 gateway）；
//   - CLI 子进程类 Prober 若经本地 gateway 测试，需要 Token + GatewayURL + Model。
type ProbeDeps struct {
	GatewayURL string
	Token      string
	Model      string
}

// Prober 抽象"对一条 backend 跑一轮 agent loop"这个外部依赖。
//
// 默认生产注册表目前只登记 builtinProber（cago app/coding in-process）。
// 单测可注入 fake 或替换注册表，避免真实 LLM / 子进程调用。
type Prober interface {
	Run(ctx context.Context, b *agent_backend_entity.AgentBackend, deps ProbeDeps) (reply string, err error)
}
