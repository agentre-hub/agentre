package chat_svc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
)

// 宿主重启前后,同一份内容必须仍是同一个编号 —— 否则对端的游标在重启后指向一个
// 宿主认不回来的号,高水位低于游标,自愈规则只能删表重拉(问题 B)。
//
// 这条用例走的正是那条路:attach → 拉平 → 实时输出 → 宿主重启 → 再 attach。
func TestAttachPeerSession_GivenHostRestartAfterLiveOutput_ThenPeerCursorStaysInsideTheSeqUniverse(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	messages := []*chat_entity.Message{
		{ID: 91, SessionID: 41, Role: "user", Seq: 1, BlocksJSON: `[{"type":"text","data":{"text":"hello"}}]`},
		{ID: 92, SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: `[{"type":"text","data":{"text":"world"}}]`},
	}
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil).AnyTimes()
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().List(ctx, int64(41)).Return(messages, nil).AnyTimes()

	subscriber := newRecordingPeerSubscriber()
	attached, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)
	page, err := deps.svc.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)
	require.Equal(t, attached.LatestSeq, page.Cursor, "对端在实时输出之前已经拉平")
	cursor := page.Cursor

	// 逐片段增量是预览帧:它不该推进这条对话的编号宇宙。
	deps.svc.publishPeerEvent(41, agentruntime.TextDelta{Text: "live-"})
	deps.svc.publishPeerEvent(41, agentruntime.TextDelta{Text: "token"})
	require.Eventually(t, func() bool {
		return len(subscriber.notifications()) == 2
	}, time.Second, time.Millisecond)
	// 对端的游标随它收到的**持久**帧走(预览帧按协议不推进游标)。
	for _, record := range subscriber.notifications() {
		frame, ok := record.params.(wire.EventFrame)
		require.True(t, ok)
		assert.True(t, frame.Preview, "逐片段增量必须是预览帧")
		if !frame.Preview && frame.Seq > cursor {
			cursor = frame.Seq
		}
	}

	// 宿主重启:同一份库、同一批消息,换一个进程内的 chatSvc。
	restarted := NewChat(NoopEmitter{}).(*chatSvc)
	reattached, err := restarted.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, newRecordingPeerSubscriber())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, reattached.LatestSeq, cursor,
		"重启后的高水位低于对端游标就会触发删表重拉 —— 预览帧不得占号")
}

// fakeFrameSeqLedger 是帧编号台账的内存替身。它活得比 chatSvc 长 —— 宿主「重启」
// 换一个 chatSvc 之后读到的仍是同一份账,这正是这组用例要观察的那件事。
type fakeFrameSeqLedger struct {
	mu       sync.Mutex
	rows     []transcript_repo.FrameSeqRow
	allocErr error
}

func (l *fakeFrameSeqLedger) Load(_ context.Context, sessionID int64) (map[transcript_repo.FrameKey]int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[transcript_repo.FrameKey]int64{}
	for _, row := range l.rows {
		if row.SessionID != sessionID {
			continue
		}
		key := transcript_repo.FrameKey{MessageID: row.MessageID, BlockIdx: row.BlockIdx, Ordinal: row.Ordinal}
		if prev, ok := out[key]; ok && prev >= row.Seq {
			continue
		}
		out[key] = row.Seq
	}
	return out, nil
}

// failAllocate 让接下来的取号都失败(err 为 nil 时恢复)。落库失败这条分支在 spec
// 「帧编号」里是明写的:分配与落库不可分,落库失败即不得发布。
func (l *fakeFrameSeqLedger) failAllocate(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.allocErr = err
}

func (l *fakeFrameSeqLedger) Allocate(_ context.Context, sessionID int64, keys []transcript_repo.FrameKey) ([]int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.allocErr != nil {
		return nil, l.allocErr
	}
	if len(keys) == 0 {
		return nil, nil
	}
	var latest int64
	for _, row := range l.rows {
		if row.SessionID == sessionID && row.Seq > latest {
			latest = row.Seq
		}
	}
	seqs := make([]int64, 0, len(keys))
	for i, key := range keys {
		seq := latest + int64(i) + 1
		l.rows = append(l.rows, transcript_repo.FrameSeqRow{
			SessionID: sessionID, MessageID: key.MessageID,
			BlockIdx: key.BlockIdx, Ordinal: key.Ordinal, Seq: seq,
		})
		seqs = append(seqs, seq)
	}
	return seqs, nil
}

