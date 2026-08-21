// Package llm_provider_svc 暴露 LLM 供应商的应用服务实现。
//
// 编排规则（全部落在 service 层，仓储只做原子落库）：
//   - 创建 = 连接配置 + 选中 Models + 默认模型一个业务操作（CreateWithModels 事务）；
//   - 发现只做人工导入建议，永不自动删改本地模型；
//   - 改默认 / 删除 / 修改被引用 ModelID 前先算引用影响：改默认与删除只要求二次确认，
//     删除后引用方保持原样并降级为「目标已失效」；
//   - 启用 Provider 必须已有属于它的启用默认模型；
//   - 展示 DTO 只带掩码 key，明文 key 只存在于执行侧契约 ResolvedModel。
package llm_provider_svc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cago-frame/agents/provider/models"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/pkg/code"
	"github.com/agentre-ai/agentre/internal/pkg/llmcatalog"
	"github.com/agentre-ai/agentre/internal/pkg/llmurl"
	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
	"github.com/agentre-ai/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-ai/agentre/internal/service/sync_svc"
)

// 默认 endpoint。BaseURL 留空时使用。
const (
	defaultAnthropicBaseURL = "https://api.anthropic.com"
	defaultOpenAIBaseURL    = "https://api.openai.com/v1"
	testConnectionPrompt    = "hi"
	testConnectionMaxTokens = 16
	// anthropicVersion Anthropic Messages / Models API 必填的版本头。
	// 与 cago agents/provider/anthropics 当前 SDK 使用的版本对齐。
	anthropicVersion = "2023-06-01"
)

// httpDoer 抽象 http.Client，方便在单测里替换实现。
type httpDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// LLMProviderSvc LLM 供应商及其稳定模型的应用服务。
//
// ResolveTarget 是 Go 内部执行侧解析口（Backend / Chat / Gateway / Remote 消费），
// 不通过 Wails 绑定暴露给前端。
type LLMProviderSvc interface {
	List(ctx context.Context, req *ListProvidersRequest) (*ListProvidersResponse, error)
	Create(ctx context.Context, req *CreateProviderRequest) (*CreateProviderResponse, error)
	Update(ctx context.Context, req *UpdateProviderRequest) (*UpdateProviderResponse, error)
	Delete(ctx context.Context, req *DeleteProviderRequest) (*DeleteProviderResponse, error)
	SetProviderEnabled(ctx context.Context, req *SetProviderEnabledRequest) (*SetProviderEnabledResponse, error)
	ProviderRefCounts(ctx context.Context, req *ProviderRefCountsRequest) (*ProviderRefCountsResponse, error)

	ListModels(ctx context.Context, req *ListModelsRequest) (*ListModelsResponse, error)
	ImportModels(ctx context.Context, req *ImportModelsRequest) (*ImportModelsResponse, error)
	UpdateModel(ctx context.Context, req *UpdateModelRequest) (*UpdateModelResponse, error)
	SetModelDefault(ctx context.Context, req *SetModelDefaultRequest) (*SetModelDefaultResponse, error)
	SetModelEnabled(ctx context.Context, req *SetModelEnabledRequest) (*SetModelEnabledResponse, error)
	DeleteModel(ctx context.Context, req *DeleteModelRequest) (*DeleteModelResponse, error)
	ModelRefCounts(ctx context.Context, req *ModelRefCountsRequest) (*ModelRefCountsResponse, error)

	PreviewModels(ctx context.Context, req *PreviewModelsRequest) (*PreviewModelsResponse, error)
	TestConnection(ctx context.Context, req *TestConnectionRequest) (*TestConnectionResponse, error)
	LookupModel(ctx context.Context, req *LookupModelRequest) (*LookupModelResponse, error)
	ResolveTarget(ctx context.Context, target ModelTarget) (*ResolvedModel, error)
}

type llmProviderSvc struct {
	http httpDoer
	now  func() int64
}

var defaultLLMProvider LLMProviderSvc = &llmProviderSvc{
	http: &http.Client{Timeout: 15 * time.Second},
	now:  func() int64 { return time.Now().Unix() },
}

// LLMProvider 取默认服务单例。
func LLMProvider() LLMProviderSvc { return defaultLLMProvider }

func (s *llmProviderSvc) List(ctx context.Context, _ *ListProvidersRequest) (*ListProvidersResponse, error) {
	rows, err := llm_provider_repo.LLMProvider().List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]*ProviderItem, 0, len(rows))
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		items = append(items, toItem(row))
		ids = append(ids, row.ID)
	}
	counts, err := llm_provider_repo.LLMProvider().CountModelsByProvider(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		item.ModelCount = counts[item.ID]
	}
	return &ListProvidersResponse{Items: items}, nil
}

