package department_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo/mock_memory_repo"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

type recordingSync struct {
	sync_svc.SyncSvc
	changes []sync_svc.LocalChange
}

func (r *recordingSync) NotifyLocalChange(_ context.Context, ch sync_svc.LocalChange) {
	r.changes = append(r.changes, ch)
}

func registerRecordingSync(t *testing.T) *recordingSync {
	t.Helper()
	rec := &recordingSync{}
	sync_svc.SetDefault(rec)
	t.Cleanup(func() { sync_svc.SetDefault(nil) })
	return rec
}

func registerMemoryMock(t *testing.T) *mock_memory_repo.MockMemoryRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := mock_memory_repo.NewMockMemoryRepo(ctrl)
	memory_repo.RegisterMemory(m)
	return m
}

func memoryTombstoneIDs(rec *recordingSync) []string {
	var out []string
	for _, ch := range rec.changes {
		if ch.Kind == syncwire.KindMemory {
			out = append(out, ch.Op+"/"+ch.Meta.SyncID)
		}
	}
	return out
}

func TestDeleteDepartment_Reparent_DeletesOnlyTheDepartmentsOwnMemories(t *testing.T) {
	ctx, deptMock, agentMock, svc := setupSvc(t)
	mem := registerMemoryMock(t)
	rec := registerRecordingSync(t)
	owners := []memory_entity.Owner{{Kind: memory_entity.OwnerDepartment, ID: 3}}
	row := &memory_entity.Memory{ID: 31, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "m-31"}}

	deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(&department_entity.Department{ID: 3, ParentID: 1, Status: 1}, nil)
	deptMock.EXPECT().ReparentChildren(gomock.Any(), int64(3), int64(1)).Return(nil)
	agentMock.EXPECT().ListByDepartment(gomock.Any(), int64(3)).Return(nil, nil)
	mem.EXPECT().ListByOwners(gomock.Any(), owners).Return([]*memory_entity.Memory{row}, nil)
	mem.EXPECT().DeleteByOwners(gomock.Any(), owners).Return(nil)
	deptMock.EXPECT().Delete(gomock.Any(), int64(3)).Return(nil)

	_, err := svc.Delete(ctx, &DeleteDepartmentRequest{ID: 3, Strategy: StrategyReparent})

	require.NoError(t, err)
	assert.Equal(t, []string{"delete/m-31"}, memoryTombstoneIDs(rec))
}

func TestDeleteDepartment_Cascade_DeletesMemoriesOfEveryDepartmentAndAgentRemoved(t *testing.T) {
	ctx, deptMock, agentMock, svc := setupSvc(t)
	mem := registerMemoryMock(t)
	rec := registerRecordingSync(t)
	owners := []memory_entity.Owner{
		{Kind: memory_entity.OwnerAgent, ID: 7}, {Kind: memory_entity.OwnerAgent, ID: 8},
		{Kind: memory_entity.OwnerDepartment, ID: 3}, {Kind: memory_entity.OwnerDepartment, ID: 4},
	}
	rows := []*memory_entity.Memory{
		{ID: 1, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "m-1"}},
		{ID: 2, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "m-2"}},
	}

	deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(&department_entity.Department{ID: 3, ParentID: 1, Status: 1}, nil)
	deptMock.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{{ID: 1}, {ID: 3, ParentID: 1}, {ID: 4, ParentID: 3}}, nil)
	agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
		{ID: 7, DepartmentID: 3}, {ID: 8, DepartmentID: 4}, {ID: 9, DepartmentID: 1},
	}, nil)
	agentMock.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	deptMock.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).Times(2)
	mem.EXPECT().ListByOwners(gomock.Any(), owners).Return(rows, nil)
	mem.EXPECT().DeleteByOwners(gomock.Any(), owners).Return(nil)

	_, err := svc.Delete(ctx, &DeleteDepartmentRequest{ID: 3, Strategy: StrategyCascade})

	require.NoError(t, err)
	assert.Equal(t, []string{"delete/m-1", "delete/m-2"}, memoryTombstoneIDs(rec))
}

func TestDeleteDepartment_GivenDeleteFails_EmitsNoMemoryTombstones(t *testing.T) {
	ctx, deptMock, agentMock, svc := setupSvc(t)
	mem := registerMemoryMock(t)
	rec := registerRecordingSync(t)

	deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(&department_entity.Department{ID: 3, ParentID: 1, Status: 1}, nil)
	deptMock.EXPECT().ReparentChildren(gomock.Any(), int64(3), int64(1)).Return(nil)
	agentMock.EXPECT().ListByDepartment(gomock.Any(), int64(3)).Return(nil, nil)
	mem.EXPECT().ListByOwners(gomock.Any(), gomock.Any()).Return([]*memory_entity.Memory{{ID: 1}}, nil)
	mem.EXPECT().DeleteByOwners(gomock.Any(), gomock.Any()).Return(nil)
	deptMock.EXPECT().Delete(gomock.Any(), int64(3)).Return(errors.New("db down"))

	_, err := svc.Delete(ctx, &DeleteDepartmentRequest{ID: 3, Strategy: StrategyReparent})

	assert.Error(t, err)
	assert.Empty(t, rec.changes)
}
