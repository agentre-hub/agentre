package enginesnapshot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/state"
)

const (
	testAccessToken = "device-access-token"
	testCLIPath     = "/private/bin/claude"
)

func loggedInState(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	st.Login("account-1", state.AccountCredential{AccessToken: testAccessToken})
	require.NoError(t, st.Save())
	return st
}

func snapshotBody(providerKey, apiKey, cliPath string) string {
	return snapshotBodyWithBackends(providerKey, apiKey, cliPath, nil)
}

// snapshotBodyWithBackends 与 snapshotBody 同形,额外带上 backends 数组。backends 为
// nil 时整个键缺席,用来自证「服务端还没下发 backends 时客户端行为一字不变」。
func snapshotBodyWithBackends(providerKey, apiKey, cliPath string, backends []map[string]any) string {
	payload := map[string]any{
		"providers": []map[string]any{{
			"provider_key": providerKey, "name": "Anthropic", "type": "anthropic",
			"base_url": "https://api.example", "api_key": apiKey, "default_model_key": "model-1",
			"models": []map[string]any{{
				"model_key": "model-1", "model_id": "claude-1", "name": "Claude", "enabled": true,
				"context_window": 200000, "max_output": 8192,
			}},
		}},
		"cli_overlays": []map[string]any{{"backend_sync_id": "backend-1", "cli_path": cliPath}},
	}
	if backends != nil {
		payload["backends"] = backends
	}
	encoded, _ := json.Marshal(map[string]any{"data": payload})
	return string(encoded)
}

func TestManager_GivenSuccessfulSnapshot_WhenPulling_ThenReplacesProvidersAndKeepsCLIPathsMemoryOnly(t *testing.T) {
	st := loggedInState(t)
	st.Mutate(func(s *state.State) {
		s.LLMProviders["removed-provider"] = state.LLMProviderMeta{Name: "Removed", APIKey: "removed-key"}
	})
	require.NoError(t, st.Save())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/prefix/v1/engine/snapshot", r.URL.Path)
		assert.Equal(t, "Bearer "+testAccessToken, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, snapshotBody("provider-1", "snapshot-key", testCLIPath))
	}))
	t.Cleanup(server.Close)

	manager := New(Options{
		State:       st,
		ServerURL:   func() string { return server.URL + "/prefix" },
		AccessToken: func() string { return testAccessToken },
		HTTPClient:  server.Client(),
	})
	require.NoError(t, manager.Pull(context.Background()))

	snapshot := st.Snapshot()
	require.Len(t, snapshot.LLMProviders, 1)
	provider := snapshot.LLMProviders["provider-1"]
	assert.Equal(t, "snapshot-key", provider.APIKey)
	assert.Equal(t, "model-1", provider.DefaultModelKey)
	require.Len(t, provider.Models, 1)
	assert.Equal(t, int64(200000), *provider.Models[0].ContextWindow)
	assert.Equal(t, int64(8192), *provider.Models[0].MaxOutput)
	assert.NotContains(t, snapshot.LLMProviders, "removed-provider")

	path, authoritative := manager.ResolveCLIPath("backend-1")
	assert.True(t, authoritative)
	assert.Equal(t, testCLIPath, path)

	onDisk, err := os.ReadFile(filepath.Join(st.Dir(), "state.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(onDisk), testCLIPath, "absolute CLI overlays are execution-only and must not enter state.json")
	assert.NotContains(t, string(onDisk), "cliOverlays")
}

// TestManager_GivenSnapshotWithBackendConfigs_WhenPulling_ThenResolvesWholeConfigBySyncID
// 钉住设备凭据快照的后端配置入口:按 backend_sync_id 寻址,正文是整份 syncwire.
// AgentBackendConfig —— 不是逐键平铺,也不是只有 acp 一档。缺席的同步标识不是
// 权威空值(false),调用方据此保留直接桌面调用带来的配置。
func TestManager_GivenSnapshotWithBackendConfigs_WhenPulling_ThenResolvesWholeConfigBySyncID(t *testing.T) {
	st := loggedInState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, snapshotBodyWithBackends("provider-1", "snapshot-key", testCLIPath, []map[string]any{
			{"backend_sync_id": "be-acp", "config": map[string]any{
				"acpCommand": "npx", "acpArgs": []string{"-y", "@agentclientprotocol/codex-acp"},
			}},
			{"backend_sync_id": "be-codex", "config": map[string]any{
				"sandbox": "read-only", "approval": "never", "modelRoutes": map[string]any{"OPUS": map[string]any{"providerKey": "p", "modelKey": "m"}},
			}},
		}))
	}))
	t.Cleanup(server.Close)
	manager := New(Options{
		State:       st,
		ServerURL:   func() string { return server.URL },
		AccessToken: func() string { return testAccessToken },
		HTTPClient:  server.Client(),
	})
	require.NoError(t, manager.Pull(context.Background()))

	acp, authoritative := manager.ResolveBackendConfig("be-acp")
	require.True(t, authoritative)
	assert.Equal(t, "npx", acp.ACPCommand)
	assert.Equal(t, []string{"-y", "@agentclientprotocol/codex-acp"}, acp.ACPArgs)

	codex, authoritative := manager.ResolveBackendConfig("be-codex")
	require.True(t, authoritative)
	assert.Equal(t, "read-only", codex.Sandbox)
	assert.Equal(t, "never", codex.Approval)
	assert.JSONEq(t, `{"OPUS":{"providerKey":"p","modelKey":"m"}}`, string(codex.ModelRoutes))

	_, authoritative = manager.ResolveBackendConfig("be-absent")
	assert.False(t, authoritative, "an absent entry must not look like an authoritative empty config")
}

