package hermes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatedServe is a scripted stand-in for a gated `hermes serve`: it speaks the
// real native PKCE flow closely enough that a client bug shows up as a test
// failure (wrong challenge method, missing cookie, skipped state check, ...).
type gatedServe struct {
	provider     string
	username     string
	password     string
	refreshToken string
	accessToken  string
	ticket       string

	mu             sync.Mutex
	state          string
	authorizeSeen  int
	loginSeen      int
	tokenSeen      int
	refreshSeen    int
	ticketSeen     int
	cookieRejected bool
}

func newGatedServe() *gatedServe {
	return &gatedServe{
		provider:     "basic",
		username:     "alice",
		password:     "secret",
		refreshToken: "refresh-1",
		accessToken:  "access-1",
		ticket:       "ticket-1",
	}
}

func (g *gatedServe) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/auth/providers", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"providers":[{"name":"basic","display_name":"Basic","supports_password":true},{"name":"github","display_name":"GitHub","supports_password":false}]}`))
	})

	mux.HandleFunc("/auth/native/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		g.mu.Lock()
		g.authorizeSeen++
		g.state = q.Get("state")
		g.mu.Unlock()
		if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "bad pkce", http.StatusBadRequest)
			return
		}
		if q.Get("redirect_uri") != nativeRedirectURI {
			http.Error(w, "bad redirect", http.StatusBadRequest)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: pkceCookieName, Value: "pkce-cookie-1", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/login", http.StatusFound)
	})

	mux.HandleFunc("/auth/password-login", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.loginSeen++
		state := g.state
		g.mu.Unlock()
		if cookie, err := r.Cookie(pkceCookieName); err != nil || cookie.Value == "" {
			g.mu.Lock()
			g.cookieRejected = true
			g.mu.Unlock()
			http.Error(w, "missing pkce cookie", http.StatusUnauthorized)
			return
		}
		var body struct {
			Provider string `json:"provider"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		switch {
		case body.Provider != g.provider:
			http.Error(w, "unknown provider", http.StatusNotFound)
			return
		case body.Username != g.username || body.Password != g.password:
			http.Error(w, "bad creds", http.StatusUnauthorized)
			return
		}
		next := "http://127.0.0.1:54999/cb?code=code-1&state=" + url.QueryEscape(state)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "next": next})
	})

	mux.HandleFunc("/auth/native/token", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.tokenSeen++
		g.mu.Unlock()
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["code"] == "" || body["code_verifier"] == "" {
			http.Error(w, "bad code", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  g.accessToken,
			"refresh_token": g.refreshToken,
			"expires_at":    1700000000,
			"provider":      g.provider,
			"user_id":       "user-7",
			"token_type":    "Bearer",
		})
	})

	mux.HandleFunc("/auth/native/refresh", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.refreshSeen++
		g.mu.Unlock()
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["refresh_token"] != g.refreshToken || body["provider"] != g.provider {
			http.Error(w, "invalid_grant", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  g.accessToken + "-refreshed",
			"refresh_token": g.refreshToken,
			"expires_at":    1700003600,
			"provider":      g.provider,
			"user_id":       "user-7",
			"token_type":    "Bearer",
		})
	})

	mux.HandleFunc("/api/auth/ws-ticket", func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.ticketSeen++
		g.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer "+g.accessToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ticket": g.ticket, "ttl_seconds": 30})
	})

	return mux
}

func TestPKCEChallengeIsS256Base64URL(t *testing.T) {
	verifier, err := randomToken(32)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(verifier), 43)

	challenge := codeChallenge(verifier)
	require.NotEmpty(t, challenge)
	assert.NotContains(t, challenge, "=", "base64url must be unpadded")
	assert.NotContains(t, challenge, "+")
	assert.NotContains(t, challenge, "/")

	decoded, err := base64.RawURLEncoding.DecodeString(challenge)
	require.NoError(t, err)
	require.Len(t, decoded, 32)
}

func TestListAuthProviders(t *testing.T) {
	server := httptest.NewServer(newGatedServe().handler())
	t.Cleanup(server.Close)

	providers, err := ListAuthProviders(context.Background(), server.URL, nil)

	require.NoError(t, err)
	require.Len(t, providers, 2)
	assert.Equal(t, "basic", providers[0].Name)
	assert.True(t, providers[0].SupportsPassword)
	assert.False(t, providers[1].SupportsPassword)
}

func TestPasswordLogin_GivenValidCredentials_ThenTokensAndPkceCookieSent(t *testing.T) {
	g := newGatedServe()
	server := httptest.NewServer(g.handler())
	t.Cleanup(server.Close)

	tokens, err := PasswordLogin(context.Background(), PasswordLoginRequest{
		BaseURL: server.URL, Provider: g.provider,
		Username: g.username, Password: g.password,
	})

	require.NoError(t, err)
	assert.Equal(t, "access-1", tokens.AccessToken)
	assert.Equal(t, "refresh-1", tokens.RefreshToken)
	assert.Equal(t, "user-7", tokens.UserID)
	assert.Equal(t, "basic", tokens.Provider)
	assert.False(t, g.cookieRejected, "password-login must carry the PKCE cookie")
	assert.EqualValues(t, 1, g.authorizeSeen)
	assert.EqualValues(t, 1, g.loginSeen)
	assert.EqualValues(t, 1, g.tokenSeen)
}

func TestPasswordLogin_GivenBadCredentials_ThenInvalidCredentials(t *testing.T) {
	g := newGatedServe()
	server := httptest.NewServer(g.handler())
	t.Cleanup(server.Close)

	_, err := PasswordLogin(context.Background(), PasswordLoginRequest{
		BaseURL: server.URL, Provider: g.provider,
		Username: g.username, Password: "wrong",
	})

	require.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestPasswordLogin_GivenRateLimited_ThenRateLimited(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/native/authorize", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: pkceCookieName, Value: "c", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/auth/password-login", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "slow down", http.StatusTooManyRequests)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, err := PasswordLogin(context.Background(), PasswordLoginRequest{
		BaseURL: server.URL, Provider: "basic", Username: "a", Password: "b",
	})

	require.ErrorIs(t, err, ErrAuthRateLimited)
}

func TestPasswordLogin_GivenUnsupportedProvider_ThenUnsupported(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/native/authorize", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: pkceCookieName, Value: "c", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/auth/password-login", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no password", http.StatusNotFound)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, err := PasswordLogin(context.Background(), PasswordLoginRequest{
		BaseURL: server.URL, Provider: "github", Username: "a", Password: "b",
	})

	require.ErrorIs(t, err, ErrPasswordLoginUnsupported)
}

func TestPasswordLogin_GivenAuthorizeRejectsPKCE_ThenProtocolError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/native/authorize", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "bad redirect", http.StatusBadRequest)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, err := PasswordLogin(context.Background(), PasswordLoginRequest{
		BaseURL: server.URL, Provider: "basic", Username: "a", Password: "b",
	})

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalidCredentials)
}

func TestPasswordLogin_GivenStateMismatch_ThenRejected(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/native/authorize", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: pkceCookieName, Value: "c", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(http.StatusFound)
	})
	mux.HandleFunc("/auth/password-login", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"next":"http://127.0.0.1:54999/cb?code=code-1&state=forged"}`))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, err := PasswordLogin(context.Background(), PasswordLoginRequest{
		BaseURL: server.URL, Provider: "basic", Username: "a", Password: "b",
	})

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrInvalidCredentials)
}

