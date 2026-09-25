package agent_backend_svc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// fakeHermesServe is a scripted gated serve used by the service-level auth
// tests. It mirrors the real native PKCE flow.
type fakeHermesServe struct {
	provider     string
	username     string
	password     string
	refreshToken string
	accessToken  string

	mu             sync.Mutex
	state          string
	refreshCalls   int
	authorizeCalls int
}

func newFakeHermesServe() *fakeHermesServe {
	return &fakeHermesServe{
		provider:     "basic",
		username:     "alice",
		password:     "correct-horse",
		refreshToken: "refresh-1",
		accessToken:  "access-1",
	}
}

func (f *fakeHermesServe) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/providers", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"providers":[{"name":"basic","display_name":"Basic","supports_password":true},{"name":"oidc","display_name":"SSO","supports_password":false}]}`))
	})
	mux.HandleFunc("/auth/native/authorize", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.authorizeCalls++
		f.state = r.URL.Query().Get("state")
		f.mu.Unlock()
		if r.URL.Query().Get("code_challenge") == "" || r.URL.Query().Get("code_challenge_method") != "S256" {
			http.Error(w, "bad pkce", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "hermes_session_pkce", Value: "c", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/auth/password-login", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		state := f.state
		f.mu.Unlock()
		if c, err := r.Cookie("hermes_session_pkce"); err != nil || c.Value == "" {
			http.Error(w, "no cookie", http.StatusUnauthorized)
			return
		}
		var body struct {
			Provider string `json:"provider"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Provider != f.provider {
			http.Error(w, "no such provider", http.StatusNotFound)
			return
		}
		if body.Username != f.username || body.Password != f.password {
			http.Error(w, "bad creds", http.StatusUnauthorized)
			return
		}
		next := "http://127.0.0.1:54999/cb?code=code-1&state=" + url.QueryEscape(state)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "next": next})
	})
	mux.HandleFunc("/auth/native/token", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": f.accessToken, "refresh_token": f.refreshToken,
			"provider": f.provider, "user_id": "user-7",
		})
	})
	mux.HandleFunc("/auth/native/refresh", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.refreshCalls++
		f.mu.Unlock()
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["refresh_token"] != f.refreshToken || body["provider"] != f.provider {
			http.Error(w, "invalid_grant", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": f.accessToken + "-refreshed", "refresh_token": f.refreshToken,
			"provider": f.provider, "user_id": "user-7",
		})
	})
	return mux
}

func newServiceWithHermesKeychain(kc keychain.Keychain) *agentBackendSvc {
	return &agentBackendSvc{
		now:    func() int64 { return 1 },
		hermes: backendcred.NewHermesCredentials(func() backendcred.Store { return kc }),
	}
}

func TestListHermesAuthProviders_ReturnsServerDirectory(t *testing.T) {
	server := httptest.NewServer(newFakeHermesServe().handler())
	t.Cleanup(server.Close)
	svc := newServiceWithHermesKeychain(keychain.NewMemory())

	resp, err := svc.ListHermesAuthProviders(context.Background(), &ListHermesAuthProvidersRequest{URL: server.URL})

	require.NoError(t, err)
	require.Len(t, resp.Providers, 2)
	assert.Equal(t, "basic", resp.Providers[0].Name)
	assert.True(t, resp.Providers[0].SupportsPassword)
	assert.False(t, resp.Providers[1].SupportsPassword)
}

func TestLoginHermes_GivenValidCredentials_ThenOnlyRefreshTokenIsStored(t *testing.T) {
	server := httptest.NewServer(newFakeHermesServe().handler())
	t.Cleanup(server.Close)
	kc := keychain.NewMemory()
	svc := newServiceWithHermesKeychain(kc)

	resp, err := svc.LoginHermes(context.Background(), &LoginHermesRequest{
		URL: server.URL, Provider: "basic", Username: "alice", Password: "correct-horse",
	})

	require.NoError(t, err)
	assert.Equal(t, "user-7", resp.UserID)
	assert.Equal(t, "basic", resp.Provider)

	stored, err := kc.Get(backendcred.HermesAccount(server.URL))
	require.NoError(t, err)
	assert.Equal(t, "refresh-1", stored)
	assert.NotEqual(t, "correct-horse", stored)
	// The password must not appear anywhere in the keychain.
	_, err = kc.Get("correct-horse")
	assert.ErrorIs(t, err, keychain.ErrNotFound)
}

func TestLoginHermes_GivenBadCredentials_ThenLocalizedInvalidCredentials(t *testing.T) {
	server := httptest.NewServer(newFakeHermesServe().handler())
	t.Cleanup(server.Close)
	svc := newServiceWithHermesKeychain(keychain.NewMemory())

	_, err := svc.LoginHermes(context.Background(), &LoginHermesRequest{
		URL: server.URL, Provider: "basic", Username: "alice", Password: "wrong",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "用户名或密码错误")
}

func TestLoginHermes_GivenRateLimited_ThenLocalizedRateLimited(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/native/authorize", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "hermes_session_pkce", Value: "c", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/auth/password-login", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	svc := newServiceWithHermesKeychain(keychain.NewMemory())

	_, err := svc.LoginHermes(context.Background(), &LoginHermesRequest{
		URL: server.URL, Provider: "basic", Username: "alice", Password: "x",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "过于频繁")
}

func TestLoginHermes_GivenUnsupportedProvider_ThenReadableReason(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/native/authorize", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "hermes_session_pkce", Value: "c", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/auth/password-login", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "oauth only", http.StatusNotFound)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	svc := newServiceWithHermesKeychain(keychain.NewMemory())

	_, err := svc.LoginHermes(context.Background(), &LoginHermesRequest{
		URL: server.URL, Provider: "oidc", Username: "alice", Password: "x",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "浏览器授权")
}

func TestLogoutHermes_ClearsKeychainAndSavedDisplayFields(t *testing.T) {
	ctx, backendMock, _, _, _, svc := setupSvcTest(t)
	kc := keychain.NewMemory()
	svc.hermes = backendcred.NewHermesCredentials(func() backendcred.Store { return kc })

	row := &agent_backend_entity.AgentBackend{
		ID: 5, Type: string(agent_backend_entity.TypeHermes), Name: "h",
		HermesURL: "http://127.0.0.1:9119", HermesAuthProvider: "basic", HermesUserID: "user-7",
		Status: consts.ACTIVE,
	}
	require.NoError(t, row.MarshalConfig())
	require.NoError(t, kc.Set(backendcred.HermesAccount(row.HermesURL), "refresh-1"))
	backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(row, nil)
	backendMock.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
			assert.Equal(t, "", b.HermesAuthProvider)
			assert.Equal(t, "", b.HermesUserID)
			return nil
		})

	_, err := svc.LogoutHermes(ctx, &LogoutHermesRequest{ID: 5})

	require.NoError(t, err)
	_, err = kc.Get(backendcred.HermesAccount(row.HermesURL))
	assert.ErrorIs(t, err, keychain.ErrNotFound)
}

func TestDeleteHermesBackend_RemovesKeychainCredential(t *testing.T) {
	ctx, backendMock, _, _, _, svc := setupSvcTest(t)
	kc := keychain.NewMemory()
	svc.hermes = backendcred.NewHermesCredentials(func() backendcred.Store { return kc })

	row := &agent_backend_entity.AgentBackend{
		ID: 7, Type: string(agent_backend_entity.TypeHermes), Name: "h",
		HermesURL: "http://127.0.0.1:9119", Status: consts.ACTIVE,
	}
	require.NoError(t, kc.Set(backendcred.HermesAccount(row.HermesURL), "refresh-1"))
	backendMock.EXPECT().Find(gomock.Any(), int64(7)).Return(row, nil)
	backendMock.EXPECT().Delete(gomock.Any(), int64(7)).Return(nil)
	backendMock.EXPECT().List(gomock.Any()).Return(nil, nil)

	_, err := svc.Delete(ctx, &DeleteBackendRequest{ID: 7})

	require.NoError(t, err)
	_, err = kc.Get(backendcred.HermesAccount(row.HermesURL))
	assert.ErrorIs(t, err, keychain.ErrNotFound)
}

func TestDeleteHermesBackend_SameURLRetention(t *testing.T) {
	deleted := func() *agent_backend_entity.AgentBackend {
		return &agent_backend_entity.AgentBackend{
			ID: 7, Type: string(agent_backend_entity.TypeHermes), Name: "h",
			HermesURL: "http://127.0.0.1:9119", Status: consts.ACTIVE,
		}
	}
	hermesRow := func(id int64, rawURL string, device devicefp.Carrier) *agent_backend_entity.AgentBackend {
		return &agent_backend_entity.AgentBackend{
			ID: id, Type: string(agent_backend_entity.TypeHermes), Name: "other",
			HermesURL: rawURL, DeviceFingerprint: device, Status: consts.ACTIVE,
		}
	}
	cases := []struct {
		name      string
		remaining []*agent_backend_entity.AgentBackend
		listErr   error
		kept      bool
	}{
		{
			name:      "Given another local backend points at the same serve when one is deleted then the shared login is kept",
			remaining: []*agent_backend_entity.AgentBackend{hermesRow(8, "ws://127.0.0.1:9119/", "")},
			kept:      true,
		},
		{
			name: "Given the only other same-URL backend is bound to another device when deleted then the login is cleared",
			remaining: []*agent_backend_entity.AgentBackend{
				hermesRow(8, "http://127.0.0.1:9119", "sha256:another-device"),
				hermesRow(9, "http://127.0.0.1:9120", ""),
				{ID: 10, Type: string(agent_backend_entity.TypeOpenClaw), HermesURL: "http://127.0.0.1:9119", Status: consts.ACTIVE},
			},
			kept: false,
		},
		{
			name:      "Given the list still returns the deleted row when deleted then it does not count as another backend",
			remaining: []*agent_backend_entity.AgentBackend{deleted()},
			kept:      false,
		},
		{
			name:    "Given remaining backends cannot be listed when deleted then the delete succeeds and the login is kept",
			listErr: errors.New("database is locked"),
			kept:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, backendMock, _, _, _, svc := setupSvcTest(t)
			kc := keychain.NewMemory()
			svc.hermes = backendcred.NewHermesCredentials(func() backendcred.Store { return kc })
			row := deleted()
			require.NoError(t, kc.Set(backendcred.HermesAccount(row.HermesURL), "refresh-1"))
			backendMock.EXPECT().Find(gomock.Any(), int64(7)).Return(row, nil)
			backendMock.EXPECT().Delete(gomock.Any(), int64(7)).Return(nil)
			backendMock.EXPECT().List(gomock.Any()).Return(tc.remaining, tc.listErr)

			_, err := svc.Delete(ctx, &DeleteBackendRequest{ID: 7})

			require.NoError(t, err)
			stored, getErr := kc.Get(backendcred.HermesAccount(row.HermesURL))
			if tc.kept {
				require.NoError(t, getErr)
				assert.Equal(t, "refresh-1", stored)
			} else {
				assert.ErrorIs(t, getErr, keychain.ErrNotFound)
			}
		})
	}
}

func TestBuildItem_CarriesHermesAuthDisplayFields(t *testing.T) {
	svc := newServiceWithHermesKeychain(keychain.NewMemory())
	row := &agent_backend_entity.AgentBackend{
		ID: 9, Type: string(agent_backend_entity.TypeHermes), Name: "h",
		HermesURL: "http://127.0.0.1:9119", HermesAuthProvider: "basic", HermesUserID: "user-7",
	}
	item := svc.buildItem(row, nil, backendItemLookup{})
	assert.Equal(t, "basic", item.HermesAuthProvider)
	assert.Equal(t, "user-7", item.HermesUserID)
}

// TestBuildItem_CarriesACPCommand：acp 的可执行文件与参数随 BackendItem 读出（agrctl
// get backend 的 config 里要有它们）。
func TestBuildItem_CarriesACPCommand(t *testing.T) {
	svc := newServiceWithHermesKeychain(keychain.NewMemory())
	row := &agent_backend_entity.AgentBackend{
		ID: 9, Type: string(agent_backend_entity.TypeACP), Name: "a",
		ACPCommand: "/usr/local/bin/gemini", ACPArgs: []string{"--acp"},
	}
	item := svc.buildItem(row, nil, backendItemLookup{})
	assert.Equal(t, "/usr/local/bin/gemini", item.ACPCommand)
	assert.Equal(t, []string{"--acp"}, item.ACPArgs)
}

func TestHermesAuthCode_UnreachableIsReadable(t *testing.T) {
	_, frontendCode, ok := hermesAuthCode(hermes.ErrAuthUnreachable)
	require.True(t, ok)
	assert.Equal(t, HermesCodeUnreachable, frontendCode)
	assert.False(t, strings.Contains(strings.ToUpper(frontendCode), "PASSWORD"))
}

func TestTestHermes_GivenLoginRequired_ThenStructuredCode(t *testing.T) {
	ctx, _, _, _, prober, svc := setupSvcTest(t)
	prober.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any()).Return("", hermes.ErrLoginRequired)

	resp, err := svc.Test(ctx, &TestBackendRequest{
		Type: string(agent_backend_entity.TypeHermes), Name: "h",
		HermesURL: "http://127.0.0.1:9119",
	})

	require.NoError(t, err)
	require.False(t, resp.OK)
	assert.Equal(t, HermesCodeLoginRequired, resp.Code)
	assert.NotEmpty(t, resp.Message)
}

func TestTestHermes_GivenLoginExpired_ThenStructuredCode(t *testing.T) {
	ctx, _, _, _, prober, svc := setupSvcTest(t)
	prober.EXPECT().Run(gomock.Any(), gomock.Any(), gomock.Any()).Return("", hermes.ErrLoginExpired)

	resp, err := svc.Test(ctx, &TestBackendRequest{
		Type: string(agent_backend_entity.TypeHermes), Name: "h",
		HermesURL: "http://127.0.0.1:9119",
	})

	require.NoError(t, err)
	require.False(t, resp.OK)
	assert.Equal(t, HermesCodeLoginExpired, resp.Code)
}

func TestListHermesAuthProviders_GivenInvalidURL_ThenInvalidParameter(t *testing.T) {
	svc := newServiceWithHermesKeychain(keychain.NewMemory())
	_, err := svc.ListHermesAuthProviders(context.Background(), &ListHermesAuthProvidersRequest{URL: "not a url"})
	require.Error(t, err)
}
