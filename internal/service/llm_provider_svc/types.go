// Package llm_provider_svc 暴露 LLM 供应商及其稳定模型的应用服务接口与请求/响应类型。
//
// 类型定义直接被 Wails 绑定层引用，会被 wails dev / wails build 提取成 TypeScript
// 类型暴露给前端，因此字段名要稳定、json tag 要明确。
//
// 秘密边界：Provider/Model 展示 DTO（ProviderItem / ModelItem）只携带掩码与
// hasApiKey，绝不携带明文 APIKey。明文 APIKey 只存在于执行侧契约 ResolvedModel
// 中，而 ResolveTarget 是 Go 内部解析口，不通过 Wails 绑定暴露给前端。
package llm_provider_svc

// ModelTarget 是跨实体持久化的稳定目标身份（Backend / Session / Route 引用）。
//
// 只持久化 providerKey / modelKey：
//   - 两个 key 都为空表示 native / inherit-agent / inherit-main（由消费方语义决定）；
//   - ProviderKey 非空且 ModelKey 为空表示 provider-default，运行时每轮解析当前默认模型；
//   - 两个 key 都非空表示 fixed-model，运行时解析指定 Model 记录。
type ModelTarget struct {
	ProviderKey string `json:"providerKey"`
	ModelKey    string `json:"modelKey"`
}

// ProviderItem 单条供应商配置（脱敏后）。
//
// 注意：apiKey 仅在创建 / 更新请求中由前端传入；List / 更新响应返回 maskedApiKey
// 替代，避免把明文 key 暴露到日志、IPC trace 或 React DevTools 中。
type ProviderItem struct {
	ID              int64  `json:"id"`
	Type            string `json:"type"`
	ProviderKey     string `json:"providerKey"`
	Name            string `json:"name"`
	BaseURL         string `json:"baseUrl"`
	MaskedAPIKey    string `json:"maskedApiKey"`
	HasAPIKey       bool   `json:"hasApiKey"`
	Enabled         bool   `json:"enabled"`
	DefaultModelKey string `json:"defaultModelKey"`
	ModelCount      int64  `json:"modelCount"`
	Createtime      int64  `json:"createtime"`
	Updatetime      int64  `json:"updatetime"`
}

// ModelItem 一条已持久化的稳定模型（脱敏后，不含任何凭证）。
type ModelItem struct {
	ID            int64  `json:"id"`
	ProviderID    int64  `json:"providerId"`
	ProviderKey   string `json:"providerKey"`
	ModelKey      string `json:"modelKey"`
	ModelID       string `json:"modelId"`
	Name          string `json:"name"`
	ContextWindow int    `json:"contextWindow"`
	MaxOutput     int    `json:"maxOutput"`
	Enabled       bool   `json:"enabled"`
	IsDefault     bool   `json:"isDefault"`
	Createtime    int64  `json:"createtime"`
	Updatetime    int64  `json:"updatetime"`
}

// ReferenceCounts 一个 Provider / Model 被 Backend / Session / Route 引用的影响计数。
// 修改默认模型、编辑被引用 Model 的 ModelID、删除被引用的 Provider / Model 前，
// 前端先展示该计数再二次确认。
type ReferenceCounts struct {
	Backends int64 `json:"backends"`
	Sessions int64 `json:"sessions"`
	Routes   int64 `json:"routes"`
}

// ResolvedModel 执行侧解析结果（EffectiveLLMConfig 的 Provider/Model 部分）。
//
// 这不是展示 DTO：它携带明文 APIKey / BaseURL 供 Backend、Chat、Gateway 与远端
// 执行使用。前端展示永远走脱敏的 ProviderItem / ModelItem，不调用 ResolveTarget。
type ResolvedModel struct {
	ProviderKey   string `json:"providerKey"`
	ModelKey      string `json:"modelKey"`
	ProviderType  string `json:"providerType"`
	ModelID       string `json:"modelId"`
	ContextWindow int    `json:"contextWindow"`
	MaxOutput     int    `json:"maxOutput"`
	BaseURL       string `json:"baseUrl"`
	APIKey        string `json:"apiKey"`
	HasAPIKey     bool   `json:"hasApiKey"`
}

// ── Provider 管理 ──

// ListProvidersRequest 入参占位。
type ListProvidersRequest struct{}

// ListProvidersResponse 列出全部未删除的供应商（含停用，供重新启用）。
type ListProvidersResponse struct {
	Items []*ProviderItem `json:"items"`
}

