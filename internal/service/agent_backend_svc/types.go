// Package agent_backend_svc 暴露 Agent 后端的应用服务接口与请求/响应类型。
//
// 类型定义直接被 Wails 绑定层引用，会被 wails dev / wails build 提取为 TypeScript
// 类型暴露给前端，因此字段名要稳定、json tag 要明确。
package agent_backend_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
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
	HasToken             bool   `json:"hasToken"`
	// DeviceID 是目标机器的 canonical fingerprint；遗留记录可能仍是 paired_agents.id
	// 的数字字符串。当前安装自己的 fingerprint 表示本机，跨机展示/编辑必须保留原值；
	// 只有调用本地 daemon RPC 时才翻译成 paired row ID。
	DeviceID string `json:"deviceId"`
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
type CreateBackendRequest struct {
	Type                  string                 `json:"type" binding:"required"`
	Name                  string                 `json:"name" binding:"required"`
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
	DeviceID              string                 `json:"deviceId"`
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
	DeviceID              string                 `json:"deviceId"`
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
	RequestID             string                 `json:"requestId"`
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
	BackendSyncID string `json:"backendSyncId"`
	Fingerprint   string `json:"fingerprint"`
	Status        string `json:"status"`
}

type ListCLIOverlaysRequest struct{}
type ListCLIOverlaysResponse struct {
	Items []*CLIOverlayItem `json:"items"`
}

// GetCLIOverlayRequest reads the current desktop's per-device CLI override.
type GetCLIOverlayRequest struct {
	BackendSyncID string `json:"backendSyncId" binding:"required"`
}

type GetCLIOverlayResponse struct {
	CLIPath string `json:"cliPath"`
	Status  string `json:"status"`
}

// SetCLIOverlayRequest updates the current desktop's per-device CLI override.
type SetCLIOverlayRequest struct {
	BackendSyncID string `json:"backendSyncId" binding:"required"`
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
//   - 其它 canonical fingerprint（或遗留 paired_agents.id 数字串）→ 在本地派发
//     边界解析成 paired row ID，再拨该 device 的 daemon cli.resolvePath RPC。
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
