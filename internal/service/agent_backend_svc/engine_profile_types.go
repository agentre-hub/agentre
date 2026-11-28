package agent_backend_svc

// 引擎配置（spec 2026-11-25 决策 2）的 Wails 请求 / 响应类型。字段与后端那一组同名同义，
// 只是去掉了设备（DeviceID / DeviceName / Online）与 CLI 路径，运行时类型只在新建时给出。

// EngineProfileItem 一份引擎配置（已 join LLM Provider 摘要）。
type EngineProfileItem struct {
	ID                    int64                  `json:"id"`
	SyncID                string                 `json:"syncId"`
	Type                  string                 `json:"type"`
	Name                  string                 `json:"name"`
	LLMProviderKey        string                 `json:"llmProviderKey"`
	LLMProviderName       string                 `json:"llmProviderName"`
	LLMProviderType       string                 `json:"llmProviderType"`
	LLMProviderModel      string                 `json:"llmProviderModel"`
	LLMProviderActive     bool                   `json:"llmProviderActive"`
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
	ACPCommand            string                 `json:"acpCommand"`
	ACPArgs               []string               `json:"acpArgs"`
	// HasToken 本机钥匙串里有没有这份 OpenClaw 配置的 Gateway token（槽位按配置 sync_id）。
	HasToken bool `json:"hasToken"`
	// IsAccountDefault 账号默认的那一份（行上带标记）。
	IsAccountDefault bool `json:"isAccountDefault"`
	// Active false = 已删除：只会出现在 GetEngineProfile 里（已钉在它上面的会话还要读它）。
	Active bool `json:"active"`
	// AgentCount 直接引用它的活成员数；List 时注入。
	AgentCount int64 `json:"agentCount"`
	Createtime int64 `json:"createtime"`
	Updatetime int64 `json:"updatetime"`
}

// ListEngineProfilesRequest 入参占位。
type ListEngineProfilesRequest struct{}

// ListEngineProfilesResponse 全部活的引擎配置（按创建顺序）。
type ListEngineProfilesResponse struct {
	Items []*EngineProfileItem `json:"items"`
}

// GetEngineProfileRequest 取一份配置，已删除的也取得到。
type GetEngineProfileRequest struct {
	ID int64 `json:"id" binding:"required"`
}

// GetEngineProfileResponse 一份配置。
type GetEngineProfileResponse struct {
	Item *EngineProfileItem `json:"item"`
}

// CreateEngineProfileRequest 新建引擎配置。Name 留空 = 运行时名；同名允许。
type CreateEngineProfileRequest struct {
	Type                  string                 `json:"type" binding:"required"`
	Name                  string                 `json:"name"`
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
	ACPCommand            string                 `json:"acpCommand"`
	ACPArgs               []string               `json:"acpArgs"`
}

// CreateEngineProfileResponse 新建后的配置。
type CreateEngineProfileResponse struct {
	Item *EngineProfileItem `json:"item"`
}

// UpdateEngineProfileRequest 更新引擎配置。没有 Type：运行时类型创建后不可改。
type UpdateEngineProfileRequest struct {
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
	ACPCommand            string                 `json:"acpCommand"`
	ACPArgs               []string               `json:"acpArgs"`
}

// UpdateEngineProfileResponse 更新后的配置。
type UpdateEngineProfileResponse struct {
	Item *EngineProfileItem `json:"item"`
}

// EngineProfileRef 一个直接引用某份配置的成员或部门。
type EngineProfileRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// EngineProfileReferences 直接引用某份配置的成员与部门（删除确认里列出；删除后它们改为
// 跟随上一级）。
type EngineProfileReferences struct {
	Agents      []EngineProfileRef `json:"agents"`
	Departments []EngineProfileRef `json:"departments"`
}

// ListEngineProfileReferencesRequest 查一份配置的直接引用者。
type ListEngineProfileReferencesRequest struct {
	ID int64 `json:"id" binding:"required"`
}

// DeleteEngineProfileRequest 删除（软删）一份配置。账号默认那一份不能删。
type DeleteEngineProfileRequest struct {
	ID int64 `json:"id" binding:"required"`
}

// DeleteEngineProfileResponse 删除时改为跟随上一级的成员与部门。
type DeleteEngineProfileResponse struct {
	References EngineProfileReferences `json:"references"`
}

// SetAccountDefaultEngineProfileRequest 把一份配置设为账号默认。
type SetAccountDefaultEngineProfileRequest struct {
	ID int64 `json:"id" binding:"required"`
}

// SetAccountDefaultEngineProfileResponse 新的账号默认。
type SetAccountDefaultEngineProfileResponse struct {
	Item *EngineProfileItem `json:"item"`
}
