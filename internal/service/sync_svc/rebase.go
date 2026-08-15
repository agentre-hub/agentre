package sync_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/repository/syncstate_repo"
)

// 本文件只处置一种失效：**server 不认识本端的下行游标**（syncwire.ErrCursorUnknown）。
//
// 它发生在账号的版本序列被从头开始的时候——服务端数据库被重建，或用户换了一套自建
// 服务端。这与 R6a 的「离线超窗口」是**相反**的两件事，因此走两条不同的路：
//
//   - R6a（上行被拒）：server 的历史是全的、本端的不全。以快照为准，队列里基版本
//     对不上的一律拦下并进 R5 列表（reevaluateAfterResync）——那正是防止一台陈旧的
//     机器把已被回收的删除推回来的那道闸。**不重推。**
//   - 本文件（下行被拒）：server 的历史没了、本端的才是全的。不重推的话，那些「以为
//     自己同步过了」的行会静默留在本机：出站队列是空的，界面上待同步是 0、没有任何
//     错误，两台机器从此互相看不见。
//
// 重推的边界只有一条，也是这条路径上唯一的真陷阱：**已被服务端正当删除的行不许被
// 推回去（R6）。** 靠的是次序——先忘掉旧版本号，再让全量快照（含窗口内的墓碑）落地，
// 最后才在**落地之后的**存活行里挑重推对象。墓碑落地时那一行已经不在了，挑不到它。

// rebase 重建整份历史：忘掉上一套 server 的版本号 → 拉一份全量快照 → 把快照没送来
// 的存活行重新入队上行。
//
// 三步的次序不能改。先忘版本号，快照才落得下去（applyInbound 的版本守卫是「本机
// 版本 >= 来的版本就不落」，而新序列的版本号普遍比旧序列小）；快照先落地，重推才
// 挑得对（server 认识的行已经拿到新版本号，被删的行已经不在了）。
func (s *service) rebase(ctx context.Context, accountID int64) error {
	logger.Ctx(ctx).Warn("sync_svc.rebase: server does not recognize our cursor, rebuilding from a full snapshot",
		zap.Int64("accountId", accountID))
	if err := s.forgetServerVersions(ctx, accountID); err != nil {
		return err
	}
	if err := s.forgetLocalPathReport(ctx, accountID); err != nil {
		return err
	}
	if err := s.pullFrom(ctx, accountID, 0); err != nil {
		return err
	}
	return s.requeueUnknownRows(ctx, accountID)
}

// forgetServerVersions 清掉本账号名下全部行的同步版本号。
//
// 它们是**上一套序列**里的值：既比不了大小（R4 的胜负判据要求同一个序列），也会把
// 新序列的全量快照整个挡在版本守卫外面。墓碑标记不清——清掉它等于让已经删掉的行
// 重新变成一条待上行的新建（R6）。
func (s *service) forgetServerVersions(ctx context.Context, accountID int64) error {
	for _, kind := range syncKinds {
		if err := syncstate_repo.SyncState().ResetVersions(ctx, kind, accountID); err != nil {
			return err
		}
	}
	return nil
}

// forgetLocalPathReport 清掉本机路径上报的进度（R16 的内容指纹）。
//
// 那份指纹同样是一份「相对上一套 server」的缓存：内容没变就不发，而新库那边一条
// 都没有。不清掉它，web 端会把每个项目永远显示成「本机未配置路径」——同一个根因
// 造成的第二处静默失联，且因为上报是纯投影，谁都不会报错。
func (s *service) forgetLocalPathReport(ctx context.Context, accountID int64) error {
	return s.saveLocalPathReportState(ctx, localPathReportState{AccountID: accountID})
}

// requeueUnknownRows 把全量快照没送来的存活行重新入队上行。
//
// 判据是「同步版本号仍为 0」——版本号只由 server 在受理上行时分配，快照刚刚给它认识
// 的每一行都盖上了新序列的版本号，剩下还是 0 的就是它不认识的那些。基版本一并为 0：
// 按 R4a 这是一次新建，新库确实从没见过这个标识。
//
// **墓碑不在其中。** 存活判据（ListUnversioned）排除了本机软删的行与墓碑，而快照里
// 那些窗口内的删除刚刚在上一步落了地——被删的行此刻要么已经不存在，要么已经带着
// 墓碑标记，两种都挑不到。这是 R6「删除不会被复活」在这条路径上的落点。
func (s *service) requeueUnknownRows(ctx context.Context, accountID int64) error {
	for _, kind := range syncKinds {
		syncIDs, err := syncstate_repo.SyncState().ListUnversioned(ctx, kind, accountID)
		if err != nil {
			return err
		}
		if len(syncIDs) == 0 {
			continue
		}
		for _, syncID := range syncIDs {
			if err := s.enqueue(ctx, accountID, LocalChange{
				Kind: kind, Op: OpCreate,
				Meta: syncmeta_entity.SyncMeta{SyncID: syncID, SyncAccountID: accountID},
			}); err != nil {
				return err
			}
		}
		logger.Ctx(ctx).Info("sync_svc.requeueUnknownRows: rows the server no longer knows are queued again",
			zap.String("kind", kind), zap.Int("count", len(syncIDs)))
	}
	return nil
}
