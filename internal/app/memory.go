package app

import (
	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
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
