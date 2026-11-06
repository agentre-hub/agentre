package handlers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 控制台拥有的会话,审批卡由 agentred 自己出:卡要落进本会话的转录(控制台重连补齐看得到)
// 并作为持久帧实时推出(tool_approval_requested / _resolved),与桌面端 chat_svc 的卡同形。

// memTranscript 是 TranscriptPort 的内存实现:记下每次落库的块正文,按序发号。
type memTranscript struct {
	mu          sync.Mutex
	assistant   *transcript_entity.Message
	checkpoints []string
	finished    []string
	seq         int64
	// next 是插话分段后接上的那条新 assistant;nil = 不分段(SegmentTurn 什么都不交回)。
	next *transcript_entity.Message
	// persisted 按消息 id 记最近一次落库的块正文(Checkpoint / SegmentTurn / FinishTurn)。
	persisted map[int64]string
	// upserts 记每次增量 checkpoint(UpsertBlocks)交来的块行下标与截断点。
	upserts []memUpsert
	// failUpserts > 0 时接下来这么多次 UpsertBlocks 失败。
	failUpserts int
	// segments / finishes 记每次插话分段(SegmentTurn)与收口(FinishTurn)交来的块行下标与截断点。
	segments []memUpsert
	finishes []memUpsert
	// failFinish 非空时 FinishTurn 以它失败。
	failFinish error
}

func upsertOf(msg *transcript_entity.Message, rows []*transcript_entity.MessageBlock, truncateFrom int) memUpsert {
	up := memUpsert{messageID: msg.ID, truncateFrom: truncateFrom}
	for _, r := range rows {
		up.idx = append(up.idx, r.Idx)
	}
	return up
}

type memUpsert struct {
	messageID    int64
	idx          []int
	truncateFrom int
}

func (m *memTranscript) persistLocked(msg *transcript_entity.Message) {
	if m.persisted == nil {
		m.persisted = map[int64]string{}
	}
	m.persisted[msg.ID] = msg.BlocksJSON
}

func (m *memTranscript) persistedBody(id int64) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.persisted[id]
}

func (m *memTranscript) StartTurn(context.Context, string, string, []blocks.ContentBlock, transcript.UserSource) (*transcript_entity.Message, *transcript_entity.Message, error) {
	return nil, m.assistant, nil
}

func (m *memTranscript) Checkpoint(_ context.Context, msg *transcript_entity.Message, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints = append(m.checkpoints, msg.BlocksJSON)
	m.persistLocked(msg)
	return nil
}

func (m *memTranscript) UpsertBlocks(
	_ context.Context, msg *transcript_entity.Message, rows []*transcript_entity.MessageBlock, truncateFrom int,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upserts = append(m.upserts, upsertOf(msg, rows, truncateFrom))
	if m.failUpserts > 0 {
		m.failUpserts--
		return errors.New("database is locked")
	}
	m.checkpoints = append(m.checkpoints, msg.BlocksJSON)
	m.persistLocked(msg)
	return nil
}

func (m *memTranscript) upsertLog() []memUpsert {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]memUpsert(nil), m.upserts...)
}

func (m *memTranscript) SegmentTurn(
	_ context.Context, current *transcript_entity.Message, rows []*transcript_entity.MessageBlock, truncateFrom int,
	_ []agentruntime.ConsumedSteer,
) ([]*transcript_entity.Message, *transcript_entity.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.next == nil {
		return nil, nil, nil
	}
	m.segments = append(m.segments, upsertOf(current, rows, truncateFrom))
	m.persistLocked(current)
	return nil, m.next, nil
}

func (m *memTranscript) FinishTurn(
	_ context.Context, msg *transcript_entity.Message, rows []*transcript_entity.MessageBlock, truncateFrom int,
) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishes = append(m.finishes, upsertOf(msg, rows, truncateFrom))
	if m.failFinish != nil {
		return m.failFinish
	}
	m.finished = append(m.finished, msg.BlocksJSON)
	m.persistLocked(msg)
	return nil
}