func (l *fakeFrameSeqLedger) DeleteBySession(_ context.Context, sessionID int64) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.rows[:0]
	var removed int64
	for _, row := range l.rows {
		if row.SessionID == sessionID {
			removed++
			continue
		}
		kept = append(kept, row)
	}
	l.rows = kept
	return removed, nil
}

func (l *fakeFrameSeqLedger) rowCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.rows)
}

// publishPeerDurableFrame 发一帧持久帧。一条 user 消息配一个 text 块正好投影出一帧,
// 所以这是「让这条对话往前走一个号」的最小动作 —— 逐片段增量如今是预览帧,不再占号,
// 需要观察编号宇宙的用例走这条路。
func publishPeerDurableFrame(t *testing.T, svc *chatSvc, sessionID, messageID int64, text string) {
	t.Helper()
	svc.publishPeerMessageFrames(context.Background(), sessionID, &chat_entity.Message{
		ID: messageID, SessionID: sessionID, Role: "user", Seq: int(messageID),
		BlocksJSON: `[{"type":"text","data":{"text":"` + text + `"}}]`,
	}, true)
}

// richPeerTranscript 是一条覆盖多种块类型的转录:文本、工具调用与结果、一次尚未被
// 回答的 UserAskRequest、以及一条带 usage 的收口。
func richPeerTranscript() []*chat_entity.Message {
	return []*chat_entity.Message{
		{ID: 91, SessionID: 41, Role: "user", Seq: 1, Createtime: 1000,
			BlocksJSON: `[{"type":"text","data":{"text":"帮我看看"}}]`},
		{ID: 92, SessionID: 41, Role: "assistant", Seq: 2, Createtime: 2000,
			Model: "claude-sonnet-4-6", PromptTokens: 12, CompletionTokens: 34,
			BlocksJSON: `[` +
				`{"type":"thinking","data":{"text":"先读文件"}},` +
				`{"type":"text","data":{"text":"这就去看"}},` +
				`{"type":"tool_use","data":{"id":"tu-1","name":"read","input":{"path":"a.go"}}},` +
				`{"type":"tool_result","data":{"tool_use_id":"tu-1","content":[{"type":"text","data":{"text":"内容"}}]}},` +
				`{"type":"user_ask","data":{"request_id":"ask-1","tool_call_id":"tu-2","questions":[{"id":"q1","question":"继续?"}]}}` +
				`]`},
	}
}

// 帧编号必须活在库里,不能是每进程现算的:一条对话被 attach、宿主重启、再 attach
// 之后,同一份内容仍要是同一个号(spec「帧编号」;问题 B 的回归锚)。
func TestAttachPeerSession_GivenHostRestart_ThenSameContentKeepsTheSamePersistedSeq(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	messages := richPeerTranscript()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil).AnyTimes()
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().List(ctx, int64(41)).Return(messages, nil).AnyTimes()

	before := peerSeqByContent(t, deps.svc, ctx)
	require.NotEmpty(t, before)
	allocated := deps.ledger.rowCount()
	require.Equal(t, len(before), allocated, "首次补齐给每一帧都落了一个号")

	// 宿主重启:同一份库,换一个进程内的 chatSvc。
	restarted := NewChat(NoopEmitter{}).(*chatSvc)
	after := peerSeqByContent(t, restarted, ctx)

	assert.Equal(t, before, after, "同一份内容在重启前后必须是同一个 seq")
	assert.Equal(t, allocated, deps.ledger.rowCount(), "重启后不得重新取号")
}

