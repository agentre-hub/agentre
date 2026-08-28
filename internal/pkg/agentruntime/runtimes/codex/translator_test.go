package codex

import (
	"encoding/json"
	"testing"

	"github.com/cago-frame/agents/provider"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/pkg/codex"
)

// TestTranslate_TextThinkingDelta 基本 emit 类型映射。
func TestTranslate_TextThinkingDelta(t *testing.T) {
	Convey("EventTextDelta → TextDelta", t, func() {
		out, _, _ := translate(codex.Event{Kind: codex.EventTextDelta, Text: "hi"})
		So(len(out), ShouldEqual, 1)
		td, ok := out[0].(agentruntime.TextDelta)
		So(ok, ShouldBeTrue)
		So(td.Text, ShouldEqual, "hi")
	})

	Convey("EventThinkingDelta → ThinkingDelta", t, func() {
		out, _, _ := translate(codex.Event{Kind: codex.EventThinkingDelta, Text: "think"})
		So(len(out), ShouldEqual, 1)
		_, ok := out[0].(agentruntime.ThinkingDelta)
		So(ok, ShouldBeTrue)
	})
}

// TestTranslate_PreToolUseRawTools 非 file_change 工具走 raw,Canonical=nil。
func TestTranslate_PreToolUseRawTools(t *testing.T) {
	Convey("command_execution → ToolCall.Canonical=nil(走 raw)", t, func() {
		out, _, _ := translate(codex.Event{
			Kind: codex.EventPreToolUse,
			Tool: &codex.ToolEvent{ID: "tu-1", Name: "command_execution", Input: json.RawMessage(`{"cmd":"ls"}`)},
		})
		So(len(out), ShouldEqual, 1)
		tc, ok := out[0].(agentruntime.ToolCall)
		So(ok, ShouldBeTrue)
		So(tc.ID, ShouldEqual, "tu-1")
		So(tc.Name, ShouldEqual, "command_execution")
		So(tc.Canonical, ShouldBeNil)
	})
}

// TestTranslate_FileChangeCanonical file_change 工具 → ToolCall.Canonical =
// FileEdit(per-Kind dispatch via diff.FromFileChange;Plan A 不细分 FileWrite
// for created,统一走 FileEdit 保留 diff 表示)。
func TestTranslate_FileChangeCanonical(t *testing.T) {
	Convey("file_change with modified diff → canonical.FileEdit", t, func() {
		raw := json.RawMessage(`{"changes":[{"path":"/x.go","kind":"modified","diff":"@@ -1,1 +1,1 @@\n-old\n+new\n"}]}`)
		out, _, _ := translate(codex.Event{
			Kind: codex.EventPreToolUse,
			Tool: &codex.ToolEvent{ID: "tu", Name: "file_change", Input: raw},
		})
		tc := out[0].(agentruntime.ToolCall)
		fe, ok := tc.Canonical.(canonical.FileEdit)
		So(ok, ShouldBeTrue)
		So(len(fe.Files), ShouldEqual, 1)
		So(fe.Files[0].Path, ShouldEqual, "/x.go")
		So(fe.Files[0].Kind, ShouldEqual, canonical.ChangeModified)
		So(len(fe.Files[0].Hunks), ShouldBeGreaterThan, 0)
	})

	Convey("file_change with created kind 保留 diff,仍走 FileEdit", t, func() {
		raw := json.RawMessage(`{"changes":[{"path":"/new.go","kind":"created","diff":"@@ -0,0 +1,2 @@\n+package x\n+\n"}]}`)
		out, _, _ := translate(codex.Event{
			Kind: codex.EventPreToolUse,
			Tool: &codex.ToolEvent{ID: "tu", Name: "file_change", Input: raw},
		})
		tc := out[0].(agentruntime.ToolCall)
		fe, ok := tc.Canonical.(canonical.FileEdit)
		So(ok, ShouldBeTrue)
		So(fe.Files[0].Kind, ShouldEqual, canonical.ChangeCreated)
	})

	Convey("file_change with Codex add kind object raw content → FileEdit hunk", t, func() {
		raw := json.RawMessage(`{"changes":[{"path":"/hello","kind":{"type":"add"},"diff":"Hello\n"}]}`)
		out, _, _ := translate(codex.Event{
			Kind: codex.EventPreToolUse,
			Tool: &codex.ToolEvent{ID: "tu", Name: "file_change", Input: raw},
		})
		tc := out[0].(agentruntime.ToolCall)
		fe, ok := tc.Canonical.(canonical.FileEdit)
		So(ok, ShouldBeTrue)
		So(fe.Files[0].Path, ShouldEqual, "/hello")
		So(fe.Files[0].Kind, ShouldEqual, canonical.ChangeCreated)
		So(fe.Files[0].Plus, ShouldEqual, 1)
		So(fe.Files[0].Hunks, ShouldHaveLength, 1)
		So(fe.Files[0].Hunks[0].Lines[0].Text, ShouldEqual, "Hello")
	})

	Convey("file_change 空 changes → nil canonical(走 raw)", t, func() {
		raw := json.RawMessage(`{"changes":[]}`)
		out, _, _ := translate(codex.Event{
			Kind: codex.EventPreToolUse,
			Tool: &codex.ToolEvent{ID: "tu", Name: "file_change", Input: raw},
		})
		tc := out[0].(agentruntime.ToolCall)
		So(tc.Canonical, ShouldBeNil)
	})

	Convey("update_plan tool → canonical.PlanUpdate", t, func() {
		raw := json.RawMessage(`{"plan":[{"step":"inspect","status":"completed"},{"step":"report","status":"in_progress"}]}`)
		out, _, _ := translate(codex.Event{
			Kind: codex.EventPreToolUse,
			Tool: &codex.ToolEvent{ID: "tu-plan", Name: "update_plan", Input: raw},
		})
		tc := out[0].(agentruntime.ToolCall)
		pu, ok := tc.Canonical.(canonical.PlanUpdate)
		So(ok, ShouldBeTrue)
		So(pu.Steps, ShouldHaveLength, 2)
		So(pu.Steps[0].Step, ShouldEqual, "inspect")
		So(pu.Steps[0].Status, ShouldEqual, canonical.StepCompleted)
		So(pu.Steps[1].Step, ShouldEqual, "report")
		So(pu.Steps[1].Status, ShouldEqual, canonical.StepInProgress)
	})

	Convey("非 file_change/update_plan 工具不识别", t, func() {
		So(recognizeCanonical("command_execution", json.RawMessage(`{}`)), ShouldBeNil)
		So(recognizeCanonical("custom_tool", json.RawMessage(`{}`)), ShouldBeNil)
	})
}