// Create 一个业务操作完成 Provider + 选中 Models + 默认模型，事务落库（CreateWithModels）。
// DefaultModelID 必须在 Models 内；留空则 Provider 以停用态创建。
func (s *llmProviderSvc) Create(ctx context.Context, req *CreateProviderRequest) (*CreateProviderResponse, error) {
	now := s.now()
	p := &llm_provider_entity.LLMProvider{
		Type:        strings.TrimSpace(req.Type),
		Name:        strings.TrimSpace(req.Name),
		ProviderKey: uuid.NewString(),
		APIKey:      strings.TrimSpace(req.APIKey),
		BaseURL:     strings.TrimSpace(req.BaseURL),
		Enabled:     llm_provider_entity.EnabledOff,
		Status:      consts.ACTIVE,
		Createtime:  now,
		Updatetime:  now,
	}
	// provider_key is the stable sync identity; never mint a second ID for it.
	p.SyncMeta = syncmeta_entity.SyncMeta{SyncID: p.ProviderKey}
	if err := p.Check(ctx); err != nil {
		return nil, err
	}

	models, defaultKey, err := buildCreateModels(ctx, req, p.ID, now)
	if err != nil {
		return nil, err
	}
	p.DefaultModelKey = defaultKey
	if defaultKey != "" {
		p.Enabled = llm_provider_entity.EnabledOn
	}

	exist, err := llm_provider_repo.LLMProvider().FindByName(ctx, p.Name)
	if err != nil {
		return nil, err
	}
	if exist != nil {
		return nil, i18n.NewError(ctx, code.LLMProviderNameDuplicated)
	}

	if err := llm_provider_repo.LLMProvider().CreateWithModels(ctx, p, models, defaultKey); err != nil {
		return nil, err
	}
	sync_svc.NotifyCreate(ctx, syncwire.KindLLMProvider, p.ID, p.SyncMeta)
	logger.Ctx(ctx).Info("llmProviderSvc.Create: provider created",
		zap.Int64("id", p.ID),
		zap.String("providerKey", p.ProviderKey),
		zap.String("providerType", p.Type),
		zap.Int("modelCount", len(models)),
		zap.Bool("enabled", p.IsEnabled()),
		zap.String("maskedApiKey", p.MaskedAPIKey()))
	return &CreateProviderResponse{Item: toItem(p)}, nil
}

// buildCreateModels 把请求里的 ModelInput 转成实体，mint 稳定 ModelKey，并校验默认模型
// 必须属于这批模型。不落库；Create 在名称查重后原子落库。
func buildCreateModels(ctx context.Context, req *CreateProviderRequest, providerID int64, now int64) ([]*llm_provider_model_entity.LLMProviderModel, string, error) {
	models := make([]*llm_provider_model_entity.LLMProviderModel, 0, len(req.Models))
	keyByModelID := make(map[string]string, len(req.Models))
	for _, mi := range req.Models {
		m := &llm_provider_model_entity.LLMProviderModel{
			ProviderID:    providerID,
			ModelKey:      uuid.NewString(),
			ModelID:       strings.TrimSpace(mi.ModelID),
			Name:          strings.TrimSpace(mi.Name),
			ContextWindow: clampTokens(mi.ContextWindow),
			MaxOutput:     clampTokens(mi.MaxOutput),
			Enabled:       llm_provider_model_entity.EnabledOn,
			Status:        consts.ACTIVE,
			Createtime:    now,
			Updatetime:    now,
		}
		if err := m.Check(ctx); err != nil {
			return nil, "", err
		}
		models = append(models, m)
		keyByModelID[m.ModelID] = m.ModelKey
	}
	defaultKey := ""
	if strings.TrimSpace(req.DefaultModelID) != "" {
		key, ok := keyByModelID[strings.TrimSpace(req.DefaultModelID)]
		if !ok {
			return nil, "", i18n.NewError(ctx, code.InvalidParameter)
		}
		defaultKey = key
	}
	return models, defaultKey, nil
}

// Update 更新连接配置；APIKey 留空表示沿用既有值（不清空已保存凭证）。
func (s *llmProviderSvc) Update(ctx context.Context, req *UpdateProviderRequest) (*UpdateProviderResponse, error) {
	p, err := llm_provider_repo.LLMProvider().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
	}

	newName := strings.TrimSpace(req.Name)
	if newName != p.Name {
		exist, err := llm_provider_repo.LLMProvider().FindByName(ctx, newName)
		if err != nil {
			return nil, err
		}
		if exist != nil && exist.ID != p.ID {
			return nil, i18n.NewError(ctx, code.LLMProviderNameDuplicated)
		}
	}

	p.Name = newName
	p.BaseURL = strings.TrimSpace(req.BaseURL)
	if newKey := strings.TrimSpace(req.APIKey); newKey != "" {
		p.APIKey = newKey
	}
	p.Updatetime = s.now()

	if err := p.Check(ctx); err != nil {
		return nil, err
	}
	if err := llm_provider_repo.LLMProvider().Update(ctx, p); err != nil {
		return nil, err
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindLLMProvider, p.ID, p.SyncMeta)
	logger.Ctx(ctx).Info("llmProviderSvc.Update: provider updated",
		zap.Int64("id", p.ID),
		zap.String("providerKey", p.ProviderKey),
		zap.String("maskedApiKey", p.MaskedAPIKey()))
	return &UpdateProviderResponse{Item: toItem(p)}, nil
}

