package acp

import (
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

func ptr[T any](v T) *T { return &v }

// textChunk builds an agent_message_chunk / agent_thought_chunk union value.
func textChunk(text string) acpsdk.SessionUpdate {
	return acpsdk.UpdateAgentMessageText(text)
}

// SoMsg helpers keep table asserts readable.
func requireJSONEq(t *testing.T, want, got json.RawMessage) {
	t.Helper()
	if len(want) == 0 && len(got) == 0 {
		return
	}
	assert.JSONEq(t, string(want), string(got))
}

// TestTranslateChunks 覆盖 §3.7 的文本 / 思考 / 用户回放三行。
func TestTranslateChunks(t *testing.T) {
	Convey("Given the v1 translator", t, func() {
		Convey("agent_message_chunk (text) becomes a TextDelta", func() {
			events, _ := translate(textChunk("hello "))
			require.Len(t, events, 1)
			So(events[0], ShouldHaveSameTypeAs, agentruntime.TextDelta{})
			So(events[0].(agentruntime.TextDelta).Text, ShouldEqual, "hello ")
		})
		Convey("agent_thought_chunk becomes a ThinkingDelta", func() {
			events, _ := translate(acpsdk.UpdateAgentThoughtText("thinking"))
			require.Len(t, events, 1)
			So(events[0], ShouldHaveSameTypeAs, agentruntime.ThinkingDelta{})
			So(events[0].(agentruntime.ThinkingDelta).Text, ShouldEqual, "thinking")
		})
		Convey("empty text chunk produces no event", func() {
			events, _ := translate(textChunk(""))
			So(len(events), ShouldEqual, 0)
		})
		Convey("user_message_chunk produces no event (session/load replay)", func() {
			events, _ := translate(acpsdk.UpdateUserMessageText("replayed"))
			So(len(events), ShouldEqual, 0)
		})
		Convey("non-text agent message content produces no event", func() {
			events, _ := translate(acpsdk.SessionUpdate{AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
				Content: acpsdk.ImageBlock("AAAA", "image/png"),
			}})
			So(len(events), ShouldEqual, 0)
		})
	})
}

