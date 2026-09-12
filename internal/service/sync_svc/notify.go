package sync_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
)

// NotifyLocalChange 入队 + 当场触发一次上行（R3）。
//
// 入队是同步的（一次小写，进程立刻退出也不丢改动），推送在后台（R8：用户的编辑
// 不因同步未完成而被阻塞或回滚）。任何失败只记日志——本地写入已经成功了，同步层
// 没有资格让它失败。
func (s *service) NotifyLocalChange(ctx context.Context, ch LocalChange) {
	if !kindKnown(ch.Kind) || ch.Meta.SyncID == "" {
		return
	}
	accountID, _, _, _, ok := s.account(ctx)
	// 这一行不参与**当前账号**的同步：要么没登录（R12），要么登录的是另一个账号
	// 而它属于上一个账号（R13a）。
	if !ok || !ch.Meta.EligibleForSync(accountID) {
		// 删除是这两条规则共同的例外。一行已经归属某个账号，就意味着 server 上有
		// 它的副本；把它删掉，这条删除欠着**那个**账号一个墓碑（R6：删除必须到达
		// 各端）。排进那个账号的出站队列，等它自己登回来时送达。
		//
		// 当场丢掉的后果不是「晚一点再同步」，而是**这次删除永远不会发生**：本地
		// 行只是 status = DELETE，sync_account_id 与 sync_version 都还在，此后
		// ClaimForAccount 收不到它（只收**存活**的行），ListUnversioned 也收不到
		// 它（只认 sync_version = 0 的存活行）。那个账号的其它端于是永远看得见一个
		// 这台机器上早已删掉的对象。
		//
		// 这不违反 R13a。R13a 说的是这些行不上行到**当前**账号，而 flush 只取当前
		// 登录账号的队列行——排在别的账号名下的这一条，一个字节也不会发给现在登录
		// 的这个账号。
		//
		// 从没归属过账号的行不在此列：server 上没有它的副本，凭空推一条墓碑上去
		// 只会占掉那个同步标识，而 R6 说墓碑不会被复活——同一个标识此后再也建不回来。
		if ch.Op != OpDelete || ch.Meta.SyncAccountID == 0 {
			return
		}
		if err := s.enqueue(ctx, ch.Meta.SyncAccountID, ch); err != nil {
			logger.Ctx(ctx).Warn("sync_svc.NotifyLocalChange: enqueue tombstone for a non-current account failed",
				zap.String("kind", ch.Kind), zap.String("syncId", ch.Meta.SyncID), zap.Error(err))
		}
		return
	}

	if err := s.enqueue(ctx, accountID, ch); err != nil {
		logger.Ctx(ctx).Warn("sync_svc.NotifyLocalChange: enqueue failed",
			zap.String("kind", ch.Kind), zap.String("op", ch.Op), zap.Error(err))
		return
	}

	bgCtx := context.WithoutCancel(ctx)
	s.background(func() {
		if err := s.SyncOnce(bgCtx); err != nil {
			logger.Ctx(bgCtx).Debug("sync_svc.NotifyLocalChange: push failed, staying queued",
				zap.String("kind", ch.Kind), zap.Error(err))
		}
	})
}

// claimAnonymousQueue attaches R13 runtime-claim mutations recorded while
// logged out to the account that has just authenticated. It reuses the normal
// outbound queue: account 0 is only a temporary local holding key and is never
// sent to the server.
func (s *service) claimAnonymousQueue(ctx context.Context, accountID int64) error {
	rows, err := syncqueue_repo.OutboundQueue().ListByAccount(ctx, 0)
	if err != nil {
		return err
	}
	for _, row := range rows {
		moved := *row
		moved.ID = 0
		moved.SyncAccountID = accountID
		if err := syncqueue_repo.OutboundQueue().Create(ctx, &moved); err != nil {
			return err
		}
		if err := syncqueue_repo.OutboundQueue().Delete(ctx, row.ID); err != nil {
			return err
		}
	}
	return nil
}

