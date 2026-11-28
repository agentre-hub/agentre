package project_svc_test

import (
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// 默认设备是项目同步载荷的一部分：保存成功后项目那一行要排进上行。
func TestSetDefaultDevice_NotifiesSync(t *testing.T) {
	t.Run("stored default device queues a project update", func(t *testing.T) {
		ctx, mp, _, svc := setupDefaultDevice(t)
		rec := registerRecordingSync(t)
		mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{
			ID: 5, Path: "/code/a", Status: consts.ACTIVE, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "proj-5"},
		}, nil)
		mp.EXPECT().SetDefaultDevice(ctx, int64(5), selfFP).Return(nil)
		require.NoError(t, svc.SetDefaultDevice(ctx, 5, selfFP))
		require.Len(t, rec.changes, 1)
		assert.Equal(t, syncwire.KindProject, rec.changes[0].Kind)
		assert.Equal(t, sync_svc.OpUpdate, rec.changes[0].Op)
		assert.Equal(t, "proj-5", rec.changes[0].Meta.SyncID)
	})
	t.Run("refused device queues nothing", func(t *testing.T) {
		ctx, mp, _, svc := setupDefaultDevice(t)
		rec := registerRecordingSync(t)
		mp.EXPECT().Find(ctx, int64(5)).Return(&project_entity.Project{ID: 5, LocalPathMissing: true, Status: consts.ACTIVE}, nil)
		require.Error(t, svc.SetDefaultDevice(ctx, 5, selfFP))
		assert.Empty(t, rec.changes)
	})
}