// SetProviderEnabled 启用 / 停用 Provider。启用前必须已有属于该 Provider 的启用默认模型；
// 停用不做引用检查（被引用的 Provider 允许停用）。
func (s *llmProviderSvc) SetProviderEnabled(ctx context.Context, req *SetProviderEnabledRequest) (*SetProviderEnabledResponse, error) {
	p, err := llm_provider_repo.LLMProvider().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
	}

	if req.Enabled {
		if !p.HasDefaultModel() {
			return nil, i18n.NewError(ctx, code.LLMProviderNoEnabledDefault)
		}
		m, err := llm_provider_repo.LLMProvider().FindModelByKey(ctx, p.DefaultModelKey)
		if err != nil {
			return nil, err
		}
		if m == nil || !m.IsEnabled() {
			return nil, i18n.NewError(ctx, code.LLMProviderNoEnabledDefault)
		}
	}

	if req.Enabled {
		p.Enabled = llm_provider_entity.EnabledOn
	} else {
		p.Enabled = llm_provider_entity.EnabledOff
	}
	p.Updatetime = s.now()
	if err := llm_provider_repo.LLMProvider().Update(ctx, p); err != nil {
		return nil, err
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindLLMProvider, p.ID, p.SyncMeta)
	logger.Ctx(ctx).Info("llmProviderSvc.SetProviderEnabled: provider toggled",
		zap.Int64("id", p.ID),
		zap.String("providerKey", p.ProviderKey),
		zap.Bool("enabled", p.IsEnabled()))
	return &SetProviderEnabledResponse{Item: toItem(p)}, nil
}

// Delete 软删除 Provider 及其全部 Models。被 Backend / Session / Route 引用不阻止删除，
// 只要求 ConfirmReference=true —— 调用方先看过影响再删；删除后引用方一行不改，由既有的
// 「目标已失效」语义承接（fixed-model 严格阻止下一轮、provider-default 回退 Agent 绑定）。
func (s *llmProviderSvc) Delete(ctx context.Context, req *DeleteProviderRequest) (*DeleteProviderResponse, error) {
	p, err := llm_provider_repo.LLMProvider().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
	}

	refs, err := llm_provider_repo.LLMProvider().CountProviderReferences(ctx, p.ProviderKey)
	if err != nil {
		return nil, err
	}
	referenced := refs.Backends > 0 || refs.Sessions > 0 || refs.Routes > 0
	if referenced && !req.ConfirmReference {
		return nil, i18n.NewError(ctx, code.LLMProviderReferenced)
	}

	// Provider 与其 Models 在同一事务内一并软删除，不留半批。
	if err := llm_provider_repo.LLMProvider().DeleteWithModels(ctx, p.ID); err != nil {
		return nil, err
	}
	sync_svc.NotifyDelete(ctx, syncwire.KindLLMProvider, p.ID, p.SyncMeta)
	logger.Ctx(ctx).Info("llmProviderSvc.Delete: provider deleted",
		zap.Int64("id", p.ID),
		zap.String("providerKey", p.ProviderKey),
		zap.Int64("refBackends", refs.Backends),
		zap.Int64("refSessions", refs.Sessions),
		zap.Int64("refRoutes", refs.Routes))
	return &DeleteProviderResponse{}, nil
}

// ProviderRefCounts 透传一个 Provider 的引用影响计数。
func (s *llmProviderSvc) ProviderRefCounts(ctx context.Context, req *ProviderRefCountsRequest) (*ProviderRefCountsResponse, error) {
	counts, err := llm_provider_repo.LLMProvider().CountProviderReferences(ctx, req.ProviderKey)
	if err != nil {
		return nil, err
	}
	return &ProviderRefCountsResponse{Counts: providerRefCountsToDTO(counts)}, nil
}

// ListModels 列出某 Provider 已持久化的模型（含 enabled / isDefault），不含任何凭证。
func (s *llmProviderSvc) ListModels(ctx context.Context, req *ListModelsRequest) (*ListModelsResponse, error) {
	p, err := llm_provider_repo.LLMProvider().Find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
	}
	rows, err := llm_provider_repo.LLMProvider().ListModels(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	items := make([]*ModelItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, toModelItem(row, p))
	}
	return &ListModelsResponse{Items: items}, nil
}

