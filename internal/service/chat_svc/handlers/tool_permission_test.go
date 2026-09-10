package handlers

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

func TestToolPermissionRequestHandler(t *testing.T) {
	Convey("ToolPermissionRequest 入 acc + emit", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		input, _ := json.Marshal(map[string]any{"command": "ls"})
		err := ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{
				RequestID: "perm-1", ToolName: "Bash", Input: input,
			},
			acc, emit, nil, nil)
		So(err, ShouldBeNil)

		got := acc.Finalize()[0].(*blocks.ToolPermissionBlock)
		So(got.RequestID, ShouldEqual, "perm-1")
		So(got.ToolName, ShouldEqual, "Bash")
		So(got.ToolInput["command"], ShouldEqual, "ls")
		p := emit.events[0].payload.(map[string]any)
		So(p["kind"], ShouldEqual, "tool_permission_request")
	})
}

func TestToolPermissionResolvedHandler_Allow(t *testing.T) {
	Convey("Resolved Allow=true 改 block 状态", t, func() {
		acc := turn.New()
		_ = ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{RequestID: "perm-1", ToolName: "Bash"},
			acc, nil, nil, nil)

		err := ToolPermissionResolvedHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionResolved{RequestID: "perm-1", Allowed: true, AlwaysAllow: true},
			acc, nil, nil, nil)
		So(err, ShouldBeNil)

		got := acc.Finalize()[0].(*blocks.ToolPermissionBlock)
		So(got.Resolved, ShouldBeTrue)
		So(got.Allowed, ShouldBeTrue)
		So(got.AlwaysAllow, ShouldBeTrue)
	})
}

func TestToolPermissionResolvedHandler_DenyReason(t *testing.T) {
	Convey("Resolved Allow=false 带 DenyReason", t, func() {
		acc := turn.New()
		_ = ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{RequestID: "perm-2"}, acc, nil, nil, nil)
		_ = ToolPermissionResolvedHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionResolved{RequestID: "perm-2", Allowed: false, DenyReason: "user refused"},
			acc, nil, nil, nil)

		got := acc.Finalize()[0].(*blocks.ToolPermissionBlock)
		So(got.Allowed, ShouldBeFalse)
		So(got.DenyReason, ShouldEqual, "user refused")
	})
}

// resolved emit 必须带 toolName / toolInput / toolPermission block —— 否则
// dispatcher_emitter 据空 toolName 误把 ExitPlanMode 切到 tool.permission canonical,
// 前端 PlanApproveCard 被覆盖成空白 ToolPermissionCard。
func TestToolPermissionResolvedHandler_EmitCarriesToolNameAndBlock(t *testing.T) {
	Convey("Resolved emit 携带 toolName + toolInput + toolPermission block", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		input, _ := json.Marshal(map[string]any{"plan": "## Plan\n1. a"})
		_ = ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{RequestID: "perm-3", ToolName: "ExitPlanMode", Input: input},
			acc, emit, nil, nil)
		// 清掉 request emit,只看 resolved emit。
		emit.events = nil

		err := ToolPermissionResolvedHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionResolved{RequestID: "perm-3", Allowed: true},
			acc, emit, nil, nil)
		So(err, ShouldBeNil)
		So(len(emit.events), ShouldEqual, 1)

		p := emit.events[0].payload.(map[string]any)
		So(p["kind"], ShouldEqual, "tool_permission_request")
		So(p["requestId"], ShouldEqual, "perm-3")
		So(p["resolved"], ShouldEqual, true)
		So(p["allowed"], ShouldEqual, true)
		So(p["toolName"], ShouldEqual, "ExitPlanMode")
		ti, ok := p["toolInput"].(map[string]any)
		So(ok, ShouldBeTrue)
		So(ti["plan"], ShouldEqual, "## Plan\n1. a")
		blk, ok := p["toolPermission"].(*blocks.ToolPermissionBlock)
		So(ok, ShouldBeTrue)
		So(blk.ToolName, ShouldEqual, "ExitPlanMode")
		So(blk.Resolved, ShouldBeTrue)
		So(blk.Allowed, ShouldBeTrue)
	})
}

