package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// RuntimeStatusHandler 处理 agentruntime.RuntimeStatus 事件。
//
// 不落 block —— runtime 状态是过渡态 (compacting 段一直生效到 compact_boundary 落定),
// 不入 history。仅 emit "runtime_status" 中间形态;dispatcherEmitter 转 StreamRuntimeStatus
// 给前端,Composer 据此显示 "正在压缩上下文…" chip。
//
// 守门:空 Status 直接 no-op (translator 已经过滤过,这里 defense in depth)。
type RuntimeStatusHandler struct{}

func (RuntimeStatusHandler) Apply(ctx context.Context, ev agentruntime.Event, _ *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.RuntimeStatus)
	if r.Status == "" {
		return nil
	}
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":       "runtime_status",
			"status":     r.Status,
			"compacting": r.Status == "compacting",
		})
	}
	return nil
}
