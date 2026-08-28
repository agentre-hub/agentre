package agentruntime

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/cago-frame/agents/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

// TestEvent_RoundTrip 每个 sealed Event 至少 1 个 happy + 1 个边界 case,
// 走 json.Marshal -> UnmarshalEvent 检查值相等。ErrorEvent.Err 单独比 .Error() 字符串
// (errors.New 不能 == )。
func TestEvent_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Event
	}{
		// TextDelta
		{"text_delta", TextDelta{Text: "hello"}},
		{"text_delta_empty", TextDelta{}},

		// ThinkingDelta
		{"thinking_delta", ThinkingDelta{Text: "let me think"}},
		{"thinking_delta_empty", ThinkingDelta{}},

		// OutputActivity — 无字段的纯计时信号,wire 上只剩 kind
		{"output_activity", OutputActivity{}},

		// ToolCall — 含 / 不含 canonical
		{"tool_call_no_canonical", ToolCall{
			ID: "tu_1", Name: "Bash",
			Input: json.RawMessage(`{"cmd":"ls"}`),
		}},
		{"tool_call_with_file_write", ToolCall{
			ID: "tu_2", Name: "Write",
			Input:     json.RawMessage(`{"file_path":"/a","content":"x"}`),
			Canonical: canonical.FileWrite{Path: "/a", Content: "x", Lines: 1, Bytes: 1},
		}},
		{"tool_call_parent_subagent", ToolCall{
			ID: "tu_3", Name: "Read",
			ParentToolCallID: "tu_parent", SubagentRunID: "run-0",
		}},

		// ToolResult
		{"tool_result_success", ToolResult{
			ToolCallID: "tu_1", Content: "ok",
		}},
		{"tool_result_error", ToolResult{
			ToolCallID: "tu_1", Content: "oops", IsError: true,
			ParentToolCallID: "tu_parent", SubagentRunID: "run-0",
			Meta: json.RawMessage(`{"exitCode":1}`),
		}},

		// SteerConsumed
		{"steer_consumed", SteerConsumed{Steers: []ConsumedSteer{
			{QueuedID: "q1", Text: "hey", SourcePeer: "sha256:peer-a", SourceName: "iPhone"},
			{QueuedID: "q2", Text: "more"},
		}}},
		{"steer_consumed_empty", SteerConsumed{}},

		// UserAskRequest
		{"user_ask_request", UserAskRequest{
			RequestID: "r1", ToolCallID: "tu_a", ParentToolCallID: "tu_parent",
			Questions: []AskQuestion{{
				Question: "Pick one", MultiSelect: false,
				Options: []AskOption{{Label: "A", Description: "first"}, {Label: "B"}},
			}},
		}},

		// UserAskResolved
		{"user_ask_resolved_answered", UserAskResolved{
			RequestID: "r1", ParentToolCallID: "tu_parent",
			Answers: []AskAnswer{{QuestionIndex: 0, Labels: []string{"A"}}},
		}},
		{"user_ask_resolved_skipped", UserAskResolved{RequestID: "r2", Skipped: true}},

		// ToolPermissionRequest
		{"tool_permission_request", ToolPermissionRequest{
			RequestID: "p1", ToolCallID: "tu_x", ToolName: "Bash",
			Input: json.RawMessage(`{"cmd":"rm"}`),
		}},

		// ToolPermissionResolved
		{"tool_permission_resolved_allow", ToolPermissionResolved{
			RequestID: "p1", Allowed: true, AlwaysAllow: true,
		}},
		{"tool_permission_resolved_deny", ToolPermissionResolved{
			RequestID: "p2", Allowed: false, DenyReason: "no thanks",
		}},

		// OpenClaw exec approval lifecycle is intentionally separate from tool
		// completion. The available decisions come from the Gateway verbatim.
		{"exec_approval_requested", ExecApprovalRequested{
			ID: "approval-1", CommandText: "rm -rf build", CommandPreview: "rm -rf build",
			AllowedDecisions: []string{"allow-once", "deny"}, Host: "gateway",
			AgentID: "main", SessionKey: "agentre:12:34", CreatedAtMs: 100, ExpiresAtMs: 200,
		}},
		{"exec_approval_resolved", ExecApprovalResolved{
			ID: "approval-1", Status: "resolved", Decision: "deny", ResolvedBy: "device-2", ResolvedAtMs: 150,
		}},
		{"exec_approval_expired", ExecApprovalResolved{ID: "approval-2", Status: "expired"}},

		// PermissionModeChanged
		{"permission_mode_changed", PermissionModeChanged{Mode: "bypassPermissions"}},

		// SubagentStarted / Progress / Done
		{"subagent_started", SubagentStarted{
			ToolCallID: "tu_task",
			Info: SubagentInfo{
				TaskID: "t1", SubagentType: "researcher",
				TaskDescription: "look", Prompt: "p", Status: "running",
			},
		}},
		{"subagent_progress", SubagentProgress{
			ToolCallID: "tu_task",
			Info: SubagentInfo{
				TaskID: "t1", LastToolName: "Read", ToolUses: 3, Status: "running",
				Mode: "single",
				Runs: []SubagentRun{{
					ID: "run-0", Index: 0, Profile: "read-only", Task: "inspect",
					RequestedModel: "requested", Model: "observed", Status: "running",
					LastToolName: "Read", ToolUses: 3,
				}},
			},
		}},
		{"subagent_done", SubagentDone{
			ToolCallID: "tu_task",
			Info: SubagentInfo{
				TaskID: "t1", ToolUses: 8, TotalTokens: 4000, DurationMs: 5000,
				Status: "completed",
			},
		}},
		{"subagent_model", SubagentModel{ToolCallID: "tu_task", Model: "claude-haiku-4-5-20251001"}},
		{"subagent_model_empty", SubagentModel{}},

		// Retry
		{"retry", Retry{
			Message: "rate limited", Details: "wait 5s",
			Attempt: 2, Max: 5,
		}},

		// UsageUpdate
		{"usage_update_full", UsageUpdate{
			Usage: &provider.Usage{
				PromptTokens: 100, CompletionTokens: 50, ReasoningTokens: 25,
				CachedTokens: 10, CacheCreationTokens: 5, TotalTokens: 190,
			},
			TotalInputTokens: 115,
			ContextWindow:    258000,
		}},
		{"usage_update_nil_usage", UsageUpdate{TotalInputTokens: 0}},

		// ContextWindowUpdated
		{"context_window_updated", ContextWindowUpdated{Tokens: 200000}},
		{"context_window_zero", ContextWindowUpdated{}},

		// CompactBoundary
		{"compact_boundary_manual", CompactBoundary{
			PreTokens: 30117, PostTokens: 2697, Trigger: "manual", DurationMs: 20696,
		}},
		{"compact_boundary_auto_pre_only", CompactBoundary{PreTokens: 12345, Trigger: "auto"}},
		{"compact_boundary_empty", CompactBoundary{}},

		// RuntimeStatus
		{"runtime_status_compacting", RuntimeStatus{Status: "compacting"}},
		{"runtime_status_requesting", RuntimeStatus{Status: "requesting"}},

		// PlanUpdated
		{"plan_updated_steps", PlanUpdated{Plan: canonical.PlanUpdate{
			Steps: []canonical.PlanStep{
				{ID: "s1", Step: "fetch", Status: canonical.StepCompleted},
				{ID: "s2", Step: "render", Status: canonical.StepInProgress},
			},
		}}},
		{"plan_updated_text", PlanUpdated{Plan: canonical.PlanUpdate{
			Text: "## Plan\n- a\n- b",
			Actions: []canonical.PlanAction{
				{ID: "plan.execute", Kind: canonical.PlanActionApprove},
			},
		}}},
		{"plan_updated_empty", PlanUpdated{}},

		// Done
		{"done", Done{}},

		// ErrorEvent
		{"error_with_msg", ErrorEvent{Err: errors.New("boom")}},
		{"error_nil_err", ErrorEvent{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.in)
			require.NoError(t, err, "marshal")

			// Wire shape sanity: kind present at top level.
			var head struct {
				Kind EventKind `json:"kind"`
			}
			require.NoError(t, json.Unmarshal(b, &head))
			assert.NotEmpty(t, head.Kind, "wire output must carry kind")

			out, err := UnmarshalEvent(b)
			require.NoError(t, err, "unmarshal: %s", string(b))

			// ErrorEvent.Err is errors.New(...) which is not == reflexively;
			// compare by .Error() string.
			if eIn, ok := tc.in.(ErrorEvent); ok {
				eOut, ok2 := out.(ErrorEvent)
				require.True(t, ok2, "type")
				if eIn.Err == nil {
					assert.Nil(t, eOut.Err)
				} else {
					require.NotNil(t, eOut.Err)
					assert.Equal(t, eIn.Err.Error(), eOut.Err.Error())
				}
				return
			}
			assert.Equal(t, tc.in, out)
		})
	}
}

