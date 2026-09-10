package handlers

import (
	"context"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// OutputActivity 是纯计时信号:不进 accumulator(它不带内容),但要 emit 一条同名
// 事件 —— 前端的「首 token」是它自己按 live stream 事件算的,后端记了表而前端收不到
// 信号,live 与落库两边就会各说各话。
//
// 记表那一半归 turn.Dispatcher(映射与口径在 internal/pkg/turnstats),不在本 handler。
func TestOutputActivityHandler(t *testing.T) {
	Convey("emit output_activity,不碰 accumulator", t, func() {
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
		So(emit.events, ShouldHaveLength, 1)
		So(emit.events[0].stream, ShouldEqual, "chat:event:1:2")
		So(emit.events[0].payload.(map[string]any)["kind"], ShouldEqual, "output_activity")
	})
}
