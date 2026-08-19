package sync_svc

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-ai/agentre/internal/repository/syncqueue_repo"
	"github.com/agentre-ai/agentre/internal/repository/syncstate_repo"
)

// OriginDeviceServer 是「这一版是被**服务端**覆盖掉的」这个来源标识。
//
// server 的组织管理面让浏览器直接建 / 改 / 删组织架构，那些行在 server 上记的
// SourceDeviceID 是 0（server 侧决策 21：0 = 不是任何一台设备推上来的）。冲突应答
// 因此会带回 0，而 R5 要向用户交代覆盖方是谁 —— 记成 "0" 就成了「设备 #0」这台
// 并不存在的机器，记成空串又会落到「不知道被谁」那一支。这个哨兵值让呈现层能把
// 它译成「服务端（浏览器）」（sync.lostChanges.originServer）。
//
// 它不会与真的设备撞：这个字段只由 originDeviceOf 产出，另一支永远是十进制数字。
const OriginDeviceServer = "server"

// originDeviceOf 把冲突应答里的来源设备标识翻成留存记录里的那一格。
func originDeviceOf(deviceID int64) string {
	if deviceID == 0 {
		return OriginDeviceServer
	}
	return strconv.FormatInt(deviceID, 10)
}

// Status 同步区要展示的状态。未登录时 Enabled 为 false —— 界面据此让整个同步项
// 不存在（R12）。
func (s *service) Status(ctx context.Context) (*Status, error) {
	accountID, _, ok := s.account(ctx)
	if !ok {
		return &Status{}, nil
	}
	st, err := s.loadCursor(ctx, accountID)
	if err != nil {
		return nil, err
	}
	outbound, err := syncqueue_repo.OutboundQueue().ListByAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	deferred, err := syncqueue_repo.InboundQueue().ListByAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	lost, err := syncqueue_repo.LostChange().ListByAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return &Status{
		Enabled:         true,
		AccountID:       accountID,
		Cursor:          st.Cursor,
		LastSuccessAt:   st.LastSuccessAt,
		PendingCount:    len(outbound) + len(deferred),
		DeferredCount:   len(deferred),
		LostChangeCount: len(lost),
		LastError:       s.getLastErr(),
	}, nil
}

// ListLostChanges 「没能同步的改动」列表（R5）：三种原因共用一份，只列当前账号的
// ——上一个账号的记录留在本地，但不在新账号下展示（R13a）。
func (s *service) ListLostChanges(ctx context.Context) ([]*LostChangeView, error) {
	accountID, _, ok := s.account(ctx)
	if !ok {
		return []*LostChangeView{}, nil
	}
	rows, err := syncqueue_repo.LostChange().ListByAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]*LostChangeView, 0, len(rows))
	for _, row := range rows {
		out = append(out, &LostChangeView{
			ID:           row.ID,
			EntityType:   row.EntityType,
			EntitySyncID: row.EntitySyncID,
			Reason:       row.Reason,
			PayloadJSON:  row.PayloadJSON,
			OriginDevice: row.OriginDevice,
			BaseVersion:  row.BaseVersion,
			OccurredAt:   row.OccurredAt,
		})
	}
	return out, nil
}