// TestTranslateToolCall 覆盖 tool_call 创建行的每个要求：显示名 / 原始字节透传 /
// canonical 识别 / Input 里必须能读到 path。
func TestTranslateToolCall(t *testing.T) {
	Convey("Given a tool_call frame", t, func() {
		Convey("title wins, kind is the fallback, missing kind falls back to other", func() {
			_, st := translateToolCall(&acpsdk.SessionUpdateToolCall{
				ToolCallId: "tc-1",
				Title:      "Editing main.go",
				Kind:       acpsdk.ToolKindEdit,
				RawInput:   map[string]any{"file_path": "/tmp/main.go", "content": "x"},
			})
			So(st.name, ShouldEqual, "Editing main.go")

			_, st = translateToolCall(&acpsdk.SessionUpdateToolCall{
				ToolCallId: "tc-2",
				Kind:       acpsdk.ToolKindExecute,
				RawInput:   map[string]any{"command": "ls"},
			})
			So(st.name, ShouldEqual, "execute")

			_, st = translateToolCall(&acpsdk.SessionUpdateToolCall{
				ToolCallId: "tc-3",
				RawInput:   map[string]any{},
			})
			So(st.name, ShouldEqual, "other")
		})
		Convey("missing rawInput yields an empty JSON object", func() {
			ev, _ := translateToolCall(&acpsdk.SessionUpdateToolCall{ToolCallId: "tc-4", Title: "t"})
			requireJSONEq(t, json.RawMessage(`{}`), ev.Input)
		})
		Convey("edit with locations becomes FileEdit and Input carries the path", func() {
			ev, _ := translateToolCall(&acpsdk.SessionUpdateToolCall{
				ToolCallId: "tc-5",
				Title:      "edit",
				Kind:       acpsdk.ToolKindEdit,
				Locations:  []acpsdk.ToolCallLocation{{Path: "/abs/a.go"}},
				RawInput:   map[string]any{"old": "x", "new": "y"},
			})
			So(ev.ID, ShouldEqual, "tc-5")
			var in map[string]any
			require.NoError(t, json.Unmarshal(ev.Input, &in))
			So(in["path"], ShouldEqual, "/abs/a.go")

			fe, ok := ev.Canonical.(canonical.FileEdit)
			So(ok, ShouldBeTrue)
			require.Len(t, fe.Files, 1)
			So(fe.Files[0].Path, ShouldEqual, "/abs/a.go")
			So(fe.Files[0].Kind, ShouldEqual, canonical.ChangeModified)
		})
		Convey("edit with file_path input (no locations) is recognized too", func() {
			ev, _ := translateToolCall(&acpsdk.SessionUpdateToolCall{
				ToolCallId: "tc-6",
				Title:      "edit",
				Kind:       acpsdk.ToolKindEdit,
				RawInput:   map[string]any{"file_path": "/abs/b.go", "old_string": "x", "new_string": "y"},
			})
			So(ev.Canonical, ShouldNotBeNil)
			So(canonical.KindOf(ev.Canonical), ShouldEqual, canonical.KindFileEdit)
		})
		Convey("edit carrying full content becomes FileWrite", func() {
			ev, _ := translateToolCall(&acpsdk.SessionUpdateToolCall{
				ToolCallId: "tc-7",
				Title:      "edit",
				Kind:       acpsdk.ToolKindEdit,
				RawInput:   map[string]any{"file_path": "/abs/c.go", "content": "line1\nline2"},
			})
			fw, ok := ev.Canonical.(canonical.FileWrite)
			So(ok, ShouldBeTrue)
			So(fw.Path, ShouldEqual, "/abs/c.go")
			So(fw.Lines, ShouldEqual, 2)
			So(fw.Content, ShouldEqual, "line1\nline2")
		})
		Convey("delete maps every location to a deleted patch", func() {
			ev, _ := translateToolCall(&acpsdk.SessionUpdateToolCall{
				ToolCallId: "tc-8",
				Title:      "delete",
				Kind:       acpsdk.ToolKindDelete,
				Locations:  []acpsdk.ToolCallLocation{{Path: "/abs/1.go"}, {Path: "/abs/2.go"}},
			})
			fe, ok := ev.Canonical.(canonical.FileEdit)
			So(ok, ShouldBeTrue)
			require.Len(t, fe.Files, 2)
			So(fe.Files[0].Kind, ShouldEqual, canonical.ChangeDeleted)
			So(fe.Files[1].Kind, ShouldEqual, canonical.ChangeDeleted)
			var in map[string]any
			require.NoError(t, json.Unmarshal(ev.Input, &in))
			So(in["path"], ShouldEqual, "/abs/1.go")
		})
		Convey("codex-style changes[].path is recognized", func() {
			ev, _ := translateToolCall(&acpsdk.SessionUpdateToolCall{
				ToolCallId: "tc-9",
				Title:      "edit",
				Kind:       acpsdk.ToolKindEdit,
				RawInput:   map[string]any{"changes": []any{map[string]any{"path": "/abs/d.go", "kind": "modify"}}},
			})
			So(ev.Canonical, ShouldNotBeNil)
			var in map[string]any
			require.NoError(t, json.Unmarshal(ev.Input, &in))
			So(in["path"], ShouldEqual, "/abs/d.go")
		})
		Convey("read/execute/other kinds stay raw", func() {
			for _, kind := range []acpsdk.ToolKind{acpsdk.ToolKindRead, acpsdk.ToolKindExecute, acpsdk.ToolKindSearch} {
				ev, _ := translateToolCall(&acpsdk.SessionUpdateToolCall{
					ToolCallId: "tc-10",
					Title:      "t",
					Kind:       kind,
					Locations:  []acpsdk.ToolCallLocation{{Path: "/abs/x.go"}},
					RawInput:   map[string]any{"path": "/abs/x.go"},
				})
				So(ev.Canonical, ShouldBeNil)
			}
		})
		Convey("non-object rawInput passes through as-is", func() {
			ev, _ := translateToolCall(&acpsdk.SessionUpdateToolCall{
				ToolCallId: "tc-11",
				Title:      "t",
				RawInput:   "just a string",
			})
			requireJSONEq(t, json.RawMessage(`"just a string"`), ev.Input)
		})
	})
}

