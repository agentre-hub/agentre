package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/daemon/state"
)

func TestLoginCompletesDeviceFlowAndPersistsOpaqueAccountState(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)

	// 设备令牌是不透明的:login 从中读不出任何东西,账号标识只能向 server 要(H5)。
	issued := "opaque-device-access-token"
	var polls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/oauth/device/authorize":
			assert.Equal(t, http.MethodPost, r.Method)
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "agentred", body["device_kind"])
			// 账号侧登记的指纹必须就是 auth.pair 交给桌面端做 TOFU 的那一个
			// (identity.DaemonFingerprint(uuid) = "sha256:<hex>"),不是裸 instance uuid:
			// devices.fingerprint 与它「本就是同一个概念」。桌面端按本地配对行里的
			// DaemonFingerprint 向 server 点名中转目标,也按它与账号清单合并设备面板的
			// 一行；登记成另一个值，中转永远解析不到这台 daemon，面板也永远合不上。
			assert.Equal(t, string(identity.DaemonFingerprint(st.InstanceUUID())), body["fingerprint"])
			assert.Equal(t, "linux", body["platform"])
			assert.Equal(t, "dev", body["version"])
			// 主机名是设备列表里唯一有意义的名字来源：设备流不带它，服务端只能
			// 拿指纹缩写当名字，同一个账号下的机器就都长得一样了。
			assert.Equal(t, "devbox", body["name"])
			// 能力概念已从账号侧移除：授权一台设备拿到的就是账号的完整权限，
			// 再自报一份服务端不校验、也不据以限制任何事的清单只是噪声。
			assert.NotContains(t, body, "capabilities")
			_, _ = io.WriteString(w, `{"device_code":"code-1","user_code":"ABCD-EFGH","verification_uri":"https://verify.example/device","verification_uri_complete":"https://verify.example/device?user_code=ABCD-EFGH","interval":1,"expires_in":60}`)
		case "/v1/oauth/device/token":
			polls++
			if polls == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"authorization_pending"}`)
				return
			}
			_, _ = io.WriteString(w, `{"access_token":"`+issued+`","token_type":"Bearer","expires_in":3600,"refresh_token":"refresh-token","refresh_expires_in":7200,"device_id":9}`)
		case "/v1/auth/me":
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "Bearer "+issued, r.Header.Get("Authorization"), "the account is identified with the freshly issued device token")
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{"user_id":42,"device_id":9}}`)
		case "/v1/engine/snapshot":
			assert.Equal(t, http.MethodGet, r.Method)
			assert.Equal(t, "Bearer "+issued, r.Header.Get("Authorization"))
			_, _ = io.WriteString(w, `{"providers":[{"provider_key":"provider-login","name":"Login Provider","type":"anthropic","base_url":"https://api.example","api_key":"login-key","default_model_key":"model-login","models":[{"model_key":"model-login","model_id":"claude-login","name":"Claude Login","enabled":true}]}],"cli_overlays":[{"backend_sync_id":"backend-login","cli_path":"/private/bin/claude"}]}`)
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	var openedURL string
	cmd := newLoginCmdWithDeps(loginDeps{
		dataDir: func() (string, error) { return dir, nil },
		http:    server.Client(),
		openBrowser: func(url string) error {
			openedURL = url
			return nil
		},
		wait:     func(_ time.Duration) error { return nil },
		platform: "linux",
		version:  "dev",
		hostname: func() (string, error) { return "devbox", nil },
	})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--server", server.URL})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "https://verify.example/device?user_code=ABCD-EFGH", openedURL)
	assert.Contains(t, out.String(), "ABCD-EFGH")
	assert.Contains(t, out.String(), "https://verify.example/device?user_code=ABCD-EFGH")
	assert.Contains(t, out.String(), "Logged in")
	assert.Equal(t, 2, polls)

	got, err := state.Load(dir)
	require.NoError(t, err)
	assert.Equal(t, "42", got.AccountID)
	assert.Equal(t, int64(9), got.Credential.DeviceID)
	assert.Equal(t, issued, got.Credential.AccessToken)
	assert.Equal(t, "refresh-token", got.Credential.RefreshToken)
	assert.NotZero(t, got.Credential.AccessTokenExpiresAt)
	assert.NotZero(t, got.Credential.RefreshTokenExpiresAt)
	assert.Equal(t, server.URL, got.AccountServerURL, "successful login must persist the server used by service startup")
	provider, ok := got.LLMProviders["provider-login"]
	require.True(t, ok, "successful login must immediately pull the account engine snapshot")
	assert.Equal(t, "login-key", provider.APIKey)
	assert.Equal(t, "model-login", provider.DefaultModelKey)
	onDisk, readErr := os.ReadFile(filepath.Join(dir, "state.json")) //nolint:gosec // G304: dir is this test's t.TempDir, not untrusted input.
	require.NoError(t, readErr)
	assert.NotContains(t, string(onDisk), "/private/bin/claude", "login must not persist absolute CLI overlays")
}