// ImportModels 原子导入某 Provider 的一组模型。
// 已存在的同名 ModelID 保留原 ModelKey，且不覆盖用户维护的非空元数据；仅本地字段为空时
// 用提交值补齐；新模型 mint 稳定 ModelKey。补齐与新增同一次 ImportModels 事务落库，
// 任一步失败整体回滚，不留半批。
func (s *llmProviderSvc) ImportModels(ctx context.Context, req *ImportModelsRequest) (*ImportModelsResponse, error) {
	p, err := llm_provider_repo.LLMProvider().Find(ctx, req.ProviderID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
	}

	existing, err := llm_provider_repo.LLMProvider().ListModels(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	existingByModelID := make(map[string]*llm_provider_model_entity.LLMProviderModel, len(existing))
	for _, m := range existing {
		existingByModelID[m.ModelID] = m
	}

	now := s.now()
	var updates, toImport []*llm_provider_model_entity.LLMProviderModel
	updated := 0
	for _, mi := range req.Models {
		modelID := strings.TrimSpace(mi.ModelID)
		if cur, ok := existingByModelID[modelID]; ok {
			if fillModelGaps(ctx, cur, mi, now) {
				updates = append(updates, cur)
				updated++
			}
			continue
		}
		m := &llm_provider_model_entity.LLMProviderModel{
			ProviderID:    p.ID,
			ModelKey:      uuid.NewString(),
			ModelID:       modelID,
			Name:          strings.TrimSpace(mi.Name),
			ContextWindow: clampTokens(mi.ContextWindow),
			MaxOutput:     clampTokens(mi.MaxOutput),
			Enabled:       llm_provider_model_entity.EnabledOn,
			Status:        consts.ACTIVE,
			Createtime:    now,
			Updatetime:    now,
		}
		if err := m.Check(ctx); err != nil {
			return nil, err
		}
		toImport = append(toImport, m)
	}
	// 补齐与新增同一次原子调用：任一步失败整体回滚，已存在行的补齐不会残留半批。
	if len(updates) > 0 || len(toImport) > 0 {
		if err := llm_provider_repo.LLMProvider().ImportModels(ctx, updates, toImport); err != nil {
			return nil, err
		}
	}

	// 返回合并后的全量列表（已存在 + 新导入），保持稳定 key。
	all := make([]*llm_provider_model_entity.LLMProviderModel, 0, len(existing)+len(toImport))
	all = append(all, existing...)
	all = append(all, toImport...)
	items := make([]*ModelItem, 0, len(all))
	for _, row := range all {
		items = append(items, toModelItem(row, p))
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindLLMProvider, p.ID, p.SyncMeta)
	logger.Ctx(ctx).Info("llmProviderSvc.ImportModels: models imported",
		zap.Int64("providerID", p.ID),
		zap.Int("imported", len(toImport)),
		zap.Int("updated", updated))
	return &ImportModelsResponse{Items: items, Imported: len(toImport), Updated: updated}, nil
}

// fillModelGaps 仅补齐本地为空的字段（不覆盖用户维护的非空元数据），返回是否有改动。
func fillModelGaps(ctx context.Context, m *llm_provider_model_entity.LLMProviderModel, mi *ModelInput, now int64) bool {
	changed := false
	if m.Name == "" && strings.TrimSpace(mi.Name) != "" {
		m.Name = strings.TrimSpace(mi.Name)
		changed = true
	}
	if m.ContextWindow == 0 && mi.ContextWindow > 0 {
		m.ContextWindow = mi.ContextWindow
		changed = true
	}
	if m.MaxOutput == 0 && mi.MaxOutput > 0 {
		m.MaxOutput = mi.MaxOutput
		changed = true
	}
	if changed {
		m.Updatetime = now
	}
	return changed
}

// UpdateModel 编辑一个模型的元数据。ModelID 被引用时修改需要显式 ConfirmReference。
func (s *llmProviderSvc) UpdateModel(ctx context.Context, req *UpdateModelRequest) (*UpdateModelResponse, error) {
	m, err := llm_provider_repo.LLMProvider().FindModel(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderModelNotFound)
	}

	newModelID := strings.TrimSpace(req.ModelID)
	if newModelID != "" && newModelID != m.ModelID {
		refs, err := llm_provider_repo.LLMProvider().CountModelReferences(ctx, m.ModelKey)
		if err != nil {
			return nil, err
		}
		if (refs.Backends > 0 || refs.Sessions > 0 || refs.Routes > 0) && !req.ConfirmReference {
			return nil, i18n.NewError(ctx, code.LLMProviderModelConfirmRequired)
		}
	}

	if newModelID != "" {
		m.ModelID = newModelID
	}
	if strings.TrimSpace(req.Name) != "" {
		m.Name = strings.TrimSpace(req.Name)
	}
	if req.ContextWindow >= 0 {
		m.ContextWindow = req.ContextWindow
	}
	if req.MaxOutput >= 0 {
		m.MaxOutput = req.MaxOutput
	}
	m.Updatetime = s.now()

	if err := m.Check(ctx); err != nil {
		return nil, err
	}
	if err := llm_provider_repo.LLMProvider().UpdateModel(ctx, m); err != nil {
		return nil, err
	}
	s.notifyProviderUpdate(ctx, m.ProviderID)
	logger.Ctx(ctx).Info("llmProviderSvc.UpdateModel: model updated",
		zap.Int64("id", m.ID),
		zap.String("modelKey", m.ModelKey),
		zap.String("modelId", m.ModelID))
	return &UpdateModelResponse{Item: toModelItem(m, nil)}, nil
}

// notifyProviderUpdate turns a nested model mutation into its parent provider
// sync event. Service tests leave sync unassembled, so no needless parent read
// occurs outside the real synchronization boundary.
func (s *llmProviderSvc) notifyProviderUpdate(ctx context.Context, providerID int64) {
	if !sync_svc.Active() {
		return
	}
	provider, err := llm_provider_repo.LLMProvider().Find(ctx, providerID)
	if err != nil || provider == nil {
		logger.Ctx(ctx).Warn("llmProviderSvc.notifyProviderUpdate: provider unavailable after model mutation", zap.Int64("providerId", providerID), zap.Error(err))
		return
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindLLMProvider, provider.ID, provider.SyncMeta)
}

