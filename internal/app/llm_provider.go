package app

import (
	"github.com/agentre-ai/agentre/internal/service/llm_provider_svc"
)

// ListLLMProviders 列出全部 LLM 供应商配置（脱敏，含停用态供重新启用）。
func (a *App) ListLLMProviders() (*llm_provider_svc.ListProvidersResponse, error) {
	return llm_provider_svc.LLMProvider().List(a.ctx, &llm_provider_svc.ListProvidersRequest{})
}

// CreateLLMProvider 一个业务操作新建供应商 + 选中 Models + 默认模型，事务落库。
func (a *App) CreateLLMProvider(req *llm_provider_svc.CreateProviderRequest) (*llm_provider_svc.CreateProviderResponse, error) {
	return llm_provider_svc.LLMProvider().Create(a.ctx, req)
}

// UpdateLLMProvider 更新供应商连接配置；apiKey 留空保留原值。
func (a *App) UpdateLLMProvider(req *llm_provider_svc.UpdateProviderRequest) (*llm_provider_svc.UpdateProviderResponse, error) {
	return llm_provider_svc.LLMProvider().Update(a.ctx, req)
}

// SetLLMProviderEnabled 启用 / 停用供应商；启用前必须已有属于它的启用默认模型。
func (a *App) SetLLMProviderEnabled(req *llm_provider_svc.SetProviderEnabledRequest) (*llm_provider_svc.SetProviderEnabledResponse, error) {
	return llm_provider_svc.LLMProvider().SetProviderEnabled(a.ctx, req)
}

// DeleteLLMProvider 软删除供应商；被 Backend / Session / Route 引用时需 ConfirmReference=true。
func (a *App) DeleteLLMProvider(req *llm_provider_svc.DeleteProviderRequest) (*llm_provider_svc.DeleteProviderResponse, error) {
	return llm_provider_svc.LLMProvider().Delete(a.ctx, req)
}

// LLMProviderRefCounts 查一个供应商的引用影响计数（供删除 / 改默认前确认）。
func (a *App) LLMProviderRefCounts(req *llm_provider_svc.ProviderRefCountsRequest) (*llm_provider_svc.ProviderRefCountsResponse, error) {
	return llm_provider_svc.LLMProvider().ProviderRefCounts(a.ctx, req)
}

// ListLLMModels 列出某供应商已持久化的模型（含 enabled / isDefault），不含凭证。
func (a *App) ListLLMModels(req *llm_provider_svc.ListModelsRequest) (*llm_provider_svc.ListModelsResponse, error) {
	return llm_provider_svc.LLMProvider().ListModels(a.ctx, req)
}

// ImportLLMModels 原子批量导入某供应商的一组模型；已存在模型保留稳定 key 且不覆盖非空元数据。
func (a *App) ImportLLMModels(req *llm_provider_svc.ImportModelsRequest) (*llm_provider_svc.ImportModelsResponse, error) {
	return llm_provider_svc.LLMProvider().ImportModels(a.ctx, req)
}

// UpdateLLMModel 编辑一个模型的元数据；ModelID 被引用时修改需 ConfirmReference=true。
func (a *App) UpdateLLMModel(req *llm_provider_svc.UpdateModelRequest) (*llm_provider_svc.UpdateModelResponse, error) {
	return llm_provider_svc.LLMProvider().UpdateModel(a.ctx, req)
}

// SetLLMModelDefault 把某供应商的一个启用模型设为默认，并顺带启用供应商。
func (a *App) SetLLMModelDefault(req *llm_provider_svc.SetModelDefaultRequest) (*llm_provider_svc.SetModelDefaultResponse, error) {
	return llm_provider_svc.LLMProvider().SetModelDefault(a.ctx, req)
}

// SetLLMModelEnabled 启用 / 停用一个模型；默认模型不能停用。
func (a *App) SetLLMModelEnabled(req *llm_provider_svc.SetModelEnabledRequest) (*llm_provider_svc.SetModelEnabledResponse, error) {
	return llm_provider_svc.LLMProvider().SetModelEnabled(a.ctx, req)
}

// DeleteLLMModel 软删除一个模型；默认模型拒绝，被引用时需 ConfirmReference=true。
func (a *App) DeleteLLMModel(req *llm_provider_svc.DeleteModelRequest) (*llm_provider_svc.DeleteModelResponse, error) {
	return llm_provider_svc.LLMProvider().DeleteModel(a.ctx, req)
}

// LLMModelRefCounts 查一个模型的引用影响计数（供删除 / 改 ModelID 前确认）。
func (a *App) LLMModelRefCounts(req *llm_provider_svc.ModelRefCountsRequest) (*llm_provider_svc.ModelRefCountsResponse, error) {
	return llm_provider_svc.LLMProvider().ModelRefCounts(a.ctx, req)
}

// PreviewLLMModels 按表单草稿凭证拉取模型列表（发现建议，瞬时）；编辑时空 apiKey 沿用已保存值。
func (a *App) PreviewLLMModels(req *llm_provider_svc.PreviewModelsRequest) (*llm_provider_svc.PreviewModelsResponse, error) {
	return llm_provider_svc.LLMProvider().PreviewModels(a.ctx, req)
}

// LookupLLMModel 按模型 id 查 cago 内置 catalog 的默认上下文 / 最大输出，无网络请求。
func (a *App) LookupLLMModel(req *llm_provider_svc.LookupModelRequest) (*llm_provider_svc.LookupModelResponse, error) {
	return llm_provider_svc.LLMProvider().LookupModel(a.ctx, req)
}

// TestLLMProvider 用明确目标执行一次真实最小调用（空 modelKey 测默认，具体 modelKey 测子模型）。
func (a *App) TestLLMProvider(req *llm_provider_svc.TestConnectionRequest) (*llm_provider_svc.TestConnectionResponse, error) {
	return llm_provider_svc.LLMProvider().TestConnection(a.ctx, req)
}
