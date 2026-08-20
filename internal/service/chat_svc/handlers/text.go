// Package handlers 一个 file 一个 agentruntime.Event 类型。Handler 不依赖 chat_svc
// 顶层(避免循环),emit 形态为 map[string]any{"kind": "<stream-name>", ...},chat_svc
// 在 dispatch wiring 层把这种"中间形态"映射成 ChatStreamEvent。
package handlers

import (
	"context"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/service/chat_svc/turn"
)

type TextDeltaHandler struct{}

func (TextDeltaHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	td := ev.(agentruntime.TextDelta)
	acc.AddText(td.Text)
	tc.NoteVisibleToken()
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{"kind": "chunk", "delta": td.Text})
	}
	return nil
}

type ThinkingDeltaHandler struct{}

func (ThinkingDeltaHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	td := ev.(agentruntime.ThinkingDelta)
	acc.AddThinking(td.Text)
	tc.NoteVisibleToken()
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{"kind": "thinking", "delta": td.Text})
	}
	return nil
}

// streamOf 取当前 turn 的 stream name;TurnContext 可能 nil(单测场景)。
func streamOf(tc *turn.TurnContext) string {
	if tc == nil {
		return ""
	}
	return tc.Stream
}