// 存量对话没有编号:它在**第一次**被发布或补齐时才惰性补齐,没被访问过的对话
// 不付出任何代价(spec「帧编号」)。
func TestAttachPeerSession_GivenLegacyConversationWithoutSeqs_ThenNumbersAreAllocatedLazilyOnFirstCatchup(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	messages := richPeerTranscript()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil).AnyTimes()
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().List(ctx, int64(41)).Return(messages, nil).AnyTimes()

	require.Equal(t, 0, deps.ledger.rowCount(), "存量转录一个号都没有")

	subscriber := newRecordingPeerSubscriber()
	attached, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)
	page, err := deps.svc.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(41), Limit: 100}, subscriber)
	require.NoError(t, err)

	require.NotEmpty(t, page.Notifications)
	assert.Equal(t, len(page.Notifications), deps.ledger.rowCount(), "补齐时按块的自然顺序确定性地补上编号")
	assert.Equal(t, attached.LatestSeq, page.Cursor)
	for index, notification := range page.Notifications {
		assert.Equal(t, int64(index+1), notification.Seq, "存量补齐按投影顺序从 1 起连号")
	}
}

// 块被原地修补(一次 UserAskRequest 被回答)时,新增的那一帧取**末尾新号**,
// 已发布过的编号一律不动;补齐因此按 seq 顺序重放,resolved 出现在 request 之后
// 若干位(spec「帧编号」)。
func TestAttachPeerSession_GivenAnsweredUserAsk_ThenResolvedFrameTakesAFreshTailSeq(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	messages := richPeerTranscript()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil).AnyTimes()
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().List(ctx, int64(41)).Return(messages, nil).AnyTimes()

	before := peerSeqByContent(t, deps.svc, ctx)
	requestSeq, ok := before["UserAskRequest:ask-1"]
	require.True(t, ok, "未被回答时只投影出 request 一帧")
	_, hadResolved := before["UserAskResolved:ask-1"]
	require.False(t, hadResolved)
	tail := int64(0)
	for _, seq := range before {
		if seq > tail {
			tail = seq
		}
	}

	// 原地修补:同一个块被回答。
	messages[1].BlocksJSON = strings.Replace(messages[1].BlocksJSON,
		`"questions":[{"id":"q1","question":"继续?"}]`,
		`"questions":[{"id":"q1","question":"继续?"}],"answered":true,"answers":[{"question_index":0,"labels":["好"]}]`, 1)

	restarted := NewChat(NoopEmitter{}).(*chatSvc)
	after := peerSeqByContent(t, restarted, ctx)

	assert.Equal(t, requestSeq, after["UserAskRequest:ask-1"], "已发布过的编号不许挪动")
	for content, seq := range before {
		assert.Equal(t, seq, after[content], "原地修补不得挤动任何已发布的编号: "+content)
	}
	resolvedSeq, ok := after["UserAskResolved:ask-1"]
	require.True(t, ok, "回答之后多出 resolved 一帧")
	assert.Equal(t, tail+1, resolvedSeq, "新增的那一帧取的是末尾新号")
	assert.Greater(t, resolvedSeq, requestSeq, "补齐按 seq 重放,resolved 排在 request 之后")
}

// peerSeqByContent attach + 补齐一整条对话,交回「帧内容 → seq」,并顺带校验补齐
// 确实是按 seq 递增重放的。
func peerSeqByContent(t *testing.T, svc *chatSvc, ctx context.Context) map[string]int64 {
	t.Helper()
	subscriber := newRecordingPeerSubscriber()
	_, err := svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)
	page, err := svc.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(41), Limit: 100}, subscriber)
	require.NoError(t, err)
	require.False(t, page.HasMore)
	out := make(map[string]int64, len(page.Notifications))
	var previous int64
	for _, notification := range page.Notifications {
		assert.Greater(t, notification.Seq, previous, "补齐按 seq 顺序重放")
		previous = notification.Seq
		frame, ok := notification.Params.(*wire.EventFrame)
		require.True(t, ok)
		require.False(t, frame.Preview, "补齐只返回持久帧")
		out[peerFrameContentKey(t, frame.Event)] = notification.Seq
	}
	return out
}

