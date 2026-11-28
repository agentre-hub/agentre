package handlers_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/state"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testProviderKey = "4f8c1d2e-3b5a-4c6d-8e9f-1a2b3c4d5e6f"

func setupLLMTest(t *testing.T) (
	context.Context,
	*state.State,
	*handlers.LLMHandlers,
) {
	t.Helper()

	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	h := handlers.NewLLMHandlers(st)
	return context.Background(), st, h
}

func TestLLMUpsert(t *testing.T) {
	convey.Convey("llm.upsert", t, func() {
		ctx, st, h := setupLLMTest(t)

		convey.Convey("writes a local provider with its API key, never into the account section", func() {
			res, err := h.Upsert(ctx, handlers.LLMUpsertParams{ //nolint:gosec // credential-shaped API key is a test fixture.
				ProviderKey: testProviderKey,
				Name:        "anthropic-main",
				Type:        "anthropic",
				BaseURL:     "https://api.anthropic.com",
				Model:       "claude-sonnet-4-6",
				APIKey:      "sk-ant-secret",
				ModelRoutes: map[string]string{"OPUS": "claude-opus-4"},
				UpdatedAt:   1716000000,
			})
			require.NoError(t, err)
			assert.Equal(t, handlers.OK{OK: true}, res)

			meta, ok := st.Snapshot().LocalLLMProviders[testProviderKey]
			require.True(t, ok)
			assert.Equal(t, "anthropic-main", meta.Name)
			assert.Equal(t, "claude-sonnet-4-6", meta.Model)
			assert.Equal(t, "sk-ant-secret", meta.APIKey)
			assert.NotContains(t, st.Snapshot().LLMProviders, testProviderKey,
				"account provider metadata is the snapshot's; a local add never lands there")
		})

		convey.Convey("masks short keys gracefully", func() {
			_, err := h.Upsert(ctx, handlers.LLMUpsertParams{ProviderKey: testProviderKey, APIKey: "ab"})
			require.NoError(t, err)
			res, err := h.List(ctx)
			require.NoError(t, err)
			require.Len(t, res.Providers, 1)
			assert.Equal(t, "...ab", res.Providers[0].MaskedTail)
		})

		convey.Convey("persists state.json", func() {
			_, _ = h.Upsert(ctx, handlers.LLMUpsertParams{ProviderKey: testProviderKey, Name: "n", APIKey: "k"})
			_, err := os.Stat(filepath.Join(st.Dir(), "state.json"))
			require.NoError(t, err)
		})

		convey.Convey("missing providerKey is rejected", func() {
			_, err := h.Upsert(ctx, handlers.LLMUpsertParams{APIKey: "k"})
			assert.Error(t, err)
		})
	})
}

// llm.delete 删的是本机添加的供应商;账号供应商归快照管,删不掉、也不受影响。
func TestLLMDelete(t *testing.T) {
	ctx, st, h := setupLLMTest(t)
	_, err := h.Upsert(ctx, handlers.LLMUpsertParams{ProviderKey: testProviderKey, Name: "x", APIKey: "k"})
	require.NoError(t, err)
	require.NoError(t, st.ReplaceLLMProviders(map[string]state.LLMProviderMeta{"account-1": {Name: "acct"}}))

	res, err := h.Delete(ctx, handlers.LLMDeleteParams{ProviderKey: testProviderKey})
	require.NoError(t, err)
	assert.Equal(t, handlers.OK{OK: true}, res)
	assert.NotContains(t, st.Snapshot().LocalLLMProviders, testProviderKey)

	_, err = h.Delete(ctx, handlers.LLMDeleteParams{ProviderKey: "account-1"})
	require.NoError(t, err)
	assert.Contains(t, st.Snapshot().LLMProviders, "account-1")
}

// llm.list 列出这台 agentred 能用的全部供应商:本机添加的带 key 尾巴,账号的只有元数据
// (key 不在这台机器的盘上,尾巴为空)。
func TestLLMList(t *testing.T) {
	ctx, st, h := setupLLMTest(t)
	_, err := h.Upsert(ctx, handlers.LLMUpsertParams{ProviderKey: testProviderKey, Name: "a", BaseURL: "u", APIKey: "fixture-test-key", Model: "claude-sonnet-4-6", UpdatedAt: 100})
	require.NoError(t, err)
	require.NoError(t, st.ReplaceLLMProviders(map[string]state.LLMProviderMeta{"another-uuid-key": {Name: "b", UpdatedAt: 200}}))
	res, err := h.List(ctx)
	require.NoError(t, err)
	assert.Len(t, res.Providers, 2)
	for _, p := range res.Providers {
		switch p.ProviderKey {
		case testProviderKey:
			assert.Equal(t, "claude-sonnet-4-6", p.Model)
			assert.Equal(t, "...-key", p.MaskedTail)
		case "another-uuid-key":
			assert.Equal(t, "b", p.Name)
			assert.Empty(t, p.MaskedTail)
		}
	}
}

// TestLLMUpsert_PersistsModelCatalog 钉死本机添加的供应商把 DefaultModelKey + 非敏感
// 模型目录连同凭证一并落 state。
func TestLLMUpsert_PersistsModelCatalog(t *testing.T) {
	ctx, st, h := setupLLMTest(t)

	_, err := h.Upsert(ctx, handlers.LLMUpsertParams{ //nolint:gosec // credential-shaped API key is a test fixture.
		ProviderKey:     testProviderKey,
		Name:            "anthropic-main",
		Type:            "anthropic",
		Model:           "claude-sonnet-4-6",
		DefaultModelKey: "model-1",
		Models: []state.LLMModelMeta{
			{ModelKey: "model-1", ModelID: "claude-sonnet-4-6", Enabled: true},
			{ModelKey: "model-2", ModelID: "claude-opus-4-5", Enabled: true},
		},
		APIKey:    "sk-ant-secret",
		UpdatedAt: 1716000000,
	})
	require.NoError(t, err)

	meta, ok := st.Snapshot().LocalLLMProviders[testProviderKey]
	require.True(t, ok)
	assert.Equal(t, "model-1", meta.DefaultModelKey)
	require.Len(t, meta.Models, 2)
	assert.Equal(t, "claude-sonnet-4-6", meta.Models[0].ModelID)
	assert.Equal(t, "claude-opus-4-5", meta.Models[1].ModelID)
	assert.True(t, meta.Models[0].Enabled)

	res, err := h.List(ctx)
	require.NoError(t, err)
	require.Len(t, res.Providers, 1)
	assert.Equal(t, "model-1", res.Providers[0].DefaultModelKey)
	require.Len(t, res.Providers[0].Models, 2)
}