// TestTranslateToolCallUpdate 覆盖更新行：非终态重发 ToolCall（同 ID），
// 终态产出 ToolResult（completed/failed）。
func TestTranslateToolCallUpdate(t *testing.T) {
	base := func() toolCallState {
		_, st := translateToolCall(&acpsdk.SessionUpdateToolCall{
			ToolCallId: "tc-1",
			Title:      "Run tests",
			Kind:       acpsdk.ToolKindExecute,
			RawInput:   map[string]any{"command": "go test"},
		})
		return st
	}
	Convey("Given an existing tool call", t, func() {
		Convey("missing status re-emits the same ToolCall id", func() {
			events := translateToolCallUpdate(base(), &acpsdk.SessionToolCallUpdate{ToolCallId: "tc-1"})
			require.Len(t, events, 1)
			call, ok := events[0].(agentruntime.ToolCall)
			So(ok, ShouldBeTrue)
			So(call.ID, ShouldEqual, "tc-1")
			So(call.Name, ShouldEqual, "Run tests")
		})
		Convey("in_progress re-emits the ToolCall with the new title", func() {
			events := translateToolCallUpdate(base(), &acpsdk.SessionToolCallUpdate{
				ToolCallId: "tc-1",
				Title:      ptr("Running go test ./..."),
				Status:     ptr(acpsdk.ToolCallStatusInProgress),
			})
			require.Len(t, events, 1)
			So(events[0].(agentruntime.ToolCall).Name, ShouldEqual, "Running go test ./...")
		})
		Convey("completed yields a ToolResult with joined content text", func() {
			events := translateToolCallUpdate(base(), &acpsdk.SessionToolCallUpdate{
				ToolCallId: "tc-1",
				Status:     ptr(acpsdk.ToolCallStatusCompleted),
				Content: []acpsdk.ToolCallContent{
					acpsdk.ToolContent(acpsdk.TextBlock("line1\n")),
					acpsdk.ToolContent(acpsdk.TextBlock("line2")),
				},
			})
			require.Len(t, events, 1)
			res, ok := events[0].(agentruntime.ToolResult)
			So(ok, ShouldBeTrue)
			So(res.ToolCallID, ShouldEqual, "tc-1")
			So(res.Content, ShouldEqual, "line1\nline2")
			So(res.IsError, ShouldBeFalse)
		})
		Convey("failed yields an error ToolResult", func() {
			events := translateToolCallUpdate(base(), &acpsdk.SessionToolCallUpdate{
				ToolCallId: "tc-1",
				Status:     ptr(acpsdk.ToolCallStatusFailed),
				Content:    []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock("boom"))},
			})
			require.Len(t, events, 1)
			res := events[0].(agentruntime.ToolResult)
			So(res.IsError, ShouldBeTrue)
			So(res.Content, ShouldEqual, "boom")
		})
		Convey("completed without content yields an empty ToolResult", func() {
			events := translateToolCallUpdate(base(), &acpsdk.SessionToolCallUpdate{
				ToolCallId: "tc-1",
				Status:     ptr(acpsdk.ToolCallStatusCompleted),
			})
			require.Len(t, events, 1)
			So(events[0].(agentruntime.ToolResult).Content, ShouldEqual, "")
		})
	})
}