// SetModelDefault 把某 Provider 的一个启用模型设为默认，并顺带启用 Provider。
func (s *llmProviderSvc) SetModelDefault(ctx context.Context, req *SetModelDefaultRequest) (*SetModelDefaultResponse, error) {
	p, err := llm_provider_repo.LLMProvider().Find(ctx, req.ProviderID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
	}
	m, err := llm_provider_repo.LLMProvider().FindModelByKey(ctx, req.ModelKey)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderModelNotFound)
	}
	if m.ProviderID != p.ID {
		return nil, i18n.NewError(ctx, code.LLMProviderModelNotOwned)
	}
	if !m.IsEnabled() {
		return nil, i18n.NewError(ctx, code.LLMProviderModelDisabled)
	}

	p.DefaultModelKey = m.ModelKey
	p.Enabled = llm_provider_entity.EnabledOn
	p.Updatetime = s.now()
	if err := llm_provider_repo.LLMProvider().Update(ctx, p); err != nil {
		return nil, err
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindLLMProvider, p.ID, p.SyncMeta)
	logger.Ctx(ctx).Info("llmProviderSvc.SetModelDefault: default model set",
		zap.Int64("id", p.ID),
		zap.String("providerKey", p.ProviderKey),
		zap.String("modelKey", p.DefaultModelKey))
	return &SetModelDefaultResponse{Item: toItem(p)}, nil
}

// SetModelEnabled 启用 / 停用一个模型。默认模型不能停用。
func (s *llmProviderSvc) SetModelEnabled(ctx context.Context, req *SetModelEnabledRequest) (*SetModelEnabledResponse, error) {
	m, err := llm_provider_repo.LLMProvider().FindModel(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderModelNotFound)
	}
	p, err := llm_provider_repo.LLMProvider().Find(ctx, m.ProviderID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
	}
	if !req.Enabled && p.DefaultModelKey == m.ModelKey {
		return nil, i18n.NewError(ctx, code.LLMProviderModelIsDefault)
	}

	if req.Enabled {
		m.Enabled = llm_provider_model_entity.EnabledOn
	} else {
		m.Enabled = llm_provider_model_entity.EnabledOff
	}
	m.Updatetime = s.now()
	if err := llm_provider_repo.LLMProvider().UpdateModel(ctx, m); err != nil {
		return nil, err
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindLLMProvider, p.ID, p.SyncMeta)
	logger.Ctx(ctx).Info("llmProviderSvc.SetModelEnabled: model toggled",
		zap.Int64("id", m.ID),
		zap.String("modelKey", m.ModelKey),
		zap.Bool("enabled", m.IsEnabled()))
	return &SetModelEnabledResponse{Item: toModelItem(m, p)}, nil
}

// DeleteModel 软删除一个模型。默认模型始终拒绝（Provider 自身不变量：删了它，该 Provider
// 下所有 provider-default 目标都解析不出模型）；被 Backend / Session / Route 引用不阻止删除，
// 只要求 ConfirmReference=true。
func (s *llmProviderSvc) DeleteModel(ctx context.Context, req *DeleteModelRequest) (*DeleteModelResponse, error) {
	m, err := llm_provider_repo.LLMProvider().FindModel(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderModelNotFound)
	}
	p, err := llm_provider_repo.LLMProvider().Find(ctx, m.ProviderID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
	}
	if p.DefaultModelKey == m.ModelKey {
		return nil, i18n.NewError(ctx, code.LLMProviderModelIsDefault)
	}
	refs, err := llm_provider_repo.LLMProvider().CountModelReferences(ctx, m.ModelKey)
	if err != nil {
		return nil, err
	}
	referenced := refs.Backends > 0 || refs.Sessions > 0 || refs.Routes > 0
	if referenced && !req.ConfirmReference {
		return nil, i18n.NewError(ctx, code.LLMProviderModelReferenced)
	}
	if err := llm_provider_repo.LLMProvider().DeleteModel(ctx, m.ID); err != nil {
		return nil, err
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindLLMProvider, p.ID, p.SyncMeta)
	logger.Ctx(ctx).Info("llmProviderSvc.DeleteModel: model deleted",
		zap.Int64("id", m.ID),
		zap.String("modelKey", m.ModelKey),
		zap.Int64("refBackends", refs.Backends),
		zap.Int64("refSessions", refs.Sessions),
		zap.Int64("refRoutes", refs.Routes))
	return &DeleteModelResponse{}, nil
}

// ModelRefCounts 透传一个 Model 的引用影响计数。
func (s *llmProviderSvc) ModelRefCounts(ctx context.Context, req *ModelRefCountsRequest) (*ModelRefCountsResponse, error) {
	counts, err := llm_provider_repo.LLMProvider().CountModelReferences(ctx, req.ModelKey)
	if err != nil {
		return nil, err
	}
	return &ModelRefCountsResponse{Counts: modelRefCountsToDTO(counts)}, nil
}

