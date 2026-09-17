package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/state"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesauth"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesgateway"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 本文件的替身凭据值都刻意取「一眼能在 state.json、应答、日志里搜到」的字面量:
// 用例要证的正是它们只出现在该出现的地方。
//
//nolint:gosec // G101: 本块全是用例造的替身凭据与标识,不是真凭据。
const (
	credTestPassword      = "hunter2-password-secret"
	credTestGatewayToken  = "gateway-token-saved-secret"
	credTestDraftToken    = "gateway-token-draft-secret"
	credTestRefreshToken  = "hermes-refresh-secret"
	credTestAccessToken   = "hermes-access-secret"
	credTestSyncID        = "3f6a1c52-2b8e-4c1d-9e7a-5b4c3d2e1f00"
	credTestOtherSyncID   = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
	credTestOpenClawURL   = "ws://127.0.0.1:18789"
	credTestHermesUser    = "alice"
	credTestHermesUserID  = "7"
	credTestHermesService = "basic"
)

// fakeHermesServe 是一个开了认证的 `hermes serve` 的认证端点替身(原生 PKCE 密码登录)。
type fakeHermesServe struct {
	mu    sync.Mutex
	state string
}

func newFakeHermesServe(t *testing.T) string {
	t.Helper()
	f := &fakeHermesServe{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/providers", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"providers":[{"name":"oidc","display_name":"SSO","supports_password":false},{"name":"basic","display_name":"Password","supports_password":true}]}`))
	})
	mux.HandleFunc("/auth/native/authorize", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.state = r.URL.Query().Get("state")
		f.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: hermesauth.PKCECookieName, Value: "c", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/auth/password-login", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["username"] != credTestHermesUser || body["password"] != credTestPassword {
			http.Error(w, "bad creds", http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		state := f.state
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "next": "http://127.0.0.1:54999/cb?code=c1&state=" + url.QueryEscape(state)})
	})
	mux.HandleFunc("/auth/native/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": credTestAccessToken, "refresh_token": credTestRefreshToken,
			"provider": credTestHermesService, "user_id": 7,
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server.URL
}

type credentialFixture struct {
	dir      string
	st       *state.State
	h        *handlers.BackendCredentialHandlers
	logs     *observer.ObservedLogs
	ctx      context.Context
	hermes   *hermesProbeRecorder
	openclaw *openClawProbeRecorder
}

type hermesProbeRecorder struct {
	request hermesgateway.ProbeRequest
	token   string
	err     error
}

type openClawProbeRecorder struct {
	config    openclawgateway.Config
	selection openclawgateway.ProbeSelection
	result    *openclawgateway.ProbeResult
	err       error
}

func newCredentialFixture(t *testing.T) *credentialFixture {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	core, logs := observer.New(zapcore.DebugLevel)
	f := &credentialFixture{
		dir: dir, st: st, logs: logs,
		ctx:      logger.WithContextLogger(context.Background(), zap.New(core)),
		hermes:   &hermesProbeRecorder{},
		openclaw: &openClawProbeRecorder{result: &openclawgateway.ProbeResult{}},
	}
	f.h = handlers.NewBackendCredentialHandlers(handlers.BackendCredentialDeps{
		State: st,
		ProbeHermes: func(ctx context.Context, req hermesgateway.ProbeRequest) (string, error) {
			f.hermes.request = req
			if f.hermes.err != nil {
				return "", f.hermes.err
			}
			// 真实探测在开了认证的 serve 上会向凭据源要 bearer:替身照做,证明设备
			// 拿的是自己 state.json 里的登录态。
			if req.Credentials != nil {
				token, err := req.Credentials.AccessToken(ctx, req.URL, req.AuthProvider)
				if err != nil {
					return "", err
				}
				f.hermes.token = token
			}
			return req.URL, nil
		},
		ProbeOpenClaw: func(_ context.Context, config openclawgateway.Config, selection openclawgateway.ProbeSelection) (*openclawgateway.ProbeResult, error) {
			f.openclaw.config = config
			f.openclaw.selection = selection
			return f.openclaw.result, f.openclaw.err
		},
	})
	return f
}

