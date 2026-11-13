package chat_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// delete_propagation.go 是「用户在桌面端删了一条对话」之后，把删除带到其它副本的那一段
// （spec 2026-11-12「删除传播 · 从桌面端删」）。只挂在用户路径 DeleteByUser 上：入站删除
// 走 Delete，只删本机（决策 10）。
//
// 其它副本只走一条路（决策 7/8）：已登录交给 server（它删自己那份，再让执行端删，执行端
// 离线时它自己记待办）；没登录且对话在一台配对的 agentred 上，直连它发 SESSION_DELETE。
// 请求发不出去就在本地记一笔待删除，联系上再补发（决策 12）。日志只记对话身份、机器指纹，
// 不记标题与内容。

// deletePropagation 交代一次用户删除里，本机以外每一份副本的去向。
type deletePropagation struct {
	// serverPending 已登录，但 server 那一份没删成：本地记了一笔 target=server 的待删除。
	serverPending bool
	// carrierFingerprint 执行这条对话的 agentred 实例指纹；本机执行的对话为空。
	carrierFingerprint devicefp.Carrier
	// carrierOffline 执行端那一份此刻没删成：server 答它离线（server 记了待办），或没登录
	// 时直连它失败（本地记了一笔 target=agentred 的待删除）。
	carrierOffline bool
}

// propagateDelete 在本机那一份已经删掉之后调用。它从不返回错误：别处删不掉不改变本机
// 已删的结果，只多一笔待删除。
func (s *chatSvc) propagateDelete(ctx context.Context, sess *chat_entity.Session) deletePropagation {
	var out deletePropagation
	if sess.ExecDeviceID > 0 {
		out.carrierFingerprint = sess.ExecDeviceFingerprint
	}
	log := logger.Ctx(ctx).With(
		zap.String("conversationId", sess.ConversationID),
		zap.String("machineFingerprint", string(out.carrierFingerprint)))
	if sess.ConversationID == "" {
		log.Warn("chat_svc.propagateDelete: session has no conversation id, nothing to propagate",
			zap.Int64("sessionId", sess.ID))
		return out
	}

	status, err := deleteSavedSession(ctx, sess.ConversationID, string(out.carrierFingerprint))
	switch {
	case err == nil:
		out.carrierOffline = status == server_svc.MachineStatusPending
		log.Info("chat_svc.propagateDelete: server deleted its copy", zap.String("machineStatus", status))
		return out
	case !isLoggedOut(err):
		log.Warn("chat_svc.propagateDelete: server delete failed, recording pending delete", zap.Error(err))
		s.recordPendingDelete(ctx, &chat_entity.PendingDelete{
			ConversationID:     sess.ConversationID,
			Target:             chat_entity.PendingDeleteTargetServer,
			MachineFingerprint: string(out.carrierFingerprint),
		})
		out.serverPending = true
		return out
	}

	// 没登录：本机执行的对话没有别的副本可删。
	if sess.ExecDeviceID <= 0 {
		return out
	}
	if err := s.deleteOnAgentred(ctx, sess.ExecDeviceID, sess.ConversationID); err != nil {
		log.Warn("chat_svc.propagateDelete: agentred delete failed, recording pending delete",
			zap.Int64("deviceId", sess.ExecDeviceID), zap.Error(err))
		s.recordPendingDelete(ctx, &chat_entity.PendingDelete{
			ConversationID:     sess.ConversationID,
			Target:             chat_entity.PendingDeleteTargetAgentred,
			MachineFingerprint: string(out.carrierFingerprint),
			DeviceID:           sess.ExecDeviceID,
		})
		out.carrierOffline = true
		return out
	}
	log.Info("chat_svc.propagateDelete: agentred deleted its copy", zap.Int64("deviceId", sess.ExecDeviceID))
	return out
}

// deleteSavedSession 经 server_svc 请 server 删除；server_svc 尚未接线等同于没登录。
func deleteSavedSession(ctx context.Context, conversationID, machineFingerprint string) (string, error) {
	srv := server_svc.Server()
	if srv == nil {
		return "", server_svc.ErrNotLoggedIn
	}
	return srv.DeleteSavedSession(ctx, conversationID, machineFingerprint)
}

// isLoggedOut 区分「没有登录、根本不该经 server」与「请求没发成」：前者走直连，不记
// server 待删除。凭据在这次请求里被 server 明确作废时，本机已经登出，同样按没登录处理。
func isLoggedOut(err error) bool {
	return errors.Is(err, server_svc.ErrNotLoggedIn) || server_svc.IsCredentialRejected(err)
}

// deleteOnAgentred 借那台配对 agentred 的连接发 SESSION_DELETE。发起端指纹留空：agentred
// 按调用方自己的指纹解析，配对桌面只能删自己发起的对话。
func (s *chatSvc) deleteOnAgentred(ctx context.Context, deviceID int64, conversationID string) error {
	lease, err := s.pool().Borrow(ctx, deviceID)
	if err != nil {
		return err
	}
	defer lease.Release()
	_, err = wirecall.SessionDelete(ctx, lease.Client(),
		&agentrewire.SessionDeleteRequest{ConversationId: conversationID})
	return err
}

// recordPendingDelete 记一笔待删除。记不下来时这份副本就再没有人去删了 —— 记 Error，
// 但不改变本机已删的结果。
func (s *chatSvc) recordPendingDelete(ctx context.Context, row *chat_entity.PendingDelete) {
	if err := chat_repo.PendingDelete().Create(ctx, row); err != nil {
		logger.Ctx(ctx).Error("chat_svc.recordPendingDelete: failed to record pending delete",
			zap.String("conversationId", row.ConversationID),
			zap.String("target", string(row.Target)),
			zap.String("machineFingerprint", row.MachineFingerprint),
			zap.Error(err))
	}
}
