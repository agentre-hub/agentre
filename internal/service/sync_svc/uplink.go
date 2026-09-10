package sync_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
)

// pending 是折叠后的一条待上行改动：同一行在队列里的多次改动只推最后一次，
// 被它盖掉的队列行一起出队（R7：不丢失、不重复应用）。
type pending struct {
	kind        string
	syncID      string
	op          string
	baseVersion int64
	queueIDs    []int64
}

// flush 把出站队列推上去（R3 的上行、R7 的补齐）。
//
// 队列按入队顺序上行，一个批次一次请求——server 按顺序逐条处理，父行先落地、
// 子行后落地的次序因此在对端也成立。
func (s *service) flush(ctx context.Context, accountID int64, originFingerprint string) error {
	rows, err := syncqueue_repo.OutboundQueue().ListByAccount(ctx, accountID)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	items := collapseQueue(rows)

	for start := 0; start < len(items); start += pushBatch {
		end := start + pushBatch
		if end > len(items) {
			end = len(items)
		}
		if err := s.pushBatch(ctx, accountID, originFingerprint, items[start:end], true); err != nil {
			return err
		}
	}
	return nil
}

// collapseQueue 按 (对象类型, 同步标识) 折叠队列：保留最后一次操作，按第一次出现的
// 次序排列——先建的行仍然先上行，它的引用目标因此在对端先落地。
func collapseQueue(rows []*syncqueue_entity.OutboundQueueItem) []*pending {
	index := make(map[string]*pending, len(rows))
	order := make([]*pending, 0, len(rows))
	for _, row := range rows {
		key := row.EntityType + ":" + row.EntitySyncID
		p := index[key]
		if p == nil {
			p = &pending{
				kind: row.EntityType, syncID: row.EntitySyncID,
				op: row.Op, baseVersion: row.BaseVersion,
			}
			index[key] = p
			order = append(order, p)
		}
		p.op = row.Op
		p.queueIDs = append(p.queueIDs, row.ID)
	}
	return order
}

// pushBatch 推一批并落实应答。allowResync 为 true 时，遇上「超窗口」先做一次全量
// 重同步再重试（R6a）；重试那一次不再允许递归重同步。
func (s *service) pushBatch(
	ctx context.Context, accountID int64, originFingerprint string, batch []*pending, allowResync bool,
) error {
	items := make([]syncwire.PushItem, 0, len(batch))
	kept := make([]*pending, 0, len(batch))
	var doneQueueIDs []int64

	for _, p := range batch {
		item, ok, err := s.buildPushItem(ctx, p)
		if err != nil {
			return err
		}
		if !ok {
			// 本机已经没有这一行、或它翻不成一份能过机的载荷：出队，不上行。
			doneQueueIDs = append(doneQueueIDs, p.queueIDs...)
			continue
		}
		items = append(items, *item)
		kept = append(kept, p)
	}
	if err := s.dropQueueRows(ctx, doneQueueIDs); err != nil {
		return err
	}
	if len(items) == 0 {
		return nil
	}

	results, err := s.getTransport().SyncPush(ctx, items)
	if err != nil {
		if allowResync && errors.Is(err, syncwire.ErrResyncRequired) {
			logger.Ctx(ctx).Info("sync_svc.pushBatch: resync required, pulling full snapshot")
			if rerr := s.resync(ctx, accountID); rerr != nil {
				return rerr
			}
			// items 是**重同步之前**建好的那一份，必须原样传进去：快照刚刚把本地
			// 行覆盖成了 server 的内容，此刻再去读本地行拿到的是覆盖它的那一份。
			survivors, serr := s.reevaluateAfterResync(ctx, accountID, kept, items)
			if serr != nil {
				return serr
			}
			if len(survivors) == 0 {
				return nil
			}
			return s.pushBatch(ctx, accountID, originFingerprint, survivors, false)
		}
		return err
	}

	byKey := make(map[string]syncwire.PushResult, len(results))
	for _, r := range results {
		byKey[r.Kind+":"+r.SyncID] = r
	}
	for i, p := range kept {
		res, ok := byKey[p.kind+":"+p.syncID]
		if !ok {
			continue
		}
		if err := s.applyPushResult(ctx, accountID, originFingerprint, p, items[i], res); err != nil {
			return err
		}
		if err := s.dropQueueRows(ctx, p.queueIDs); err != nil {
			return err
		}
	}
	return nil
}

