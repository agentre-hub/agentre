package chat_svc

import (
	"encoding/json"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

// convertOldEventToNew 旧 RuntimeEvent → 新 Event,仅留给 chat_svc_test 包的
// fake runner 当 fixture 模板 —— 老测试用 RuntimeEvent{Kind: ...} 字面量驱动,
// 由 ConvertOldEventToNewForTest 暴露给外部测试包。生产路径已直接吃 NEW Event
// channel,不再走转换。
//
// 返回 nil 表示该旧 RuntimeEvent 无对应新 Event 类型(EventToolUseEnd 在新 API
// 通过 ToolResult 隐式表达;nested struct 缺失时的防御 nil)。
//
// canonical 弥补:旧 RuntimeEvent 不承载 canonical;在新建 ToolCall 时按 name+input
// 透过 canonical.FromToolUse 重新推断,弥补 round-trip 丢失。
func convertOldEventToNew(ev agentruntime.RuntimeEvent) agentruntime.Event {
	switch ev.Kind {
	case agentruntime.EventTextDelta:
		return agentruntime.TextDelta{Text: ev.Text}
	case agentruntime.EventThinkingDelta:
		return agentruntime.ThinkingDelta{Text: ev.Text}
	case agentruntime.EventToolUseStart:
		if ev.ToolUse == nil {
			return nil
		}
		tc := agentruntime.ToolCall{
			ID:               ev.ToolUse.ID,
			Name:             ev.ToolUse.Name,
			Input:            cloneBytesForConvert(ev.ToolUse.Input),
			ParentToolCallID: ev.ToolUse.ParentToolCallID,
			SubagentRunID:    ev.ToolUse.SubagentRunID,
		}
		if len(tc.Input) > 0 {
			var input map[string]any
			if err := json.Unmarshal(tc.Input, &input); err == nil {
				if c, ok := canonical.FromToolUse(tc.Name, input); ok {
					tc.Canonical = c
				}
			}
		}
		return tc
	case agentruntime.EventToolUseEnd:
		// 新 API 没有独立的 tool_use_end 事件 —— ToolResult 隐式收尾。
		return nil
	case agentruntime.EventToolResult:
		if ev.ToolResult == nil {
			return nil
		}
		return agentruntime.ToolResult{
			ToolCallID:       ev.ToolResult.ToolCallID,
			Content:          ev.ToolResult.Content,
			IsError:          ev.ToolResult.IsError,
			ParentToolCallID: ev.ToolResult.ParentToolCallID,
			SubagentRunID:    ev.ToolResult.SubagentRunID,
			Meta:             cloneBytesForConvert(ev.ToolResult.ResultMeta),
		}
	case agentruntime.EventSteerConsumed:
		return agentruntime.SteerConsumed{Steers: ev.Steers}
	case agentruntime.EventAskUserQuestion:
		if ev.AskUserQuestion == nil {
			return nil
		}
		return agentruntime.UserAskRequest{
			RequestID:        ev.AskUserQuestion.RequestID,
			ToolCallID:       ev.AskUserQuestion.ToolCallID,
			ParentToolCallID: ev.AskUserQuestion.ParentToolCallID,
			Questions:        ev.AskUserQuestion.Questions,
		}
	case agentruntime.EventAskUserQuestionAnswered:
		if ev.AskUserQuestion == nil {
			return nil
		}
		return agentruntime.UserAskResolved{
			RequestID:        ev.AskUserQuestion.RequestID,
			ParentToolCallID: ev.AskUserQuestion.ParentToolCallID,
			Answers:          ev.AskUserQuestion.Answers,
			Skipped:          ev.AskUserQuestion.Skipped,
		}
	case agentruntime.EventToolPermissionRequest:
		if ev.ToolPermission == nil {
			return nil
		}
		return agentruntime.ToolPermissionRequest{
			RequestID:  ev.ToolPermission.RequestID,
			ToolCallID: ev.ToolPermission.ToolCallID,
			ToolName:   ev.ToolPermission.ToolName,
			Input:      cloneBytesForConvert(ev.ToolPermission.Input),
		}
	case agentruntime.EventToolPermissionResolved:
		if ev.ToolPermission == nil {
			return nil
		}
		return agentruntime.ToolPermissionResolved{
			RequestID:   ev.ToolPermission.RequestID,
			Allowed:     ev.ToolPermission.Allowed,
			AlwaysAllow: ev.ToolPermission.AlwaysAllow,
			DenyReason:  ev.ToolPermission.DenyReason,
		}
	case agentruntime.EventPermissionModeChanged:
		return agentruntime.PermissionModeChanged{Mode: ev.PermissionMode}
	case agentruntime.EventSubagentStarted:
		return agentruntime.SubagentStarted{ToolCallID: ev.ToolCallID, Info: subagentInfoFromPtrForConvert(ev.Subagent)}
	case agentruntime.EventSubagentProgress:
		return agentruntime.SubagentProgress{ToolCallID: ev.ToolCallID, Info: subagentInfoFromPtrForConvert(ev.Subagent)}
	case agentruntime.EventSubagentDone:
		return agentruntime.SubagentDone{ToolCallID: ev.ToolCallID, Info: subagentInfoFromPtrForConvert(ev.Subagent)}
	case agentruntime.EventRetry:
		if ev.Retry == nil {
			return nil
		}
		return agentruntime.Retry{
			Message: ev.Retry.Message,
			Details: ev.Retry.AdditionalDetails,
			Attempt: ev.Retry.Attempt,
			Max:     ev.Retry.MaxAttempts,
		}
	case agentruntime.EventUsage:
		return agentruntime.UsageUpdate{
			Usage:            ev.Usage,
			TotalInputTokens: ev.TotalInputTokens,
		}
	case agentruntime.EventPlanUpdated:
		return agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{
			Steps: planUpdateStepsFromOldForConvert(ev.Plan),
			Text:  ev.PlanText,
		}}
	case agentruntime.EventDone:
		return agentruntime.Done{}
	case agentruntime.EventError:
		return agentruntime.ErrorEvent{Err: ev.Err}
	}
	return nil
}

func subagentInfoFromPtrForConvert(p *agentruntime.SubagentInfo) agentruntime.SubagentInfo {
	if p == nil {
		return agentruntime.SubagentInfo{}
	}
	return *p
}

func cloneBytesForConvert(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func agentSpawnRunsFromRuntime(runs []agentruntime.SubagentRun) []canonical.AgentSpawnRun {
	if runs == nil {
		return nil
	}
	out := make([]canonical.AgentSpawnRun, len(runs))
	for i, run := range runs {
		out[i] = canonical.AgentSpawnRun{
			ID:             run.ID,
			Index:          run.Index,
			Agent:          run.Agent,
			Profile:        run.Profile,
			AgentSource:    run.AgentSource,
			Task:           run.Task,
			RequestedModel: run.RequestedModel,
		}
	}
	return out
}

func planUpdateStepsFromOldForConvert(steps []agentruntime.PlanStep) []canonical.PlanStep {
	if len(steps) == 0 {
		return nil
	}
	out := make([]canonical.PlanStep, len(steps))
	for i, s := range steps {
		out[i] = canonical.PlanStep{Step: s.Step, Status: canonical.PlanStepStatus(s.Status)}
	}
	return out
}
