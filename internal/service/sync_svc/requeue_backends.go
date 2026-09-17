package sync_svc

import (
	"context"
	"strconv"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// backendRequeueKeyPrefix 是「存量修复：升级后把已绑定账号的后端各重新上行一次」这条
// 一次性标记的存放位置（docs/specs/2026-09-17-backend-config-sync.md 决策 4）。
//
// f791eb9b 之前，桌面端把独占设置上行成了空值（Problem 1）；那次修复之后，只有
// **这一轮真的落过库**的后端才会带着真实设置重新上行——已经在线上躺着的账号，
// 它们的后端从不会再被本地编辑触发一次上行，因此 server 上的空值永远不会被更正。
// 这条标记不能是单个全局 key：账号是本地可以更换的身份（sync_account_repo），
// 换一个账号登录时，那个账号名下的后端从未补过这一次，因此按 accountID 分 key，
// 与 boardJoinNoticeKey 的「看板并入」一次性说明同一个存放方式，但那条是单一
// 事件、这条是逐账号事件。
const backendRequeueKeyPrefix = "sync.backend_config_requeue."

// backendRequeueDone 标记这个账号已经跑过一次存量修复。
const backendRequeueDone = "done"

func backendRequeueKey(accountID int64) string {
	return backendRequeueKeyPrefix + strconv.FormatInt(accountID, 10)
}

// requeueBackendConfigs 是存量修复的落点：把「已绑定 accountID、未删除」的每个后端
// 各排入一次上行队列，只在这个账号从未跑过这一步时才做，跑过一次后原样跳过。
//
// **排在 flush 之前**（见 SyncOnce）：入队的这些行要在当前这一轮就被推上去，而不是
// 排到下一轮 30 秒轮询——那份正确的 config 只存在本机，早一轮送达就少一轮空值窗口。
//
// 标记只在入队成功之后才写（先入队、后标记）：一次网络或落库失败如果仍然记下
// 「已经修复过」，这个账号就永远补不回这一次——下次启动看到标记已经是 done，
// 不会再试。失败因此原样返回，让调用方（SyncOnce）按现有的重试节奏下一轮再来。
func (s *service) requeueBackendConfigs(ctx context.Context, accountID int64) error {
	// 引擎没装配 agent_backend 适配器（单机构建 / 只装了子集的测试）：这一步没有
	// 落点，与 claimForCurrentAccount 对未知 kind 的处理同一条纪律。
	if s.adapters[syncwire.KindAgentBackend] == nil {
		return nil
	}

	key := backendRequeueKey(accountID)
	marker, err := app_setting_repo.AppSetting().Get(ctx, key)
	if err != nil {
		return err
	}
	if marker != nil && marker.Value == backendRequeueDone {
		return nil
	}

	// 「已绑定、未删除」的判据整段交给仓储（ListSyncedForAccount）：活着
	// （status=ACTIVE）、有同步标识、归属这个账号、且没有落过跨机墓碑
	// （sync_deleted_at=0）——四条同时成立才算「这个账号还在用的后端」。
	rows, err := agent_backend_repo.AgentBackend().ListSyncedForAccount(ctx, accountID)
	if err != nil {
		return err
	}

	var queueRows []*syncqueue_entity.OutboundQueueItem
	for _, row := range rows {
		// 按更新处理，基版本沿用本机已知的那一版：这些行早已同步过，这次只是把
		// 之前上行时丢掉的 config 补齐，不是新建（buildQueueRows 对 agent_backend
		// 这一 kind 没有从属行，dependents 恒为空，一行只产出一条队列行）。
		built, err := s.buildQueueRows(ctx, accountID, LocalChange{
			Kind: syncwire.KindAgentBackend, Op: OpUpdate,
			Meta: syncmeta_entity.SyncMeta{
				SyncID: row.SyncID, SyncAccountID: accountID, SyncVersion: row.SyncVersion,
			},
		})
		if err != nil {
			return err
		}
		queueRows = append(queueRows, built...)
	}
	if len(queueRows) > 0 {
		if err := syncqueue_repo.OutboundQueue().CreateMany(ctx, queueRows); err != nil {
			return err
		}
	}

	if err := app_setting_repo.AppSetting().Set(ctx, &app_setting_entity.AppSetting{
		Key: key, Value: backendRequeueDone, Updatetime: s.now(),
	}); err != nil {
		return err
	}
	logger.Ctx(ctx).Info("sync_svc.requeueBackendConfigs: requeued existing backends for upload",
		zap.Int64("accountId", accountID), zap.Int("count", len(rows)))
	return nil
}
