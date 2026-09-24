package handlers

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
}

func (m *memTranscript) StartTurn(context.Context, string, string, []blocks.ContentBlock, transcript.UserSource) (*transcript_entity.Message, *transcript_entity.Message, error) {
	return nil, m.assistant, nil
}

func (m *memTranscript) Checkpoint(_ context.Context, msg *transcript_entity.Message, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkpoints = append(m.checkpoints, msg.BlocksJSON)
	return nil
}

func (m *memTranscript) SegmentTurn(context.Context, *transcript_entity.Message, []agentruntime.ConsumedSteer) ([]*transcript_entity.Message, *transcript_entity.Message, error) {
	return nil, nil, nil
}

func (m *memTranscript) FinishTurn(_ context.Context, msg *transcript_entity.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finished = append(m.finished, msg.BlocksJSON)
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