// RestoreLostChange 把某一版恢复成当前值：这是一次普通的本地修改，按 R3 正常上行。
//
// 目标已经在别处被删除时明确失败（R5a）——本机已经收到那份墓碑，或者上行时被
// server 按基版本判定拒绝。两种情况都返回 TargetDeleted，界面据此说明「恢复不会
// 让它回来」并给出「按这份内容新建」。
func (s *service) RestoreLostChange(ctx context.Context, id int64) (*RestoreOutcome, error) {
	accountID, _, ok := s.account(ctx)
	if !ok {
		return nil, ErrNotLoggedIn
	}
	row, err := s.findLostChange(ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	ad := s.adapters[row.EntityType]
	if ad == nil {
		return nil, ErrLostChangeNotFound
	}

	_, deleted, found, err := syncstate_repo.SyncState().FindVersion(ctx, row.EntityType, row.EntitySyncID)
	if err != nil {
		return nil, err
	}
	if found && deleted {
		return &RestoreOutcome{TargetDeleted: true}, nil
	}

	in := inboundOfLostChange(row, row.EntitySyncID)
	if err := s.applyLocally(ctx, ad, in); err != nil {
		return nil, err
	}

	// 恢复后的这一行按本端当前见到的版本上行；server 若判它已是墓碑，上行结果会
	// 把本机也置成墓碑（acceptRemoteTombstone），下面再查一次即可分辨。
	version, _, _, err := syncstate_repo.SyncState().FindVersion(ctx, row.EntityType, row.EntitySyncID)
	if err != nil {
		return nil, err
	}
	s.NotifyLocalChange(ctx, LocalChange{
		Kind: row.EntityType, Op: OpUpdate,
		Meta: syncmeta_entity.SyncMeta{
			SyncID: row.EntitySyncID, SyncAccountID: accountID, SyncVersion: version,
		},
	})
	if err := s.SyncOnce(ctx); err != nil {
		// 服务端此刻不可达：改动已经落在本地并留在队列里，联网后补齐（R7）。
		return &RestoreOutcome{Restored: true}, syncqueue_repo.LostChange().Delete(ctx, row.ID)
	}
	_, deleted, found, err = syncstate_repo.SyncState().FindVersion(ctx, row.EntityType, row.EntitySyncID)
	if err != nil {
		return nil, err
	}
	if found && deleted {
		return &RestoreOutcome{TargetDeleted: true}, nil
	}
	return &RestoreOutcome{Restored: true}, syncqueue_repo.LostChange().Delete(ctx, row.ID)
}

// RecreateFromLostChange 按这份内容新建一个对象（R5a）：分配新的同步标识，作为一个
// 新对象正常上行——它不是复活，原来那个仍然是墓碑（R6 不被破坏）。
func (s *service) RecreateFromLostChange(ctx context.Context, id int64) error {
	accountID, _, ok := s.account(ctx)
	if !ok {
		return ErrNotLoggedIn
	}
	row, err := s.findLostChange(ctx, accountID, id)
	if err != nil {
		return err
	}
	ad := s.adapters[row.EntityType]
	if ad == nil {
		return ErrLostChangeNotFound
	}
	in := inboundOfLostChange(row, syncmeta_entity.NewSyncID())
	if err := s.applyLocally(ctx, ad, in); err != nil {
		return err
	}
	s.NotifyLocalChange(ctx, LocalChange{
		Kind: row.EntityType, Op: OpCreate,
		Meta: syncmeta_entity.SyncMeta{SyncID: in.SyncID, SyncAccountID: accountID},
	})
	return syncqueue_repo.LostChange().Delete(ctx, row.ID)
}

// DiscardLostChange 丢掉一条记录（用户看过了、不想要了）。
func (s *service) DiscardLostChange(ctx context.Context, id int64) error {
	accountID, _, ok := s.account(ctx)
	if !ok {
		return ErrNotLoggedIn
	}
	row, err := s.findLostChange(ctx, accountID, id)
	if err != nil {
		return err
	}
	return syncqueue_repo.LostChange().Delete(ctx, row.ID)
}

// inboundOfLostChange 把一条留存记录翻回落地用的下行项。**自然键必须一起带上**
// （R5a）：路径记录靠 (项目同步标识, agentred 指纹) 才解析得出归属，远端 backend
// 靠指纹才不会被当成本机的——这两项不在正文里，是上行项的顶层字段（决策 26）。
func inboundOfLostChange(row *syncqueue_entity.LostChange, syncID string) *inbound {
	return &inbound{
		Kind:                row.EntityType,
		SyncID:              syncID,
		ProjectSyncID:       row.ProjectSyncID,
		AgentredFingerprint: row.AgentredFingerprint,
		Payload:             json.RawMessage(row.PayloadJSON),
	}
}

// applyLocally 把一份留存的正文落成本地行。引用目标缺失时明确失败——恢复是一次
// 用户主动的动作，不该悄悄变成一条暂缓落地的行。
func (s *service) applyLocally(ctx context.Context, ad adapter, in *inbound) error {
	resolved, _, err := resolveRefs(ctx, ad.refs(in))
	if err != nil {
		return err
	}
	return ad.apply(ctx, in, resolved)
}

func (s *service) findLostChange(
	ctx context.Context, accountID, id int64,
) (*syncqueue_entity.LostChange, error) {
	rows, err := syncqueue_repo.LostChange().ListByAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return nil, ErrLostChangeNotFound
}
