package chat_svc

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// Given 一段里夹着本仓认不出的转录块的持久化记录,When 合成对端快照,Then 认不出
// 的那个**如实送到对端**(R8),而不是被丢掉或伪造成一条送不出去的帧。
//
// 这条边界踩过两次坑,都记在这里:
//
//   - 一开始它伪造一条 kind 为 "unrecognized_block" 的事件。那个判别值不在密封
//     词表里,接收侧 UnmarshalEvent 报 unknown kind,而 flushPeerSubscribers 把
//     Notify 的错误当成「这个订阅者不行了」直接摘掉 —— 一个认不出的块会让整条
//     实时流无声中断。
//   - 于是先改成跳过。流是保住了,但 R8 丢了:对端连「这里有一块我读不懂的东西」
//     都看不到。
//
// 现在它是真的密封事件类型:带自己的 EventKind、proto 字段与两端生成产物,既送
// 得出去,又如实。
func TestSynthesizePeerHistory_GivenPersistedBlocks_ThenForwardsUnrecognizedBlockVerbatim(t *testing.T) {
	messages := []*chat_entity.Message{
		{SessionID: 41, Role: "user", Seq: 1, BlocksJSON: `[{"type":"text","data":{"text":"ship it"}}]`},
		{SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: `[{"type":"thinking","data":{"text":"checking"}},{"type":"tool_use","data":{"id":"tool-1","name":"Read","input":{"path":"README.md"}}},{"type":"tool_result","data":{"tool_use_id":"tool-1","content":[{"type":"text","data":{"text":"ok"}}]}},{"type":"future_block","data":{"nested":{"keep":true}}}]`, ErrorText: "provider stopped"},
	}

	events, err := synthesizePeerHistory(41, messages)
	require.NoError(t, err)

	kinds := make([]agentruntime.EventKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, peerEventKind(t, event.Event))
	}
	assert.Equal(t, []agentruntime.EventKind{
		agentruntime.EventUserMessage,
		agentruntime.EventThinkingDelta,
		agentruntime.EventToolUseStart,
		agentruntime.EventToolResult,
		agentruntime.EventUnrecognizedBlock,
		agentruntime.EventError,
		agentruntime.EventDone,
	}, kinds)
	// 原样:块类型与载荷字节一个都不改,对端才有可能认出本仓认不出的东西。
	assert.Equal(t, agentruntime.UnrecognizedBlock{
		BlockType: "future_block",
		Data:      json.RawMessage(`{"nested":{"keep":true}}`),
	}, events[4].Event)

	// 每一帧都必须真能过协议边界 —— 从前那条伪造事件正是卡在这里,而当时没有
	// 任何用例走到这一步。
	for i, frame := range events {
		_, err := protowire.WireNotificationToProto(wire.NotifyEvent, frame)
		require.NoErrorf(t, err, "第 %d 帧送不出去,整条实时流会被摘掉", i)
	}
}

// Given an attached peer and a frozen history, when live canonical events
// arrive before the peer has pulled the snapshot high-water mark, then pull
// emits deterministic 1..H history first and releases the buffered live frame
// only after the cursor reaches H.
// Given persisted final control-card state, when the desktop synthesizes a
// history, then it reconstructs both the card creation and its final update so
// the existing reducer can reach the stored readable state.
func TestSynthesizePeerHistory_GivenFinalControlAndSnapshotBlocks_ThenEmitsReducerCompleteEvents(t *testing.T) {
	messages := []*chat_entity.Message{{
		SessionID: 41, Role: "assistant", Seq: 1, PromptTokens: 10, TotalInputTokens: 10,
		BlocksJSON: `[` +
			`{"type":"user_ask","data":{"request_id":"ask-1","tool_call_id":"tool-1","questions":[{"question":"continue?","options":[]}],"answered":true,"answers":[{"questionIndex":0,"labels":["yes"]}]}},` +
			`{"type":"tool_permission","data":{"request_id":"permission-1","tool_call_id":"tool-2","tool_name":"Bash","tool_input":{"command":"pwd"},"resolved":true,"allowed":true}},` +
			`{"type":"permission_mode_change","data":{"to":"plan"}},` +
			`{"type":"subagent_state","data":{"parent_tool_call_id":"agent-1","status":"completed","total_tokens":7,"model":"claude"}},` +
			`{"type":"plan","data":{"steps":[{"step":"inspect","status":"completed"}],"text":"# Plan"}}` +
			`]`,
	}}

	events, err := synthesizePeerHistory(41, messages)
	require.NoError(t, err)
	kinds := make([]agentruntime.EventKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, peerEventKind(t, event.Event))
	}
	assert.Equal(t, []agentruntime.EventKind{
		agentruntime.EventAskUserQuestion,
		agentruntime.EventAskUserQuestionAnswered,
		agentruntime.EventToolPermissionRequest,
		agentruntime.EventToolPermissionResolved,
		agentruntime.EventPermissionModeChanged,
		agentruntime.EventSubagentDone,
		agentruntime.EventSubagentModel,
		agentruntime.EventPlanUpdated,
		agentruntime.EventUsage,
		agentruntime.EventDone,
	}, kinds)
}

