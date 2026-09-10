// Package agent_repo 提供 Agent 的持久化访问。
package agent_repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/consts"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

//go:generate mockgen -source agent.go -destination mock_agent_repo/mock_agent.go

type AgentRepo interface {
	Create(ctx context.Context, a *agent_entity.Agent) error
	// UpdateWithTargets 落 Agent 行，并把执行目标列表整表替换成 targets 给出的**完整
	// 有序列表**（agent_svc，R15 多档编辑）。
	//
	// 没有「只给 Agent 行、让仓储自己折出执行目标」的那一档变体：它会把
	// a.AgentBackendID/a.SkillsJSON 折成单元素列表，从而把对端配好的多档列表默默
	// 截断成一档。要么给出完整列表（这里），要么明说不动列表（UpdateRow）。
	UpdateWithTargets(ctx context.Context, a *agent_entity.Agent, targets []*agent_entity.AgentExecTarget) error
	// UpdateRow 只落 Agent 这一行，不动它的执行目标列表（同步落地专用，见实现注释）。
	UpdateRow(ctx context.Context, a *agent_entity.Agent) error
	Find(ctx context.Context, id int64) (*agent_entity.Agent, error)
	FindByName(ctx context.Context, name string) (*agent_entity.Agent, error)
	FindSystem(ctx context.Context) (*agent_entity.Agent, error)
	List(ctx context.Context) ([]*agent_entity.Agent, error)
	ListByDepartment(ctx context.Context, departmentID int64) ([]*agent_entity.Agent, error)
	ListByParent(ctx context.Context, parentAgentID int64) ([]*agent_entity.Agent, error)
	CountByBackends(ctx context.Context, backendIDs []int64) (map[int64]int64, error)
	NextSortOrder(ctx context.Context, departmentID int64) (int, error)
	NextSortOrderByParent(ctx context.Context, parentAgentID int64) (int, error)
	UpdatePlacement(ctx context.Context, id, departmentID, parentAgentID int64, sortOrder int) error
	UpdateAvatar(ctx context.Context, id int64, avatarDataURL string, updatetime int64) error
	SetPinned(ctx context.Context, id int64, pinned bool) error
	ReparentChildren(ctx context.Context, fromParentAgentID, toDepartmentID, toParentAgentID int64) error
	ClearLeadOfDepartment(ctx context.Context, agentID int64) error
	Delete(ctx context.Context, id int64) error
	ReorderSiblings(ctx context.Context, departmentID, parentAgentID int64, orderedIDs []int64) error
}

var defaultAgent AgentRepo

func Agent() AgentRepo             { return defaultAgent }
func RegisterAgent(impl AgentRepo) { defaultAgent = impl }
func NewAgent() AgentRepo          { return &agentRepo{} }

type agentRepo struct{}

// Create 落 Agent 行，并把它的 AgentBackendID + SkillsJSON 落成单元素执行目标列表
// （0 = 空列表）。两张表必须同事务：只落一半会让 Agent 派发不到 backend。
// a.SkillsJSON 是仓储写入载荷（agent_entity.Agent.SkillsJSON 字段注释）：技能授权
// 的存放位置已下沉到执行目标行（R15e），这里只是把调用方给的值原样转落到那一行。
func (r *agentRepo) Create(ctx context.Context, a *agent_entity.Agent) error {
	// 同步标识在行创建时就地生成，未登录期间也照常写入（R1/R12a）。
	a.EnsureSyncID()
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(a).Error; err != nil {
			return err
		}
		return insertExecTargets(tx, a.ID, primaryTargetList(a.AgentBackendID, a.SkillsJSON))
	})
}

// UpdateRow 只落 Agent 这一行，**不动**它的执行目标列表。
//
// 同步落地（internal/service/sync_svc）走这条：执行目标是账号级的独立同步对象，
// 各自带着自己的同步标识与版本，不能被 Agent 行上的派生字段 AgentBackendID 重写
// 成单元素列表——那会把对端配好的多档列表冲掉。
func (r *agentRepo) UpdateRow(ctx context.Context, a *agent_entity.Agent) error {
	a.EnsureSyncID()
	return db.Ctx(ctx).Save(a).Error
}

// UpdateWithTargets 见 AgentRepo 接口注释。
func (r *agentRepo) UpdateWithTargets(ctx context.Context, a *agent_entity.Agent, targets []*agent_entity.AgentExecTarget) error {
	// 迁移前已存在、还没有标识的历史行在下一次落库时补齐（JIT），已有标识的行
	// 原样保留（R1：终身不变）。
	a.EnsureSyncID()
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(a).Error; err != nil {
			return err
		}
		return replaceExecTargets(tx, a.ID, targets)
	})
}

func (r *agentRepo) Find(ctx context.Context, id int64) (*agent_entity.Agent, error) {
	out := &agent_entity.Agent{}
	err := db.Ctx(ctx).Where("id = ? AND status = ?", id, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return hydrateOne(ctx, out)
}

func (r *agentRepo) FindByName(ctx context.Context, name string) (*agent_entity.Agent, error) {
	out := &agent_entity.Agent{}
	err := db.Ctx(ctx).Where("name = ? AND status = ?", name, consts.ACTIVE).First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return hydrateOne(ctx, out)
}

func (r *agentRepo) FindSystem(ctx context.Context) (*agent_entity.Agent, error) {
	out := &agent_entity.Agent{}
	err := db.Ctx(ctx).
		Where("system_badge = ? AND status = ?", agent_entity.SystemBadgeDefault, consts.ACTIVE).
		First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return hydrateOne(ctx, out)
}

func (r *agentRepo) List(ctx context.Context) ([]*agent_entity.Agent, error) {
	var rows []*agent_entity.Agent
	err := db.Ctx(ctx).
		Where("status = ?", consts.ACTIVE).
		Order("department_id ASC, parent_agent_id ASC, sort_order ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return rows, err
	}
	return rows, hydrateExecTargets(ctx, rows)
}

func (r *agentRepo) ListByDepartment(ctx context.Context, departmentID int64) ([]*agent_entity.Agent, error) {
	var rows []*agent_entity.Agent
	err := db.Ctx(ctx).
		Where("department_id = ? AND parent_agent_id = ? AND status = ?", departmentID, int64(0), consts.ACTIVE).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return rows, err
	}
	return rows, hydrateExecTargets(ctx, rows)
}

