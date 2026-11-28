package app

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/service/agent_svc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/memory_svc"
)

// 记忆页的绑定。这里的每一次写入都是用户本人做的：写入者固定为 UserActor，不从前端
// 入参里取——AI 写入走会话通道，带着自己的 agent 与会话。

// MemoryCreateRequest 记忆页添加框：在某个归属下新增一条。
type MemoryCreateRequest struct {
	Owner   memory_entity.Owner `json:"owner"`
	Summary string              `json:"summary"`
	Detail  string              `json:"detail"`
}

// MemoryUpdateRequest 就地编辑摘要与详情。
type MemoryUpdateRequest struct {
	ID      int64  `json:"id"`
	Summary string `json:"summary"`
	Detail  string `json:"detail"`
}

// MemoryCreate 新增一条记忆；同层已有摘要完全相同的正常条目时返回那一条（existed=true）。
func (a *App) MemoryCreate(req *MemoryCreateRequest) (*memory_svc.CreateResult, error) {
	res, err := memory_svc.Memory().Create(a.ctx, &memory_svc.CreateRequest{
		Owner: req.Owner, Summary: req.Summary, Detail: req.Detail, Actor: memory_entity.UserActor(),
	})
	return res, codedError(err)
}

// MemoryUpdate 改摘要与详情，修改前的内容成为上一版。
func (a *App) MemoryUpdate(req *MemoryUpdateRequest) (*memory_svc.MemoryItem, error) {
	res, err := memory_svc.Memory().Update(a.ctx, &memory_svc.UpdateRequest{
		ID: req.ID, Summary: req.Summary, Detail: req.Detail, Actor: memory_entity.UserActor(),
	})
	return res, codedError(err)
}

// MemorySetPinned 置顶 / 取消置顶。
func (a *App) MemorySetPinned(id int64, pinned bool) (*memory_svc.MemoryItem, error) {
	res, err := memory_svc.Memory().SetPinned(a.ctx, id, pinned)
	return res, codedError(err)
}

// MemoryArchive 归档。
func (a *App) MemoryArchive(id int64) (*memory_svc.MemoryItem, error) {
	res, err := memory_svc.Memory().Archive(a.ctx, id, memory_entity.UserActor())
	return res, codedError(err)
}

// MemoryUnarchive 从归档恢复。
func (a *App) MemoryUnarchive(id int64) (*memory_svc.MemoryItem, error) {
	res, err := memory_svc.Memory().Unarchive(a.ctx, id, memory_entity.UserActor())
	return res, codedError(err)
}

// MemoryRestorePrevious 当前内容与上一版对调。
func (a *App) MemoryRestorePrevious(id int64) (*memory_svc.MemoryItem, error) {
	res, err := memory_svc.Memory().RestorePrevious(a.ctx, id, memory_entity.UserActor())
	return res, codedError(err)
}

// MemoryMove 移到其他层。
func (a *App) MemoryMove(id int64, owner memory_entity.Owner) (*memory_svc.MemoryItem, error) {
	res, err := memory_svc.Memory().Move(a.ctx, id, owner, memory_entity.UserActor())
	return res, codedError(err)
}

// MemoryDelete 彻底删除。
func (a *App) MemoryDelete(id int64) error {
	return codedError(memory_svc.Memory().Delete(a.ctx, id))
}

// MemoryGet 按编号读一条记忆（对话里的记忆卡片用）；不存在返回 null。
func (a *App) MemoryGet(id int64) (*memory_svc.MemoryItem, error) {
	res, err := memory_svc.Memory().Get(a.ctx, id)
	return res, codedError(err)
}

// MemoryUndoWrite 撤销对话里记忆卡片代表的那次 AI 写入：新增的彻底删除、修改的恢复上一版、
// 归档的恢复为正常。条目之后又被改过则不动它，结果 status 为 "changed"。
func (a *App) MemoryUndoWrite(req *memory_svc.UndoWriteRequest) (*memory_svc.UndoWriteResult, error) {
	res, err := memory_svc.Memory().UndoWrite(a.ctx, req)
	return res, codedError(err)
}

// MemoryListByOwner 某个归属的条目：本次带上、超出预算、已归档三组与预算用量。
func (a *App) MemoryListByOwner(owner memory_entity.Owner) (*memory_svc.OwnerList, error) {
	res, err := memory_svc.Memory().ListByOwner(a.ctx, owner)
	return res, codedError(err)
}

