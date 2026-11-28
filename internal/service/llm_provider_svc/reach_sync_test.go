package llm_provider_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type reachSyncRecorder struct {
	sync_svc.SyncSvc
	changes []sync_svc.LocalChange
}

func (r *reachSyncRecorder) NotifyLocalChange(_ context.Context, ch sync_svc.LocalChange) {
	r.changes = append(r.changes, ch)
}

func TestSetProviderReach_NotifiesSync(t *testing.T) {
	t.Run("saved reach queues a provider update", func(t *testing.T) {
		ctx, mockRepo, _, svc := setupSvcTest(t)
		rec := &reachSyncRecorder{}
		sync_svc.SetDefault(rec)
		t.Cleanup(func() { sync_svc.SetDefault(nil) })
		mockRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(&llm_provider_entity.LLMProvider{
			ID: 3, Status: consts.ACTIVE, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "pk-3"}}, nil)
		mockRepo.EXPECT().UpdateReach(gomock.Any(), int64(3), llm_provider_entity.ReachDevices, `["sha256:a"]`).Return(nil)
		_, err := svc.SetProviderReach(ctx, &SetProviderReachRequest{ID: 3, Mode: llm_provider_entity.ReachDevices, Devices: []devicefp.Carrier{"sha256:a"}})
		require.NoError(t, err)
		require.Len(t, rec.changes, 1)
		assert.Equal(t, syncwire.KindLLMProvider, rec.changes[0].Kind)
		assert.Equal(t, sync_svc.OpUpdate, rec.changes[0].Op)
		assert.Equal(t, "pk-3", rec.changes[0].Meta.SyncID)
	})
	t.Run("invalid reach queues nothing", func(t *testing.T) {
		ctx, mockRepo, _, svc := setupSvcTest(t)
		rec := &reachSyncRecorder{}
		sync_svc.SetDefault(rec)
		t.Cleanup(func() { sync_svc.SetDefault(nil) })
		mockRepo.EXPECT().Find(gomock.Any(), int64(3)).Return(&llm_provider_entity.LLMProvider{ID: 3, Status: consts.ACTIVE}, nil)
		_, err := svc.SetProviderReach(ctx, &SetProviderReachRequest{ID: 3, Mode: llm_provider_entity.ReachDevices})
		require.Error(t, err)
		assert.Empty(t, rec.changes)
	})
}