// TestEvent_WireKindMatchesType pins the discriminator: each concrete type
// must marshal to a kind string equal to the EventKind constant the runtime
// pipeline already uses. Catches accidental typo / drift between MarshalJSON
// and the switch in UnmarshalEvent.
func TestUsageUpdate_ContextWindowWireRoundTrip(t *testing.T) {
	raw := []byte(`{"kind":"usage","usage":{"promptTokens":10},"totalInputTokens":10,"contextWindow":258000}`)

	ev, err := UnmarshalEvent(raw)
	require.NoError(t, err)
	encoded, err := json.Marshal(ev)
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(encoded, &got))
	assert.Equal(t, float64(258000), got["contextWindow"])
}

func TestEvent_WireKindMatchesType(t *testing.T) {
	pairs := []struct {
		kind EventKind
		ev   Event
	}{
		{EventTextDelta, TextDelta{}},
		{EventThinkingDelta, ThinkingDelta{}},
		{EventOutputActivity, OutputActivity{}},
		{EventToolUseStart, ToolCall{}},
		{EventToolResult, ToolResult{}},
		{EventSteerConsumed, SteerConsumed{}},
		{EventAskUserQuestion, UserAskRequest{}},
		{EventAskUserQuestionAnswered, UserAskResolved{}},
		{EventToolPermissionRequest, ToolPermissionRequest{}},
		{EventToolPermissionResolved, ToolPermissionResolved{}},
		{EventExecApprovalRequested, ExecApprovalRequested{}},
		{EventExecApprovalResolved, ExecApprovalResolved{}},
		{EventPermissionModeChanged, PermissionModeChanged{}},
		{EventSubagentStarted, SubagentStarted{}},
		{EventSubagentProgress, SubagentProgress{}},
		{EventSubagentDone, SubagentDone{}},
		{EventSubagentModel, SubagentModel{}},
		{EventRetry, Retry{}},
		{EventUsage, UsageUpdate{}},
		{EventContextWindowUpdated, ContextWindowUpdated{}},
		{EventCompactBoundary, CompactBoundary{}},
		{EventRuntimeStatus, RuntimeStatus{}},
		{EventPlanUpdated, PlanUpdated{}},
		{EventDone, Done{}},
		{EventError, ErrorEvent{}},
	}
	for _, p := range pairs {
		t.Run(string(p.kind), func(t *testing.T) {
			b, err := json.Marshal(p.ev)
			require.NoError(t, err)
			var head struct {
				Kind EventKind `json:"kind"`
			}
			require.NoError(t, json.Unmarshal(b, &head))
			assert.Equal(t, p.kind, head.Kind)
		})
	}
}

