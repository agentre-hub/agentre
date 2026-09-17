package agent_backend_svc

import (
	"context"
	"errors"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// defaultHermesCredentials is the singleton the registered runtime reaches
// through hermes.DefaultCredentialSource. It resolves the keychain lazily so
// bootstrap's isolated-keychain swap is honored.
var defaultHermesCredentials = backendcred.NewHermesCredentials(func() backendcred.Store {
	if kc := keychain.Default(); kc != nil {
		return kc
	}
	return nil
})

func init() {
	hermes.SetDefaultCredentialSource(defaultHermesCredentials)
}

func (s *agentBackendSvc) credentialStore() *backendcred.HermesCredentials {
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
	identity, err := s.credentialStore().Login(ctx, backendcred.HermesLogin{
		BaseURL:  base,
		Provider: req.Provider,
		Username: req.Username,
		Password: req.Password,
	})
	if err != nil {
		return nil, hermesAuthError(ctx, err)
	}
	return &LoginHermesResponse{Provider: identity.Provider, UserID: identity.UserID}, nil
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

// deleteHermesCredential is the delete-backend hook. The credential is keyed by
// URL and shared by every backend on this device pointing at the same serve, so
// it is cleared only when no remaining local backend still uses that URL. It is
// best-effort: a failure is logged and never blocks the delete, and when the
// remaining backends cannot be listed the login is kept rather than risk logging
// out another backend (a leftover credential is harmless).
func (s *agentBackendSvc) deleteHermesCredential(ctx context.Context, backend *agent_backend_entity.AgentBackend) {
	if backend == nil || !backend.IsHermes() || strings.TrimSpace(backend.HermesURL) == "" {
		return
	}
	base, err := agent_backend_entity.NormalizeHermesURL(backend.HermesURL)
	if err != nil {
		return
	}
	inUse, err := hermesURLUsedByAnotherLocalBackend(ctx, backend.ID, base)
	if err != nil {
		logger.Ctx(ctx).Warn("agent_backend delete: cannot list backends; keeping the shared hermes login",
			zap.Int64("id", backend.ID), zap.Error(err))
		return
	}
	if inUse {
		return
	}
	if err := s.credentialStore().Logout(base); err != nil {
		logger.Ctx(ctx).Warn("agent_backend delete: hermes keychain delete failed; the credential is keyed by URL, so a leak is harmless",
			zap.Int64("id", backend.ID), zap.Error(err))
	}
}

// hermesURLUsedByAnotherLocalBackend reports whether an active Hermes backend
// other than deletedID, bound to this device, points at the normalized URL.
func hermesURLUsedByAnotherLocalBackend(ctx context.Context, deletedID int64, base string) (bool, error) {
	rows, err := agent_backend_repo.AgentBackend().List(ctx)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row == nil || row.ID == deletedID || !row.IsHermes() ||
			remote_device_svc.TargetsAnotherMachine(row.DeviceFingerprint) {
			continue
		}
		if other, err := agent_backend_entity.NormalizeHermesURL(row.HermesURL); err == nil && other == base {
			return true, nil
		}
	}
	return false, nil
}
