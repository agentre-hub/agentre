package turn

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

type fakeHandler struct {
	called int
	err    error
	saw    agentruntime.Event
}

func (f *fakeHandler) Apply(_ context.Context, ev agentruntime.Event, _ *Accumulator, _ Emitter, _ View, _ *TurnContext) error {
	f.called++
	f.saw = ev
	return f.err
}

func TestDispatcher_RoutesByEventType(t *testing.T) {
	Convey("dispatcher 按 Event 类型路由到对应 handler", t, func() {
		d := NewDispatcher()
		textH := &fakeHandler{}
		toolH := &fakeHandler{}
		d.Register((*agentruntime.TextDelta)(nil), textH)
		d.Register((*agentruntime.ToolCall)(nil), toolH)

		err := d.Apply(context.Background(), agentruntime.TextDelta{Text: "hi"}, New(), nil, nil, nil)
		So(err, ShouldBeNil)
		So(textH.called, ShouldEqual, 1)
		So(toolH.called, ShouldEqual, 0)
	})
}

func TestDispatcher_UnknownEventNoOp(t *testing.T) {
	Convey("未注册 Event 类型默默丢弃(forward-compat)", t, func() {
		d := NewDispatcher()
		err := d.Apply(context.Background(), agentruntime.TextDelta{}, New(), nil, nil, nil)
		So(err, ShouldBeNil)
	})
}

func TestDispatcher_PropagatesHandlerError(t *testing.T) {
	Convey("handler 返 error 时 dispatcher 透传", t, func() {
		d := NewDispatcher()
		boom := errors.New("boom")
		h := &fakeHandler{err: boom}
		d.Register((*agentruntime.Done)(nil), h)

		err := d.Apply(context.Background(), agentruntime.Done{}, New(), nil, nil, nil)
		So(err, ShouldEqual, boom)
	})
}

func TestDispatcher_NilEventNoOp(t *testing.T) {
	Convey("ev=nil 直接返 nil", t, func() {
		d := NewDispatcher()
		err := d.Apply(context.Background(), nil, New(), nil, nil, nil)
		So(err, ShouldBeNil)
	})
}

// 计时归 Dispatcher,不归各个 handler。
//
// 从前 NoteVisibleToken / Suspend / Resume 散落在 TextDelta / ThinkingDelta /
// OutputActivity / ToolCall / ToolResult 五个 handler 里,而 agentred 的 fanout
// 要在**没有 chat_svc**的前提下算出同一份数(浏览器的转录只有事件流可读)。两处
// 各写一份映射必然漂,于是映射统一收进 turnstats.ObserveAt,由这里逐帧调一次。
//
// 门槛因此是「事件经过 dispatcher」,而不是「事件有 handler」:未注册的事件照样要
// 动表 —— 否则某个 backend 的一跳里恰好全是未注册事件,那段耗时就凭空消失。
func TestDispatcher_DrivesTurnClock(t *testing.T) {
	Convey("经过 dispatcher 的事件驱动本轮计时", t, func() {
		d := NewDispatcher()
		tc := &TurnContext{}
		tc.StartGenerationAt(time.UnixMilli(0))

		Convey("已注册的事件", func() {
			d.Register((*agentruntime.TextDelta)(nil), &fakeHandler{})
			So(d.Apply(context.Background(), agentruntime.TextDelta{Text: "hi"}, nil, nil, nil, tc), ShouldBeNil)
			So(tc.FirstTokenAt.IsZero(), ShouldBeFalse)
		})

		Convey("未注册的事件同样动表", func() {
			So(d.Apply(context.Background(), agentruntime.ToolCall{ID: "t1"}, nil, nil, nil, tc), ShouldBeNil)
			So(tc.PendingTools, ShouldContainKey, "t1")
		})

		Convey("turnCtx 为 nil 不炸", func() {
			So(func() {
				_ = d.Apply(context.Background(), agentruntime.TextDelta{}, nil, nil, nil, nil)
			}, ShouldNotPanic)
		})
	})
}
