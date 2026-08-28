package remote_device_svc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// SyncProvider copies a local provider's metadata, API key and model catalog to
// one paired daemon. It is intentionally explicit because the operation
// transfers a secret to another machine (spec「Remote execution and credential
// boundary」:向 daemon 同步 Provider 凭证必须由用户显式确认；成功后才刷新远端目录，
// 也不自动替用户选择 target）。
func (s *service) SyncProvider(ctx context.Context, deviceID int64, providerKey string) error {
	key := strings.TrimSpace(providerKey)
	if deviceID <= 0 || key == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if s.pool == nil {
		return errors.New("remote device connection pool unavailable")
	}

	repo := llm_provider_repo.LLMProvider()
	if repo == nil {
		return errors.New("llm provider repo unavailable")
	}
	p, err := repo.FindByKey(ctx, key)
	if err != nil {
		return err
	}
	if p == nil || !p.IsActive() {
		return i18n.NewError(ctx, code.LLMProviderNotFound)
	}

	// 模型目录一次性拉齐：既解析默认模型，也把全部模型（含停用，供 daemon 精确拒绝
	// 停用 fixed-model）随同步过线 —— daemon 是远端可运行事实源。
	models, err := repo.ListModels(ctx, p.ID)
	if err != nil {
		return err
	}
	defaultModelID := defaultModelIDOf(p, models)

	lease, err := s.pool.Borrow(ctx, deviceID)
	if err != nil {
		return mapSyncBorrowError(ctx, err)
	}
	defer lease.Release()

	if _, err := lease.LLMUpsert(ctx, providerToUpsertRequest(p, defaultModelID, models)); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return i18n.NewError(ctx, code.RemoteDeviceTimeout)
		}
		return fmt.Errorf("remote llm.upsert: %w", err)
	}
	s.upsertDeviceProviderCache(deviceID, providerSummaryFromModels(p, models))
	return nil
}

// defaultModelIDOf 解析 Provider 当前启用的默认模型的 ModelID（spec：同步默认模型）。
// 无默认模型、默认模型缺失或停用时返回空串，保持 daemon 单模型字段「没有就空」的既有语义。
func defaultModelIDOf(p *llm_provider_entity.LLMProvider, models []*llm_provider_model_entity.LLMProviderModel) string {
	if p == nil || !p.HasDefaultModel() {
		return ""
	}
	for _, m := range models {
		if m != nil && m.ModelKey == p.DefaultModelKey && m.IsEnabled() {
			return m.ModelID
		}
	}
	return ""
}

func providerToUpsertRequest(
	p *llm_provider_entity.LLMProvider,
	defaultModelID string,
	models []*llm_provider_model_entity.LLMProviderModel,
) *agentrewire.LLMUpsertRequest {
	metas := make([]*agentrewire.LLMModel, 0, len(models))
	for _, m := range models {
		if m == nil {
			continue
		}
		metas = append(metas, &agentrewire.LLMModel{
			ModelKey: m.ModelKey, ModelId: m.ModelID, Name: m.Name, Enabled: m.IsEnabled(),
		})
	}
	return &agentrewire.LLMUpsertRequest{
		ProviderKey:     p.ProviderKey,
		Name:            p.Name,
		Type:            p.Type,
		BaseUrl:         p.BaseURL,
		Model:           defaultModelID,
		DefaultModelKey: p.DefaultModelKey,
		Models:          metas,
		ApiKey:          p.APIKey,
		UpdatedAt:       p.Updatetime,
	}
}

// providerSummaryFromModels 组 daemon 侧目录的 ProviderSummary（含非敏感模型摘要），
// 同步成功后立即刷新本机缓存。
func providerSummaryFromModels(
	p *llm_provider_entity.LLMProvider,
	models []*llm_provider_model_entity.LLMProviderModel,
) ProviderSummary {
	ms := make([]ModelSummary, 0, len(models))
	for _, m := range models {
		if m == nil {
			continue
		}
		ms = append(ms, ModelSummary{
			Key: m.ModelKey, ModelID: m.ModelID, Name: m.Name, Enabled: m.IsEnabled(),
		})
	}
	return ProviderSummary{
		Key:             p.ProviderKey,
		Name:            p.Name,
		Type:            p.Type,
		DefaultModelKey: p.DefaultModelKey,
		Models:          ms,
	}
}

func mapSyncBorrowError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, ErrDeviceNotFound):
		return i18n.NewError(ctx, code.RemoteDeviceNotFound)
	case errors.Is(err, ErrDeviceUnauthorized):
		return i18n.NewError(ctx, code.RemoteDeviceUnauthorized)
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded):
		return i18n.NewError(ctx, code.RemoteDeviceTimeout)
	default:
		return i18n.NewError(ctx, code.RemoteDeviceDialFailed)
	}
}

func (s *service) upsertDeviceProviderCache(deviceID int64, p ProviderSummary) {
	s.providerCacheMu.Lock()
	defer s.providerCacheMu.Unlock()

	prev := s.providerCache[deviceID]
	next := make([]ProviderSummary, 0, len(prev)+1)
	replaced := false
	for _, existing := range prev {
		if existing.Key == p.Key {
			next = append(next, p)
			replaced = true
			continue
		}
		next = append(next, existing)
	}
	if !replaced {
		next = append(next, p)
	}
	s.providerCache[deviceID] = next
}