// stateFile 读回磁盘上的 state.json 原文,给「落没落盘」「有没有明文」两类断言共用。
func (f *credentialFixture) stateFile(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.dir, "state.json"))
	require.NoError(t, err)
	return string(raw)
}

func (f *credentialFixture) requireLogsFreeOf(t *testing.T, secrets ...string) {
	t.Helper()
	for _, entry := range f.logs.All() {
		line := entry.Message + fmt.Sprint(entry.ContextMap())
		for _, secret := range secrets {
			assert.NotContains(t, line, secret, "日志里出现了明文凭据")
		}
	}
}

func requireInvalidParams(t *testing.T, err error) {
	t.Helper()
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.Equal(t, protorpc.CodeInvalidParams, rpcErr.Code)
}

// ── 保存 / 清除 OpenClaw token ──────────────────────────────────────────────

func TestBackendCredential_SetOpenClawToken_GivenAToken_ThenItLandsInStateJSONUnderTheSyncIDSlot(t *testing.T) {
	f := newCredentialFixture(t)

	resp, err := f.h.SetOpenClawToken(f.ctx, &agentrewire.OpenClawTokenSetRequest{SyncId: credTestSyncID, Token: credTestGatewayToken})

	require.NoError(t, err)
	assert.True(t, resp.GetTokenSaved())
	reloaded, err := state.Load(f.dir)
	require.NoError(t, err)
	stored, ok := reloaded.BackendCredential(backendcred.OpenClawTokenAccount(credTestSyncID))
	assert.True(t, ok, "槽位按 sync_id 取(决定 5)")
	assert.Equal(t, credTestGatewayToken, stored)
	status, err := f.h.Status(f.ctx, &agentrewire.BackendCredentialStatusRequest{BackendType: "openclaw", SyncId: credTestSyncID})
	require.NoError(t, err)
	assert.True(t, status.GetOpenclawTokenSaved())
	other, err := f.h.Status(f.ctx, &agentrewire.BackendCredentialStatusRequest{BackendType: "openclaw", SyncId: credTestOtherSyncID})
	require.NoError(t, err)
	assert.False(t, other.GetOpenclawTokenSaved(), "另一个后端的槽位不受影响")
	f.requireLogsFreeOf(t, credTestGatewayToken)
}

func TestBackendCredential_SetOpenClawToken_GivenClear_ThenTheSlotIsRemovedOnDisk(t *testing.T) {
	f := newCredentialFixture(t)
	_, err := f.h.SetOpenClawToken(f.ctx, &agentrewire.OpenClawTokenSetRequest{SyncId: credTestSyncID, Token: credTestGatewayToken})
	require.NoError(t, err)

	resp, err := f.h.SetOpenClawToken(f.ctx, &agentrewire.OpenClawTokenSetRequest{SyncId: credTestSyncID, Clear: true})

	require.NoError(t, err)
	assert.False(t, resp.GetTokenSaved())
	assert.NotContains(t, f.stateFile(t), credTestGatewayToken)
	status, err := f.h.Status(f.ctx, &agentrewire.BackendCredentialStatusRequest{BackendType: "openclaw", SyncId: credTestSyncID})
	require.NoError(t, err)
	assert.False(t, status.GetOpenclawTokenSaved())
}

func TestBackendCredential_SetOpenClawToken_GivenAMalformedRequest_ThenInvalidParamsAndNothingStored(t *testing.T) {
	f := newCredentialFixture(t)

	for name, req := range map[string]*agentrewire.OpenClawTokenSetRequest{
		"blank sync_id":           {SyncId: "  ", Token: credTestGatewayToken},
		"neither token nor clear": {SyncId: credTestSyncID},
		"token and clear":         {SyncId: credTestSyncID, Token: credTestGatewayToken, Clear: true},
	} {
		_, err := f.h.SetOpenClawToken(f.ctx, req)
		requireInvalidParams(t, err)
		assert.NotContains(t, f.stateFile(t), credTestGatewayToken, name)
	}
}

