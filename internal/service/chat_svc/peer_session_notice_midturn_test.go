package chat_svc

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// Given 一位对端已 attach,会话正在跑一轮(用户那一行与本轮 assistant 的轮内 checkpoint
// 都已发出);
// When  轮中切换了会话级设置(这里是思考力度),随后本轮收口、下一轮开轮;
// Then  切换 notice 的持久帧排在本轮收口帧(done)之后、下一轮用户消息之前 —— 与桌面端
// 一致:那边 notice 是排在在跑 assistant 之后的独立一行,显示在整轮之后。
//
// 轮中立即发布的话,notice 的号会落在本轮的轮内帧与收口帧之间;拿帧重建转录的那两个面
// 把它塞进在跑的那条回答里,收口帧再另起一条 —— 一轮回答被劈成两段。
func TestSessionSettingNotice_GivenMidTurnSwitch_ThenPublishedAfterTheTurnSettles(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	sess := chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}
	deps.session.EXPECT().Find(gomock.Any(), int64(41)).DoAndReturn(
		func(context.Context, int64) (*chat_entity.Session, error) {
			cp := sess
			return &cp, nil
		}).AnyTimes()
	deps.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(gomock.Any(), int64(11)).Return(&agent_backend_entity.AgentBackend{
		ID: 11, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
	}, nil).AnyTimes()
	deps.message.EXPECT().List(gomock.Any(), int64(41)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().CreateAtNextSeq(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs ...*chat_entity.Message) error {
			require.Len(t, msgs, 1)
			msgs[0].ID, msgs[0].Seq = 72, 3
			return nil
		}).Times(1)
	deps.session.EXPECT().UpdateReasoningEffort(gomock.Any(), int64(41), "high").Return(nil)

	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)

	// 本轮开轮:用户那一行取号(start_turn.emitTurnStarted 的发布点),随后一次轮内
	// checkpoint(dispatcher_runtime 的发布点):第一块已定稿,结尾那块还会继续长。
	_, _ = deps.svc.publishPeerMessageFrames(ctx, 41, &chat_entity.Message{
		ID: 70, SessionID: 41, Role: "user", Seq: 1,
		BlocksJSON: `[{"type":"text","data":{"text":"first"}}]`,
	}, true)
	assistant := &chat_entity.Message{
		ID: 71, SessionID: 41, Role: "assistant", Seq: 2,
		BlocksJSON: `[{"type":"thinking","data":{"text":"plan"}},{"type":"text","data":{"text":"part"}}]`,
	}
	_, _ = deps.svc.publishPeerMessageFrames(ctx, 41, assistant, false)

	_, err = deps.svc.SetChatSessionReasoningEffort(ctx, &SetChatSessionReasoningEffortRequest{SessionID: 41, ReasoningEffort: "high"})
	require.NoError(t, err)

	// 本轮收口(turn_run.finalize 的发布点),然后下一轮开轮。
	assistant.BlocksJSON = `[{"type":"thinking","data":{"text":"plan"}},{"type":"text","data":{"text":"part of the reply"}}]`
	deps.svc.publishPeerTurnDone(ctx, 41, assistant)
	_, _ = deps.svc.publishPeerMessageFrames(ctx, 41, &chat_entity.Message{
		ID: 73, SessionID: 41, Role: "user", Seq: 4,
		BlocksJSON: `[{"type":"text","data":{"text":"next"}}]`,
	}, true)

	want := []string{"user:first", "thinking", "text", "done", "notice", "done", "user:next"}
	var got []string
	require.Eventually(t, func() bool {
		got = durableFrameLabelsBySeq(subscriber)
		return len(got) >= len(want)
	}, time.Second, time.Millisecond, "对端应收到本轮、notice 与下一轮的全部持久帧")
	assert.Equal(t, want, got, "按号排序后,notice 必须在本轮收口之后、下一轮之前")
}

// durableFrameLabelsBySeq 把对端收到的持久帧按号排序,折成可读的标签。
func durableFrameLabelsBySeq(subscriber *peerRecordingSubscriber) []string {
	var frames []wire.EventFrame
	for _, record := range subscriber.notifications() {
		if frame, ok := record.params.(wire.EventFrame); ok && !frame.Preview && frame.Seq > 0 {
			frames = append(frames, frame)
		}
	}
	sort.SliceStable(frames, func(i, j int) bool { return frames[i].Seq < frames[j].Seq })
	labels := make([]string, 0, len(frames))
	for _, frame := range frames {
		switch event := frame.Event.(type) {
		case agentruntime.UserMessageEvent:
			labels = append(labels, "user:"+event.Text)
		case agentruntime.ThinkingDelta:
			labels = append(labels, "thinking")
		case agentruntime.TextDelta:
			labels = append(labels, "text")
		case agentruntime.Done:
			labels = append(labels, "done")
		case agentruntime.UnrecognizedBlock:
			labels = append(labels, event.BlockType)
		default:
			labels = append(labels, "other")
		}
	}
	return labels
}

// Given 轮中扣下了一条切换 notice;
// When  这一轮没走到收口就失败了(failTurn,不发收口帧);
// Then  notice 照样放行给对端 —— 扣着它的那一轮已经结束,不能把它压到下一轮。
func TestSessionSettingNotice_GivenMidTurnSwitch_WhenTurnFails_ThenNoticeIsReleased(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	sess := chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}
	deps.session.EXPECT().Find(gomock.Any(), int64(41)).DoAndReturn(
		func(context.Context, int64) (*chat_entity.Session, error) {
			cp := sess
			return &cp, nil
		}).AnyTimes()
	deps.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(gomock.Any(), int64(11)).Return(&agent_backend_entity.AgentBackend{
		ID: 11, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
	}, nil).AnyTimes()
	deps.message.EXPECT().List(gomock.Any(), int64(41)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().CreateAtNextSeq(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs ...*chat_entity.Message) error {
			msgs[0].ID, msgs[0].Seq = 72, 3
			return nil
		}).Times(1)
	deps.session.EXPECT().UpdateReasoningEffort(gomock.Any(), int64(41), "high").Return(nil)
	deps.message.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
	deps.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)
	_, _ = deps.svc.publishPeerMessageFrames(ctx, 41, &chat_entity.Message{
		ID: 70, SessionID: 41, Role: "user", Seq: 1,
		BlocksJSON: `[{"type":"text","data":{"text":"first"}}]`,
	}, true)
	_, err = deps.svc.SetChatSessionReasoningEffort(ctx, &SetChatSessionReasoningEffortRequest{SessionID: 41, ReasoningEffort: "high"})
	require.NoError(t, err)
	assert.NotContains(t, durableFrameLabelsBySeq(subscriber), "notice", "轮中的切换 notice 先扣着")

	running := sess
	deps.svc.failTurn(ctx, &running, &chat_entity.Message{ID: 71, SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: "[]"},
		"chat:41:1", assert.AnError)

	require.Eventually(t, func() bool {
		for _, label := range durableFrameLabelsBySeq(subscriber) {
			if label == "notice" {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond, "失败收尾也要放行扣下的 notice")
}