// claimForCurrentAccount 把本机**不属于当前账号**的存活行归入它，并**带着各自那个
// 同步标识**正常上行。两类行都收：
//
//   - 登录前已有的行（R12a）。它们不是别人的数据，只是还没上过云。
//   - 属于**上一个**账号的行（规格 2026-09-04 决策 1，推翻 R13a）。这一类的版本号
//     由仓储一并清零：那是上一个账号那套序列里的坐标，拿它当基版本上行是撒谎。
//
// 每一轮同步都跑一次，不另记「认领过没有」的状态：判据是行上的归属，认领完成之后
// 就是空集，因此重跑是幂等的。软删行与墓碑一律不收（决策 3）——给一个账号推它从没
// 有过的墓碑会按 R6 永久占掉那个同步标识，而那条删除是欠着上一个账号的债，留在它
// 名下等它登回来（见 NotifyLocalChange 的删除分支）。
//
// **排在 ensureServerIdentity 之后**（见 SyncOnce）。换账号时那一步会先拉一份新账号
// 的全量快照，跨账号唯一会撞上的那个固定同步标识（agent_entity.DefaultAgentSyncID）
// 因此已经被新账号那份覆盖、归属也已改写；认领于是自然跳过它，不会把上一个账号的
// 那份推上去。次序反过来这条保证就没了。
func (s *service) claimForCurrentAccount(ctx context.Context, accountID int64) error {
	for _, kind := range syncKinds {
		rows, err := syncstate_repo.SyncState().ClaimForAccount(ctx, kind, accountID)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			continue
		}
		if isBoardKind(kind) {
			// 看板刚被并进这个账号：合并的后果要说在前面（一次性说明）。
			s.markBoardJoinNotice(ctx)
		}
		for _, row := range rows {
			// 基版本沿用仓储交回来的那一个：server 从没见过的行、以及刚从上一个
			// 账号收过来的行都是 0，按 R4a 当新建处理。
			if err := s.enqueue(ctx, accountID, LocalChange{
				Kind: kind, Op: OpCreate,
				Meta: syncmeta_entity.SyncMeta{
					SyncID: row.SyncID, SyncAccountID: accountID, SyncVersion: row.Version,
				},
			}); err != nil {
				return err
			}
		}
		logger.Ctx(ctx).Info("sync_svc.claimForCurrentAccount: claimed rows that did not belong to this account",
			zap.String("kind", kind), zap.Int("count", len(rows)))
	}
	return nil
}

// reconcileLostTombstones 补上「本机已经软删、这条删除却从没送达过 server」的那些行
// （R6：删除必须到达各端）。
//
// 需要这条兜底，是因为一次没发生的入队此后没有任何取数会再看见那一行：
// ClaimForAccount 与 ListUnversioned 都只收存活的行。server 那份于是一直活着，控制台一直
// 列着一个用户明明删过的对象——这正是登出期间删除被丢掉后留下的存量。
//
// 每一轮都跑一次，不另记「补过没有」的状态：判据就是行上的 sync_deleted_at 为 0，而
// 一条墓碑上行成功之后 applyPushResult 当场把删除时刻写进这一列，第一轮之后它自然是
// 空集。入队而不是直接推，是为了走同一条出站队列——折叠（collapseQueue）会把它与可能
// 已经排在队列里的同一条删除合成一条，离线时它也和别的改动一样留在队列里等下一轮。
func (s *service) reconcileLostTombstones(ctx context.Context, accountID int64) error {
	for _, kind := range syncKinds {
		rows, err := syncstate_repo.SyncState().ListUnsyncedTombstones(ctx, kind, accountID)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err := s.enqueue(ctx, accountID, LocalChange{
				Kind: kind, Op: OpDelete,
				Meta: syncmeta_entity.SyncMeta{
					SyncID: row.SyncID, SyncAccountID: accountID, SyncVersion: row.Version,
				},
			}); err != nil {
				return err
			}
			logger.Ctx(ctx).Info("sync_svc.reconcileLostTombstones: queued a delete that never reached the server",
				zap.String("kind", kind), zap.String("syncId", row.SyncID))
		}
	}
	return nil
}

// enqueue 把一条本地改动（连同它的从属行 / 子行）写进出站队列。
//
//   - 增改：Agent 的执行目标随 Agent 一起入队——它们是独立的同步对象，却只跟着
//     Agent 的写入路径变化（agent_repo 在同一个事务里落两张表）。
//   - 删除：子行一并落墓碑（R6）——删项目连它的路径记录与成员关系，删 Agent 连
//     它的成员关系与执行目标，删 backend 连引用它的执行目标（Agent 本身不删）。
func (s *service) enqueue(ctx context.Context, accountID int64, ch LocalChange) error {
	ad := s.adapters[ch.Kind]
	if ad == nil {
		return nil
	}

	var related []relatedRow
	var err error
	if ch.Op == OpDelete {
		related, err = ad.children(ctx, ch.Meta.SyncID)
	} else {
		related, err = ad.dependents(ctx, ch.Meta.SyncID)
	}
	if err != nil {
		return err
	}

	now := s.now()
	rows := make([]*syncqueue_entity.OutboundQueueItem, 0, len(related)+1)
	rows = append(rows, &syncqueue_entity.OutboundQueueItem{
		SyncAccountID: accountID,
		EntityType:    ch.Kind,
		LocalID:       ch.LocalID,
		EntitySyncID:  ch.Meta.SyncID,
		Op:            ch.Op,
		BaseVersion:   ch.Meta.SyncVersion,
		QueuedAt:      now,
	})
	for _, r := range related {
		if r.SyncID == "" {
			continue
		}
		op := OpUpdate
		if ch.Op == OpDelete {
			op = OpDelete
		}
		rows = append(rows, &syncqueue_entity.OutboundQueueItem{
			SyncAccountID: accountID,
			EntityType:    r.Kind,
			LocalID:       r.LocalID,
			EntitySyncID:  r.SyncID,
			Op:            op,
			BaseVersion:   r.Version,
			QueuedAt:      now,
		})
	}

	for _, row := range rows {
		if err := syncqueue_repo.OutboundQueue().Create(ctx, row); err != nil {
			return err
		}
	}
	return nil
}
