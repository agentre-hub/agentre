package hermes

import (
	"context"
	"net/http"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesauth"
)

// The native auth client lives in the side-effect-free hermesauth package so
// credential code can use it without registering this runtime. The names below
// keep the runtime's existing surface; sentinels are the same values, so
// errors.Is matches across both packages.
var (
	ErrLoginRequired            = hermesauth.ErrLoginRequired
	ErrLoginExpired             = hermesauth.ErrLoginExpired
	ErrPasswordLoginUnsupported = hermesauth.ErrPasswordLoginUnsupported
	ErrInvalidCredentials       = hermesauth.ErrInvalidCredentials
	ErrAuthRateLimited          = hermesauth.ErrAuthRateLimited
	ErrAuthProviderUnavailable  = hermesauth.ErrAuthProviderUnavailable
	ErrAuthUnreachable          = hermesauth.ErrAuthUnreachable
	ErrAccessTokenRejected      = hermesauth.ErrAccessTokenRejected
)

const (
	pkceCookieName    = hermesauth.PKCECookieName
	nativeRedirectURI = hermesauth.NativeRedirectURI
)

type (
	AuthProvider         = hermesauth.AuthProvider
	AuthTokens           = hermesauth.AuthTokens
	PasswordLoginRequest = hermesauth.PasswordLoginRequest
	RefreshAuthRequest   = hermesauth.RefreshAuthRequest
)

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

// ListAuthProviders reads GET /api/auth/providers.
func ListAuthProviders(ctx context.Context, baseURL string, client *http.Client) ([]AuthProvider, error) {
	return hermesauth.ListAuthProviders(ctx, baseURL, client)
}

// PasswordLogin runs the native PKCE password chain; see hermesauth.PasswordLogin.
func PasswordLogin(ctx context.Context, req PasswordLoginRequest) (*AuthTokens, error) {
	return hermesauth.PasswordLogin(ctx, req)
}

// RefreshAuthTokens exchanges a stored refresh token for a new access token.
func RefreshAuthTokens(ctx context.Context, req RefreshAuthRequest) (*AuthTokens, error) {
	return hermesauth.RefreshAuthTokens(ctx, req)
}

// FetchWSTicket mints the single-use WebSocket ticket for a gated serve.
func FetchWSTicket(ctx context.Context, baseURL, accessToken string, client *http.Client) (string, error) {
	return hermesauth.FetchWSTicket(ctx, baseURL, accessToken, client)
}

func randomToken(bytesLen int) (string, error) { return hermesauth.RandomToken(bytesLen) }

func codeChallenge(verifier string) string { return hermesauth.CodeChallenge(verifier) }
