package agent_backend_svc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// hermesRefreshAccountPrefix namespaces the URL-derived keychain slots. Only the
// refresh token is stored there; the password is never persisted.
const hermesRefreshAccountPrefix = "agentre-hermes-refresh-"

// hermesAccessRefreshSkew refreshes a cached access token slightly before it
// actually expires so a dial never races the expiry.
const hermesAccessRefreshSkew = 30 * time.Second

// hermesKeychainAccount derives the keychain slot for a normalized serve URL:
// every backend pointing at the same serve (and therefore the same identity)
// shares one credential, and login works before the backend is saved.
func hermesKeychainAccount(normalizedURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(normalizedURL)))
	return hermesRefreshAccountPrefix + hex.EncodeToString(sum[:16])
}

type cachedHermesAccess struct {
	token     string
	expiresAt time.Time
}

// hermesCredentialStore is the process-wide bridge between the login flow and
// the Hermes runtime: login writes the refresh token to the keychain and caches
// the access token, the runtime asks for a bearer without ever touching the
// keychain itself, and a rejected access token forces exactly one refresh.
type hermesCredentialStore struct {
	mu    sync.Mutex
	cache map[string]cachedHermesAccess
	kc    func() keychain.Keychain
}

func newHermesCredentialStore(kc func() keychain.Keychain) *hermesCredentialStore {
	return &hermesCredentialStore{
		cache: map[string]cachedHermesAccess{},
		kc:    kc,
	}
}

func (c *hermesCredentialStore) keychain() keychain.Keychain {
	if c.kc != nil {
		return c.kc()
	}
	return keychain.Default()
}

// StoreLogin persists the refresh token and caches the freshly minted access
// token. normalizedURL must already be normalized.
func (c *hermesCredentialStore) StoreLogin(normalizedURL string, tokens *hermes.AuthTokens) error {
	if tokens == nil || strings.TrimSpace(tokens.RefreshToken) == "" {
		return errors.New("hermes credential store: login returned no refresh token")
	}
	if err := c.keychain().Set(hermesKeychainAccount(normalizedURL), tokens.RefreshToken); err != nil {
		return err
	}
	c.remember(normalizedURL, tokens.AccessToken, tokens.ExpiresAt)
	return nil
}

// Logout drops both the keychain entry and the cached access token.
func (c *hermesCredentialStore) Logout(normalizedURL string) error {
	c.mu.Lock()
	delete(c.cache, normalizedURL)
	c.mu.Unlock()
	err := c.keychain().Delete(hermesKeychainAccount(normalizedURL))
	if errors.Is(err, keychain.ErrNotFound) {
		return nil
	}
	return err
}

// AccessToken implements hermes.CredentialSource.
func (c *hermesCredentialStore) AccessToken(ctx context.Context, baseURL, provider string) (string, error) {
	base, err := agent_backend_entity.NormalizeHermesURL(baseURL)
	if err != nil {
		return "", err
	}
	if token, ok := c.cached(base); ok {
		return token, nil
	}
	refresh, err := c.keychain().Get(hermesKeychainAccount(base))
	if errors.Is(err, keychain.ErrNotFound) {
		return "", hermes.ErrLoginRequired
	}
	if err != nil {
		return "", fmt.Errorf("%w: keychain: %v", hermes.ErrLoginRequired, err)
	}
	if strings.TrimSpace(refresh) == "" {
		return "", hermes.ErrLoginRequired
	}
	resolvedProvider, err := c.resolveProvider(ctx, base, provider)
	if err != nil {
		return "", err
	}
	tokens, err := hermes.RefreshAuthTokens(ctx, hermes.RefreshAuthRequest{
		BaseURL:      base,
		Provider:     resolvedProvider,
		RefreshToken: refresh,
	})
	if err != nil {
		return "", err
	}
	// basic providers are long-lived, but a rotating provider would change the
	// refresh token here; persist the new one so the next refresh sees it.
	if next := strings.TrimSpace(tokens.RefreshToken); next != "" && next != refresh {
		if setErr := c.keychain().Set(hermesKeychainAccount(base), next); setErr != nil {
			return "", setErr
		}
	}
	c.remember(base, tokens.AccessToken, tokens.ExpiresAt)
	return tokens.AccessToken, nil
}

