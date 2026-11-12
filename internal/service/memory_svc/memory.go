// Package memory_svc 提供 Agent 记忆的应用服务：四层归属的读写、上一版、归档、移层、
// 预算取舍，以及一个会话可见的记忆合成（Visible）。
//
// 写入方法都带 memory_entity.Actor：记忆页走 UserActor，会话里的 AI 写入走
// AIActor(agentID, sessionID)，创建者与最近变更据此记下。记忆的摘要与详情不进日志。
package memory_svc

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// MemorySvc Agent 记忆应用服务。
type MemorySvc interface {
	// Create 新增；同层已有摘要完全相同的正常条目时返回那一条（Existed=true）。
	Create(ctx context.Context, req *CreateRequest) (*CreateResult, error)
	// Update 改摘要与详情，修改前的内容成为上一版。
	Update(ctx context.Context, req *UpdateRequest) (*MemoryItem, error)
	SetPinned(ctx context.Context, id int64, pinned bool) (*MemoryItem, error)
	Archive(ctx context.Context, id int64, actor memory_entity.Actor) (*MemoryItem, error)
	Unarchive(ctx context.Context, id int64, actor memory_entity.Actor) (*MemoryItem, error)
	// RestorePrevious 当前内容与上一版对调。
	RestorePrevious(ctx context.Context, id int64, actor memory_entity.Actor) (*MemoryItem, error)
	Move(ctx context.Context, id int64, owner memory_entity.Owner, actor memory_entity.Actor) (*MemoryItem, error)
	// Delete 彻底删除（只有用户能做；AI 的删除是 Archive）。
	Delete(ctx context.Context, id int64) error
	// Get 按编号读一条；不存在返回 nil。
	Get(ctx context.Context, id int64) (*MemoryItem, error)
	// UndoWrite 撤销对话卡片代表的那次 AI 写入；条目之后被改过则拒绝（Status=changed）。
	UndoWrite(ctx context.Context, req *UndoWriteRequest) (*UndoWriteResult, error)
	ListByOwner(ctx context.Context, owner memory_entity.Owner) (*OwnerList, error)
	CountsByOwner(ctx context.Context) ([]*OwnerCount, error)
	// RecentChanges 近 14 天内被新增、修改、归档、恢复或移层过的条目，新的在前。
	RecentChanges(ctx context.Context) ([]*MemoryItem, error)
	Search(ctx context.Context, keyword string) ([]*MemoryItem, error)
	// Visible 一个 agent 在某项目（0 = 无项目）里的会话看得到的记忆，逐层按预算分好。
	Visible(ctx context.Context, agentID, projectID int64) (*VisibleMemory, error)
	// SessionOwners AI 写入者所属（可见、可写）的层；项目取自它的会话。
	SessionOwners(ctx context.Context, actor memory_entity.Actor) ([]memory_entity.Owner, error)
	// SessionVisible AI 写入者所在会话看得到的记忆（agrctl 记忆命令的读）。
	SessionVisible(ctx context.Context, actor memory_entity.Actor) (*VisibleMemory, error)
}

type memorySvc struct {
	now func() int64
	// createMu 让「查同层重复 → 新增」在本进程内串行：同一 agent 的多个会话并行时
	// 最容易同时记下同一件事（决策 18）。
	createMu sync.Mutex
}

var defaultMemory MemorySvc = New()

// Memory 取默认服务单例。
func Memory() MemorySvc { return defaultMemory }

// SetDefault 注入服务实现（测试 / bootstrap 装配用）。
func SetDefault(svc MemorySvc) { defaultMemory = svc }

// New 构造默认实现。
func New() MemorySvc { return newMemorySvc(func() int64 { return time.Now().UnixMilli() }) }

func newMemorySvc(now func() int64) *memorySvc { return &memorySvc{now: now} }

func (s *memorySvc) Create(ctx context.Context, req *CreateRequest) (*CreateResult, error) {
	if err := checkActor(ctx, req.Actor); err != nil {
		return nil, err
	}
	summary, detail := memory_entity.NormalizeContent(req.Summary, req.Detail)
	if err := memory_entity.CheckContent(ctx, summary, detail); err != nil {
		return nil, err
	}
	if err := s.requireOwnLayer(ctx, req.Actor, req.Owner); err != nil {
		return nil, err
	}
	if err := s.requireOwner(ctx, req.Owner); err != nil {
		return nil, err
	}

	s.createMu.Lock()
	defer s.createMu.Unlock()
	existing, err := memory_repo.Memory().FindNormalBySummary(ctx, req.Owner, summary)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return &CreateResult{Item: toItem(existing), Existed: true}, nil
	}
	m := memory_entity.NewMemory(req.Owner, summary, detail, req.Actor, s.now())
	if err := memory_repo.Memory().Create(ctx, m); err != nil {
		return nil, err
	}
	sync_svc.NotifyCreate(ctx, syncwire.KindMemory, m.ID, m.SyncMeta)
	logWrite(ctx, m, string(memory_entity.ActionCreated))
	return &CreateResult{Item: toItem(m)}, nil
}

