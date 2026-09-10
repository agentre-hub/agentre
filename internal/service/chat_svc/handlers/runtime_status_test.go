package handlers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// TestRuntimeStatusHandler_CompactingEmits compacting → emit
// {kind:"runtime_status", status:"compacting", compacting:true}。
// 不落 block (runtime status 是过渡态,不入 history)。
func TestRuntimeStatusHandler_CompactingEmits(t *testing.T) {
	Convey("Status=compacting → emit runtime_status payload with compacting=true", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		tc := &turn.TurnContext{Stream: "chat:event:1:42"}

		err := RuntimeStatusHandler{}.Apply(context.Background(),
			agentruntime.RuntimeStatus{Status: "compacting"},
			acc, emit, nil, tc)
		So(err, ShouldBeNil)
		So(acc.Empty(), ShouldBeTrue) // runtime status 不应落 block

		So(emit.events, ShouldHaveLength, 1)
		So(emit.events[0].stream, ShouldEqual, "chat:event:1:42")
		p := emit.events[0].payload.(map[string]any)
		So(p["kind"], ShouldEqual, "runtime_status")
		So(p["status"], ShouldEqual, "compacting")
		So(p["compacting"], ShouldEqual, true)
	})
}

// TestRuntimeStatusHandler_OtherStatusOnlyPassthroughs 未来其它 status 值
// (e.g. "requesting") compacting=false,但仍透传 status 给前端做扩展。
func TestRuntimeStatusHandler_OtherStatus(t *testing.T) {
	Convey("Status=requesting → compacting=false 但仍 emit", t, func() {
		emit := &fakeEmit{}
		tc := &turn.TurnContext{Stream: "s"}

		err := RuntimeStatusHandler{}.Apply(context.Background(),
			agentruntime.RuntimeStatus{Status: "requesting"},
			turn.New(), emit, nil, tc)
		So(err, ShouldBeNil)
		So(emit.events, ShouldHaveLength, 1)
		p := emit.events[0].payload.(map[string]any)
		So(p["status"], ShouldEqual, "requesting")
		So(p["compacting"], ShouldEqual, false)
	})
}

// TestRuntimeStatusHandler_EmptyStatusNoOp 空 Status 不 emit
// (translator 已经过滤过,这里作 defense in depth)。
func TestRuntimeStatusHandler_EmptyStatusNoOp(t *testing.T) {
	Convey("Status 空 → no-op", t, func() {
		emit := &fakeEmit{}
		err := RuntimeStatusHandler{}.Apply(context.Background(),
			agentruntime.RuntimeStatus{}, turn.New(), emit, nil, nil)
		So(err, ShouldBeNil)
		So(emit.events, ShouldBeEmpty)
	})
}