// peerFrameContentKey 给一帧起一个与它在转录里的位置无关的内容名,用来跨重启比对
// 「同一份内容拿到的还是不是同一个号」。
func peerFrameContentKey(t *testing.T, event agentruntime.Event) string {
	t.Helper()
	payload, err := json.Marshal(event)
	require.NoError(t, err)
	switch e := event.(type) {
	case agentruntime.UserAskRequest:
		return "UserAskRequest:" + e.RequestID
	case agentruntime.UserAskResolved:
		return "UserAskResolved:" + e.RequestID
	default:
		return fmt.Sprintf("%T:%s", event, payload)
	}
}

// 帧的位置(消息 / 块下标 / 块内第几帧)是本轮编号挂靠的那一格,而它由共享投影器
// 说了算 —— 逐块单独投影再拼起来,必须与整条一次投影逐帧相等。这条守卫在
// 「块 → 帧」的分派表被复制出第二份、或两条路开始漂移时判红。
func TestProjectMessageListFrames_MatchesSharedProjection(t *testing.T) {
	messages := richPeerTranscript()
	want, wantTimes, err := transcript.ProjectMessages("conv-1", messages)
	require.NoError(t, err)

	got, err := transcript.ProjectKeyedMessages("conv-1", messages)
	require.NoError(t, err)

	require.Len(t, got, len(want))
	for index := range want {
		assert.Equal(t, want[index], got[index].Frame, "第 %d 帧", index)
		assert.Equal(t, wantTimes[index], got[index].Createtime, "第 %d 帧的时刻", index)
	}
}

// 分配与落库不可分:落库失败即没有分配,这一帧因此**不得发布** —— 否则对端会持有
// 一个宿主认不回来的号(spec「帧编号」的失败分支)。
func TestPublishPeerMessageFrames_GivenSeqAllocationFails_ThenTheFrameIsWithheld(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil)

	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)

	deps.ledger.failAllocate(errors.New("ledger write failed"))
	publishPeerDurableFrame(t, deps.svc, 41, 71, "落不了库")

	publication := deps.svc.peerPublication(41, convID(41))
	publication.mu.Lock()
	logged, highWater := len(publication.history), publication.nextSeq
	publication.mu.Unlock()
	assert.Zero(t, logged, "取不到号的帧不得进补齐日志")
	assert.Zero(t, highWater, "取不到号的帧不得推进高水位")
	assert.Empty(t, subscriber.notifications(), "取不到号的帧不得发给对端")

	// 台账恢复之后同一份内容照常取号发布:失败没有把这个位置记成「已发布」。
	deps.ledger.failAllocate(nil)
	publishPeerDurableFrame(t, deps.svc, 41, 71, "落不了库")
	require.Eventually(t, func() bool {
		return len(subscriber.notifications()) == 1
	}, time.Second, time.Millisecond)
	assert.Equal(t, int64(1), eventFrameSeq(t, subscriber.notifications()[0].params),
		"失败那一次没有烧掉任何号")
}

// subagentStatePeerBlocks 是一条 assistant 消息的正文:一个 text 块,外加一个投影出
// **两帧**(SubagentDone + SubagentModel)的 subagent_state 块 —— 后者正是 spec 点名
// 的那类会被原地修补的块,也是「块内第几帧(ordinal)」唯一说得清编号归属的形状。
func subagentStatePeerBlocks(toolUses int) string {
	return `[{"type":"text","data":{"text":"派个子代理"}},` +
		fmt.Sprintf(`{"type":"subagent_state","data":{"parent_tool_call_id":"tu-1","task_id":"t-1",`+
			`"status":"running","model":"claude-sonnet-4-6","tool_uses":%d}}]`, toolUses)
}

