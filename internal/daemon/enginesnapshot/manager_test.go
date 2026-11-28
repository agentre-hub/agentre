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
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

const (
	testAccessToken = "device-access-token"
	testCLIPath     = "/private/bin/claude"
	// leakedKey 是一份「不该出现」的 key:老 server 仍在快照里下发 api_key 时,它绝不能
	// 落进 state.json。
	leakedKey = "sk-leaked-from-an-old-server"
)

func loggedInState(t *testing.T) *state.State {
	t.Helper()
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	st.Login("account-1", state.AccountCredential{AccessToken: testAccessToken})
	require.NoError(t, st.Save())
	return st
}

func testProvider(providerKey string) syncwire.SnapshotProvider {
	return syncwire.SnapshotProvider{
		ProviderKey: providerKey, Name: "Anthropic", Type: "anthropic", BaseURL: "https://api.example",
		DefaultModelKey: "model-1",
		Models: []syncwire.LLMProviderModel{{
			ModelKey: "model-1", ModelID: "claude-1", Name: "Claude", Enabled: true,
			ContextWindow: 200000, MaxOutput: 8192,
		}},
	}
}

func profile(syncID, typ string, cfg syncwire.AgentBackendConfig) syncwire.SnapshotEngineProfile {
	return syncwire.SnapshotEngineProfile{SyncID: syncID, EngineProfilePayload: syncwire.EngineProfilePayload{Type: typ, Name: syncID, Config: cfg}}
}

func encodeSnapshot(snapshot syncwire.EngineSnapshot) string {
	encoded, _ := json.Marshal(map[string]any{"data": snapshot})
	return string(encoded)
}

func serve(t *testing.T, body func() string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body())
	}))
	t.Cleanup(server.Close)
	return server
}

func newManager(st *state.State, server *httptest.Server) *Manager {
	return New(Options{
		State:       st,
		ServerURL:   func() string { return server.URL },
		AccessToken: func() string { return testAccessToken },
		HTTPClient:  server.Client(),
	})
}

