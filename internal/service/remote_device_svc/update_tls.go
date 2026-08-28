// internal/service/remote_device_svc/update_tls.go
package remote_device_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

func (s *service) UpdateTLS(ctx context.Context, id int64, mode, pem string) (*DeviceView, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}
	row.TLSMode = mode
	row.TLSCertPEM = pem
	if err := row.Check(ctx); err != nil {
		return nil, err
	}
	if err := s.repo.UpdateTLS(ctx, id, mode, pem); err != nil {
		return nil, err
	}
	if s.watcher != nil {
		_ = s.watcher.Restart(ctx, id)
	}
	return s.Refresh(ctx, id)
}