// TestManager_GivenBackendConfigWithMutableFields_WhenResolving_ThenReturnsDefensiveCopies
// 快照在多个运行轮次之间共享,消费方改自己手里那份不能反噬下一轮。
func TestManager_GivenBackendConfigWithMutableFields_WhenResolving_ThenReturnsDefensiveCopies(t *testing.T) {
	st := loggedInState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, snapshotBodyWithBackends("provider-1", "snapshot-key", testCLIPath, []map[string]any{
			{"backend_sync_id": "be-acp", "config": map[string]any{
				"acpCommand": "npx", "acpArgs": []string{"-y", "acp"},
				"modelRoutes": map[string]any{"OPUS": map[string]any{"providerKey": "p", "modelKey": "m"}},
			}},
		}))
	}))
	t.Cleanup(server.Close)
	manager := New(Options{
		State:       st,
		ServerURL:   func() string { return server.URL },
		AccessToken: func() string { return testAccessToken },
		HTTPClient:  server.Client(),
	})
	require.NoError(t, manager.Pull(context.Background()))

	first, authoritative := manager.ResolveBackendConfig("be-acp")
	require.True(t, authoritative)
	first.ACPArgs[0] = "tampered"
	first.ModelRoutes[0] = 'X'

	second, authoritative := manager.ResolveBackendConfig("be-acp")
	require.True(t, authoritative)
	assert.Equal(t, []string{"-y", "acp"}, second.ACPArgs)
	assert.JSONEq(t, `{"OPUS":{"providerKey":"p","modelKey":"m"}}`, string(second.ModelRoutes))
}

// TestManager_GivenSnapshotWithoutBackendsKey_WhenPulling_ThenStaysBackwardCompatible
// 旧服务端(没有 backends 键)下发的快照必须照常落地,后端配置一律「无权威项」。
func TestManager_GivenSnapshotWithoutBackendsKey_WhenPulling_ThenStaysBackwardCompatible(t *testing.T) {
	st := loggedInState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, snapshotBody("provider-1", "snapshot-key", testCLIPath))
	}))
	t.Cleanup(server.Close)
	manager := New(Options{
		State:       st,
		ServerURL:   func() string { return server.URL },
		AccessToken: func() string { return testAccessToken },
		HTTPClient:  server.Client(),
	})
	require.NoError(t, manager.Pull(context.Background()))

	path, authoritative := manager.ResolveCLIPath("backend-1")
	assert.True(t, authoritative)
	assert.Equal(t, testCLIPath, path)
	_, authoritative = manager.ResolveBackendConfig("backend-1")
	assert.False(t, authoritative)
}

