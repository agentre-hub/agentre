package server_svc

import (
	"context"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
)

// IntrospectCredential asks agentre-server whether token belongs to this
// device's own logged-in account (H1, desktop inbound side). Verification,
// the 60s success cache and the error classification (invalid / unreachable /
// receiver-not-ready) all live in auth.Introspector — this method only wires
// it to this device's live server URL, its own current access token and a
// one-shot refresh of that token.
func (s *service) IntrospectCredential(ctx context.Context, token string) (auth.Introspection, error) {
	return s.accountIntrospector().Verify(ctx, token)
}

// accountIntrospector builds this device's single Introspector instance the
// first time it's needed and reuses it afterwards. It must stay a singleton:
// the 60s success cache (H2) lives inside the instance, so a fresh one on
// every call would never hit.
func (s *service) accountIntrospector() *auth.Introspector {
	s.introspectOnce.Do(func() {
		s.introspector = auth.NewIntrospector(auth.IntrospectorOptions{
			ServerURL:         s.introspectServerURL,
			AccessToken:       s.AccessToken,
			RefreshCredential: s.refreshOwnCredentialForIntrospection,
			Now:               time.Now,
		})
	})
	return s.introspector
}

// introspectServerURL resolves the account server base URL at each
// verification, the same way NewInboundHubLink resolves it at each reconnect —
// login may finish, or the client may be swapped, after this device boots.
func (s *service) introspectServerURL() string {
	c := s.getClient()
	if c == nil {
		return ""
	}
	return c.baseURL
}

// refreshOwnCredentialForIntrospection refreshes this device's own access
// token exactly once, for auth.Introspector to call after the account server
// answers 401 to it — never for a business-invalid verdict on the *presented*
// credential, which auth.Introspector already classifies before ever
// reaching here. A server-confirmed rejection of the refresh token clears the
// local login, the same handling withAuth gives that same signal on every
// other authenticated call.
func (s *service) refreshOwnCredentialForIntrospection(ctx context.Context) error {
	err := s.refresh(ctx)
	if err == nil {
		return nil
	}
	if IsCredentialRejected(err) {
		logger.Ctx(ctx).Warn("server_svc.IntrospectCredential: account server rejected this device's own refresh token, clearing login",
			zap.Error(err))
		_ = s.clearLogin(ctx)
		return auth.ErrReceiverNotReady
	}
	return err
}
