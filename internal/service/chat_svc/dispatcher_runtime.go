package chat_svc

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// checkpointAssistantNew 中途 checkpoint(ToolResult 帧后调):把 acc.Snapshot
// 作为中途状态写回 DB(抗 abort,partial 状态可被刷新页面看到)。
func (s *chatSvc) checkpointAssistantNew(ctx context.Context, msg *chat_entity.Message, acc *turn.Accumulator) {
	if msg == nil || acc == nil {
		return
	}
	if err := msg.SetBlocks(acc.Snapshot()); err != nil {
		logger.Ctx(ctx).Warn("chat assistant checkpoint encode failed",
			zap.Int64("messageID", msg.ID),
			zap.Error(err))
		return
	}
	if err := chat_repo.Message().Update(context.WithoutCancel(ctx), msg); err != nil {
		logger.Ctx(ctx).Warn("chat assistant checkpoint persist failed",
			zap.Int64("messageID", msg.ID),
			zap.Error(err))
	}
}
