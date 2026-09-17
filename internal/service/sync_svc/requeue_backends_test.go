package sync_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo/mock_app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo/mock_syncqueue_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// setupRequeueBackendsTest 装一个只带 agent_backend 适配器的最小引擎：三件仓储全走
// mockgen（决策：存量修复只碰 app_setting 的一次性标记 + agent_backend 列表 +
// 出站队列，不需要完整 newHarness 那份 transport/pull 装配）。
func setupRequeueBackendsTest(t *testing.T) (
	context.Context, *service,
	*mock_agent_backend_repo.MockAgentBackendRepo,
	*mock_app_setting_repo.MockAppSettingRepo,
	*mock_syncqueue_repo.MockOutboundQueueRepo,
) {
	t.Helper()
	ctrl := gomock.NewController(t)

	backends := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	agent_backend_repo.RegisterAgentBackend(backends)

	settings := mock_app_setting_repo.NewMockAppSettingRepo(ctrl)
	app_setting_repo.RegisterAppSetting(settings)

	outbound := mock_syncqueue_repo.NewMockOutboundQueueRepo(ctrl)
	syncqueue_repo.RegisterOutboundQueue(outbound)

	svc := &service{
		adapters: map[string]adapter{syncwire.KindAgentBackend: &agentBackendAdapter{}},
		now:      func() int64 { return 1_700_000_000_000 },
	}
	return context.Background(), svc, backends, settings, outbound
}

func TestRequeueBackendConfigs_FirstRunEnqueuesEachQualifyingBackendOnce(t *testing.T) {
	convey.Convey("Given 升级后从未跑过存量修复", t, func() {
		ctx, svc, backends, settings, outbound := setupRequeueBackendsTest(t)
		const accountID = int64(9)

		convey.Convey("When SyncOnce 首次跑（deleted / unbound / 别的账号的行仓储已经不会交出来）", func() {
			// ListSyncedForAccount 本身就是「活着、有同步标识、归属这个账号」的过滤
			// 判据（agent_backend_repo 的仓储测试另证），这里只需证明 service 层按
			// 它交回来的每一行各建一条入队记录，不多不少。
			settings.EXPECT().Get(ctx, "sync.backend_config_requeue.9").Return(nil, nil)
			backends.EXPECT().ListSyncedForAccount(ctx, accountID).Return([]*agent_backend_entity.AgentBackend{
				{ID: 1, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "backend-1", SyncAccountID: accountID, SyncVersion: 3}},
				{ID: 2, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "backend-2", SyncAccountID: accountID, SyncVersion: 5}},
			}, nil)
			var written []*syncqueue_entity.OutboundQueueItem
			outbound.EXPECT().CreateMany(ctx, gomock.Any()).DoAndReturn(
				func(_ context.Context, rows []*syncqueue_entity.OutboundQueueItem) error {
					written = rows
					return nil
				})
			settings.EXPECT().Set(ctx, gomock.Any()).DoAndReturn(
				func(_ context.Context, s *app_setting_entity.AppSetting) error {
					assert.Equal(t, "sync.backend_config_requeue.9", s.Key)
					assert.Equal(t, backendRequeueDone, s.Value)
					return nil
				})

			require.NoError(t, svc.requeueBackendConfigs(ctx, accountID))

			convey.Convey("Then 两条活着的后端各入队一次，且都是这个账号名下的更新", func() {
				if assert.Len(t, written, 2) {
					assert.Equal(t, "backend-1", written[0].EntitySyncID)
					assert.Equal(t, "backend-2", written[1].EntitySyncID)
					for _, row := range written {
						assert.Equal(t, syncwire.KindAgentBackend, row.EntityType)
						assert.Equal(t, OpUpdate, row.Op)
						assert.Equal(t, accountID, row.SyncAccountID)
					}
				}
			})
		})
	})
}

func TestRequeueBackendConfigs_SecondRunEnqueuesNothing(t *testing.T) {
	convey.Convey("Given 标记已经写过 done", t, func() {
		ctx, svc, _, settings, _ := setupRequeueBackendsTest(t)
		const accountID = int64(9)
		settings.EXPECT().Get(ctx, "sync.backend_config_requeue.9").
			Return(&app_setting_entity.AppSetting{Key: "sync.backend_config_requeue.9", Value: backendRequeueDone}, nil)
		// 不给 backends / outbound 设任何期望：一旦 requeueBackendConfigs 仍然去列
		// 后端或写队列，gomock 的严格模式当场判失败——这就是「不重复」的证明。

		convey.Convey("When 再跑一次 SyncOnce 的这一步", func() {
			err := svc.requeueBackendConfigs(ctx, accountID)

			convey.Convey("Then 什么都不入队，直接返回", func() {
				require.NoError(t, err)
			})
		})
	})
}