// ModelInput 创建 / 批量导入时提交的一个模型（ModelKey 由服务端 mint，不来自前端）。
type ModelInput struct {
	ModelID       string `json:"modelId" binding:"required"`
	Name          string `json:"name"`
	ContextWindow int    `json:"contextWindow"`
	MaxOutput     int    `json:"maxOutput"`
}

// CreateProviderRequest 新建供应商：连接配置 + 选中的 Models + 默认模型，作为一个业务操作。
// DefaultModelID 指明 Models 中哪个 ModelID 设为默认；留空则 Provider 先以停用态创建。
type CreateProviderRequest struct {
	Type           string        `json:"type" binding:"required"`
	Name           string        `json:"name" binding:"required"`
	APIKey         string        `json:"apiKey"`
	BaseURL        string        `json:"baseUrl"`
	Models         []*ModelInput `json:"models"`
	DefaultModelID string        `json:"defaultModelId"`
}

// CreateProviderResponse 返回创建后的实体。
type CreateProviderResponse struct {
	Item *ProviderItem `json:"item"`
}

// UpdateProviderRequest 更新供应商连接配置。APIKey 留空表示沿用既有值。
type UpdateProviderRequest struct {
	ID      int64  `json:"id" binding:"required"`
	Name    string `json:"name" binding:"required"`
	APIKey  string `json:"apiKey"`
	BaseURL string `json:"baseUrl"`
}

// UpdateProviderResponse 返回更新后的实体。
type UpdateProviderResponse struct {
	Item *ProviderItem `json:"item"`
}

// SetProviderEnabledRequest 启用 / 停用供应商。
// 启用前必须已存在属于该供应商的启用默认模型；停用不受引用限制。
type SetProviderEnabledRequest struct {
	ID      int64 `json:"id" binding:"required"`
	Enabled bool  `json:"enabled"`
}

// SetProviderEnabledResponse 返回更新后的实体。
type SetProviderEnabledResponse struct {
	Item *ProviderItem `json:"item"`
}

// DeleteProviderRequest 软删除供应商。被 Backend / Session / Route 引用时需要
// ConfirmReference=true —— 引用不阻止删除，只要求调用方先看过影响。
type DeleteProviderRequest struct {
	ID               int64 `json:"id" binding:"required"`
	ConfirmReference bool  `json:"confirmReference"`
}

// DeleteProviderResponse 占位返回。
type DeleteProviderResponse struct{}

// ProviderRefCountsRequest 查一个 Provider 的引用影响计数。
type ProviderRefCountsRequest struct {
	ProviderKey string `json:"providerKey" binding:"required"`
}

// ProviderRefCountsResponse 一个 Provider 的引用影响计数。
type ProviderRefCountsResponse struct {
	Counts *ReferenceCounts `json:"counts"`
}

// ── Model 管理 ──

// ListModelsRequest 列出某 Provider 已持久化的模型。ID 为 Provider 行 id。
type ListModelsRequest struct {
	ID int64 `json:"id" binding:"required"`
}

// ListModelsResponse 已持久化模型列表（含 isDefault / enabled）。
type ListModelsResponse struct {
	Items []*ModelItem `json:"items"`
}

// ImportModelsRequest 原子批量导入某 Provider 的一组模型（发现结果人工确认后落地，
// 也允许手工添加上游未列出的模型）。已存在的同名 ModelID 保留原 ModelKey，且不覆盖
// 用户维护的非空元数据；仅本地字段为空时用提交值补齐。
type ImportModelsRequest struct {
	ProviderID int64         `json:"providerId" binding:"required"`
	Models     []*ModelInput `json:"models" binding:"required"`
}

// ImportModelsResponse 返回导入后的全量模型列表与新增 / 补齐计数。
type ImportModelsResponse struct {
	Items    []*ModelItem `json:"items"`
	Imported int          `json:"imported"`
	Updated  int          `json:"updated"`
}

// UpdateModelRequest 编辑一个模型的元数据。ModelID 被引用时修改需要 ConfirmReference=true。
type UpdateModelRequest struct {
	ID               int64  `json:"id" binding:"required"`
	ModelID          string `json:"modelId"`
	Name             string `json:"name"`
	ContextWindow    int    `json:"contextWindow"`
	MaxOutput        int    `json:"maxOutput"`
	ConfirmReference bool   `json:"confirmReference"`
}

// UpdateModelResponse 返回更新后的模型。
type UpdateModelResponse struct {
	Item *ModelItem `json:"item"`
}

