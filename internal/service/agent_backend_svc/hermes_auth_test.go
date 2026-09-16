package agent_backend_svc

import (
	"context"
	"encoding/json"
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
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
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
		hermes: newHermesCredentialStore(func() keychain.Keychain { return kc }),
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

	stored, err := kc.Get(hermesKeychainAccount(server.URL))
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

func TestHermesCredentialStore_AccessTokenRefreshesOnceThenCaches(t *testing.T) {
	serve := newFakeHermesServe()
	server := httptest.NewServer(serve.handler())
	t.Cleanup(server.Close)
	kc := keychain.NewMemory()
	require.NoError(t, kc.Set(hermesKeychainAccount(server.URL), "refresh-1"))
	store := newHermesCredentialStore(func() keychain.Keychain { return kc })

	first, err := store.AccessToken(context.Background(), server.URL, "basic")
	require.NoError(t, err)
	assert.Equal(t, "access-1-refreshed", first)

	second, err := store.AccessToken(context.Background(), server.URL, "basic")
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.EqualValues(t, 1, serve.refreshCalls, "the cached access token must not trigger a second refresh")

	store.Invalidate(server.URL)
	third, err := store.AccessToken(context.Background(), server.URL, "basic")
	require.NoError(t, err)
	assert.Equal(t, first, third)
	assert.EqualValues(t, 2, serve.refreshCalls)
}

func TestHermesCredentialStore_GivenMissingCredential_ThenLoginRequired(t *testing.T) {
	server := httptest.NewServer(newFakeHermesServe().handler())
	t.Cleanup(server.Close)
	store := newHermesCredentialStore(func() keychain.Keychain { return keychain.NewMemory() })

	_, err := store.AccessToken(context.Background(), server.URL, "basic")

	require.ErrorIs(t, err, hermes.ErrLoginRequired)
}

func TestHermesCredentialStore_GivenRejectedRefresh_ThenLoginExpired(t *testing.T) {
	server := httptest.NewServer(newFakeHermesServe().handler())
	t.Cleanup(server.Close)
	kc := keychain.NewMemory()
	require.NoError(t, kc.Set(hermesKeychainAccount(server.URL), "stale-token"))
	store := newHermesCredentialStore(func() keychain.Keychain { return kc })

	_, err := store.AccessToken(context.Background(), server.URL, "basic")

	require.ErrorIs(t, err, hermes.ErrLoginExpired)
}

func TestLogoutHermes_ClearsKeychainAndSavedDisplayFields(t *testing.T) {
	ctx, backendMock, _, _, _, svc := setupSvcTest(t)
	kc := keychain.NewMemory()
	svc.hermes = newHermesCredentialStore(func() keychain.Keychain { return kc })

	row := &agent_backend_entity.AgentBackend{
		ID: 5, Type: string(agent_backend_entity.TypeHermes), Name: "h",
		HermesURL: "http://127.0.0.1:9119", HermesAuthProvider: "basic", HermesUserID: "user-7",
		Status: consts.ACTIVE,
	}
	require.NoError(t, row.MarshalConfig())
	require.NoError(t, kc.Set(hermesKeychainAccount(row.HermesURL), "refresh-1"))
	backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(row, nil)
	backendMock.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
		DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
			assert.Equal(t, "", b.HermesAuthProvider)
			assert.Equal(t, "", b.HermesUserID)
			return nil
		})

	_, err := svc.LogoutHermes(ctx, &LogoutHermesRequest{ID: 5})

	require.NoError(t, err)
	_, err = kc.Get(hermesKeychainAccount(row.HermesURL))
	assert.ErrorIs(t, err, keychain.ErrNotFound)
}

func TestDeleteHermesBackend_RemovesKeychainCredential(t *testing.T) {
	ctx, backendMock, _, _, _, svc := setupSvcTest(t)
	kc := keychain.NewMemory()
	svc.hermes = newHermesCredentialStore(func() keychain.Keychain { return kc })

	row := &agent_backend_entity.AgentBackend{
		ID: 7, Type: string(agent_backend_entity.TypeHermes), Name: "h",
		HermesURL: "http://127.0.0.1:9119", Status: consts.ACTIVE,
	}
	require.NoError(t, kc.Set(hermesKeychainAccount(row.HermesURL), "refresh-1"))
	backendMock.EXPECT().Find(gomock.Any(), int64(7)).Return(row, nil)
	backendMock.EXPECT().Delete(gomock.Any(), int64(7)).Return(nil)

	_, err := svc.Delete(ctx, &DeleteBackendRequest{ID: 7})

	require.NoError(t, err)
	_, err = kc.Get(hermesKeychainAccount(row.HermesURL))
	assert.ErrorIs(t, err, keychain.ErrNotFound)
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
