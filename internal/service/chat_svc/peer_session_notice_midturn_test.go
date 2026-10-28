package chat_svc

import (
	"context"
	"sort"
	"sync/atomic"
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

	// 本轮开轮:用户那一行取号(start_turn.emitTurnStarted),随后一次轮内
	// checkpoint(dispatcher_runtime 的发布点):思考与工具调用已定稿、取号发出,结尾那段
	// 正文还会继续长,留给收口。
	assistant := &chat_entity.Message{
		ID: 71, SessionID: 41, Role: "assistant", Seq: 2,
		BlocksJSON: `[{"type":"thinking","data":{"text":"plan"}},` + toolUseBlockJSON + `,{"type":"text","data":{"text":"part"}}]`,
	}
	startUserTurn(ctx, deps.svc, sess, &chat_entity.Message{
		ID: 70, SessionID: 41, Role: "user", Seq: 1,
		BlocksJSON: `[{"type":"text","data":{"text":"first"}}]`,
	}, assistant)
	_, _ = deps.svc.publishPeerMessageFrames(ctx, 41, assistant, false)

	_, err = deps.svc.SetChatSessionReasoningEffort(ctx, &SetChatSessionReasoningEffortRequest{SessionID: 41, ReasoningEffort: "high"})
	require.NoError(t, err)

	// 本轮收口(turn_run.finalize 的发布点),然后下一轮开轮。
	assistant.BlocksJSON = `[{"type":"thinking","data":{"text":"plan"}},` + toolUseBlockJSON + `,{"type":"text","data":{"text":"part of the reply"}}]`
	deps.svc.publishPeerTurnDone(ctx, 41, StreamName(41, assistant.ID), assistant)
	_, _ = deps.svc.publishPeerMessageFrames(ctx, 41, &chat_entity.Message{
		ID: 73, SessionID: 41, Role: "user", Seq: 4,
		BlocksJSON: `[{"type":"text","data":{"text":"next"}}]`,
	}, true)

	want := []string{"user:first", "thinking", "tool", "text", "done", "notice", "done", "user:next"}
	var got []string
	require.Eventually(t, func() bool {
		got = durableFrameLabelsBySeq(subscriber)
		return len(got) >= len(want)
	}, time.Second, time.Millisecond, "对端应收到本轮、notice 与下一轮的全部持久帧")
	assert.Equal(t, want, got, "按号排序后,notice 必须在本轮收口之后、下一轮之前")
}

// startUserTurn 走用户发起那一轮的开轮点(start_turn.emitTurnStarted):用户那一行与
// 本轮 assistant 都已落库,用户那一行在这里取号。
func startUserTurn(ctx context.Context, svc *chatSvc, sess chat_entity.Session, userMsg, assistant *chat_entity.Message) {
	start := &turnStart{svc: svc, sess: &sess, userMsg: userMsg, assistantMsg: assistant}
	start.emitTurnStarted(ctx, StreamName(sess.ID, assistant.ID))
}

// toolUseBlockJSON 是一块已定稿的工具调用:轮内 checkpoint 会给它取号发出(结尾还在长
// 的正文 / 思考块不会)。
const toolUseBlockJSON = `{"type":"tool_use","data":{"id":"tu-1","name":"Bash","input":{}}}`

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
		case agentruntime.ToolCall:
			labels = append(labels, "tool")
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
	failing := &chat_entity.Message{ID: 71, SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: "[]"}
	startUserTurn(ctx, deps.svc, sess, &chat_entity.Message{
		ID: 70, SessionID: 41, Role: "user", Seq: 1,
		BlocksJSON: `[{"type":"text","data":{"text":"first"}}]`,
	}, failing)
	_, err = deps.svc.SetChatSessionReasoningEffort(ctx, &SetChatSessionReasoningEffortRequest{SessionID: 41, ReasoningEffort: "high"})
	require.NoError(t, err)
	// 切换之后、失败之前还有一次轮内 checkpoint:notice 扣着的话,它的号排在这之后。
	failing.BlocksJSON = `[{"type":"thinking","data":{"text":"plan"}},` + toolUseBlockJSON + `]`
	_, _ = deps.svc.publishPeerMessageFrames(ctx, 41, failing, false)

	running := sess
	deps.svc.failTurn(ctx, &running, failing, StreamName(41, failing.ID), assert.AnError)

	want := []string{"user:first", "thinking", "tool", "notice", "done"}
	var got []string
	require.Eventually(t, func() bool {
		got = durableFrameLabelsBySeq(subscriber)
		return len(got) >= len(want)
	}, time.Second, time.Millisecond, "失败收尾也要放行扣下的 notice")
	assert.Equal(t, want, got, "按号排序后,notice 扣到这一轮失败收尾之后")
}

