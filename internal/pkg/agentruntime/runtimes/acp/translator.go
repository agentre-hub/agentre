// Package acp is the Agent Client Protocol runtime: Agentre acts as the ACP
// client, spawning one external ACP agent subprocess (stdio JSON-RPC, protocol
// v1) per chat session and translating its session stream into sealed
// agentruntime events.
//
// This file owns the v1 `session/update` translation only. The protocol
// version decision lives in client.go's handshake; when v2 support arrives it
// takes the shape of an additional version adapter beside this one — nothing
// here should grow v2 branches.
package acp

import (
	"encoding/json"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

// usageSnapshot is the cumulative context usage an ACP usage_update carries
// (`used` = tokens currently in context, `size` = the context-window
// denominator). The drain loop turns consecutive snapshots into per-turn
// deltas; the translator itself stays stateless.
type usageSnapshot struct {
	used    int
	size    int
	present bool
}

// toolCallState is the drain-loop aggregate for one ACP tool call id. The
// translator merges frames into it (passed by value, pure); the drain loop
// owns the map keyed by tool call id.
type toolCallState struct {
	name   string
	status acpsdk.ToolCallStatus
	// shell 标记 execute 类工具:命令文本参与 AI 当前位置识别(见 location.go)。
	shell bool
}

// translate is the pure v1 session/update translator: one union in, 0/1/n
// sealed events out, plus the usage snapshot when the frame carried one.
// Updates that produce no events (user_message_chunk replay, plan_update,
// available_commands_update, ...) are returned empty; the drain loop logs them
// as raw frames. An unknown update (empty union) also yields nothing — a
// frame we do not understand must never wedge the turn.
func translate(u acpsdk.SessionUpdate) ([]agentruntime.Event, usageSnapshot) {
	switch {
	case u.AgentMessageChunk != nil:
		if t := blockText(u.AgentMessageChunk.Content); t != "" {
			return []agentruntime.Event{agentruntime.TextDelta{Text: t}}, usageSnapshot{}
		}
		return nil, usageSnapshot{}

	case u.AgentThoughtChunk != nil:
		if t := blockText(u.AgentThoughtChunk.Content); t != "" {
			return []agentruntime.Event{agentruntime.ThinkingDelta{Text: t}}, usageSnapshot{}
		}
		return nil, usageSnapshot{}

	case u.ToolCall != nil:
		ev, _ := translateToolCall(u.ToolCall)
		return []agentruntime.Event{ev}, usageSnapshot{}

	case u.ToolCallUpdate != nil:
		// Routed by the drain loop through translateToolCallUpdate: the update
		// has to be merged into the per-id aggregate the drain owns.
		return nil, usageSnapshot{}

	case u.Plan != nil:
		if ev, ok := translatePlan(u.Plan); ok {
			return []agentruntime.Event{ev}, usageSnapshot{}
		}
		return nil, usageSnapshot{}

	case u.UsageUpdate != nil:
		return nil, usageSnapshot{
			used:    u.UsageUpdate.Used,
			size:    u.UsageUpdate.Size,
			present: true,
		}
	}
	// user_message_chunk (session/load replay; Agentre has its own history),
	// plan_update / plan_removed / available_commands_update /
	// current_mode_update / config_option_update / session_info_update and any
	// unknown update: no events.
	return nil, usageSnapshot{}
}

// translateToolCall merges a tool_call creation frame into the sealed ToolCall
// event plus the aggregate state the drain loop keeps for later updates.
// translateToolCallAggregate is the same pair shaped for in-place map storage.
//
// Name: ACP tool calls carry `title` + `kind` but no protocol-level tool name;
// downstream derive.ts and the tool cards render `toolName`, so we pick title,
// fall back to kind, then to "other" (the kind default) — see §3.7.
//
// Input: the rawInput value re-serialized as-is. The SDK already decodes
// params into Go values, so byte-identical passthrough is not reachable
// without hand-writing the dispatcher; re-marshaling preserves content while
// key order is normalized. A missing rawInput yields `{}`.
func translateToolCall(c *acpsdk.SessionUpdateToolCall) (agentruntime.ToolCall, toolCallState) {
	name := toolDisplayName(c.Title, c.Kind)
	input := rawInputBytes(c.RawInput)
	canonicalTool, input := recognizeToolCanonical(c.Kind, c.Locations, input)
	return agentruntime.ToolCall{
		ID:        string(c.ToolCallId),
		Name:      name,
		Input:     input,
		Canonical: canonicalTool,
	}, toolCallState{name: name, status: c.Status, shell: c.Kind == acpsdk.ToolKindExecute}
}

// translateToolCallAggregate 返回 (事件, 聚合态指针),供 drain 循环直接存表。
func translateToolCallAggregate(c *acpsdk.SessionUpdateToolCall) (agentruntime.ToolCall, *toolCallState) {
	ev, st := translateToolCall(c)
	return ev, &st
}

// toolCallUpdateStatus 读更新帧上的终态/非终态(缺省按 pending 处理)。
func toolCallUpdateStatus(u *acpsdk.SessionToolCallUpdate) acpsdk.ToolCallStatus {
	if u.Status != nil {
		return *u.Status
	}
	return acpsdk.ToolCallStatusPending
}

// translateToolCallUpdate merges a tool_call_update into the aggregate and
// returns the sealed events: non-terminal statuses re-emit a ToolCall with the
// same id (canonical increments flow through the accumulator's mutateIndex),
// terminal statuses (completed/failed) produce the ToolResult with the joined
// content text.
func translateToolCallUpdate(st toolCallState, u *acpsdk.SessionToolCallUpdate) []agentruntime.Event {
	status := acpsdk.ToolCallStatusPending
	if u.Status != nil {
		status = *u.Status
	}
	name := st.name
	if u.Title != nil && strings.TrimSpace(*u.Title) != "" {
		name = *u.Title
	}
	switch status {
	case acpsdk.ToolCallStatusCompleted, acpsdk.ToolCallStatusFailed:
		return []agentruntime.Event{agentruntime.ToolResult{
			ToolCallID: string(u.ToolCallId),
			Content:    toolCallContentText(u.Content),
			IsError:    status == acpsdk.ToolCallStatusFailed,
		}}
	default:
		return []agentruntime.Event{agentruntime.ToolCall{
			ID:   string(u.ToolCallId),
			Name: name,
		}}
	}
}

// translatePlan maps the plan entry list onto canonical.PlanUpdate steps.
// Text stays empty: ACP's plan is a structured entry list, not markdown.
func translatePlan(p *acpsdk.SessionUpdatePlan) (agentruntime.PlanUpdated, bool) {
	if len(p.Entries) == 0 {
		return agentruntime.PlanUpdated{}, false
	}
	steps := make([]canonical.PlanStep, 0, len(p.Entries))
	for _, e := range p.Entries {
		if strings.TrimSpace(e.Content) == "" {
			continue
		}
		steps = append(steps, canonical.PlanStep{
			Step:   e.Content,
			Status: planStepStatus(e.Status),
		})
	}
	if len(steps) == 0 {
		return agentruntime.PlanUpdated{}, false
	}
	return agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{Steps: steps}}, true
}