// TestTranslate_PlanUpdated update_plan / plan delta 同时通过 EventPlanUpdated
// 触达。translator 收编到 canonical.PlanUpdate 一条 sealed PlanUpdated,
// 下游不再二态分支 PlanText vs Plan steps。
func TestTranslate_PlanUpdated(t *testing.T) {
	Convey("PlanText 形态(item/plan delta)→ PlanUpdated{Text}", t, func() {
		out, _, _ := translate(codex.Event{
			Kind:     codex.EventPlanUpdated,
			PlanText: "# Plan\n\n1. step\n",
		})
		So(len(out), ShouldEqual, 1)
		pu, ok := out[0].(agentruntime.PlanUpdated)
		So(ok, ShouldBeTrue)
		So(pu.Plan.Text, ShouldEqual, "# Plan\n\n1. step\n")
		So(len(pu.Plan.Steps), ShouldEqual, 0)
	})

	Convey("Plan steps 形态(turn/plan/updated)→ PlanUpdated{Steps}", t, func() {
		out, _, _ := translate(codex.Event{
			Kind: codex.EventPlanUpdated,
			Plan: []codex.PlanStep{
				{Step: "inspect", Status: "completed"},
				{Step: "report", Status: "in_progress"},
			},
		})
		pu := out[0].(agentruntime.PlanUpdated)
		So(len(pu.Plan.Steps), ShouldEqual, 2)
		So(pu.Plan.Steps[0].Step, ShouldEqual, "inspect")
		So(string(pu.Plan.Steps[1].Status), ShouldEqual, "in_progress") // status 不归一化
	})

	Convey("空 PlanText + 空 Plan → 不产事件", t, func() {
		events, _, _ := translate(codex.Event{Kind: codex.EventPlanUpdated, PlanText: "   "})
		So(events, ShouldBeNil)
	})
}

func TestAttachPlanModeActions(t *testing.T) {
	Convey("collaborationMode=plan + plan text → attach codex plan actions", t, func() {
		ev := attachPlanModeActions(agentruntime.PlanUpdated{
			Plan: canonical.PlanUpdate{Text: "# Plan\n\n1. step\n"},
		}, "plan")
		pu := ev.(agentruntime.PlanUpdated)
		So(pu.Plan.Actions, ShouldHaveLength, 2)
		So(pu.Plan.Actions[0].ID, ShouldEqual, "plan.execute")
		So(pu.Plan.Actions[0].Kind, ShouldEqual, canonical.PlanActionApprove)
		So(pu.Plan.Actions[1].ID, ShouldEqual, "plan.refine")
		So(pu.Plan.Actions[1].RequiresFeedback, ShouldBeTrue)
	})

	Convey("default mode 不附加 actions", t, func() {
		ev := attachPlanModeActions(agentruntime.PlanUpdated{
			Plan: canonical.PlanUpdate{Text: "# Plan\n\n1. step\n"},
		}, "default")
		pu := ev.(agentruntime.PlanUpdated)
		So(pu.Plan.Actions, ShouldBeNil)
	})

	Convey("steps-only plan update 不附加 actions", t, func() {
		ev := attachPlanModeActions(agentruntime.PlanUpdated{
			Plan: canonical.PlanUpdate{Steps: []canonical.PlanStep{{Step: "inspect", Status: canonical.StepPending}}},
		}, "plan")
		pu := ev.(agentruntime.PlanUpdated)
		So(pu.Plan.Actions, ShouldBeNil)
	})
}