// buildPushItem 把一条待上行改动翻成上行项。第二个返回值为 false 表示这一条不该发。
func (s *service) buildPushItem(ctx context.Context, p *pending) (*syncwire.PushItem, bool, error) {
	ad := s.adapters[p.kind]
	if ad == nil {
		return nil, false, nil
	}
	if p.op == OpDelete {
		// 墓碑不带正文：本地行可能已经软删，读不回来也不需要。
		return &syncwire.PushItem{
			Kind: p.kind, SyncID: p.syncID, BaseVersion: p.baseVersion,
			UpdatedAt: s.now(), DeletedAt: s.now(),
		}, true, nil
	}
	out, err := ad.load(ctx, p.syncID)
	if err != nil {
		return nil, false, err
	}
	if out == nil {
		return nil, false, nil
	}
	// 上行前的守卫：载荷里绝不出现本地自增 ID 或 provider 正文（R2、决策 6）。
	if err := syncwire.GuardPayload(p.kind, out.Payload); err != nil {
		logger.Ctx(ctx).Error("sync_svc.buildPushItem: payload rejected by guard",
			zap.String("kind", p.kind), zap.String("syncId", p.syncID), zap.Error(err))
		return nil, false, nil
	}
	return &syncwire.PushItem{
		Kind:   p.kind,
		SyncID: p.syncID,
		// 基版本取**入队时**记下的那一个，不是此刻行上的 SyncVersion（R4a、决策 27）：
		// 编辑与出队之间落了一次他端下行时，行上的版本已经被那次下行推到了最新，
		// 拿它上行等于永远「基版本 == 当前版本」，server 判不出冲突，R5 的「被覆盖」
		// 永不写——那正是决策 27 要兜住的场景。
		BaseVersion:         p.baseVersion,
		UpdatedAt:           out.UpdatedAt,
		AgentredFingerprint: out.AgentredFingerprint,
		ProjectSyncID:       out.ProjectSyncID,
		Payload:             out.Payload,
	}, true, nil
}

// applyPushResult 落实一条应答。
//
//   - accepted：把 server 分配的版本写回本行，它就是下一次上行的基版本（R4a）。
//   - conflict：本次上行照常生效（R4 后到者胜），但被覆盖的那一版按 R5 落一条
//     「被覆盖」记录。
//   - rejected / deleted：对象在 server 上已是墓碑，删除不被复活（R6）——本机跟着
//     落墓碑，内容留进 R5 的列表，界面据此给「按这份内容新建」的出路（R5a）。
//   - 路径记录被自然键合并掉时（R4b），落败的那一份同样进列表。
func (s *service) applyPushResult(
	ctx context.Context, accountID int64, originFingerprint string,
	p *pending, item syncwire.PushItem, res syncwire.PushResult,
) error {
	now := s.now()

	if res.Status == syncwire.PushStatusRejected {
		if res.Reason == syncwire.PushRejectReasonDeleted {
			return s.acceptRemoteTombstone(ctx, accountID, p, item, res, now)
		}
		// 其它单条拒绝（对象类型不认、载荷过不了 server 的守卫）：这一条出队——
		// 留着它只会每一轮再被拒一次，把整条上行队列堵死——并进 R5 的列表，
		// 用户才知道这次改动没能同步上去，而不是以为它已经在别的机器上了。
		logger.Ctx(ctx).Warn("sync_svc.applyPushResult: item rejected",
			zap.String("kind", p.kind), zap.String("syncId", p.syncID),
			zap.String("reason", res.Reason))
		return s.recordLostChange(ctx, accountID, &syncqueue_entity.LostChange{
			EntityType:          p.kind,
			EntitySyncID:        p.syncID,
			BaseVersion:         res.Version,
			Reason:              syncqueue_entity.ReasonRejected,
			PayloadJSON:         string(item.Payload),
			ProjectSyncID:       item.ProjectSyncID,
			AgentredFingerprint: item.AgentredFingerprint,
			OccurredAt:          now,
		})
	}

	meta := syncmeta_entity.SyncMeta{
		SyncID:                p.syncID,
		SyncAccountID:         accountID,
		SyncVersion:           res.Version,
		SyncUpdatedAt:         item.UpdatedAt,
		SyncOriginFingerprint: originFingerprint,
	}
	if p.op == OpDelete {
		meta.SyncDeletedAt = now
	}
	if err := syncstate_repo.SyncState().SaveMeta(ctx, p.kind, p.syncID, meta); err != nil {
		return err
	}

	if res.Status == syncwire.PushStatusConflict {
		if err := s.recordLostChange(ctx, accountID, &syncqueue_entity.LostChange{
			EntityType:   p.kind,
			EntitySyncID: p.syncID,
			BaseVersion:  res.OverwrittenVersion,
			Reason:       syncqueue_entity.ReasonOverwritten,
			// 记的是**被覆盖掉的那一版**，由 server 随应答带回来。本端手上那一份
			// （item.Payload）是覆盖别人的那一份，它此刻正是 server 上的当前值——
			// 把它记成「被覆盖」会让 R5 的「追回」变成「把刚生效的内容再推一遍」。
			PayloadJSON:         string(res.OverwrittenPayload),
			ProjectSyncID:       item.ProjectSyncID,
			AgentredFingerprint: item.AgentredFingerprint,
			// 空串 = 服务端直写（浏览器改的组织架构），不是「不知道被谁」，见 originDeviceOf。
			OriginDevice: originDeviceOf(res.OverwrittenOriginFingerprint),
			OccurredAt:   now,
		}); err != nil {
			return err
		}
	}
	// R4b：本次上行的这一份在自然键上落败，已经被 server 落成墓碑。
	if res.MergedSyncID == p.syncID {
		if err := s.acceptRemoteTombstone(ctx, accountID, p, item, res, now); err != nil {
			return err
		}
	}
	return nil
}

