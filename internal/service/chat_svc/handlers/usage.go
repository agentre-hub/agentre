package handlers

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// UsageWriter handler 通过这个把 per-call usage patch 到 assistantMsg **并落库**。
// chat_svc 在 wire 时实现:把 ev.Usage 字段往 *chat_entity.ChatMessage 上 patch。
// MessageID 返回 assistantMsg.ID,emit 时附带让前端按消息匹配。
//
// 落库故意收在这个接口里(而不是再调一次 TurnContext.MessageUpdater):后者是整行
// Save,会把实体上那份**已累积的 blocks_json** 一起写回去。usage 帧是每个 API call
// 一帧,而 blocks_json 在长轮次里是 MB 级的(实测单行最大 12.9 MB),于是「存 6 个
// 整数」被放大成「重写几 MB」×几十次。实现方只碰 token 那几列。
// 与 ContextWindowWriter 同源的理由,只是这里的载荷大三个量级。
type UsageWriter interface {
	WriteUsage(ctx context.Context, msg any, u *agentruntime.UsageUpdate) error
	MessageID(msg any) int64
}

type UsageUpdateHandler struct {
	// Writer 可选,nil 时仅 emit。chat_svc 在 dispatcher Register 时注入。
	Writer UsageWriter
}

// Apply 把 per-call usage 写回 assistantMsg 并 emit StreamUsage 中间形态。
// 落库由 Writer 自己完成(context.WithoutCancel 抗 abort,spec §1.4)。
func (h UsageUpdateHandler) Apply(ctx context.Context, ev agentruntime.Event, _ *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	u := ev.(agentruntime.UsageUpdate)
	if u.Usage == nil {
		return nil
	}
	if tc != nil && h.Writer != nil && tc.AssistantMsg != nil {
		_ = h.Writer.WriteUsage(context.WithoutCancel(ctx), tc.AssistantMsg, &u)
	}
	if emit != nil {
		var msgID int64
		if tc != nil && h.Writer != nil && tc.AssistantMsg != nil {
			msgID = h.Writer.MessageID(tc.AssistantMsg)
		}
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind": "usage",
			"usage": map[string]any{
				"messageId":           msgID,
				"promptTokens":        u.Usage.PromptTokens,
				"completionTokens":    u.Usage.CompletionTokens,
				"cachedTokens":        u.Usage.CachedTokens,
				"cacheCreationTokens": u.Usage.CacheCreationTokens,
				"reasoningTokens":     u.Usage.ReasoningTokens,
				"totalInputTokens":    u.TotalInputTokens,
				"contextWindow":       u.ContextWindow,
			},
		})
	}
	return nil
}