func TestRequeueBackendConfigs_EnqueueFailureLeavesTheMarkerUnset(t *testing.T) {
	convey.Convey("Given 出站队列这次会写失败", t, func() {
		ctx, svc, backends, settings, outbound := setupRequeueBackendsTest(t)
		const accountID = int64(9)
		settings.EXPECT().Get(ctx, "sync.backend_config_requeue.9").Return(nil, nil)
		backends.EXPECT().ListSyncedForAccount(ctx, accountID).Return([]*agent_backend_entity.AgentBackend{
			{ID: 1, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "backend-1", SyncAccountID: accountID, SyncVersion: 1}},
		}, nil)
		outbound.EXPECT().CreateMany(ctx, gomock.Any()).Return(errors.New("disk full"))
		// 不给 settings.Set 设期望：标记必须没写，写了 gomock 会因为没有匹配的期望而判失败。

		convey.Convey("When SyncOnce 的这一步跑入队", func() {
			err := svc.requeueBackendConfigs(ctx, accountID)

			convey.Convey("Then 入队失败原样返回，标记不落地，下一轮还会重试", func() {
				assert.ErrorContains(t, err, "disk full")
			})
		})
	})
}

func TestRequeueBackendConfigs_MarkerIsPerAccount(t *testing.T) {
	convey.Convey("Given 账号 9 已经跑过存量修复，用户换登了账号 42", t, func() {
		ctx, svc, backends, settings, outbound := setupRequeueBackendsTest(t)
		const otherAccountID = int64(42)
		// 只给账号 42 的 key 设期望：如果实现按单个全局 key 记「跑过没有」，
		// 这里会去查/写 "sync.backend_config_requeue.9" 而不是 "...42"，
		// gomock 严格模式下没有匹配期望的调用直接判失败。
		settings.EXPECT().Get(ctx, "sync.backend_config_requeue.42").Return(nil, nil)
		backends.EXPECT().ListSyncedForAccount(ctx, otherAccountID).Return([]*agent_backend_entity.AgentBackend{
			{ID: 3, SyncMeta: syncmeta_entity.SyncMeta{SyncID: "backend-3", SyncAccountID: otherAccountID, SyncVersion: 1}},
		}, nil)
		outbound.EXPECT().CreateMany(ctx, gomock.Any()).Return(nil)
		settings.EXPECT().Set(ctx, gomock.Any()).DoAndReturn(
			func(_ context.Context, s *app_setting_entity.AppSetting) error {
				assert.Equal(t, "sync.backend_config_requeue.42", s.Key)
				return nil
			})

		convey.Convey("When 以账号 42 的身份跑这一步", func() {
			err := svc.requeueBackendConfigs(ctx, otherAccountID)

			convey.Convey("Then 账号 42 仍然算它自己的首次运行，照常入队并各自记标记", func() {
				require.NoError(t, err)
			})
		})
	})
}

// TestRequeueBackendConfigs_GivenRepositoriesNotBootstrapped_DoesNotPanic 复现
// project_svc 那类最小同步测试踩到的真实故障：它们用 sync_svc.New（生产形态，
// defaultAdapters 恒定装配 agentBackendAdapter）装引擎，却只关心自己那个域对象，
// 从没调用过 RegisterAgentBackend / RegisterAppSetting——这两个仓储单例照样是
// nil。这一步真正依赖的是**仓储有没有装配**，不是适配器表里有没有这个 kind；
// 后者只是「引擎装了哪些同步对象类型」，两件事在这类测试里刚好对不上号。
func TestRequeueBackendConfigs_GivenRepositoriesNotBootstrapped_DoesNotPanic(t *testing.T) {
	convey.Convey("Given 引擎按生产形态装配了全部适配器，但仓储层还没 bootstrap（如 project_svc 的域测试）", t, func() {
		agent_backend_repo.RegisterAgentBackend(nil)
		app_setting_repo.RegisterAppSetting(nil)
		svc := &service{
			adapters: defaultAdapters(nil),
			now:      func() int64 { return 1_700_000_000_000 },
		}

		convey.Convey("When 跑这一步", func() {
			err := svc.requeueBackendConfigs(context.Background(), 7)

			convey.Convey("Then 安静跳过，既不 panic 也不报错", func() {
				assert.NoError(t, err)
			})
		})
	})
}