// Given an attached peer and a frozen history, when live canonical events
// arrive before the peer has pulled the snapshot high-water mark, then pull
// emits deterministic 1..H history first and releases the buffered live frame
// only after the cursor reaches H.
func TestPeerSessionPull_GivenSnapshotAndEarlyLiveEvent_ThenKeepsOneOrderedSeqUniverse(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	messages := []*chat_entity.Message{
		{SessionID: 41, Role: "user", Seq: 1, BlocksJSON: `[{"type":"text","data":{"text":"hello"}}]`},
		{SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: `[{"type":"text","data":{"text":"world"}}]`},
	}
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(messages, nil)

	subscriber := newRecordingPeerSubscriber()
	attached, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{SessionID: 41}, subscriber)
	require.NoError(t, err)
	assert.Equal(t, int64(3), attached.LatestSeq, "history must be frozen as user_message, text, done")

	deps.svc.publishPeerEvent(41, agentruntime.TextDelta{Text: "live"})
	assert.Empty(t, subscriber.notifications(), "live output must wait behind the frozen snapshot")

	first, err := deps.svc.PullPeerSession(ctx, wire.SessionPullParams{SessionID: 41, Cursor: 0, Limit: 2}, subscriber)
	require.NoError(t, err)
	assert.Equal(t, int64(1), first.OldestSeq)
	assert.Equal(t, int64(2), first.Cursor)
	assert.True(t, first.HasMore)
	assertPeerNotificationSeqs(t, first.Notifications, 1, 2)
	assert.Empty(t, subscriber.notifications(), "cursor below H must retain live buffering")

	second, err := deps.svc.PullPeerSession(ctx, wire.SessionPullParams{SessionID: 41, Cursor: first.Cursor, Limit: 2}, subscriber)
	require.NoError(t, err)
	assert.Equal(t, int64(3), second.Cursor)
	assert.False(t, second.HasMore)
	assertPeerNotificationSeqs(t, second.Notifications, 3)
	require.Eventually(t, func() bool {
		return len(subscriber.notifications()) == 1
	}, time.Second, time.Millisecond, "live delivery moves to the out-of-lock flush worker once the pull catches up")
	live := subscriber.notifications()
	require.Len(t, live, 1)
	assert.Equal(t, wire.NotifyEvent, live[0].method)
	assert.Equal(t, int64(4), eventFrameSeq(t, live[0].params))
}

