package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// OutputActivityHandler 处理「模型开始产出一个输出块」的纯计时信号。
//
// 它不带内容:不进 accumulator、不落库,只把信号原样转给前端 —— live 的「首 token」
// 是前端自己按 stream 事件算的,后端记了表而前端收不到,两边会在同一轮里各说各话
// (刷新前后数字不一致)。
//
// 记表这一半不在这里:它归 turn.Dispatcher(口径与映射见 internal/pkg/turnstats),
// 因为 agentred 的 fanout 要在没有 chat_svc 的前提下算出同一份数。
type OutputActivityHandler struct{}

func (OutputActivityHandler) Apply(ctx context.Context, _ agentruntime.Event, _ *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{"kind": "output_activity"})
	}
	return nil
}
