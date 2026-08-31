package handlers

import (
	"context"
	"testing"

	"github.com/cago-frame/agents/provider"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

type fakeUsageWriter struct {
	written *agentruntime.UsageUpdate
	msgID   int64
}

func (f *fakeUsageWriter) WriteUsage(_ context.Context, _ any, u *agentruntime.UsageUpdate) error {
	f.written = u
	return nil
}
func (f *fakeUsageWriter) MessageID(_ any) int64 { return f.msgID }

func TestUsageUpdateHandler(t *testing.T) {
	Convey("UsageUpdate 调 Writer + Updater + emit usage", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		wr := &fakeUsageWriter{}
		tc := &turn.TurnContext{AssistantMsg: struct{}{}, Stream: "s"}

		err := UsageUpdateHandler{Writer: wr}.Apply(context.Background(),
			agentruntime.UsageUpdate{
				Usage:            &provider.Usage{PromptTokens: 100, CachedTokens: 30},
				TotalInputTokens: 130,
				ContextWindow:    258000,
			},
			acc, emit, nil, tc)
		So(err, ShouldBeNil)
		So(wr.written, ShouldNotBeNil)
		So(wr.written.TotalInputTokens, ShouldEqual, 130)
		// usage 落库归 Writer 单列写,handler 不得再整行回写 assistantMsg —— 整行会带上
		// MB 级的 blocks_json,而这一帧只存 6 个整数。TurnContext 上那个通用的
		// 「整行 Save」端口已经删掉,这条约束现在由类型系统兜着。

		p := emit.events[0].payload.(map[string]any)
		So(p["kind"], ShouldEqual, "usage")
		usage := p["usage"].(map[string]any)
		So(usage["promptTokens"], ShouldEqual, 100)
		So(usage["totalInputTokens"], ShouldEqual, 130)
		So(usage["contextWindow"], ShouldEqual, 258000)
	})
}

func TestUsageUpdateHandler_NilUsageNoOp(t *testing.T) {
	Convey("Usage=nil → no-op", t, func() {
		acc := turn.New()
		emit := &fakeEmit{}
		err := UsageUpdateHandler{}.Apply(context.Background(),
			agentruntime.UsageUpdate{Usage: nil}, acc, emit, nil, nil)
		So(err, ShouldBeNil)
		So(emit.events, ShouldHaveLength, 0)
	})
}
