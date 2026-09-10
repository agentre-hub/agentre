package handlers

import (
	"context"
	"encoding/json"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

const toolNameExitPlanMode = "ExitPlanMode"

type ToolPermissionRequestHandler struct{}

func (ToolPermissionRequestHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.ToolPermissionRequest)
	var input map[string]any
	if len(r.Input) > 0 {
		_ = json.Unmarshal(r.Input, &input)
	}
	// claudecode v2.1.x:ExitPlanMode 的 input={},plan markdown 由先前的 Write 写到
	// ~/.claude/plans/<slug>.md,ToolCallHandler 寄到 tc。这里注入回 input["plan"],
	// 让 持久化 block + live canonical + 回放 (toolPermissionBlockToChatBlock 读
	// ToolInput["plan"]) 三条路径共用同一份 plan,不必各自兜底。
	if r.ToolName == toolNameExitPlanMode && tc != nil && tc.LastPlanWriteContent != "" {
		if input == nil {
			input = map[string]any{}
		}
		if existing, _ := input["plan"].(string); existing == "" {
			input["plan"] = tc.LastPlanWriteContent
		}
	}
	blk := &blocks.ToolPermissionBlock{
		RequestID:  r.RequestID,
		ToolCallID: r.ToolCallID,
		ToolName:   r.ToolName,
		ToolInput:  input,
	}
	acc.AddBlock(blk, "tool_permission:"+r.RequestID)

	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":           "tool_permission_request",
			"requestId":      r.RequestID,
			"toolCallId":     r.ToolCallID,
			"toolName":       r.ToolName,
			"toolInput":      input,
			"toolPermission": blk,
			"canonical":      buildToolPermissionCanonical(r.RequestID, r.ToolName, input, false, false, false, tc),
		})
	}
	tc.BeginWait(ctx, "tool_permission", r.RequestID)
	return nil
}

type ToolPermissionResolvedHandler struct{}

func (ToolPermissionResolvedHandler) Apply(ctx context.Context, ev agentruntime.Event, acc *turn.Accumulator, emit turn.Emitter, _ turn.View, tc *turn.TurnContext) error {
	r := ev.(agentruntime.ToolPermissionResolved)
	// captured 暴露 Mutate 闭包内的 *block,emit 时一并下发 toolName/toolInput,
	// 否则 dispatcher_emitter 据空 toolName 把 ExitPlanMode 误切到 tool.permission
	// canonical,前端 PlanApproveCard 被覆盖成空白 ToolPermissionCard。
	var captured *blocks.ToolPermissionBlock
	hit := turn.Mutate[blocks.ToolPermissionBlock](acc, "tool_permission:"+r.RequestID, func(b *blocks.ToolPermissionBlock) {
		b.Resolved = true
		b.Allowed = r.Allowed
		b.AlwaysAllow = r.AlwaysAllow
		b.DenyReason = r.DenyReason
		captured = b
	})
	if !hit {
		return nil
	}
	if emit != nil {
		emit.Emit(ctx, streamOf(tc), map[string]any{
			"kind":           "tool_permission_request",
			"requestId":      r.RequestID,
			"toolCallId":     captured.ToolCallID,
			"toolName":       captured.ToolName,
			"toolInput":      captured.ToolInput,
			"resolved":       true,
			"allowed":        r.Allowed,
			"alwaysAllow":    r.AlwaysAllow,
			"denyReason":     r.DenyReason,
			"toolPermission": captured,
			// Actions=nil 由 buildToolPermissionCanonical 在 resolved=true 时兜底
			// (前端拿到 resolved=true 直接切只读态,Actions 即便有也忽略)。
			"canonical": buildToolPermissionCanonical(r.RequestID, captured.ToolName, captured.ToolInput, true, r.Allowed, r.AlwaysAllow, tc),
		})
	}
	tc.ResolveWait(ctx, "tool_permission", r.RequestID)
	return nil
}

// buildToolPermissionCanonical 算 ev.Canonical 的载荷:ExitPlanMode 走
// PlanApproveRequest(带 Actions),其它工具走 ToolPermission(无 Actions)。
//
// Resolved=true 时 Actions=nil(前端读到 resolved 切只读态)。
func buildToolPermissionCanonical(
	requestID, toolName string,
	input map[string]any,
	resolved, allowed, alwaysAllow bool,
	tc *turn.TurnContext,
) canonical.CanonicalTool {
	if toolName == toolNameExitPlanMode {
		planText, _ := input["plan"].(string)
		req := canonical.PlanApproveRequest{
			RequestID: requestID,
			PlanText:  planText,
			Resolved:  resolved,
			Allowed:   allowed,
		}
		if !resolved {
			req.Actions = BuildPlanApproveActions(launchModeOf(tc))
		}
		return req
	}
	return canonical.ToolPermission{
		RequestID:   requestID,
		ToolName:    toolName,
		ToolInput:   input,
		Resolved:    resolved,
		Allowed:     allowed,
		AlwaysAllow: alwaysAllow,
	}
}

func launchModeOf(tc *turn.TurnContext) string {
	if tc == nil {
		return ""
	}
	return tc.LaunchPermissionMode
}
