package backendcred

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesauth"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// fakeServe is a scripted gated `hermes serve` speaking the native PKCE flow.
type fakeServe struct {
	mu           sync.Mutex
	state        string
	refreshToken string
	refreshCalls int
}

func newFakeServe(t *testing.T) (*fakeServe, string) {
	t.Helper()
	f := &fakeServe{refreshToken: "refresh-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/providers", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"providers":[{"name":"oidc","supports_password":false},{"name":"basic","supports_password":true}]}`))
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
		if body["provider"] != "basic" || body["username"] != "alice" || body["password"] != "correct-horse" {
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
			"access_token": "access-1", "refresh_token": "refresh-1", "provider": "basic", "user_id": 7,
		})
	})
	mux.HandleFunc("/auth/native/refresh", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.refreshCalls++
		if body["refresh_token"] != f.refreshToken || body["provider"] != "basic" {
			http.Error(w, "invalid_grant", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-refreshed", "refresh_token": f.refreshToken, "provider": "basic",
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return f, server.URL
}

func memoryCredentials() (*HermesCredentials, keychain.Keychain) {
	kc := keychain.NewMemory()
	return NewHermesCredentials(func() Store { return kc }), kc
}

func TestHermesAccount_GivenSameServe_ThenOneSlotPerURL(t *testing.T) {
	assert.Equal(t, HermesAccount("http://127.0.0.1:9119"), HermesAccount(" http://127.0.0.1:9119 "))
	assert.NotEqual(t, HermesAccount("http://127.0.0.1:9119"), HermesAccount("http://127.0.0.1:9120"))
	assert.NotContains(t, HermesAccount("http://127.0.0.1:9119"), "127.0.0.1")
}

func TestHermesCredentials_Login_GivenValidPassword_ThenOnlyRefreshTokenIsStored(t *testing.T) {
	_, base := newFakeServe(t)
	creds, kc := memoryCredentials()

	identity, err := creds.Login(context.Background(), HermesLogin{BaseURL: base, Username: "alice", Password: "correct-horse"})

	require.NoError(t, err)
	assert.Equal(t, HermesIdentity{Provider: "basic", UserID: "7"}, *identity, "a blank provider resolves to the only password provider")
	stored, err := kc.Get(HermesAccount(base))
	require.NoError(t, err)
	assert.Equal(t, "refresh-1", stored)
	token, err := creds.AccessToken(context.Background(), base, "basic")
	require.NoError(t, err)
	assert.Equal(t, "access-1", token, "the freshly minted access token is cached")
}

func TestHermesCredentials_Login_GivenWrongPassword_ThenInvalidAndNothingStored(t *testing.T) {
	_, base := newFakeServe(t)
	creds, kc := memoryCredentials()

	_, err := creds.Login(context.Background(), HermesLogin{BaseURL: base, Provider: "basic", Username: "alice", Password: "super-secret-42"})

	require.ErrorIs(t, err, hermesauth.ErrInvalidCredentials)
	assert.NotContains(t, err.Error(), "super-secret-42")
	_, err = kc.Get(HermesAccount(base))
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestHermesCredentials_AccessToken_RefreshesOnceThenCaches(t *testing.T) {
	serve, base := newFakeServe(t)
	creds, kc := memoryCredentials()
	require.NoError(t, kc.Set(HermesAccount(base), "refresh-1"))

	first, err := creds.AccessToken(context.Background(), base, "basic")
	require.NoError(t, err)
	assert.Equal(t, "access-refreshed", first)
	second, err := creds.AccessToken(context.Background(), base+"/", "basic")
	require.NoError(t, err)
	assert.Equal(t, first, second)
	assert.Equal(t, 1, serve.refreshCalls, "the cached access token must not trigger a second refresh")

	creds.Invalidate(base)
	_, err = creds.AccessToken(context.Background(), base, "basic")
	require.NoError(t, err)
	assert.Equal(t, 2, serve.refreshCalls)
}

func TestHermesCredentials_AccessToken_GivenMissingCredential_ThenLoginRequired(t *testing.T) {
	_, base := newFakeServe(t)
	creds, _ := memoryCredentials()

	_, err := creds.AccessToken(context.Background(), base, "basic")

	require.ErrorIs(t, err, hermesauth.ErrLoginRequired)
}

func TestHermesCredentials_AccessToken_GivenRejectedRefresh_ThenLoginExpired(t *testing.T) {
	_, base := newFakeServe(t)
	creds, kc := memoryCredentials()
	require.NoError(t, kc.Set(HermesAccount(base), "stale-token"))

	_, err := creds.AccessToken(context.Background(), base, "basic")

	require.ErrorIs(t, err, hermesauth.ErrLoginExpired)
}

func TestHermesCredentials_AccessToken_GivenNoStore_ThenLoginRequired(t *testing.T) {
	_, base := newFakeServe(t)
	creds := NewHermesCredentials(func() Store { return nil })

	_, err := creds.AccessToken(context.Background(), base, "basic")

	require.ErrorIs(t, err, hermesauth.ErrLoginRequired)
}

func TestHermesCredentials_Logout_DropsStoredAndCachedCredential(t *testing.T) {
	_, base := newFakeServe(t)
	creds, kc := memoryCredentials()
	_, err := creds.Login(context.Background(), HermesLogin{BaseURL: base, Provider: "basic", Username: "alice", Password: "correct-horse"})
	require.NoError(t, err)

	require.NoError(t, creds.Logout(base))
	require.NoError(t, creds.Logout(base), "logging out twice is not an error")

	_, err = kc.Get(HermesAccount(base))
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = creds.AccessToken(context.Background(), base, "basic")
	assert.ErrorIs(t, err, hermesauth.ErrLoginRequired, "the cached access token must not outlive logout")
}

func TestHermesCredentials_GivenInvalidURL_ThenRejectedWithoutTouchingTheStore(t *testing.T) {
	creds, _ := memoryCredentials()

	_, err := creds.Login(context.Background(), HermesLogin{BaseURL: "not a url", Username: "a", Password: "b"})
	require.Error(t, err)
	require.Error(t, creds.Logout("not a url"))
}
