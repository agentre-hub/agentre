package project_svc

import (
	"context"
	"errors"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func selfDeviceFingerprint() (devicefp.Carrier, error) {
	return remote_device_svc.Default().DeviceFingerprint()
}

// SetDefaultDevice 见 ProjectSvc 接口注释。默认设备是项目同步载荷的一部分：保存成功后
// 项目那一行排进上行。
func (s *projectSvc) SetDefaultDevice(ctx context.Context, id int64, fingerprint devicefp.Carrier) error {
	project, err := project_repo.Project().Find(ctx, id)
	if err != nil {
		return err
	}
	if project == nil {
		return i18n.NewError(ctx, code.ProjectNotFound)
	}
	fingerprint = devicefp.Carrier(strings.TrimSpace(string(fingerprint)))
	if fingerprint != "" {
		ok, err := s.hasPath(ctx, project, fingerprint)
		if err != nil {
			return err
		}
		if !ok {
			return i18n.NewError(ctx, code.ProjectDefaultDeviceNoPath)
		}
	}
	if err := project_repo.Project().SetDefaultDevice(ctx, id, fingerprint); err != nil {
		return err
	}
	sync_svc.NotifyUpdate(ctx, syncwire.KindProject, project.ID, project.SyncMeta)
	sync_svc.NotifyConfigChanged(syncwire.KindProject)
	return nil
}

func (s *projectSvc) GetDefaultDevice(ctx context.Context, id int64) (devicefp.Carrier, error) {
	project, err := project_repo.Project().Find(ctx, id)
	if err != nil {
		return "", err
	}
	if project == nil {
		return "", i18n.NewError(ctx, code.ProjectNotFound)
	}
	return project.DefaultDeviceFingerprint, nil
}

// hasPath 这台设备在这个项目里有没有路径：本机看项目自己的路径，其它设备看路径记录。
func (s *projectSvc) hasPath(ctx context.Context, project *project_entity.Project, fingerprint devicefp.Carrier) (bool, error) {
	self, err := s.self()
	if err != nil {
		return false, err
	}
	if fingerprint == self {
		return !project.LocalPathMissing && strings.TrimSpace(project.Path) != "", nil
	}
	loc, err := project_location_repo.ProjectLocation().FindByProjectAndFingerprint(ctx, project.ID, fingerprint)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return loc.IsActive() && strings.TrimSpace(loc.Path) != "", nil
}