func (m *memTranscript) AllocateFrameSeqs(_ context.Context, _ int64, keys []transcript.FrameKey) ([]int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]int64, len(keys))
	for i := range keys {
		m.seq++
		out[i] = m.seq
	}
	return out, nil
}

func (m *memTranscript) lastCheckpoint() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.checkpoints) == 0 {
		return ""
	}
	return m.checkpoints[len(m.checkpoints)-1]
}

// eventSink 收下推出去的帧,翻回 wire 事件。
type eventSink struct {
	mu     sync.Mutex
	events []agentruntime.Event
}

func (s *eventSink) Notify(n *agentrewire.RpcNotification) error {
	method, value, err := protowire.ProtoNotificationToWire(n)
	if err != nil {
		return err
	}
	if method != wire.NotifyEvent {
		return nil
	}
	var ev agentruntime.Event
	switch f := value.(type) {
	case wire.EventFrame:
		ev = f.Event
	case *wire.EventFrame:
		ev = f.Event
	}
	s.mu.Lock()
	s.events = append(s.events, ev)
	s.mu.Unlock()
	return nil
}

func (*eventSink) Request(context.Context, string, any, any) error { return nil }

func (s *eventSink) snapshot() []agentruntime.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agentruntime.Event(nil), s.events...)
}

func newApprovalTurn(t *testing.T) (*turnTranscript, *memTranscript, *eventSink) {
	t.Helper()
	mem := &memTranscript{assistant: &transcript_entity.Message{ID: 21, SessionID: 12, Role: "assistant", Seq: 2, BlocksJSON: "[]"}}
	sink := &eventSink{}
	h := NewRuntimeHandlers(RuntimeDeps{
		Transcript: mem,
		NotifyFor:  func(devicefp.Initiator) NotifierPort { return sink },
	})
	em := h.newEmitterFor(context.Background(), ctlTestConversation, "sha256:browser")
	scribe, _, _ := h.beginTranscript(em, "", nil, transcript.UserSource{})
	require.NotNil(t, scribe)
	return scribe, mem, sink
}

func ctlCard(requestID string) *transcriptblocks.ToolApprovalBlock {
	return &transcriptblocks.ToolApprovalBlock{
		ToolKey: agenttool.KeyCtl, RequestID: requestID, ToolName: "ctl_update_provider",
		ToolInput: transcriptblocks.CtlApprovalInput{Command: "agrctl update provider x"}.ToolInput(),
		Status:    "pending",
	}
}

func TestTurnTranscript_GivenConsoleApproval_WhenBegunAndResolved_ThenPersistedAndPublishedLive(t *testing.T) {
	scribe, mem, sink := newApprovalTurn(t)
	ctx := context.Background()

	sid, err := scribe.beginApproval(ctx, ctlCard("req-1"))
	require.NoError(t, err)
	assert.Equal(t, int64(12), sid, "the waiting line names this daemon's own session")
	assert.Contains(t, mem.lastCheckpoint(), `"tool_approval"`, "the card lands in this session's transcript")
	assert.Contains(t, mem.lastCheckpoint(), `"pending"`)

	events := sink.snapshot()
	require.NotEmpty(t, events)
	requested, ok := events[len(events)-1].(agentruntime.ToolApprovalRequested)
	require.True(t, ok, "the card goes out live as tool_approval_requested, got %T", events[len(events)-1])
	assert.Equal(t, agenttool.KeyCtl, requested.ToolKey)
	assert.Equal(t, "req-1", requested.RequestID)
	assert.Contains(t, string(requested.ToolInput), "agrctl update provider x")

	scribe.resolveApproval(ctx, "req-1", "approved", "已更新 provider x")
	assert.Contains(t, mem.lastCheckpoint(), `"approved"`)
	events = sink.snapshot()
	resolved, ok := events[len(events)-1].(agentruntime.ToolApprovalResolved)
	require.True(t, ok, "the verdict goes out live as tool_approval_resolved, got %T", events[len(events)-1])
	assert.Equal(t, agentruntime.ToolApprovalResolved{RequestID: "req-1", Status: "approved", Result: "已更新 provider x"}, resolved)
}

