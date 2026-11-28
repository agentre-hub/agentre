package chat_svc

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo/mock_llm_provider_repo"
)

// Given 桌面端经直连派一轮给 agentred,When 要给这一轮可能用到的供应商附带租约,
// Then 每个本机有 key 的启用供应商给一份(key 来自本机钥匙串支撑的仓储,端点照抄),
// 到期时刻是 24 小时的上限(长轮次不至于中途断掉);没 key、停用或不存在的不给。
func TestDesktopProviderLeases_GivenTheTurnsProviders_ThenEachUsableOneGetsARunScopedLease(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mock_llm_provider_repo.NewMockLLMProviderRepo(ctrl)
	prev := llm_provider_repo.LLMProvider()
	llm_provider_repo.RegisterLLMProvider(repo)
	t.Cleanup(func() { llm_provider_repo.RegisterLLMProvider(prev) })

	repo.EXPECT().FindByKey(gomock.Any(), "pk-ok").Return(&llm_provider_entity.LLMProvider{
		ID: 3, ProviderKey: "pk-ok", Name: "Anthropic", Type: "anthropic", APIKey: "sk-desk", BaseURL: "https://api.example",
		DefaultModelKey: "mk-1", Enabled: llm_provider_entity.EnabledOn, Status: consts.ACTIVE,
	}, nil)
	repo.EXPECT().ListModels(gomock.Any(), int64(3)).Return([]*llm_provider_model_entity.LLMProviderModel{
		{ModelKey: "mk-1", ModelID: "claude-x", Name: "X", ContextWindow: 200000, Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
		{ModelKey: "mk-2", ModelID: "claude-y", Enabled: llm_provider_model_entity.EnabledOff, Status: consts.ACTIVE},
	}, nil)
	repo.EXPECT().FindByKey(gomock.Any(), "pk-nokey").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "pk-nokey", Enabled: llm_provider_entity.EnabledOn, Status: consts.ACTIVE,
	}, nil)
	repo.EXPECT().FindByKey(gomock.Any(), "pk-off").Return(&llm_provider_entity.LLMProvider{
		ProviderKey: "pk-off", APIKey: "sk-off", Enabled: llm_provider_entity.EnabledOff, Status: consts.ACTIVE,
	}, nil)
	repo.EXPECT().FindByKey(gomock.Any(), "pk-gone").Return(nil, nil)

	now := time.UnixMilli(1_700_000_000_000)
	got := desktopProviderLeases{now: func() time.Time { return now }}.ProviderLeases(context.Background(),
		[]string{"pk-ok", "pk-nokey", "pk-off", "pk-gone"})

	require.Len(t, got, 1)
	assert.Equal(t, "pk-ok", got[0].GetProviderKey())
	assert.Equal(t, "sk-desk", got[0].GetApiKey())
	assert.Equal(t, "https://api.example", got[0].GetBaseUrl())
	assert.Equal(t, now.Add(24*time.Hour).UnixMilli(), got[0].GetExpiresAt())
	// 元数据随行:未登录账号的 agentred 没有快照,类型与模型目录只能从这一轮拿。
	assert.Equal(t, "anthropic", got[0].GetType())
	assert.Equal(t, "Anthropic", got[0].GetName())
	assert.Equal(t, "mk-1", got[0].GetDefaultModelKey())
	require.Len(t, got[0].GetModels(), 2)
	assert.Equal(t, "claude-x", got[0].GetModels()[0].GetModelId())
	assert.True(t, got[0].GetModels()[0].GetEnabled())
	assert.Equal(t, int64(200000), got[0].GetModels()[0].GetContextWindow())
	assert.False(t, got[0].GetModels()[1].GetEnabled())
}