// 块被原地修补时只有**内容变了的那一帧**取一个新的末尾号;同一个块里没变的那一帧、
// 以及它前后已发布过的帧,编号一律不动、也不重发(spec「帧编号」)。
func TestPublishPeerMessageFrames_GivenBlockPatchedInPlace_ThenOnlyTheChangedFrameTakesAFreshTailSeq(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil)

	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)

	msg := &chat_entity.Message{ID: 71, SessionID: 41, Role: "assistant", Seq: 1,
		BlocksJSON: subagentStatePeerBlocks(1)}
	deps.svc.publishPeerMessageFrames(ctx, 41, msg, true)
	// text / SubagentDone / SubagentModel / Done —— 一个块两帧,所以是 4 而不是 3。
	require.Eventually(t, func() bool {
		return len(subscriber.notifications()) == 4
	}, time.Second, time.Millisecond)
	before := subscriber.notifications()
	for index, record := range before {
		require.Equal(t, int64(index+1), eventFrameSeq(t, record.params), "首发按投影顺序连号")
	}

	// 同一份内容再发一次:一帧都不该重发,一个号都不该烧掉。
	deps.svc.publishPeerMessageFrames(ctx, 41, msg, true)
	assert.Equal(t, before, subscriber.notifications(), "内容没变就不再发布,也不取新号")
	assert.Equal(t, 4, deps.ledger.rowCount())

	// 原地修补:subagent_state 的进度推进(spec 点名的那一类)。
	msg.BlocksJSON = subagentStatePeerBlocks(2)
	deps.svc.publishPeerMessageFrames(ctx, 41, msg, true)
	require.Eventually(t, func() bool {
		return len(subscriber.notifications()) == 5
	}, time.Second, time.Millisecond)
	after := subscriber.notifications()

	assert.Equal(t, before, after[:4], "已发布过的帧一律不重发、编号一律不动")
	patched, ok := after[4].params.(wire.EventFrame)
	require.True(t, ok)
	assert.Equal(t, int64(5), patched.Seq, "修补后的那一帧取的是末尾新号")
	assert.False(t, patched.Preview, "它是持久帧")
	done, ok := patched.Event.(agentruntime.SubagentDone)
	require.True(t, ok, "变的是 subagent_state 投影出的第一帧")
	assert.Equal(t, 2, done.Info.ToolUses)
}

// 实时发出去的持久帧,它的号同样活在库里:宿主重启后对端手里那个号仍然认得回来,
// 高水位不会掉到游标以下 —— 这正是问题 B 里「每次重启都删表重拉」的那条回归锚。
func TestAttachPeerSession_GivenFramesPublishedLiveThenHostRestart_ThenThePeerCursorSurvives(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	rows := richPeerTranscript()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil).AnyTimes()
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().List(ctx, int64(41)).DoAndReturn(
		func(context.Context, int64) ([]*chat_entity.Message, error) { return rows, nil }).AnyTimes()

	subscriber := newRecordingPeerSubscriber()
	attached, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)
	page, err := deps.svc.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(41), Limit: 100}, subscriber)
	require.NoError(t, err)
	require.Equal(t, attached.LatestSeq, page.Cursor, "对端在实时输出之前已经拉平")
	cursor := page.Cursor

	// 本轮新落库的一条 assistant:它的持久帧在这里实时取号(text + Done 两帧)。
	live := &chat_entity.Message{ID: 93, SessionID: 41, Role: "assistant", Seq: 3, Createtime: 3000,
		Model: "claude-opus-4-6", BlocksJSON: `[{"type":"text","data":{"text":"看完了"}}]`}
	rows = append(rows, live)
	deps.svc.publishPeerMessageFrames(ctx, 41, live, true)
	require.Eventually(t, func() bool {
		return len(subscriber.notifications()) == 2
	}, time.Second, time.Millisecond)
	liveSeqs := map[string]int64{}
	for _, record := range subscriber.notifications() {
		frame, ok := record.params.(wire.EventFrame)
		require.True(t, ok)
		require.False(t, frame.Preview, "落了库的块投影出的是持久帧")
		assert.Greater(t, frame.Seq, cursor, "实时持久帧取的是末尾新号")
		liveSeqs[peerFrameContentKey(t, frame.Event)] = frame.Seq
		if frame.Seq > cursor {
			cursor = frame.Seq
		}
	}
	require.Len(t, liveSeqs, 2)

	// 宿主重启:同一份库、同一批消息,换一个进程内的 chatSvc。
	restarted := NewChat(NoopEmitter{}).(*chatSvc)
	reattached, err := restarted.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, newRecordingPeerSubscriber())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, reattached.LatestSeq, cursor,
		"重启后的高水位低于对端游标就会触发删表重拉")

	after := peerSeqByContent(t, restarted, ctx)
	for content, seq := range liveSeqs {
		assert.Equal(t, seq, after[content], "实时发过的那一帧在重启后仍是同一个号: "+content)
	}
}

