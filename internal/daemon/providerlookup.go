// Package daemon assembles the agentred daemon: state, gateway, rpc server,
// handlers, notifier. Lives one level up from sub-packages to avoid import
// cycles (state/handlers/rpc don't depend on daemon).
package daemon

import (
	"context"
	"fmt"

	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/leases"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// LeaseSource hands out an in-memory credential lease for an account provider
// (*leases.Manager). It is a port so the lookup can be tested without a server.
type LeaseSource interface {
	Get(ctx context.Context, providerKey, purpose string) (leases.Lease, error)
	// AttachedProviders returns the metadata desktops attached with leases to the
	// runs in flight (for an agentred without an account snapshot).
	AttachedProviders() map[string]state.LLMProviderMeta
}

// ProviderLookup implements httpgateway.ProviderLookup: given a stable provider
// key, return the full LLMProvider entity. A provider added on this machine
// (`agentred llm add`) carries its own key; an account provider has metadata
// only, and its key comes from a lease held in memory.
type ProviderLookup struct {
	state   *state.State
	leases  LeaseSource
	purpose string
}

// NewProviderLookup constructs a ProviderLookup backed by state and the lease
// source (nil = no account leases: account providers fail as lease-unavailable).
func NewProviderLookup(s *state.State, leaseSource LeaseSource) *ProviderLookup {
	return &ProviderLookup{state: s, leases: leaseSource, purpose: syncwire.LeasePurposeTurn}
}

// ForPurpose returns a lookup that leases for another purpose (a CLI probe is a
// provider test, not a turn).
func (l *ProviderLookup) ForPurpose(purpose string) *ProviderLookup {
	out := *l
	out.purpose = purpose
	return &out
}

// lookup finds a provider's metadata: this machine's state first (local and
// account snapshot), then the metadata a desktop attached to a run in flight.
func (l *ProviderLookup) lookup(key string) (state.ProviderEntry, bool) {
	if entry, ok := l.state.Snapshot().LookupProvider(key); ok {
		return entry, true
	}
	if l.leases == nil {
		return state.ProviderEntry{}, false
	}
	meta, ok := l.leases.AttachedProviders()[key]
	return state.ProviderEntry{LLMProviderMeta: meta}, ok
}

// catalog is every provider's metadata this lookup can see (see lookup).
func (l *ProviderLookup) catalog() map[string]state.LLMProviderMeta {
	out := l.state.Snapshot().ProviderCatalog()
	if l.leases == nil {
		return out
	}
	for key, meta := range l.leases.AttachedProviders() {
		if _, known := out[key]; !known {
			out[key] = meta
		}
	}
	return out
}

// leaseCredentials adapts the lease manager to handlers.ProviderCredentialPort.
type leaseCredentials struct{ leases LeaseSource }

func (c leaseCredentials) ProviderCredential(ctx context.Context, providerKey, purpose string) (handlers.ProviderCredential, error) {
	if c.leases == nil {
		return handlers.ProviderCredential{}, rpcerror.ErrProviderLeaseUnavailable
	}
	lease, err := c.leases.Get(ctx, providerKey, purpose)
	if err != nil {
		return handlers.ProviderCredential{}, err
	}
	return handlers.ProviderCredential{APIKey: lease.APIKey, BaseURL: lease.BaseURL}, nil
}

// FindByKey satisfies httpgateway.ProviderLookup and handlers.LLMProviderLookupPort.
// It errors when the key is unknown, and with an error wrapping
// rpcerror.ErrProviderLeaseUnavailable when an account provider's lease cannot
// be had.
func (l *ProviderLookup) FindByKey(ctx context.Context, key string) (*llm_provider_entity.LLMProvider, error) {
	entry, ok := l.lookup(key)
	if !ok {
		return nil, fmt.Errorf("provider %q not configured", key)
	}
	apiKey, baseURL := entry.APIKey, entry.BaseURL
	if !entry.Local {
		if l.leases == nil {
			return nil, fmt.Errorf("provider %q: %w", key, rpcerror.ErrProviderLeaseUnavailable)
		}
		lease, err := l.leases.Get(ctx, key, l.purpose)
		if err != nil {
			return nil, err
		}
		apiKey = lease.APIKey
		if lease.BaseURL != "" {
			baseURL = lease.BaseURL
		}
	}
	if apiKey == "" {
		return nil, fmt.Errorf("provider %q apiKey not configured", key)
	}
	return &llm_provider_entity.LLMProvider{
		ProviderKey:     key,
		Type:            entry.Type,
		Name:            entry.Name,
		APIKey:          apiKey,
		BaseURL:         baseURL,
		DefaultModelKey: entry.DefaultModelKey,
		Status:          consts.ACTIVE,
	}, nil
}

// FindModelByKey satisfies httpgateway.ModelLookup：按稳定 model_key 在 daemon 目录里
// 找模型，使远端网关与桌面一样改写请求里的模型。model_key 全局唯一，故遍历各供应商。
// 找不到返回 (nil, nil)（与桌面仓储一致，由网关报 model not found）；停用的模型照实
// 返回，由网关按 IsEnabled 拒绝。
func (l *ProviderLookup) FindModelByKey(_ context.Context, modelKey string) (*llm_provider_model_entity.LLMProviderModel, error) {
	for _, meta := range l.catalog() {
		for _, m := range meta.Models {
			if m.ModelKey != modelKey {
				continue
			}
			enabled := llm_provider_model_entity.EnabledOff
			if m.Enabled {
				enabled = llm_provider_model_entity.EnabledOn
			}
			return &llm_provider_model_entity.LLMProviderModel{
				ModelKey:      m.ModelKey,
				ModelID:       m.ModelID,
				Name:          m.Name,
				ContextWindow: intFromPtr(m.ContextWindow),
				MaxOutput:     intFromPtr(m.MaxOutput),
				Enabled:       enabled,
				Status:        consts.ACTIVE,
			}, nil
		}
	}
	return nil, nil
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
	entry, ok := l.lookup(providerKey)
	meta := entry.LLMProviderMeta
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
