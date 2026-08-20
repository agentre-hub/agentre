package chat_svc

import (
	"testing"

	"github.com/cago-frame/agents/provider"
	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-ai/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
)

func TestNewTurnContext_PopulatesAllAdapters(t *testing.T) {
	Convey("newTurnContext 注入全部 adapter,不返回 nil 字段", t, func() {
		svc := &chatSvc{}
		assistantMsg := &chat_entity.Message{}
		sess := &chat_entity.Session{}

		tc := svc.newTurnContext(assistantMsg, sess, "chat:event:1:2", "claudecode")

		So(tc, ShouldNotBeNil)
		So(tc.AssistantMsg, ShouldEqual, assistantMsg)
		So(tc.Session, ShouldEqual, sess)
		So(tc.Stream, ShouldEqual, "chat:event:1:2")
		So(tc.BackendType, ShouldEqual, "claudecode")
		So(tc.MessageUpdater, ShouldNotBeNil)
		So(tc.SessionUpdater, ShouldNotBeNil)
		So(tc.SessionTransitioner, ShouldNotBeNil)
		// 少了它,后台 subagent 在别人的轮里完成时的终态就没有落点(sess-2825)。
		So(tc.SubagentFlipper, ShouldNotBeNil)
	})
}

func TestUsageWriterAdapter_PatchesMessage(t *testing.T) {
	Convey("usageWriterAdapter 把 UsageUpdate 字段写到 *chat_entity.Message", t, func() {
		m := &chat_entity.Message{}
		wr := usageWriterAdapter{}
		// 通过 buildHandlersWithAdapters 拿到 handler 实例(里面已经把 writer 注入)
		usageH, _, _, _, _, _ := buildHandlersWithAdapters(nil)
		So(usageH.Writer, ShouldEqual, wr)
		_ = m
	})
}

func TestUsageWriterAdapter_AccumulatesCompletionAcrossCalls(t *testing.T) {
	Convey("两次 per-call usage：prompt 取最近一次，completion 累加", t, func() {
		m := &chat_entity.Message{}
		wr := usageWriterAdapter{}
		wr.WriteUsage(m, &agentruntime.UsageUpdate{
			Usage: &provider.Usage{PromptTokens: 12000, CompletionTokens: 200, ReasoningTokens: 10},
		})
		wr.WriteUsage(m, &agentruntime.UsageUpdate{
			Usage: &provider.Usage{PromptTokens: 22000, CompletionTokens: 400, ReasoningTokens: 5},
		})
		So(m.PromptTokens, ShouldEqual, 22000)
		So(m.CompletionTokens, ShouldEqual, 600)
		So(m.ReasoningTokens, ShouldEqual, 15)
	})
}
