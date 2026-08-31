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
	if !ok {
		// 未登录：本规格引入的一切都不存在（R12）。
		return
	}
	// 换账号登录时，属于上一个账号的行不上行、也不携带原同步标识（R13a）。
	if !ch.Meta.EligibleForSync(accountID) {
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

// claimUnowned 落实 R12a：登录前已有的行——它们还不属于任何账号——在登录后归入
// 当前账号，并**带着自己那个同步标识**正常上行。它们不是别人的数据，只是还没上过云。
//
// 每一轮同步都跑一次，不另记「认领过没有」的状态：认领只匹配 sync_account_id = 0
// 的存活行，第一轮之后就是空集。属于**另一个**账号的行一行不动（R13a）。
func (s *service) claimUnowned(ctx context.Context, accountID int64) error {
	for _, kind := range syncKinds {
		rows, err := syncstate_repo.SyncState().ClaimUnowned(ctx, kind, accountID)
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
			// 基版本沿用行上那一个：server 从没见过的行是 0，按 R4a 当新建处理。
			if err := s.enqueue(ctx, accountID, LocalChange{
				Kind: kind, Op: OpCreate,
				Meta: syncmeta_entity.SyncMeta{
					SyncID: row.SyncID, SyncAccountID: accountID, SyncVersion: row.Version,
				},
			}); err != nil {
				return err
			}
		}
		logger.Ctx(ctx).Info("sync_svc.claimUnowned: claimed rows that predate login",
			zap.String("kind", kind), zap.Int("count", len(rows)))
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