// TestUnmarshalEvent_AllKindsCovered guards the symmetry: every EventKind
// emitted by a MarshalJSON above must decode back successfully via
// UnmarshalEvent. Adding a 20th Event type and forgetting to wire it into
// UnmarshalEvent fails here.
func TestUnmarshalEvent_AllKindsCovered(t *testing.T) {
	specimens := []Event{
		TextDelta{}, ThinkingDelta{}, OutputActivity{}, ToolCall{}, ToolResult{}, SteerConsumed{},
		UserAskRequest{}, UserAskResolved{},
		ToolPermissionRequest{}, ToolPermissionResolved{},
		ExecApprovalRequested{}, ExecApprovalResolved{},
		PermissionModeChanged{},
		SubagentStarted{}, SubagentProgress{}, SubagentDone{}, SubagentModel{},
		Retry{}, UsageUpdate{}, ContextWindowUpdated{}, CompactBoundary{}, RuntimeStatus{}, PlanUpdated{},
		Done{}, ErrorEvent{}, UserMessageEvent{},
	}
	for _, sp := range specimens {
		b, err := json.Marshal(sp)
		require.NoError(t, err)
		_, err = UnmarshalEvent(b)
		require.NoError(t, err, "type %T failed UnmarshalEvent round-trip: %s", sp, string(b))
	}
}

func TestUserMessageEvent_RoundTrip(t *testing.T) {
	// R18:「开新一轮」发起方标记。Text 是发起方的用户文本,SourceDevice / SourceDeviceName
	// 是发起方设备身份(R19 名字缺失时空,前端回退指纹)。
	cases := []struct {
		name string
		in   Event
	}{
		{"带名字", UserMessageEvent{Text: "跑一下测试", SourceDevice: "sha256:web-device", SourceDeviceName: "Chrome · macOS"}},
		{"名字缺失", UserMessageEvent{Text: "继续", SourceDevice: "sha256:web-device"}},
		{"空文本", UserMessageEvent{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.in)
			require.NoError(t, err)
			assert.Contains(t, string(b), `"kind":"user_message"`)
			got, err := UnmarshalEvent(b)
			require.NoError(t, err)
			assert.Equal(t, c.in, got)
		})
	}
}

