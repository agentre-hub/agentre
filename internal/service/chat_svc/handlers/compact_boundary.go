package handlers

import (
	"context"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// CompactInspector 抽象从 TurnContext.AssistantMsg 读 ID/Seq 的能力。
// chat_svc 注入具体实现(走 *chat_entity.Message 类型断言);单测可用 stub。
type CompactInspector interface {
	MessageID(msg any) int64
	MessageSeq(msg any) int
}

// CompactBoundaryHandler 处理 agentruntime.CompactBoundary 事件。
//
// 落点:在当前 assistant message blocks 末尾追加一条 CompactBoundaryBlock(走 acc)。
// 设计理由:compact_boundary 帧到达时 turn 已经有 assistant 消息正在累积,挂上去既
// 能让 LoadSession 重放重建 UI、又避免引入 role=system 这种新消息类型对 history 重
// 放路径的污染(CompactBoundaryBlock.Audience() = ToUI,不喂给 LLM)。
//
// emit "compact_boundary" 中间形态,dispatcherEmitter 转成 StreamCompactBoundary 推给前端。
type CompactBoundaryHandler struct {
	Inspector CompactInspector
}

func (h CompactBoundaryHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.CompactBoundary)
	at := time.Now().UnixMilli()
	blk := &blocks.CompactBoundaryBlock{PreTokens: r.PreTokens, Trigger: r.Trigger, At: at}
	if acc != nil {
		acc.AddBlock(blk, "")
	}
	if emit != nil {
		var msgID int64
		var seq int
		if h.Inspector != nil && tc != nil {
			msgID = h.Inspector.MessageID(tc.AssistantMsg)
			seq = h.Inspector.MessageSeq(tc.AssistantMsg)
		}
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":      "compact_boundary",
			"messageId": msgID,
			"seq":       seq,
			"preTokens": r.PreTokens,
			"trigger":   r.Trigger,
			"at":        at,
		})
	}
	return nil
}
