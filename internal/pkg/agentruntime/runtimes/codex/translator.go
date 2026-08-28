package codex

import (
	"encoding/json"
	"strings"

	"github.com/cago-frame/agents/provider"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/diff"
	"github.com/agentre-hub/agentre/pkg/codex"
)

// translate 把单帧 codex.Event 翻成 0/1/n 个 sealed agentruntime.Event。
//
// 与顶层 codex.go.translateCodexEvent 平行 + **加 canonical 识别**:
//   - file_change → ToolCall.Canonical = canonical.FileEdit(per-File Kind:
//     created/modified/deleted 都保留 diff 表示;canonical.FileWrite 适合带
//     raw content 的 claudecode Write,codex 这里 created 也是 diff 形式,统一
//     走 FileEdit 更自然)
//   - EventPlanUpdated → 直接 emit PlanUpdated{Plan: canonical.PlanUpdate}
//     (新 sealed Event,不走 ToolCall 路径)
//   - EventRequestUserInput → emit UserAskRequest(已结构化,不带 ToolCall canonical)
//   - 其它工具(command_execution / collabAgent / 自定义)走 raw ToolCall 路径
func translate(ev codex.Event) (events []agentruntime.Event, usage *provider.Usage, stopErr error) {
	switch ev.Kind {
	case codex.EventTextDelta:
		events = append(events, agentruntime.TextDelta{Text: ev.Text})
	case codex.EventThinkingDelta:
		events = append(events, agentruntime.ThinkingDelta{Text: ev.Text})
	case codex.EventPreToolUse:
		if ev.Tool != nil {
			events = append(events, agentruntime.ToolCall{
				ID:        ev.Tool.ID,
				Name:      ev.Tool.Name,
				Input:     ev.Tool.Input,
				Canonical: recognizeCanonical(ev.Tool.Name, ev.Tool.Input),
			})
		}
	case codex.EventPostToolUse:
		if ev.Tool != nil {
			events = append(events, agentruntime.ToolResult{
				ToolCallID: ev.Tool.ID,
				Content:    string(ev.Tool.Response),
				IsError:    ev.Tool.Err != nil,
			})
		}
	case codex.EventRequestUserInput:
		if ev.RequestUserInput != nil {
			events = append(events, agentruntime.UserAskRequest{
				RequestID:  ev.RequestUserInput.RequestID,
				ToolCallID: ev.RequestUserInput.ItemID,
				Questions:  requestUserInputQuestionsToRuntime(ev.RequestUserInput.Questions),
			})
		}
	case codex.EventApprovalRequest:
		if ev.Approval != nil {
			events = append(events, agentruntime.ToolPermissionRequest{
				RequestID:  ev.Approval.RequestID,
				ToolCallID: ev.Approval.ItemID,
				ToolName:   ev.Approval.ToolName,
				Input:      ev.Approval.Input,
			})
		}
	case codex.EventPlanUpdated:
		// codex 同时支持两种 wire 形态:item/plan delta 带完整 PlanText;
		// turn/plan/updated 带 []PlanStep。translator 都收编到 canonical.PlanUpdate,
		// 一条 sealed PlanUpdated event 同时携带 Text 和 Steps,下游不再二态分支。
		//
		// PlanText 原值保留(含末尾换行),仅用 TrimSpace 做"是否空"判断;trim 后
		// 字符串会被前端 markdown 渲染端误认为 "无尾换行",影响格式。
		hasText := strings.TrimSpace(ev.PlanText) != ""
		var steps []canonical.PlanStep
		if len(ev.Plan) > 0 {
			steps = make([]canonical.PlanStep, 0, len(ev.Plan))
			for _, p := range ev.Plan {
				steps = append(steps, canonical.PlanStep{
					Step:   p.Step,
					Status: canonical.PlanStepStatus(p.Status),
				})
			}
		}
		if hasText || len(steps) > 0 {
			events = append(events, agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{
				Text:  ev.PlanText,
				Steps: steps,
			}})
		}
	case codex.EventRetry:
		if ev.Retry != nil {
			events = append(events, agentruntime.Retry{
				Message: ev.Retry.Message,
				Details: ev.Retry.AdditionalDetails,
				Attempt: ev.Retry.Attempt,
				Max:     ev.Retry.MaxAttempts,
			})
		}
	case codex.EventCompactBoundary:
		var info agentruntime.CompactBoundary
		if ev.Compact != nil {
			info.PreTokens = ev.Compact.PreTokens
			info.Trigger = ev.Compact.Trigger
		}
		events = append(events, info)
	case codex.EventError:
		if ev.Err != nil {
			events = append(events, agentruntime.ErrorEvent{Err: ev.Err})
			stopErr = ev.Err
		}
	case codex.EventUsage:
		// codex app-server 推 thread/tokenUsage/updated 时上游已 emit。透传到
		// UsageUpdate.Usage + TotalInputTokens=PromptTokens(OpenAI family 不区
		// 分 cached / cacheCreation,直接 prompt 即可;spec §A token contract)。
		if ev.Usage.PromptTokens > 0 || ev.Usage.CompletionTokens > 0 {
			u := ev.Usage
			events = append(events, agentruntime.UsageUpdate{
				Usage:            &u,
				TotalInputTokens: u.PromptTokens,
			})
		}
	}
	// turn 收尾时 ev.Usage 仍然填上 —— 把它作为 RunResult.Usage 的兜底值。
	if ev.Usage.PromptTokens > 0 || ev.Usage.CompletionTokens > 0 {
		u := ev.Usage
		usage = &u
	}
	return
}

