package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/cliprober"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

const engineProviderKey = "provider-key-must-not-return"
const engineAPIKey = "engine-api-key-must-not-return" //nolint:gosec // credential-shaped test fixture must exercise response redaction

type countingHTTPDoer struct{ calls int }

func (d *countingHTTPDoer) Do(*http.Request) (*http.Response, error) {
	d.calls++
	return nil, errors.New("must not call upstream")
}

// stubCredentials 是账号供应商的租约来源替身:发 engineAPIKey,或一律拿不到;记下每次的用途。
type stubCredentials struct {
	err      error
	purposes []string
}

func (c *stubCredentials) ProviderCredential(_ context.Context, _ string, purpose string) (handlers.ProviderCredential, error) {
	c.purposes = append(c.purposes, purpose)
	if c.err != nil {
		return handlers.ProviderCredential{}, c.err
	}
	return handlers.ProviderCredential{APIKey: engineAPIKey}, nil
}

func setupEngineTest(t *testing.T, scan func() []cliprober.CLIProbeResult) (*state.State, *handlers.EngineHandlers) {
	st, h, _ := setupEngineTestWithCredentials(t, scan, nil)
	return st, h
}

func setupEngineTestWithCredentials(t *testing.T, scan func() []cliprober.CLIProbeResult, leaseErr error) (*state.State, *handlers.EngineHandlers, *stubCredentials) {
	t.Helper()
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	creds := &stubCredentials{err: leaseErr}
	return st, handlers.NewEngineHandlers(handlers.EngineDeps{State: st, Credentials: creds, HTTPClient: http.DefaultClient, ScanAllCLIs: scan}), creds
}

func TestEngineTest_GivenConfiguredProvider_WhenTestingDefaultModel_ThenUsesALeasedCredentialWithoutReturningIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer "+engineAPIKey, r.Header.Get("Authorization"))
		_, err := w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		require.NoError(t, err)
	}))
	t.Cleanup(server.Close)
	st, h := setupEngineTest(t, nil)
	st.Mutate(func(s *state.State) {
		s.LLMProviders[engineProviderKey] = state.LLMProviderMeta{Type: "openai-chat", BaseURL: server.URL, DefaultModelKey: "default-model-key", Models: []state.LLMModelMeta{{ModelKey: "default-model-key", ModelID: "gpt-test", Enabled: true}}}
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

func TestEngineTest_GivenDisabledModel_WhenTesting_ThenDoesNotCallUpstream(t *testing.T) {
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	doer := &countingHTTPDoer{}
	h := handlers.NewEngineHandlers(handlers.EngineDeps{State: st, Credentials: &stubCredentials{}, HTTPClient: doer})
	st.Mutate(func(s *state.State) {
		s.LLMProviders[engineProviderKey] = state.LLMProviderMeta{
			Type: "openai-chat", BaseURL: "https://api.example",
			DefaultModelKey: "disabled-model",
			Models:          []state.LLMModelMeta{{ModelKey: "disabled-model", ModelID: "gpt-disabled", Enabled: false}},
		}
	})

	result, err := h.Test(context.Background(), handlers.EngineTestParams{ProviderKey: engineProviderKey})

	require.NoError(t, err)
	assert.False(t, result.OK)
	assert.Zero(t, doer.calls, "a disabled model must not be sent to the upstream provider")
}

func TestEngineTest_GivenProviderAbsentFromSnapshot_WhenTesting_ThenReportsProviderMissingWithoutLeakingCredentials(t *testing.T) {
	st, h := setupEngineTest(t, nil)
	st.Mutate(func(s *state.State) { s.LLMProviders[engineProviderKey] = state.LLMProviderMeta{} })
	result, err := h.Test(context.Background(), handlers.EngineTestParams{ProviderKey: "missing-provider"})
	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, rpcerror.CodeProviderMissing, rpcErr.Code)
	payload, merr := json.Marshal(result)
	require.NoError(t, merr)
	assert.NotContains(t, string(payload)+rpcErr.Message, engineAPIKey)
	assert.NotContains(t, string(payload)+rpcErr.Message, engineProviderKey)
}

func TestEngineTest_GivenKnownProviderWithUnknownModel_WhenTesting_ThenStaysAnOKFalseResult(t *testing.T) {
	st, h := setupEngineTest(t, nil)
	st.Mutate(func(s *state.State) {
		s.LLMProviders[engineProviderKey] = state.LLMProviderMeta{Type: "openai-chat", BaseURL: "https://api.example"}
	})
	result, err := h.Test(context.Background(), handlers.EngineTestParams{ProviderKey: engineProviderKey, ModelKey: "nope"})
	require.NoError(t, err)
	assert.False(t, result.OK)
	assert.NotEmpty(t, result.Message)
}

func TestEngineDiscover_GivenProviderAbsentFromSnapshot_WhenDiscovering_ThenReportsProviderMissing(t *testing.T) {
	_, h := setupEngineTest(t, nil)
	_, err := h.Discover(context.Background(), handlers.EngineDiscoverParams{ProviderKey: "missing-provider"})
	var rpcErr *rpcerror.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, rpcerror.CodeProviderMissing, rpcErr.Code)
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
		s.LLMProviders[engineProviderKey] = state.LLMProviderMeta{Type: "openai-chat", BaseURL: server.URL}
	})

	result, err := h.Discover(context.Background(), handlers.EngineDiscoverParams{ProviderKey: engineProviderKey})
	require.NoError(t, err)
	require.Equal(t, []handlers.EngineModel{{ModelID: "gpt-test", Name: "GPT Test"}, {ModelID: "gpt-unnamed", Name: "gpt-unnamed"}}, result.Models)
	payload, err := json.Marshal(result)
	require.NoError(t, err)
	assert.NotContains(t, string(payload), engineAPIKey)
	assert.NotContains(t, string(payload), engineProviderKey)
}

