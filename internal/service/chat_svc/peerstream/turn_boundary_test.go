package peerstream

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

/*
轮次边界帧(开轮 / 终态)此前桌面端承载时一帧都不发。

对端判「这一轮结束了」只认 runtime.runResultDone:浏览器控制台据它放下停止键与三点,
server 的账号镜像据它把那一行落回 idle。agentred 一直发,桌面端只发了逐条的持久帧
(含 done 事件),于是派到桌面端跑的一轮在控制台上永远是「进行中」(2026-11-19 线上)。

边界帧走与持久帧**同一条**订阅者队列:终态帧必须排在本轮最后一批持久帧之后到达,
否则对端会先收场、再收到半截正文。
*/
func attachReadyPeer(t *testing.T, deps *peerSessionTestDeps, sessionID int64, sub PeerSessionSubscriber) {
	t.Helper()
	ctx := context.Background()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(sessionID)}, sub)
	require.NoError(t, err)
	_, err = deps.svc.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(sessionID), Cursor: 0, Limit: 100}, sub)
	require.NoError(t, err)
}

func expectPeerSession41(deps *peerSessionTestDeps) {
	ctx := context.Background()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, ConversationID: convID(41), AgentID: 7, AgentStatus: "running"}, nil).AnyTimes()
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil).AnyTimes()
}

func TestPublishRunResultDone_GivenAttachedPeer_WhenTurnEnds_ThenTerminalFrameFollowsDurableFrames(t *testing.T) {
	deps := setupPeerSessionTest(t)
	expectPeerSession41(deps)
	sub := newRecordingPeerSubscriber()
	attachReadyPeer(t, deps, 41, sub)

	publishPeerDurableFrame(t, deps.svc, 41, 100, "回答")
	deps.svc.PublishRunResultDone(41, wire.RunResultDoneFrame{Model: "claude-opus-5-5", DurationMs: 6743})

	var records []peerNotification
	require.Eventually(t, func() bool {
		records = sub.notifications()
		return len(records) >= 2
	}, 2*time.Second, time.Millisecond)

	last := records[len(records)-1]
	require.Equal(t, wire.NotifyRunResultDone, last.method, "终态帧必须是最后到的那一帧")
	frame, ok := last.params.(wire.RunResultDoneFrame)
	require.True(t, ok, "终态帧的参数类型是 %T", last.params)
	assert.Equal(t, convID(41), frame.ConversationID, "会话身份由 publication 盖上,调用方不必知道")
	assert.Equal(t, "claude-opus-5-5", frame.Model)
	assert.Equal(t, 6743, frame.DurationMs)
	for _, r := range records[:len(records)-1] {
		assert.Equal(t, wire.NotifyEvent, r.method)
	}
}

func TestPublishTurnStarted_GivenAttachedPeer_ThenPeerSeesTurnStartedForThisConversation(t *testing.T) {
	deps := setupPeerSessionTest(t)
	expectPeerSession41(deps)
	sub := newRecordingPeerSubscriber()
	attachReadyPeer(t, deps, 41, sub)

	deps.svc.PublishTurnStarted(41)

	require.Eventually(t, func() bool { return len(sub.notifications()) == 1 }, 2*time.Second, time.Millisecond)
	record := sub.notifications()[0]
	assert.Equal(t, wire.NotifyTurnStarted, record.method)
	assert.Equal(t, wire.TurnStartedFrame{ConversationID: convID(41)}, record.params)
}

func TestPublishAutonomousTurnBoundaries_GivenAttachedPeer_ThenPeerSeesAutonomousPair(t *testing.T) {
	deps := setupPeerSessionTest(t)
	expectPeerSession41(deps)
	sub := newRecordingPeerSubscriber()
	attachReadyPeer(t, deps, 41, sub)

	deps.svc.PublishAutonomousTurnStarted(41, "background_task")
	deps.svc.PublishAutonomousTurnDone(41, wire.RunResultDoneFrame{StopErrMsg: "boom"})

	require.Eventually(t, func() bool { return len(sub.notifications()) == 2 }, 2*time.Second, time.Millisecond)
	records := sub.notifications()
	assert.Equal(t, wire.NotifyAutonomousTurnStarted, records[0].method)
	assert.Equal(t, wire.AutonomousTurnStartedFrame{ConversationID: convID(41), Trigger: "background_task"}, records[0].params)
	assert.Equal(t, wire.NotifyAutonomousTurnDone, records[1].method)
	assert.Equal(t, wire.RunResultDoneFrame{ConversationID: convID(41), StopErrMsg: "boom"}, records[1].params)
}

/*
边界帧不能被「落后订阅者封顶」丢掉。

持久帧丢了可以补:它带 seq,对端看到跳号会按游标拉回来。边界帧不带号、也不进日志,
丢了就再也补不回来 —— 那一轮在对端永远停在「进行中」,正是本轮要修的症状。
它们每轮只有一两帧,不封顶也撑不爆内存。
*/
func TestPublishRunResultDone_GivenSubscriberQueueFull_ThenTerminalFrameIsStillQueued(t *testing.T) {
	deps := setupPeerSessionTest(t)
	expectPeerSession41(deps)
	stalled := &blockingPeerSubscriber{released: make(chan struct{}), notified: make(chan struct{}, 1)}
	attachReadyPeer(t, deps, 41, stalled)
	t.Cleanup(func() { close(stalled.released) })

	for i := range peerSubscriberQueueDepth * 2 {
		publishPeerDurableFrame(t, deps.svc, 41, int64(100+i), "flood")
	}
	deps.svc.PublishRunResultDone(41, wire.RunResultDoneFrame{})

	publication := deps.svc.peerPublication(41, convID(41))
	publication.mu.Lock()
	defer publication.mu.Unlock()
	var methods []string
	for _, sub := range publication.subscribers {
		for _, n := range sub.pending {
			methods = append(methods, n.method)
		}
	}
	require.NotEmpty(t, methods)
	assert.Equal(t, wire.NotifyRunResultDone, methods[len(methods)-1])
}

func TestPublishRunResultDone_GivenNoPeerEverAttached_ThenNothingHappens(t *testing.T) {
	deps := setupPeerSessionTest(t)
	assert.NotPanics(t, func() {
		deps.svc.PublishTurnStarted(99)
		deps.svc.PublishRunResultDone(99, wire.RunResultDoneFrame{})
	})
	_, exists := deps.svc.peerPublications.Load(int64(99))
	assert.False(t, exists, "没人订阅的会话不该为了发边界帧凭空建一份 publication")
}
