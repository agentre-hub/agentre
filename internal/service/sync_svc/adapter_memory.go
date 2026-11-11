package sync_svc

import (
	"context"
	"encoding/json"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// ── 记忆 ────────────────────────────────────────────────────────────────────

// memoryAdapter 同步 Agent 记忆条目。归属（部门 / agent / 项目）跨机用同步标识表达，
// 全局层没有引用。记忆是硬删：彻底删除就是墓碑，归档只是 state 列。
type memoryAdapter struct{ baseAdapter }

func (memoryAdapter) kind() string { return syncwire.KindMemory }

func (memoryAdapter) load(ctx context.Context, syncID string) (*outbound, error) {
	row := &memory_entity.Memory{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindMemory, syncID, row)
	if err != nil || !found {
		return nil, err
	}
	ownerSyncID, err := memoryOwnerSyncID(ctx, row.Owner())
	if err != nil {
		return nil, err
	}
	// 非全局层的归属在本机表达不出跨机引用（行不在、或还没有同步标识）就一条都不发：
	// 发出去是一条别的设备落不了库的孤儿；归属拿到标识后会随认领重发。
	if row.OwnerKind != memory_entity.OwnerGlobal && ownerSyncID == "" {
		return nil, nil
	}
	payload, err := json.Marshal(syncwire.MemoryPayload{
		OwnerKind:       string(row.OwnerKind),
		OwnerSyncID:     ownerSyncID,
		Summary:         row.Summary,
		Detail:          row.Detail,
		PrevSummary:     row.PrevSummary,
		PrevDetail:      row.PrevDetail,
		Pinned:          row.Pinned,
		State:           string(row.State),
		CreatorKind:     string(row.CreatorKind),
		ChangeAction:    string(row.ChangeAction),
		ChangeActorKind: string(row.ChangeActorKind),
		ChangedAt:       row.ChangedAt,
	})
	if err != nil {
		return nil, err
	}
	return &outbound{SyncID: row.SyncID, UpdatedAt: row.Updatetime, Payload: payload}, nil
}

// memoryOwnerSyncID 把本机归属翻成跨机引用；全局层与查不到的归属返回空串。
func memoryOwnerSyncID(ctx context.Context, owner memory_entity.Owner) (string, error) {
	switch owner.Kind {
	case memory_entity.OwnerAgent:
		return agentSyncIDOfLocalID(ctx, owner.ID)
	case memory_entity.OwnerDepartment:
		dept, err := department_repo.Department().Find(ctx, owner.ID)
		if err != nil || dept == nil {
			return "", err
		}
		return syncIDOf(dept.SyncMeta), nil
	case memory_entity.OwnerProject:
		project, err := project_repo.Project().Find(ctx, owner.ID)
		if err != nil || project == nil {
			return "", err
		}
		return syncIDOf(project.SyncMeta), nil
	default:
		return "", nil
	}
}

// memoryOwnerRef 一份载荷的归属引用；全局层（或不认识的层）没有引用。
func memoryOwnerRef(p syncwire.MemoryPayload) ref {
	switch memory_entity.OwnerKind(p.OwnerKind) {
	case memory_entity.OwnerAgent:
		return ref{Kind: syncwire.KindAgent, SyncID: p.OwnerSyncID}
	case memory_entity.OwnerDepartment:
		return ref{Kind: syncwire.KindDepartment, SyncID: p.OwnerSyncID}
	case memory_entity.OwnerProject:
		return ref{Kind: syncwire.KindProject, SyncID: p.OwnerSyncID}
	default:
		return ref{}
	}
}

// refs 归属是阻塞引用（R2a）：它在本机还不存在就暂缓落地，不写悬空归属。
func (memoryAdapter) refs(in *inbound) []ref {
	var p syncwire.MemoryPayload
	_ = json.Unmarshal(in.Payload, &p)
	if r := memoryOwnerRef(p); !r.empty() {
		return []ref{r}
	}
	return nil
}

func (memoryAdapter) apply(ctx context.Context, in *inbound, resolved map[string]int64) error {
	var p syncwire.MemoryPayload
	if err := json.Unmarshal(in.Payload, &p); err != nil {
		return err
	}
	owner := memory_entity.Owner{Kind: memory_entity.OwnerKind(p.OwnerKind)}
	if owner.Kind != memory_entity.OwnerGlobal {
		owner.ID = resolvedID(resolved, memoryOwnerRef(p))
		if owner.ID == 0 {
			return errRefMissing
		}
	}
	row := &memory_entity.Memory{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindMemory, in.SyncID, row)
	if err != nil {
		return err
	}
	row.OwnerKind, row.OwnerID = owner.Kind, owner.ID
	row.Summary, row.Detail = p.Summary, p.Detail
	row.PrevSummary, row.PrevDetail = p.PrevSummary, p.PrevDetail
	row.Pinned, row.State = p.Pinned, memory_entity.State(p.State)
	row.CreatorKind = memory_entity.ActorKind(p.CreatorKind)
	row.ChangeAction = memory_entity.ChangeAction(p.ChangeAction)
	row.ChangeActorKind = memory_entity.ActorKind(p.ChangeActorKind)
	row.ChangedAt = p.ChangedAt
	if !found {
		// 会话号与 agent 本地 ID 只在产生它的那台机器上有意义，落在这里是 0。
		row.SyncID = in.SyncID
		return memory_repo.Memory().Create(ctx, row)
	}
	return memory_repo.Memory().Update(ctx, row)
}

func (memoryAdapter) remove(ctx context.Context, in *inbound) error {
	row := &memory_entity.Memory{}
	found, err := syncstate_repo.SyncState().FindRow(ctx, syncwire.KindMemory, in.SyncID, row)
	if err != nil || !found {
		return err
	}
	return memory_repo.Memory().Delete(ctx, row.ID)
}
