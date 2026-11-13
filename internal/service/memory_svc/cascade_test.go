package memory_svc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

func ownedRow(id int64, owner memory_entity.Owner, syncID string) *memory_entity.Memory {
	m := stored(id, owner, "s")
	m.SyncMeta = syncmeta_entity.SyncMeta{SyncID: syncID, SyncVersion: 3}
	return m
}

func TestDeleteOwned_DeletesRowsAndReturnsTombstonesForEach(t *testing.T) {
	ctx, m, _ := setupMemorySvc(t)
	agent := memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: 5}
	rows := []*memory_entity.Memory{ownedRow(1, agent, "m-1"), ownedRow(2, agent, "m-2")}
	gomock.InOrder(
		m.memory.EXPECT().ListByOwners(ctx, []memory_entity.Owner{agent}).Return(rows, nil),
		m.memory.EXPECT().DeleteByOwners(ctx, []memory_entity.Owner{agent}).Return(nil),
	)

	got, err := DeleteOwned(ctx, agent)
	require.NoError(t, err)
	require.Len(t, got, 2)
	for i, ch := range got {
		assert.Equal(t, syncwire.KindMemory, ch.Kind)
		assert.Equal(t, sync_svc.OpDelete, ch.Op)
		assert.Equal(t, rows[i].ID, ch.LocalID)
		assert.Equal(t, rows[i].SyncMeta, ch.Meta, "墓碑带着删前读到的同步元数据")
	}
}

func TestDeleteOwned_NothingOwned_DoesNotDelete(t *testing.T) {
	ctx, m, _ := setupMemorySvc(t)
	agent := memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: 5}
	m.memory.EXPECT().ListByOwners(ctx, []memory_entity.Owner{agent}).Return(nil, nil)

	got, err := DeleteOwned(ctx, agent)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestDeleteOwned_ListFails_DeletesNothing(t *testing.T) {
	ctx, m, _ := setupMemorySvc(t)
	agent := memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: 5}
	m.memory.EXPECT().ListByOwners(ctx, gomock.Any()).Return(nil, assert.AnError)

	_, err := DeleteOwned(ctx, agent)
	assert.ErrorIs(t, err, assert.AnError)
}

func TestMoveOwned_ReassignsAndReturnsUpdatesForEach(t *testing.T) {
	ctx, m, _ := setupMemorySvc(t)
	from := memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 2}
	to := memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 8}
	rows := []*memory_entity.Memory{ownedRow(1, from, "m-1")}
	gomock.InOrder(
		m.memory.EXPECT().ListByOwners(ctx, []memory_entity.Owner{from}).Return(rows, nil),
		m.memory.EXPECT().ReassignOwner(ctx, from, to).Return(nil),
	)

	got, err := MoveOwned(ctx, from, to)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, sync_svc.OpUpdate, got[0].Op)
	assert.Equal(t, syncwire.KindMemory, got[0].Kind)
	assert.Equal(t, int64(1), got[0].LocalID)
}

func TestMoveOwned_NothingOwned_DoesNotReassign(t *testing.T) {
	ctx, m, _ := setupMemorySvc(t)
	from := memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 2}
	m.memory.EXPECT().ListByOwners(ctx, gomock.Any()).Return(nil, nil)

	got, err := MoveOwned(ctx, from, memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 8})
	require.NoError(t, err)
	assert.Empty(t, got)
}
