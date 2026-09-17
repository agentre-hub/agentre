package handlers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
)

type fakeCWWriter struct {
	tokens int
	calls  int
}

func (f *fakeCWWriter) WriteContextWindow(_ context.Context, _ any, t int) error {
	f.tokens = t
	f.calls++
	return nil
}

func TestContextWindowUpdatedHandler(t *testing.T) {
	Convey("ContextWindowUpdated 写 session.ContextWindow + emit patch", t, func() {
		emit := &fakeEmit{}
		wr := &fakeCWWriter{}
		tc := &turn.TurnContext{Session: struct{}{}, Stream: "s"}

		err := ContextWindowUpdatedHandler{Writer: wr}.Apply(context.Background(),
			agentruntime.ContextWindowUpdated{Tokens: 200000},
			nil, emit, tc)
		So(err, ShouldBeNil)
		So(wr.tokens, ShouldEqual, 200000)
		So(wr.calls, ShouldEqual, 1)

		p := emit.events[0].payload.(map[string]any)
		ss := p["sessionStatus"].(map[string]any)
		So(ss["contextWindow"], ShouldEqual, 200000)
	})
}

func TestContextWindowUpdatedHandler_ZeroTokensNoOp(t *testing.T) {
	Convey("Tokens=0 → no-op", t, func() {
		emit := &fakeEmit{}
		wr := &fakeCWWriter{}
		err := ContextWindowUpdatedHandler{Writer: wr}.Apply(context.Background(),
			agentruntime.ContextWindowUpdated{Tokens: 0}, nil, emit, nil)
		So(err, ShouldBeNil)
		So(emit.events, ShouldHaveLength, 0)
		So(wr.calls, ShouldEqual, 0)
	})
}
