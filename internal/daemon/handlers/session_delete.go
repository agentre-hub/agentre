// Package handlers — session_delete.go 实现 runtime.session.delete:把一条会话连同
// 它的整段通知日志从这台 daemon 上抹掉。
//
// 它与 session_catchup.go 分开是因为方向相反:补齐族只读存储、只读实时状态,明写着
// 「看一眼不该改变任何东西」;删除是这条 wire 上**第一个破坏性方法**,越界的代价不再
// 是「读到了不该读的」而是「删掉了别人的对话」。范围判定因此照搬同一个入口
// (ResolveSessionPeer),一个字都不放宽。
package handlers

import (
	"context"
	"fmt"
	"strconv"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-ai/agentre/internal/daemon/rpc"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// SessionDeleteDeps 是 SessionDeleteHandlers 的显式构造入参。
type SessionDeleteDeps struct {
	// Sessions 删会话的身份行。
	Sessions SessionDeletePort
	// Journal 清该会话的全部通知日志。
	Journal JournalPurgePort
	// ClaimedAccountID returns the daemon account allowed to name another peer.
	ClaimedAccountID func() string
}

// SessionDeleteHandlers 实现删除这一个 RPC。无状态。
type SessionDeleteHandlers struct {
	deps SessionDeleteDeps
}

// NewSessionDeleteHandlers 组装删除 handler。
func NewSessionDeleteHandlers(deps SessionDeleteDeps) *SessionDeleteHandlers {
	return &SessionDeleteHandlers{deps: deps}
}

// Delete 删掉调用方名下的那条会话:先删会话行,再清它的整段通知日志。
//
// 两步的顺序是硬的。反过来(先清日志、后删会话行)只要第二步失败,就会留下一条
// 「还在清单里、但 MAX(seq) 归零」的会话:此后 Append 从 1 重新分配 seq,而客户端游标
// 还停在旧高水位上,每条实时通知都被它当成重复丢弃 —— 会话没有跳号、没有错误地冻住。
// 按现在的顺序,中途失败留下的是一段没人引用的日志,下一次重试(server 那条删除待办
// 会重放)照样把它清掉:日志那一步**不因为会话行已经不在就跳过**,重试才收敛得了。
//
// 幂等:会话早就不在时删掉零行、照样报成功(见 wire.SessionDeleteResult 的说明)。
func (h *SessionDeleteHandlers) Delete(ctx context.Context, p wire.SessionDeleteParams) (wire.SessionDeleteResult, error) {
	if p.SessionID <= 0 {
		// 会话 id 是正整数主键。0 / 负数删不到任何东西,却会让调用方收到「删好了」。
		return wire.SessionDeleteResult{}, rpc.ErrInvalidParams
	}
	peer, err := ResolveSessionPeer(ctx, p.PeerFingerprint, h.deps.ClaimedAccountID)
	if err != nil {
		return wire.SessionDeleteResult{}, err
	}
	sid := strconv.FormatInt(p.SessionID, 10)
	rows, err := h.deps.Sessions.Delete(ctx, peer, sid)
	if err != nil {
		return wire.SessionDeleteResult{}, fmt.Errorf("delete session: %w", err)
	}
	purged, err := h.deps.Journal.DeleteAll(ctx, peer, sid)
	if err != nil {
		// 交出成功会让 server 把待办勾掉,那段转录就永远留在这台机器上了。
		return wire.SessionDeleteResult{}, fmt.Errorf("purge session journal: %w", err)
	}
	// 会话已经不存在了,它在本机常驻的 CLI 子进程再也不会被任何一轮用到:不放掉的话
	// 它只能等 idle 上限把自己挤出去,否则一直活到 daemon 退出。释放用的会话键与
	// runtime.run 交给 backend 的是同一个(按对端隔离),否则放的是别人那条同号会话。
	agentruntime.CloseSessionEverywhere(ctx, runtimeSessionID(peer, p.SessionID))
	logger.Ctx(ctx).Info("handlers.SessionDeleteHandlers.Delete: session removed",
		zap.Int64("sessionId", p.SessionID), zap.Int64("sessionRows", rows),
		zap.Int64("journalRows", purged))
	return wire.SessionDeleteResult{Deleted: true}, nil
}