// 对端在**轮中**挂上来:此刻在飞那条 assistant 结尾的正文块还在长。它不该在这一刻
// 就取号 —— 编号是一次性的(分配与落库不可分),半截内容占掉一个号之后,收口时同一个
// 位置的内容变了只能再取一个末尾号,对端于是把同一段话收两遍(硬不变量 1 的「重」)。
//
// agentred 的补齐读侧已经这么做;桌面端做宿主时是同一件事,必须由同一行代码判定
// (transcript.WithoutUnsettledTail,spec「复用边界」)。
func TestAttachPeerSession_GivenTurnStillRunning_ThenTheGrowingTailWaitsForSettlement(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	assistant := &chat_entity.Message{ID: 92, SessionID: 41, Role: "assistant", Seq: 2,
		BlocksJSON: `[{"type":"text","data":{"text":"one"}}]`}
	rows := []*chat_entity.Message{
		{ID: 91, SessionID: 41, Role: "user", Seq: 1, BlocksJSON: `[{"type":"text","data":{"text":"hi"}}]`},
		assistant,
	}
	deps.session.EXPECT().Find(ctx, int64(41)).Return(
		&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "running"}, nil).AnyTimes()
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().List(ctx, int64(41)).DoAndReturn(
		func(context.Context, int64) ([]*chat_entity.Message, error) { return rows, nil }).AnyTimes()

	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)
	page, err := deps.svc.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(41), Limit: 100}, subscriber)
	require.NoError(t, err)

	// 这一轮收口:同一个位置的正文长完了。
	assistant.BlocksJSON = `[{"type":"text","data":{"text":"onetwo"}}]`
	deps.svc.publishPeerMessageFrames(ctx, 41, assistant, true)
	// 对端这条转录 = 补齐拿回的前缀 + 随后实时收到的持久帧。
	deliveredTexts := func() []string {
		var texts []string
		frames := make([]any, 0, len(page.Notifications))
		for _, notification := range page.Notifications {
			frames = append(frames, notification.Params)
		}
		for _, record := range subscriber.notifications() {
			frames = append(frames, record.params)
		}
		for _, params := range frames {
			var event agentruntime.Event
			switch frame := params.(type) {
			case wire.EventFrame:
				event = frame.Event
			case *wire.EventFrame:
				event = frame.Event
			default:
				t.Fatalf("对端收到的不是事件帧:%T", params)
			}
			if delta, isText := event.(agentruntime.TextDelta); isText {
				texts = append(texts, delta.Text)
			}
		}
		return texts
	}
	require.Eventually(t, func() bool {
		return slices.Contains(deliveredTexts(), "onetwo")
	}, time.Second, time.Millisecond, "收口那一发要把定稿的正文交出去")

	assert.Equal(t, []string{"onetwo"}, deliveredTexts(),
		"同一个正文位置在对端的转录里只能出现一次 —— 在飞的半截不该先占一个号")
}

