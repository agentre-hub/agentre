package handlers

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
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
		su := &fakeSessionUpdater{}
		tc := &turn.TurnContext{Session: struct{}{}, SessionUpdater: su, Stream: "s"}

		err := ContextWindowUpdatedHandler{Writer: wr}.Apply(context.Background(),
			agentruntime.ContextWindowUpdated{Tokens: 200000},
			nil, emit, nil, tc)
		So(err, ShouldBeNil)
		So(wr.tokens, ShouldEqual, 200000)
		So(wr.calls, ShouldEqual, 1)

		// sess-2974:落库只能走 Writer 的单列更新。整行回写 (SessionUpdater) 会把带外轮
		// 手里那份起步时读出的旧快照连 agent_status / last_message_at 一起拍回库里 ——
		// 用户在带外轮进行中发的新一轮刚写好的 running 就此被抹成 idle。
		So(su.calls, ShouldEqual, 0)

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
			agentruntime.ContextWindowUpdated{Tokens: 0}, nil, emit, nil, nil)
		So(err, ShouldBeNil)
		So(emit.events, ShouldHaveLength, 0)
		So(wr.calls, ShouldEqual, 0)
	})
}