func TestTurnTranscript_GivenPendingApproval_WhenTurnFinishes_ThenCardExpiresAndNoNewCardsAreAccepted(t *testing.T) {
	scribe, mem, _ := newApprovalTurn(t)
	ctx := context.Background()
	_, err := scribe.beginApproval(ctx, ctlCard("req-2"))
	require.NoError(t, err)

	scribe.finish(ctx, wire.RunResultDoneFrame{ConversationID: ctlTestConversation})

	mem.mu.Lock()
	finished := strings.Join(mem.finished, "")
	mem.mu.Unlock()
	assert.Contains(t, finished, `"expired"`, "a card nobody can answer any more is not left pending")
	_, err = scribe.beginApproval(ctx, ctlCard("req-3"))
	assert.Error(t, err, "a finished turn takes no new cards")
}

// 收口的一轮不再是审批卡的落点:会话表放掉它(不留着整轮的累积状态),它的 ended 关上。
func TestTurnTranscript_GivenTurnFinishes_ThenCtlSessionsLetGoOfIt(t *testing.T) {
	mem := &memTranscript{assistant: &transcript_entity.Message{ID: 21, SessionID: 12, Role: "assistant", Seq: 2, BlocksJSON: "[]"}}
	ctl := NewCtlSessions(func() string { return "http://gw" })
	h := NewRuntimeHandlers(RuntimeDeps{
		Transcript: mem, Ctl: ctl,
		NotifyFor: func(devicefp.Initiator) NotifierPort { return &eventSink{} },
	})
	em := h.newEmitterFor(context.Background(), ctlTestConversation, "sha256:browser")
	ctl.bind(em.rid, em.peer, em.conversationID, DesktopCtlSession{}, false)
	scribe, _, _ := h.beginTranscript(em, "", nil, transcript.UserSource{})
	require.NotNil(t, scribe)
	tok := ctl.Credentials(0, em.rid).Token
	owner, ok := ctl.resolve(tok)
	require.True(t, ok)
	require.NotNil(t, owner.turn)

	scribe.finish(context.Background(), wire.RunResultDoneFrame{ConversationID: ctlTestConversation})

	owner, ok = ctl.resolve(tok)
	require.True(t, ok)
	assert.Nil(t, owner.turn, "the finished turn is released")
	select {
	case <-scribe.ended():
	default:
		t.Fatal("ended() stays open after finish")
	}
}

// 插话把轮次切段后,审批卡留在前一条消息里:它的决议必须写回**那一条**(落库 + 持久帧),
// 不能只改内存 —— 否则控制台重连补齐时那张卡永远停在 pending。
func segmentedApprovalTurn(t *testing.T) (*turnTranscript, *memTranscript, *eventSink) {
	t.Helper()
	scribe, mem, sink := newApprovalTurn(t)
	mem.next = &transcript_entity.Message{ID: 23, SessionID: 12, Role: "assistant", Seq: 4, BlocksJSON: "[]"}
	ctx := context.Background()
	_, err := scribe.beginApproval(ctx, ctlCard("req-seg"))
	require.NoError(t, err)
	scribe.observe(ctx, agentruntime.SteerConsumed{Steers: []agentruntime.ConsumedSteer{{QueuedID: "q1", Text: "顺便看下日志"}}})
	require.Equal(t, int64(23), scribe.msg.ID, "the steer segments the turn")
	return scribe, mem, sink
}

func lastResolved(events []agentruntime.Event) (agentruntime.ToolApprovalResolved, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if r, ok := events[i].(agentruntime.ToolApprovalResolved); ok {
			return r, true
		}
	}
	return agentruntime.ToolApprovalResolved{}, false
}

