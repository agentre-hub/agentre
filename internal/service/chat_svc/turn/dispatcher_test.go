package turn

import (
	"context"
	"errors"
	"testing"

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
