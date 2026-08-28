package handlers

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

type fakeErrorWriter struct{ text string }

func (f *fakeErrorWriter) WriteErrorText(_ context.Context, _ any, s string) error {
	f.text = s
	return nil
}

func TestErrorHandler(t *testing.T) {
	Convey("ErrorHandler patch ErrorText + emit error", t, func() {
		emit := &fakeEmit{}
		wr := &fakeErrorWriter{}
		mu := &fakeMsgUpdater{}
		tc := &turn.TurnContext{AssistantMsg: struct{}{}, MessageUpdater: mu, Stream: "s"}

		err := ErrorHandler{Writer: wr}.Apply(context.Background(),
			agentruntime.ErrorEvent{Err: errors.New("boom")},
			nil, emit, nil, tc)
		So(err, ShouldBeNil)
		So(wr.text, ShouldEqual, "boom")
		// 同 usage:error_text 走单列写,不整行回写。
		So(mu.calls, ShouldEqual, 0)

		p := emit.events[0].payload.(map[string]any)
		So(p["kind"], ShouldEqual, "error")
		So(p["error"], ShouldEqual, "boom")
	})
}
