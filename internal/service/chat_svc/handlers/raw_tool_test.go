package handlers

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-ai/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-ai/agentre/internal/service/chat_svc/turn"
)

func TestToolCallHandler_Outer(t *testing.T) {
	Convey("外层 ToolCall → cago.ToolUseBlock + emit tool_use", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		input, _ := json.Marshal(map[string]any{"a": 1})
		err := ToolCallHandler{}.Apply(
			context.Background(),
			agentruntime.ToolCall{ID: "tu-1", Name: "X", Input: input},
			acc, emit, nil, nil,
		)
		So(err, ShouldBeNil)
		So(acc.HasToolUse("tu-1"), ShouldBeTrue)
		So(emit.events, ShouldHaveLength, 1)
		p := emit.events[0].payload.(map[string]any)
		So(p["kind"], ShouldEqual, "tool_use")
		So(p["toolUseId"], ShouldEqual, "tu-1")
	})
}

func TestToolCallHandler_Nested(t *testing.T) {
	Convey("内层 ToolCall (ParentToolCallID 非空) → 带运行分组的 NestedToolUseBlock + stream", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		err := ToolCallHandler{}.Apply(
			context.Background(),
			agentruntime.ToolCall{ID: "n-1", Name: "Read", ParentToolCallID: "task-1", SubagentRunID: "run-1"},
			acc, emit, nil, nil,
		)
		So(err, ShouldBeNil)
		final := acc.Finalize()
		So(final, ShouldHaveLength, 1)
		nested, isNested := final[0].(*blocks.NestedToolUseBlock)
		So(isNested, ShouldBeTrue)
		So(nested.SubagentRunID, ShouldEqual, "run-1")
		payload := emit.events[0].payload.(map[string]any)
		So(payload["subagentRunId"], ShouldEqual, "run-1")
	})

	Convey("缺失运行 ID 的内层 ToolCall 保留为 fallback step 且不合成结果", t, func() {
		acc := turn.New()
		err := ToolCallHandler{}.Apply(
			context.Background(),
			agentruntime.ToolCall{ID: "n-unknown", Name: "Read", ParentToolCallID: "task-1"},
			acc, nil, nil, nil,
		)
		So(err, ShouldBeNil)
		final := acc.Finalize()
		So(final, ShouldHaveLength, 1)
		nested := final[0].(*blocks.NestedToolUseBlock)
		So(nested.SubagentRunID, ShouldEqual, "")
	})
}

func TestToolCallHandler_CanonicalKindInEmit(t *testing.T) {
	Convey("Canonical 非空时 emit 携带 canonicalKind", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		err := ToolCallHandler{}.Apply(
			context.Background(),
			agentruntime.ToolCall{
				ID: "tu-1", Name: "Write",
				Canonical: canonical.FileWrite{Path: "/tmp/a", Content: "x"},
			},
			acc, emit, nil, nil,
		)
		So(err, ShouldBeNil)
		p := emit.events[0].payload.(map[string]any)
		So(p["canonicalKind"], ShouldEqual, string(canonical.KindFileWrite))
	})
}

// claudecode v2.1.x:ExitPlanMode 的 input={},plan markdown 在前一个 Write 工具
// 写到 ~/.claude/plans/<slug>.md 里。ToolCallHandler 见到 Write→plan 路径时把
// content 寄存到 tc.LastPlanWriteContent,buildToolPermissionCanonical 兜底用。
func TestToolCallHandler_CapturesPlanWrite(t *testing.T) {
	Convey("Write canonical 到 .claude/plans/*.md 把 content 寄到 tc.LastPlanWriteContent", t, func() {
		acc := turn.New()
		tc := &turn.TurnContext{}
		err := ToolCallHandler{}.Apply(
			context.Background(),
			agentruntime.ToolCall{
				ID: "tu-plan", Name: "Write",
				Canonical: canonical.FileWrite{
					Path:    "/Users/x/.claude/plans/happy-cooking-alpaca.md",
					Content: "# Plan\n1. step a\n",
				},
			},
			acc, nil, nil, tc,
		)
		So(err, ShouldBeNil)
		So(tc.LastPlanWriteContent, ShouldEqual, "# Plan\n1. step a\n")
	})
	Convey("Write 到非 plan 路径不动 tc.LastPlanWriteContent", t, func() {
		acc := turn.New()
		tc := &turn.TurnContext{LastPlanWriteContent: "prev"}
		err := ToolCallHandler{}.Apply(
			context.Background(),
			agentruntime.ToolCall{
				ID: "tu-other", Name: "Write",
				Canonical: canonical.FileWrite{Path: "/tmp/a.md", Content: "noise"},
			},
			acc, nil, nil, tc,
		)
		So(err, ShouldBeNil)
		So(tc.LastPlanWriteContent, ShouldEqual, "prev")
	})
	Convey("tc == nil 不 panic", t, func() {
		acc := turn.New()
		err := ToolCallHandler{}.Apply(
			context.Background(),
			agentruntime.ToolCall{
				ID: "tu-plan2", Name: "Write",
				Canonical: canonical.FileWrite{Path: "/x/.claude/plans/y.md", Content: "p"},
			},
			acc, nil, nil, nil,
		)
		So(err, ShouldBeNil)
	})
}

