package memory_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// DeleteOwned 删掉这些归属名下的全部记忆（含已归档），供 agent / 部门 / 项目的删除在
// 自己的事务里调用：记忆与归属同生共死，要么一起删、要么一起回滚。
//
// 记忆是硬删，删后读不到同步元数据，所以先读出来再删；返回的墓碑由调用方在事务
// **提交之后**逐条交给 sync_svc.Notify。
func DeleteOwned(ctx context.Context, owners ...memory_entity.Owner) ([]sync_svc.LocalChange, error) {
	rows, err := memory_repo.Memory().ListByOwners(ctx, owners)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	if err := memory_repo.Memory().DeleteByOwners(ctx, owners); err != nil {
		return nil, err
	}
	return memoryChanges(rows, sync_svc.OpDelete), nil
}

// MoveOwned 把 from 层的全部记忆改挂到同类的 to 层（项目合并），返回提交后要发的
// 更新通知：载荷里的归属标识变了，对端靠它把条目挂到保留的项目上。
func MoveOwned(ctx context.Context, from, to memory_entity.Owner) ([]sync_svc.LocalChange, error) {
	rows, err := memory_repo.Memory().ListByOwners(ctx, []memory_entity.Owner{from})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	if err := memory_repo.Memory().ReassignOwner(ctx, from, to); err != nil {
		return nil, err
	}
	return memoryChanges(rows, sync_svc.OpUpdate), nil
}

func memoryChanges(rows []*memory_entity.Memory, op string) []sync_svc.LocalChange {
	out := make([]sync_svc.LocalChange, 0, len(rows))
	for _, m := range rows {
		out = append(out, sync_svc.LocalChange{Kind: syncwire.KindMemory, LocalID: m.ID, Op: op, Meta: m.SyncMeta})
	}
	return out
}
