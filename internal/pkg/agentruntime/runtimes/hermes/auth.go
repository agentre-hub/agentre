package hermes

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The auth flow sentinels. The runtime and the service layer match on these to
// decide between "log in", "log in again", and a transient failure; the UI copy
// is derived from them, never from the server's raw text.
var (
	// ErrLoginRequired means there is no stored credential for this serve yet.
	ErrLoginRequired = errors.New("hermes auth: login required")
	// ErrLoginExpired means the stored refresh token was rejected by the server.
	ErrLoginExpired = errors.New("hermes auth: login expired")
	// ErrPasswordLoginUnsupported means the selected provider needs a browser
	// (OAuth) and cannot be driven from this client.
	ErrPasswordLoginUnsupported = errors.New("hermes auth: provider does not support password login")
	// ErrInvalidCredentials means the server rejected the username/password pair.
	ErrInvalidCredentials = errors.New("hermes auth: invalid username or password")
	// ErrAuthRateLimited means the password-login endpoint throttled the attempt.
	ErrAuthRateLimited = errors.New("hermes auth: too many attempts, try again later")
	// ErrAuthProviderUnavailable means the identity provider is down (HTTP 503).
	ErrAuthProviderUnavailable = errors.New("hermes auth: identity provider unavailable")
	// ErrAuthUnreachable means the auth endpoint could not be reached at all.
	ErrAuthUnreachable = errors.New("hermes auth: server unreachable")
	// ErrAccessTokenRejected means the bearer token was refused by a protected
	// endpoint; the caller should refresh once and retry.
	ErrAccessTokenRejected = errors.New("hermes auth: access token rejected")
)

// pkceCookieName is the cookie Hermes plants at the native authorize step and
// requires on the password-login step.
const pkceCookieName = "hermes_session_pkce"

// nativeRedirectURI only has to look like a loopback callback; Hermes never
// contacts it. The port is arbitrary but explicit.
const nativeRedirectURI = "http://127.0.0.1:54999/cb"

// AuthProvider is one entry of GET /api/auth/providers.
type AuthProvider struct {
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	SupportsPassword bool   `json:"supports_password"`
}

// AuthTokens is a decoded successful login/refresh response.
type AuthTokens struct {
	AccessToken  string
	RefreshToken string
	Provider     string
	UserID       string
	ExpiresAt    time.Time
}

// CredentialSource supplies a bearer access token for a gated `hermes serve`.
//
// The runtime never touches the keychain or the database itself: production
// wires an implementation backed by the credential service, tests inject a
// scripted fake.
type CredentialSource interface {
	// AccessToken returns a valid bearer token for baseURL. provider is the
	// optional persisted provider name. It returns ErrLoginRequired when no
	// credential is stored and ErrLoginExpired when the stored refresh token
	// has been rejected.
	AccessToken(ctx context.Context, baseURL, provider string) (string, error)
	// Invalidate drops the cached access token for baseURL so the next
	// AccessToken refreshes it.
	Invalidate(baseURL string)
}

var defaultCredentialSource CredentialSource

// SetDefaultCredentialSource is the bootstrap seam that lets the registered
// Hermes runtime reach the credential store without importing the service.
func SetDefaultCredentialSource(c CredentialSource) { defaultCredentialSource = c }

// DefaultCredentialSource returns the process-wide credential source (may be nil
// in isolated unit tests).
func DefaultCredentialSource() CredentialSource { return defaultCredentialSource }

// PasswordLoginRequest is the input of the native password flow.
type PasswordLoginRequest struct {
	BaseURL    string
	Provider   string
	Username   string
	Password   string
	HTTPClient *http.Client
}

// RefreshAuthRequest exchanges a refresh token for a fresh access token.
type RefreshAuthRequest struct {
	BaseURL      string
	Provider     string
	RefreshToken string
	HTTPClient   *http.Client
}