// Invalidate implements hermes.CredentialSource: the next access request must
// refresh instead of trusting the cache.
func (c *hermesCredentialStore) Invalidate(baseURL string) {
	base, err := agent_backend_entity.NormalizeHermesURL(baseURL)
	if err != nil {
		return
	}
	c.mu.Lock()
	delete(c.cache, base)
	c.mu.Unlock()
}

func (c *hermesCredentialStore) cached(base string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[base]
	if !ok || strings.TrimSpace(entry.token) == "" {
		return "", false
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt.Add(-hermesAccessRefreshSkew)) {
		return "", false
	}
	return entry.token, true
}

func (c *hermesCredentialStore) remember(base, token string, expiresAt time.Time) {
	c.mu.Lock()
	c.cache[base] = cachedHermesAccess{token: token, expiresAt: expiresAt}
	c.mu.Unlock()
}

// resolveProvider picks the provider used for refresh. An explicit hint wins;
// otherwise the only password-capable provider is chosen. An empty result means
// the serve only offers browser-based providers.
func (c *hermesCredentialStore) resolveProvider(ctx context.Context, base, hint string) (string, error) {
	if p := strings.TrimSpace(hint); p != "" {
		return p, nil
	}
	providers, err := hermes.ListAuthProviders(ctx, base, nil)
	if err != nil {
		return "", err
	}
	for _, p := range providers {
		if p.SupportsPassword {
			return p.Name, nil
		}
	}
	return "", hermes.ErrPasswordLoginUnsupported
}

// defaultHermesCredentials is the singleton the registered runtime reaches
// through hermes.DefaultCredentialSource. It resolves the keychain lazily so
// bootstrap's isolated-keychain swap is honored.
var defaultHermesCredentials = newHermesCredentialStore(func() keychain.Keychain { return keychain.Default() })

func init() {
	hermes.SetDefaultCredentialSource(defaultHermesCredentials)
}

func (s *agentBackendSvc) credentialStore() *hermesCredentialStore {
	if s != nil && s.hermes != nil {
		return s.hermes
	}
	return defaultHermesCredentials
}

// Hermes test-connection codes. The frontend localizes these so a gated serve
// reports "login required" / "login expired" instead of a generic failure.
const (
	HermesCodeLoginRequired       = "HERMES_LOGIN_REQUIRED"
	HermesCodeLoginExpired        = "HERMES_LOGIN_EXPIRED"
	HermesCodeInvalidCredentials  = "HERMES_INVALID_CREDENTIALS"
	HermesCodeRateLimited         = "HERMES_RATE_LIMITED"
	HermesCodeProviderUnsupported = "HERMES_PROVIDER_UNSUPPORTED"
	HermesCodeProviderUnavailable = "HERMES_PROVIDER_UNAVAILABLE"
	HermesCodeProviderNotFound    = "HERMES_PROVIDER_NOT_FOUND"
	HermesCodeUnreachable         = "HERMES_UNREACHABLE"
)

// hermesAuthCode maps the auth-layer sentinels to (business code, frontend code).
func hermesAuthCode(err error) (int, string, bool) {
	switch {
	case err == nil:
		return 0, "", false
	case errors.Is(err, hermes.ErrLoginRequired):
		return code.HermesLoginRequired, HermesCodeLoginRequired, true
	case errors.Is(err, hermes.ErrLoginExpired):
		return code.HermesLoginExpired, HermesCodeLoginExpired, true
	case errors.Is(err, hermes.ErrInvalidCredentials):
		return code.HermesLoginRejected, HermesCodeInvalidCredentials, true
	case errors.Is(err, hermes.ErrAuthRateLimited):
		return code.HermesRateLimited, HermesCodeRateLimited, true
	case errors.Is(err, hermes.ErrPasswordLoginUnsupported):
		return code.HermesProviderUnsupported, HermesCodeProviderUnsupported, true
	case errors.Is(err, hermes.ErrAuthProviderUnavailable):
		return code.HermesProviderUnavailable, HermesCodeProviderUnavailable, true
	case errors.Is(err, hermes.ErrAuthUnreachable):
		return code.HermesUnreachable, HermesCodeUnreachable, true
	default:
		return 0, "", false
	}
}

// hermesAuthError turns an auth failure into the localized business error the
// Wails layer can prefix with its business code. It deliberately drops the raw
// error: only the mapped reason is user-facing, never the server's text.
func hermesAuthError(ctx context.Context, err error) error {
	if bizCode, _, ok := hermesAuthCode(err); ok {
		return i18n.NewError(ctx, bizCode)
	}
	return err
}