func TestBackendCredential_SetOpenClawToken_GivenTheStateCannotBeWritten_ThenAReadableErrorWithoutTheToken(t *testing.T) {
	f := newCredentialFixture(t)
	require.NoError(t, os.Mkdir(filepath.Join(f.dir, "state.json.tmp"), 0o700))

	_, err := f.h.SetOpenClawToken(f.ctx, &agentrewire.OpenClawTokenSetRequest{SyncId: credTestSyncID, Token: credTestGatewayToken})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), credTestGatewayToken)
	status, statusErr := f.h.Status(f.ctx, &agentrewire.BackendCredentialStatusRequest{BackendType: "openclaw", SyncId: credTestSyncID})
	require.NoError(t, statusErr)
	assert.False(t, status.GetOpenclawTokenSaved(), "没落盘就不能报已保存")
	f.requireLogsFreeOf(t, credTestGatewayToken)
}

// ── 查询凭据状态 ────────────────────────────────────────────────────────────

func TestBackendCredential_Status_GivenAnUnknownTypeOrMissingKey_ThenInvalidParams(t *testing.T) {
	f := newCredentialFixture(t)

	for _, req := range []*agentrewire.BackendCredentialStatusRequest{
		{BackendType: "claudecode", SyncId: credTestSyncID},
		{BackendType: "openclaw"},
		{BackendType: "hermes", HermesUrl: "not a url::"},
	} {
		_, err := f.h.Status(f.ctx, req)
		requireInvalidParams(t, err)
	}
}

// ── Hermes:提供方、登录、登出 ─────────────────────────────────────────────

func TestBackendCredential_HermesAuthProviders_GivenAReachableServe_ThenListsTheDirectory(t *testing.T) {
	f := newCredentialFixture(t)
	serve := newFakeHermesServe(t)

	resp, err := f.h.HermesAuthProviders(f.ctx, &agentrewire.HermesAuthProvidersRequest{HermesUrl: serve})

	require.NoError(t, err)
	assert.Empty(t, resp.GetCode())
	require.Len(t, resp.GetProviders(), 2)
	assert.Equal(t, "basic", resp.GetProviders()[1].GetName())
	assert.Equal(t, "Password", resp.GetProviders()[1].GetDisplayName())
	assert.True(t, resp.GetProviders()[1].GetSupportsPassword())
	assert.False(t, resp.GetProviders()[0].GetSupportsPassword())
}

func TestBackendCredential_HermesAuthProviders_GivenNothingListens_ThenUnreachableCode(t *testing.T) {
	f := newCredentialFixture(t)
	server := httptest.NewServer(http.NotFoundHandler())
	gone := server.URL
	server.Close()

	resp, err := f.h.HermesAuthProviders(f.ctx, &agentrewire.HermesAuthProvidersRequest{HermesUrl: gone})

	require.NoError(t, err)
	assert.Equal(t, "HERMES_UNREACHABLE", resp.GetCode())
	assert.Empty(t, resp.GetProviders())
}

func TestBackendCredential_HermesLogin_GivenValidPassword_ThenOnlyTheRefreshTokenLandsInStateJSONUnderTheURLSlot(t *testing.T) {
	f := newCredentialFixture(t)
	serve := newFakeHermesServe(t)

	resp, err := f.h.HermesLogin(f.ctx, &agentrewire.HermesLoginRequest{HermesUrl: serve + "/", Username: credTestHermesUser, Password: credTestPassword})

	require.NoError(t, err)
	assert.Empty(t, resp.GetCode())
	assert.Equal(t, credTestHermesService, resp.GetProvider())
	assert.Equal(t, credTestHermesUserID, resp.GetUserId())
	reloaded, err := state.Load(f.dir)
	require.NoError(t, err)
	stored, ok := reloaded.BackendCredential(backendcred.HermesAccount(serve))
	assert.True(t, ok, "槽位按规范化 URL 取(决定 5)")
	assert.Equal(t, credTestRefreshToken, stored)
	onDisk := f.stateFile(t)
	assert.NotContains(t, onDisk, credTestPassword, "密码只用于这一次登录,不落盘")
	assert.NotContains(t, onDisk, credTestAccessToken, "access token 只缓存在内存里")

	status, err := f.h.Status(f.ctx, &agentrewire.BackendCredentialStatusRequest{BackendType: "hermes", HermesUrl: serve})
	require.NoError(t, err)
	assert.True(t, status.GetHermesLoggedIn())
	assert.Equal(t, credTestHermesService, status.GetHermesProvider())
	assert.Equal(t, credTestHermesUserID, status.GetHermesUserId())
	f.requireLogsFreeOf(t, credTestPassword, credTestRefreshToken, credTestAccessToken)
}

