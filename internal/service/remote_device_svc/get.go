package remote_device_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/i18n"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// Get 按 id 返回一份只读的 DeviceView（不主动探活；Online 仍由 LastSeenAt 推算）。
func (s *service) Get(ctx context.Context, id int64) (*DeviceView, error) {
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, i18n.NewError(ctx, code.RemoteDeviceNotFound)
		}
		return nil, err
	}
	if row == nil {
		return nil, i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}
	return s.toView(row), nil
}