// ResolveTarget 把持久化目标解析成可执行配置。
//   - 空 ModelKey（provider-default）：解析当前启用的默认模型；
//   - 具体 ModelKey（fixed-model）：只解析该启用且归属本 Provider 的模型。
//
// 返回携带明文 APIKey / BaseURL 的执行侧契约，供 Backend / Chat / Gateway / Remote
// 使用；不通过 Wails 绑定暴露给前端。
func (s *llmProviderSvc) ResolveTarget(ctx context.Context, target ModelTarget) (*ResolvedModel, error) {
	if strings.TrimSpace(target.ProviderKey) == "" {
		// 双空 key 的 native / inherit 语义由消费方判定，不进入 Provider 解析。
		return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
	}
	p, err := llm_provider_repo.LLMProvider().FindByKey(ctx, target.ProviderKey)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
	}
	if !p.IsEnabled() {
		return nil, i18n.NewError(ctx, code.LLMProviderDisabled)
	}

	var m *llm_provider_model_entity.LLMProviderModel
	if strings.TrimSpace(target.ModelKey) != "" {
		m, err = llm_provider_repo.LLMProvider().FindModelByKey(ctx, target.ModelKey)
		if err != nil {
			return nil, err
		}
		if m == nil {
			return nil, i18n.NewError(ctx, code.LLMProviderModelNotFound)
		}
		if m.ProviderID != p.ID {
			return nil, i18n.NewError(ctx, code.LLMProviderModelNotOwned)
		}
		if !m.IsEnabled() {
			return nil, i18n.NewError(ctx, code.LLMProviderModelDisabled)
		}
	} else {
		if !p.HasDefaultModel() {
			return nil, i18n.NewError(ctx, code.LLMProviderDefaultModelInvalid)
		}
		m, err = llm_provider_repo.LLMProvider().FindModelByKey(ctx, p.DefaultModelKey)
		if err != nil {
			return nil, err
		}
		if m == nil || !m.IsEnabled() {
			return nil, i18n.NewError(ctx, code.LLMProviderDefaultModelInvalid)
		}
	}
	return &ResolvedModel{
		ProviderKey:   p.ProviderKey,
		ModelKey:      m.ModelKey,
		ProviderType:  p.Type,
		ModelID:       m.ModelID,
		ContextWindow: m.ContextWindow,
		MaxOutput:     m.MaxOutput,
		BaseURL:       p.BaseURL,
		APIKey:        p.APIKey,
		HasAPIKey:     p.APIKey != "",
	}, nil
}

// ── 发现 / 目录 / 测试（瞬时，不落库） ──

func (s *llmProviderSvc) LookupModel(_ context.Context, req *LookupModelRequest) (*LookupModelResponse, error) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return &LookupModelResponse{}, nil
	}
	info, ok := llmcatalog.Lookup(id)
	if !ok {
		return &LookupModelResponse{}, nil
	}
	return &LookupModelResponse{
		Known:         true,
		Vendor:        string(info.Vendor),
		ContextWindow: info.ContextWindow,
		MaxOutput:     info.MaxOutput,
	}, nil
}

// PreviewModels 按用户填写的临时凭证拉取模型列表（发现建议，瞬时不落库）。
// ID 非零表示编辑已有 provider；此时 APIKey 留空会沿用已保存凭证，其余草稿字段仍按当前表单值请求。
func (s *llmProviderSvc) PreviewModels(ctx context.Context, req *PreviewModelsRequest) (*PreviewModelsResponse, error) {
	var saved *llm_provider_entity.LLMProvider
	if req.ID > 0 {
		p, err := llm_provider_repo.LLMProvider().Find(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, i18n.NewError(ctx, code.LLMProviderNotFound)
		}
		saved = p
	}
	probe := mergeProviderDraft(saved, req.Type, req.APIKey, req.BaseURL)
	ids, err := s.fetchModelIDs(ctx, probe)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", i18n.NewError(ctx, code.LLMProviderFetchModels), err)
	}
	items := make([]*ModelInfo, 0, len(ids))
	for _, id := range ids {
		items = append(items, enrichModel(id, probe))
	}
	return &PreviewModelsResponse{Items: items}, nil
}