// planStepStatus maps ACP plan entry statuses onto the canonical set. The v1
// schema enum is pending/in_progress/completed only; the extra status mapped
// below is accepted defensively because the raw string reaches us unchanged
// from agents that send it anyway.
func planStepStatus(s acpsdk.PlanEntryStatus) canonical.PlanStepStatus {
	switch s {
	case acpsdk.PlanEntryStatusInProgress:
		return canonical.StepInProgress
	case acpsdk.PlanEntryStatusCompleted:
		return canonical.StepCompleted
	case acpsdk.PlanEntryStatus("cancelled"): //nolint:misspell // defensive, see above
		return canonical.StepCancelled
	default:
		return canonical.StepPending
	}
}

// updateKind names the union variant of an update, for raw-frame logs of the
// frames that produce no events.
func updateKind(u acpsdk.SessionUpdate) string {
	switch {
	case u.UserMessageChunk != nil:
		return "user_message_chunk"
	case u.AgentMessageChunk != nil:
		return "agent_message_chunk"
	case u.AgentThoughtChunk != nil:
		return "agent_thought_chunk"
	case u.ToolCall != nil:
		return "tool_call"
	case u.ToolCallUpdate != nil:
		return "tool_call_update"
	case u.Plan != nil:
		return "plan"
	case u.PlanUpdate != nil:
		return "plan_update"
	case u.PlanRemoved != nil:
		return "plan_removed"
	case u.AvailableCommandsUpdate != nil:
		return "available_commands_update"
	case u.CurrentModeUpdate != nil:
		return "current_mode_update"
	case u.ConfigOptionUpdate != nil:
		return "config_option_update"
	case u.SessionInfoUpdate != nil:
		return "session_info_update"
	case u.UsageUpdate != nil:
		return "usage_update"
	default:
		return "unknown"
	}
}