func authBaseURL(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

func authClient(client *http.Client) *http.Client {
	if client != nil {
		return client
	}
	return &http.Client{Timeout: gatewayHTTPTimeout}
}

// codeChallenge derives the S256 challenge for a verifier.
func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomToken(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ListAuthProviders reads GET /api/auth/providers.
func ListAuthProviders(ctx context.Context, baseURL string, client *http.Client) ([]AuthProvider, error) {
	var env struct {
		Providers []AuthProvider `json:"providers"`
	}
	if err := authRequest(ctx, authClient(client), http.MethodGet, authBaseURL(baseURL)+"/api/auth/providers", nil, nil, &env); err != nil {
		return nil, err
	}
	return env.Providers, nil
}

// PasswordLogin runs the full native PKCE chain: authorize (planting the PKCE
// cookie) → password-login (reading the code out of `next`) → token exchange.
// It never opens a browser and never listens on a loopback port.
func PasswordLogin(ctx context.Context, req PasswordLoginRequest) (*AuthTokens, error) {
	base := authBaseURL(req.BaseURL)
	client := authClient(req.HTTPClient)

	verifier, err := randomToken(32)
	if err != nil {
		return nil, fmt.Errorf("hermes auth: generate code verifier: %w", err)
	}
	state, err := randomToken(24)
	if err != nil {
		return nil, fmt.Errorf("hermes auth: generate state: %w", err)
	}

	cookie, err := beginNativeAuthorize(ctx, client, base, req.Provider, codeChallenge(verifier), state)
	if err != nil {
		return nil, err
	}

	code, err := passwordLoginCode(ctx, client, base, cookie, req.Provider, req.Username, req.Password, state)
	if err != nil {
		return nil, err
	}

	var tokens tokenResponse
	err = authRequest(ctx, client, http.MethodPost, base+"/auth/native/token", nil, map[string]string{
		"code":          code,
		"code_verifier": verifier,
	}, &tokens)
	if err != nil {
		return nil, err
	}
	return tokens.authTokens(), nil
}

// RefreshAuthTokens exchanges a stored refresh token for a new access token.
// A rejected token is normalized to ErrLoginExpired.
func RefreshAuthTokens(ctx context.Context, req RefreshAuthRequest) (*AuthTokens, error) {
	base := authBaseURL(req.BaseURL)
	var tokens tokenResponse
	err := authRequest(ctx, authClient(req.HTTPClient), http.MethodPost, base+"/auth/native/refresh",
		nil,
		map[string]string{"refresh_token": req.RefreshToken, "provider": req.Provider},
		&tokens)
	if err != nil {
		if errors.Is(err, ErrAccessTokenRejected) {
			return nil, fmt.Errorf("%w: refresh token rejected", ErrLoginExpired)
		}
		return nil, err
	}
	return tokens.authTokens(), nil
}

// FetchWSTicket mints the single-use WebSocket ticket for a gated serve. A
// rejected bearer token maps to ErrAccessTokenRejected so the caller can
// refresh once before giving up.
func FetchWSTicket(ctx context.Context, baseURL, accessToken string, client *http.Client) (string, error) {
	var env struct {
		Ticket     string `json:"ticket"`
		TTLSeconds int    `json:"ttl_seconds"`
	}
	headers := map[string]string{"Authorization": "Bearer " + accessToken}
	if err := authRequest(ctx, authClient(client), http.MethodPost, authBaseURL(baseURL)+"/api/auth/ws-ticket", headers, nil, &env); err != nil {
		return "", err
	}
	if strings.TrimSpace(env.Ticket) == "" {
		return "", fmt.Errorf("%w: ws-ticket response carried no ticket", ErrGatewayProtocol)
	}
	return env.Ticket, nil
}

// beginNativeAuthorize performs the PKCE start and returns the PKCE cookie the
// password-login step must echo. Redirects are not followed: the cookie is on
// the redirect response itself.
func beginNativeAuthorize(ctx context.Context, client *http.Client, base, provider, challenge, state string) (*http.Cookie, error) {
	query := url.Values{
		"provider":              {provider},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
		"redirect_uri":          {nativeRedirectURI},
		"state":                 {state},
	}
	authorizeURL := base + "/auth/native/authorize?" + query.Encode()

	noRedirect := *client
	noRedirect.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthUnreachable, err)
	}
	resp, err := noRedirect.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: native authorize returned %s", ErrGatewayProtocol, resp.Status)
	}
	for _, c := range resp.Cookies() {
		if c.Name == pkceCookieName {
			return c, nil
		}
	}
	return nil, fmt.Errorf("%w: native authorize did not set the PKCE cookie", ErrGatewayProtocol)
}