func TestManager_GivenSuccessfulSnapshot_WhenPulling_ThenReplacesProviderMetadataAndKeepsCLIPathsMemoryOnly(t *testing.T) {
	st := loggedInState(t)
	st.Mutate(func(s *state.State) {
		s.LLMProviders["removed-provider"] = state.LLMProviderMeta{Name: "Removed"}
	})
	require.NoError(t, st.Save())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/prefix"+syncwire.PathEngineSnapshot, r.URL.Path)
		assert.Equal(t, "Bearer "+testAccessToken, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, encodeSnapshot(syncwire.EngineSnapshot{
			Providers: []syncwire.SnapshotProvider{testProvider("provider-1")},
			CLIPaths:  []syncwire.SnapshotCLIPath{{Runtime: "claudecode", CLIPath: testCLIPath}},
		}))
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
	assert.Equal(t, "https://api.example", provider.BaseURL)
	assert.Equal(t, "model-1", provider.DefaultModelKey)
	require.Len(t, provider.Models, 1)
	assert.Equal(t, int64(200000), *provider.Models[0].ContextWindow)
	assert.Equal(t, int64(8192), *provider.Models[0].MaxOutput)
	assert.NotContains(t, snapshot.LLMProviders, "removed-provider")

	path, authoritative := manager.ResolveCLIPath("claudecode")
	assert.True(t, authoritative)
	assert.Equal(t, testCLIPath, path)
	path, authoritative = manager.ResolveCLIPath("codex")
	assert.True(t, authoritative, "after a snapshot an absent runtime authoritatively means PATH")
	assert.Empty(t, path)

	onDisk, err := os.ReadFile(filepath.Join(st.Dir(), "state.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(onDisk), testCLIPath, "absolute CLI paths are execution-only and must not enter state.json")
}

// Given 一个仍在快照里下发 api_key 的老 server,When 拉取并应用,Then state.json 里没有
// 那份 key 的任何字节 —— 快照只带元数据,key 从不落盘。
func TestManager_GivenASnapshotThatStillCarriesAKey_WhenPulling_ThenNoKeyBytesReachStateJSON(t *testing.T) {
	st := loggedInState(t)
	server := serve(t, func() string {
		encoded, _ := json.Marshal(map[string]any{"data": map[string]any{
			"providers": []map[string]any{{
				"provider_key": "provider-1", "name": "Anthropic", "type": "anthropic",
				"base_url": "https://api.example", "api_key": leakedKey, "default_model_key": "model-1",
			}},
		}})
		return string(encoded)
	})
	require.NoError(t, newManager(st, server).Pull(context.Background()))

	require.Contains(t, st.Snapshot().LLMProviders, "provider-1")
	onDisk, err := os.ReadFile(filepath.Join(st.Dir(), "state.json"))
	require.NoError(t, err)
	assert.NotContains(t, string(onDisk), leakedKey)
}

// 本地用 `agentred llm add` 添加的供应商是那台机器上的明确操作:快照整份替换账号供应商时
// 碰不到它,它的 key 照旧在。
func TestManager_GivenALocallyAddedProvider_WhenASnapshotIsApplied_ThenItSurvivesWithItsKey(t *testing.T) {
	st := loggedInState(t)
	require.NoError(t, st.UpsertLocalLLMProvider("local-1", state.LocalLLMProvider{
		LLMProviderMeta: state.LLMProviderMeta{Name: "Ollama", Type: "openai-chat", BaseURL: "http://127.0.0.1:11434"},
		APIKey:          "local-key",
	}))
	server := serve(t, func() string {
		return encodeSnapshot(syncwire.EngineSnapshot{Providers: []syncwire.SnapshotProvider{testProvider("provider-1")}})
	})
	require.NoError(t, newManager(st, server).Pull(context.Background()))

	snapshot := st.Snapshot()
	assert.Contains(t, snapshot.LLMProviders, "provider-1")
	require.Contains(t, snapshot.LocalLLMProviders, "local-1")
	assert.Equal(t, "local-key", snapshot.LocalLLMProviders["local-1"].APIKey)
}

// 引擎配置按 sync_id 寻址,带类型与整份 config;缺席的 sync_id 不是权威空值。
func TestManager_GivenSnapshotWithEngineProfiles_WhenPulling_ThenResolvesTheWholeProfileBySyncID(t *testing.T) {
	st := loggedInState(t)
	cc := profile("eng-cc", "claudecode", syncwire.AgentBackendConfig{
		Sandbox: "read-only", ModelRoutes: json.RawMessage(`{"OPUS":{"providerKey":"p","modelKey":"m"}}`),
	})
	cc.ProviderKey, cc.ModelKey, cc.ReasoningEffort, cc.EnvJSON = "provider-1", "model-1", "high", `{"HTTPS_PROXY":"http://127.0.0.1:7890"}`
	server := serve(t, func() string {
		return encodeSnapshot(syncwire.EngineSnapshot{EngineProfiles: []syncwire.SnapshotEngineProfile{
			cc,
			profile("eng-acp", "acp", syncwire.AgentBackendConfig{ACPCommand: "npx", ACPArgs: []string{"-y", "@agentclientprotocol/codex-acp"}}),
		}})
	})
	manager := newManager(st, server)
	require.NoError(t, manager.Pull(context.Background()))

	got, authoritative := manager.ResolveEngineProfile("eng-cc")
	require.True(t, authoritative)
	assert.Equal(t, "claudecode", got.Type)
	assert.Equal(t, "provider-1", got.ProviderKey)
	assert.Equal(t, "model-1", got.ModelKey)
	assert.Equal(t, "high", got.ReasoningEffort)
	assert.Equal(t, `{"HTTPS_PROXY":"http://127.0.0.1:7890"}`, got.EnvJSON)
	assert.Equal(t, "read-only", got.Sandbox)
	assert.JSONEq(t, `{"OPUS":{"providerKey":"p","modelKey":"m"}}`, string(got.ModelRoutes))

	acp, authoritative := manager.ResolveEngineProfile("eng-acp")
	require.True(t, authoritative)
	assert.Equal(t, "npx", acp.ACPCommand)
	assert.Equal(t, []string{"-y", "@agentclientprotocol/codex-acp"}, acp.ACPArgs)

	_, authoritative = manager.ResolveEngineProfile("eng-absent")
	assert.False(t, authoritative, "an absent entry must not look like an authoritative empty profile")
}

// 快照在多个运行轮次之间共享,消费方改自己手里那份不能反噬下一轮。
func TestManager_GivenProfileWithMutableFields_WhenResolving_ThenReturnsDefensiveCopies(t *testing.T) {
	st := loggedInState(t)
	server := serve(t, func() string {
		return encodeSnapshot(syncwire.EngineSnapshot{EngineProfiles: []syncwire.SnapshotEngineProfile{
			profile("eng-acp", "acp", syncwire.AgentBackendConfig{
				ACPCommand: "npx", ACPArgs: []string{"-y", "acp"},
				ModelRoutes: json.RawMessage(`{"OPUS":{"providerKey":"p","modelKey":"m"}}`),
			}),
		}})
	})
	manager := newManager(st, server)
	require.NoError(t, manager.Pull(context.Background()))

	first, authoritative := manager.ResolveEngineProfile("eng-acp")
	require.True(t, authoritative)
	first.ACPArgs[0] = "tampered"
	first.ModelRoutes[0] = 'X'

	second, authoritative := manager.ResolveEngineProfile("eng-acp")
	require.True(t, authoritative)
	assert.Equal(t, []string{"-y", "acp"}, second.ACPArgs)
	assert.JSONEq(t, `{"OPUS":{"providerKey":"p","modelKey":"m"}}`, string(second.ModelRoutes))
}

// 新服务端加字段而旧 daemon 未升级时,整份快照不能被一个不认识的键打挂,已知键照常落地。
func TestManager_GivenSnapshotWithUnknownKeys_WhenPulling_ThenIgnoresThemAndKeepsKnownFields(t *testing.T) {
	st := loggedInState(t)
	server := serve(t, func() string {
		encoded, _ := json.Marshal(map[string]any{"data": map[string]any{
			"unknown_top_level": "ignored",
			"engine_profiles": []map[string]any{{
				"sync_id": "eng-acp", "type": "acp", "future_entry_key": 7,
				"config": map[string]any{"acpCommand": "npx", "futureConfigKey": true},
			}},
		}})
		return string(encoded)
	})
	manager := newManager(st, server)
	require.NoError(t, manager.Pull(context.Background()))

	cfg, authoritative := manager.ResolveEngineProfile("eng-acp")
	require.True(t, authoritative)
	assert.Equal(t, "npx", cfg.ACPCommand)
}

func TestManager_GivenLoggedOutDaemon_WhenResolving_ThenReportsNoAuthoritativeEntry(t *testing.T) {
	st, err := state.Load(t.TempDir())
	require.NoError(t, err)
	manager := New(Options{State: st})

	_, authoritative := manager.ResolveEngineProfile("eng-acp")
	assert.False(t, authoritative)
	path, authoritative := manager.ResolveCLIPath("claudecode")
	assert.False(t, authoritative, "a paired desktop's own CLI path is left untouched")
	assert.Empty(t, path)
}

func TestManager_GivenSnapshotPullFailure_WhenPullingAgain_ThenKeepsPreviousProvidersAndPaths(t *testing.T) {
	st := loggedInState(t)
	var fail atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, encodeSnapshot(syncwire.EngineSnapshot{
			Providers: []syncwire.SnapshotProvider{testProvider("provider-1")},
			CLIPaths:  []syncwire.SnapshotCLIPath{{Runtime: "claudecode", CLIPath: testCLIPath}},
		}))
	}))
	t.Cleanup(server.Close)
	manager := newManager(st, server)
	require.NoError(t, manager.Pull(context.Background()))
	before := st.Snapshot().LLMProviders
	fail.Store(true)

	require.Error(t, manager.Pull(context.Background()))
	assert.Equal(t, before, st.Snapshot().LLMProviders)
	path, authoritative := manager.ResolveCLIPath("claudecode")
	assert.True(t, authoritative)
	assert.Equal(t, testCLIPath, path)
}