// TestManager_GivenSnapshotWithUnknownBackendKeys_WhenPulling_ThenIgnoresThemAndKeepsKnownFields
// 新服务端加字段而旧 daemon 未升级时,整份快照不能被一个不认识的键打挂,已知键照常落地。
func TestManager_GivenSnapshotWithUnknownBackendKeys_WhenPulling_ThenIgnoresThemAndKeepsKnownFields(t *testing.T) {
	st := loggedInState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		encoded, _ := json.Marshal(map[string]any{"data": map[string]any{
			"unknown_top_level": "ignored",
			"backends": []map[string]any{{
				"backend_sync_id":  "be-acp",
				"future_entry_key": 7,
				"config":           map[string]any{"acpCommand": "npx", "futureConfigKey": true},
			}},
		}})
		_, _ = w.Write(encoded)
	}))
	t.Cleanup(server.Close)
	manager := New(Options{
		State:       st,
		ServerURL:   func() string { return server.URL },
		AccessToken: func() string { return testAccessToken },
		HTTPClient:  server.Client(),
	})
	require.NoError(t, manager.Pull(context.Background()))

	cfg, authoritative := manager.ResolveBackendConfig("be-acp")
	require.True(t, authoritative)
	assert.Equal(t, "npx", cfg.ACPCommand)
}

func TestManager_GivenLoggedOutDaemon_WhenResolvingBackendConfig_ThenReportsNoAuthoritativeEntry(t *testing.T) {
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	manager := New(Options{State: st})

	_, authoritative := manager.ResolveBackendConfig("be-acp")
	assert.False(t, authoritative)
}

func TestManager_GivenSnapshotPullFailure_WhenPullingAgain_ThenKeepsPreviousProvidersAndOverlays(t *testing.T) {
	st := loggedInState(t)
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, snapshotBody("provider-1", "stable-key", testCLIPath))
	}))
	t.Cleanup(server.Close)
	manager := New(Options{
		State:       st,
		ServerURL:   func() string { return server.URL },
		AccessToken: func() string { return testAccessToken },
		HTTPClient:  server.Client(),
	})
	require.NoError(t, manager.Pull(context.Background()))
	before := st.Snapshot().LLMProviders
	fail.Store(true)

	err := manager.Pull(context.Background())
	require.Error(t, err)
	assert.Equal(t, before, st.Snapshot().LLMProviders)
	path, authoritative := manager.ResolveCLIPath("backend-1")
	assert.True(t, authoritative)
	assert.Equal(t, testCLIPath, path)
}

func TestManager_GivenLoggedOutDaemon_WhenResolvingCLIPath_ThenLeavesPairedDesktopExecutionUntouched(t *testing.T) {
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	manager := New(Options{State: st})

	path, authoritative := manager.ResolveCLIPath("backend-1")
	assert.False(t, authoritative)
	assert.Empty(t, path)
}

// 账号信号(sync_version 等)触发 Pull 不由 Manager 自己拨号:账号信号经由 daemon 那条
// 中继连接上的保留通道抵达(relaytransport.SignalChannelID),由 internal/daemon 包的
// serveAccountSignal 消费后直接调 Manager.PullAsync——所以本包没有 websocket 拨号测试。
// 那条路由行为的测试见 internal/daemon/daemon_test.go 的
// TestDaemon_GivenAccountSignalOnTheReservedChannel_WhenReceived_ThenPullsEngineSnapshotWithoutTouchingTheRPCRegistry。
