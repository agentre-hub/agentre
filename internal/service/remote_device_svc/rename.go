package remote_device_svc

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

func (s *service) Rename(ctx context.Context, id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	row, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if row == nil {
		return i18n.NewError(ctx, code.RemoteDeviceNotFound)
	}
	return s.repo.Rename(ctx, id, name)
}
