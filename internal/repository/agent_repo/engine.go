package agent_repo

import (
	"context"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

// engineSettingsColumns 是成员执行设置的四列：只由 UpdateEngineSettings 写，整行回写
// （UpdateRow / UpdateWithTargets）一律 Omit——一份读得早的旧实体会把刚换好的引擎配置拍回去。
var engineSettingsColumns = []string{"engine_profile_id", "exec_constraint_json", "skill_grants_runtime", "skill_grants_json"}

// UpdateEngineSettings 见 AgentRepo 接口注释。窄写而不是整行 Save：成员页的自动保存与
// 同步落地各自整行回写别的列，执行设置有自己的一个写入口，互不冲掉。
func (r *agentRepo) UpdateEngineSettings(ctx context.Context, a *agent_entity.Agent) error {
	return db.Ctx(ctx).Model(&agent_entity.Agent{}).
		Where("id = ? AND status = ?", a.ID, consts.ACTIVE).
		Updates(map[string]any{
			"engine_profile_id":    a.EngineProfileID,
			"exec_constraint_json": a.ExecConstraintJSON,
			"skill_grants_runtime": a.SkillGrantsRuntime,
			"skill_grants_json":    a.SkillGrantsJSON,
			"updatetime":           a.Updatetime,
		}).Error
}

func (r *agentRepo) ListByEngineProfile(ctx context.Context, engineProfileID int64) ([]*agent_entity.Agent, error) {
	var rows []*agent_entity.Agent
	err := db.Ctx(ctx).
		Where("engine_profile_id = ? AND status = ?", engineProfileID, consts.ACTIVE).
		Order("id ASC").
		Find(&rows).Error
	return rows, err
}

// ClearEngineProfile 不限成员状态：软删的成员也不该留着指向已删配置的引用。
func (r *agentRepo) ClearEngineProfile(ctx context.Context, engineProfileID int64) error {
	return db.Ctx(ctx).Model(&agent_entity.Agent{}).
		Where("engine_profile_id = ?", engineProfileID).
		Updates(map[string]any{
			"engine_profile_id": int64(0),
			"updatetime":        time.Now().UnixMilli(),
		}).Error
}

func (r *agentRepo) CountByEngineProfiles(ctx context.Context, engineProfileIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(engineProfileIDs))
	if len(engineProfileIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		EngineProfileID int64 `gorm:"column:engine_profile_id"`
		Cnt             int64 `gorm:"column:cnt"`
	}
	err := db.Ctx(ctx).Model(&agent_entity.Agent{}).
		Select("engine_profile_id, COUNT(*) AS cnt").
		Where("engine_profile_id IN ? AND status = ?", engineProfileIDs, consts.ACTIVE).
		Group("engine_profile_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.EngineProfileID] = row.Cnt
	}
	return out, nil
}
