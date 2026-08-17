// PlanBlock 是 plan 工具的持久化形式 —— 落库走 dispatcher_adapters.planWriterAdapter,
// 读回则在 toChatMessage 里用 planBlockToChatBlock 投影成 ChatBlock + canonical.PlanUpdate
// 让前端 TaskProgressBar/PlanCard 消费。formatPlanText / planStatusMarker 提供 Markdown 兜底文本,
// 用在没有显式 text 字段的旧 plan 写入。
package chat_svc

import (
	"strings"

	"github.com/cago-frame/agents/agent/blocks"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-ai/agentre/internal/service/chat_svc/view"
)

type PlanBlock struct {
	Steps   []PlanStepDTO          `json:"steps"`
	Text    string                 `json:"text,omitempty"`
	Actions []canonical.PlanAction `json:"actions,omitempty"`
}

func (PlanBlock) Type() string                  { return "plan" }
func (PlanBlock) Audience() blocks.AudienceMask { return blocks.ToUI }

func init() {
	blocks.RegisterFactory[PlanBlock]()
}

type PlanStepDTO struct {
	Step   string `json:"step"`
	Status string `json:"status"`
}

func planBlockToChatBlock(b PlanBlock) ChatBlock {
	text := b.Text
	if strings.TrimSpace(text) == "" {
		text = formatPlanText(b.Steps)
	}
	cu := canonical.PlanUpdate{
		Text:    text,
		Actions: append([]canonical.PlanAction(nil), b.Actions...),
	}
	if len(b.Steps) > 0 {
		cu.Steps = make([]canonical.PlanStep, 0, len(b.Steps))
		for _, s := range b.Steps {
			cu.Steps = append(cu.Steps, canonical.PlanStep{
				Step:   s.Step,
				Status: canonical.PlanStepStatus(s.Status),
			})
		}
	}
	return ChatBlock{
		Type:      ChatBlockTypePlan,
		Text:      text,
		Canonical: view.FromCanonical(cu),
	}
}

func hasActionablePlanBlock(bs []blocks.ContentBlock) bool {
	for _, b := range bs {
		switch p := b.(type) {
		case PlanBlock:
			if len(p.Actions) > 0 {
				return true
			}
		case *PlanBlock:
			if p != nil && len(p.Actions) > 0 {
				return true
			}
		}
	}
	return false
}

func formatPlanText(steps []PlanStepDTO) string {
	if len(steps) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("Plan\n")
	for _, s := range steps {
		step := strings.TrimSpace(s.Step)
		if step == "" {
			continue
		}
		sb.WriteString("- ")
		sb.WriteString(planStatusMarker(s.Status))
		sb.WriteString(" ")
		sb.WriteString(step)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func planStatusMarker(status string) string {
	switch strings.TrimSpace(status) {
	case "completed":
		return "[x]"
	case "inProgress":
		return "[>]"
	default:
		return "[ ]"
	}
}