// TestTranslatePlan 覆盖 plan 行：Steps 映射，Text 不填。
func TestTranslatePlan(t *testing.T) {
	Convey("Given a plan update", t, func() {
		events, _ := translate(acpsdk.SessionUpdate{Plan: &acpsdk.SessionUpdatePlan{
			Entries: []acpsdk.PlanEntry{
				{Content: "scan the repo", Status: acpsdk.PlanEntryStatusCompleted},
				{Content: "write the patch", Status: acpsdk.PlanEntryStatusInProgress},
				{Content: "run the tests", Status: acpsdk.PlanEntryStatusPending},
			},
		}})
		require.Len(t, events, 1)
		pu, ok := events[0].(agentruntime.PlanUpdated)
		So(ok, ShouldBeTrue)
		require.Len(t, pu.Plan.Steps, 3)
		So(pu.Plan.Steps[0].Status, ShouldEqual, canonical.StepCompleted)
		So(pu.Plan.Steps[1].Status, ShouldEqual, canonical.StepInProgress)
		So(pu.Plan.Steps[2].Status, ShouldEqual, canonical.StepPending)
		So(pu.Plan.Text, ShouldEqual, "")
		Convey("An empty entry list produces no event", func() {
			events, _ := translate(acpsdk.SessionUpdate{Plan: &acpsdk.SessionUpdatePlan{}})
			So(len(events), ShouldEqual, 0)
		})
	})
}

// TestTranslateUsageAndUnknown 覆盖 usage_update 与未知 update。
func TestTranslateUsageAndUnknown(t *testing.T) {
	Convey("Given a usage_update frame", t, func() {
		Convey("it returns a usage snapshot with used/size", func() {
			events, usage := translate(acpsdk.SessionUpdate{UsageUpdate: &acpsdk.SessionUsageUpdate{
				Used: 1200,
				Size: 200000,
			}})
			So(len(events), ShouldEqual, 0)
			So(usage.used, ShouldEqual, 1200)
			So(usage.size, ShouldEqual, 200000)
			So(usage.present, ShouldBeTrue)
		})
		Convey("zero snapshot when absent", func() {
			_, usage := translate(textChunk("x"))
			So(usage.present, ShouldBeFalse)
		})
	})
	Convey("Given updates that produce no events", t, func() {
		for _, u := range []acpsdk.SessionUpdate{
			{AvailableCommandsUpdate: &acpsdk.SessionAvailableCommandsUpdate{}},
			{CurrentModeUpdate: &acpsdk.SessionCurrentModeUpdate{}},
			{ConfigOptionUpdate: &acpsdk.SessionConfigOptionUpdate{}},
			{SessionInfoUpdate: &acpsdk.SessionSessionInfoUpdate{}},
			{PlanUpdate: &acpsdk.SessionPlanUpdate{}},
			{PlanRemoved: &acpsdk.SessionUpdatePlanRemoved{}},
			{}, // unknown / undecodable update: union is empty
		} {
			events, usage := translate(u)
			So(len(events), ShouldEqual, 0)
			So(usage.present, ShouldBeFalse)
		}
		Convey("updateKind names the variant for raw-frame logs", func() {
			So(updateKind(acpsdk.UpdateUserMessageText("x")), ShouldEqual, "user_message_chunk")
			So(updateKind(acpsdk.SessionUpdate{}), ShouldEqual, "unknown")
		})
	})
}

// TestPlanStepStatusMapping 覆盖 §3.7 的 plan status 映射。
func TestPlanStepStatusMapping(t *testing.T) {
	cases := map[acpsdk.PlanEntryStatus]canonical.PlanStepStatus{
		acpsdk.PlanEntryStatusPending:       canonical.StepPending,
		acpsdk.PlanEntryStatusInProgress:    canonical.StepInProgress,
		acpsdk.PlanEntryStatusCompleted:     canonical.StepCompleted,
		acpsdk.PlanEntryStatus("cancelled"): canonical.StepCancelled, //nolint:misspell // defensive: schema has no cancelled const, agents may still send it
		acpsdk.PlanEntryStatus(""):          canonical.StepPending,
	}
	for in, want := range cases {
		assert.Equal(t, want, planStepStatus(in), "planStepStatus(%q)", in)
	}
}