// 一帧的**时刻**同样要活在内容里,不能是「发布的那一刻」:对端那条转录的 HH:mm 读的
// 就是补齐带回来的这一格,而宿主重启之后同一条转录是从投影重建的(时刻的归属由
// transcript.ProjectMessages 说死 —— 取所属消息的 createtime)。实时发布若写「此刻」,
// 同一个 seq 在重启前后报出两个时刻:一轮里消息建行与块定稿隔着整趟工具往返,对端的
// 转录会在重连之后整体跳回本轮起点。
func TestPullPeerSession_GivenFramePublishedLive_ThenItsCreatetimeSurvivesHostRestart(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	rows := richPeerTranscript()
	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil).AnyTimes()
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().List(ctx, int64(41)).DoAndReturn(
		func(context.Context, int64) ([]*chat_entity.Message, error) { return rows, nil }).AnyTimes()

	first := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, first)
	require.NoError(t, err)
	_, err = deps.svc.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(41), Limit: 100}, first)
	require.NoError(t, err)

	// 本轮新落库的一条 assistant:它在这里实时发布,时刻应当是它这一行的 createtime。
	const liveCreatetime = int64(3000)
	live := &chat_entity.Message{ID: 93, SessionID: 41, Role: "assistant", Seq: 3,
		Createtime: liveCreatetime, Model: "claude-opus-4-6",
		BlocksJSON: `[{"type":"text","data":{"text":"看完了"}}]`}
	rows = append(rows, live)
	deps.svc.publishPeerMessageFrames(ctx, 41, live, true)
	require.Eventually(t, func() bool {
		return len(first.notifications()) == 2
	}, time.Second, time.Millisecond)

	// 断线重连(同一个进程):新订阅的高水位包含刚才那两帧,补齐从 publication 的日志读。
	reconnected := newRecordingPeerSubscriber()
	_, err = deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, reconnected)
	require.NoError(t, err)
	livePage, err := deps.svc.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(41), Limit: 100}, reconnected)
	require.NoError(t, err)

	// 宿主重启:同一份库、同一批消息,换一个进程内的 chatSvc —— 这一份是从投影重建的。
	restarted := NewChat(NoopEmitter{}).(*chatSvc)
	restartedSub := newRecordingPeerSubscriber()
	_, err = restarted.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, restartedSub)
	require.NoError(t, err)
	restartedPage, err := restarted.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(41), Limit: 100}, restartedSub)
	require.NoError(t, err)

	liveTimes := createtimeBySeq(t, livePage.Notifications)
	restartedTimes := createtimeBySeq(t, restartedPage.Notifications)
	require.NotEmpty(t, liveTimes)
	assert.Equal(t, restartedTimes, liveTimes,
		"每一个 seq 的时刻在重启前后必须一致 —— 重启那一份是从投影重建的")

	// 而实时发布的那两帧带的正是它所属消息(93)的 createtime,不是发布的那一刻。
	liveSeqs := make([]int64, 0, 2)
	for _, record := range first.notifications() {
		frame, ok := record.params.(wire.EventFrame)
		require.True(t, ok)
		liveSeqs = append(liveSeqs, frame.Seq)
	}
	require.Len(t, liveSeqs, 2)
	for _, seq := range liveSeqs {
		assert.Equal(t, liveCreatetime, liveTimes[seq],
			"seq %d 的时刻取所属消息的 createtime", seq)
	}
}

// createtimeBySeq 把一页补齐摊成「seq → 时刻」。
func createtimeBySeq(t *testing.T, notifications []wire.JournaledNotification) map[int64]int64 {
	t.Helper()
	out := make(map[int64]int64, len(notifications))
	for _, notification := range notifications {
		out[notification.Seq] = notification.Createtime
	}
	return out
}

// 桌面端做宿主时,发布用户那一行的持久帧要交回**这一发里取到的最高号**:发起方据它把
// 游标推进到「我已经持有的内容」,补齐于是不再重放它自己写下的那条用户消息
// (spec 2026-09-07 决策 1、决策 4 的两宿主对称)。没人 attach 过、因而没有编号宇宙时
// 交回 0 —— 发起方据此不推进游标,行为退回本轮之前。
func TestPublishPeerMessageFrames_GivenFramesAllocated_ThenReturnsHighestSeq(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()

	user := &chat_entity.Message{ID: 70, SessionID: 41, Role: "user", Seq: 1,
		BlocksJSON: `[{"type":"text","data":{"text":"hello"}}]`}

	// attach 之前没有编号宇宙:一个号都不取,交回 0。
	require.Zero(t, deps.svc.publishPeerMessageFrames(ctx, 41, user, true),
		"没有编号宇宙时不得交回一个宿主并未分配的号")

	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil)
	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)

	// 用户那一行只有一个文本块 → 一帧,取到 1 号。
	require.Equal(t, int64(1), deps.svc.publishPeerMessageFrames(ctx, 41, user, true))

	// 同一份内容再发一次:一帧都不该重发,也没有新号可交回。
	require.Zero(t, deps.svc.publishPeerMessageFrames(ctx, 41, user, true),
		"内容没变就没有新号,不得把上一次的号再报一遍")
}