func TestTurnTranscript_GivenApprovalInSegmentedMessage_WhenResolved_ThenVerdictPersistedAndPublishedOnOwningMessage(t *testing.T) {
	scribe, mem, sink := segmentedApprovalTurn(t)
	ctx := context.Background()
	require.Contains(t, mem.persistedBody(21), `"pending"`)

	scribe.resolveApproval(ctx, "req-seg", "approved", "已更新 provider x")

	assert.Contains(t, mem.persistedBody(21), `"approved"`, "the verdict is written back to the message that owns the card")
	assert.NotContains(t, mem.persistedBody(23), `"tool_approval"`, "the card does not leak into the next segment")
	resolved, ok := lastResolved(sink.snapshot())
	require.True(t, ok, "the verdict goes out as a durable tool_approval_resolved frame")
	assert.Equal(t, agentruntime.ToolApprovalResolved{RequestID: "req-seg", Status: "approved", Result: "已更新 provider x"}, resolved)
}

func TestTurnTranscript_GivenPendingApprovalInSegmentedMessage_WhenTurnFinishes_ThenExpiryPersistedAndPublishedOnOwningMessage(t *testing.T) {
	scribe, mem, sink := segmentedApprovalTurn(t)

	scribe.finish(context.Background(), wire.RunResultDoneFrame{ConversationID: ctlTestConversation})

	assert.Contains(t, mem.persistedBody(21), `"expired"`, "the expiry is written back to the message that owns the card")
	assert.NotContains(t, mem.persistedBody(21), `"pending"`)
	resolved, ok := lastResolved(sink.snapshot())
	require.True(t, ok, "the expiry goes out as a durable tool_approval_resolved frame")
	assert.Equal(t, agentruntime.ToolApprovalResolved{RequestID: "req-seg", Status: "expired"}, resolved)
}

// 轮内 checkpoint 只编码、只写新增与被改动的块(累加器的增量写集合),与消息已有多少块无关;
// 一次写库失败不吞块:下一次 checkpoint 照样交出它们。
func TestTurnTranscript_GivenGrowingTurn_WhenCheckpointing_ThenOnlyChangedBlocksAreWritten(t *testing.T) {
	scribe, mem, _ := newApprovalTurn(t)
	ctx := context.Background()

	scribe.observe(ctx, agentruntime.ToolCall{ID: "tu-1", Name: "Bash"})
	scribe.observe(ctx, agentruntime.ToolResult{ToolCallID: "tu-1", Content: "ok"})
	scribe.observe(ctx, agentruntime.ToolCall{ID: "tu-2", Name: "Bash"})
	scribe.observe(ctx, agentruntime.ToolResult{ToolCallID: "tu-2", Content: "ok"})
	log := mem.upsertLog()
	require.NotEmpty(t, log)
	assert.Equal(t, memUpsert{messageID: 21, idx: []int{2, 3}, truncateFrom: -1}, log[len(log)-1],
		"the second tool round writes only its own two blocks")

	_, err := scribe.beginApproval(ctx, ctlCard("req-1"))
	require.NoError(t, err)
	scribe.resolveApproval(ctx, "req-1", "approved", "done")
	log = mem.upsertLog()
	assert.Equal(t, []int{4}, log[len(log)-1].idx, "resolving the card rewrites just the card")
	assert.Contains(t, mem.lastCheckpoint(), `"approved"`)

	mem.mu.Lock()
	mem.failUpserts = 1
	mem.mu.Unlock()
	scribe.observe(ctx, agentruntime.ToolCall{ID: "tu-3", Name: "Bash"})
	scribe.observe(ctx, agentruntime.ToolResult{ToolCallID: "tu-3", Content: "ok"})
	scribe.observe(ctx, agentruntime.ToolCall{ID: "tu-4", Name: "Bash"})
	scribe.observe(ctx, agentruntime.ToolResult{ToolCallID: "tu-4", Content: "ok"})
	log = mem.upsertLog()
	assert.Equal(t, []int{5, 6, 7, 8}, log[len(log)-1].idx, "blocks of a failed write are written by the next checkpoint")
}