func TestLogin_GivenEngineSnapshotFailure_WhenLoginSucceeds_ThenKeepsPreviousProvidersAndReportsSuccess(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) {
		s.LLMProviders["provider-old"] = state.LLMProviderMeta{Name: "Old", APIKey: "old-key"}
	})
	require.NoError(t, st.Save())
	issued := "opaque-device-access-token"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := ""
		switch r.URL.Path {
		case "/v1/oauth/device/authorize":
			body = `{"device_code":"code-1","user_code":"ABCD","verification_uri":"https://verify.example/device","verification_uri_complete":"https://verify.example/device?user_code=ABCD","interval":1,"expires_in":60}`
		case "/v1/oauth/device/token":
			body = `{"access_token":"` + issued + `","token_type":"Bearer","expires_in":3600,"refresh_token":"refresh-token","refresh_expires_in":7200,"device_id":9}`
		case "/v1/auth/me":
			body = `{"code":0,"msg":"ok","data":{"user_id":42,"device_id":9}}`
		case "/v1/engine/snapshot":
			status = http.StatusServiceUnavailable
			body = `{"code":503,"msg":"temporarily unavailable"}`
		default:
			t.Fatalf("unexpected request %s", r.URL.Path)
		}
		return &http.Response{
			StatusCode: status, Status: http.StatusText(status), Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: r,
		}, nil
	})}
	cmd := newLoginCmdWithDeps(loginDeps{
		dataDir: func() (string, error) { return dir, nil }, http: client,
		openBrowser: func(string) error { return nil }, wait: func(time.Duration) error { return nil },
		platform: "linux", version: "dev", hostname: func() (string, error) { return "devbox", nil },
	})
	var stderr bytes.Buffer
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"--server", "http://account.example"})

	require.NoError(t, cmd.Execute(), "snapshot refresh is post-login best effort")
	got, loadErr := state.Load(dir)
	require.NoError(t, loadErr)
	assert.True(t, got.IsLoggedIn())
	assert.Contains(t, got.LLMProviders, "provider-old")
	assert.Contains(t, stderr.String(), "will retry")
}

func TestGivenInjectedBuildIdentityWhenLoginAuthorizesThenRegistersSameIdentity(t *testing.T) {
	setAgentredBuildIdentityForTest(t, "v1.2.3", "abcdef1234567890")

	dir := t.TempDir()
	t.Setenv("AGENTRED_DATA_DIR", dir)
	var registeredVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/oauth/device/authorize", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		registeredVersion, _ = body["version"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	defer server.Close()

	cmd := newLoginCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--server", server.URL})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid response")
	assert.Equal(t, "v1.2.3 (abcdef1)", registeredVersion)
	got, loadErr := state.Load(dir)
	require.NoError(t, loadErr)
	assert.Empty(t, got.AccountServerURL, "failed login must not replace persisted runtime configuration")
}

func TestLoginRejectsAlreadyLoggedInDaemonWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Load(dir)
	require.NoError(t, err)
	st.Mutate(func(s *state.State) { s.AccountID = "account-1" })
	require.NoError(t, st.Save())

	var networkCalls int
	cmd := newLoginCmdWithDeps(loginDeps{
		dataDir: func() (string, error) { return dir, nil },
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			networkCalls++
			return nil, assert.AnError
		})},
		openBrowser: func(string) error { return nil },
		wait:        func(time.Duration) error { return nil },
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil)
	err = cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already logged in")
	assert.Equal(t, 0, networkCalls)
}

