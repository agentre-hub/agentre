package handlers

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// SessionCLIModelDeps 与模型目标同族，只保存执行端的会话设置镜像。
type SessionCLIModelDeps struct {
	Sessions          SessionCLIModelPort
	LoggedInAccountID func() string
}

// SessionCLIModelHandlers 保存三态；即时热切换由 runtime.setCLIModel 承担。
type SessionCLIModelHandlers struct{ deps SessionCLIModelDeps }

func NewSessionCLIModelHandlers(deps SessionCLIModelDeps) *SessionCLIModelHandlers {
	return &SessionCLIModelHandlers{deps: deps}
}

func (h *SessionCLIModelHandlers) SetCLIModel(ctx context.Context, req *agentrewire.SetSessionCLIModelRequest) (*agentrewire.SetSessionCLIModelResponse, error) {
	if err := ErrInvalidConversationID(req.GetConversationId()); err != nil {
		return nil, err
	}
	mode, value := strings.TrimSpace(req.GetMode()), strings.TrimSpace(req.GetValue())
	if !chat_entity.ValidCLIModelChoice(mode, value) {
		return nil, &rpcerror.Error{Code: rpcerror.CodeInvalidParams, Message: "invalid CLI model choice"}
	}
	peer, err := ResolveSessionPeer(ctx, devicefp.Initiator(req.GetPeerFingerprint()), h.deps.LoggedInAccountID)
	if err != nil {
		return nil, err
	}
	rows, err := h.deps.Sessions.SetCLIModel(ctx, peer, req.GetConversationId(), mode, value)
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		return nil, rpcerror.ErrSessionNotFound
	}
	logger.Ctx(ctx).Info("handlers.SessionCLIModelHandlers.SetCLIModel: session model updated", zap.String("conversationId", req.GetConversationId()), zap.String("mode", mode), zap.String("model", value))
	return &agentrewire.SetSessionCLIModelResponse{}, nil
}
