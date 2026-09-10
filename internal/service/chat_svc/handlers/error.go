package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// ErrorWriter handler 通过这个把 error text patch 到 assistantMsg **并落库**。
// 落库收在接口里而不是把整条 assistantMsg 交回去整行 Save,理由同 UsageWriter:
// 整行 Save 会把 MB 级的 blocks_json 一起重写,而这里只存一个字符串。
type ErrorWriter interface {
	WriteErrorText(ctx context.Context, msg any, errText string) error
}

type ErrorHandler struct {
	Writer ErrorWriter
}

// Apply 把错误信息写到 assistantMsg.ErrorText 并 emit StreamError。
// dispatcher 调完后,chat.go runTurn 会断开 stream(dispatcher 不直接负责关闭)。
func (h ErrorHandler) Apply(ctx context.Context, ev agentruntime.Event, _ *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	e := ev.(agentruntime.ErrorEvent)
	msg := ""
	if e.Err != nil {
		msg = e.Err.Error()
	}
	if tc != nil && h.Writer != nil && tc.AssistantMsg != nil && msg != "" {
		_ = h.Writer.WriteErrorText(context.WithoutCancel(ctx), tc.AssistantMsg, msg)
	}
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":  "error",
			"error": msg,
		})
	}
	return nil
}