// MemoryCountsByOwner 记忆页左列：各归属的正常条目数。
func (a *App) MemoryCountsByOwner() ([]*memory_svc.OwnerCount, error) {
	res, err := memory_svc.Memory().CountsByOwner(a.ctx)
	return res, codedError(err)
}

// MemoryCountOwned 这些归属名下会随归属一并删掉的记忆条数（含已归档）：agent、部门、项目
// 的删除确认用它。
func (a *App) MemoryCountOwned(owners []memory_entity.Owner) (int64, error) {
	res, err := memory_svc.Memory().CountOwned(a.ctx, owners)
	return res, codedError(err)
}

// MemoryRecentChanges 近 14 天的最近变更。
func (a *App) MemoryRecentChanges() ([]*memory_svc.MemoryItem, error) {
	res, err := memory_svc.Memory().RecentChanges(a.ctx)
	return res, codedError(err)
}

// MemorySearch 按摘要与详情搜索所有层。
func (a *App) MemorySearch(keyword string) ([]*memory_svc.MemoryItem, error) {
	res, err := memory_svc.Memory().Search(a.ctx, keyword)
	return res, codedError(err)
}

// MemoryVisible 某 agent 在某项目（0 = 无项目）的会话可见的各层记忆；记忆页 agent 归属
// 的「继承的」一组按它的层计数。
func (a *App) MemoryVisible(agentID, projectID int64) (*memory_svc.VisibleMemory, error) {
	res, err := memory_svc.Memory().Visible(a.ctx, agentID, projectID)
	return res, codedError(err)
}

// MemoryRestrictedTargets 记忆页 agent 头部：这个 agent 的哪些执行目标不带记忆或只能读摘要，
// 以及原因。全部完整支持（或没有执行目标）时返回空列表。
func (a *App) MemoryRestrictedTargets(agentID int64) ([]memory_svc.RestrictedTarget, error) {
	advisor := memory_svc.NewTargetAdvisor(newMemoryTargetSource(), memoryTier)
	res, err := advisor.Restricted(a.ctx, agentID)
	return res, codedError(err)
}

// memoryTier 把 chat_svc.MemoryTier 接给 memory_svc：memory_svc 不能 import chat_svc
// （chat_svc 依赖 memory_svc），判档规则只有 chat_svc 这一份。
func memoryTier(caps capability.Capabilities, backendType string) string {
	return string(chat_svc.MemoryTier(caps, backendType))
}

// memoryTargetSource 是 memory_svc.TargetSource 的宿主实现：判档看成员生效的引擎配置
// （成员 › 部门链 › 账号默认）的运行时能力 —— 执行位置由派发决定，记忆带多少只看用什么
// 运行时，不再逐档看执行目标。
type memoryTargetSource struct {
	effective func(ctx context.Context, agentID int64) (*agent_backend_entity.EngineProfile, error)
	// caps 运行时类型的能力集；ok=false 表示本机没有这种运行时（不下结论）。
	caps func(runtimeType string) (capability.Capabilities, bool)
}

func newMemoryTargetSource() *memoryTargetSource {
	return &memoryTargetSource{
		effective: func(ctx context.Context, agentID int64) (*agent_backend_entity.EngineProfile, error) {
			eff, err := agent_svc.Agent().EffectiveEngine(ctx, agentID)
			if err != nil || eff == nil {
				return nil, err
			}
			return eff.Profile, nil
		},
		caps: func(runtimeType string) (capability.Capabilities, bool) {
			r := agentruntime.RuntimeFor(agent_backend_entity.BackendType(runtimeType))
			if r == nil {
				return capability.Capabilities{}, false
			}
			return r.Capabilities(), true
		},
	}
}

func (s *memoryTargetSource) ExecTargets(ctx context.Context, agentID int64) ([]memory_svc.ExecTarget, error) {
	p, err := s.effective(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	caps, known := s.caps(p.Type)
	if !known {
		return nil, nil
	}
	return []memory_svc.ExecTarget{{
		BackendID: p.ID, BackendName: p.Name, BackendType: p.Type,
		Caps: caps, TypeSupportsMemory: caps.Has(capability.CapMemory),
	}}, nil
}
