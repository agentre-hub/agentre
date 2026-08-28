package handlers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

type fakeCompactInspector struct {
	id  int64
	seq int
}

func (f fakeCompactInspector) MessageID(any) int64 { return f.id }
func (f fakeCompactInspector) MessageSeq(any) int  { return f.seq }

func TestCompactBoundaryHandler_AddsBlockAndEmits(t *testing.T) {
	Convey("CompactBoundary auto trigger 落 block + emit metadata", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		insp := fakeCompactInspector{id: 42, seq: 7}
		tc := &turn.TurnContext{AssistantMsg: struct{}{}, Stream: "chat:event:1:42"}

		err := CompactBoundaryHandler{Inspector: insp}.Apply(context.Background(),
			agentruntime.CompactBoundary{PreTokens: 12345, Trigger: "auto"},
			acc, emit, nil, tc)
		So(err, ShouldBeNil)

		got, ok := acc.Finalize()[0].(*blocks.CompactBoundaryBlock)
		So(ok, ShouldBeTrue)
		So(got.PreTokens, ShouldEqual, 12345)
		So(got.Trigger, ShouldEqual, "auto")
		So(got.At, ShouldBeGreaterThan, int64(0))

		So(emit.events, ShouldHaveLength, 1)
		So(emit.events[0].stream, ShouldEqual, "chat:event:1:42")
		p := emit.events[0].payload.(map[string]any)
		So(p["kind"], ShouldEqual, "compact_boundary")
		So(p["messageId"], ShouldEqual, int64(42))
		So(p["seq"], ShouldEqual, 7)
		So(p["preTokens"], ShouldEqual, 12345)
		So(p["trigger"], ShouldEqual, "auto")
		So(p["at"], ShouldBeGreaterThan, int64(0))
	})
}

func TestCompactBoundaryHandler_ManualZeroMetadata(t *testing.T) {
	Convey("CompactBoundary 零 metadata 时仍 emit + 落 block(字段零值)", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		tc := &turn.TurnContext{Stream: "s"}

		err := CompactBoundaryHandler{}.Apply(context.Background(),
			agentruntime.CompactBoundary{}, acc, emit, nil, tc)
		So(err, ShouldBeNil)

		got := acc.Finalize()[0].(*blocks.CompactBoundaryBlock)
		So(got.PreTokens, ShouldEqual, 0)
		So(got.Trigger, ShouldEqual, "")

		So(emit.events, ShouldHaveLength, 1)
		p := emit.events[0].payload.(map[string]any)
		So(p["preTokens"], ShouldEqual, 0)
		So(p["trigger"], ShouldEqual, "")
		// Inspector nil → messageId/seq 零值
		So(p["messageId"], ShouldEqual, int64(0))
		So(p["seq"], ShouldEqual, 0)
	})
}
