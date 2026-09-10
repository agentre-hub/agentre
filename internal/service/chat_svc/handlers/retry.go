package handlers

import (
	"context"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

type RetryHandler struct{}

// Apply 仅 emit retry 中间形态(no acc / no persist;spec §1.6)。
func (RetryHandler) Apply(ctx context.Context, ev agentruntime.Event, _ *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.Retry)
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":             "retry",
			"retryAttempt":     r.Attempt,
			"retryMaxAttempts": r.Max,
			"retryMessage":     r.Message,
			"retryDetails":     r.Details,
			"retryAt":          time.Now().UnixMilli(),
		})
	}
	return nil
}
