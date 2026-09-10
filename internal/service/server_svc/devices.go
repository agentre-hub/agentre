package server_svc

import (
	"context"
	"net/http"

	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
)

// listDevicesItem mirrors the hub's /v1/devices response item.
type listDevicesItem struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Platform     string `json:"platform"`
	Version      string `json:"version"`
	Fingerprint  string `json:"fingerprint"`
	LastSeenAt   int64  `json:"last_seen_at"`
	Status       int    `json:"status"`
	Online       bool   `json:"online"`
	IsThisDevice bool   `json:"is_this_device"`
}

type listDevicesResp struct {
	Devices []listDevicesItem `json:"devices"`
}

// ListDevices returns the user's devices known to the Hub. Requires login.
// Refresh-on-401 is handled via withAuth.
func (s *service) ListDevices(ctx context.Context) ([]Device, error) {
	row, err := server_state_repo.ServerState().Get(ctx)
	if err != nil {
		return nil, err
	}
	if row == nil || !row.IsLoggedIn() {
		return nil, ErrNotLoggedIn
	}

	var resp []Device
	wrapErr := s.withAuth(ctx, func(ctx context.Context) error {
		var env envelope[listDevicesResp]
		_, e := s.getClient().do(ctx, http.MethodGet, "/v1/devices", nil, &env)
		if e != nil {
			return e
		}
		if env.Code != 0 {
			return ErrServerUnreachable
		}
		resp = make([]Device, 0, len(env.Data.Devices))
		for _, d := range env.Data.Devices {
			resp = append(resp, Device(d))
		}
		return nil
	})
	if wrapErr != nil {
		return nil, wrapErr
	}
	return resp, nil
}
