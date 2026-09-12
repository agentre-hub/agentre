package server_svc

import (
	"context"
	"net/http"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
)

// Logout best-effort revokes server-side, then unconditionally clears local state.
// The local clear runs even if the remote call fails — a user clicking "disconnect"
// should always end up disconnected from their machine's perspective.
func (s *service) Logout(ctx context.Context) error {
	// best-effort revoke (server-side)
	_, _ = s.getClient().do(ctx, http.MethodPost, "/v1/oauth/token/revoke",
		map[string]int64{}, nil)

	// always wipe local state
	_ = keychain.Default().Delete(keychainAccountName)
	if err := server_state_repo.ServerState().ClearLoginFields(ctx); err != nil {
		return err
	}
	s.getClient().SetAccessToken("")
	// 常驻中继连接只在登录态成立（规格「常驻与空闲宽限的冲突要裁决」）：不收掉它，
	// 它会带着一个空凭据按退避一直重拨下去。下一次登录时 ensureRelay 重新懒建。
	s.dropRelay()
	s.emit(map[string]any{"kind": "logged_out", "reason": "user"})
	return nil
}
