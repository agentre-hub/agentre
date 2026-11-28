package chat_svc

import (
	"context"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// directRunLeaseLifetime caps a lease this desktop attaches to a run it sends
// over a direct connection. The agentred keeps it only for that run and drops it
// when the run ends, so the cap only has to outlast the longest turn.
const directRunLeaseLifetime = 24 * time.Hour

// desktopProviderLeases hands this desktop's own provider keys (from the
// keychain-backed provider repository) to a direct run as leases, so an
// agentred that cannot lease from the account server — not logged in, or the
// server unreachable — still runs the turn (规格「凭据 › agentred：按需租用」).
type desktopProviderLeases struct {
	now func() time.Time
}

var _ remote.ProviderLeaseSource = desktopProviderLeases{}

func (d desktopProviderLeases) ProviderLeases(ctx context.Context, providerKeys []string) []*agentrewire.ProviderLease {
	now := time.Now
	if d.now != nil {
		now = d.now
	}
	expiresAt := now().Add(directRunLeaseLifetime).UnixMilli()
	out := make([]*agentrewire.ProviderLease, 0, len(providerKeys))
	for _, key := range providerKeys {
		provider, err := llm_provider_repo.LLMProvider().FindByKey(ctx, key)
		if err != nil {
			// 拿不到就不附带:agentred 自己再去账号服务器申请,申请不到时报租约不可用。
			logger.Ctx(ctx).Warn("chat_svc.desktopProviderLeases: provider unreadable, attaching no lease",
				zap.String("providerKey", key), zap.Error(err))
			continue
		}
		if !provider.IsActive() || !provider.IsEnabled() || strings.TrimSpace(provider.APIKey) == "" {
			continue
		}
		// 元数据随行:一台没登录账号的 agentred 没有快照,类型与模型目录只能从这一轮拿。
		models, err := llm_provider_repo.LLMProvider().ListModels(ctx, provider.ID)
		if err != nil {
			logger.Ctx(ctx).Warn("chat_svc.desktopProviderLeases: model catalog unreadable, attaching the lease without it",
				zap.String("providerKey", key), zap.Error(err))
		}
		out = append(out, &agentrewire.ProviderLease{
			ProviderKey: key, ApiKey: provider.APIKey, BaseUrl: provider.BaseURL, ExpiresAt: expiresAt,
			Type: provider.Type, Name: provider.Name, DefaultModelKey: provider.DefaultModelKey, Models: leaseModels(models),
		})
	}
	return out
}

func leaseModels(models []*llm_provider_model_entity.LLMProviderModel) []*agentrewire.LLMModel {
	out := make([]*agentrewire.LLMModel, 0, len(models))
	for _, m := range models {
		if m == nil {
			continue
		}
		out = append(out, &agentrewire.LLMModel{
			ModelKey: m.ModelKey, ModelId: m.ModelID, Name: m.Name, Enabled: m.IsEnabled(),
			ContextWindow: optionalCount(m.ContextWindow), MaxOutput: optionalCount(m.MaxOutput),
		})
	}
	return out
}

// optionalCount 把「0 = 未知」的计数翻成线上的可缺席字段。
func optionalCount(v int) *int64 {
	if v <= 0 {
		return nil
	}
	n := int64(v)
	return &n
}