// 账号信号(sync_version 等)触发 Pull 不由 Manager 自己拨号:账号信号经由 daemon 那条
// 中继连接上的保留通道抵达(relaytransport.SignalChannelID),由 internal/daemon 包的
// serveAccountSignal 消费后直接调 Manager.PullAsync——所以本包没有 websocket 拨号测试。

// Given 快照里有 ACP、Hermes、OpenClaw 与普通 CLI 引擎配置,When 设备情况要列账号引擎,
// Then 只交出有设备事实的三类,按引擎配置自己的类型认、按引擎配置的 sync_id 上报,
// 且每次成功应用快照之后通知一次,设备情况据此重新核对。
func TestManager_GivenProfilesInSnapshot_WhenListingEngines_ThenServiceAndACPEnginesAreKeyedByProfileAndApplyIsAnnounced(t *testing.T) {
	st := loggedInState(t)
	server := serve(t, func() string {
		return encodeSnapshot(syncwire.EngineSnapshot{EngineProfiles: []syncwire.SnapshotEngineProfile{
			profile("eng-acp", "acp", syncwire.AgentBackendConfig{ACPCommand: "gemini", ACPArgs: []string{"--acp"}}),
			profile("eng-hermes", "hermes", syncwire.AgentBackendConfig{HermesURL: "http://hermes.local:8642"}),
			profile("eng-openclaw", "openclaw", syncwire.AgentBackendConfig{OpenClawGatewayURL: "ws://gw.local"}),
			profile("eng-claude", "claudecode", syncwire.AgentBackendConfig{DefaultModel: "opus"}),
			// 类型说了算:一份 claudecode 配置哪怕带着别的类型的键也不是 ACP。
			profile("eng-claude-stray", "claudecode", syncwire.AgentBackendConfig{ACPCommand: "stray"}),
		}})
	})

	var applied atomic.Int32
	manager := New(Options{
		State:       st,
		ServerURL:   func() string { return server.URL },
		AccessToken: func() string { return testAccessToken },
		HTTPClient:  server.Client(),
		OnApplied:   func() { applied.Add(1) },
	})
	assert.Empty(t, manager.Engines(), "快照拉到之前没有账号引擎")

	require.NoError(t, manager.Pull(context.Background()))
	assert.Equal(t, int32(1), applied.Load())
	assert.Equal(t, []devicefacts.Engine{
		{SyncID: "eng-acp", Runtime: "acp", ACPCommand: "gemini"},
		{SyncID: "eng-hermes", Runtime: "hermes", HermesURL: "http://hermes.local:8642"},
		{SyncID: "eng-openclaw", Runtime: "openclaw", OpenClawGatewayURL: "ws://gw.local"},
	}, manager.Engines())
}

// 拉取失败不算一次应用:设备情况不必为一份没变的快照重探。
func TestManager_GivenFailedPull_ThenApplyIsNotAnnounced(t *testing.T) {
	st := loggedInState(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	var applied atomic.Int32
	manager := New(Options{
		State: st, ServerURL: func() string { return server.URL }, AccessToken: func() string { return testAccessToken },
		HTTPClient: server.Client(), OnApplied: func() { applied.Add(1) },
	})
	require.Error(t, manager.Pull(context.Background()))
	assert.Equal(t, int32(0), applied.Load())
}
