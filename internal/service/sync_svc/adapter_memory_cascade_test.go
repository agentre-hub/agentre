package sync_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo/mock_memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo/mock_syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo/mock_syncstate_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// 同步落地的墓碑删掉 agent / 部门 / 项目时，名下的记忆在本机一并删掉，并给每一条已经
// 在账号上的记忆排一条出站墓碑（记忆是硬删，只能凭删前读到的行）。
func TestRemoveOwner_DeletesItsMemoriesAndQueuesTombstonesForTheOnesOnTheAccount(t *testing.T) {
	cases := []struct {
		name   string
		kind   string
		owner  memory_entity.Owner
		remove func(ctx context.Context, in *inbound) error
		expect func(ctrl *gomock.Controller)
	}{
		{
			name: "agent", kind: syncwire.KindAgent,
			owner:  memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: 5},
			remove: (&agentAdapter{}).remove,
			expect: func(ctrl *gomock.Controller) {
				m := mock_agent_repo.NewMockAgentRepo(ctrl)
				m.EXPECT().Delete(gomock.Any(), int64(5)).Return(nil)
				agent_repo.RegisterAgent(m)
			},
		},
		{
			name: "department", kind: syncwire.KindDepartment,
			owner:  memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 5},
			remove: departmentAdapter{}.remove,
			expect: func(ctrl *gomock.Controller) {
				m := mock_department_repo.NewMockDepartmentRepo(ctrl)
				m.EXPECT().Delete(gomock.Any(), int64(5)).Return(nil)
				department_repo.RegisterDepartment(m)
			},
		},
		{
			name: "project", kind: syncwire.KindProject,
			owner:  memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 5},
			remove: projectAdapter{}.remove,
			expect: func(ctrl *gomock.Controller) {
				m := mock_project_repo.NewMockProjectRepo(ctrl)
				m.EXPECT().Delete(gomock.Any(), int64(5)).Return(nil)
				project_repo.RegisterProject(m)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
			state.EXPECT().FindLocalID(gomock.Any(), tc.kind, "owner-sync").Return(int64(5), nil).AnyTimes()
			syncstate_repo.RegisterSyncState(state)
			tc.expect(ctrl)

			onAccount := &memory_entity.Memory{ID: 31, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "m-31", SyncAccountID: 7, SyncVersion: 4}}
			neverSynced := &memory_entity.Memory{ID: 32, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "m-32"}}
			mems := mock_memory_repo.NewMockMemoryRepo(ctrl)
			gomock.InOrder(
				mems.EXPECT().ListByOwners(gomock.Any(), []memory_entity.Owner{tc.owner}).Return([]*memory_entity.Memory{onAccount, neverSynced}, nil),
				mems.EXPECT().DeleteByOwners(gomock.Any(), []memory_entity.Owner{tc.owner}).Return(nil),
			)
			memory_repo.RegisterMemory(mems)

			var queued []*syncqueue_entity.OutboundQueueItem
			queue := mock_syncqueue_repo.NewMockOutboundQueueRepo(ctrl)
			queue.EXPECT().CreateMany(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, rows []*syncqueue_entity.OutboundQueueItem) error {
					queued = rows
					return nil
				})
			syncqueue_repo.RegisterOutboundQueue(queue)

			require.NoError(t, tc.remove(context.Background(), &inbound{Kind: tc.kind, SyncID: "owner-sync"}))

			require.Len(t, queued, 1, "没上过账号的记忆没有副本，不欠墓碑")
			assert.Equal(t, int64(7), queued[0].SyncAccountID)
			assert.Equal(t, syncwire.KindMemory, queued[0].EntityType)
			assert.Equal(t, "m-31", queued[0].EntitySyncID)
			assert.Equal(t, OpDelete, queued[0].Op)
			assert.Equal(t, int64(4), queued[0].BaseVersion)
			assert.Equal(t, int64(31), queued[0].LocalID)
			assert.NotZero(t, queued[0].QueuedAt)
		})
	}
}

func TestRemoveOwner_GivenNoMemories_QueuesNothing(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindLocalID(gomock.Any(), syncwire.KindProject, "owner-sync").Return(int64(5), nil)
	syncstate_repo.RegisterSyncState(state)
	projects := mock_project_repo.NewMockProjectRepo(ctrl)
	projects.EXPECT().Delete(gomock.Any(), int64(5)).Return(nil)
	project_repo.RegisterProject(projects)
	mems := mock_memory_repo.NewMockMemoryRepo(ctrl)
	mems.EXPECT().ListByOwners(gomock.Any(), gomock.Any()).Return(nil, nil)
	memory_repo.RegisterMemory(mems)
	syncqueue_repo.RegisterOutboundQueue(mock_syncqueue_repo.NewMockOutboundQueueRepo(ctrl))

	require.NoError(t, projectAdapter{}.remove(context.Background(), &inbound{Kind: syncwire.KindProject, SyncID: "owner-sync"}))
}

func TestRemoveOwner_GivenOwnerRowAlreadyGone_TouchesNoMemories(t *testing.T) {
	ctrl := gomock.NewController(t)
	state := mock_syncstate_repo.NewMockSyncStateRepo(ctrl)
	state.EXPECT().FindLocalID(gomock.Any(), syncwire.KindProject, "owner-sync").Return(int64(0), nil)
	syncstate_repo.RegisterSyncState(state)
	memory_repo.RegisterMemory(mock_memory_repo.NewMockMemoryRepo(ctrl))

	require.NoError(t, projectAdapter{}.remove(context.Background(), &inbound{Kind: syncwire.KindProject, SyncID: "owner-sync"}))
}