func (s *memorySvc) Update(ctx context.Context, req *UpdateRequest) (*MemoryItem, error) {
	if err := checkActor(ctx, req.Actor); err != nil {
		return nil, err
	}
	summary, detail := memory_entity.NormalizeContent(req.Summary, req.Detail)
	if err := memory_entity.CheckContent(ctx, summary, detail); err != nil {
		return nil, err
	}
	return s.mutateAs(ctx, req.ID, req.Actor, string(memory_entity.ActionEdited), func(m *memory_entity.Memory) (bool, error) {
		return m.Edit(summary, detail, req.Actor, s.now()), nil
	})
}

func (s *memorySvc) SetPinned(ctx context.Context, id int64, pinned bool) (*MemoryItem, error) {
	return s.mutate(ctx, id, "pinned", func(m *memory_entity.Memory) (bool, error) {
		if m.Pinned == pinned {
			return false, nil
		}
		m.Pinned = pinned
		return true, nil
	})
}

func (s *memorySvc) Archive(ctx context.Context, id int64, actor memory_entity.Actor) (*MemoryItem, error) {
	if err := checkActor(ctx, actor); err != nil {
		return nil, err
	}
	return s.mutateAs(ctx, id, actor, string(memory_entity.ActionArchived), func(m *memory_entity.Memory) (bool, error) {
		if m.IsArchived() {
			return false, nil
		}
		m.Archive(actor, s.now())
		return true, nil
	})
}

func (s *memorySvc) Unarchive(ctx context.Context, id int64, actor memory_entity.Actor) (*MemoryItem, error) {
	if err := checkActor(ctx, actor); err != nil {
		return nil, err
	}
	return s.mutateAs(ctx, id, actor, string(memory_entity.ActionUnarchived), func(m *memory_entity.Memory) (bool, error) {
		if !m.IsArchived() {
			return false, nil
		}
		m.Unarchive(actor, s.now())
		return true, nil
	})
}

func (s *memorySvc) RestorePrevious(ctx context.Context, id int64, actor memory_entity.Actor) (*MemoryItem, error) {
	if err := checkActor(ctx, actor); err != nil {
		return nil, err
	}
	return s.mutateAs(ctx, id, actor, string(memory_entity.ActionRestored), func(m *memory_entity.Memory) (bool, error) {
		if !m.RestorePrevious(actor, s.now()) {
			return false, i18n.NewError(ctx, code.MemoryNoPrevious)
		}
		return true, nil
	})
}

func (s *memorySvc) Move(ctx context.Context, id int64, owner memory_entity.Owner, actor memory_entity.Actor) (*MemoryItem, error) {
	if err := checkActor(ctx, actor); err != nil {
		return nil, err
	}
	return s.mutateAs(ctx, id, actor, string(memory_entity.ActionMoved), func(m *memory_entity.Memory) (bool, error) {
		if m.Owner() == owner {
			return false, nil
		}
		if err := s.requireOwnLayer(ctx, actor, owner); err != nil {
			return false, err
		}
		if err := s.requireOwner(ctx, owner); err != nil {
			return false, err
		}
		m.MoveTo(owner, actor, s.now())
		return true, nil
	})
}

func (s *memorySvc) Delete(ctx context.Context, id int64) error {
	m, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	if err := memory_repo.Memory().Delete(ctx, m.ID); err != nil {
		return err
	}
	sync_svc.NotifyDelete(ctx, syncwire.KindMemory, m.ID, m.SyncMeta)
	logWrite(ctx, m, "deleted")
	return nil
}

// mutateAs 同 mutate，先确认条目在 actor 能写的层里（AI 只能写它所属的层）。
func (s *memorySvc) mutateAs(ctx context.Context, id int64, actor memory_entity.Actor, op string, apply func(*memory_entity.Memory) (bool, error)) (*MemoryItem, error) {
	return s.mutate(ctx, id, op, func(m *memory_entity.Memory) (bool, error) {
		if err := s.requireOwnLayer(ctx, actor, m.Owner()); err != nil {
			return false, err
		}
		return apply(m)
	})
}

// mutate 读出条目、交给 apply 改，有改动才落库并记一条 op 日志。apply 返回的错误原样返回。
func (s *memorySvc) mutate(ctx context.Context, id int64, op string, apply func(*memory_entity.Memory) (bool, error)) (*MemoryItem, error) {
	m, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	changed, err := apply(m)
	if err != nil {
		return nil, err
	}
	if changed {
		if err := memory_repo.Memory().Update(ctx, m); err != nil {
			return nil, err
		}
		sync_svc.NotifyUpdate(ctx, syncwire.KindMemory, m.ID, m.SyncMeta)
		logWrite(ctx, m, op)
	}
	return toItem(m), nil
}

