package ctlbind

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

func TestNestChildEvent(t *testing.T) {
	Convey("NestChildEvent 把子会话的事件转成挂在父 shell 调用下的嵌套事件", t, func() {
		input := json.RawMessage(`{"file_path":"/a.go","content":"x"}`)
		write := canonical.FileWrite{Path: "/a.go", Content: "x"}

		Convey("子会话外层 ToolCall → 父转录里的嵌套 ToolCall", func() {
			got, ok := NestChildEvent("sh-1", agentruntime.ToolCall{
				ID: "toolu_1", Name: "Write", Input: input, Canonical: write,
			})
			So(ok, ShouldBeTrue)
			So(got, ShouldResemble, agentruntime.ToolCall{
				ID: NestedToolCallID("sh-1", "toolu_1"), Name: "Write", Input: input, Canonical: write,
				ParentToolCallID: "sh-1",
			})
		})

		Convey("子会话外层 ToolResult → 父转录里的嵌套 ToolResult", func() {
			meta := json.RawMessage(`{"m":1}`)
			got, ok := NestChildEvent("sh-1", agentruntime.ToolResult{
				ToolCallID: "toolu_1", Content: "package a", IsError: true, Meta: meta,
			})
			So(ok, ShouldBeTrue)
			So(got, ShouldResemble, agentruntime.ToolResult{
				ToolCallID: NestedToolCallID("sh-1", "toolu_1"), Content: "package a", IsError: true, Meta: meta,
				ParentToolCallID: "sh-1",
			})
		})

		Convey("子会话自己的嵌套调用/结果、其它事件、空父 ID 都不转", func() {
			_, ok := NestChildEvent("sh-1", agentruntime.ToolCall{ID: "x", ParentToolCallID: "agent-1"})
			So(ok, ShouldBeFalse)
			_, ok = NestChildEvent("sh-1", agentruntime.ToolResult{ToolCallID: "x", ParentToolCallID: "agent-1"})
			So(ok, ShouldBeFalse)
			_, ok = NestChildEvent("sh-1", agentruntime.TextDelta{Text: "hi"})
			So(ok, ShouldBeFalse)
			_, ok = NestChildEvent("", agentruntime.ToolCall{ID: "x"})
			So(ok, ShouldBeFalse)
		})

		Convey("嵌套 ID 由父调用 + 子 ID 确定:同一子 ID 恒等,不同父调用 / 拼接歧义不撞", func() {
			So(NestedToolCallID("sh-1", "item_3"), ShouldEqual, NestedToolCallID("sh-1", "item_3"))
			So(NestedToolCallID("sh-1", "item_3"), ShouldNotEqual, NestedToolCallID("sh-2", "item_3"))
			So(NestedToolCallID("a:b", "c"), ShouldNotEqual, NestedToolCallID("a", "b:c"))
			So(NestedToolCallID("sh-1", "item_3"), ShouldNotEqual, "item_3")
		})

		Convey("Given 同一条子调用/结果被投递两次, When 经 handler 落进父 accumulator, Then 各只有一块", func() {
			acc := turn.New()
			ctx := context.Background()
			for range 2 {
				ev, _ := NestChildEvent("sh-1", agentruntime.ToolCall{ID: "toolu_1", Name: "Read", Input: input})
				So(handlers.ToolCallHandler{}.Apply(ctx, ev, acc, nil, nil), ShouldBeNil)
				ev, _ = NestChildEvent("sh-1", agentruntime.ToolResult{ToolCallID: "toolu_1", Content: "ok"})
				So(handlers.ToolResultHandler{}.Apply(ctx, ev, acc, nil, nil), ShouldBeNil)
			}
			final := acc.Finalize()
			So(final, ShouldHaveLength, 2)
			use, isUse := final[0].(*blocks.NestedToolUseBlock)
			So(isUse, ShouldBeTrue)
			So(use.ParentToolCallID, ShouldEqual, "sh-1")
			res, isRes := final[1].(*blocks.NestedToolResultBlock)
			So(isRes, ShouldBeTrue)
			So(res.ToolCallID, ShouldEqual, use.ID)
		})
	})
}