func TestBackendCredential_HermesLogin_GivenAWrongPassword_ThenInvalidCredentialsCodeAndNothingStored(t *testing.T) {
	f := newCredentialFixture(t)
	serve := newFakeHermesServe(t)

	resp, err := f.h.HermesLogin(f.ctx, &agentrewire.HermesLoginRequest{HermesUrl: serve, Username: credTestHermesUser, Password: "wrong-" + credTestPassword})

	require.NoError(t, err)
	assert.Equal(t, "HERMES_INVALID_CREDENTIALS", resp.GetCode())
	assert.Empty(t, resp.GetProvider())
	status, err := f.h.Status(f.ctx, &agentrewire.BackendCredentialStatusRequest{BackendType: "hermes", HermesUrl: serve})
	require.NoError(t, err)
	assert.False(t, status.GetHermesLoggedIn())
	assert.NotContains(t, f.stateFile(t), credTestPassword)
	f.requireLogsFreeOf(t, credTestPassword)
}

func TestBackendCredential_HermesLogin_GivenAServeWithOnlyBrowserProviders_ThenProviderUnsupportedCode(t *testing.T) {
	f := newCredentialFixture(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/providers", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"providers":[{"name":"oidc","supports_password":false}]}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	resp, err := f.h.HermesLogin(f.ctx, &agentrewire.HermesLoginRequest{HermesUrl: server.URL, Username: credTestHermesUser, Password: credTestPassword})

	require.NoError(t, err)
	assert.Equal(t, "HERMES_PROVIDER_UNSUPPORTED", resp.GetCode())
}

func TestBackendCredential_HermesLogout_GivenALogin_ThenTheURLSlotAndIdentityAreGone(t *testing.T) {
	f := newCredentialFixture(t)
	serve := newFakeHermesServe(t)
	_, err := f.h.HermesLogin(f.ctx, &agentrewire.HermesLoginRequest{HermesUrl: serve, Username: credTestHermesUser, Password: credTestPassword})
	require.NoError(t, err)

	_, err = f.h.HermesLogout(f.ctx, &agentrewire.HermesLogoutRequest{HermesUrl: serve})

	require.NoError(t, err)
	onDisk := f.stateFile(t)
	assert.NotContains(t, onDisk, credTestRefreshToken)
	status, err := f.h.Status(f.ctx, &agentrewire.BackendCredentialStatusRequest{BackendType: "hermes", HermesUrl: serve})
	require.NoError(t, err)
	assert.False(t, status.GetHermesLoggedIn())
	assert.Empty(t, status.GetHermesProvider())
	// 登出后缓存的 access token 也不能再被拿去连。
	test, err := f.h.TestConnection(f.ctx, &agentrewire.BackendConnectionTestRequest{BackendType: "hermes", HermesUrl: serve})
	require.NoError(t, err)
	assert.Equal(t, "HERMES_LOGIN_REQUIRED", test.GetCode())
}

// ── 测试连接 ────────────────────────────────────────────────────────────────

func TestBackendCredential_TestConnection_GivenAHermesLogin_ThenProbesWithTheDeviceCredential(t *testing.T) {
	f := newCredentialFixture(t)
	serve := newFakeHermesServe(t)
	_, err := f.h.HermesLogin(f.ctx, &agentrewire.HermesLoginRequest{HermesUrl: serve, Username: credTestHermesUser, Password: credTestPassword})
	require.NoError(t, err)

	resp, err := f.h.TestConnection(f.ctx, &agentrewire.BackendConnectionTestRequest{BackendType: "hermes", HermesUrl: serve + "/", HermesAuthProvider: credTestHermesService})

	require.NoError(t, err)
	assert.True(t, resp.GetOk())
	assert.Empty(t, resp.GetCode())
	assert.Equal(t, serve, f.hermes.request.URL, "探测用规范化后的 URL")
	assert.Equal(t, credTestHermesService, f.hermes.request.AuthProvider)
	assert.Equal(t, credTestAccessToken, f.hermes.token, "探测拿到的是这台设备登录得到的凭据")
	assert.NotContains(t, resp.String(), credTestAccessToken)
	f.requireLogsFreeOf(t, credTestPassword, credTestRefreshToken, credTestAccessToken)
}

func TestBackendCredential_TestConnection_GivenHermesFailures_ThenTheDesktopResultCodes(t *testing.T) {
	for probeErr, want := range map[error]string{
		hermesauth.ErrLoginRequired:           "HERMES_LOGIN_REQUIRED",
		hermesauth.ErrLoginExpired:            "HERMES_LOGIN_EXPIRED",
		hermesgateway.ErrGatewayUnreachable:   "HERMES_UNREACHABLE",
		hermesauth.ErrAuthUnreachable:         "HERMES_UNREACHABLE",
		hermesauth.ErrAuthProviderUnavailable: "HERMES_PROVIDER_UNAVAILABLE",
	} {
		f := newCredentialFixture(t)
		f.hermes.err = fmt.Errorf("%w: detail", probeErr)

		resp, err := f.h.TestConnection(f.ctx, &agentrewire.BackendConnectionTestRequest{BackendType: "hermes", HermesUrl: "http://127.0.0.1:9119"})

		require.NoError(t, err)
		assert.False(t, resp.GetOk())
		assert.Equal(t, want, resp.GetCode(), probeErr.Error())
	}
}

func TestBackendCredential_TestConnection_GivenASavedOpenClawToken_ThenProbesWithItAndTheDeviceIdentity(t *testing.T) {
	f := newCredentialFixture(t)
	_, err := f.h.SetOpenClawToken(f.ctx, &agentrewire.OpenClawTokenSetRequest{SyncId: credTestSyncID, Token: credTestGatewayToken})
	require.NoError(t, err)
	f.openclaw.result = &openclawgateway.ProbeResult{
		GatewayVersion: "2026.7.1", Protocol: 4, GrantedScopes: []string{"operator.read"},
		Agents: []openclawgateway.AgentSummary{{ID: "main", Name: "Main", PrimaryModel: "openai/gpt-5", Default: true}},
		Models: []openclawgateway.ModelSummary{{ID: "openai/gpt-5", Name: "GPT-5", Provider: "openai", Available: true}},
	}

	resp, err := f.h.TestConnection(f.ctx, &agentrewire.BackendConnectionTestRequest{
		BackendType: "openclaw", SyncId: credTestSyncID, OpenclawGatewayUrl: credTestOpenClawURL,
		OpenclawAgentId: "main", OpenclawDefaultModel: "openai/gpt-5",
	})

	require.NoError(t, err)
	assert.True(t, resp.GetOk())
	assert.Equal(t, credTestGatewayToken, f.openclaw.config.Token)
	assert.Equal(t, credTestOpenClawURL, f.openclaw.config.URL)
	require.NotNil(t, f.openclaw.config.Identity, "设备身份在本机生成")
	assert.Equal(t, openclawgateway.ProbeSelection{AgentID: "main", Model: "openai/gpt-5"}, f.openclaw.selection)
	assert.Equal(t, "2026.7.1", resp.GetGatewayVersion())
	assert.Equal(t, int32(4), resp.GetProtocol())
	require.Len(t, resp.GetOpenclawAgents(), 1)
	assert.True(t, resp.GetOpenclawAgents()[0].GetIsDefault())
	require.Len(t, resp.GetOpenclawModels(), 1)
	assert.True(t, resp.GetOpenclawModels()[0].GetAvailable())
	assert.NotContains(t, resp.String(), credTestGatewayToken)
	// 身份种子本机生成、写进本机 state.json,不随应答离开设备。
	seed, ok := f.st.BackendCredential(backendcred.OpenClawIdentityAccount)
	require.True(t, ok)
	assert.NotContains(t, resp.String(), seed)
	f.requireLogsFreeOf(t, credTestGatewayToken, seed)
}

func TestBackendCredential_TestConnection_GivenADraftOpenClawToken_ThenUsesItOnceWithoutStoringIt(t *testing.T) {
	f := newCredentialFixture(t)
	_, err := f.h.SetOpenClawToken(f.ctx, &agentrewire.OpenClawTokenSetRequest{SyncId: credTestSyncID, Token: credTestGatewayToken})
	require.NoError(t, err)

	resp, err := f.h.TestConnection(f.ctx, &agentrewire.BackendConnectionTestRequest{
		BackendType: "openclaw", SyncId: credTestSyncID, OpenclawGatewayUrl: credTestOpenClawURL, OpenclawToken: credTestDraftToken,
	})

	require.NoError(t, err)
	assert.True(t, resp.GetOk())
	assert.Equal(t, credTestDraftToken, f.openclaw.config.Token, "草稿 token 优先于已保存的")
	assert.NotContains(t, f.stateFile(t), credTestDraftToken, "一次性 token 不落盘")
	stored, _ := f.st.BackendCredential(backendcred.OpenClawTokenAccount(credTestSyncID))
	assert.Equal(t, credTestGatewayToken, stored, "已保存的 token 不被草稿覆盖")
	f.requireLogsFreeOf(t, credTestDraftToken)
}

func TestBackendCredential_TestConnection_GivenOpenClawFailures_ThenStructuredCodesWithoutTheToken(t *testing.T) {
	cases := map[string]struct {
		err  error
		want string
	}{
		"auth failed": {&openclawgateway.RPCError{Code: "UNAUTHORIZED", Message: "bad token " + credTestDraftToken}, "AUTH_FAILED"},
		"not paired":  {&openclawgateway.RPCError{Code: "NOT_PAIRED"}, "OPENCLAW_NOT_PAIRED"},
		"cannot connect": {fmt.Errorf("dial %s: connection refused (token %s)", credTestOpenClawURL, credTestDraftToken),
			"OPENCLAW_CONNECTION_FAILED"},
		"agent missing": {fmt.Errorf("%w: main", openclawgateway.ErrSelectedAgentNotFound), "OPENCLAW_AGENT_NOT_FOUND"},
		"timeout":       {context.DeadlineExceeded, "OPENCLAW_PROBE_TIMEOUT"},
	}
	for name, tc := range cases {
		f := newCredentialFixture(t)
		f.openclaw.err = tc.err

		resp, err := f.h.TestConnection(f.ctx, &agentrewire.BackendConnectionTestRequest{
			BackendType: "openclaw", SyncId: credTestSyncID, OpenclawGatewayUrl: credTestOpenClawURL, OpenclawToken: credTestDraftToken,
		})

		require.NoError(t, err, name)
		assert.False(t, resp.GetOk(), name)
		assert.Equal(t, tc.want, resp.GetCode(), name)
		assert.NotContains(t, resp.String(), credTestDraftToken, name)
		f.requireLogsFreeOf(t, credTestDraftToken)
	}
}

func TestBackendCredential_TestConnection_GivenABadOpenClawURL_ThenTheDraftURLCodeWithoutProbing(t *testing.T) {
	f := newCredentialFixture(t)

	resp, err := f.h.TestConnection(f.ctx, &agentrewire.BackendConnectionTestRequest{BackendType: "openclaw", SyncId: credTestSyncID, OpenclawGatewayUrl: "http://gateway.example"})

	require.NoError(t, err)
	assert.False(t, resp.GetOk())
	assert.True(t, strings.HasPrefix(resp.GetCode(), "OPENCLAW_URL_"), resp.GetCode())
	assert.Empty(t, f.openclaw.config.URL, "URL 不合法就不去连")
}

func TestBackendCredential_TestConnection_GivenAnUnknownBackendType_ThenInvalidParams(t *testing.T) {
	f := newCredentialFixture(t)

	_, err := f.h.TestConnection(f.ctx, &agentrewire.BackendConnectionTestRequest{BackendType: "codex"})

	requireInvalidParams(t, err)
}
