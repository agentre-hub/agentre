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

func claimedState(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	st.Claim("account-1", "PEM", state.AccountCredential{AccessToken: testAccessToken})
	require.NoError(t, st.Save())
	return st
}

func snapshotBody(providerKey, apiKey, cliPath string) string {
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
	encoded, _ := json.Marshal(map[string]any{"data": payload})
	return string(encoded)
}

func TestManager_GivenSuccessfulSnapshot_WhenPulling_ThenReplacesProvidersAndKeepsCLIPathsMemoryOnly(t *testing.T) {
	st := claimedState(t)
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

func TestManager_GivenSnapshotPullFailure_WhenPullingAgain_ThenKeepsPreviousProvidersAndOverlays(t *testing.T) {
	st := claimedState(t)
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

func TestManager_GivenUnclaimedDaemon_WhenResolvingCLIPath_ThenLeavesPairedDesktopExecutionUntouched(t *testing.T) {
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	manager := New(Options{State: st})

	path, authoritative := manager.ResolveCLIPath("backend-1")
	assert.False(t, authoritative)
	assert.Empty(t, path)
}

// 账号信号(sync_version 等)触发 Pull 的行为不再由 Manager 自己拨号验证——那条
// 独立的 /v1/account/channel 连接与 Manager.WatchAccountChannel /
// dialAccountChannel / consumeAccountChannel 已随决策 13 一起删除:账号信号现在
// 经由 daemon 已经在跑的那条中继连接上的保留通道抵达(relaytransport.SignalChannelID),
// 由 internal/daemon 包的 serveAccountSignal 消费后直接调 Manager.PullAsync——
// 这就是本包不再需要 websocket 拨号测试的原因,也是「Manager 成为已合并连接的
// 消费者」这句话在代码里的样子。那条路由行为的测试见
// internal/daemon/daemon_test.go 的
// TestDaemon_GivenAccountSignalOnTheReservedChannel_WhenReceived_ThenPullsEngineSnapshotWithoutTouchingTheRPCRegistry。