// Given 账号供应商(state 里只有元数据),When 供应商测试与模型发现要它的 key,
// Then 各按自己的用途申请租约;拿不到时两者都报「凭据租约不可用」,不调上游。
func TestEngine_GivenNoLease_WhenTestingOrDiscovering_ThenBothReportLeaseUnavailable(t *testing.T) {
	st, h, creds := setupEngineTestWithCredentials(t, nil, rpcerror.ErrProviderLeaseUnavailable)
	st.Mutate(func(s *state.State) {
		s.LLMProviders[engineProviderKey] = state.LLMProviderMeta{Type: "openai-chat", BaseURL: "https://api.example", DefaultModelKey: "m", Models: []state.LLMModelMeta{{ModelKey: "m", ModelID: "gpt", Enabled: true}}}
	})

	_, err := h.Test(context.Background(), handlers.EngineTestParams{ProviderKey: engineProviderKey})
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable)
	_, err = h.Discover(context.Background(), handlers.EngineDiscoverParams{ProviderKey: engineProviderKey})
	require.ErrorIs(t, err, rpcerror.ErrProviderLeaseUnavailable)
	assert.Equal(t, []string{"test", "discover"}, creds.purposes)
}

// 本机用 `agentred llm add` 添加的供应商用它自己的 key 测试,不申请租约。
func TestEngineTest_GivenALocalProvider_WhenTesting_ThenUsesItsOwnKeyWithoutALease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer local-key", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	t.Cleanup(server.Close)
	st, h, creds := setupEngineTestWithCredentials(t, nil, rpcerror.ErrProviderLeaseUnavailable)
	require.NoError(t, st.UpsertLocalLLMProvider("local-1", state.LocalLLMProvider{
		LLMProviderMeta: state.LLMProviderMeta{Type: "openai-chat", BaseURL: server.URL, DefaultModelKey: "m", Models: []state.LLMModelMeta{{ModelKey: "m", ModelID: "gpt", Enabled: true}}},
		APIKey:          "local-key",
	}))

	result, err := h.Test(context.Background(), handlers.EngineTestParams{ProviderKey: "local-1"})
	require.NoError(t, err)
	assert.True(t, result.OK)
	assert.Empty(t, creds.purposes)
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
