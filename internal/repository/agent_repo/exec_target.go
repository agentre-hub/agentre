package agent_repo

import (
	"context"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

//go:generate mockgen -source exec_target.go -destination mock_agent_repo/mock_exec_target.go

// AgentExecTargetRepo Agent 执行目标列表的持久化访问。列表是有序的，读口一律按
// sort_order 升序给出；写口整表替换（下标即 sort_order）。
type AgentExecTargetRepo interface {
	ListByAgent(ctx context.Context, agentID int64) ([]*agent_entity.AgentExecTarget, error)
	ListByAgents(ctx context.Context, agentIDs []int64) (map[int64][]*agent_entity.AgentExecTarget, error)
	// ListByBackend 列出引用了该 backend 的全部执行目标行（删 backend 时它们一并
	// 落墓碑，R6）。
	ListByBackend(ctx context.Context, backendID int64) ([]*agent_entity.AgentExecTarget, error)
	// Replace 把列表替换成 targets 给出的顺序：下标即 sort_order，调用方只需给
	// AgentBackendID + SkillsJSON，ID/AgentID/SortOrder 由仓储自己落。技能授权
	// （R15e）与它所在的档同生共死：没有被带进新列表的档连它的 skills_json 一起
	// 消失，不需要一个单独的"清理授权"步骤。
	//
	// 活下来的档**保持同一行**（同一个自增主键与同一个同步标识）：跨机身份终身
	// 不变（R1），删后重插会让每次改 Agent 都在对端表现成「删了又建」。
	Replace(ctx context.Context, agentID int64, targets []*agent_entity.AgentExecTarget) error
	// UpsertFromSync 按同步标识落地一行下行来的执行目标（新建或更新），沿用它自己
	// 的同步标识。同一 Agent 下被它占位的 sort_order 让位到末尾，等那一行自己的
	// 下行项到达时再归位——(agent_id, sort_order) 上有唯一索引。
	UpsertFromSync(ctx context.Context, row *agent_entity.AgentExecTarget) error
	// DeleteBySyncID 按同步标识删掉一行执行目标（墓碑到达，R6）。
	DeleteBySyncID(ctx context.Context, syncID string) error
}

var defaultAgentExecTarget AgentExecTargetRepo

func AgentExecTarget() AgentExecTargetRepo             { return defaultAgentExecTarget }
func RegisterAgentExecTarget(impl AgentExecTargetRepo) { defaultAgentExecTarget = impl }
func NewAgentExecTarget() AgentExecTargetRepo          { return &agentExecTargetRepo{} }

type agentExecTargetRepo struct{}

func (r *agentExecTargetRepo) ListByAgent(ctx context.Context, agentID int64) ([]*agent_entity.AgentExecTarget, error) {
	return listExecTargets(ctx, []int64{agentID})
}

func (r *agentExecTargetRepo) ListByAgents(ctx context.Context, agentIDs []int64) (map[int64][]*agent_entity.AgentExecTarget, error) {
	rows, err := listExecTargets(ctx, agentIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[int64][]*agent_entity.AgentExecTarget, len(agentIDs))
	for _, row := range rows {
		out[row.AgentID] = append(out[row.AgentID], row)
	}
	return out, nil
}

func (r *agentExecTargetRepo) ListByBackend(ctx context.Context, backendID int64) ([]*agent_entity.AgentExecTarget, error) {
	var rows []*agent_entity.AgentExecTarget
	err := db.Ctx(ctx).
		Where("agent_backend_id = ?", backendID).
		Order("agent_id ASC, sort_order ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

func (r *agentExecTargetRepo) Replace(ctx context.Context, agentID int64, targets []*agent_entity.AgentExecTarget) error {
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		return replaceExecTargets(tx, agentID, targets)
	})
}

func (r *agentExecTargetRepo) UpsertFromSync(ctx context.Context, row *agent_entity.AgentExecTarget) error {
	if row == nil || row.SyncID == "" {
		return nil
	}
	return db.Ctx(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []*agent_entity.AgentExecTarget
		if err := tx.Where("agent_id = ?", row.AgentID).Find(&existing).Error; err != nil {
			return err
		}
		var mine *agent_entity.AgentExecTarget
		maxOrder := -1
		for _, e := range existing {
			if e.SyncID == row.SyncID {
				mine = e
			}
			if e.SortOrder > maxOrder {
				maxOrder = e.SortOrder
			}
		}
		// 腾位：别的行占着这个 sort_order 时先挪到末尾。它自己的下行项到达时会
		// 把它放回正确的位置——两端最终都按各自的下行序列收敛（R4）。
		for _, e := range existing {
			if e.SyncID != row.SyncID && e.SortOrder == row.SortOrder {
				maxOrder++
				if err := tx.Model(&agent_entity.AgentExecTarget{}).
					Where("id = ?", e.ID).
					Update("sort_order", maxOrder).Error; err != nil {
					return err
				}
			}
		}
		if mine != nil {
			return tx.Model(&agent_entity.AgentExecTarget{}).
				Where("id = ?", mine.ID).
				Updates(map[string]any{
					"agent_id":         row.AgentID,
					"agent_backend_id": row.AgentBackendID,
					"sort_order":       row.SortOrder,
					"skills_json":      row.SkillsJSON,
				}).Error
		}
		return tx.Create(row).Error
	})
}