// Given 一位对端已 attach,会话正在跑一轮**自主续轮**(后台任务完成后 CLI 自己续的那一轮):
// 它没有用户那一行,纯文字回答在收口前也不 checkpoint —— 收口之前没有任何持久帧发出;
// When  这一轮进行中切换了会话级设置,随后本轮收口;
// Then  切换 notice 同样排在这一轮的收口帧之后 —— 桌面端这一轮的 assistant 行开轮即
// 落库,notice 由 NextSeq 排在它之后,显示在整轮之后。
func TestSessionSettingNotice_GivenMidAutonomousTurnSwitch_ThenPublishedAfterTheTurnSettles(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	sess := chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}
	deps.session.EXPECT().Find(gomock.Any(), int64(41)).DoAndReturn(
		func(context.Context, int64) (*chat_entity.Session, error) {
			cp := sess
			return &cp, nil
		}).AnyTimes()
	deps.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	be := &agent_backend_entity.AgentBackend{
		ID: 11, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
	}
	deps.backend.EXPECT().Find(gomock.Any(), int64(11)).Return(be, nil).AnyTimes()
	deps.message.EXPECT().List(gomock.Any(), int64(41)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().CreateAtNextSeq(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs ...*chat_entity.Message) error {
			msgs[0].ID, msgs[0].Seq = 72, 3
			return nil
		}).Times(1)
	deps.session.EXPECT().UpdateReasoningEffort(gomock.Any(), int64(41), "high").Return(nil)

	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)

	// 自主续轮开轮:assistant 行已落库(persistTurnMessages),随后宣告开轮。
	assistant := &chat_entity.Message{ID: 71, SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: "[]"}
	running := sess
	turn := &autonomousTurnRun{
		svc: deps.svc, sessionID: 41, be: be, sess: &running, assistantMsg: assistant,
		at: agentruntime.AutonomousTurn{Trigger: "background_task"},
	}
	turn.emitStarted(ctx)

	_, err = deps.svc.SetChatSessionReasoningEffort(ctx, &SetChatSessionReasoningEffortRequest{SessionID: 41, ReasoningEffort: "high"})
	require.NoError(t, err)

	assistant.BlocksJSON = `[{"type":"text","data":{"text":"background job finished"}}]`
	deps.svc.publishPeerTurnDone(ctx, 41, StreamName(41, assistant.ID), assistant)

	want := []string{"text", "done", "notice", "done"}
	var got []string
	require.Eventually(t, func() bool {
		got = durableFrameLabelsBySeq(subscriber)
		return len(got) >= len(want)
	}, time.Second, time.Millisecond, "对端应收到这一轮与 notice 的全部持久帧")
	assert.Equal(t, want, got, "按号排序后,notice 必须在自主续轮收口之后")
}

// Given 一轮已经在 runTurn 里跑(这一轮的行早已落库),但还没有任何持久帧发出 ——
// 手动 compact、或者回答到此为止只有正文增量(收口前不 checkpoint);
// When  这一轮进行中切换了会话级设置,随后本轮收口;
// Then  切换 notice 排在这一轮的收口帧之后 —— 桌面端由 NextSeq 把它排在本轮 assistant
// 之后。扣留不能等「第一条持久帧」才开:那之前的切换会抢在整轮回答前面取号。
func TestSessionSettingNotice_GivenTurnWithoutDurableFramesYet_ThenPublishedAfterTheTurnSettles(t *testing.T) {
	rig := startApprovalTurnWith(t, approvalTurnOpts{backend: &agent_backend_entity.AgentBackend{
		ID: 11, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
	}})
	ctx := context.Background()
	rig.deps.message.EXPECT().CreateAtNextSeq(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs ...*chat_entity.Message) error {
			msgs[0].ID, msgs[0].Seq = 9002, 3
			return nil
		}).Times(1)
	rig.deps.session.EXPECT().UpdateReasoningEffort(gomock.Any(), approvalSessionID, "high").Return(nil)

	rig.feed(agentruntime.TextDelta{Text: "part"}, StreamChunk)
	_, err := rig.svc.SetChatSessionReasoningEffort(ctx, &SetChatSessionReasoningEffortRequest{SessionID: approvalSessionID, ReasoningEffort: "high"})
	require.NoError(t, err)

	rig.endTurn()
	want := []string{"text", "done", "notice", "done"}
	var got []string
	require.Eventually(t, func() bool {
		got = durableFrameLabelsBySeq(rig.subscriber)
		return len(got) >= len(want)
	}, time.Second, time.Millisecond, "对端应收到这一轮与 notice 的全部持久帧")
	assert.Equal(t, want, got, "按号排序后,notice 必须在这一轮收口之后")
}

