package handlers

import (
	"context"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// PlanWriter 抽象 PlanUpdated 事件的持久化落点。chat_svc 实现把它写到
// chat_svc.PlanBlock 上;ChatBlock 投影时再由 planBlockToChatBlock 转成
// canonical.PlanUpdate 渲染。
type PlanWriter interface {
	WritePlan(acc *turn.Accumulator, plan canonical.PlanUpdate)
}

type PlanUpdatedHandler struct {
	Writer PlanWriter
}

// Apply 把 canonical.PlanUpdate 写到 acc(走 Writer 兜底 PlanBlock) +
// emit plan_update。plan_update 与普通 chunk 分开,前端才能把它作为
// TaskProgressBar 数据源保存,而不是混进 assistant markdown 文本。
//
// Actions 由 runtime translator 自己决定是否携带;handler 不按 backend 类型合成,
// 只负责透传、持久化和 emit。无 actions 的计划更新只做进度/只读展示。
func (h PlanUpdatedHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.PlanUpdated)
	plan := r.Plan
	text := strings.TrimSpace(plan.Text)
	if h.Writer != nil {
		h.Writer.WritePlan(acc, plan)
	}
	if emit != nil && (text != "" || len(plan.Steps) > 0) {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":      "plan_update",
			"delta":     text,
			"canonical": plan,
		})
	}
	return nil
}