// TestTranslate_RequestUserInput EventRequestUserInput → UserAskRequest;
// codex MultiSelect 永远 false(协议限制)。
func TestTranslate_RequestUserInput(t *testing.T) {
	Convey("EventRequestUserInput → UserAskRequest", t, func() {
		out, _, _ := translate(codex.Event{
			Kind: codex.EventRequestUserInput,
			RequestUserInput: &codex.RequestUserInputEvent{
				RequestID: "req-1",
				ItemID:    "item-a",
				Questions: []codex.RequestUserInputQuestion{
					{ID: "q1", Question: "Which?", Options: []codex.RequestUserInputOption{{Label: "A"}, {Label: "B"}}},
				},
			},
		})
		So(len(out), ShouldEqual, 1)
		uar, ok := out[0].(agentruntime.UserAskRequest)
		So(ok, ShouldBeTrue)
		So(uar.RequestID, ShouldEqual, "req-1")
		So(uar.ToolCallID, ShouldEqual, "item-a")
		So(len(uar.Questions), ShouldEqual, 1)
		So(uar.Questions[0].MultiSelect, ShouldBeFalse) // codex 协议固定 false
		So(len(uar.Questions[0].Options), ShouldEqual, 2)
	})
}

func TestTranslate_ApprovalRequest(t *testing.T) {
	Convey("EventApprovalRequest → ToolPermissionRequest", t, func() {
		out, _, _ := translate(codex.Event{
			Kind: codex.EventApprovalRequest,
			Approval: &codex.ApprovalRequestEvent{
				RequestID: "approval-1",
				ItemID:    "item-command",
				ToolName:  "Bash",
				Input:     json.RawMessage(`{"command":"rm -rf build"}`),
			},
		})
		So(len(out), ShouldEqual, 1)
		req, ok := out[0].(agentruntime.ToolPermissionRequest)
		So(ok, ShouldBeTrue)
		So(req.RequestID, ShouldEqual, "approval-1")
		So(req.ToolCallID, ShouldEqual, "item-command")
		So(req.ToolName, ShouldEqual, "Bash")
		So(string(req.Input), ShouldEqual, `{"command":"rm -rf build"}`)
	})

	Convey("EventApprovalRequest without payload → no event", t, func() {
		out, _, _ := translate(codex.Event{Kind: codex.EventApprovalRequest})
		So(out, ShouldBeNil)
	})
}

// TestTranslate_Usage_OpenAIFamily TotalInputTokens 按 OpenAI family 聚合 =
// PromptTokens(不区分 cached / cacheCreation)。spec §A token contract。
func TestTranslate_Usage_OpenAIFamily(t *testing.T) {
	Convey("EventUsage → UsageUpdate.TotalInputTokens = PromptTokens", t, func() {
		out, _, _ := translate(codex.Event{
			Kind: codex.EventUsage,
			Usage: provider.Usage{
				PromptTokens:     5000,
				CompletionTokens: 200,
			},
		})
		So(len(out), ShouldEqual, 1)
		uu := out[0].(agentruntime.UsageUpdate)
		So(uu.TotalInputTokens, ShouldEqual, 5000)
	})
}

// TestTranslate_Error EventError + Err → ErrorEvent + stopErr。
func TestTranslate_Error(t *testing.T) {
	Convey("EventError 带 Err → ErrorEvent + stopErr 填", t, func() {
		errBoom := &boomErr{msg: "boom"}
		events, _, stopErr := translate(codex.Event{Kind: codex.EventError, Err: errBoom})
		So(len(events), ShouldEqual, 1)
		e, ok := events[0].(agentruntime.ErrorEvent)
		So(ok, ShouldBeTrue)
		So(e.Err, ShouldEqual, errBoom)
		So(stopErr, ShouldEqual, errBoom)
	})
}

func TestTranslate_CompactBoundary(t *testing.T) {
	Convey("EventCompactBoundary with metadata → CompactBoundary", t, func() {
		out, _, _ := translate(codex.Event{
			Kind: codex.EventCompactBoundary,
			Compact: &codex.CompactEvent{
				PreTokens: 12345,
				Trigger:   "manual",
			},
		})
		So(len(out), ShouldEqual, 1)
		cb, ok := out[0].(agentruntime.CompactBoundary)
		So(ok, ShouldBeTrue)
		So(cb.PreTokens, ShouldEqual, 12345)
		So(cb.Trigger, ShouldEqual, "manual")
	})

	Convey("EventCompactBoundary nil metadata → CompactBoundary zero value", t, func() {
		out, _, _ := translate(codex.Event{Kind: codex.EventCompactBoundary})
		So(len(out), ShouldEqual, 1)
		cb, ok := out[0].(agentruntime.CompactBoundary)
		So(ok, ShouldBeTrue)
		So(cb.PreTokens, ShouldEqual, 0)
		So(cb.Trigger, ShouldEqual, "")
	})
}

type boomErr struct{ msg string }

func (e *boomErr) Error() string { return e.msg }