func TestRefreshAuthTokens_GivenValidToken_ThenNewAccess(t *testing.T) {
	g := newGatedServe()
	server := httptest.NewServer(g.handler())
	t.Cleanup(server.Close)

	tokens, err := RefreshAuthTokens(context.Background(), RefreshAuthRequest{
		BaseURL: server.URL, Provider: g.provider, RefreshToken: g.refreshToken,
	})

	require.NoError(t, err)
	assert.Equal(t, "access-1-refreshed", tokens.AccessToken)
	assert.Equal(t, "refresh-1", tokens.RefreshToken)
	assert.EqualValues(t, 1, g.refreshSeen)
}

func TestRefreshAuthTokens_GivenRejectedToken_ThenLoginExpired(t *testing.T) {
	g := newGatedServe()
	server := httptest.NewServer(g.handler())
	t.Cleanup(server.Close)

	_, err := RefreshAuthTokens(context.Background(), RefreshAuthRequest{
		BaseURL: server.URL, Provider: g.provider, RefreshToken: "stale",
	})

	require.ErrorIs(t, err, ErrLoginExpired)
}

func TestFetchWSTicket_GivenValidBearer_ThenTicket(t *testing.T) {
	g := newGatedServe()
	server := httptest.NewServer(g.handler())
	t.Cleanup(server.Close)

	ticket, err := FetchWSTicket(context.Background(), server.URL, g.accessToken, nil)

	require.NoError(t, err)
	assert.Equal(t, "ticket-1", ticket)
}

func TestFetchWSTicket_GivenRejectedBearer_ThenAccessRejected(t *testing.T) {
	g := newGatedServe()
	server := httptest.NewServer(g.handler())
	t.Cleanup(server.Close)

	_, err := FetchWSTicket(context.Background(), server.URL, "stale-access", nil)

	require.ErrorIs(t, err, ErrAccessTokenRejected)
}

func TestAuthErrorsNeverLeakPassword(t *testing.T) {
	g := newGatedServe()
	server := httptest.NewServer(g.handler())
	t.Cleanup(server.Close)

	_, err := PasswordLogin(context.Background(), PasswordLoginRequest{
		BaseURL: server.URL, Provider: g.provider,
		Username: g.username, Password: "super-secret-42",
	})

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "super-secret-42")
}
