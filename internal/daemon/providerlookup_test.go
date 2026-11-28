package daemon

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/daemon/leases"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/pkg/httpgateway"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLeases 是租约来源的替身:按 provider key 发一份固定租约,或一律拿不到。
type fakeLeases struct {
	lease    leases.Lease
	err      error
	purposes []string
	attached map[string]state.LLMProviderMeta
}

func (f *fakeLeases) AttachedProviders() map[string]state.LLMProviderMeta { return f.attached }

func (f *fakeLeases) Get(_ context.Context, providerKey, purpose string) (leases.Lease, error) {
	f.purposes = append(f.purposes, purpose)
	if f.err != nil {
		return leases.Lease{}, f.err
	}
	lease := f.lease
	lease.ProviderKey = providerKey
	return lease, nil
}

// Given 账号快照里一个供应商的元数据(没有 key),When 网关或一轮对话要它,
// Then key 来自内存里的租约(按「转发一轮对话」申请),端点以租约给的为准。
func TestProviderLookup_GivenAnAccountProvider_WhenFound_ThenTheKeyComesFromALease(t *testing.T) {
	const key = "prov-uuid-1"
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	st.Mutate(func(s *state.State) {
		s.LLMProviders[key] = state.LLMProviderMeta{Name: "anthropic-main", Type: "anthropic", BaseURL: "https://api.anthropic.com"}
	})
	source := &fakeLeases{lease: leases.Lease{APIKey: "leased-key", BaseURL: "https://leased.example"}}

	p, err := NewProviderLookup(st, source).FindByKey(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, int64(0), p.ID, "daemon doesn't track desktop int id")
	assert.Equal(t, key, p.ProviderKey)
	assert.Equal(t, "leased-key", p.APIKey)
	assert.Equal(t, "https://leased.example", p.BaseURL)
	assert.Equal(t, "anthropic-main", p.Name)
	assert.Empty(t, p.DefaultModelKey)
	assert.True(t, p.IsActive())
	assert.Equal(t, llm_provider_entity.TypeAnthropic, llm_provider_entity.ProviderType(p.Type))
	assert.Equal(t, []string{syncwire.LeasePurposeTurn}, source.purposes)

	// 租约没给端点时用快照里的。
	source.lease.BaseURL = ""
	p, err = NewProviderLookup(st, source).FindByKey(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, "https://api.anthropic.com", p.BaseURL)
}

// 拿不到租约时报「凭据租约不可用」,不是「供应商没配」:调用方据此给出
// 「凭据租约过期，账号服务器连不上」。
func TestProviderLookup_GivenNoLease_WhenFound_ThenFailsWithLeaseUnavailable(t *testing.T) {
	const key = "prov-uuid-1"
	st, _ := state.Load(t.TempDir())
	st.Mutate(func(s *state.State) { s.LLMProviders[key] = state.LLMProviderMeta{Name: "x", Type: "anthropic"} })

	_, err := NewProviderLookup(st, &fakeLeases{err: rpcerror.ErrProviderLeaseUnavailable}).FindByKey(context.Background(), key)
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable)

	_, err = NewProviderLookup(st, nil).FindByKey(context.Background(), key)
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable, "no lease source at all is the same answer")
}

// 本机用 `agentred llm add` 添加的供应商自带 key,不经租约。
func TestProviderLookup_GivenALocalProvider_WhenFound_ThenItsOwnKeyIsUsedWithoutALease(t *testing.T) {
	const key = "local-1"
	st, _ := state.Load(t.TempDir())
	require.NoError(t, st.UpsertLocalLLMProvider(key, state.LocalLLMProvider{
		LLMProviderMeta: state.LLMProviderMeta{Name: "Ollama", Type: "openai-chat", BaseURL: "http://127.0.0.1:11434"},
		APIKey:          "local-key",
	}))
	source := &fakeLeases{err: rpcerror.ErrProviderLeaseUnavailable}

	p, err := NewProviderLookup(st, source).FindByKey(context.Background(), key)
	require.NoError(t, err)
	assert.Equal(t, "local-key", p.APIKey)
	assert.Equal(t, "http://127.0.0.1:11434", p.BaseURL)
	assert.Empty(t, source.purposes)
}

