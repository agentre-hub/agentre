package handlers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func TestDoneHandler(t *testing.T) {
	Convey("DoneHandler emit message_end", t, func() {
		emit := &fakeEmit{}
		err := DoneHandler{}.Apply(context.Background(),
			agentruntime.Done{}, nil, emit, nil, nil)
		So(err, ShouldBeNil)
		p := emit.events[0].payload.(map[string]any)
		So(p["kind"], ShouldEqual, "message_end")
	})
}