func attachPlanModeActions(ev agentruntime.Event, collaborationMode string) agentruntime.Event {
	pu, ok := ev.(agentruntime.PlanUpdated)
	if !ok {
		return ev
	}
	if strings.TrimSpace(collaborationMode) != string(codex.CollaborationPlan) {
		return ev
	}
	if strings.TrimSpace(pu.Plan.Text) == "" || len(pu.Plan.Actions) > 0 {
		return ev
	}
	pu.Plan.Actions = codexPlanActions()
	return pu
}

func codexPlanActions() []canonical.PlanAction {
	return []canonical.PlanAction{
		{ID: canonical.PlanActionIDExecute, Kind: canonical.PlanActionApprove},
		{ID: canonical.PlanActionIDRefine, Kind: canonical.PlanActionRefine, RequiresFeedback: true},
	}
}

// recognizeCanonical 按工具名 + raw input JSON 识别 codex 已知 canonical 形状。
// 当前覆盖 file_change 与 update_plan。command_execution / collabAgent /
// 自定义工具不识别,走 raw 路径。
func recognizeCanonical(name string, rawInput json.RawMessage) canonical.CanonicalTool {
	if len(rawInput) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(rawInput, &m); err != nil {
		return nil
	}
	if name == "update_plan" {
		if c, ok := canonical.FromToolUse(name, m); ok {
			return c
		}
		return nil
	}
	if name != "file_change" {
		return nil
	}
	payload, ok := diff.FromFileChange(m)
	if !ok || len(payload.Files) == 0 {
		return nil
	}
	return canonical.FileEdit{Files: canonical.PatchesFromDiff(payload)}
}

// requestUserInputQuestionsToRuntime 把 codex 自带的 RequestUserInputQuestion
// 列表映射到 agentruntime.AskQuestion。codex 协议天然不支持 MultiSelect。
func requestUserInputQuestionsToRuntime(in []codex.RequestUserInputQuestion) []agentruntime.AskQuestion {
	if len(in) == 0 {
		return nil
	}
	out := make([]agentruntime.AskQuestion, 0, len(in))
	for _, q := range in {
		opts := make([]agentruntime.AskOption, 0, len(q.Options))
		for _, opt := range q.Options {
			opts = append(opts, agentruntime.AskOption{Label: opt.Label, Description: opt.Description})
		}
		out = append(out, agentruntime.AskQuestion{
			ID:          q.ID,
			Question:    q.Question,
			Header:      q.Header,
			MultiSelect: false,
			IsOther:     q.IsOther,
			IsSecret:    q.IsSecret,
			Options:     opts,
		})
	}
	return out
}