func (s *memorySvc) find(ctx context.Context, id int64) (*memory_entity.Memory, error) {
	m, err := memory_repo.Memory().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, i18n.NewError(ctx, code.MemoryNotFound)
	}
	return m, nil
}

func (s *memorySvc) ListByOwner(ctx context.Context, owner memory_entity.Owner) (*OwnerList, error) {
	out := &OwnerList{Owner: owner, Budget: memory_entity.LayerBudgetChars}
	if owner.Kind == memory_entity.OwnerAgent && owner.Valid() {
		// agent 归属顺带读出它的记忆开关，存在性也由这一次查询判定。
		a, err := agent_repo.Agent().Find(ctx, owner.ID)
		if err != nil {
			return nil, err
		}
		if a == nil {
			return nil, i18n.NewError(ctx, code.MemoryOwnerNotFound)
		}
		out.MemoryEnabled = a.MemoryEnabled
	} else if err := s.requireOwner(ctx, owner); err != nil {
		return nil, err
	}
	rows, err := memory_repo.Memory().ListByOwner(ctx, owner)
	if err != nil {
		return nil, err
	}
	normal := make([]*memory_entity.Memory, 0, len(rows))
	archived := make([]*memory_entity.Memory, 0)
	for _, m := range rows {
		if m.IsArchived() {
			archived = append(archived, m)
			continue
		}
		normal = append(normal, m)
		out.UsedChars += m.SummaryChars()
	}
	carried, over := memory_entity.SplitByBudget(normal, memory_entity.LayerBudgetChars)
	out.Carried, out.OverBudget, out.Archived = toItems(carried), toItems(over), toItems(archived)
	return out, nil
}

func (s *memorySvc) CountsByOwner(ctx context.Context) ([]*OwnerCount, error) {
	rows, err := memory_repo.Memory().CountNormalByOwner(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*OwnerCount, 0, len(rows))
	for _, r := range rows {
		out = append(out, &OwnerCount{Owner: r.Owner, Count: r.Count})
	}
	return out, nil
}

func (s *memorySvc) RecentChanges(ctx context.Context) ([]*MemoryItem, error) {
	rows, err := memory_repo.Memory().ListChangedSince(ctx, s.now()-memory_entity.RecentChangeWindowMillis)
	if err != nil {
		return nil, err
	}
	return toItems(rows), nil
}

func (s *memorySvc) Search(ctx context.Context, keyword string) ([]*MemoryItem, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []*MemoryItem{}, nil
	}
	rows, err := memory_repo.Memory().Search(ctx, keyword)
	if err != nil {
		return nil, err
	}
	return toItems(rows), nil
}

// requireOwner 归属形状合法且（全局以外）对应的部门 / agent / 项目存在。
func (s *memorySvc) requireOwner(ctx context.Context, owner memory_entity.Owner) error {
	if !owner.Valid() {
		return i18n.NewError(ctx, code.MemoryInvalidOwner)
	}
	var exists bool
	switch owner.Kind {
	case memory_entity.OwnerGlobal:
		return nil
	case memory_entity.OwnerDepartment:
		d, err := department_repo.Department().Find(ctx, owner.ID)
		if err != nil {
			return err
		}
		exists = d != nil
	case memory_entity.OwnerAgent:
		a, err := agent_repo.Agent().Find(ctx, owner.ID)
		if err != nil {
			return err
		}
		exists = a != nil
	case memory_entity.OwnerProject:
		p, err := project_repo.Project().Find(ctx, owner.ID)
		if err != nil {
			return err
		}
		exists = p != nil
	}
	if !exists {
		return i18n.NewError(ctx, code.MemoryOwnerNotFound)
	}
	return nil
}

func checkActor(ctx context.Context, actor memory_entity.Actor) error {
	if !actor.Valid() {
		return i18n.NewError(ctx, code.MemoryInvalidActor)
	}
	return nil
}

// logWrite 记一条写入里程碑：只有标识与动作，不含摘要与详情（记忆内容不进日志）。
// 写入者取最近一次变更；置顶与删除不是记录在案的变更，写入者一栏照旧是上一次的。
func logWrite(ctx context.Context, m *memory_entity.Memory, op string) {
	change := m.LastChange()
	logger.Ctx(ctx).Info("memory_svc.write: memory written",
		zap.Int64("memoryId", m.ID),
		zap.String("ownerKind", string(m.OwnerKind)),
		zap.Int64("ownerId", m.OwnerID),
		zap.String("op", op),
		zap.String("lastActorKind", string(change.Actor.Kind)),
		zap.Int64("lastSessionId", change.Actor.SessionID))
}
