// Package memory_repo 提供 Agent 记忆的持久化访问。
package memory_repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cago-frame/cago/database/db"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/repository/repoquery"
)

//go:generate mockgen -source memory.go -destination mock_memory_repo/mock_memory.go

// OwnerCount 某个归属下的正常条目数。
type OwnerCount struct {
	Owner memory_entity.Owner
	Count int64
}

// budgetOrder 与 memory_entity.SplitByBudget 同序：置顶在前，其余按最近修改。
const budgetOrder = "pinned DESC, changed_at DESC, id DESC"

// MemoryRepo 记忆仓储接口。没有软删：彻底删除就是删行，归档是 state 列。
type MemoryRepo interface {
	Create(ctx context.Context, m *memory_entity.Memory) error
	Update(ctx context.Context, m *memory_entity.Memory) error
	Find(ctx context.Context, id int64) (*memory_entity.Memory, error)
	// FindNormalBySummary 同一层里摘要完全相同的正常条目（决策 18 的去重）。
	FindNormalBySummary(ctx context.Context, owner memory_entity.Owner, summary string) (*memory_entity.Memory, error)
	// ListByOwner 某一层的全部条目（含已归档），按预算序。
	ListByOwner(ctx context.Context, owner memory_entity.Owner) ([]*memory_entity.Memory, error)
	// ListNormalByOwners 若干层的正常条目，按预算序；owners 为空时不查库。
	ListNormalByOwners(ctx context.Context, owners []memory_entity.Owner) ([]*memory_entity.Memory, error)
	CountNormalByOwner(ctx context.Context) ([]*OwnerCount, error)
	// ListChangedSince 最近一次变更不早于 since（毫秒 epoch）的条目，新的在前。
	ListChangedSince(ctx context.Context, since int64) ([]*memory_entity.Memory, error)
	// Search 摘要或详情包含 keyword 的条目（所有层、所有状态），按归属聚在一起。
	Search(ctx context.Context, keyword string) ([]*memory_entity.Memory, error)
	Delete(ctx context.Context, id int64) error
}

var defaultMemory MemoryRepo

// Memory 取默认仓储单例。
func Memory() MemoryRepo { return defaultMemory }

// RegisterMemory 注入仓储实现，由 bootstrap 调用一次。
func RegisterMemory(impl MemoryRepo) { defaultMemory = impl }

// NewMemory 构造 GORM 实现。
func NewMemory() MemoryRepo { return &memoryRepo{} }

type memoryRepo struct{}

func (r *memoryRepo) Create(ctx context.Context, m *memory_entity.Memory) error {
	now := time.Now().UnixMilli()
	if m.Createtime == 0 {
		m.Createtime = now
	}
	m.Updatetime = now
	// 同步标识在行创建时就地生成，未登录期间也照常写入。
	m.EnsureSyncID()
	return db.Ctx(ctx).Create(m).Error
}

func (r *memoryRepo) Update(ctx context.Context, m *memory_entity.Memory) error {
	m.Updatetime = time.Now().UnixMilli()
	m.EnsureSyncID()
	return db.Ctx(ctx).Model(&memory_entity.Memory{}).
		Where("id = ?", m.ID).
		Updates(map[string]any{
			"owner_kind":        m.OwnerKind,
			"owner_id":          m.OwnerID,
			"summary":           m.Summary,
			"detail":            m.Detail,
			"prev_summary":      m.PrevSummary,
			"prev_detail":       m.PrevDetail,
			"pinned":            m.Pinned,
			"state":             m.State,
			"change_action":     m.ChangeAction,
			"change_actor_kind": m.ChangeActorKind,
			"change_agent_id":   m.ChangeAgentID,
			"change_session_id": m.ChangeSessionID,
			"changed_at":        m.ChangedAt,
			"sync_id":           m.SyncID,
			"updatetime":        m.Updatetime,
		}).Error
}

func (r *memoryRepo) Find(ctx context.Context, id int64) (*memory_entity.Memory, error) {
	return first(db.Ctx(ctx).Where("id = ?", id))
}

func (r *memoryRepo) FindNormalBySummary(ctx context.Context, owner memory_entity.Owner, summary string) (*memory_entity.Memory, error) {
	return first(db.Ctx(ctx).
		Where("owner_kind = ? AND owner_id = ? AND summary = ? AND state = ?",
			owner.Kind, owner.ID, summary, memory_entity.StateNormal))
}

func first(q *gorm.DB) (*memory_entity.Memory, error) {
	out := &memory_entity.Memory{}
	err := q.First(out).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *memoryRepo) ListByOwner(ctx context.Context, owner memory_entity.Owner) ([]*memory_entity.Memory, error) {
	var rows []*memory_entity.Memory
	err := db.Ctx(ctx).
		Where("owner_kind = ? AND owner_id = ?", owner.Kind, owner.ID).
		Order(budgetOrder).
		Find(&rows).Error
	return rows, err
}

func (r *memoryRepo) ListNormalByOwners(ctx context.Context, owners []memory_entity.Owner) ([]*memory_entity.Memory, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	clauses := make([]string, 0, len(owners))
	args := make([]any, 0, 2*len(owners))
	for _, o := range owners {
		clauses = append(clauses, "(owner_kind = ? AND owner_id = ?)")
		args = append(args, o.Kind, o.ID)
	}
	var rows []*memory_entity.Memory
	err := db.Ctx(ctx).
		Where("state = ?", memory_entity.StateNormal).
		Where(strings.Join(clauses, " OR "), args...).
		Order(budgetOrder).
		Find(&rows).Error
	return rows, err
}

func (r *memoryRepo) CountNormalByOwner(ctx context.Context) ([]*OwnerCount, error) {
	var rows []struct {
		OwnerKind memory_entity.OwnerKind
		OwnerID   int64
		Count     int64
	}
	err := db.Ctx(ctx).Model(&memory_entity.Memory{}).
		Select("owner_kind, owner_id, COUNT(*) AS count").
		Where("state = ?", memory_entity.StateNormal).
		Group("owner_kind, owner_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]*OwnerCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, &OwnerCount{Owner: memory_entity.Owner{Kind: row.OwnerKind, ID: row.OwnerID}, Count: row.Count})
	}
	return out, nil
}

func (r *memoryRepo) ListChangedSince(ctx context.Context, since int64) ([]*memory_entity.Memory, error) {
	var rows []*memory_entity.Memory
	err := db.Ctx(ctx).
		Where("changed_at >= ?", since).
		Order("changed_at DESC, id DESC").
		Find(&rows).Error
	return rows, err
}

func (r *memoryRepo) Search(ctx context.Context, keyword string) ([]*memory_entity.Memory, error) {
	like := "%" + repoquery.EscapeLike(keyword) + "%"
	var rows []*memory_entity.Memory
	err := db.Ctx(ctx).
		Where(`(summary LIKE ? ESCAPE '\' OR detail LIKE ? ESCAPE '\')`, like, like).
		Order("owner_kind, owner_id, " + budgetOrder).
		Find(&rows).Error
	return rows, err
}

func (r *memoryRepo) Delete(ctx context.Context, id int64) error {
	return db.Ctx(ctx).Delete(&memory_entity.Memory{ID: id}).Error
}