// Given 一台没登录账号的 agentred(没有快照),When 桌面端经直连派来的一轮附带了租约与
// 供应商元数据,Then 这一轮里查得到这个供应商:key 来自租约,类型与模型目录来自附带的元数据。
func TestProviderLookup_GivenOnlyADesktopAttachedLease_WhenFound_ThenItsMetadataAndKeyServeTheRun(t *testing.T) {
	st, _ := state.Load(t.TempDir())
	source := &fakeLeases{
		lease: leases.Lease{APIKey: "desk-key", BaseURL: "https://desk.example"},
		attached: map[string]state.LLMProviderMeta{"pk-desk": {
			Name: "Desk", Type: "anthropic", DefaultModelKey: "mk", Models: []state.LLMModelMeta{{ModelKey: "mk", ModelID: "claude-x", Enabled: true}},
		}},
	}
	lookup := NewProviderLookup(st, source)

	p, err := lookup.FindByKey(context.Background(), "pk-desk")
	require.NoError(t, err)
	assert.Equal(t, "desk-key", p.APIKey)
	assert.Equal(t, "anthropic", p.Type)
	assert.Equal(t, "https://desk.example", p.BaseURL)

	eff, err := lookup.ResolveModel(context.Background(), "pk-desk", "")
	require.NoError(t, err)
	assert.Equal(t, "claude-x", eff.ModelID)
	m, err := lookup.FindModelByKey(context.Background(), "mk")
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, "claude-x", m.ModelID)
}

func TestProviderLookup_FindByKey_StateMiss(t *testing.T) {
	st, _ := state.Load(t.TempDir())
	_, err := NewProviderLookup(st, &fakeLeases{}).FindByKey(context.Background(), "prov-uuid-missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not configured")
	assert.NotErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable)
}