// Given 一轮自主续轮正在跑,用户又发起了一轮(它排在自主续轮后面,用户那一行已发出);
// When  自主续轮先收口,随后在用户那一轮进行中切换了会话级设置,最后用户那一轮收口;
// Then  切换 notice 排在**用户那一轮**的收口帧之后 —— 一轮收口只放行它自己扣下的,
// 另一轮仍在跑时 notice 继续扣着。
func TestSessionSettingNotice_GivenOverlappingTurns_WhenOneSettles_ThenNoticeWaitsForTheOther(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	sess := chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}
	deps.session.EXPECT().Find(gomock.Any(), int64(41)).DoAndReturn(
		func(context.Context, int64) (*chat_entity.Session, error) {
			cp := sess
			return &cp, nil
		}).AnyTimes()
	deps.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	be := &agent_backend_entity.AgentBackend{
		ID: 11, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
	}
	deps.backend.EXPECT().Find(gomock.Any(), int64(11)).Return(be, nil).AnyTimes()
	deps.message.EXPECT().List(gomock.Any(), int64(41)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().CreateAtNextSeq(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs ...*chat_entity.Message) error {
			msgs[0].ID, msgs[0].Seq = 74, 5
			return nil
		}).Times(1)
	deps.session.EXPECT().UpdateReasoningEffort(gomock.Any(), int64(41), "high").Return(nil)

	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)

	// 自主续轮开轮。
	autoAssistant := &chat_entity.Message{ID: 71, SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: "[]"}
	autoSess := sess
	auto := &autonomousTurnRun{
		svc: deps.svc, sessionID: 41, be: be, sess: &autoSess, assistantMsg: autoAssistant,
		at: agentruntime.AutonomousTurn{Trigger: "background_task"},
	}
	auto.emitStarted(ctx)

	// 用户那一轮开轮,排在自主续轮后面。
	userAssistant := &chat_entity.Message{ID: 73, SessionID: 41, Role: "assistant", Seq: 4, BlocksJSON: "[]"}
	startUserTurn(ctx, deps.svc, sess, &chat_entity.Message{
		ID: 72, SessionID: 41, Role: "user", Seq: 3,
		BlocksJSON: `[{"type":"text","data":{"text":"next"}}]`,
	}, userAssistant)

	// 自主续轮收口,之后在用户那一轮进行中切换。
	autoAssistant.BlocksJSON = `[{"type":"text","data":{"text":"background job finished"}}]`
	deps.svc.publishPeerTurnDone(ctx, 41, StreamName(41, autoAssistant.ID), autoAssistant)
	_, err = deps.svc.SetChatSessionReasoningEffort(ctx, &SetChatSessionReasoningEffortRequest{SessionID: 41, ReasoningEffort: "high"})
	require.NoError(t, err)

	userAssistant.BlocksJSON = `[{"type":"text","data":{"text":"answer"}}]`
	deps.svc.publishPeerTurnDone(ctx, 41, StreamName(41, userAssistant.ID), userAssistant)

	want := []string{"user:next", "text", "done", "text", "done", "notice", "done"}
	var got []string
	require.Eventually(t, func() bool {
		got = durableFrameLabelsBySeq(subscriber)
		return len(got) >= len(want)
	}, time.Second, time.Millisecond, "对端应收到两轮与 notice 的全部持久帧")
	assert.Equal(t, want, got, "按号排序后,notice 必须在仍在跑的那一轮收口之后")
}

// Given 一轮在 runTurn 里中途 panic 了(生产上 dispatchTurn 用 gogo.WithIgnorePanic 吞掉,
// 应用照常运行),没走到任何收口点;
// When  之后会话空闲时切换了会话级设置;
// Then  notice 照常发给对端 —— 死掉的那一轮不能把这条会话之后的切换 notice 永远扣住。
func TestSessionSettingNotice_GivenTurnPanicked_ThenLaterNoticeIsNotHeldForever(t *testing.T) {
	var panicked atomic.Bool
	rig := startApprovalTurnWith(t, approvalTurnOpts{
		backend: &agent_backend_entity.AgentBackend{
			ID: 11, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
		},
		// runner 起手报回的 provider session id 要落库(attachRuntime):这一次写库 panic。
		result: &agentruntime.RunResult{ProviderSessionID: "provider-1"},
		onSessionUpdate: func() {
			if panicked.CompareAndSwap(false, true) {
				panic("boom")
			}
		},
		recoverPanic: true,
	})
	ctx := context.Background()
	rig.deps.message.EXPECT().CreateAtNextSeq(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, msgs ...*chat_entity.Message) error {
			msgs[0].ID, msgs[0].Seq = 9002, 3
			return nil
		}).Times(1)
	rig.deps.session.EXPECT().UpdateReasoningEffort(gomock.Any(), approvalSessionID, "high").Return(nil)

	select {
	case <-rig.done:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not stop")
	}
	require.True(t, panicked.Load(), "这一轮应当是 panic 退出的")

	_, err := rig.svc.SetChatSessionReasoningEffort(ctx, &SetChatSessionReasoningEffortRequest{SessionID: approvalSessionID, ReasoningEffort: "high"})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		for _, label := range durableFrameLabelsBySeq(rig.subscriber) {
			if label == "notice" {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond, "panic 掉的那一轮不该把之后的 notice 扣住")
}