func TestSubagentStarted_KindRoundTrip(t *testing.T) {
	ev := SubagentStarted{ToolCallID: "tu1", Info: SubagentInfo{Kind: "local_bash"}}
	b, err := json.Marshal(ev)
	require.NoError(t, err)
	got, err := UnmarshalEvent(b)
	require.NoError(t, err)
	assert.Equal(t, "local_bash", got.(SubagentStarted).Info.Kind)
}

func TestSubagentRuntimeContractsUseLowerCamelWireFields(t *testing.T) {
	ev := SubagentProgress{ToolCallID: "outer", Info: SubagentInfo{
		Mode: "parallel",
		Runs: []SubagentRun{
			{ID: "run-0", Index: 0, Agent: "a", Task: "inspect", Model: "model-a", Status: "completed"},
			{ID: "run-1", Index: 1, Agent: "b", Task: "test", Status: "failed", ErrorMessage: "boom"},
		},
	}}
	b, err := json.Marshal(ev)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"kind":"subagent_progress",
		"toolCallId":"outer",
		"info":{"mode":"parallel","runs":[
			{"id":"run-0","index":0,"agent":"a","task":"inspect","model":"model-a","status":"completed"},
			{"id":"run-1","index":1,"agent":"b","task":"test","status":"failed","errorMessage":"boom"}
		]}
	}`, string(b))
	decoded, err := UnmarshalEvent(b)
	require.NoError(t, err)
	assert.Equal(t, ev, decoded)

	for _, event := range []Event{
		ToolCall{ID: "child-call", ParentToolCallID: "outer", SubagentRunID: "run-0"},
		ToolResult{ToolCallID: "child-call", ParentToolCallID: "outer", SubagentRunID: "run-0", Content: "ok"},
	} {
		wire, marshalErr := json.Marshal(event)
		require.NoError(t, marshalErr)
		assert.Contains(t, string(wire), `"subagentRunId":"run-0"`)
		roundTrip, unmarshalErr := UnmarshalEvent(wire)
		require.NoError(t, unmarshalErr)
		assert.Equal(t, event, roundTrip)
	}
}

// TestSubagentModel_KindRoundTrip 独立事件类型透传（不复用 SubagentProgress，见
// spec 实现决策 3）：wire 上只有 toolCallId + model 两个字段，不携带
// toolUses/totalTokens/status，保证反序列化端不会误以为这是一次完整的进度更新。
func TestSubagentModel_KindRoundTrip(t *testing.T) {
	ev := SubagentModel{ToolCallID: "tu1", Model: "claude-haiku-4-5-20251001"}
	b, err := json.Marshal(ev)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(b, &raw))
	assert.NotContains(t, raw, "toolUses")
	assert.NotContains(t, raw, "totalTokens")
	assert.NotContains(t, raw, "status")

	got, err := UnmarshalEvent(b)
	require.NoError(t, err)
	assert.Equal(t, ev, got.(SubagentModel))
}

func TestUnmarshalEvent_ErrorPaths(t *testing.T) {
	t.Run("empty bytes", func(t *testing.T) {
		_, err := UnmarshalEvent(nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty data")
	})
	t.Run("missing kind", func(t *testing.T) {
		_, err := UnmarshalEvent([]byte(`{"text":"x"}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing kind")
	})
	t.Run("unknown kind", func(t *testing.T) {
		_, err := UnmarshalEvent([]byte(`{"kind":"flying_squirrel"}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unknown kind")
	})
	t.Run("malformed json", func(t *testing.T) {
		_, err := UnmarshalEvent([]byte(`{not json`))
		require.Error(t, err)
	})
	t.Run("tool_call with bad canonical", func(t *testing.T) {
		_, err := UnmarshalEvent([]byte(`{"kind":"tool_use_start","id":"x","canonical":{"kind":"unknown.thing"}}`))
		require.Error(t, err)
	})
}

// TestEvent_InterfaceFieldDispatch verifies that json.Marshal on an
// `interface Event` field dispatches to the concrete type's MarshalJSON.
// This is the property that lets daemon's notifyEvents do
// `json.Marshal(wire.EventFrame{Event: ev})` without writing a switch.
func TestEvent_InterfaceFieldDispatch(t *testing.T) {
	type envelope struct {
		Event Event `json:"event"`
	}
	in := envelope{Event: TextDelta{Text: "hi"}}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"kind":"text_delta"`)
	assert.Contains(t, string(b), `"text":"hi"`)
}
