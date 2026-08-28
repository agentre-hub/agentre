package handlers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

type fakeEmit struct{ events []emittedEvent }
type emittedEvent struct {
	stream  string
	payload any
}

func (f *fakeEmit) Emit(_ context.Context, name string, ev any) {
	f.events = append(f.events, emittedEvent{stream: name, payload: ev})
}

func TestTextDeltaHandler(t *testing.T) {
	Convey("TextDeltaHandler 累 textBuf + emit chunk", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		err := TextDeltaHandler{}.Apply(
			context.Background(), agentruntime.TextDelta{Text: "hi"},
			acc, emit, nil, &turn.TurnContext{Stream: "chat:event:1:2"},
		)
		So(err, ShouldBeNil)
		So(emit.events, ShouldHaveLength, 1)
		So(emit.events[0].stream, ShouldEqual, "chat:event:1:2")
		payload := emit.events[0].payload.(map[string]any)
		So(payload["kind"], ShouldEqual, "chunk")
		So(payload["delta"], ShouldEqual, "hi")
		So(acc.Empty(), ShouldBeFalse)
	})
}

func TestThinkingDeltaHandler(t *testing.T) {
	Convey("ThinkingDeltaHandler 累 thinkingBuf + emit thinking", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		err := ThinkingDeltaHandler{}.Apply(
			context.Background(), agentruntime.ThinkingDelta{Text: "thought"},
			acc, emit, nil, nil,
		)
		So(err, ShouldBeNil)
		So(emit.events, ShouldHaveLength, 1)
		payload := emit.events[0].payload.(map[string]any)
		So(payload["kind"], ShouldEqual, "thinking")
	})
}