// TestConnection 用明确目标执行一次真实最小调用（同能力两个入口）：
//   - 已保存配置（ID>0 且 UseDraft=false）：ModelKey 空 → 测当前默认模型；
//     ModelKey 具体值 → 测该子模型；
//   - 草稿配置（UseDraft=true 或 ID=0）：直接按 ModelID 测试，空则报错。
func (s *llmProviderSvc) TestConnection(ctx context.Context, req *TestConnectionRequest) (*TestConnectionResponse, error) {
	p, modelID, err := s.providerForTest(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.sendTestMessage(ctx, p, modelID); err != nil {
		// 测试连通性时上游错误属于"用户可读结果"，不上抛 i18n error，让前端
		// 拿 message 展示。nilerr 的 lint 由此豁免。
		return &TestConnectionResponse{OK: false, Message: err.Error()}, nil //nolint:nilerr
	}
	logger.Ctx(ctx).Info("llmProviderSvc.TestConnection: ok",
		zap.Int64("id", req.ID),
		zap.String("providerType", p.Type),
		zap.String("modelId", modelID),
		zap.String("maskedApiKey", p.MaskedAPIKey()))
	return &TestConnectionResponse{OK: true, Message: "模型调用成功"}, nil
}

// providerForTest 解析 TestConnection 的目标 Provider + ModelID。已保存路径按 ModelKey
// 规则解析（空→默认，具体→子模型）；草稿路径直接按 ModelID 构造。
func (s *llmProviderSvc) providerForTest(ctx context.Context, req *TestConnectionRequest) (*llm_provider_entity.LLMProvider, string, error) {
	if req.ID > 0 && !req.UseDraft {
		p, err := llm_provider_repo.LLMProvider().Find(ctx, req.ID)
		if err != nil {
			return nil, "", err
		}
		if p == nil {
			return nil, "", i18n.NewError(ctx, code.LLMProviderNotFound)
		}
		modelKey := strings.TrimSpace(req.ModelKey)
		if modelKey == "" {
			if !p.HasDefaultModel() {
				// 未配置默认模型属配置问题：交给 sendTestMessage 产出 OK=false，不上抛错误。
				return p, "", nil
			}
			modelKey = p.DefaultModelKey
		}
		m, err := llm_provider_repo.LLMProvider().FindModelByKey(ctx, modelKey)
		if err != nil {
			return nil, "", err
		}
		if m == nil {
			return p, "", nil
		}
		return p, m.ModelID, nil
	}
	out := mergeProviderDraft(nil, req.Type, req.APIKey, req.BaseURL)
	return out, strings.TrimSpace(req.ModelID), nil
}

func mergeProviderDraft(saved *llm_provider_entity.LLMProvider, typ, apiKey, baseURL string) *llm_provider_entity.LLMProvider {
	out := &llm_provider_entity.LLMProvider{}
	if saved != nil {
		*out = *saved
	}
	if typ := strings.TrimSpace(typ); typ != "" {
		out.Type = typ
	}
	if key := strings.TrimSpace(apiKey); key != "" || saved == nil {
		out.APIKey = key
	}
	// BaseURL 只在表单显式填写时覆盖已保存值，避免编辑时空 baseUrl 清空已保存地址。
	if base := strings.TrimSpace(baseURL); base != "" || saved == nil {
		out.BaseURL = base
	}
	return out
}

// fetchModelIDs 调 provider 的 /v1/models endpoint，返回原始 id 列表。
// openai-chat 与 openai-response 共用 /v1/models —— OpenAI 的 models 接口不区分
// 是给 chat 还是 responses API 用的。
func (s *llmProviderSvc) fetchModelIDs(ctx context.Context, p *llm_provider_entity.LLMProvider) ([]string, error) {
	switch llm_provider_entity.ProviderType(p.Type) {
	case llm_provider_entity.TypeAnthropic:
		return s.fetchAnthropicModels(ctx, p)
	case llm_provider_entity.TypeOpenAIChat, llm_provider_entity.TypeOpenAIResponse:
		return s.fetchOpenAIModels(ctx, p)
	default:
		return nil, i18n.NewError(ctx, code.LLMProviderInvalidType)
	}
}

func (s *llmProviderSvc) fetchAnthropicModels(ctx context.Context, p *llm_provider_entity.LLMProvider) ([]string, error) {
	endpoint, err := llmurl.Build(firstNonEmpty(p.BaseURL, defaultAnthropicBaseURL), "/v1/models")
	if err != nil {
		return nil, err
	}
	return s.fetchModelList(ctx, endpoint.String(), func(h http.Header) {
		h.Set("x-api-key", p.APIKey)
		h.Set("anthropic-version", anthropicVersion)
	})
}

func (s *llmProviderSvc) fetchOpenAIModels(ctx context.Context, p *llm_provider_entity.LLMProvider) ([]string, error) {
	endpoint, err := llmurl.Build(firstNonEmpty(p.BaseURL, defaultOpenAIBaseURL), "/models")
	if err != nil {
		return nil, err
	}
	return s.fetchModelList(ctx, endpoint.String(), func(h http.Header) {
		if p.APIKey != "" {
			h.Set("Authorization", "Bearer "+p.APIKey)
		}
	})
}

// sendTestMessage 发送一条最小用户消息，验证模型不只是凭证可列，而是真的能完成一次 LLM 调用。
func (s *llmProviderSvc) sendTestMessage(ctx context.Context, p *llm_provider_entity.LLMProvider, modelID string) error {
	if strings.TrimSpace(modelID) == "" {
		return errors.New("请先选择默认模型")
	}
	switch llm_provider_entity.ProviderType(p.Type) {
	case llm_provider_entity.TypeAnthropic:
		return s.sendAnthropicTestMessage(ctx, p, modelID)
	case llm_provider_entity.TypeOpenAIChat:
		return s.sendOpenAITestMessage(ctx, p, modelID)
	case llm_provider_entity.TypeOpenAIResponse:
		return s.sendOpenAIResponseTestMessage(ctx, p, modelID)
	default:
		return i18n.NewError(ctx, code.LLMProviderInvalidType)
	}
}

func (s *llmProviderSvc) sendAnthropicTestMessage(ctx context.Context, p *llm_provider_entity.LLMProvider, modelID string) error {
	endpoint, err := llmurl.Build(firstNonEmpty(p.BaseURL, defaultAnthropicBaseURL), "/v1/messages")
	if err != nil {
		return err
	}
	payload := struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}{
		Model:     modelID,
		MaxTokens: testConnectionMaxTokens,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{Role: "user", Content: testConnectionPrompt},
		},
	}
	req, err := newJSONRequest(ctx, endpoint.String(), payload)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", p.APIKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
	}
	if err := s.doJSON(req, &resp); err != nil {
		return err
	}
	if len(resp.Content) == 0 && resp.StopReason == "" {
		return errors.New("empty completion response")
	}
	return nil
}

