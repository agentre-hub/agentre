package department_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
)

// UpdateDefaultEngineProfile 见 DepartmentRepo 接口注释。窄写：部门的其它列由 Update 整行
// 回写（部门详情、同步落地），成员默认引擎只有这一个写入口。
func (r *departmentRepo) UpdateDefaultEngineProfile(ctx context.Context, id, engineProfileID int64) error {
	return db.Ctx(ctx).Model(&department_entity.Department{}).
		Where("id = ? AND status = ?", id, consts.ACTIVE).
		Updates(map[string]any{
			"default_engine_profile_id": engineProfileID,
			"updatetime":                time.Now().UnixMilli(),
		}).Error
}

func (r *departmentRepo) ListByDefaultEngineProfile(ctx context.Context, engineProfileID int64) ([]*department_entity.Department, error) {
	var rows []*department_entity.Department
	err := db.Ctx(ctx).
		Where("default_engine_profile_id = ? AND status = ?", engineProfileID, consts.ACTIVE).
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

// ClearDefaultEngineProfile 不限部门状态，理由同 agent_repo.ClearEngineProfile。
func (r *departmentRepo) ClearDefaultEngineProfile(ctx context.Context, engineProfileID int64) error {
	return db.Ctx(ctx).Model(&department_entity.Department{}).
		Where("default_engine_profile_id = ?", engineProfileID).
		Updates(map[string]any{
			"default_engine_profile_id": int64(0),
			"updatetime":                time.Now().UnixMilli(),
		}).Error
}
