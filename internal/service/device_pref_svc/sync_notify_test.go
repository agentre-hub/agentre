package device_pref_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type recordingSync struct {
	sync_svc.SyncSvc
	changes []sync_svc.LocalChange
}

func (r *recordingSync) NotifyLocalChange(_ context.Context, ch sync_svc.LocalChange) {
	r.changes = append(r.changes, ch)
}

func recordSync(t *testing.T) *recordingSync {
	t.Helper()
	rec := &recordingSync{}
	sync_svc.SetDefault(rec)
	t.Cleanup(func() { sync_svc.SetDefault(nil) })
	return rec
}

func (r *recordingSync) find(kind, op, syncID string) bool {
	for _, c := range r.changes {
		if c.Kind == kind && c.Op == op && c.Meta.SyncID == syncID {
			return true
		}
	}
	return false
}

func syncedPref(fp devicefp.Carrier, order int, syncID string) *device_pref_entity.DevicePref {
	p := pref(fp, order, `[]`)
	p.SyncMeta = syncmeta_entity.SyncMeta{SyncID: syncID}
	return p
}

func TestDevicePrefSvc_NotifiesSync(t *testing.T) {
	t.Run("labels on an existing row queue a device_pref update", func(t *testing.T) {
		h := setup(t, boxA)
		rec := recordSync(t)
		h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{syncedPref(self, 0, "dp-self"), syncedPref(boxA, 1, "dp-a")}, nil)
		h.prefs.EXPECT().SetLabels(h.ctx, boxA, `["gpu"]`).Return(nil)
		require.NoError(t, h.svc.UpdateLabels(h.ctx, boxA, []string{"gpu"}))
		require.Len(t, rec.changes, 1)
		assert.True(t, rec.find(syncwire.KindDevicePref, sync_svc.OpUpdate, "dp-a"))
	})

	t.Run("labels on a device without a row queue the created row", func(t *testing.T) {
		h := setup(t, boxA)
		rec := recordSync(t)
		h.prefs.EXPECT().List(h.ctx).Return(nil, nil)
		h.prefs.EXPECT().Create(h.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, p *device_pref_entity.DevicePref) error {
			p.ID, p.SyncID = 8, "dp-new"
			return nil
		})
		require.NoError(t, h.svc.UpdateLabels(h.ctx, boxA, []string{"gpu"}))
		assert.True(t, rec.find(syncwire.KindDevicePref, sync_svc.OpCreate, "dp-new"))
	})

	t.Run("reorder queues only the rows whose position moved, and created rows", func(t *testing.T) {
		h := setup(t, boxA)
		rec := recordSync(t)
		// self 0, boxA 1 -> new order boxA, self: both move; a third unchanged case below.
		h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{syncedPref(self, 0, "dp-self"), syncedPref(boxA, 1, "dp-a")}, nil)
		h.prefs.EXPECT().SetSortOrder(h.ctx, gomock.Any(), gomock.Any()).Return(nil).Times(2)
		require.NoError(t, h.svc.Reorder(h.ctx, []devicefp.Carrier{boxA, self}))
		assert.True(t, rec.find(syncwire.KindDevicePref, sync_svc.OpUpdate, "dp-self"))
		assert.True(t, rec.find(syncwire.KindDevicePref, sync_svc.OpUpdate, "dp-a"))
	})

	t.Run("reorder to the same order queues nothing", func(t *testing.T) {
		h := setup(t, boxA)
		rec := recordSync(t)
		h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{syncedPref(self, 0, "dp-self"), syncedPref(boxA, 1, "dp-a")}, nil)
		h.prefs.EXPECT().SetSortOrder(h.ctx, gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
		require.NoError(t, h.svc.Reorder(h.ctx, []devicefp.Carrier{self, boxA}))
		assert.Empty(t, rec.changes)
	})

	t.Run("failed reorder queues nothing", func(t *testing.T) {
		h := setup(t, boxA)
		rec := recordSync(t)
		h.prefs.EXPECT().List(h.ctx).Return([]*device_pref_entity.DevicePref{syncedPref(self, 0, "dp-self"), syncedPref(boxA, 1, "dp-a")}, nil)
		h.prefs.EXPECT().SetSortOrder(h.ctx, gomock.Any(), gomock.Any()).Return(errors.New("boom"))
		require.Error(t, h.svc.Reorder(h.ctx, []devicefp.Carrier{boxA, self}))
		assert.Empty(t, rec.changes)
	})
}

func TestDevicePrefSvc_CLIPathNotifiesSync(t *testing.T) {
	t.Run("new override queues a create", func(t *testing.T) {
		h := setup(t)
		rec := recordSync(t)
		h.cli.EXPECT().Find(h.ctx, "codex", self).Return(nil, nil)
		h.cli.EXPECT().Create(h.ctx, gomock.Any()).DoAndReturn(func(_ context.Context, o *device_pref_entity.CLIPathOverride) error {
			o.ID, o.SyncID = 4, "cp-4"
			return nil
		})
		require.NoError(t, h.svc.SetCLIPath(h.ctx, "codex", "", "/bin/codex"))
		assert.True(t, rec.find(syncwire.KindDeviceCLIPath, sync_svc.OpCreate, "cp-4"))
	})
	t.Run("changed path queues an update", func(t *testing.T) {
		h := setup(t)
		rec := recordSync(t)
		row := &device_pref_entity.CLIPathOverride{ID: 4, RuntimeType: "codex", DeviceFingerprint: self, CLIPath: "/a", Status: consts.ACTIVE,
			SyncMeta: syncmeta_entity.SyncMeta{SyncID: "cp-4"}}
		h.cli.EXPECT().Find(h.ctx, "codex", self).Return(row, nil)
		h.cli.EXPECT().UpdatePath(h.ctx, int64(4), "/b").Return(nil)
		require.NoError(t, h.svc.SetCLIPath(h.ctx, "codex", "", "/b"))
		assert.True(t, rec.find(syncwire.KindDeviceCLIPath, sync_svc.OpUpdate, "cp-4"))
	})
	t.Run("clearing the path queues a tombstone", func(t *testing.T) {
		h := setup(t)
		rec := recordSync(t)
		row := &device_pref_entity.CLIPathOverride{ID: 4, RuntimeType: "codex", DeviceFingerprint: self, CLIPath: "/a", Status: consts.ACTIVE,
			SyncMeta: syncmeta_entity.SyncMeta{SyncID: "cp-4"}}
		h.cli.EXPECT().Find(h.ctx, "codex", self).Return(row, nil)
		h.cli.EXPECT().Delete(h.ctx, int64(4)).Return(nil)
		require.NoError(t, h.svc.SetCLIPath(h.ctx, "codex", "", ""))
		assert.True(t, rec.find(syncwire.KindDeviceCLIPath, sync_svc.OpDelete, "cp-4"))
	})
	t.Run("clearing an absent override queues nothing", func(t *testing.T) {
		h := setup(t)
		rec := recordSync(t)
		h.cli.EXPECT().Find(h.ctx, "codex", self).Return(nil, nil)
		require.NoError(t, h.svc.SetCLIPath(h.ctx, "codex", "", ""))
		assert.Empty(t, rec.changes)
	})
}