func (s *llmProviderSvc) sendOpenAITestMessage(ctx context.Context, p *llm_provider_entity.LLMProvider, modelID string) error {
	endpoint, err := llmurl.Build(firstNonEmpty(p.BaseURL, defaultOpenAIBaseURL), "/chat/completions")
	if err != nil {
		return err
	}
	payload := struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}{
		Model: modelID,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{
			{Role: "user", Content: testConnectionPrompt},
		},
	}
	req, err := newJSONRequest(ctx, endpoint.String(), payload)
	if err != nil {
		return err
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
				Role    string `json:"role"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := s.doJSON(req, &resp); err != nil {
		return err
	}
	if len(resp.Choices) == 0 {
		return errors.New("empty completion choices")
	}
	return nil
}

// sendOpenAIResponseTestMessage 走 /v1/responses，验证 openai-response 凭证 + 模型可用。
// 请求体只带 model + input（字符串形式），最大输出限到 testConnectionMaxTokens 减少花费。
// 响应里 output[].content[].text 是模型回答；空回也认为成功（part of empty 200）。
func (s *llmProviderSvc) sendOpenAIResponseTestMessage(ctx context.Context, p *llm_provider_entity.LLMProvider, modelID string) error {
	endpoint, err := llmurl.Build(firstNonEmpty(p.BaseURL, defaultOpenAIBaseURL), "/responses")
	if err != nil {
		return err
	}
	payload := struct {
		Model           string `json:"model"`
		Input           string `json:"input"`
		MaxOutputTokens int    `json:"max_output_tokens"`
	}{
		Model:           modelID,
		Input:           testConnectionPrompt,
		MaxOutputTokens: testConnectionMaxTokens,
	}
	req, err := newJSONRequest(ctx, endpoint.String(), payload)
	if err != nil {
		return err
	}
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	var resp struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := s.doJSON(req, &resp); err != nil {
		return err
	}
	// 200 + 空 output 也认为联通；只要 doJSON 没抛 http error，凭证 + 模型就 OK。
	return nil
}

// fetchModelList Anthropic 与 OpenAI 的 /models 接口同享 `{"data":[{"id":"..."}]}`
// 形状，差异仅在 endpoint 与认证头；setAuth 负责注入特定 provider 需要的请求头。
func (s *llmProviderSvc) fetchModelList(ctx context.Context, url string, setAuth func(http.Header)) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setAuth(req.Header)

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := s.doJSON(req, &payload); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(payload.Data))
	for _, m := range payload.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out, nil
}

func newJSONRequest(ctx context.Context, url string, payload any) (*http.Request, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (s *llmProviderSvc) doJSON(req *http.Request, out any) error {
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	if len(body) == 0 {
		return errors.New("empty response body")
	}
	return json.Unmarshal(body, out)
}

// ── DTO 转换（展示侧只带掩码 / 布尔，不含明文凭证） ──

func toItem(p *llm_provider_entity.LLMProvider) *ProviderItem {
	return &ProviderItem{
		ID:              p.ID,
		Type:            p.Type,
		ProviderKey:     p.ProviderKey,
		Name:            p.Name,
		BaseURL:         p.BaseURL,
		MaskedAPIKey:    p.MaskedAPIKey(),
		HasAPIKey:       p.APIKey != "",
		Enabled:         p.IsEnabled(),
		DefaultModelKey: p.DefaultModelKey,
		Createtime:      p.Createtime,
		Updatetime:      p.Updatetime,
	}
}

// toModelItem 转展示 DTO。p 为空时（如 UpdateModel 场景）不填 ProviderKey / IsDefault。
func toModelItem(m *llm_provider_model_entity.LLMProviderModel, p *llm_provider_entity.LLMProvider) *ModelItem {
	out := &ModelItem{
		ID:            m.ID,
		ProviderID:    m.ProviderID,
		ModelKey:      m.ModelKey,
		ModelID:       m.ModelID,
		Name:          m.Name,
		ContextWindow: m.ContextWindow,
		MaxOutput:     m.MaxOutput,
		Enabled:       m.IsEnabled(),
		Createtime:    m.Createtime,
		Updatetime:    m.Updatetime,
	}
	if p != nil {
		out.ProviderKey = p.ProviderKey
		out.IsDefault = p.DefaultModelKey == m.ModelKey
	}
	return out
}

func providerRefCountsToDTO(c llm_provider_repo.ProviderRefCounts) *ReferenceCounts {
	return &ReferenceCounts{Backends: c.Backends, Sessions: c.Sessions, Routes: c.Routes}
}

func modelRefCountsToDTO(c llm_provider_repo.ModelRefCounts) *ReferenceCounts {
	return &ReferenceCounts{Backends: c.Backends, Sessions: c.Sessions, Routes: c.Routes}
}

// enrichModel 用 cago agents 内置目录补全已知模型的元数据；命中失败时只携带 id。
func enrichModel(id string, p *llm_provider_entity.LLMProvider) *ModelInfo {
	out := &ModelInfo{ID: id}
	if info, ok := llmcatalog.Lookup(id); ok {
		out.Vendor = string(info.Vendor)
		out.ContextWindow = info.ContextWindow
		out.MaxOutput = info.MaxOutput
		out.Modalities = toStrings(info.Modalities)
		out.Thinking = info.Thinking
		out.KnownInCago = true
		return out
	}
	// 未命中目录：vendor 退而用 provider type 推断。
	switch llm_provider_entity.ProviderType(p.Type) {
	case llm_provider_entity.TypeAnthropic:
		out.Vendor = string(models.VendorAnthropic)
	case llm_provider_entity.TypeOpenAIChat, llm_provider_entity.TypeOpenAIResponse:
		out.Vendor = string(models.VendorOpenAI)
	}
	return out
}

// clampTokens 把负值视作未指定（0），其余保持原值。
func clampTokens(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func toStrings(ms []models.Modality) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, string(m))
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
