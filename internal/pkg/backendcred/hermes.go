package backendcred

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesauth"
)

// hermesRefreshAccountPrefix namespaces the URL-derived slots. Only the refresh
// token is stored there; the password is never persisted.
const hermesRefreshAccountPrefix = "agentre-hermes-refresh-"

// hermesAccessRefreshSkew refreshes a cached access token slightly before it
// actually expires so a dial never races the expiry.
const hermesAccessRefreshSkew = 30 * time.Second

// HermesAccount derives the slot for a normalized serve URL: every backend on a
// device pointing at the same serve (and therefore the same identity) shares one
// credential, and login works before the backend is saved.
func HermesAccount(normalizedURL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(normalizedURL)))
	return hermesRefreshAccountPrefix + hex.EncodeToString(sum[:16])
}

// HermesLogin is one password login. The password lives only for the call.
type HermesLogin struct {
	BaseURL string
	// Provider is optional; blank picks the serve's only password provider.
	Provider string
	Username string
	Password string
}

// HermesIdentity is the non-sensitive result of a login, safe to display.
type HermesIdentity struct {
	Provider string
	UserID   string
}

type cachedHermesAccess struct {
	token     string
	expiresAt time.Time
}

// HermesCredentials bridges the login flow and the Hermes runtime: login writes
// the refresh token to the Store and caches the access token, the runtime asks
// for a bearer without touching the Store, and a rejected access token forces
// exactly one refresh. It satisfies the runtime's hermes.CredentialSource.
type HermesCredentials struct {
	mu    sync.Mutex
	cache map[string]cachedHermesAccess
	store func() Store
}

// NewHermesCredentials resolves the Store lazily on every use so a host can swap
// its store (for example an isolated keychain) after construction.
func NewHermesCredentials(store func() Store) *HermesCredentials {
	return &HermesCredentials{cache: map[string]cachedHermesAccess{}, store: store}
}

func (c *HermesCredentials) currentStore() Store {
	if c.store == nil {
		return nil
	}
	return c.store()
}

// Login runs the native PKCE password chain against baseURL and persists only
// the refresh token. Auth failures are the hermesauth sentinels.
func (c *HermesCredentials) Login(ctx context.Context, req HermesLogin) (*HermesIdentity, error) {
	base, err := agent_backend_entity.NormalizeHermesURL(req.BaseURL)
	if err != nil {
		return nil, err
	}
	provider, err := c.ResolveProvider(ctx, base, req.Provider)
	if err != nil {
		return nil, err
	}
	tokens, err := hermesauth.PasswordLogin(ctx, hermesauth.PasswordLoginRequest{
		BaseURL:  base,
		Provider: provider,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		return nil, err
	}
	if err := c.storeLogin(base, tokens); err != nil {
		return nil, err
	}
	if strings.TrimSpace(tokens.Provider) != "" {
		provider = tokens.Provider
	}
	return &HermesIdentity{Provider: provider, UserID: tokens.UserID}, nil
}

func (c *HermesCredentials) storeLogin(base string, tokens *hermesauth.AuthTokens) error {
	if tokens == nil || strings.TrimSpace(tokens.RefreshToken) == "" {
		return errors.New("hermes credential store: login returned no refresh token")
	}
	store := c.currentStore()
	if store == nil {
		return ErrStoreUnavailable
	}
	if err := store.Set(HermesAccount(base), tokens.RefreshToken); err != nil {
		return err
	}
	c.remember(base, tokens.AccessToken, tokens.ExpiresAt)
	return nil
}

// Logout drops both the stored refresh token and the cached access token of the
// serve. Logging out a serve that has no credential is not an error.
func (c *HermesCredentials) Logout(baseURL string) error {
	base, err := agent_backend_entity.NormalizeHermesURL(baseURL)
	if err != nil {
		return err
	}
	c.forget(base)
	store := c.currentStore()
	if store == nil {
		return ErrStoreUnavailable
	}
	if err := store.Delete(HermesAccount(base)); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// AccessToken returns a valid bearer for baseURL, refreshing it from the stored
// refresh token when the cache is empty or near expiry.
func (c *HermesCredentials) AccessToken(ctx context.Context, baseURL, provider string) (string, error) {
	base, err := agent_backend_entity.NormalizeHermesURL(baseURL)
	if err != nil {
		return "", err
	}
	if token, ok := c.cached(base); ok {
		return token, nil
	}
	store := c.currentStore()
	if store == nil {
		return "", fmt.Errorf("%w: %v", hermesauth.ErrLoginRequired, ErrStoreUnavailable)
	}
	refresh, err := store.Get(HermesAccount(base))
	if errors.Is(err, ErrNotFound) {
		return "", hermesauth.ErrLoginRequired
	}
	if err != nil {
		return "", fmt.Errorf("%w: keychain: %v", hermesauth.ErrLoginRequired, err)
	}
	if strings.TrimSpace(refresh) == "" {
		return "", hermesauth.ErrLoginRequired
	}
	resolvedProvider, err := c.ResolveProvider(ctx, base, provider)
	if err != nil {
		return "", err
	}
	tokens, err := hermesauth.RefreshAuthTokens(ctx, hermesauth.RefreshAuthRequest{
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
		if setErr := store.Set(HermesAccount(base), next); setErr != nil {
			return "", setErr
		}
	}
	c.remember(base, tokens.AccessToken, tokens.ExpiresAt)
	return tokens.AccessToken, nil
}

// Invalidate drops the cached access token so the next AccessToken refreshes.
func (c *HermesCredentials) Invalidate(baseURL string) {
	base, err := agent_backend_entity.NormalizeHermesURL(baseURL)
	if err != nil {
		return
	}
	c.forget(base)
}

// ResolveProvider picks the provider used for login or refresh. An explicit
// hint wins; otherwise the only password-capable provider is chosen, and a
// serve offering only browser-based providers is ErrPasswordLoginUnsupported.
func (c *HermesCredentials) ResolveProvider(ctx context.Context, normalizedURL, hint string) (string, error) {
	if p := strings.TrimSpace(hint); p != "" {
		return p, nil
	}
	providers, err := hermesauth.ListAuthProviders(ctx, normalizedURL, nil)
	if err != nil {
		return "", err
	}
	for _, p := range providers {
		if p.SupportsPassword {
			return p.Name, nil
		}
	}
	return "", hermesauth.ErrPasswordLoginUnsupported
}

func (c *HermesCredentials) cached(base string) (string, bool) {
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

func (c *HermesCredentials) remember(base, token string, expiresAt time.Time) {
	c.mu.Lock()
	c.cache[base] = cachedHermesAccess{token: token, expiresAt: expiresAt}
	c.mu.Unlock()
}

func (c *HermesCredentials) forget(base string) {
	c.mu.Lock()
	delete(c.cache, base)
	c.mu.Unlock()
}
