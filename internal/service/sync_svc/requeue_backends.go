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
	// 两条前提都要成立，各自守着这一步真正踩到的一类装配缺口，缺一不可：
	//
	//  1. 引擎的适配器表要真的装了 agent_backend 这个 kind——与
	//     claimForCurrentAccount 对未知 kind 的处理同一条纪律。只关心别的对象类型
	//     的最小引擎（本包多数用 newHarness 拼出来的测试，只装一两个 fakeAdapter）
	//     没有这个 kind，本来就不该碰 agent_backend_repo：它们的仓储单例可能是
	//     nil，也可能是**上一个测试用例留下、controller 早已收尾的陈旧 mock**
	//     （包级单例跨用例存活，docs/testing.md「Package-level globals leak across
	//     cases」）——后一种情况一个 repo != nil 判断看不出来，唯有先看适配器表。
	//  2. 三个仓储单例要真的被 bootstrap 注册过，不是 nil——与 account() 对
	//     sync_account_repo 未装配时的处理同一条纪律。装了全套生产适配器表
	//     （sync_svc.New 的 defaultAdapters 恒定包含 agentBackendAdapter）却只关心
	//     自己那个域对象的单测（如 project_svc）符合前一条，却从没调用过
	//     RegisterAgentBackend / RegisterAppSetting，这时候只看适配器表判断不出
	//     仓储没装配。
	//
	// 两条合起来才是这一步真正依赖的生产前提：适配器表回答「这个引擎实例管不管
	// agent_backend」，仓储是否注册回答「管的话，它的仓储装没装好」。
	if s.adapters[syncwire.KindAgentBackend] == nil {
		return nil
	}
	settingsRepo := app_setting_repo.AppSetting()
	backendRepo := agent_backend_repo.AgentBackend()
	outboundRepo := syncqueue_repo.OutboundQueue()
	if settingsRepo == nil || backendRepo == nil || outboundRepo == nil {
		return nil
	}

	key := backendRequeueKey(accountID)
	marker, err := settingsRepo.Get(ctx, key)
	if err != nil {
		return err
	}
	if marker != nil && marker.Value == backendRequeueDone {
		return nil
	}

	// 「已绑定、未删除」的判据整段交给仓储（ListSyncedForAccount）：活着
	// （status=ACTIVE）、有同步标识、归属这个账号、且没有落过跨机墓碑
	// （sync_deleted_at=0）——四条同时成立才算「这个账号还在用的后端」。
	rows, err := backendRepo.ListSyncedForAccount(ctx, accountID)
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
		if err := outboundRepo.CreateMany(ctx, queueRows); err != nil {
			return err
		}
	}

	if err := settingsRepo.Set(ctx, &app_setting_entity.AppSetting{
		Key: key, Value: backendRequeueDone, Updatetime: s.now(),
	}); err != nil {
		return err
	}
	logger.Ctx(ctx).Info("sync_svc.requeueBackendConfigs: requeued existing backends for upload",
		zap.Int64("accountId", accountID), zap.Int("count", len(rows)))
	return nil
}
