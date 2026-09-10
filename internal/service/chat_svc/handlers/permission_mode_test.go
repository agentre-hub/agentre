package handlers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

type fakeSessionUpdater struct{ calls int }

func (f *fakeSessionUpdater) Update(_ context.Context, _ any) error { f.calls++; return nil }

type fakePermissionModeWriter struct {
	current  string
	setCalls int
	setMode  string
}

func (f *fakePermissionModeWriter) CurrentMode(_ any) string { return f.current }
func (f *fakePermissionModeWriter) SetMode(_ context.Context, _ any, m string) error {
	f.setCalls++
	f.setMode = m
	return nil
}

func TestPermissionModeChangedHandler(t *testing.T) {
	Convey("PermissionModeChanged 不同 mode 落 block + 调 Writer + emit session_status", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		wr := &fakePermissionModeWriter{current: "default"}
		tc := &turn.TurnContext{Session: struct{}{}, Stream: "s"}

		err := PermissionModeChangedHandler{Writer: wr}.Apply(context.Background(),
			agentruntime.PermissionModeChanged{Mode: "plan"},
			acc, emit, nil, tc)
		So(err, ShouldBeNil)
		So(wr.setCalls, ShouldEqual, 1)
		So(wr.setMode, ShouldEqual, "plan")

		got := acc.Finalize()[0].(*blocks.PermissionModeChangeBlock)
		So(got.To, ShouldEqual, "plan")
		p := emit.events[0].payload.(map[string]any)
		So(p["kind"], ShouldEqual, "session_status")
	})
}

func TestPermissionModeChangedHandler_SameModeIdempotent(t *testing.T) {
	Convey("Mode 相同时不写 / 不 emit (幂等)", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		wr := &fakePermissionModeWriter{current: "plan"}
		tc := &turn.TurnContext{Session: struct{}{}, Stream: "s"}

		err := PermissionModeChangedHandler{Writer: wr}.Apply(context.Background(),
			agentruntime.PermissionModeChanged{Mode: "plan"}, acc, emit, nil, tc)
		So(err, ShouldBeNil)
		So(wr.setCalls, ShouldEqual, 0)
		So(emit.events, ShouldHaveLength, 0)
		So(acc.Empty(), ShouldBeTrue)
	})
}

func TestPermissionModeChangedHandler_EmptyModeNoOp(t *testing.T) {
	Convey("Mode 空时 no-op", t, func() {
		acc := turn.New()
		err := PermissionModeChangedHandler{}.Apply(context.Background(),
			agentruntime.PermissionModeChanged{Mode: ""}, acc, nil, nil, nil)
		So(err, ShouldBeNil)
		So(acc.Empty(), ShouldBeTrue)
	})
}
