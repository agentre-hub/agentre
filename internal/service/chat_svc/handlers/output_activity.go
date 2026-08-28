package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// OutputActivityHandler 处理「模型开始产出一个输出块」的纯计时信号。
//
// 它不带内容:不进 accumulator、不落库,只记首 token(TTFT)并把信号原样转给前端 ——
// live 的「首 token」是前端自己按 stream 事件算的,后端记了表而前端收不到,两边会
// 在同一轮里各说各话(刷新前后数字不一致)。口径见 turn/timing.go。
type OutputActivityHandler struct{}

func (OutputActivityHandler) Apply(ctx context.Context, _ agentruntime.Event, _ *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	tc.NoteOutputToken()
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{"kind": "output_activity"})
	}
	return nil
}
