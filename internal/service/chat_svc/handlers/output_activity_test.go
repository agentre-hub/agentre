package handlers

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// OutputActivity 是纯计时信号:记首 token,不进 accumulator(它不带内容),但要 emit
// 一条同名事件 —— 前端的「首 token」是它自己按 live stream 事件算的,后端记了表而
// 前端收不到信号,live 与落库两边就会各说各话。
func TestOutputActivityHandler(t *testing.T) {
	Convey("记首 token + emit output_activity,不碰 accumulator", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		tc := &turn.TurnContext{Stream: "chat:event:1:2"}
		tc.StartGenerationAt(time.UnixMilli(0))

		err := OutputActivityHandler{}.Apply(
			context.Background(), agentruntime.OutputActivity{},
			acc, emit, nil, tc,
		)

		So(err, ShouldBeNil)
		So(acc.Empty(), ShouldBeTrue)
		So(tc.FirstTokenAt.IsZero(), ShouldBeFalse)
		So(emit.events, ShouldHaveLength, 1)
		So(emit.events[0].stream, ShouldEqual, "chat:event:1:2")
		So(emit.events[0].payload.(map[string]any)["kind"], ShouldEqual, "output_activity")
	})
}

// 现场 sess-3241:一轮 190.1s、23 跳纯工具调用,首 token 报了 166.6s —— 因为只有
// 可见正文记表。claudecode 走 OutputActivity 拿到更早更准的时刻,codex / piagent
// 没有等价帧,由工具调用本身兜底:merged 帧到达时模型显然早就在产出 token 了。
func TestToolCallHandler_RecordsFirstTokenWhenNoVisibleText(t *testing.T) {
	Convey("整跳没有正文时,工具调用本身兜底记首 token", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		tc := &turn.TurnContext{Stream: "chat:event:1:2"}
		tc.StartGenerationAt(time.UnixMilli(0))

		err := ToolCallHandler{}.Apply(
			context.Background(),
			agentruntime.ToolCall{ID: "tu1", Name: "Bash"},
			acc, emit, nil, tc,
		)

		So(err, ShouldBeNil)
		So(tc.FirstTokenAt.IsZero(), ShouldBeFalse)
		// 兜底记表不得动分母:工具空档照旧被停表挂账。
		So(tc.PendingTools, ShouldContainKey, "tu1")
		So(tc.BurstStartedAt.IsZero(), ShouldBeTrue)
	})

	Convey("子代理内层工具不记主轮的表", t, func() {
		acc := turn.New()
		tc := &turn.TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))

		err := ToolCallHandler{}.Apply(
			context.Background(),
			agentruntime.ToolCall{ID: "tu2", Name: "Bash", ParentToolCallID: "tu-outer"},
			acc, &fakeEmit{}, nil, tc,
		)

		So(err, ShouldBeNil)
		So(tc.FirstTokenAt.IsZero(), ShouldBeTrue)
	})
}