// Given a reconnecting peer while this desktop is still serving a session,
// when canonical live output was published after the initial snapshot, then a
// fresh attach sees the same monotonic sequence universe instead of a gap.
func TestAttachPeerSession_GivenReconnectAfterLiveEvent_ThenRetainsLiveSeqForDedup(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	messages := []*chat_entity.Message{{SessionID: 41, Role: "assistant", Seq: 1, BlocksJSON: `[{"type":"text","data":{"text":"history"}}]`}}
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil).Times(2)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil).Times(2)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil).Times(2)
	deps.message.EXPECT().List(ctx, int64(41)).Return(messages, nil)

	first := newRecordingPeerSubscriber()
	attached, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{SessionID: 41}, first)
	require.NoError(t, err)
	assert.Equal(t, int64(2), attached.LatestSeq)
	deps.svc.publishPeerEvent(41, agentruntime.TextDelta{Text: "live"})

	second := newRecordingPeerSubscriber()
	reconnected, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{SessionID: 41}, second)
	require.NoError(t, err)
	assert.Equal(t, int64(3), reconnected.LatestSeq)
	page, err := deps.svc.PullPeerSession(ctx, wire.SessionPullParams{SessionID: 41, Limit: 3}, second)
	require.NoError(t, err)
	assertPeerNotificationSeqs(t, page.Notifications, 1, 2, 3)
}

// Given a desktop session with no stored messages, when an attached peer pulls
// its transcript, then the non-reclaiming desktop contract reports OldestSeq=0.
func TestPeerSessionPull_GivenEmptyHistory_ThenReportsZeroOldestSeq(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil)

	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{SessionID: 41}, subscriber)
	require.NoError(t, err)
	page, err := deps.svc.PullPeerSession(ctx, wire.SessionPullParams{SessionID: 41}, subscriber)
	require.NoError(t, err)
	assert.Equal(t, int64(0), page.OldestSeq)
	assert.Empty(t, page.Notifications)
}

// Given an attached peer whose relay write is stalled (Notify blocks), when a
// live canonical event is published from the local turn loop, then the publish
// returns immediately instead of head-of-line blocking the local session, and
// the frame is still delivered once the peer's connection drains.
func TestPublishPeerEvent_GivenStalledSubscriber_ThenLocalPublishDoesNotBlockAndFrameStillDelivers(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil)

	released := make(chan struct{})
	notified := make(chan struct{}, 2)
	subscriber := &blockingPeerSubscriber{released: released, notified: notified}
	attached, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{SessionID: 41}, subscriber)
	require.NoError(t, err)
	assert.Equal(t, int64(0), attached.LatestSeq)
	// 把游标抬到高水位，让该订阅进入「已拉平、收实时帧」的 ready 状态。
	_, err = deps.svc.PullPeerSession(ctx, wire.SessionPullParams{SessionID: 41}, subscriber)
	require.NoError(t, err)

	published := make(chan struct{})
	go func() {
		deps.svc.publishPeerEvent(41, agentruntime.TextDelta{Text: "live"})
		close(published)
	}()
	select {
	case <-published:
		// 发布方没有因为对端卡住而阻塞（旧实现会在 Notify 上卡死本地 turn）。
	case <-time.After(time.Second):
		t.Fatal("publishPeerEvent blocked on a stalled peer subscriber; a local turn would stall")
	}

	// 对端恢复后，缓冲帧仍被送达（投递语义不变）。
	close(released)
	select {
	case <-notified:
	case <-time.After(2 * time.Second):
		t.Fatal("queued live frame never reached the recovered subscriber")
	}
}

