package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentre-ai/agentre/internal/daemon/handlers"
	"github.com/agentre-ai/agentre/internal/daemon/state"
	"github.com/agentre-ai/agentre/internal/pkg/cliprober"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const engineProviderKey = "provider-key-must-not-return"
const engineAPIKey = "engine-api-key-must-not-return"

func setupEngineTest(t *testing.T, scan func() []cliprober.CLIProbeResult) (*state.State, *handlers.EngineHandlers) {
	t.Helper()
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	return st, handlers.NewEngineHandlers(handlers.EngineDeps{State: st, HTTPClient: http.DefaultClient, ScanAllCLIs: scan})
}

func TestEngineTest_GivenConfiguredProvider_WhenTestingDefaultModel_ThenUsesStateCredentialWithoutReturningIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer "+engineAPIKey, r.Header.Get("Authorization"))
		_, err := w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	st, h := setupEngineTest(t, nil)
	st.Mutate(func(s *state.State) {
		s.LLMProviders[engineProviderKey] = state.LLMProviderMeta{Type: "openai-chat", BaseURL: server.URL, APIKey: engineAPIKey, DefaultModelKey: "default-model-key", Models: []state.LLMModelMeta{{ModelKey: "default-model-key", ModelID: "gpt-test"}}}
	})

	result, err := h.Test(context.Background(), handlers.EngineTestParams{ProviderKey: engineProviderKey})
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.NotEmpty(t, result.Message)
	require.NotNil(t, result.LatencyMs)
	payload, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), engineAPIKey)
	assert.NotContains(t, string(payload), engineProviderKey)
}

func TestEngineTest_GivenUnknownProvider_WhenTesting_ThenReportsFailureWithoutLeakingConfiguredCredentials(t *testing.T) {
	st, h := setupEngineTest(t, nil)
	st.Mutate(func(s *state.State) { s.LLMProviders[engineProviderKey] = state.LLMProviderMeta{APIKey: engineAPIKey} })
	result, err := h.Test(context.Background(), handlers.EngineTestParams{ProviderKey: "missing-provider"})
	require.NoError(t, err)
	assert.False(t, result.OK)
	assert.NotEmpty(t, result.Message)
	payload, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), engineAPIKey)
	assert.NotContains(t, string(payload), engineProviderKey)
}

func TestEngineDiscover_GivenConfiguredProvider_WhenDiscoveringModels_ThenReturnsOnlyModelIDAndName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/models", r.URL.Path)
		assert.Equal(t, "Bearer "+engineAPIKey, r.Header.Get("Authorization"))
		_, err := w.Write([]byte(`{"data":[{"id":"gpt-test","name":"GPT Test"},{"id":"gpt-unnamed"}]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	st, h := setupEngineTest(t, nil)
	st.Mutate(func(s *state.State) {
		s.LLMProviders[engineProviderKey] = state.LLMProviderMeta{Type: "openai-chat", BaseURL: server.URL, APIKey: engineAPIKey}
	})

	result, err := h.Discover(context.Background(), handlers.EngineDiscoverParams{ProviderKey: engineProviderKey})
	require.NoError(t, err)
	require.Equal(t, []handlers.EngineModel{{ModelID: "gpt-test", Name: "GPT Test"}, {ModelID: "gpt-unnamed", Name: "gpt-unnamed"}}, result.Models)
	payload, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), engineAPIKey)
	assert.NotContains(t, string(payload), engineProviderKey)
}

func TestEngineScan_GivenResolvedAndMissingCLIs_WhenScanning_ThenMapsToPrivacySafeStatuses(t *testing.T) {
	_, h := setupEngineTest(t, func() []cliprober.CLIProbeResult {
		return []cliprober.CLIProbeResult{{BackendType: "claudecode", Path: "/private/bin/claude", Found: true}, {BackendType: "codex", Found: false}}
	})
	result, err := h.Scan(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []handlers.EngineScanItem{{BackendType: "claudecode", Status: "recognized"}, {BackendType: "codex", Status: "unchecked"}}, result.Items)
	payload, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), "/private/bin/claude")
	assert.NotContains(t, string(payload), `"path"`)
	assert.NotContains(t, string(payload), "\"status\":\"path\"")
}