// ListHermesAuthProviders reads the serve's provider directory so the editor
// never hard-codes "basic".
func (s *agentBackendSvc) ListHermesAuthProviders(ctx context.Context, req *ListHermesAuthProvidersRequest) (*ListHermesAuthProvidersResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	base, err := agent_backend_entity.NormalizeHermesURL(req.URL)
	if err != nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	providers, err := hermes.ListAuthProviders(ctx, base, nil)
	if err != nil {
		return nil, hermesAuthError(ctx, err)
	}
	items := make([]HermesAuthProviderItem, 0, len(providers))
	for _, p := range providers {
		items = append(items, HermesAuthProviderItem{
			Name:             p.Name,
			DisplayName:      p.DisplayName,
			SupportsPassword: p.SupportsPassword,
		})
	}
	return &ListHermesAuthProvidersResponse{Providers: items}, nil
}

// LoginHermes runs the native PKCE chain, persists only the refresh token and
// returns the non-sensitive identity for display. The password lives only for
// the duration of this call.
func (s *agentBackendSvc) LoginHermes(ctx context.Context, req *LoginHermesRequest) (*LoginHermesResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	base, err := agent_backend_entity.NormalizeHermesURL(req.URL)
	if err != nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	store := s.credentialStore()
	provider := strings.TrimSpace(req.Provider)
	if provider == "" {
		provider, err = store.resolveProvider(ctx, base, "")
		if err != nil {
			return nil, hermesAuthError(ctx, err)
		}
	}
	tokens, err := hermes.PasswordLogin(ctx, hermes.PasswordLoginRequest{
		BaseURL:  base,
		Provider: provider,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		return nil, hermesAuthError(ctx, err)
	}
	if err := store.StoreLogin(base, tokens); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokens.Provider) != "" {
		provider = tokens.Provider
	}
	return &LoginHermesResponse{Provider: provider, UserID: tokens.UserID}, nil
}

// LogoutHermes drops the stored credential for the serve and clears the two
// display fields on a saved backend. Credentials are keyed by URL, so every
// backend pointing at the same serve shares the logout.
func (s *agentBackendSvc) LogoutHermes(ctx context.Context, req *LogoutHermesRequest) (*LogoutHermesResponse, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	rawURL := strings.TrimSpace(req.URL)
	if req.ID > 0 {
		row, err := agent_backend_repo.AgentBackend().Find(ctx, req.ID)
		if err != nil {
			return nil, err
		}
		if row == nil {
			return nil, i18n.NewError(ctx, code.AgentBackendNotFound)
		}
		if strings.TrimSpace(row.HermesURL) != "" {
			rawURL = row.HermesURL
		}
		if strings.TrimSpace(row.HermesAuthProvider) != "" || strings.TrimSpace(row.HermesUserID) != "" {
			row.HermesAuthProvider = ""
			row.HermesUserID = ""
			row.Updatetime = s.now()
			if err := agent_backend_repo.AgentBackend().Update(ctx, row); err != nil {
				return nil, err
			}
			sync_svc.NotifyUpdate(ctx, syncwire.KindAgentBackend, row.ID, row.SyncMeta)
		}
	}
	if base, err := agent_backend_entity.NormalizeHermesURL(rawURL); err == nil && base != "" {
		if err := s.credentialStore().Logout(base); err != nil {
			return nil, err
		}
	}
	return &LogoutHermesResponse{}, nil
}

// deleteHermesCredential is the delete-backend hook: remove the shared keychain
// credential, logging a failure rather than blocking the delete.
func (s *agentBackendSvc) deleteHermesCredential(ctx context.Context, backend *agent_backend_entity.AgentBackend) {
	if backend == nil || !backend.IsHermes() || strings.TrimSpace(backend.HermesURL) == "" {
		return
	}
	base, err := agent_backend_entity.NormalizeHermesURL(backend.HermesURL)
	if err != nil {
		return
	}
	if err := s.credentialStore().Logout(base); err != nil {
		logger.Ctx(ctx).Warn("agent_backend delete: hermes keychain delete failed; the credential is keyed by URL, so a leak is harmless",
			zap.Int64("id", backend.ID), zap.Error(err))
	}
}
