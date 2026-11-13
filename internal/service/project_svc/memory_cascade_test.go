package project_svc_test

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo/mock_memory_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// noMemories 是本包测试的默认记忆仓储：谁都没有记忆。删 / 合并项目会顺带处理名下的
// 记忆，与记忆无关的用例不必各自摆桩；要断言记忆的用例自己 registerMemoryMock。
type noMemories struct{ memory_repo.MemoryRepo }

func (noMemories) ListByOwners(context.Context, []memory_entity.Owner) ([]*memory_entity.Memory, error) {
	return nil, nil
}

func TestMain(m *testing.M) {
	memory_repo.RegisterMemory(noMemories{})
	os.Exit(m.Run())
}

func registerMemoryMock(t *testing.T) *mock_memory_repo.MockMemoryRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := mock_memory_repo.NewMockMemoryRepo(ctrl)
	memory_repo.RegisterMemory(m)
	t.Cleanup(func() { memory_repo.RegisterMemory(noMemories{}) })
	return m
}

func memoryRow(id int64, syncID string) *memory_entity.Memory {
	return &memory_entity.Memory{ID: id, SyncMeta: syncmeta_entity.SyncMeta{SyncID: syncID, SyncVersion: 2}}
}

func memoryChangesOf(changes []sync_svc.LocalChange) []string {
	var out []string
	for _, ch := range changes {
		if ch.Kind == syncwire.KindMemory {
			out = append(out, ch.Op+"/"+ch.Meta.SyncID)
		}
	}
	return out
}

func projectOwner(id int64) memory_entity.Owner {
	return memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: id}
}

func TestProjectSvcDelete_GivenSubtree_ThenEveryProjectsMemoriesGoInTheTxAndAreTombstonedAfterCommit(t *testing.T) {
	ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), nil)
	f.tx.txCtx = context.WithValue(ctx, txMarkerKey{}, "delete-tx")
	rec := registerTxAwareSync(t, f.tx)
	mem := registerMemoryMock(t)
	for _, id := range []int64{1, 2, 3, 4} {
		owners := []memory_entity.Owner{projectOwner(id)}
		rows := []*memory_entity.Memory{memoryRow(100+id, "m-"+string(rune('0'+id)))}
		mem.EXPECT().ListByOwners(gomock.Any(), owners).Return(rows, nil)
		mem.EXPECT().DeleteByOwners(gomock.Any(), owners).Return(nil)
	}

	require.NoError(t, svc.Delete(ctx, 1))

	var tombstones []string
	for _, n := range rec.seen {
		if n.change.Kind != syncwire.KindMemory {
			continue
		}
		assert.True(t, n.afterCommit, "记忆墓碑在提交之前就发出了")
		assert.False(t, n.withTxCtx)
		assert.Equal(t, sync_svc.OpDelete, n.change.Op)
		tombstones = append(tombstones, n.change.Meta.SyncID)
	}
	assert.ElementsMatch(t, []string{"m-1", "m-2", "m-3", "m-4"}, tombstones)
}

func TestProjectSvcDeleteLeaf_DeletesThatProjectsMemoriesOnly(t *testing.T) {
	ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), nil)
	rec := registerRecordingSync(t)
	mem := registerMemoryMock(t)
	owners := []memory_entity.Owner{projectOwner(3)}
	mem.EXPECT().ListByOwners(gomock.Any(), owners).Return([]*memory_entity.Memory{memoryRow(7, "m-7")}, nil)
	mem.EXPECT().DeleteByOwners(gomock.Any(), owners).Return(nil)

	require.NoError(t, svc.DeleteLeaf(ctx, 3))

	assert.Equal(t, []string{"delete/m-7"}, memoryChangesOf(rec.changes))
	assert.True(t, f.tx.committed)
}

func TestProjectSvcDelete_GivenMemoryDeleteFails_ThenNothingIsAnnounced(t *testing.T) {
	ctx, f, svc := setupDeleteSubtreeTest(t, twoLevelTree(), nil)
	rec := registerRecordingSync(t)
	mem := registerMemoryMock(t)
	mem.EXPECT().ListByOwners(gomock.Any(), gomock.Any()).Return([]*memory_entity.Memory{memoryRow(7, "m-7")}, nil).AnyTimes()
	mem.EXPECT().DeleteByOwners(gomock.Any(), gomock.Any()).Return(assert.AnError)

	err := svc.Delete(ctx, 1)

	assert.ErrorIs(t, err, assert.AnError)
	assert.False(t, f.tx.committed)
	assert.Empty(t, rec.changes)
}

func TestProjectSvcMerge_MovesTheDroppedProjectsMemoriesToTheKeptOneAndAnnouncesThem(t *testing.T) {
	ctx, m, svc := setupMergeTest(t)
	rec := registerTxAwareSync(t, m.tx)
	mem := registerMemoryMock(t)

	older := &project_entity.Project{ID: 5, Name: "older", Createtime: 100, Path: "/p1"}
	newer := &project_entity.Project{ID: 6, Name: "newer", Createtime: 200, Path: "/p2"}
	m.project.EXPECT().Find(ctx, int64(6)).Return(newer, nil)
	m.project.EXPECT().Find(ctx, int64(5)).Return(older, nil)
	m.project.EXPECT().Update(ctx, gomock.Any()).Return(nil)
	m.session.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	m.pa.EXPECT().ListByProject(ctx, gomock.Any()).Return(nil, nil).Times(2)
	m.project.EXPECT().ListByParent(ctx, int64(6)).Return(nil, nil)
	m.project.EXPECT().ReassignParent(ctx, int64(6), int64(5)).Return(nil)
	m.issue.EXPECT().List(ctx, gomock.Any()).Return(nil, nil).AnyTimes()
	m.issue.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	m.location.EXPECT().ListByProject(ctx, int64(6)).Return(nil, nil)
	m.location.EXPECT().ReassignProject(ctx, int64(6), int64(5)).Return(nil)
	expectCleanDelete(ctx, m, 6, newer)

	drop := []memory_entity.Owner{projectOwner(6)}
	gomock.InOrder(
		mem.EXPECT().ListByOwners(ctx, drop).Return([]*memory_entity.Memory{memoryRow(41, "m-41")}, nil),
		mem.EXPECT().ReassignOwner(ctx, projectOwner(6), projectOwner(5)).Return(nil),
		// 搬完之后删被并项目时它名下已经没有记忆。
		mem.EXPECT().ListByOwners(ctx, drop).Return(nil, nil),
	)

	_, err := svc.Merge(ctx, &project_svc.MergeProjectsRequest{SourceID: 6, TargetID: 5})
	require.NoError(t, err)

	require.Equal(t, []string{"update/m-41"}, memoryChangesOf(changesOf(rec)))
	for _, n := range rec.seen {
		if n.change.Kind == syncwire.KindMemory {
			assert.True(t, n.afterCommit, "搬迁的同步通知要在提交之后发")
		}
	}
}

func changesOf(rec *txAwareSync) []sync_svc.LocalChange {
	out := make([]sync_svc.LocalChange, 0, len(rec.seen))
	for _, n := range rec.seen {
		out = append(out, n.change)
	}
	return out
}
