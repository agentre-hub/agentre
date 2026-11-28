package agent_backend_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// notifyRecorder 记下域服务交给同步层的每一条本地改动。
type notifyRecorder struct {
	sync_svc.SyncSvc
	changes []sync_svc.LocalChange
}

func (r *notifyRecorder) NotifyLocalChange(_ context.Context, ch sync_svc.LocalChange) {
	r.changes = append(r.changes, ch)
}

func recordSync(t *testing.T) *notifyRecorder {
	t.Helper()
	rec := &notifyRecorder{}
	sync_svc.SetDefault(rec)
	t.Cleanup(func() { sync_svc.SetDefault(nil) })
	return rec
}

func (r *notifyRecorder) has(kind, op, syncID string) bool {
	for _, c := range r.changes {
		if c.Kind == kind && c.Op == op && c.Meta.SyncID == syncID {
			return true
		}
	}
	return false
}

func syncedProfile(id int64, typ string, isDefault bool, syncID string) *agent_backend_entity.EngineProfile {
	p := activeProfile(id, typ, isDefault)
	p.SyncMeta = syncmeta_entity.SyncMeta{SyncID: syncID}
	return p
}

func TestEngineProfileSvc_NotifiesSync(t *testing.T) {
	t.Run("create queues an engine_profile create", func(t *testing.T) {
		ctx, m, svc := setupEngineProfileTest(t)
		rec := recordSync(t)
		m.profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(nil, nil)
		m.profiles.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p *agent_backend_entity.EngineProfile) error {
			p.ID, p.SyncID = 5, "ep-5"
			return nil
		})
		_, err := svc.Create(ctx, &CreateEngineProfileRequest{Type: "claudecode"})
		require.NoError(t, err)
		require.Len(t, rec.changes, 1)
		assert.True(t, rec.has(syncwire.KindEngineProfile, sync_svc.OpCreate, "ep-5"))
		assert.Equal(t, int64(5), rec.changes[0].LocalID)
	})

	t.Run("update queues an engine_profile update", func(t *testing.T) {
		ctx, m, svc := setupEngineProfileTest(t)
		rec := recordSync(t)
		m.profiles.EXPECT().Find(gomock.Any(), int64(4)).Return(syncedProfile(4, "codex", false, "ep-4"), nil)
		m.profiles.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
		_, err := svc.Update(ctx, &UpdateEngineProfileRequest{ID: 4, Name: "x"})
		require.NoError(t, err)
		assert.True(t, rec.has(syncwire.KindEngineProfile, sync_svc.OpUpdate, "ep-4"))
	})

	t.Run("delete queues the tombstone and the members and departments that fell back", func(t *testing.T) {
		ctx, m, svc := setupEngineProfileTest(t)
		rec := recordSync(t)
		m.profiles.EXPECT().Find(gomock.Any(), int64(4)).Return(syncedProfile(4, "codex", false, "ep-4"), nil)
		m.agents.EXPECT().ListByEngineProfile(gomock.Any(), int64(4)).Return([]*agent_entity.Agent{
			{ID: 11, Name: "Eva", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "ag-11"}}}, nil)
		m.depts.EXPECT().ListByDefaultEngineProfile(gomock.Any(), int64(4)).Return([]*department_entity.Department{
			{ID: 7, Name: "eng", SyncMeta: syncmeta_entity.SyncMeta{SyncID: "dp-7"}}}, nil)
		m.agents.EXPECT().ClearEngineProfile(gomock.Any(), int64(4)).Return(nil)
		m.depts.EXPECT().ClearDefaultEngineProfile(gomock.Any(), int64(4)).Return(nil)
		m.profiles.EXPECT().Delete(gomock.Any(), int64(4)).Return(nil)
		_, err := svc.Delete(ctx, &DeleteEngineProfileRequest{ID: 4})
		require.NoError(t, err)
		assert.True(t, rec.has(syncwire.KindEngineProfile, sync_svc.OpDelete, "ep-4"))
		assert.True(t, rec.has(syncwire.KindAgent, sync_svc.OpUpdate, "ag-11"))
		assert.True(t, rec.has(syncwire.KindDepartment, sync_svc.OpUpdate, "dp-7"))
	})

	t.Run("failed delete queues nothing", func(t *testing.T) {
		ctx, m, svc := setupEngineProfileTest(t)
		rec := recordSync(t)
		m.profiles.EXPECT().Find(gomock.Any(), int64(4)).Return(syncedProfile(4, "codex", false, "ep-4"), nil)
		m.agents.EXPECT().ListByEngineProfile(gomock.Any(), int64(4)).Return(nil, nil)
		m.depts.EXPECT().ListByDefaultEngineProfile(gomock.Any(), int64(4)).Return(nil, nil)
		m.agents.EXPECT().ClearEngineProfile(gomock.Any(), int64(4)).Return(assert.AnError)
		_, err := svc.Delete(ctx, &DeleteEngineProfileRequest{ID: 4})
		require.Error(t, err)
		assert.Empty(t, rec.changes)
	})

	t.Run("changing the account default queues both the new and the previous default", func(t *testing.T) {
		ctx, m, svc := setupEngineProfileTest(t)
		rec := recordSync(t)
		m.profiles.EXPECT().Find(gomock.Any(), int64(4)).Return(syncedProfile(4, "codex", false, "ep-4"), nil)
		m.profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(syncedProfile(1, "claudecode", true, "ep-1"), nil)
		m.profiles.EXPECT().SetAccountDefault(gomock.Any(), int64(4)).Return(nil)
		_, err := svc.SetAccountDefault(ctx, &SetAccountDefaultEngineProfileRequest{ID: 4})
		require.NoError(t, err)
		assert.True(t, rec.has(syncwire.KindEngineProfile, sync_svc.OpUpdate, "ep-4"))
		assert.True(t, rec.has(syncwire.KindEngineProfile, sync_svc.OpUpdate, "ep-1"))
	})

	t.Run("re-selecting the current default queues it once", func(t *testing.T) {
		ctx, m, svc := setupEngineProfileTest(t)
		rec := recordSync(t)
		m.profiles.EXPECT().Find(gomock.Any(), int64(1)).Return(syncedProfile(1, "claudecode", true, "ep-1"), nil)
		m.profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(syncedProfile(1, "claudecode", true, "ep-1"), nil)
		m.profiles.EXPECT().SetAccountDefault(gomock.Any(), int64(1)).Return(nil)
		_, err := svc.SetAccountDefault(ctx, &SetAccountDefaultEngineProfileRequest{ID: 1})
		require.NoError(t, err)
		assert.Len(t, rec.changes, 1)
	})
}