// acceptRemoteTombstone server 上这一行已是墓碑：本机跟着落墓碑（删除不复活，R6），
// 本次没能生效的内容进 R5 的列表。
func (s *service) acceptRemoteTombstone(
	ctx context.Context, accountID int64,
	p *pending, item syncwire.PushItem, res syncwire.PushResult, now int64,
) error {
	if ad := s.adapters[p.kind]; ad != nil {
		if err := ad.remove(ctx, &inbound{Kind: p.kind, SyncID: p.syncID}); err != nil {
			return err
		}
	}
	if err := syncstate_repo.SyncState().SaveMeta(ctx, p.kind, p.syncID, syncmeta_entity.SyncMeta{
		SyncID:        p.syncID,
		SyncAccountID: accountID,
		SyncVersion:   res.Version,
		SyncUpdatedAt: item.UpdatedAt,
		SyncDeletedAt: now,
	}); err != nil {
		return err
	}
	return s.recordLostChange(ctx, accountID, &syncqueue_entity.LostChange{
		EntityType:          p.kind,
		EntitySyncID:        p.syncID,
		BaseVersion:         res.Version,
		Reason:              syncqueue_entity.ReasonRejected,
		PayloadJSON:         string(item.Payload),
		ProjectSyncID:       item.ProjectSyncID,
		AgentredFingerprint: item.AgentredFingerprint,
		OccurredAt:          now,
	})
}

// reevaluateAfterResync 落实 R6a：重同步之后，队列里的每一条按基版本判去留。
//
//   - 基版本为空 → 照常上行（本端离线期间新建的行，不是复活）。
//   - 基版本非空且与快照里该行的当前版本不符 → 拦下，以「超时未上传」进 R5 列表。
//   - 基版本非空但该同步标识在快照里已不存在（墓碑都回收了）→ 同上。这一条正是
//     复活风险本身。
//
// items 与 batch 同序，是**重同步之前**就建好的那一批上行项；被拦下的那一条要留住
// 的正是它——用户自己那一版。重同步刚刚把本地行覆盖成了 server 的内容，此刻再去读
// 本地行只会读回覆盖它的那一份，列表里那条「追回」点下去就变成把别人的内容再写
// 一遍。
func (s *service) reevaluateAfterResync(
	ctx context.Context, accountID int64, batch []*pending, items []syncwire.PushItem,
) ([]*pending, error) {
	survivors := make([]*pending, 0, len(batch))
	for i, p := range batch {
		if p.baseVersion == 0 {
			survivors = append(survivors, p)
			continue
		}
		version, _, found, err := syncstate_repo.SyncState().FindVersion(ctx, p.kind, p.syncID)
		if err != nil {
			return nil, err
		}
		if found && version == p.baseVersion {
			survivors = append(survivors, p)
			continue
		}
		lost := &syncqueue_entity.LostChange{
			EntityType:   p.kind,
			EntitySyncID: p.syncID,
			BaseVersion:  p.baseVersion,
			Reason:       syncqueue_entity.ReasonRejected,
			OccurredAt:   s.now(),
		}
		if i < len(items) {
			lost.PayloadJSON = string(items[i].Payload)
			lost.ProjectSyncID, lost.AgentredFingerprint = items[i].ProjectSyncID, items[i].AgentredFingerprint
		}
		if err := s.recordLostChange(ctx, accountID, lost); err != nil {
			return nil, err
		}
		if err := s.dropQueueRows(ctx, p.queueIDs); err != nil {
			return nil, err
		}
	}
	return survivors, nil
}

// resync 拉一份全量快照并以之为准（R6a）。
func (s *service) resync(ctx context.Context, accountID int64) error {
	return s.pullFrom(ctx, accountID, 0)
}

func (s *service) dropQueueRows(ctx context.Context, ids []int64) error {
	return syncqueue_repo.OutboundQueue().DeleteMany(ctx, ids)
}

func (s *service) recordLostChange(ctx context.Context, accountID int64, row *syncqueue_entity.LostChange) error {
	row.SyncAccountID = accountID
	if row.Createtime == 0 {
		row.Createtime = s.now()
	}
	if row.OccurredAt == 0 {
		row.OccurredAt = row.Createtime
	}
	return syncqueue_repo.LostChange().Create(ctx, row)
}
