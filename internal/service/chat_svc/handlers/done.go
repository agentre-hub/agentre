package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

type DoneHandler struct{}

// Apply emit message_end 中间形态。实际 finalize(SetBlocks / Update / stream
// close)由 chat_svc runTurn 在 dispatcher 退出后统一做,handler 不直接 touch。
func (DoneHandler) Apply(ctx context.Context, _ agentruntime.Event, _ *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{"kind": "message_end"})
	}
	return nil
}
