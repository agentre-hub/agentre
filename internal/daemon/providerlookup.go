// Package daemon assembles the agentred daemon: state, gateway, rpc server,
// handlers, notifier. Lives one level up from sub-packages to avoid import
// cycles (state/handlers/rpc don't depend on daemon).
package daemon

import (
	"context"
	"fmt"

	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
)

// ProviderLookup implements httpgateway.ProviderLookup: given a stable provider key,
// return the full LLMProvider entity from agentred state.
type ProviderLookup struct {
	state *state.State
}

// NewProviderLookup constructs a ProviderLookup backed by the given state.
func NewProviderLookup(s *state.State) *ProviderLookup {
	return &ProviderLookup{state: s}
}

// FindByKey satisfies httpgateway.ProviderLookup and handlers.LLMProviderLookupPort.
// It errors when the key has no metadata in state.
func (l *ProviderLookup) FindByKey(ctx context.Context, key string) (*llm_provider_entity.LLMProvider, error) {
	snap := l.state.Snapshot()
	meta, ok := snap.LLMProviders[key]
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", key)
	}
	if meta.APIKey == "" {
		return nil, fmt.Errorf("provider %q apiKey not configured", key)
	}
	return &llm_provider_entity.LLMProvider{
		ProviderKey:     key,
		Type:            meta.Type,
		Name:            meta.Name,
		APIKey:          meta.APIKey,
		BaseURL:         meta.BaseURL,
		DefaultModelKey: meta.DefaultModelKey,
		Status:          consts.ACTIVE,
	}, nil
}

// ResolveModel satisfies handlers.LLMProviderLookupPort: resolve the provider's
// execution model from the daemon's own catalog (decision 11).
//
//   - modelKey 空 → provider-default：必须解析出 Provider 当前启用的默认模型。
//     Provider 存在但无合法启用默认模型（无默认 / 默认缺失 / 默认停用）按配置损坏
//     严格阻止——绝不回落到旧单模型字段或空值静默执行。只有 Provider 本身缺失/停用
//     才走 resolveTarget 的 provider-default 回退语义（回退 agent 绑定或 CLI 登录态）。
//   - modelKey 非空 → fixed-model：精确查 Models 里启用模型，缺失/停用返回 error，
//     由调用方严格阻止本轮 —— 绝不静默降级为默认模型。
func (l *ProviderLookup) ResolveModel(ctx context.Context, providerKey, modelKey string) (handlers.EffectiveModel, error) {
	snap := l.state.Snapshot()
	meta, ok := snap.LLMProviders[providerKey]
	if !ok {
		return handlers.EffectiveModel{}, fmt.Errorf("provider %q not configured", providerKey)
	}
	if modelKey == "" {
		// provider-default：只认默认模型精确命中且启用。其余一律配置损坏 → 报错阻止，
		// 不沿用旧单模型字段 Model 的宽松 CLI 默认行为。
		for _, m := range meta.Models {
			if m.ModelKey == meta.DefaultModelKey && m.Enabled {
				return effectiveModelFrom(m), nil
			}
		}
		return handlers.EffectiveModel{}, fmt.Errorf(
			"provider %q has no legal enabled default model (defaultModelKey=%q): configuration corruption",
			providerKey, meta.DefaultModelKey)
	}
	// fixed-model：精确匹配，缺失/停用一律拒绝。
	for _, m := range meta.Models {
		if m.ModelKey == modelKey {
			if !m.Enabled {
				return handlers.EffectiveModel{}, fmt.Errorf("model %q disabled on provider %q", modelKey, providerKey)
			}
			return effectiveModelFrom(m), nil
		}
	}
	return handlers.EffectiveModel{}, fmt.Errorf("model %q not configured on provider %q", modelKey, providerKey)
}

// effectiveModelFrom 把目录里的一条模型元数据翻成执行侧模型（含 ContextWindow /
// MaxOutput —— 目录没记时为 0，与桌面解析口对齐）。
func effectiveModelFrom(m state.LLMModelMeta) handlers.EffectiveModel {
	return handlers.EffectiveModel{
		ModelKey:      m.ModelKey,
		ModelID:       m.ModelID,
		ContextWindow: intFromPtr(m.ContextWindow),
		MaxOutput:     intFromPtr(m.MaxOutput),
	}
}

func intFromPtr(v *int64) int {
	if v == nil {
		return 0
	}
	return int(*v)
}