// passwordLoginCode posts the credentials and extracts the one-time code from
// the JSON `next` URL, after checking the echoed state.
func passwordLoginCode(
	ctx context.Context, client *http.Client, base string, cookie *http.Cookie,
	provider, username, password, state string,
) (string, error) {
	body, err := json.Marshal(map[string]string{
		"provider": provider,
		"username": username,
		"password": password,
	})
	if err != nil {
		return "", err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/auth/password-login", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuthUnreachable, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		httpReq.AddCookie(cookie)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAuthUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", ErrPasswordLoginUnsupported
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", ErrInvalidCredentials
	case http.StatusTooManyRequests:
		return "", ErrAuthRateLimited
	case http.StatusServiceUnavailable:
		return "", ErrAuthProviderUnavailable
	default:
		return "", fmt.Errorf("%w: password-login returned %s", ErrGatewayProtocol, resp.Status)
	}

	var env struct {
		OK   bool   `json:"ok"`
		Next string `json:"next"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("%w: decode password-login response: %v", ErrGatewayProtocol, err)
	}
	if !env.OK {
		return "", fmt.Errorf("%w: password-login did not report ok", ErrGatewayProtocol)
	}
	next, err := url.Parse(strings.TrimSpace(env.Next))
	if err != nil || next.Host == "" {
		return "", fmt.Errorf("%w: password-login returned an invalid next URL", ErrGatewayProtocol)
	}
	if got := next.Query().Get("state"); got != state {
		// Never echo the server's forged state or the credentials.
		return "", fmt.Errorf("%w: password-login state mismatch", ErrGatewayProtocol)
	}
	code := next.Query().Get("code")
	if code == "" {
		return "", fmt.Errorf("%w: password-login returned no code", ErrGatewayProtocol)
	}
	return code, nil
}

// tokenResponse is the wire shape of /auth/native/token and /auth/native/refresh.
// user_id and expires_at are decoded permissively: Hermes has shipped both a
// string and a number for user_id.
type tokenResponse struct {
	AccessToken  string          `json:"access_token"`
	RefreshToken string          `json:"refresh_token"`
	ExpiresAt    json.RawMessage `json:"expires_at"`
	Provider     string          `json:"provider"`
	UserID       json.RawMessage `json:"user_id"`
	TokenType    string          `json:"token_type"`
}

func (t tokenResponse) authTokens() *AuthTokens {
	return &AuthTokens{
		AccessToken:  strings.TrimSpace(t.AccessToken),
		RefreshToken: strings.TrimSpace(t.RefreshToken),
		Provider:     strings.TrimSpace(t.Provider),
		UserID:       jsonScalarString(t.UserID),
		ExpiresAt:    jsonUnixTime(t.ExpiresAt),
	}
}

func jsonScalarString(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return ""
}

func jsonUnixTime(raw json.RawMessage) time.Time {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		if n <= 0 {
			return time.Time{}
		}
		return time.Unix(n, 0)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if v, convErr := strconv.ParseInt(strings.TrimSpace(s), 10, 64); convErr == nil && v > 0 {
			return time.Unix(v, 0)
		}
	}
	return time.Time{}
}

// authRequest is the shared JSON request helper: it classifies transport
// failures, 401/403 (as ErrAccessTokenRejected), and 429/503 so callers can map
// them to user-facing reasons.
func authRequest(ctx context.Context, client *http.Client, method, rawURL string, headers map[string]string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuthUnreachable, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrAuthUnreachable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrAccessTokenRejected
	case http.StatusTooManyRequests:
		return ErrAuthRateLimited
	case http.StatusServiceUnavailable:
		return ErrAuthProviderUnavailable
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%w: %s returned %s", ErrGatewayProtocol, rawURL, resp.Status)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: decode %s: %v", ErrGatewayProtocol, rawURL, err)
	}
	return nil
}