// Given a peer whose pull reaches the high-water mark while a live frame is
// buffered and that frame's relay write stalls, when the local turn publishes
// another live event during the stall, then publication must not block behind
// the stalled peer: the pull path must not hold the publication lock across
// Notify. All live delivery belongs to the out-of-lock flush worker, so a slow
// peer stalls only its own fan-out, never the desktop's canonical event loop.
func TestPullPeerSession_GivenBufferedLiveFrameAndStalledNotify_ThenPublicationLockNotHeldByPullDrain(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	messages := []*chat_entity.Message{
		{SessionID: 41, Role: "user", Seq: 1, BlocksJSON: `[{"type":"text","data":{"text":"hello"}}]`},
		{SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: `[{"type":"text","data":{"text":"world"}}]`},
	}
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(messages, nil)

	released := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(released) }) }
	defer release()
	subscriber := &stallingSeqSubscriber{blockSeq: 4, entered: make(chan struct{}), released: released}
	attached, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{SessionID: 41}, subscriber)
	require.NoError(t, err)
	assert.Equal(t, int64(3), attached.LatestSeq)

	// 拉平前先到一条实时帧（seq 4）：cursor < H，worker 不会投递，帧停在 pending。
	deps.svc.publishPeerEvent(41, agentruntime.TextDelta{Text: "live-before-pull"})
	assert.Empty(t, subscriber.seenSeqs(), "cursor below H must retain live buffering")

	// pull 拉满历史并进入 catch-up：缓冲的实时帧在此刻交付。
	pullDone := make(chan struct{})
	go func() {
		_, _ = deps.svc.PullPeerSession(ctx, wire.SessionPullParams{SessionID: 41, Cursor: 0, Limit: 5}, subscriber)
		close(pullDone)
	}()
	select {
	case <-subscriber.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("catch-up never attempted the buffered live frame")
	}

	// 交付卡在对端的网络写（锁内、RED）时，本地 turn 再发布一条事件。
	published := make(chan struct{})
	go func() {
		deps.svc.publishPeerEvent(41, agentruntime.TextDelta{Text: "live-during-stall"})
		close(published)
	}()
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("publish blocked behind the pull drain's Notify; a slow peer stalls the local turn")
	}

	release()
	select {
	case <-pullDone:
	case <-time.After(2 * time.Second):
		t.Fatal("pull did not return after the stall was released")
	}
	require.Eventually(t, func() bool { return len(subscriber.seenSeqs()) == 2 }, time.Second, time.Millisecond)
	assert.Equal(t, []int64{4, 5}, subscriber.seenSeqs(), "buffered and post-stall live frames must deliver in seq order")
}

// Given a peer already caught up (ready) with the flush worker stalled
// delivering an earlier live frame, when the peer pulls again, then the pull
// must not deliver a later buffered frame out of order: the pull path hands
// live delivery to the single flush worker so frames reach the subscriber in
// monotonic seq order.
func TestPeerSessionLive_GivenWorkerStalledOnEarlierFrame_ThenPullDoesNotDeliverLaterFrameOutOfOrder(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil)

	released := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(released) }) }
	defer release()
	subscriber := &stallingSeqSubscriber{blockSeq: 1, entered: make(chan struct{}), released: released}
	attached, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{SessionID: 41}, subscriber)
	require.NoError(t, err)
	assert.Equal(t, int64(0), attached.LatestSeq)
	_, err = deps.svc.PullPeerSession(ctx, wire.SessionPullParams{SessionID: 41}, subscriber)
	require.NoError(t, err)

	// 第一条实时帧（seq 1）：flush worker 进入投递并卡在对端写上。
	deps.svc.publishPeerEvent(41, agentruntime.TextDelta{Text: "first"})
	select {
	case <-subscriber.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("flush worker never attempted the first live frame")
	}

	// 第二条实时帧（seq 2）到达后 peer 再 pull 一次：不得抢在 seq 1 之前交付。
	deps.svc.publishPeerEvent(41, agentruntime.TextDelta{Text: "second"})
	_, err = deps.svc.PullPeerSession(ctx, wire.SessionPullParams{SessionID: 41, Cursor: 0}, subscriber)
	require.NoError(t, err)
	assert.Empty(t, subscriber.seenSeqs(), "a later live frame must not be delivered ahead of an earlier stalled one")

	release()
	require.Eventually(t, func() bool { return len(subscriber.seenSeqs()) == 2 }, time.Second, time.Millisecond)
	assert.Equal(t, []int64{1, 2}, subscriber.seenSeqs(), "live frames must deliver in monotonic seq order")
}

// blockingPeerSubscriber 的 Notify 阻塞到 released 关闭，用于证明本地发布不会被
// 卡住对端的网络写拖死。
type blockingPeerSubscriber struct {
	released chan struct{}
	notified chan struct{}
}

// stallingSeqSubscriber 记录收到的帧 seq；对 blockSeq 这一帧的 Notify 阻塞到
// released 关闭，用于证明「拉平交付」与「实时 worker 投递」互不串扰：不管实时
// 帧由哪条路径投递，都不允许在 publication 锁内阻塞，也不允许抢在更早的帧之前。
type stallingSeqSubscriber struct {
	mu       sync.Mutex
	seqs     []int64
	blockSeq int64
	entered  chan struct{}
	released chan struct{}
}

