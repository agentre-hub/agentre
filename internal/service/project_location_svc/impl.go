package project_location_svc

import (
	"context"
	"errors"
	"strconv"

	"github.com/cago-frame/cago/pkg/i18n"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
)

type projectLocationImpl struct{}

func (s *projectLocationImpl) ListByProject(ctx context.Context, projectID int64) ([]*ProjectLocationView, error) {
	rows, err := project_location_repo.ProjectLocation().ListByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	devices, err := remote_device_svc.Default().List(ctx)
	if err != nil {
		return nil, err
	}
	byFingerprint := make(map[string]*remote_device_svc.DeviceView, len(devices))
	for _, d := range devices {
		if d.DaemonFingerprint != "" {
			byFingerprint[d.DaemonFingerprint] = d
		}
	}

	out := make([]*ProjectLocationView, 0, len(rows))
	for _, r := range rows {
		dv, resolved := byFingerprint[r.DeviceFingerprint]
		if !resolved {
			// R2b：本机配对表里查不到这个指纹——该行本机不参与解析、也不出现在
			// 路径列表里；若此前缓存过 device_id 先清空退回，行本身与 path 不丢。
			if r.DeviceID != "" {
				if err := project_location_repo.ProjectLocation().UpdateDeviceID(ctx, r.ID, ""); err != nil {
					return nil, err
				}
			}
			continue
		}
		newDeviceID := strconv.FormatInt(dv.ID, 10)
		if r.DeviceID != newDeviceID {
			// 取得配对行后自动生效：回填/刷新缓存，用户不需要再做第二件事。
			if err := project_location_repo.ProjectLocation().UpdateDeviceID(ctx, r.ID, newDeviceID); err != nil {
				return nil, err
			}
		}
		out = append(out, &ProjectLocationView{
			ID:         r.ID,
			ProjectID:  r.ProjectID,
			DeviceID:   newDeviceID,
			Path:       r.Path,
			DeviceName: dv.Name,
			Online:     dv.Online,
		})
	}
	return out, nil
}

func (s *projectLocationImpl) Upsert(ctx context.Context, projectID int64, deviceID, path string) (*ProjectLocationView, error) {
	// 远端 device 校验：必须能解析为 int64 AND 在 paired_agentreds 中存在；顺带取出
	// 它的指纹——账号内自然键是 (project, device_fingerprint)，不是 device_id。
	var dv *remote_device_svc.DeviceView
	var fingerprint string
	if deviceID != "" {
		id, ok := parseDeviceID(deviceID)
		if !ok {
			return nil, i18n.NewError(ctx, code.AgentBackendInvalidDevice)
		}
		got, err := remote_device_svc.Default().Get(ctx, id)
		if err != nil || got == nil {
			return nil, i18n.NewError(ctx, code.AgentBackendInvalidDevice)
		}
		dv = got
		fingerprint = dv.DaemonFingerprint
	}

	// entity 自校验
	entity := &project_location_entity.ProjectLocation{
		ProjectID: projectID, DeviceID: deviceID, DeviceFingerprint: fingerprint, Path: path,
	}
	if err := entity.Check(ctx); err != nil {
		return nil, err
	}

	existing, err := project_location_repo.ProjectLocation().FindByProjectAndFingerprint(ctx, projectID, fingerprint)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if existing != nil {
		if err := project_location_repo.ProjectLocation().UpdatePath(ctx, existing.ID, path); err != nil {
			return nil, err
		}
		if existing.DeviceID != deviceID {
			// 同一指纹换了本地 device_id（比如重新配对）：缓存跟着自愈，不留旧值。
			if err := project_location_repo.ProjectLocation().UpdateDeviceID(ctx, existing.ID, deviceID); err != nil {
				return nil, err
			}
		}
		existing.Path = path
		existing.DeviceID = deviceID
		sync_svc.NotifyUpdate(ctx, syncwire.KindProjectLocation, existing.ID, existing.SyncMeta)
		return s.toResolvedView(existing, dv), nil
	}
	if err := project_location_repo.ProjectLocation().Create(ctx, entity); err != nil {
		return nil, err
	}
	sync_svc.NotifyCreate(ctx, syncwire.KindProjectLocation, entity.ID, entity.SyncMeta)
	return s.toResolvedView(entity, dv), nil
}

func (s *projectLocationImpl) RemoveByProjectAndDevice(ctx context.Context, projectID int64, deviceID string) error {
	row, err := project_location_repo.ProjectLocation().FindByProjectAndDevice(ctx, projectID, deviceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return i18n.NewError(ctx, code.ProjectLocationNotFound)
		}
		return err
	}
	if err := project_location_repo.ProjectLocation().Delete(ctx, row.ID); err != nil {
		return err
	}
	sync_svc.NotifyDelete(ctx, syncwire.KindProjectLocation, row.ID, row.SyncMeta)
	return nil
}

// toResolvedView 把 entity 与 Upsert 已经拿到手的 DeviceView（可能为 nil）拼成
// 前端视图，不重复发起查询。
func (s *projectLocationImpl) toResolvedView(p *project_location_entity.ProjectLocation, dv *remote_device_svc.DeviceView) *ProjectLocationView {
	v := &ProjectLocationView{
		ID:        p.ID,
		ProjectID: p.ProjectID,
		DeviceID:  p.DeviceID,
		Path:      p.Path,
	}
	if dv != nil {
		v.DeviceName = dv.Name
		v.Online = dv.Online
	}
	return v
}

func parseDeviceID(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}
