package handlers

import (
	"context"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
)

// SessionReasoningEffortDeps 是「改这条会话用多大思考力度」这一个 RPC 的依赖。
type SessionReasoningEffortDeps struct {
	// Sessions 改写会话行上的 ReasoningEffort 一格。
	Sessions SessionReasoningEffortPort
	// ClaimedAccountID returns the daemon account allowed to name another peer.
	ClaimedAccountID func() string
}

// SessionReasoningEffortHandlers 实现改写会话思考力度这一个 RPC。无状态。
//
// 为什么承载执行的这一端也存这一格:同一条对话可以在桌面端与 agentred 上各有一份,
// 而此刻承载连接的那台未必是发起它的那台。浏览器换档时两台都写,用户在哪一台打开
// 都看到自己刚选的那个。读的时候以发起端那一份为准,这一份是执行侧的那一份。
//
// 这一格与 ModelTarget 那两格一样**只供显示**:本轮真正用哪一档由 runtime.run 的
// run 参数决定(规格决策 4/5),执行路径不读它。
type SessionReasoningEffortHandlers struct {
	deps SessionReasoningEffortDeps
}

func NewSessionReasoningEffortHandlers(deps SessionReasoningEffortDeps) *SessionReasoningEffortHandlers {
	return &SessionReasoningEffortHandlers{deps: deps}
}

// SetReasoningEffort 把这条会话钉的思考力度写下去。两态见
// wire.SetSessionReasoningEffortParams:空串是**要写下去的值**(改回跟随后端配置),
// 不是「不改」。
//
// 会话不存在报错而不是折成成功 —— 与删除那条路径刻意不同:删一条已经不存在的会话
// 是幂等成功,而改一条不存在的会话的力度没有任何东西可以幂等,折成成功只会让调用方
// 以为下一轮会用新档位。
func (h *SessionReasoningEffortHandlers) SetReasoningEffort(
	ctx context.Context, p wire.SetSessionReasoningEffortParams,
) (wire.OK, error) {
	if err := ErrInvalidConversationID(p.ConversationID); err != nil {
		return wire.OK{}, err
	}
	peer, err := ResolveSessionPeer(ctx, p.PeerFingerprint, h.deps.ClaimedAccountID)
	if err != nil {
		return wire.OK{}, err
	}
	sid := p.ConversationID
	rows, err := h.deps.Sessions.SetReasoningEffort(ctx, peer, sid, p.ReasoningEffort)
	if err != nil {
		return wire.OK{}, fmt.Errorf("set session reasoning effort: %w", err)
	}
	if rows == 0 {
		return wire.OK{}, rpcerror.ErrSessionNotFound
	}
	logger.Ctx(ctx).Info("handlers.SessionReasoningEffortHandlers.SetReasoningEffort: reasoning effort updated",
		zap.String("conversationId", p.ConversationID),
		zap.String("reasoningEffort", p.ReasoningEffort))
	return wire.OK{}, nil
}