func TestToolResultHandler_OrphanDropped(t *testing.T) {
	Convey("孤儿外层 ToolResult 丢弃 (spec §1.2)", t, func() {
		acc := turn.New()
		err := ToolResultHandler{}.Apply(
			context.Background(),
			agentruntime.ToolResult{ToolCallID: "orphan", Content: "x"},
			acc, nil, nil, nil,
		)
		So(err, ShouldBeNil)
		So(acc.Finalize(), ShouldHaveLength, 0)
	})
}

func TestToolResultHandler_WithPriorToolUse(t *testing.T) {
	Convey("先 ToolCall 再 ToolResult → 同时落 acc + emit", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		_ = ToolCallHandler{}.Apply(
			context.Background(),
			agentruntime.ToolCall{ID: "tu-1", Name: "Bash"},
			acc, emit, nil, nil,
		)
		err := ToolResultHandler{}.Apply(
			context.Background(),
			agentruntime.ToolResult{ToolCallID: "tu-1", Content: "ok"},
			acc, emit, nil, nil,
		)
		So(err, ShouldBeNil)
		// emit 应该有 tool_use + tool_result 2 条
		So(emit.events, ShouldHaveLength, 2)
		// final 应该有 ToolUseBlock + ToolResultBlock
		final := acc.Finalize()
		_, isUse := final[0].(*cagoblocks.ToolUseBlock)
		So(isUse, ShouldBeTrue)
		_, isResult := final[1].(*cagoblocks.ToolResultBlock)
		So(isResult, ShouldBeTrue)
	})

	Convey("内层 ToolResult 保留父调用与运行分组到 block 和 stream", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		err := ToolResultHandler{}.Apply(
			context.Background(),
			agentruntime.ToolResult{
				ToolCallID: "n-1", Content: "ok", ParentToolCallID: "task-1", SubagentRunID: "run-1",
			},
			acc, emit, nil, nil,
		)
		So(err, ShouldBeNil)
		final := acc.Finalize()
		So(final, ShouldHaveLength, 1)
		nested := final[0].(*blocks.NestedToolResultBlock)
		So(nested.SubagentRunID, ShouldEqual, "run-1")
		payload := emit.events[0].payload.(map[string]any)
		So(payload["subagentRunId"], ShouldEqual, "run-1")
	})
}

// 生成计时的接线:外层工具调用停表、结果回来开表(口径见 turn/timing.go)。内层
// (subagent 内部)工具不碰表 —— 派遣它的那个外层 Task 调用已经把表按住了,内层再
// 加减一遍只会在孤儿帧上留下按死表的挂账。
func TestToolHandlers_DriveGenerationClock(t *testing.T) {
	Convey("外层 tool_use 停表 / tool_result 开表", t, func() {
		acc := turn.New()
		tc := &turn.TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))

		So(ToolCallHandler{}.Apply(context.Background(),
			agentruntime.ToolCall{ID: "tu-1", Name: "Bash"}, acc, nil, nil, tc), ShouldBeNil)
		So(tc.BurstStartedAt.IsZero(), ShouldBeTrue)
		So(tc.PendingTools, ShouldContainKey, "tu-1")

		So(ToolResultHandler{}.Apply(context.Background(),
			agentruntime.ToolResult{ToolCallID: "tu-1", Content: "ok"}, acc, nil, nil, tc), ShouldBeNil)
		So(tc.PendingTools, ShouldBeEmpty)
		So(tc.BurstStartedAt.IsZero(), ShouldBeFalse)
	})

	Convey("内层 tool_use / tool_result 不动表", t, func() {
		acc := turn.New()
		tc := &turn.TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))

		So(ToolCallHandler{}.Apply(context.Background(),
			agentruntime.ToolCall{ID: "n-1", Name: "Read", ParentToolCallID: "tu-1"},
			acc, nil, nil, tc), ShouldBeNil)
		So(tc.BurstStartedAt.IsZero(), ShouldBeFalse)
		So(tc.PendingTools, ShouldBeEmpty)

		So(ToolResultHandler{}.Apply(context.Background(),
			agentruntime.ToolResult{ToolCallID: "n-1", ParentToolCallID: "tu-1", Content: "ok"},
			acc, nil, nil, tc), ShouldBeNil)
		So(tc.PendingTools, ShouldBeEmpty)
	})
}
