package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// ContextWindowWriter handler 通过这个把 tokens 同步到 session.ContextWindow 并落库。
//
// 落库故意收在这个接口里(而不是再调一次 TurnContext.SessionUpdater):后者是整行回写,
// 会把调用方手上那份实体的每一列都写回去。**带外轮**(自主续轮 / 后台 subagent 活动轮)
// 手里的实体是它起步时读出的快照,用户在带外轮进行中发的新一轮刚写好的
// agent_status=running / last_message_at 会被这份旧快照原样拍回去,会话在库里退回 idle
// (sess-2974)。实现方只碰 context_window 一列。
type ContextWindowWriter interface {
	WriteContextWindow(ctx context.Context, sess any, tokens int) error
}

type ContextWindowUpdatedHandler struct {
	Writer ContextWindowWriter
}

// Apply 把 runtime 探到的 model context window 写回 session 字段 + emit patch。
// Tokens=0 视为"未探到",no-op。
func (h ContextWindowUpdatedHandler) Apply(ctx context.Context, ev agentruntime.Event, _ *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.ContextWindowUpdated)
	if r.Tokens <= 0 {
		return nil
	}
	if tc != nil && h.Writer != nil && tc.Session != nil {
		_ = h.Writer.WriteContextWindow(context.WithoutCancel(ctx), tc.Session, r.Tokens)
	}
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind": "session_status",
			"sessionStatus": map[string]any{
				"contextWindow": r.Tokens,
			},
		})
	}
	return nil
}