func (r *agentExecTargetRepo) DeleteBySyncID(ctx context.Context, syncID string) error {
	if syncID == "" {
		return nil
	}
	return db.Ctx(ctx).Where("sync_id = ?", syncID).Delete(&agent_entity.AgentExecTarget{}).Error
}

// listExecTargets 按 (agent_id, sort_order, id) 升序读出给定 Agent 的执行目标行。
// 一次查询覆盖整批 Agent，调用方自己分组。
func listExecTargets(ctx context.Context, agentIDs []int64) ([]*agent_entity.AgentExecTarget, error) {
	if len(agentIDs) == 0 {
		return nil, nil
	}
	var rows []*agent_entity.AgentExecTarget
	err := db.Ctx(ctx).
		Where("agent_id IN ?", agentIDs).
		Order("agent_id ASC, sort_order ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

// replaceExecTargets 把一个 Agent 的执行目标列表替换成 targets 给出的顺序。
// tx 由调用方给出：Agent 行与它的执行目标行必须在同一个事务里落库。
//
// 按差异更新，不是删后重插：活下来的档保持同一行，连同它的同步标识（R1 跨机身份
// 终身不变——每次改 Agent 都重铸标识，会让对端把整张列表看成「删了又建」）。
// 没有被带进 targets 的档整行消失，连它的 skills_json 一起——"删档连带删授权"
// （R15e）仍然成立，不存在"档已删、授权没删"的中间态。
//
// 匹配次序：先按同步标识（调用方明确指名了是哪一行），再按 backend——同一个 Agent
// 把同一个 backend 排两档时，多出来的那一档按顺序各配一行。
func replaceExecTargets(tx *gorm.DB, agentID int64, targets []*agent_entity.AgentExecTarget) error {
	var existing []*agent_entity.AgentExecTarget
	if err := tx.Where("agent_id = ?", agentID).
		Order("sort_order ASC, id ASC").Find(&existing).Error; err != nil {
		return err
	}
	if len(existing) == 0 {
		return insertExecTargets(tx, agentID, targets)
	}

	bySyncID := make(map[string]*agent_entity.AgentExecTarget, len(existing))
	byBackend := make(map[int64][]*agent_entity.AgentExecTarget, len(existing))
	for _, e := range existing {
		if e.SyncID != "" {
			bySyncID[e.SyncID] = e
		}
		byBackend[e.AgentBackendID] = append(byBackend[e.AgentBackendID], e)
	}

	claimed := make(map[int64]bool, len(existing))
	fresh := make([]*agent_entity.AgentExecTarget, 0, len(targets))
	// 先把留下来的行挪到一个不可能相撞的临时序号（-1、-2……）：(agent_id, sort_order)
	// 上有唯一索引，中途两行短暂同号就会撞上。
	type keep struct {
		row       *agent_entity.AgentExecTarget
		sortOrder int
		skills    string
		backendID int64
	}
	keeps := make([]keep, 0, len(targets))
	for i, t := range targets {
		var match *agent_entity.AgentExecTarget
		if t.SyncID != "" {
			if row, ok := bySyncID[t.SyncID]; ok && !claimed[row.ID] {
				match = row
			}
		}
		if match == nil {
			for _, row := range byBackend[t.AgentBackendID] {
				if !claimed[row.ID] {
					match = row
					break
				}
			}
		}
		if match == nil {
			fresh = append(fresh, &agent_entity.AgentExecTarget{
				AgentID: agentID, AgentBackendID: t.AgentBackendID,
				SortOrder: i, SkillsJSON: t.SkillsJSON, SyncMeta: t.SyncMeta,
			})
			continue
		}
		claimed[match.ID] = true
		keeps = append(keeps, keep{row: match, sortOrder: i, skills: t.SkillsJSON, backendID: t.AgentBackendID})
	}

	for _, e := range existing {
		if claimed[e.ID] {
			continue
		}
		if err := tx.Where("id = ?", e.ID).Delete(&agent_entity.AgentExecTarget{}).Error; err != nil {
			return err
		}
	}
	for i, k := range keeps {
		if err := tx.Model(&agent_entity.AgentExecTarget{}).Where("id = ?", k.row.ID).
			Update("sort_order", -(i + 1)).Error; err != nil {
			return err
		}
	}
	for _, k := range keeps {
		if err := tx.Model(&agent_entity.AgentExecTarget{}).Where("id = ?", k.row.ID).
			Updates(map[string]any{
				"agent_backend_id": k.backendID,
				"sort_order":       k.sortOrder,
				"skills_json":      k.skills,
			}).Error; err != nil {
			return err
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	for _, row := range fresh {
		row.EnsureSyncID()
	}
	return tx.Create(&fresh).Error
}

// insertExecTargets 按下标落 sort_order 插入执行目标行；空列表不发 INSERT。
// 只信 targets 里的 AgentBackendID / SkillsJSON，ID/AgentID/SortOrder 由这里落。
func insertExecTargets(tx *gorm.DB, agentID int64, targets []*agent_entity.AgentExecTarget) error {
	if len(targets) == 0 {
		return nil
	}
	rows := make([]*agent_entity.AgentExecTarget, 0, len(targets))
	for i, t := range targets {
		row := &agent_entity.AgentExecTarget{
			AgentID:        agentID,
			AgentBackendID: t.AgentBackendID,
			SortOrder:      i,
			SkillsJSON:     t.SkillsJSON,
		}
		// 同步标识在行创建时就地生成（R1/R12a）。只有真正新增的档才走到这里：
		// Replace 按差异更新，活下来的档保持原行、原标识（见 replaceExecTargets），
		// 所以这里生成的标识就是这一档终身的身份。
		row.EnsureSyncID()
		rows = append(rows, row)
	}
	return tx.Create(&rows).Error
}

// primaryTargetList 把「Agent 当前的那一个 backend + 它这一档的技能授权」表达成
// 执行目标列表：0 = 空列表。转换本身住在实体层，导入侧的老 bundle 回落共用同一份
// （R15f）。
func primaryTargetList(backendID int64, skillsJSON string) []*agent_entity.AgentExecTarget {
	return agent_entity.PrimaryExecTargets(backendID, skillsJSON)
}

// hydrateOne 补齐单个 Agent 的派生值；补不齐就不交出这个 Agent —— 交出去的话
// 调用方会拿着历史列的残值（或 0）当真，把有后端的 Agent 当成「未配置后端」。
func hydrateOne(ctx context.Context, a *agent_entity.Agent) (*agent_entity.Agent, error) {
	if err := hydrateExecTargets(ctx, []*agent_entity.Agent{a}); err != nil {
		return nil, err
	}
	return a, nil
}

// hydrateExecTargets 用执行目标行补齐一批 Agent 的 AgentBackendID —— 取 sort_order
// 最小的那一行，没有目标行则为 0。读取一律走这里，agents.agent_backend_id 历史列
// 不再被读。
func hydrateExecTargets(ctx context.Context, agents []*agent_entity.Agent) error {
	ids := make([]int64, 0, len(agents))
	for _, a := range agents {
		if a != nil && a.ID > 0 {
			ids = append(ids, a.ID)
		}
	}
	rows, err := listExecTargets(ctx, ids)
	if err != nil {
		return err
	}
	primary := make(map[int64]int64, len(rows))
	for _, row := range rows {
		if _, ok := primary[row.AgentID]; !ok {
			primary[row.AgentID] = row.AgentBackendID
		}
	}
	for _, a := range agents {
		if a != nil {
			a.AgentBackendID = primary[a.ID]
		}
	}
	return nil
}
