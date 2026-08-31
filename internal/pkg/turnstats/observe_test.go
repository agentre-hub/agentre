package turnstats

import (
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// ObserveAt 是「哪条事件动哪一下表」这条映射的唯一实现:chat_svc 的 turn
// dispatcher 与 agentred 的 fanout 都经它。这几条用例逐条钉住映射本身,
// 算术口径由 clock_test.go 负责。
func TestObserveAt_MapsEventsToClock(t *testing.T) {
	Convey("可见增量记首 token 并开表", t, func() {
		c := &Clock{}
		c.StartGenerationAt(time.UnixMilli(0))
		c.ObserveAt(agentruntime.TextDelta{Text: "hi"}, time.UnixMilli(1500))
		So(c.FirstTokenMs(), ShouldEqual, 1500)

		c2 := &Clock{}
		c2.StartGenerationAt(time.UnixMilli(0))
		c2.ObserveAt(agentruntime.ThinkingDelta{Text: "…"}, time.UnixMilli(700))
		So(c2.FirstTokenMs(), ShouldEqual, 700)
	})

	Convey("OutputActivity 只记表不动表", t, func() {
		c := &Clock{}
		c.StartGenerationAt(time.UnixMilli(0))
		c.ObserveAt(agentruntime.ToolCall{ID: "t1"}, time.UnixMilli(2000))
		// 表已经被 t1 按住,这条信号不能把它偷偷开起来
		c.ObserveAt(agentruntime.OutputActivity{}, time.UnixMilli(5000))
		c.ObserveAt(agentruntime.ToolResult{ToolCallID: "t1"}, time.UnixMilli(50000))
		c.PauseGenerationAt(time.UnixMilli(52000))
		So(c.TokensPerSec(400), ShouldEqual, 100) // 400 / (2s + 2s)
	})

	Convey("外层工具停表、结果回来开表", t, func() {
		c := &Clock{}
		c.StartGenerationAt(time.UnixMilli(0))
		c.ObserveAt(agentruntime.TextDelta{Text: "a"}, time.UnixMilli(1000))
		c.ObserveAt(agentruntime.ToolCall{ID: "t1"}, time.UnixMilli(3000))
		c.ObserveAt(agentruntime.ToolResult{ToolCallID: "t1"}, time.UnixMilli(10000))
		c.ObserveAt(agentruntime.TextDelta{Text: "b"}, time.UnixMilli(11000))
		c.PauseGenerationAt(time.UnixMilli(13000))
		So(c.TokensPerSec(300), ShouldEqual, 50) // 300 / (3s + 3s)
	})

	Convey("内层(subagent)工具不碰表 —— 派遣它的外层调用已经把表按住了", t, func() {
		c := &Clock{}
		c.StartGenerationAt(time.UnixMilli(0))
		c.ObserveAt(agentruntime.ToolCall{ID: "outer"}, time.UnixMilli(1000))
		// 子调用一来一回都不该动表:动了就等于在工具空档里把分母重新打开
		c.ObserveAt(agentruntime.ToolCall{ID: "inner", ParentToolCallID: "outer"}, time.UnixMilli(2000))
		c.ObserveAt(agentruntime.ToolResult{ToolCallID: "inner", ParentToolCallID: "outer"}, time.UnixMilli(3000))
		c.ObserveAt(agentruntime.ToolResult{ToolCallID: "outer"}, time.UnixMilli(5000))
		c.PauseGenerationAt(time.UnixMilli(6000))
		So(c.TokensPerSec(200), ShouldEqual, 100) // (0→1000) + (5000→6000)
	})

	Convey("工具调用本身兜底记首 token", t, func() {
		c := &Clock{}
		c.StartGenerationAt(time.UnixMilli(0))
		c.ObserveAt(agentruntime.ToolCall{ID: "t1"}, time.UnixMilli(4000))
		So(c.FirstTokenMs(), ShouldEqual, 4000)
	})

	Convey("与计时无关的事件一下都不动", t, func() {
		c := &Clock{}
		c.StartGenerationAt(time.UnixMilli(0))
		c.ObserveAt(agentruntime.Done{}, time.UnixMilli(9000))
		c.ObserveAt(agentruntime.UsageUpdate{}, time.UnixMilli(9000))
		So(c.FirstTokenMs(), ShouldEqual, 0)
	})

	Convey("nil 事件与 nil 表都不炸", t, func() {
		var c *Clock
		So(func() { c.ObserveAt(agentruntime.TextDelta{}, time.UnixMilli(1)) }, ShouldNotPanic)
		So(func() { (&Clock{}).ObserveAt(nil, time.UnixMilli(1)) }, ShouldNotPanic)
	})
}
