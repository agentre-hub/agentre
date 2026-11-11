package agent_svc

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo/mock_memory_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

func registerMemoryMock(t *testing.T) *mock_memory_repo.MockMemoryRepo {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := mock_memory_repo.NewMockMemoryRepo(ctrl)
	memory_repo.RegisterMemory(m)
	return m
}

func TestDeleteAgent_DeletesItsMemoriesInTheTxAndTombstonesThemAfterCommit(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	mem := registerMemoryMock(t)
	rec := registerRecordingSync(t)
	owner := []memory_entity.Owner{{Kind: memory_entity.OwnerAgent, ID: 5}}
	row := &memory_entity.Memory{ID: 31, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "m-31", SyncVersion: 2}}

	agentMock.EXPECT().Find(gomock.Any(), int64(5)).Return(&agent_entity.Agent{ID: 5, DepartmentID: 2}, nil)
	agentMock.EXPECT().ClearLeadOfDepartment(gomock.Any(), int64(5)).Return(nil)
	agentMock.EXPECT().ReparentChildren(gomock.Any(), int64(5), int64(2), int64(0)).Return(nil)
	mem.EXPECT().ListByOwners(gomock.Any(), owner).Return([]*memory_entity.Memory{row}, nil)
	mem.EXPECT().DeleteByOwners(gomock.Any(), owner).Return(nil)
	agentMock.EXPECT().Delete(gomock.Any(), int64(5)).Return(nil)

	_, err := svc.Delete(ctx, &DeleteAgentRequest{ID: 5})
	require.NoError(t, err)

	var tombstones []string
	for _, ch := range rec.changes {
		if ch.Kind == syncwire.KindMemory {
			tombstones = append(tombstones, ch.Op+"/"+ch.Meta.SyncID)
		}
	}
	assert.Equal(t, []string{"delete/m-31"}, tombstones)
}

func TestDeleteAgent_GivenAgentRowFails_EmitsNoMemoryTombstones(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	mem := registerMemoryMock(t)
	rec := registerRecordingSync(t)
	row := &memory_entity.Memory{ID: 31, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "m-31"}}

	agentMock.EXPECT().Find(gomock.Any(), int64(5)).Return(&agent_entity.Agent{ID: 5, DepartmentID: 2}, nil)
	agentMock.EXPECT().ClearLeadOfDepartment(gomock.Any(), int64(5)).Return(nil)
	agentMock.EXPECT().ReparentChildren(gomock.Any(), int64(5), int64(2), int64(0)).Return(nil)
	mem.EXPECT().ListByOwners(gomock.Any(), gomock.Any()).Return([]*memory_entity.Memory{row}, nil)
	mem.EXPECT().DeleteByOwners(gomock.Any(), gomock.Any()).Return(nil)
	agentMock.EXPECT().Delete(gomock.Any(), int64(5)).Return(errors.New("db down"))

	_, err := svc.Delete(ctx, &DeleteAgentRequest{ID: 5})

	assert.Error(t, err)
	assert.Empty(t, rec.changes, "事务回滚时墓碑不能发出去")
}