// toolDisplayName picks the display name: title, then kind, then "other"
// (kind's own default when the agent sent neither).
func toolDisplayName(title string, kind acpsdk.ToolKind) string {
	if t := strings.TrimSpace(title); t != "" {
		return t
	}
	if k := strings.TrimSpace(string(kind)); k != "" {
		return k
	}
	return "other"
}

// blockText extracts the text of a ContentBlock; non-text blocks yield "".
func blockText(b acpsdk.ContentBlock) string {
	if b.Text != nil {
		return b.Text.Text
	}
	return ""
}

// rawInputBytes re-serializes the decoded rawInput value. nil/absent → `{}`.
func rawInputBytes(raw any) json.RawMessage {
	if raw == nil {
		return json.RawMessage(`{}`)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// toolCallContentText joins the text blocks of a tool call's content list.
// Diff and terminal-ref entries carry no plain text and are skipped.
func toolCallContentText(contents []acpsdk.ToolCallContent) string {
	var b strings.Builder
	for _, c := range contents {
		if c.Content == nil || c.Content.Content.Text == nil {
			continue
		}
		b.WriteString(c.Content.Content.Text.Text)
	}
	return b.String()
}

// recognizeToolCanonical maps edit/delete/move tool calls onto the shared
// canonical shapes. The absolute path is resolved from the ACP standard
// `locations[]` first, then from the rawInput keys (`file_path` / `path` /
// codex-style `changes[].path`). When a path was recognized it is also planted
// into the input object under `path` — the frontend Files view reads
// `toolInput.path` / `toolInput.file_path`, so a path only in canonical would
// leave the changed-file list empty.
func recognizeToolCanonical(kind acpsdk.ToolKind, locations []acpsdk.ToolCallLocation, input json.RawMessage) (canonical.CanonicalTool, json.RawMessage) {
	switch kind {
	case acpsdk.ToolKindEdit, acpsdk.ToolKindDelete, acpsdk.ToolKindMove:
	default:
		return nil, input
	}
	var m map[string]any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &m); err != nil {
			m = nil
		}
	}
	paths := locationPaths(locations)
	if len(paths) == 0 && m != nil {
		if p := canonical.ACPInputPath(m); p != "" {
			paths = []string{p}
		}
	}
	tool, ok := canonical.ACPEditFromCall(string(kind), paths, m)
	if !ok {
		return nil, input
	}
	// The persisted block only keeps title as the name, so the kind and every
	// path ride in the input: canonical.FromToolUse reads them back on recompute.
	if m == nil {
		m = map[string]any{}
	}
	if _, hasPath := m["path"]; !hasPath {
		if _, hasFilePath := m["file_path"]; !hasFilePath {
			m["path"] = paths[0]
		}
	}
	m[canonical.ACPKindInputKey] = string(kind)
	m[canonical.ACPPathsInputKey] = paths
	if b, err := json.Marshal(m); err == nil {
		input = b
	}
	return tool, input
}

// locationPaths collects the absolute paths of a tool call's locations.
func locationPaths(locations []acpsdk.ToolCallLocation) []string {
	out := make([]string, 0, len(locations))
	for _, l := range locations {
		if p := strings.TrimSpace(l.Path); p != "" {
			out = append(out, p)
		}
	}
	return out
}