// state.json 归运行中的 daemon 所有:它内存里存着完整一份,任何一次 Save 都是整文件
// 覆写。login 与 daemon 是两个进程各自 load-modify-save,所以在 daemon 活着的时候登录
// 会被它下一次 Save 静默回滚 —— 服务端签发了凭据、CLI 打印「成功」,而 daemon 手上还是
// 旧的。宁可当场失败,也不要让一次「成功」在几分钟后无声消失。
func TestLoginRefusesWhileDaemonIsRunningSoItCannotBeSilentlyReverted(t *testing.T) {
	dir := t.TempDir()
	var networkCalls int
	cmd := newLoginCmdWithDeps(loginDeps{
		dataDir: func() (string, error) { return dir, nil },
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			networkCalls++
			return nil, assert.AnError
		})},
		openBrowser:   func(string) error { return nil },
		wait:          func(time.Duration) error { return nil },
		daemonRunning: func() bool { return true },
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--server", "http://127.0.0.1:8443"})

	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agentred is running")
	assert.Equal(t, 0, networkCalls, "must not burn a device-flow authorization it cannot persist")

	got, loadErr := state.Load(dir)
	require.NoError(t, loadErr)
	assert.False(t, got.IsLoggedIn(), "a refused login must leave state.json untouched")
}

func TestLoginProceedsWhenNoDaemonHoldsTheStateFile(t *testing.T) {
	dir := t.TempDir()
	var networkCalls int
	cmd := newLoginCmdWithDeps(loginDeps{
		dataDir: func() (string, error) { return dir, nil },
		http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			networkCalls++
			return nil, assert.AnError
		})},
		openBrowser:   func(string) error { return nil },
		wait:          func(time.Duration) error { return nil },
		daemonRunning: func() bool { return false },
	})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--server", "http://127.0.0.1:8443"})

	err := cmd.Execute()
	require.Error(t, err) // 网络被打桩成失败,这里只关心它确实走到了网络那一步
	assert.Positive(t, networkCalls, "with no daemon holding the file, login must run normally")
}

// 设备流已经批准,但 server 认不出这枚令牌属于谁(或答不出账号):账号标识拿不到,
// 这次登录就不能落盘 —— 一台 accountId 为空却带着凭据的 daemon 既不算登录、也不算没登录。
func TestLogin_GivenTheAccountServerCannotIdentifyTheAccount_WhenLoginFinishes_ThenFailsWithoutPersistingALogin(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{name: "the device token is rejected", status: http.StatusUnauthorized, body: `{"code":401,"msg":"unauthorized"}`},
		{name: "the answer carries no account", status: http.StatusOK, body: `{"code":0,"msg":"ok","data":{"user_id":0}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				status := http.StatusOK
				body := ""
				switch r.URL.Path {
				case "/v1/oauth/device/authorize":
					body = `{"device_code":"code-1","user_code":"ABCD","verification_uri":"https://verify.example/device","verification_uri_complete":"https://verify.example/device?user_code=ABCD","interval":1,"expires_in":60}`
				case "/v1/oauth/device/token":
					body = `{"access_token":"opaque-device-access-token","token_type":"Bearer","expires_in":3600,"refresh_token":"refresh-token","refresh_expires_in":7200,"device_id":9}`
				case "/v1/auth/me":
					status, body = tc.status, tc.body
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					status = http.StatusNotFound
				}
				return &http.Response{
					StatusCode: status, Status: http.StatusText(status), Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(body)), Request: r,
				}, nil
			})}
			cmd := newLoginCmdWithDeps(loginDeps{
				dataDir: func() (string, error) { return dir, nil }, http: client,
				openBrowser: func(string) error { return nil }, wait: func(time.Duration) error { return nil },
				platform: "linux", version: "dev", hostname: func() (string, error) { return "devbox", nil },
			})
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs([]string{"--server", "http://account.example"})

			err := cmd.Execute()

			require.Error(t, err)
			assert.Contains(t, err.Error(), "identify account")
			got, loadErr := state.Load(dir)
			require.NoError(t, loadErr)
			assert.False(t, got.IsLoggedIn())
			assert.Equal(t, state.AccountCredential{}, got.Credential, "no credential may be persisted without its account")
		})
	}
}