// TestProviderLookup_ResolveModel 钉死决策 11 的 daemon 侧模型解析（task 6 多模型目录）：
// fixed-model 精确匹配（缺失/停用拒绝）；provider-default 必须解析出当前启用的默认模型，
// Provider 存在但无合法启用默认模型（无默认 / 默认缺失 / 默认停用）一律按配置损坏严格阻止，
// 绝不回落到旧单模型字段或空值静默执行。只有 Provider 本身缺失/停用才走 resolveTarget 的
// 回退语义（见 runtime_test）。
func TestProviderLookup_ResolveModel(t *testing.T) {
	const key = "prov-uuid-1"
	st, _ := state.Load(t.TempDir())
	st.Mutate(func(s *state.State) {
		s.LLMProviders[key] = state.LLMProviderMeta{
			Name:            "anthropic-main",
			Type:            "anthropic",
			Model:           "claude-sonnet-legacy", // 旧单模型字段
			DefaultModelKey: "model-default",
			Models: []state.LLMModelMeta{
				{ModelKey: "model-default", ModelID: "claude-sonnet-4-6", Enabled: true,
					ContextWindow: ptrInt64(200000), MaxOutput: ptrInt64(8192)},
				{ModelKey: "model-opus", ModelID: "claude-opus-4-5", Enabled: true,
					ContextWindow: ptrInt64(500000), MaxOutput: ptrInt64(64000)},
				{ModelKey: "model-disabled", ModelID: "claude-haiku-gone", Enabled: false},
			},
		}
	})
	lookup := NewProviderLookup(st, nil)
	ctx := context.Background()

	t.Run("fixed-model resolves exact model", func(t *testing.T) {
		eff, err := lookup.ResolveModel(ctx, key, "model-opus")
		require.NoError(t, err)
		assert.Equal(t, "model-opus", eff.ModelKey)
		assert.Equal(t, "claude-opus-4-5", eff.ModelID)
	})

	// 模型元数据必须一路带到执行侧配置里:daemon 曾在这里把 ContextWindow / MaxOutput
	// 丢掉,同一套输入下 daemon 与桌面构造出的 EffectiveLLMConfig 因此不一致。
	t.Run("resolved model carries context window and max output", func(t *testing.T) {
		eff, err := lookup.ResolveModel(ctx, key, "model-opus")
		require.NoError(t, err)
		assert.Equal(t, 500000, eff.ContextWindow)
		assert.Equal(t, 64000, eff.MaxOutput)

		def, err := lookup.ResolveModel(ctx, key, "")
		require.NoError(t, err)
		assert.Equal(t, 200000, def.ContextWindow)
		assert.Equal(t, 8192, def.MaxOutput)
	})

	t.Run("model without metadata resolves to zero", func(t *testing.T) {
		bareModelKey := "prov-bare-model"
		st.Mutate(func(s *state.State) {
			s.LLMProviders[bareModelKey] = state.LLMProviderMeta{
				Name: "bare", Type: "anthropic", DefaultModelKey: "m",
				Models: []state.LLMModelMeta{{ModelKey: "m", ModelID: "id", Enabled: true}},
			}
		})
		eff, err := lookup.ResolveModel(ctx, bareModelKey, "")
		require.NoError(t, err)
		assert.Zero(t, eff.ContextWindow)
		assert.Zero(t, eff.MaxOutput)
	})

	t.Run("fixed-model missing rejects", func(t *testing.T) {
		_, err := lookup.ResolveModel(ctx, key, "model-gone")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "model-gone")
	})

	t.Run("fixed-model disabled rejects (no silent downgrade)", func(t *testing.T) {
		_, err := lookup.ResolveModel(ctx, key, "model-disabled")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "disabled")
	})

	t.Run("provider-default resolves DefaultModelKey", func(t *testing.T) {
		eff, err := lookup.ResolveModel(ctx, key, "")
		require.NoError(t, err)
		assert.Equal(t, "model-default", eff.ModelKey)
		assert.Equal(t, "claude-sonnet-4-6", eff.ModelID)
	})

	t.Run("provider-default legacy single-model-only state blocks (config corruption)", func(t *testing.T) {
		legacyKey := "prov-legacy"
		st.Mutate(func(s *state.State) {
			s.LLMProviders[legacyKey] = state.LLMProviderMeta{
				Name: "old", Type: "anthropic", Model: "claude-opus-4",
			}
		})
		_, err := lookup.ResolveModel(ctx, legacyKey, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no legal enabled default model")
	})

	t.Run("provider-default no model at all blocks (config corruption)", func(t *testing.T) {
		bareKey := "prov-bare"
		st.Mutate(func(s *state.State) {
			s.LLMProviders[bareKey] = state.LLMProviderMeta{Name: "bare", Type: "anthropic"}
		})
		_, err := lookup.ResolveModel(ctx, bareKey, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no legal enabled default model")
	})

	t.Run("provider-default default model missing blocks (config corruption)", func(t *testing.T) {
		corruptKey := "prov-corrupt"
		st.Mutate(func(s *state.State) {
			s.LLMProviders[corruptKey] = state.LLMProviderMeta{
				Name: "corrupt", Type: "anthropic",
				DefaultModelKey: "model-gone-default",
				Models: []state.LLMModelMeta{
					{ModelKey: "model-other", ModelID: "claude-sonnet-4-6", Enabled: true},
				},
			}
		})
		_, err := lookup.ResolveModel(ctx, corruptKey, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no legal enabled default model")
	})

	t.Run("provider-default default model disabled blocks (config corruption)", func(t *testing.T) {
		disabledKey := "prov-disabled-default"
		st.Mutate(func(s *state.State) {
			s.LLMProviders[disabledKey] = state.LLMProviderMeta{
				Name: "disabled-default", Type: "anthropic",
				DefaultModelKey: "model-disabled-default",
				Models: []state.LLMModelMeta{
					{ModelKey: "model-disabled-default", ModelID: "claude-haiku-gone", Enabled: false},
				},
			}
		})
		_, err := lookup.ResolveModel(ctx, disabledKey, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no legal enabled default model")
	})

	t.Run("unknown provider rejects", func(t *testing.T) {
		_, err := lookup.ResolveModel(ctx, "prov-nope", "")
		require.Error(t, err)
	})
}

func ptrInt64(v int64) *int64 { return &v }

func TestProviderLookup_FindModelByKey(t *testing.T) {
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	st.Mutate(func(s *state.State) {
		s.LLMProviders["p1"] = state.LLMProviderMeta{Name: "g", Type: "gemini", Models: []state.LLMModelMeta{
			{ModelKey: "mk-on", ModelID: "gemini-3.1-pro-preview", Enabled: true},
			{ModelKey: "mk-off", ModelID: "gemini-old", Enabled: false},
		}}
	})
	lookup := NewProviderLookup(st, nil)

	m, err := lookup.FindModelByKey(context.Background(), "mk-on")
	require.NoError(t, err)
	require.NotNil(t, m)
	assert.Equal(t, "gemini-3.1-pro-preview", m.ModelID)
	assert.True(t, m.IsEnabled())
	assert.True(t, m.IsActive())

	// 停用的模型照实返回，由网关按 IsEnabled 判 502。
	m, err = lookup.FindModelByKey(context.Background(), "mk-off")
	require.NoError(t, err)
	assert.False(t, m.IsEnabled())

	// 不存在：与桌面仓储一致返回 (nil, nil)，由网关报 model not found。
	m, err = lookup.FindModelByKey(context.Background(), "mk-none")
	require.NoError(t, err)
	assert.Nil(t, m)
}

func TestProviderLookup_ImplementsGatewayModelLookup(t *testing.T) {
	var _ httpgateway.ModelLookup = (*ProviderLookup)(nil)
}
