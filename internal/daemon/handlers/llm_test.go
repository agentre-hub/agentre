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

		convey.Convey("writes provider meta and API key to state", func() {
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

			meta, ok := st.LLMProviders[testProviderKey]
			require.True(t, ok)
			assert.Equal(t, "anthropic-main", meta.Name)
			assert.Equal(t, "claude-sonnet-4-6", meta.Model)
			assert.Equal(t, "sk-ant-secret", meta.APIKey)
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

func TestLLMDelete(t *testing.T) {
	ctx, st, h := setupLLMTest(t)
	st.Mutate(func(s *state.State) {
		s.LLMProviders[testProviderKey] = state.LLMProviderMeta{Name: "x"}
	})

	res, err := h.Delete(ctx, handlers.LLMDeleteParams{ProviderKey: testProviderKey})
	require.NoError(t, err)
	assert.Equal(t, handlers.OK{OK: true}, res)
	_, exists := st.LLMProviders[testProviderKey]
	assert.False(t, exists)
}

func TestLLMList(t *testing.T) {
	ctx, st, h := setupLLMTest(t)
	st.Mutate(func(s *state.State) {
		s.LLMProviders[testProviderKey] = state.LLMProviderMeta{Name: "a", BaseURL: "u", APIKey: "fixture-test-key", Model: "claude-sonnet-4-6", UpdatedAt: 100}
		s.LLMProviders["another-uuid-key"] = state.LLMProviderMeta{Name: "b", UpdatedAt: 200}
	})
	res, err := h.List(ctx)
	require.NoError(t, err)
	assert.Len(t, res.Providers, 2)
	for _, p := range res.Providers {
		if p.ProviderKey == testProviderKey {
			assert.Equal(t, "claude-sonnet-4-6", p.Model)
			assert.Equal(t, "...-key", p.MaskedTail)
		}
	}
}

// TestLLMUpsert_PersistsModelCatalog 钉死 task 6 多模型同步：llm.upsert 把 Provider
// 的 DefaultModelKey + 非敏感模型目录连同凭证一并落 state。
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

	meta, ok := st.LLMProviders[testProviderKey]
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
