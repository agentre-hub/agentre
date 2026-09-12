package server_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
)

// GetState returns the current server_state row. When no row is persisted yet
// (fresh install), returns a zero-valued row with ID=1 so callers can rely
// on a non-nil pointer.
func (s *service) GetState(ctx context.Context) (*server_state_entity.ServerState, error) {
	row, err := server_state_repo.ServerState().Get(ctx)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return &server_state_entity.ServerState{ID: 1}, nil
	}
	return row, nil
}

// CheckURL is a side-effect-free probe used by the LoginDialog URL validator.
// It builds a throwaway serverClient so it doesn't perturb the singleton's
// access-token / base URL.
func (s *service) CheckURL(ctx context.Context, serverURL string) (string, error) {
	c := NewHTTPClient(serverURL, "")
	return c.Healthcheck(ctx)
}