// Request 阶段 ExitPlanMode 应该携带 canonical.PlanApproveRequest{Actions: [...]}
// 按 LaunchPermissionMode 分支。
func TestToolPermissionRequestHandler_ExitPlanMode_CanonicalActions(t *testing.T) {
	Convey("ExitPlanMode Request emit canonical 是 PlanApproveRequest 带 Actions", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		input, _ := json.Marshal(map[string]any{"plan": "## P"})
		tc := &turn.TurnContext{LaunchPermissionMode: "bypassPermissions"}
		err := ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{RequestID: "perm-x", ToolName: "ExitPlanMode", Input: input},
			acc, emit, nil, tc)
		So(err, ShouldBeNil)
		p := emit.events[0].payload.(map[string]any)
		c, ok := p["canonical"].(canonical.PlanApproveRequest)
		So(ok, ShouldBeTrue)
		So(c.RequestID, ShouldEqual, "perm-x")
		So(c.PlanText, ShouldEqual, "## P")
		So(c.Resolved, ShouldBeFalse)
		So(c.Actions, ShouldHaveLength, 3)
		So(c.Actions[0].ID, ShouldEqual, "plan.approve.bypass_permissions")
		So(c.Actions[2].ID, ShouldEqual, "plan.refine")
	})
	Convey("launch=acceptEdits → 首项是 accept_edits", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		input, _ := json.Marshal(map[string]any{"plan": ""})
		tc := &turn.TurnContext{LaunchPermissionMode: "acceptEdits"}
		_ = ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{RequestID: "perm-y", ToolName: "ExitPlanMode", Input: input},
			acc, emit, nil, tc)
		p := emit.events[0].payload.(map[string]any)
		c := p["canonical"].(canonical.PlanApproveRequest)
		So(c.Actions[0].ID, ShouldEqual, "plan.approve.accept_edits")
	})
	Convey("普通工具(非 ExitPlanMode) canonical 是 ToolPermission 且无 Actions", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		input, _ := json.Marshal(map[string]any{"command": "ls"})
		_ = ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{RequestID: "perm-z", ToolName: "Bash", Input: input},
			acc, emit, nil, nil)
		p := emit.events[0].payload.(map[string]any)
		c, ok := p["canonical"].(canonical.ToolPermission)
		So(ok, ShouldBeTrue)
		So(c.ToolName, ShouldEqual, "Bash")
	})
}

// claudecode v2.1.x:ExitPlanMode 的 input={},plan markdown 在前一个 Write 工具里。
// buildToolPermissionCanonical 在 input["plan"] 为空时回退到 tc.LastPlanWriteContent。
func TestToolPermissionRequestHandler_ExitPlanMode_FallbackToWriteCapture(t *testing.T) {
	Convey("input={} 时 PlanText 取自 tc.LastPlanWriteContent + 持久化 block.ToolInput 也带 plan", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		input, _ := json.Marshal(map[string]any{})
		tc := &turn.TurnContext{
			LaunchPermissionMode: "acceptEdits",
			LastPlanWriteContent: "# Plan\n1. step a\n",
		}
		err := ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{RequestID: "perm-fb1", ToolName: "ExitPlanMode", Input: input},
			acc, emit, nil, tc)
		So(err, ShouldBeNil)
		p := emit.events[0].payload.(map[string]any)
		c := p["canonical"].(canonical.PlanApproveRequest)
		So(c.PlanText, ShouldEqual, "# Plan\n1. step a\n")
		// 回放路径 toolPermissionBlockToChatBlock 直接读 ToolInput["plan"];
		// 必须把捕获到的 plan 也注入到 block 持久化字段。
		blk := acc.Finalize()[0].(*blocks.ToolPermissionBlock)
		So(blk.ToolInput["plan"], ShouldEqual, "# Plan\n1. step a\n")
	})
	Convey("input['plan'] 非空时取 input(back-compat 优先)", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		input, _ := json.Marshal(map[string]any{"plan": "## legacy"})
		tc := &turn.TurnContext{
			LaunchPermissionMode: "acceptEdits",
			LastPlanWriteContent: "## from write",
		}
		_ = ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{RequestID: "perm-fb2", ToolName: "ExitPlanMode", Input: input},
			acc, emit, nil, tc)
		p := emit.events[0].payload.(map[string]any)
		c := p["canonical"].(canonical.PlanApproveRequest)
		So(c.PlanText, ShouldEqual, "## legacy")
	})
	Convey("input={} + tc.LastPlanWriteContent 空 → PlanText 仍为空(无 panic)", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		input, _ := json.Marshal(map[string]any{})
		tc := &turn.TurnContext{LaunchPermissionMode: "acceptEdits"}
		_ = ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{RequestID: "perm-fb3", ToolName: "ExitPlanMode", Input: input},
			acc, emit, nil, tc)
		p := emit.events[0].payload.(map[string]any)
		c := p["canonical"].(canonical.PlanApproveRequest)
		So(c.PlanText, ShouldEqual, "")
	})
}

// Resolved 阶段 ExitPlanMode 也带 canonical,Actions 必须为 nil(只读)。
func TestToolPermissionResolvedHandler_ExitPlanMode_CanonicalNoActions(t *testing.T) {
	Convey("Resolved ExitPlanMode canonical.Actions == nil", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		input, _ := json.Marshal(map[string]any{"plan": "## P"})
		tc := &turn.TurnContext{LaunchPermissionMode: "bypassPermissions"}
		_ = ToolPermissionRequestHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionRequest{RequestID: "perm-r", ToolName: "ExitPlanMode", Input: input},
			acc, emit, nil, tc)
		emit.events = nil
		_ = ToolPermissionResolvedHandler{}.Apply(context.Background(),
			agentruntime.ToolPermissionResolved{RequestID: "perm-r", Allowed: true},
			acc, emit, nil, tc)
		p := emit.events[0].payload.(map[string]any)
		c := p["canonical"].(canonical.PlanApproveRequest)
		So(c.Resolved, ShouldBeTrue)
		So(c.Allowed, ShouldBeTrue)
		So(c.Actions, ShouldBeNil)
	})
}