func (s *stallingSeqSubscriber) Notify(_ string, params any) error {
	frame, ok := params.(wire.EventFrame)
	if !ok {
		return nil
	}
	// 先阻塞再记录：seenSeqs 反映「投递完成」的时序，而不是投递开始的时序。
	// 这样才能暴露「后一帧先写完、更早帧还卡在写」的乱序窗口。
	if frame.Seq == s.blockSeq {
		close(s.entered)
		<-s.released
	}
	s.mu.Lock()
	s.seqs = append(s.seqs, frame.Seq)
	s.mu.Unlock()
	return nil
}

func (s *stallingSeqSubscriber) seenSeqs() []int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int64(nil), s.seqs...)
}

func (s *stallingSeqSubscriber) Done() <-chan struct{} { return make(chan struct{}) }

func (s *blockingPeerSubscriber) Notify(string, any) error {
	<-s.released
	select {
	case s.notified <- struct{}{}:
	default:
	}
	return nil
}

func (s *blockingPeerSubscriber) Done() <-chan struct{} { return make(chan struct{}) }

type peerNotification struct {
	method string
	params any
}

type peerRecordingSubscriber struct {
	mu      sync.Mutex
	done    chan struct{}
	records []peerNotification
}

func newRecordingPeerSubscriber() *peerRecordingSubscriber {
	return &peerRecordingSubscriber{done: make(chan struct{})}
}

func (s *peerRecordingSubscriber) Notify(method string, params any) error {
	s.mu.Lock()
	s.records = append(s.records, peerNotification{method: method, params: params})
	s.mu.Unlock()
	return nil
}

func (s *peerRecordingSubscriber) Done() <-chan struct{} { return s.done }
func (s *peerRecordingSubscriber) notifications() []peerNotification {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]peerNotification(nil), s.records...)
}

func agentForPeerSession() *agent_entity.Agent {
	return &agent_entity.Agent{ID: 7, AgentBackendID: 11}
}

// peerEventKind 读出一条密封事件在 wire 上的判别值 —— 走的是事件自己的
// MarshalJSON,与真正推出去的那份字节同源。
func peerEventKind(t *testing.T, event agentruntime.Event) agentruntime.EventKind {
	t.Helper()
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	var head struct {
		Kind agentruntime.EventKind `json:"kind"`
	}
	require.NoError(t, json.Unmarshal(raw, &head))
	return head.Kind
}

func assertPeerNotificationSeqs(t *testing.T, notifications []wire.JournaledNotification, want ...int64) {
	t.Helper()
	require.Len(t, notifications, len(want))
	for i, seq := range want {
		assert.Equal(t, seq, notifications[i].Seq)
		assert.Equal(t, wire.NotifyEvent, notifications[i].Method)
	}
}

func eventFrameSeq(t *testing.T, params any) int64 {
	t.Helper()
	frame, ok := params.(wire.EventFrame)
	require.True(t, ok)
	return frame.Seq
}

// Given 一条落库的助手消息带着本轮的模型与计时,When 合成对端快照,Then 收口的
// Done 事件把它们一起送出去。
//
// 对端 Peer Tab 的转录与浏览器控制台走的是**同一个**共享投影器,那边的 meta
// (模型 · 耗时 · 首字 · 速率)读的正是 done 事件上的这几格。此前这里发的是一个
// 空的 Done{} —— 数据就在手边那条消息实体上,一格都没送。
//
// agentred 那侧的同一份数走 runtime.runResultDone 终态帧,不走这条:它在事件流
// 之上量表,Done 早就转发出去了,回不去。两个生产者各用自己填得起的载体,落点
// (共享包的 EventDone)是同一个。
func TestSynthesizePeerHistory_GivenTurnStats_ThenDoneCarriesThem(t *testing.T) {
	messages := []*chat_entity.Message{
		{SessionID: 41, Role: "user", Seq: 1, BlocksJSON: `[{"type":"text","data":{"text":"ship it"}}]`},
		{
			SessionID: 41, Role: "assistant", Seq: 2,
			BlocksJSON:   `[{"type":"text","data":{"text":"done"}}]`,
			Model:        "claude-sonnet-4-6",
			DurationMs:   9640,
			FirstTokenMs: 8010,
			TokensPerSec: 14.2,
		},
	}

	events, err := synthesizePeerHistory(41, messages)
	require.NoError(t, err)

	var done agentruntime.Done
	var found bool
	for _, frame := range events {
		if d, ok := frame.Event.(agentruntime.Done); ok {
			done, found = d, true
		}
	}
	require.True(t, found, "助手消息收口必须发一条 Done")
	assert.Equal(t, "claude-sonnet-4-6", done.Model)
	assert.Equal(t, 9640, done.DurationMs)
	assert.Equal(t, 8010, done.FirstTokenMs)
	assert.InDelta(t, 14.2, done.TokensPerSec, 0.001)
}

