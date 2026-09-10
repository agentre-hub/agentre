package blocks

import (
	"strings"
	"testing"

	"github.com/cago-frame/agents/agent"
	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"
)

func TestNestedToolBlocks_TypeAndAudience(t *testing.T) {
	Convey("Nested tool blocks 是 ToUI(防 LLM context 泄漏)", t, func() {
		So(NestedToolUseBlock{}.Audience(), ShouldEqual, cagoblocks.ToUI)
		So(NestedToolResultBlock{}.Audience(), ShouldEqual, cagoblocks.ToUI)
		So(NestedToolUseBlock{}.Type(), ShouldEqual, "nested_tool_use")
		So(NestedToolResultBlock{}.Type(), ShouldEqual, "nested_tool_result")
	})
}

func TestNestedToolUse_RoundTrip(t *testing.T) {
	Convey("NestedToolUseBlock round-trip 保留父调用和运行分组", t, func() {
		b := &NestedToolUseBlock{
			ID: "nested-1", Name: "Read", ParentToolCallID: "task-1", SubagentRunID: "run-1",
			Input: map[string]any{"file_path": "/tmp/x"},
		}
		sb, err := cagoblocks.Encode(b)
		So(err, ShouldBeNil)
		decoded, err := cagoblocks.Decode(sb)
		So(err, ShouldBeNil)
		got, ok := decoded.(NestedToolUseBlock)
		So(ok, ShouldBeTrue)
		So(got.ParentToolCallID, ShouldEqual, "task-1")
		So(got.SubagentRunID, ShouldEqual, "run-1")
	})

	Convey("NestedToolResultBlock round-trip 保留缺失的运行 ID 而不丢块", t, func() {
		b := &NestedToolResultBlock{ToolCallID: "nested-1", Content: "ok", ParentToolCallID: "task-1"}
		sb, err := cagoblocks.Encode(b)
		So(err, ShouldBeNil)
		decoded, err := cagoblocks.Decode(sb)
		So(err, ShouldBeNil)
		got, ok := decoded.(NestedToolResultBlock)
		So(ok, ShouldBeTrue)
		So(got.ParentToolCallID, ShouldEqual, "task-1")
		So(got.SubagentRunID, ShouldEqual, "")
	})
}

func TestNestedToolBlocks_StayOutOfOuterModelContext(t *testing.T) {
	Convey("Given nested child payloads and one extension-authored outer result, When building model context, Then only the outer result appears exactly once", t, func() {
		const childSentinel = "CHILD_SENTINEL"
		const outerSentinel = "OUTER_SUMMARY_SENTINEL"
		req := agent.BuildRequest(agent.RequestSpec{Messages: []agent.Message{{
			Role: agent.RoleTool,
			Content: []cagoblocks.ContentBlock{
				NestedToolUseBlock{ID: "child", Name: "Read", Input: map[string]any{"secret": childSentinel}, ParentToolCallID: "outer", SubagentRunID: "run-0"},
				NestedToolResultBlock{ToolCallID: "child", Content: childSentinel, ParentToolCallID: "outer", SubagentRunID: "run-0"},
				cagoblocks.ToolResultBlock{ToolUseID: "outer", Content: []cagoblocks.ContentBlock{cagoblocks.TextBlock{Text: outerSentinel}}},
			},
		}}})

		So(req.Messages, ShouldHaveLength, 1)
		So(req.Messages[0].Content, ShouldNotContainSubstring, childSentinel)
		So(strings.Count(req.Messages[0].Content, outerSentinel), ShouldEqual, 1)
		So(req.Messages[0].ToolCallID, ShouldEqual, "outer")
	})
}