// Given 一轮已 checkpoint 过两轮工具([tool_use, tool_result] ×2),收尾前又长出一段正文
// When  finish 收口
// Then  FinishTurn 只交新正文那一块,不整条重写已落库的工具块;收口正文与整条重写逐字一致
func TestTurnTranscript_GivenCheckpointedTurn_WhenFinishing_ThenOnlyChangedBlocksAreWritten(t *testing.T) {
	scribe, mem, _ := newApprovalTurn(t)
	ctx := context.Background()
	scribe.observe(ctx, agentruntime.ToolCall{ID: "tu-1", Name: "Bash"})
	scribe.observe(ctx, agentruntime.ToolResult{ToolCallID: "tu-1", Content: "ok"})
	scribe.observe(ctx, agentruntime.ToolCall{ID: "tu-2", Name: "Bash"})
	scribe.observe(ctx, agentruntime.ToolResult{ToolCallID: "tu-2", Content: "ok"})
	scribe.observe(ctx, agentruntime.TextDelta{Text: "done"})

	scribe.finish(ctx, wire.RunResultDoneFrame{ConversationID: ctlTestConversation})

	require.Len(t, mem.finishes, 1)
	assert.Equal(t, memUpsert{messageID: 21, idx: []int{4}, truncateFrom: -1}, mem.finishes[0])
	want := &transcript_entity.Message{}
	require.NoError(t, want.SetBlocks(scribe.acc.Finalize()))
	assert.Equal(t, want.BlocksJSON, mem.persistedBody(21))
}

// Given 一段已 checkpoint 过工具往返的 assistant,之后长出一段正文,插话到来
// When  插话把这一轮切段
// Then  SegmentTurn 收口前一段时只交新正文那一块,不整条重写
func TestTurnTranscript_GivenCheckpointedTurn_WhenSteerSegments_ThenOnlyChangedBlocksAreWritten(t *testing.T) {
	scribe, mem, _ := newApprovalTurn(t)
	mem.next = &transcript_entity.Message{ID: 23, SessionID: 12, Role: "assistant", Seq: 4, BlocksJSON: "[]"}
	ctx := context.Background()
	scribe.observe(ctx, agentruntime.ToolCall{ID: "tu-1", Name: "Bash"})
	scribe.observe(ctx, agentruntime.ToolResult{ToolCallID: "tu-1", Content: "ok"})
	scribe.observe(ctx, agentruntime.TextDelta{Text: "between"})

	scribe.observe(ctx, agentruntime.SteerConsumed{Steers: []agentruntime.ConsumedSteer{{QueuedID: "q1", Text: "顺便看下日志"}}})

	require.Equal(t, int64(23), scribe.msg.ID, "the steer segments the turn")
	require.Len(t, mem.segments, 1)
	assert.Equal(t, memUpsert{messageID: 21, idx: []int{2}, truncateFrom: -1}, mem.segments[0])
	assert.Contains(t, mem.persistedBody(21), `"between"`)
}

// Given 收口写库失败
// When  finish 收口
// Then  记一条 Error,带 sessionId / assistantMsgId / error(spec 2026-11-05 决策 7:轮末落库
//
//	失败一律记 Error 并带这两个 id)。
func TestTurnTranscript_GivenFinishPersistFails_ThenLogsErrorWithSessionAndAssistantIDs(t *testing.T) {
	logs := observeHandlerLogs(t)
	scribe, mem, _ := newApprovalTurn(t)
	mem.failFinish = errors.New("database is locked")
	ctx := context.Background()
	scribe.observe(ctx, agentruntime.TextDelta{Text: "answer"})

	scribe.finish(ctx, wire.RunResultDoneFrame{ConversationID: ctlTestConversation})

	entries := logs.FilterMessage("handlers.turnTranscript.finish: persist failed").All()
	require.Len(t, entries, 1)
	assert.Equal(t, zapcore.ErrorLevel, entries[0].Level)
	fields := entries[0].ContextMap()
	assert.Equal(t, int64(12), fields["sessionId"])
	assert.Equal(t, int64(21), fields["assistantMsgId"])
	assert.Equal(t, "database is locked", fields["error"])
}
