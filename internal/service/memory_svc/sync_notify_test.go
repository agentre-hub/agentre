package memory_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// recordingSync 记下域服务在改动落库成功之后交给同步层的每一条 LocalChange。
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

func syncedMemory(id int64, owner memory_entity.Owner, summary string) *memory_entity.Memory {
	m := stored(id, owner, summary)
	m.SyncMeta = syncmeta_entity.SyncMeta{SyncID: "mem-sync", SyncVersion: 4}
	return m
}

func TestMemorySvc_Create_NotifiesSyncOnce(t *testing.T) {
	ctx, m, svc := setupMemorySvc(t)
	rec := registerRecordingSync(t)
	m.memory.EXPECT().FindNormalBySummary(ctx, memory_entity.GlobalOwner(), "s").Return(nil, nil)
	m.memory.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, e *memory_entity.Memory) error {
		e.ID, e.SyncID = 11, "mem-sync"
		return nil
	})

	_, err := svc.Create(ctx, &CreateRequest{Owner: memory_entity.GlobalOwner(), Summary: "s", Actor: memory_entity.UserActor()})
	require.NoError(t, err)

	require.Len(t, rec.changes, 1)
	assert.Equal(t, syncwire.KindMemory, rec.changes[0].Kind)
	assert.Equal(t, sync_svc.OpCreate, rec.changes[0].Op)
	assert.Equal(t, int64(11), rec.changes[0].LocalID)
	assert.Equal(t, "mem-sync", rec.changes[0].Meta.SyncID)
}

// 同层已有同摘要条目：什么都没写，也就没有什么要同步。
func TestMemorySvc_Create_ExistingEntryDoesNotNotify(t *testing.T) {
	ctx, m, svc := setupMemorySvc(t)
	rec := registerRecordingSync(t)
	m.memory.EXPECT().FindNormalBySummary(ctx, memory_entity.GlobalOwner(), "s").Return(syncedMemory(4, memory_entity.GlobalOwner(), "s"), nil)

	_, err := svc.Create(ctx, &CreateRequest{Owner: memory_entity.GlobalOwner(), Summary: "s", Actor: memory_entity.UserActor()})
	require.NoError(t, err)
	assert.Empty(t, rec.changes)
}

// 每一种改动路径落库后各通知一次，且带着落库前读出的版本作基版本。
func TestMemorySvc_Mutations_NotifySyncWithTheRowsSyncMeta(t *testing.T) {
	user := memory_entity.UserActor()
	cases := map[string]func(svc *memorySvc, ctx context.Context) error{
		"update": func(svc *memorySvc, ctx context.Context) error {
			_, err := svc.Update(ctx, &UpdateRequest{ID: 4, Summary: "new", Actor: user})
			return err
		},
		"pin": func(svc *memorySvc, ctx context.Context) error {
			_, err := svc.SetPinned(ctx, 4, true)
			return err
		},
		"archive": func(svc *memorySvc, ctx context.Context) error {
			_, err := svc.Archive(ctx, 4, user)
			return err
		},
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, m, svc := setupMemorySvc(t)
			rec := registerRecordingSync(t)
			m.memory.EXPECT().Find(ctx, int64(4)).Return(syncedMemory(4, memory_entity.GlobalOwner(), "old"), nil)
			m.memory.EXPECT().Update(ctx, gomock.Any()).Return(nil)

			require.NoError(t, run(svc, ctx))

			require.Len(t, rec.changes, 1)
			assert.Equal(t, syncwire.KindMemory, rec.changes[0].Kind)
			assert.Equal(t, sync_svc.OpUpdate, rec.changes[0].Op)
			assert.Equal(t, int64(4), rec.changes[0].LocalID)
			assert.Equal(t, "mem-sync", rec.changes[0].Meta.SyncID)
			assert.Equal(t, int64(4), rec.changes[0].Meta.SyncVersion)
		})
	}
}

func TestMemorySvc_Mutation_NoChangeOrFailedWriteDoesNotNotify(t *testing.T) {
	ctx, m, svc := setupMemorySvc(t)
	rec := registerRecordingSync(t)
	pinned := syncedMemory(4, memory_entity.GlobalOwner(), "old")
	pinned.Pinned = true
	m.memory.EXPECT().Find(ctx, int64(4)).Return(pinned, nil)
	_, err := svc.SetPinned(ctx, 4, true) // 本来就置顶：不写库
	require.NoError(t, err)

	m.memory.EXPECT().Find(ctx, int64(5)).Return(syncedMemory(5, memory_entity.GlobalOwner(), "old"), nil)
	m.memory.EXPECT().Update(ctx, gomock.Any()).Return(assert.AnError)
	_, err = svc.SetPinned(ctx, 5, true)
	require.Error(t, err)

	assert.Empty(t, rec.changes)
}

func TestMemorySvc_Delete_NotifiesATombstone(t *testing.T) {
	ctx, m, svc := setupMemorySvc(t)
	rec := registerRecordingSync(t)
	m.memory.EXPECT().Find(ctx, int64(4)).Return(syncedMemory(4, memory_entity.GlobalOwner(), "old"), nil)
	m.memory.EXPECT().Delete(ctx, int64(4)).Return(nil)

	require.NoError(t, svc.Delete(ctx, 4))

	require.Len(t, rec.changes, 1)
	assert.Equal(t, sync_svc.OpDelete, rec.changes[0].Op)
	assert.Equal(t, syncwire.KindMemory, rec.changes[0].Kind)
	assert.Equal(t, "mem-sync", rec.changes[0].Meta.SyncID)
	assert.Equal(t, int64(4), rec.changes[0].Meta.SyncVersion)
}