func (r *agentRepo) ListByParent(ctx context.Context, parentAgentID int64) ([]*agent_entity.Agent, error) {
	var rows []*agent_entity.Agent
	err := db.Ctx(ctx).
		Where("parent_agent_id = ? AND status = ?", parentAgentID, consts.ACTIVE).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error
	if err != nil {
		return rows, err
	}
	return rows, hydrateExecTargets(ctx, rows)
}

// CountByBackends 统计每个 backend 被多少个活跃 Agent 的执行目标引用。同一个 Agent
// 即便把同一个 backend 排了两档也只算一次。
func (r *agentRepo) CountByBackends(ctx context.Context, backendIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(backendIDs))
	if len(backendIDs) == 0 {
		return out, nil
	}
	var rows []struct {
		AgentBackendID int64 `gorm:"column:agent_backend_id"`
		Cnt            int64 `gorm:"column:cnt"`
	}
	err := db.Ctx(ctx).Table("agent_exec_targets").
		Select("agent_exec_targets.agent_backend_id AS agent_backend_id, COUNT(DISTINCT agent_exec_targets.agent_id) AS cnt").
		Joins("JOIN agents ON agents.id = agent_exec_targets.agent_id").
		Where("agent_exec_targets.agent_backend_id IN ? AND agents.status = ?", backendIDs, consts.ACTIVE).
		Group("agent_exec_targets.agent_backend_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.AgentBackendID] = row.Cnt
	}
	return out, nil
}

func (r *agentRepo) NextSortOrder(ctx context.Context, departmentID int64) (int, error) {
	var maxOrder int
	err := db.Ctx(ctx).Table("agents").
		Where("department_id = ? AND parent_agent_id = ? AND status = ?", departmentID, int64(0), consts.ACTIVE).
		Select("COALESCE(MAX(sort_order), 0)").Row().Scan(&maxOrder)
	if err != nil {
		return 0, err
	}
	return maxOrder + 1, nil
}

func (r *agentRepo) NextSortOrderByParent(ctx context.Context, parentAgentID int64) (int, error) {
	var maxOrder int
	err := db.Ctx(ctx).Table("agents").
		Where("parent_agent_id = ? AND status = ?", parentAgentID, consts.ACTIVE).
		Select("COALESCE(MAX(sort_order), 0)").Row().Scan(&maxOrder)
	if err != nil {
		return 0, err
	}
	return maxOrder + 1, nil
}

func (r *agentRepo) UpdatePlacement(ctx context.Context, id, departmentID, parentAgentID int64, sortOrder int) error {
	return db.Ctx(ctx).Model(&agent_entity.Agent{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"department_id":   departmentID,
			"parent_agent_id": parentAgentID,
			"sort_order":      sortOrder,
		}).Error
}

func (r *agentRepo) ReorderSiblings(ctx context.Context, departmentID, parentAgentID int64, orderedIDs []int64) error {
	now := time.Now().UnixMilli()
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		for idx, id := range orderedIDs {
			sortOrder := idx + 1
			result := tx.Exec(
				"UPDATE agents SET sort_order = ?, updatetime = ? WHERE id = ? AND department_id = ? AND parent_agent_id = ? AND status = ?",
				sortOrder, now, id, departmentID, parentAgentID, consts.ACTIVE,
			)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("agent reorder affected %d rows for id %d", result.RowsAffected, id)
			}
		}
		return nil
	})
}

func (r *agentRepo) UpdateAvatar(ctx context.Context, id int64, avatarDataURL string, updatetime int64) error {
	return db.Ctx(ctx).Model(&agent_entity.Agent{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"avatar_data_url": avatarDataURL,
			"updatetime":      updatetime,
		}).Error
}

func (r *agentRepo) SetPinned(ctx context.Context, id int64, pinned bool) error {
	return db.Ctx(ctx).Model(&agent_entity.Agent{}).
		Where("id = ?", id).
		Update("pinned", pinned).Error
}

func (r *agentRepo) ReparentChildren(ctx context.Context, fromParentAgentID, toDepartmentID, toParentAgentID int64) error {
	return db.Ctx(ctx).Model(&agent_entity.Agent{}).
		Where("parent_agent_id = ? AND status = ?", fromParentAgentID, consts.ACTIVE).
		Updates(map[string]any{
			"department_id":   toDepartmentID,
			"parent_agent_id": toParentAgentID,
		}).Error
}

func (r *agentRepo) ClearLeadOfDepartment(ctx context.Context, agentID int64) error {
	return db.Ctx(ctx).Table("departments").
		Where("lead_agent_id = ?", agentID).
		Update("lead_agent_id", 0).Error
}

func (r *agentRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Model(&agent_entity.Agent{}).
		Where("id = ?", id).
		Update("status", consts.DELETE).Error
}
