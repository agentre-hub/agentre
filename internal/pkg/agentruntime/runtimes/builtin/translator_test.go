package builtin

import (
	"testing"

	"github.com/cago-frame/agents/agent"
	"github.com/cago-frame/agents/agent/blocks"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// TestTranslate_TextDelta agent.EventTextDelta → agentruntime.TextDelta。
func TestTranslate_TextDelta(t *testing.T) {
	Convey("EventTextDelta 翻成 TextDelta,Text 字段透传 Delta", t, func() {
		out := translate(agent.Event{Kind: agent.EventTextDelta, Delta: "hello"})
		So(len(out), ShouldEqual, 1)
		td, ok := out[0].(agentruntime.TextDelta)
		So(ok, ShouldBeTrue)
		So(td.Text, ShouldEqual, "hello")
	})
}

// TestTranslate_ThinkingDelta agent.EventThinkingDelta → agentruntime.ThinkingDelta。
func TestTranslate_ThinkingDelta(t *testing.T) {
	Convey("EventThinkingDelta 翻成 ThinkingDelta", t, func() {
		out := translate(agent.Event{Kind: agent.EventThinkingDelta, Delta: "thinking…"})
		So(len(out), ShouldEqual, 1)
		td, ok := out[0].(agentruntime.ThinkingDelta)
		So(ok, ShouldBeTrue)
		So(td.Text, ShouldEqual, "thinking…")
	})
}

// TestTranslate_PreToolUse agent.EventPreToolUse → agentruntime.ToolCall;
// Input map 转 JSON 字节。ev.Tool == nil 时不产事件。
func TestTranslate_PreToolUse(t *testing.T) {
	Convey("EventPreToolUse 携带 Tool 时翻成 ToolCall", t, func() {
		out := translate(agent.Event{
			Kind: agent.EventPreToolUse,
			Tool: &agent.ToolEvent{ToolUseID: "tu-1", Name: "Read", Input: map[string]any{"path": "/x"}},
		})
		So(len(out), ShouldEqual, 1)
		tc, ok := out[0].(agentruntime.ToolCall)
		So(ok, ShouldBeTrue)
		So(tc.ID, ShouldEqual, "tu-1")
		So(tc.Name, ShouldEqual, "Read")
		So(string(tc.Input), ShouldContainSubstring, `"path":"/x"`)
	})

	Convey("EventPreToolUse 但 ev.Tool == nil 时不产事件", t, func() {
		So(translate(agent.Event{Kind: agent.EventPreToolUse}), ShouldBeNil)
	})

	Convey("Tool.Input 空 map 时 ToolCall.Input 为 nil(与旧 marshalToolInput 语义一致)", t, func() {
		out := translate(agent.Event{
			Kind: agent.EventPreToolUse,
			Tool: &agent.ToolEvent{ToolUseID: "tu-2", Name: "Bash", Input: map[string]any{}},
		})
		tc, ok := out[0].(agentruntime.ToolCall)
		So(ok, ShouldBeTrue)
		So(tc.Input, ShouldBeNil)
	})
}

// TestTranslate_PostToolUse agent.EventPostToolUse → agentruntime.ToolResult;
// 拍平 ToolResultBlock 里所有 TextBlock 文本,IsError 透传。
func TestTranslate_PostToolUse(t *testing.T) {
	Convey("EventPostToolUse 携带 ToolResultBlock 时翻成 ToolResult", t, func() {
		out := translate(agent.Event{
			Kind: agent.EventPostToolUse,
			Tool: &agent.ToolEvent{
				ToolUseID: "tu-1",
				Output: &blocks.ToolResultBlock{
					Content: []blocks.ContentBlock{blocks.TextBlock{Text: "ok"}},
					IsError: false,
				},
			},
		})
		So(len(out), ShouldEqual, 1)
		tr, ok := out[0].(agentruntime.ToolResult)
		So(ok, ShouldBeTrue)
		So(tr.ToolCallID, ShouldEqual, "tu-1")
		So(tr.Content, ShouldEqual, "ok")
		So(tr.IsError, ShouldBeFalse)
	})

	Convey("IsError=true 透传", t, func() {
		out := translate(agent.Event{
			Kind: agent.EventPostToolUse,
			Tool: &agent.ToolEvent{
				ToolUseID: "tu-x",
				Output: &blocks.ToolResultBlock{
					Content: []blocks.ContentBlock{blocks.TextBlock{Text: "boom"}},
					IsError: true,
				},
			},
		})
		tr := out[0].(agentruntime.ToolResult)
		So(tr.IsError, ShouldBeTrue)
		So(tr.Content, ShouldEqual, "boom")
	})

	Convey("ev.Tool == nil 或 Output == nil 时不产事件", t, func() {
		So(translate(agent.Event{Kind: agent.EventPostToolUse}), ShouldBeNil)
		So(translate(agent.Event{
			Kind: agent.EventPostToolUse,
			Tool: &agent.ToolEvent{ToolUseID: "tu"},
		}), ShouldBeNil)
	})
}

// TestTranslate_SteerConsumed translator 输出单元素 Steers slice;Run() 负责
// 把同一安全点连续到达的多帧 SteerConsumed 合并成单帧。
func TestTranslate_SteerConsumed(t *testing.T) {
	Convey("EventSteerConsumed 翻成 SteerConsumed{Steers:[1]}", t, func() {
		out := translate(agent.Event{Kind: agent.EventSteerConsumed, SteerID: "q-1", Delta: "stop"})
		So(len(out), ShouldEqual, 1)
		sc, ok := out[0].(agentruntime.SteerConsumed)
		So(ok, ShouldBeTrue)
		So(len(sc.Steers), ShouldEqual, 1)
		So(sc.Steers[0].QueuedID, ShouldEqual, "q-1")
		So(sc.Steers[0].Text, ShouldEqual, "stop")
	})
}

// TestTranslate_TurnEnd_NoEvent EventTurnEnd 不产新 Event(usage 由 Run() 写回
// *RunResult)。
func TestTranslate_TurnEnd_NoEvent(t *testing.T) {
	Convey("EventTurnEnd 在 translator 层不产事件", t, func() {
		So(translate(agent.Event{Kind: agent.EventTurnEnd}), ShouldBeNil)
	})
}

// TestTranslate_Error agent.EventError 带 Error → ErrorEvent;不带 Error 时不产。
func TestTranslate_Error(t *testing.T) {
	Convey("EventError 携带 Error 时翻成 ErrorEvent", t, func() {
		out := translate(agent.Event{Kind: agent.EventError, Error: errBoomForTest})
		So(len(out), ShouldEqual, 1)
		e, ok := out[0].(agentruntime.ErrorEvent)
		So(ok, ShouldBeTrue)
		So(e.Err, ShouldEqual, errBoomForTest)
	})

	Convey("EventError 但 Error == nil 时不产事件", t, func() {
		So(translate(agent.Event{Kind: agent.EventError}), ShouldBeNil)
	})
}

var errBoomForTest = &boomErr{msg: "boom"}

type boomErr struct{ msg string }

func (e *boomErr) Error() string { return e.msg }
