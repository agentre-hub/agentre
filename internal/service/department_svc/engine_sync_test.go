package department_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

type engineSyncRecorder struct {
	sync_svc.SyncSvc
	changes []sync_svc.LocalChange
}

func (r *engineSyncRecorder) NotifyLocalChange(_ context.Context, ch sync_svc.LocalChange) {
	r.changes = append(r.changes, ch)
}

func TestSetDepartmentEngine_NotifiesSync(t *testing.T) {
	t.Run("a saved default engine queues a department update", func(t *testing.T) {
		ctx, deptMock, profiles, svc := setupEngineTest(t)
		rec := &engineSyncRecorder{}
		sync_svc.SetDefault(rec)
		t.Cleanup(func() { sync_svc.SetDefault(nil) })
		d := dept(3, 1, 0, "eng")
		d.SyncMeta = syncmeta_entity.SyncMeta{SyncID: "dp-3"}
		deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(d, nil)
		deptMock.EXPECT().Find(gomock.Any(), int64(1)).Return(dept(1, 0, 0, "root"), nil)
		profiles.EXPECT().Find(gomock.Any(), int64(9)).Return(profile(9, "Codex", "codex"), nil).AnyTimes()
		profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(profile(1, "Default", "claudecode"), nil).AnyTimes()
		deptMock.EXPECT().UpdateDefaultEngineProfile(gomock.Any(), int64(3), int64(9)).Return(nil)

		_, err := svc.SetDepartmentEngine(ctx, &SetDepartmentEngineRequest{DepartmentID: 3, EngineProfileID: 9})
		require.NoError(t, err)
		require.Len(t, rec.changes, 1)
		assert.Equal(t, syncwire.KindDepartment, rec.changes[0].Kind)
		assert.Equal(t, sync_svc.OpUpdate, rec.changes[0].Op)
		assert.Equal(t, "dp-3", rec.changes[0].Meta.SyncID)
	})

	t.Run("a rejected profile queues nothing", func(t *testing.T) {
		ctx, deptMock, profiles, svc := setupEngineTest(t)
		rec := &engineSyncRecorder{}
		sync_svc.SetDefault(rec)
		t.Cleanup(func() { sync_svc.SetDefault(nil) })
		deptMock.EXPECT().Find(gomock.Any(), int64(3)).Return(dept(3, 1, 0, "eng"), nil)
		profiles.EXPECT().Find(gomock.Any(), int64(9)).Return(nil, nil)
		_, err := svc.SetDepartmentEngine(ctx, &SetDepartmentEngineRequest{DepartmentID: 3, EngineProfileID: 9})
		require.Error(t, err)
		assert.Empty(t, rec.changes)
	})
}
