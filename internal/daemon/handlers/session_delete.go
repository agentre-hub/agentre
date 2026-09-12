// Package handlers — session_delete.go 实现 runtime.session.delete:把一条会话连同
// 它的全部转录从这台 daemon 上抹掉。
//
// 它与 session_catchup.go 分开是因为方向相反:补齐族只读存储、只读实时状态,明写着
// 「看一眼不该改变任何东西」;删除是这条 wire 上**第一个破坏性方法**,越界的代价不再
// 是「读到了不该读的」而是「删掉了别人的对话」。范围判定因此照搬同一个入口
// (ResolveSessionPeer),一个字都不放宽。
package handlers

import (
	"context"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// SessionDeleteDeps 是 SessionDeleteHandlers 的显式构造入参。
type SessionDeleteDeps struct {
	// Sessions 删会话的身份行。
	Sessions SessionDeletePort
	// Transcript 清该会话的全部转录(消息行 + 块行)。
	Transcript TranscriptPurgePort
	// LoggedInAccountID returns the daemon account allowed to name another peer.
	LoggedInAccountID func() string
}

// SessionDeleteHandlers 实现删除这一个 RPC。无状态。
type SessionDeleteHandlers struct {
	deps SessionDeleteDeps
}

// NewSessionDeleteHandlers 组装删除 handler。
func NewSessionDeleteHandlers(deps SessionDeleteDeps) *SessionDeleteHandlers {
	return &SessionDeleteHandlers{deps: deps}
}

// Delete 删掉调用方名下的那条会话:先清它的全部转录,再删会话行(顺序的理由见函数体)。
//
// 中途失败时 server 那条删除待办会重放:两步各自幂等,重试照样收敛。代价在重试之前:
// 转录清掉而会话行没删掉时,本会话的帧编号台账已经空了,之后新写的帧从 1 重新编号,
// 而客户端游标还停在旧高水位上。
//
// 幂等:会话早就不在时删掉零行、照样报成功。
func (h *SessionDeleteHandlers) Delete(ctx context.Context, req *agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
	conversationID := req.GetConversationId()
	if err := ErrInvalidConversationID(conversationID); err != nil {
		// 不是一条对话身份就删不到任何东西,却会让调用方收到「删好了」。
		return nil, err
	}
	peer, err := ResolveSessionPeer(ctx, devicefp.Initiator(req.GetPeerFingerprint()), h.deps.LoggedInAccountID)
	if err != nil {
		return nil, err
	}
	sid := conversationID
	// 先清转录、再删身份行:转录按**本机会话主键**挂靠,而收窄到调用方对端的那一步
	// 认的就是身份行(见 daemon.transcriptPurger)。反过来的话身份行先没了,收窄找不到
	// 会话,那段转录就成了没有主人的孤儿 —— 而会话号会被复用,它下一次会被当成新会话
	// 的历史读走,正是这条路径要防的东西。
	purged, err := h.deps.Transcript.DeleteAll(ctx, peer, sid)
	if err != nil {
		// 交出成功会让 server 把待办勾掉,那段转录就永远留在这台机器上了。
		return nil, fmt.Errorf("purge session transcript: %w", err)
	}
	rows, err := h.deps.Sessions.Delete(ctx, peer, sid)
	if err != nil {
		return nil, fmt.Errorf("delete session: %w", err)
	}
	// 会话已经不存在了,它在本机常驻的 CLI 子进程再也不会被任何一轮用到:不放掉的话
	// 它只能等 idle 上限把自己挤出去,否则一直活到 daemon 退出。释放用的会话键与
	// runtime.run 交给 backend 的是同一个,否则放的是另一条会话。
	agentruntime.CloseSessionEverywhere(ctx, runtimeSessionID(conversationID))
	logger.Ctx(ctx).Info("handlers.SessionDeleteHandlers.Delete: session removed",
		zap.String("conversationId", conversationID), zap.Int64("sessionRows", rows),
		zap.Int64("transcriptRows", purged))
	return &agentrewire.SessionDeleteResponse{Deleted: true}, nil
}