// SetModelDefaultRequest 把某 Provider 的一个启用模型设为默认（并顺带启用 Provider）。
type SetModelDefaultRequest struct {
	ProviderID int64  `json:"providerId" binding:"required"`
	ModelKey   string `json:"modelKey" binding:"required"`
}

// SetModelDefaultResponse 返回更新后的 Provider。
type SetModelDefaultResponse struct {
	Item *ProviderItem `json:"item"`
}

// SetModelEnabledRequest 启用 / 停用一个模型。默认模型不能停用。
type SetModelEnabledRequest struct {
	ID      int64 `json:"id" binding:"required"`
	Enabled bool  `json:"enabled"`
}

// SetModelEnabledResponse 返回更新后的模型。
type SetModelEnabledResponse struct {
	Item *ModelItem `json:"item"`
}

// DeleteModelRequest 软删除一个模型。默认模型始终拒绝；被引用时需要
// ConfirmReference=true —— 引用不阻止删除，只要求调用方先看过影响。
type DeleteModelRequest struct {
	ID               int64 `json:"id" binding:"required"`
	ConfirmReference bool  `json:"confirmReference"`
}

// DeleteModelResponse 占位返回。
type DeleteModelResponse struct{}

// ModelRefCountsRequest 查一个 Model 的引用影响计数。
type ModelRefCountsRequest struct {
	ModelKey string `json:"modelKey" binding:"required"`
}

// ModelRefCountsResponse 一个 Model 的引用影响计数。
type ModelRefCountsResponse struct {
	Counts *ReferenceCounts `json:"counts"`
}

// ── 发现 / 目录 / 测试 ──

// ModelInfo 上游 /v1/models 发现结果 + cago 内置目录补全的展示元数据（瞬时，不持久化）。
// 发现只是人工导入建议：本次未返回的本地模型不自动停用或删除。
type ModelInfo struct {
	ID            string   `json:"id"`
	Vendor        string   `json:"vendor"`
	ContextWindow int      `json:"contextWindow"`
	MaxOutput     int      `json:"maxOutput"`
	Modalities    []string `json:"modalities"`
	Thinking      bool     `json:"thinking"`
	KnownInCago   bool     `json:"knownInCago"`
}

// PreviewModelsRequest 按用户填写的临时凭证拉取模型列表（发现建议）。
// ID 非零表示编辑已有 provider；此时 APIKey 留空会沿用已保存凭证，其余草稿
// 字段仍按当前表单值请求，供保存前验证。
type PreviewModelsRequest struct {
	ID      int64  `json:"id"`
	Type    string `json:"type" binding:"required"`
	APIKey  string `json:"apiKey"`
	BaseURL string `json:"baseUrl"`
}

// PreviewModelsResponse 同 ListModelsResponse（瞬时发现，不含持久化状态）。
type PreviewModelsResponse struct {
	Items []*ModelInfo `json:"items"`
}

// LookupModelRequest 仅按模型 id 查询 cago 内置目录元数据，不发出 HTTP 请求。
type LookupModelRequest struct {
	ID string `json:"id" binding:"required"`
}

// LookupModelResponse 命中目录则 Known=true 并填充上下文 / 最大输出；未命中也返回成功，Known=false。
type LookupModelResponse struct {
	Known         bool   `json:"known"`
	Vendor        string `json:"vendor"`
	ContextWindow int    `json:"contextWindow"`
	MaxOutput     int    `json:"maxOutput"`
}

// TestConnectionRequest 用明确目标执行一次真实最小调用（同能力两个入口）：
//   - 已保存配置（ID>0 且 UseDraft=false）：ModelKey 空 → 测当前默认模型；
//     ModelKey 具体值 → 测该子模型；
//   - 草稿配置（UseDraft=true 或 ID=0）：直接按 ModelID 测试，空则报错。
type TestConnectionRequest struct {
	ID       int64  `json:"id"`
	UseDraft bool   `json:"useDraft"`
	Type     string `json:"type"`
	APIKey   string `json:"apiKey"`
	BaseURL  string `json:"baseUrl"`
	ModelKey string `json:"modelKey"`
	ModelID  string `json:"modelId"`
}

// TestConnectionResponse 报告测试结果。OK = false 时 Message 携带原因；OK = true 时 Message 携带成功说明。
type TestConnectionResponse struct {
	OK         bool   `json:"ok"`
	Message    string `json:"message"`
	ModelCount int    `json:"modelCount"`
}
