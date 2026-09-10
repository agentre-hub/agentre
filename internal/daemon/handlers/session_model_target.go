package handlers

import (
	"context"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// SessionModelTargetDeps 是「改这条会话用哪个模型」这一个 RPC 的依赖。
type SessionModelTargetDeps struct {
	// Sessions 改写会话行上的 ModelTarget 两列。
	Sessions SessionModelTargetPort
	// LoggedInAccountID returns the daemon account allowed to name another peer.
	LoggedInAccountID func() string
}

// SessionModelTargetHandlers 实现改写 ModelTarget 这一个 RPC。无状态。
//
// 为什么承载执行的这一端也存这两格:同一条对话可以在桌面端与 agentred 上各有一份,
// 而此刻承载连接的那台未必是发起它的那台。浏览器换模型时两台都写,用户在哪一台
// 打开都看到自己刚选的那个。读的时候以发起端那一份为准,这一份是执行侧的那一份。
type SessionModelTargetHandlers struct {
	deps SessionModelTargetDeps
}

func NewSessionModelTargetHandlers(deps SessionModelTargetDeps) *SessionModelTargetHandlers {
	return &SessionModelTargetHandlers{deps: deps}
}

// SetModelTarget 把这条会话钉的 ModelTarget 写下去。三态见 agentrewire.SetModelTargetRequest:
// 两格都空是**要写下去的值**(改回跟随 Agent 绑定),不是「不改」。
//
// 会话不存在报错而不是折成成功 —— 与删除那条路径刻意不同:删一条已经不存在的会话
// 是幂等成功,而改一条不存在的会话的模型没有任何东西可以幂等,折成成功只会让调用方
// 以为下一轮会用新模型。
func (h *SessionModelTargetHandlers) SetModelTarget(
	ctx context.Context, req *agentrewire.SetModelTargetRequest,
) (*agentrewire.SetModelTargetResponse, error) {
	conversationID := req.GetConversationId()
	if err := ErrInvalidConversationID(conversationID); err != nil {
		return nil, err
	}
	peer, err := ResolveSessionPeer(ctx, devicefp.Initiator(req.GetPeerFingerprint()), h.deps.LoggedInAccountID)
	if err != nil {
		return nil, err
	}
	rows, err := h.deps.Sessions.SetModelTarget(ctx, peer, conversationID, req.GetProviderKey(), req.GetModelKey())
	if err != nil {
		return nil, fmt.Errorf("set session model target: %w", err)
	}
	if rows == 0 {
		return nil, rpcerror.ErrSessionNotFound
	}
	// 只记 key,不记人读名 —— 那要回查供应商目录,而这条路径上没有它。
	logger.Ctx(ctx).Info("handlers.SessionModelTargetHandlers.SetModelTarget: model target updated",
		zap.String("conversationId", conversationID),
		zap.String("providerKey", req.GetProviderKey()), zap.String("modelKey", req.GetModelKey()))
	return &agentrewire.SetModelTargetResponse{}, nil
}
