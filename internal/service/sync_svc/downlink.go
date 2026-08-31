package sync_svc

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
)

// appliedKinds 是一轮下行里**真正落地**的对象类型。用集合而不是计数：界面据此
// 决定重拉哪几份数据，落了几条它不关心。
type appliedKinds map[string]struct{}

// announce 把这一轮的收获交给上层。没落地任何东西就不吭声 —— 30 秒一轮的轮询
// 大多数轮次都是空转，每轮都喊等于让界面每 30 秒白拉一遍项目树。
func (s *service) announce(ctx context.Context, landed appliedKinds) {
	if len(landed) == 0 {
		return
	}
	emit := s.getEmitter()
	if emit == nil {
		return
	}
	kinds := make([]string, 0, len(landed))
	for kind := range landed {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	logger.Ctx(ctx).Debug("sync_svc.announce: inbound landed", zap.Strings("kinds", kinds))
	emit(kinds)
}

// pull 按本端游标增量下行（R3：30 秒一轮）。
func (s *service) pull(ctx context.Context, accountID int64) error {
	st, err := s.loadCursor(ctx, accountID)
	if err != nil {
		return err
	}
	return s.pullFrom(ctx, accountID, st.Cursor)
}

// pullFrom 从给定游标开始翻页下行；cursor = 0 就是一份全量快照（R6a 的重同步）。
//
// 每一页落地完就推进游标：中途失败下次从这里继续，已经落地的行不会再来一遍
// （即便来了也被版本守卫挡掉）。
func (s *service) pullFrom(ctx context.Context, accountID, cursor int64) error {
	transport := s.getTransport()
	if transport == nil {
		return nil
	}
	var losses []*mergeLoss
	// 这一轮到底改变了本机什么。收工时交给 emitter —— 界面没有别的办法知道另一台
	// 设备刚建了个项目（项目树没有推送通道，此前靠项目页那条 1 秒轮询兜着）。
	landed := appliedKinds{}
	for page := 0; page < maxPullPages; page++ {
		p, err := transport.SyncPull(ctx, cursor, pullLimit)
		if err != nil {
			return err
		}
		for i := range p.Items {
			in := inboundOf(p.Items[i])
			loss, err := s.captureMergeLoss(ctx, in)
			if err != nil {
				return err
			}
			applied, err := s.applyInbound(ctx, accountID, in)
			if applied {
				landed[in.Kind] = struct{}{}
			}
			if err != nil {
				// 单条隔离：一行落不了地不该把整页连同游标一起卡住，否则下一轮
				// 从同一个游标拉回同一页、在同一行再断一次，那台机器从此收不到
				// 任何下行。留进暂缓队列：下一轮照常重试，30 天等不到就按 R2a
				// 进「没能同步的改动」，一行也不会被悄悄丢掉。
				if derr := s.deferFailed(ctx, accountID, in, err); derr != nil {
					return derr
				}
				continue
			}
			if loss != nil {
				losses = append(losses, loss)
			}
		}
		cursor = p.NextCursor
		if err := s.saveCursor(ctx, cursorState{
			AccountID: accountID, Cursor: cursor, LastSuccessAt: s.now(),
		}); err != nil {
			return err
		}
		// 拉到**空页**才收工，而不是拉到 HasMore=false 就收工（R6a）。
		//
		// server 把「这一页一行都没有」当作「这台设备的游标已经站在账号序列的头上」，
		// 并且只在那一刻刷新它的 last_sync_at；超窗口判定读的正是那个值。停在
		// 「最后一页非空」上，server 就永远看不到这台设备赶上进度 —— 对正常设备无所谓
		// （下一个周期的 pull 会拉回一个空页），但超窗口的设备等不到那个周期：flush
		// 失败就 return，pull 排在它后面，而 flush 正是因为超窗口才失败的。结果是
		// 「上行被拒 → 重同步 → 窗口没刷新 → 重试再被拒」的死循环，队列一条都出不去。
		//
		// 代价是每次真有增量的下行多一次往返（返回空页那一次）；空转的那些周期
		// 第一页就是空的，不受影响。翻页上限 maxPullPages 仍是兜底。
		if !p.HasMore && len(p.Items) == 0 {
			break
		}
	}
	if err := s.replayDeferred(ctx, accountID, landed); err != nil {
		return err
	}
	if err := s.recordMergeLosses(ctx, accountID, losses); err != nil {
		return err
	}
	if err := s.gcDeferred(ctx, accountID); err != nil {
		return err
	}
	if err := s.gcLostChanges(ctx, accountID); err != nil {
		return err
	}
	// 放在最后：迟到重放（replayDeferred）落地的那些行也算这一轮的收获。
	s.announce(ctx, landed)
	return nil
}

// naturalKeyed 是「账号内除同步标识之外还有一个自然键」的对象类型——今天只有
// 路径记录（项目同步标识, agentred 指纹）（决策 26）。R4b 的合并落败判定要问它：
// 这个自然键上现在站着的是哪一个同步标识。
type naturalKeyed interface {
	syncIDAtNaturalKey(ctx context.Context, in *inbound) (string, error)
}

// mergeLoss 是一条**可能**因自然键合并而被删掉的路径记录（R4b）。
type mergeLoss struct {
	kind          string
	syncID        string
	projectSyncID string
	fingerprint   string
	payload       json.RawMessage
}

// captureMergeLoss 在墓碑落地**之前**留住这一行的正文与自然键。
//
// 落败那一份的主人是另一台桌面端：server 只把 MergedSyncID 回给推上来的那一端，
// 它这边收到的仅仅是一份墓碑，和一次普通的远端删除长得一模一样。要分辨只能等
// 胜者也落地——所以这里只做「留证」，判定推迟到整轮下行结束（recordMergeLosses）。
func (s *service) captureMergeLoss(ctx context.Context, in *inbound) (*mergeLoss, error) {
	if !in.IsTombstone() {
		return nil, nil
	}
	ad, ok := s.adapters[in.Kind].(naturalKeyed)
	if !ok || ad == nil {
		return nil, nil
	}
	out, err := s.adapters[in.Kind].load(ctx, in.SyncID)
	if err != nil || out == nil {
		return nil, err
	}
	return &mergeLoss{
		kind: in.Kind, syncID: in.SyncID,
		projectSyncID: out.ProjectSyncID, fingerprint: out.AgentredFingerprint,
		payload: out.Payload,
	}, nil
}

// recordMergeLosses 落实 R4b 在**落败方**这一端的那一半：整轮下行结束后，如果那个
// 自然键上站着的已经是**另一个**同步标识的活行，本端刚被删掉的这一份就是被合并
// 掉的，按 R5 记一条「被覆盖」。
//
// 判定放在整轮之后而不是逐条：server 一次合并写两行——墓碑拿较小版本、胜者拿较大
// 版本，胜者可能落在下一页。自然键上没有别人站着就只是一次普通的远端删除，什么都
// 不记（「列表为空是常态」）。
func (s *service) recordMergeLosses(ctx context.Context, accountID int64, losses []*mergeLoss) error {
	for _, loss := range losses {
		ad, ok := s.adapters[loss.kind].(naturalKeyed)
		if !ok {
			continue
		}
		holder, err := ad.syncIDAtNaturalKey(ctx, &inbound{
			Kind: loss.kind, ProjectSyncID: loss.projectSyncID, AgentredFingerprint: loss.fingerprint,
		})
		if err != nil {
			return err
		}
		if holder == "" || holder == loss.syncID {
			continue
		}
		logger.Ctx(ctx).Info("sync_svc.recordMergeLosses: row lost the natural key merge",
			zap.String("kind", loss.kind), zap.String("syncId", loss.syncID),
			zap.String("keptSyncId", holder))
		if err := s.recordLostChange(ctx, accountID, &syncqueue_entity.LostChange{
			EntityType:          loss.kind,
			EntitySyncID:        loss.syncID,
			Reason:              syncqueue_entity.ReasonOverwritten,
			PayloadJSON:         string(loss.payload),
			ProjectSyncID:       loss.projectSyncID,
			AgentredFingerprint: loss.fingerprint,
			OccurredAt:          s.now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

// gcLostChanges 「没能同步的改动」保留 30 天（R5、决策 5），到期回收。与暂缓行的
// 回收同一个窗口、同一个节奏——两者都是 30 天承诺的一半，缺哪一半那句承诺都不成立。
func (s *service) gcLostChanges(ctx context.Context, accountID int64) error {
	rows, err := syncqueue_repo.LostChange().ListByAccount(ctx, accountID)
	if err != nil {
		return err
	}
	cutoff := s.now() - TombstoneWindow.Milliseconds()
	for _, row := range rows {
		if row.Createtime > cutoff {
			continue
		}
		if err := syncqueue_repo.LostChange().Delete(ctx, row.ID); err != nil {
			return err
		}
		logger.Ctx(ctx).Info("sync_svc.gcLostChanges: lost change expired",
			zap.String("kind", row.EntityType), zap.String("syncId", row.EntitySyncID),
			zap.String("reason", row.Reason))
	}
	return nil
}

// applyInbound 落地一条下行项。
//
// 两道闸：**版本守卫**（本机已有同版本或更新的版本就不再落——重复投递只应用一次，
// 任意到达顺序下结果相同，R4/R7）与**引用守卫**（引用目标还没到就暂缓落地，绝不写
// 悬空引用，R2a）。引用守卫只管落地，不管删除——见下面 in.IsTombstone() 那一段。
// 返回值是「本机有没有真的因此改变」：版本守卫挡下的重复投递、无处可删的墓碑、
// 等引用而暂缓的那些都是 false —— 界面据此决定要不要重拉，空转的轮次不该惊动它。
func (s *service) applyInbound(ctx context.Context, accountID int64, in *inbound) (bool, error) {
	ad := s.adapters[in.Kind]
	if ad == nil {
		return false, nil
	}
	version, _, found, err := syncstate_repo.SyncState().FindVersion(ctx, in.Kind, in.SyncID)
	if err != nil {
		return false, err
	}
	if found && version >= in.Version {
		return false, nil
	}
	if in.IsTombstone() {
		// 墓碑一到，同一个同步标识压在暂缓队列里的旧副本立刻作废。
		//
		// 少了这一步就有一条永久的复活路径：成员关系与执行目标是硬删，行没了之后
		// SaveMeta 那条 `UPDATE … WHERE sync_id = ?` 命中 0 行，同步元数据落不下去，
		// 版本守卫对它们失忆；此后重放那份旧副本会把删掉的行原样建回来，而游标早已
		// 越过这两版，谁也不会再纠正它（R6：删除不被复活）。
		if err := s.dropDeferred(ctx, accountID, in.Kind, in.SyncID); err != nil {
			return false, err
		}
	}
	if in.IsTombstone() && !found {
		// 本机从来没有这一行：墓碑没有可删的东西，也不必为它等引用目标到达
		// ——把删除挂进暂缓队列只会白等 30 天（R2a/R6）。
		return false, nil
	}

	if in.IsTombstone() {
		// 删除不过引用守卫：adapter.remove 只按同步标识找本机那一行，一个引用也
		// 不写（resolved 它根本不收）。让它等引用目标到达，等于本机没配对那台
		// agentred 时一条 backend 的墓碑要在暂缓队列里空等 30 天再被当成「引用
		// 丢失」丢掉——那一行在本机永远删不掉，而 R6 说删除必须到达各端。
		if err := ad.remove(ctx, in); err != nil {
			return false, err
		}
		return true, s.saveInboundMeta(ctx, accountID, in)
	}

	resolved, missing, err := resolveRefs(ctx, ad.refs(in))
	if errors.Is(err, errRefMissing) {
		return false, s.defer_(ctx, accountID, in, missing.key())
	}
	if err != nil {
		return false, err
	}

	if err := ad.apply(ctx, in, resolved); err != nil {
		if errors.Is(err, errRefMissing) {
			return false, s.defer_(ctx, accountID, in, "")
		}
		return false, err
	}
	return true, s.saveInboundMeta(ctx, accountID, in)
}

// saveInboundMeta 记下这一行已经消费到哪一版（版本守卫下一次靠它）。
func (s *service) saveInboundMeta(ctx context.Context, accountID int64, in *inbound) error {
	return syncstate_repo.SyncState().SaveMeta(ctx, in.Kind, in.SyncID, syncmeta_entity.SyncMeta{
		SyncID:                in.SyncID,
		SyncAccountID:         accountID,
		SyncVersion:           in.Version,
		SyncUpdatedAt:         in.UpdatedAt,
		SyncOriginFingerprint: in.OriginFingerprint,
		// 删除时刻由发起端记下、随下行原样到达（决策 20）：server 不再把墓碑压成布尔，
		// 本端也就不必在落地时另编一个「现在」。
		SyncDeletedAt: in.DeletedAt,
	})
}

// defer_ 把一条暂缓落地的行存进入站队列（R2a）：保留 30 天，等引用目标到达后完成。
// 同一个同步标识只留最新的一份。
func (s *service) defer_(ctx context.Context, accountID int64, in *inbound, missing string) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	rows, err := syncqueue_repo.InboundQueue().ListByAccount(ctx, accountID)
	if err != nil {
		return err
	}
	receivedAt := s.now()
	for _, row := range rows {
		if row.EntityType == in.Kind && row.EntitySyncID == in.SyncID {
			// 保留最早一次收到的时间：30 天窗口从「第一次等不到」开始算。
			if row.ReceivedAt > 0 && row.ReceivedAt < receivedAt {
				receivedAt = row.ReceivedAt
			}
			if err := syncqueue_repo.InboundQueue().Delete(ctx, row.ID); err != nil {
				return err
			}
		}
	}
	logger.Ctx(ctx).Debug("sync_svc.defer: reference has not arrived, holding row",
		zap.String("kind", in.Kind), zap.String("syncId", in.SyncID),
		zap.String("missingRef", missing))
	return syncqueue_repo.InboundQueue().Create(ctx, &syncqueue_entity.InboundQueueItem{
		SyncAccountID: accountID,
		EntityType:    in.Kind,
		EntitySyncID:  in.SyncID,
		PayloadJSON:   string(body),
		MissingSyncID: missing,
		ReceivedAt:    receivedAt,
	})
}

// replayDeferred 重试暂缓落地的行：引用目标可能刚刚随这一轮下行到达。一轮里只要
// 有一条落地成功，就再来一轮——A 依赖 B、B 依赖 C 的链条靠这个补齐。
//
// 它走的是与 applyInbound 同一条路（同样的版本守卫、同样的引用守卫、同样的删除
// 例外），区别只在失败之后：暂缓的行**留在队列里**等下一轮，不往上抛——一条重试
// 不成功的行不该把同一轮里其它行的重放也一起中断。
func (s *service) replayDeferred(ctx context.Context, accountID int64, landed appliedKinds) error {
	for round := 0; round < 8; round++ {
		rows, err := syncqueue_repo.InboundQueue().ListByAccount(ctx, accountID)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		progressed := false
		for _, row := range rows {
			in := &inbound{}
			if err := json.Unmarshal([]byte(row.PayloadJSON), in); err != nil {
				// 存坏了的行没有重试价值，直接丢。
				if derr := syncqueue_repo.InboundQueue().Delete(ctx, row.ID); derr != nil {
					return derr
				}
				continue
			}
			if s.adapters[in.Kind] == nil {
				continue
			}
			// 走 applyInbound 而不是自己再解析一遍引用：版本守卫也因此对重放生效。
			// 少了它，一次迟到的重放会把已经落地的更新版本盖回旧版本，而游标早已
			// 越过那一版——被盖掉的内容再也不会被重新投递，回退是永久的。
			applied, aerr := s.applyInbound(ctx, accountID, in)
			if applied {
				landed[in.Kind] = struct{}{}
			}
			if aerr != nil {
				logger.Ctx(ctx).Error("sync_svc.replayDeferred: row still cannot land, keeping it queued",
					zap.String("kind", in.Kind), zap.String("syncId", in.SyncID), zap.Error(aerr))
				continue
			}
			// applyInbound 把「还差引用」重新挂了一条暂缓行；那条替换掉这一行，
			// 这一行照常删掉（defer_ 已经保留了最早那次的 ReceivedAt）。
			if err := syncqueue_repo.InboundQueue().Delete(ctx, row.ID); err != nil {
				return err
			}
			if !s.stillDeferred(ctx, accountID, in) {
				progressed = true
			}
		}
		if !progressed {
			return nil
		}
	}
	return nil
}

// dropDeferred 清掉某个同步标识在暂缓队列里的全部行。
func (s *service) dropDeferred(ctx context.Context, accountID int64, kind, syncID string) error {
	rows, err := syncqueue_repo.InboundQueue().ListByAccount(ctx, accountID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.EntityType != kind || row.EntitySyncID != syncID {
			continue
		}
		if err := syncqueue_repo.InboundQueue().Delete(ctx, row.ID); err != nil {
			return err
		}
		logger.Ctx(ctx).Debug("sync_svc.dropDeferred: tombstone superseded a deferred row",
			zap.String("kind", kind), zap.String("syncId", syncID))
	}
	return nil
}

// stillDeferred 报告这一行是不是又被挂回了暂缓队列（引用目标还是没到）。
func (s *service) stillDeferred(ctx context.Context, accountID int64, in *inbound) bool {
	rows, err := syncqueue_repo.InboundQueue().ListByAccount(ctx, accountID)
	if err != nil {
		return true
	}
	for _, row := range rows {
		if row.EntityType == in.Kind && row.EntitySyncID == in.SyncID {
			return true
		}
	}
	return false
}

// deferFailed 把一条落不了地的下行行留进暂缓队列，让整页的其余部分继续。
//
// 不是「丢掉」：下一轮 replayDeferred 照常重试，30 天还落不了地就按 R2a 进
// 「没能同步的改动」——用户看得见，一行也没有被悄悄吞掉。
func (s *service) deferFailed(ctx context.Context, accountID int64, in *inbound, cause error) error {
	logger.Ctx(ctx).Error("sync_svc.pullFrom: row failed to land, holding it and continuing the page",
		zap.String("kind", in.Kind), zap.String("syncId", in.SyncID),
		zap.Int64("version", in.Version), zap.Error(cause))
	return s.defer_(ctx, accountID, in, "")
}

// gcDeferred 超过 30 天仍然等不到引用目标的行整行丢弃，并以「引用丢失」进 R5 的
// 列表（R2a）。
func (s *service) gcDeferred(ctx context.Context, accountID int64) error {
	rows, err := syncqueue_repo.InboundQueue().ListByAccount(ctx, accountID)
	if err != nil {
		return err
	}
	cutoff := s.now() - TombstoneWindow.Milliseconds()
	for _, row := range rows {
		if row.ReceivedAt > cutoff {
			continue
		}
		lost := &syncqueue_entity.LostChange{
			EntityType:   row.EntityType,
			EntitySyncID: row.EntitySyncID,
			Reason:       syncqueue_entity.ReasonDiscarded,
			OccurredAt:   s.now(),
		}
		// 入站队列存的是整个 inbound 信封；列表要展示与恢复的是**正文**，恢复走的
		// 又是 adapter.apply——把信封喂给它只会解出一个空对象。这里拆一次信封，
		// 顺带留住不在正文里的那两项自然键（R5a）。
		var env inbound
		if err := json.Unmarshal([]byte(row.PayloadJSON), &env); err == nil {
			lost.PayloadJSON = string(env.Payload)
			lost.ProjectSyncID, lost.AgentredFingerprint = env.ProjectSyncID, env.AgentredFingerprint
			lost.BaseVersion = env.Version
		} else {
			lost.PayloadJSON = row.PayloadJSON
		}
		if err := s.recordLostChange(ctx, accountID, lost); err != nil {
			return err
		}
		if err := syncqueue_repo.InboundQueue().Delete(ctx, row.ID); err != nil {
			return err
		}
		logger.Ctx(ctx).Info("sync_svc.gcDeferred: deferred row expired",
			zap.String("kind", row.EntityType), zap.String("syncId", row.EntitySyncID))
	}
	return nil
}

// ── 账号级实时通道：第二个下行触发源 ───────────────────────────────────────

// watchAccountChannel 把账号级实时通道（server 的 GET /v1/account/channel）接成
// 30 秒轮询之外的**第二个**下行触发源。通道上只流信号「这个账号的同步版本推进了」，
// 不流对象内容，因此收到之后照常走 SyncOnce（规格「实时通道只送信号，不送数据」）。
//
// 这条通道的设计前提就是它可以不可靠：
//
//   - 出入口根本没有它（单机构建、旧版 server、测试替身）：直接返回，只剩轮询，
//     而那本身是一个完整可用的形态；
//   - 连不上 / 断开：隔 accountChannelRetry 再试一次，不重试到底、不阻塞任何操作；
//   - 建连成功（首次与重连一视同仁）：立刻主动 Pull 一次，而不是等服务端补发——
//     通道不保存未送达的信号，断线期间的变更由这一次 Pull 补齐；
//   - 漏帧、乱序、重复：都无害。版本号只用于「该拉了」的判断，拉哪些由本端自己的
//     游标决定（cursor.go），重复信号最多多拉一页空的。
func (s *service) watchAccountChannel(ctx context.Context) {
	dialer, ok := s.getTransport().(AccountChannelDialer)
	if !ok {
		return
	}
	for ctx.Err() == nil {
		// 每一次拨号挂在自己的 ctx 上，DropAccountChannel 据此把**这一条**连接踢掉：
		// 连接在建起来的那一刻就把服务端地址与设备凭据钉死了，登录身份一变它就该走。
		dialCtx, cancel := context.WithCancel(ctx)
		s.setChannelCancel(cancel)
		signals, err := dialer.DialAccountChannel(dialCtx)
		if err != nil {
			cancel()
			// 连不上不是同步失败：不进 lastErr、不影响轮询的退避，界面上什么都不该变。
			logger.Ctx(ctx).Debug("sync_svc.watchAccountChannel: unavailable, polling only",
				zap.Error(err))
			if !s.waitBeforeRedial(ctx) {
				return
			}
			continue
		}
		s.syncOnceForSignal(ctx, "connected")
		s.consumeAccountSignals(dialCtx, signals)
		cancel()
		if !s.waitBeforeRedial(ctx) {
			return
		}
	}
}

// setChannelCancel 记下当前这一条连接的取消钩子，替换掉上一条的。
func (s *service) setChannelCancel(cancel context.CancelFunc) {
	s.mu.Lock()
	s.channelCancel = cancel
	s.mu.Unlock()
}

// DropAccountChannel 断开当前的账号级实时通道并立刻重连。
//
// 登录状态一变就要调它（app 层在 logged_in / logged_out 上调，与中继登记同一处）：
// 通道拨号时钉死的地址与凭据都跟着变了，而 gorilla 的读循环只认连接断开，不认
// 「身份过期」——不主动踢，这条常连会一直挂在上一套 server 上,新 server 的通道
// 永远拨不起来，实时下行静默退化成 30 秒轮询。
//
// 重连不等那 30 秒的重连窗口：这次断开不是故障，而是我们自己知道该换一条了。
func (s *service) DropAccountChannel() {
	s.mu.Lock()
	cancel := s.channelCancel
	s.channelCancel = nil
	s.channelRedialNow = true
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// takeRedialNow 取走并清掉「立刻重连」的标记。
func (s *service) takeRedialNow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.channelRedialNow
	s.channelRedialNow = false
	return now
}

// consumeAccountSignals 消费一条已经建起来的信号流，直到它断开或收工。
func (s *service) consumeAccountSignals(ctx context.Context, signals <-chan syncwire.AccountChannelFrame) {
	for {
		select {
		case <-ctx.Done():
			return
		case frame, ok := <-signals:
			if !ok {
				// 断开。回到外层重连，重连成功会再主动 Pull 一次补齐这段空窗。
				return
			}
			if frame.Type != syncwire.AccountChannelSyncVersion {
				// 通道日后会承载别的通知，帧上带类型标记正是为此：不认识的种类忽略，
				// 但**不断连**——旧客户端不该被一条新通知踢下线。
				continue
			}
			s.syncOnceForSignal(ctx, "signal")
		}
	}
}

// syncOnceForSignal 跑一次通道触发的同步。失败只记日志：轮询会照常再试，通道上的
// 一次失败没有资格打断这条常连。
func (s *service) syncOnceForSignal(ctx context.Context, cause string) {
	if err := s.SyncOnce(ctx); err != nil {
		logger.Ctx(ctx).Debug("sync_svc.watchAccountChannel: triggered sync failed",
			zap.String("cause", cause), zap.Error(err))
	}
}

// waitBeforeRedial 等到该重连了；返回 false 表示该收工。
//
// DropAccountChannel 要求的重连不等：那次断开是我们自己发起的，等一个为「故障」
// 设计的窗口只会让新身份白白晚 30 秒才连上。
func (s *service) waitBeforeRedial(ctx context.Context) bool {
	if s.takeRedialNow() {
		return ctx.Err() == nil
	}
	if wait := s.channelRetryWait; wait != nil {
		return wait(ctx)
	}
	return waitAccountChannelRetry(ctx)
}

// waitAccountChannelRetry 是两次拨号之间的等待（生产时钟）。
func waitAccountChannelRetry(ctx context.Context) bool {
	timer := time.NewTimer(accountChannelRetry)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// accountChannelRetry 是通道断开或连不上之后，隔多久再试一次。与轮询周期同一个
// 节奏：通道只是优化，重连不该比兜底本身还急。
const accountChannelRetry = PollInterval