// Given 一轮在这台桌面端上跑完,When 收口,Then 对端订阅者收到一条带本轮统计的
// Done —— 与重连后从快照里读到的那一条同形。
//
// 实时与历史两条路必须给出同一份数:对端 Peer Tab 上一轮 meta 的模型 / 耗时 /
// 首字 / 速率,断线重连前后不该变。历史那一半在
// TestSynthesizePeerHistory_GivenTurnStats_ThenDoneCarriesThem。
func TestPublishPeerTurnDone_GivenFinishedTurn_ThenPeersSeeTurnStats(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil)

	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{SessionID: 41}, subscriber)
	require.NoError(t, err)

	deps.svc.publishPeerTurnDone(41, &chat_entity.Message{
		SessionID: 41, Role: "assistant",
		Model: "claude-sonnet-4-6", DurationMs: 9640, FirstTokenMs: 8010, TokensPerSec: 14.2,
	})

	var done agentruntime.Done
	var found bool
	require.Eventually(t, func() bool {
		for _, record := range subscriber.notifications() {
			frame, ok := record.params.(wire.EventFrame)
			if !ok {
				continue
			}
			if d, isDone := frame.Event.(agentruntime.Done); isDone {
				done, found = d, true
			}
		}
		return found
	}, time.Second, time.Millisecond)

	assert.Equal(t, "claude-sonnet-4-6", done.Model)
	assert.Equal(t, 9640, done.DurationMs)
	assert.Equal(t, 8010, done.FirstTokenMs)
	assert.InDelta(t, 14.2, done.TokensPerSec, 0.001)
}

// 一轮收口必须走 publishPeerTurnDone —— 两条收口路径(用户轮与自主续轮各自的
// finalize)都要。
//
// 为什么用 AST 守:这两只函数各要一整套 runner / repo / 事件循环才跑得起来,而要守
// 的事实只有一句「它调了那一只」。同包的 peer tee 守卫(peer_session_tee_test.go)
// 用的是同一手法,理由也一样。漏掉任一条的表现是**静默的**:对端那一轮的 meta 空着,
// 而另一条路的照常有,两边对不上还查不出来路。
func TestTurnFinishPaths_GivenPeerSubscribers_ThenPublishTurnDone(t *testing.T) {
	for _, tc := range []struct{ file, name string }{
		{file: "turn_run.go", name: "finalize"},
		{file: "autonomous_turn_run.go", name: "finalize"},
	} {
		t.Run(tc.file+":"+tc.name, func(t *testing.T) {
			source, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			file, err := parser.ParseFile(token.NewFileSet(), tc.file, source, 0)
			require.NoError(t, err)

			var calls bool
			ast.Inspect(file, func(node ast.Node) bool {
				decl, ok := node.(*ast.FuncDecl)
				if !ok || decl.Name.Name != tc.name {
					return true
				}
				ast.Inspect(decl.Body, func(inner ast.Node) bool {
					if call, ok := inner.(*ast.CallExpr); ok && selectorName(call) == "publishPeerTurnDone" {
						calls = true
					}
					return true
				})
				return false
			})
			assert.True(t, calls, "%s 里的 %s 必须把本轮统计发给对端订阅者", tc.file, tc.name)
		})
	}
}
