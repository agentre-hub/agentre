package chat_svc

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
)

// tool_approval_transcript_test.go 钉住桌面端拥有的会话里,写工具审批卡(ctl / org
// 共用 BeginToolApproval/FinishToolApproval)**在挂起时**就进入这一轮的转录:
// 落库(checkpoint)并作为持久帧发给对端(控制台经 relay attach/pull 只看得到这两样),
// 决议同样如此;卡片停在命令发生处,不被挪到这一轮末尾;桌面端自己的流事件照旧。
//
// 驱动的是生产的 runTurn(事件循环 + finalize),不是手拼的 turnRun:审批是另一条
// goroutine(工具 MCP handler)打进来的,它与事件循环怎么交错正是要守的东西。

// approvalTurnRig 一轮可控的桌面端本机 turn:事件由用例逐条喂,库与对端都录着。
type approvalTurnRig struct {
	t          *testing.T
	deps       *peerSessionTestDeps
	svc        *chatSvc
	emitter    *syncRecordEmitter
	subscriber *peerRecordingSubscriber
	events     chan agentruntime.Event
	done       chan struct{}

	mu          sync.Mutex
	checkpoints []string // 每次 CheckpointBlocks 落库时 assistant 的 BlocksJSON
	finalBlocks string   // 收口 Update 落库的 assistant BlocksJSON
}

// syncRecordEmitter 是 recordEmitter 的并发安全版:事件循环与审批 goroutine 同时 Emit。
type syncRecordEmitter struct {
	mu       sync.Mutex
	payloads []any
}

func (r *syncRecordEmitter) Emit(_ context.Context, _ string, payload any) {
	r.mu.Lock()
	r.payloads = append(r.payloads, payload)
	r.mu.Unlock()
}

func (r *syncRecordEmitter) toolApprovalEvents() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []map[string]any
	for _, p := range r.payloads {
		if m, ok := p.(map[string]any); ok && m["kind"] == "tool_approval" {
			out = append(out, m)
		}
	}
	return out
}

func (r *syncRecordEmitter) sawKind(kind ChatStreamEventKind) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range r.payloads {
		if ev, ok := p.(ChatStreamEvent); ok && ev.Kind == kind {
			return true
		}
	}
	return false
}

type approvalFakeRuntime struct{}

func (approvalFakeRuntime) Capabilities() capability.Capabilities { return capability.Capabilities{} }
func (approvalFakeRuntime) Run(context.Context, agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	return nil, nil, nil
}

const approvalSessionID = int64(41)

func startApprovalTurn(t *testing.T) *approvalTurnRig {
	t.Helper()
	deps := setupPeerSessionTest(t)
	rig := &approvalTurnRig{
		t: t, deps: deps, emitter: &syncRecordEmitter{},
		subscriber: newRecordingPeerSubscriber(),
		events:     make(chan agentruntime.Event),
		done:       make(chan struct{}),
	}
	deps.svc.emitter = rig.emitter
	rig.svc = deps.svc
	ctx := context.Background()

	deps.session.EXPECT().Find(gomock.Any(), approvalSessionID).DoAndReturn(
		func(context.Context, int64) (*chat_entity.Session, error) {
			return &chat_entity.Session{ID: approvalSessionID, AgentID: 7, AgentStatus: "running", ConversationID: convID(approvalSessionID)}, nil
		}).AnyTimes()
	deps.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	deps.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(gomock.Any(), int64(11)).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().List(gomock.Any(), approvalSessionID).Return(nil, nil).AnyTimes()
	deps.message.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, m *chat_entity.Message, _ string) error {
			rig.mu.Lock()
			rig.checkpoints = append(rig.checkpoints, m.BlocksJSON)
			rig.mu.Unlock()
			return nil
		}).AnyTimes()
	deps.message.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, m *chat_entity.Message) error {
			rig.mu.Lock()
			rig.finalBlocks = m.BlocksJSON
			rig.mu.Unlock()
			return nil
		}).AnyTimes()

	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(approvalSessionID)}, rig.subscriber)
	require.NoError(t, err)

	sess := &chat_entity.Session{ID: approvalSessionID, AgentID: 7, ConversationID: convID(approvalSessionID), AgentStatus: "running"}
	userMsg := &chat_entity.Message{ID: 9000, SessionID: approvalSessionID, Role: "user", Seq: 1, BlocksJSON: "[]"}
	assistant := &chat_entity.Message{ID: 9001, SessionID: approvalSessionID, Role: "assistant", Seq: 2, BlocksJSON: "[]"}
	prepared := &preparedTurnRun{runner: approvalFakeRuntime{}, events: rig.events, release: func() {}}
	go func() {
		defer close(rig.done)
		deps.svc.runTurn(ctx, sess, &agent_entity.Agent{ID: 7, AgentBackendID: 11},
			&agent_backend_entity.AgentBackend{ID: 11, Type: string(agent_backend_entity.TypeClaudeCode)}, nil,
			userMsg, assistant, StreamName(approvalSessionID, 9001), "", false, prepared, turnExtras{})
	}()
	return rig
}

// feed 把一条事件交给事件循环,并等到它的流事件已经发出(即这一条已被处理)。
func (r *approvalTurnRig) feed(ev agentruntime.Event, emitted ChatStreamEventKind) {
	r.t.Helper()
	r.events <- ev
	require.Eventually(r.t, func() bool { return r.emitter.sawKind(emitted) }, 2*time.Second, time.Millisecond)
}

func (r *approvalTurnRig) endTurn() {
	r.t.Helper()
	close(r.events)
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		r.t.Fatal("turn did not finish")
	}
}

type storedBlock struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func decodeStored(t *testing.T, raw string) []storedBlock {
	t.Helper()
	var out []storedBlock
	require.NoError(t, json.Unmarshal([]byte(raw), &out), raw)
	return out
}

func (r *approvalTurnRig) lastCheckpoint() []storedBlock {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.checkpoints) == 0 {
		return nil
	}
	var out []storedBlock
	_ = json.Unmarshal([]byte(r.checkpoints[len(r.checkpoints)-1]), &out)
	return out
}

func approvalsIn(t *testing.T, stored []storedBlock) []blocks.ToolApprovalBlock {
	t.Helper()
	var out []blocks.ToolApprovalBlock
	for _, b := range stored {
		if b.Type != "tool_approval" {
			continue
		}
		var data blocks.ToolApprovalBlock
		require.NoError(t, json.Unmarshal(b.Data, &data))
		out = append(out, data)
	}
	return out
}

func blockTypes(stored []storedBlock) []string {
	out := make([]string, 0, len(stored))
	for _, b := range stored {
		out = append(out, b.Type)
	}
	return out
}

// durableFrames 交回对端此刻收到的持久帧事件(预览帧不算:它不进转录、不参与补齐)。
func (r *approvalTurnRig) durableFrames() []agentruntime.Event {
	var out []agentruntime.Event
	for _, rec := range r.subscriber.notifications() {
		frame, ok := rec.params.(wire.EventFrame)
		if !ok || frame.Preview {
			continue
		}
		out = append(out, frame.Event)
	}
	return out
}

func (r *approvalTurnRig) sawDurable(match func(agentruntime.Event) bool) func() bool {
	return func() bool {
		for _, ev := range r.durableFrames() {
			if match(ev) {
				return true
			}
		}
		return false
	}
}

func approvalRequested(requestID string) func(agentruntime.Event) bool {
	return func(ev agentruntime.Event) bool {
		got, ok := ev.(agentruntime.ToolApprovalRequested)
		return ok && got.RequestID == requestID
	}
}

func approvalResolved(requestID, status string) func(agentruntime.Event) bool {
	return func(ev agentruntime.Event) bool {
		got, ok := ev.(agentruntime.ToolApprovalResolved)
		return ok && got.RequestID == requestID && got.Status == status
	}
}

// Given 桌面端本机的一轮正在跑,agent 刚调了 agrctl 写命令(ctl 写工具的 tool_use 已到);
// When  ctl 写工具登记一张 pending 审批卡,随后被批准、这一轮继续产出正文后收口;
// Then  卡在挂起时就已落库并作为持久帧发给对端(控制台才看得到、答得了),
//
//	批准同样落库并发出决议帧;桌面端流事件照旧;收口落库的转录里卡恰好一张、
//	停在 tool_use 之后、后续正文之前。
func TestToolApproval_GivenActiveTurn_WhenCardIsPendingThenApproved_ThenTranscriptCarriesItInPlace(t *testing.T) {
	rig := startApprovalTurn(t)
	ctx := context.Background()

	rig.feed(agentruntime.TextDelta{Text: "before"}, StreamChunk)
	rig.feed(agentruntime.ToolCall{ID: "tu-1", Name: "Bash", Input: json.RawMessage(`{"command":"agrctl update agent a1"}`)}, StreamToolUse)

	card := &blocks.ToolApprovalBlock{
		ToolKey: "ctl", RequestID: "ctl-req-1", ToolName: "ctl",
		ToolInput: map[string]any{"verb": "update"}, Status: "pending",
	}
	ch, err := rig.svc.BeginToolApproval(ctx, approvalSessionID, card)
	require.NoError(t, err)
	require.NotNil(t, ch)

	// 挂起期间:已落库 + 已作为持久帧发出。
	pending := approvalsIn(t, rig.lastCheckpoint())
	require.Len(t, pending, 1, "pending 的审批卡必须在挂起时就 checkpoint 落库,控制台 reload 读的是库")
	assert.Equal(t, "ctl-req-1", pending[0].RequestID)
	assert.Equal(t, "pending", pending[0].Status)
	require.Eventually(t, rig.sawDurable(approvalRequested("ctl-req-1")), 2*time.Second, time.Millisecond,
		"pending 的审批卡必须作为持久帧发给对端,控制台在挂起期间才画得出来")
	// 桌面端自己的流事件照旧。
	require.Len(t, rig.emitter.toolApprovalEvents(), 1)
	assert.Equal(t, "pending", rig.emitter.toolApprovalEvents()[0]["status"])

	// 控制台 / 桌面端作答 → 决议。
	require.NoError(t, rig.svc.AnswerToolApproval(ctx, approvalSessionID, "ctl-req-1", true))
	assert.True(t, <-ch)
	require.NoError(t, rig.svc.FinishToolApproval(ctx, approvalSessionID, "ctl-req-1", "approved", "updated agent a1"))

	resolved := approvalsIn(t, rig.lastCheckpoint())
	require.Len(t, resolved, 1)
	assert.Equal(t, "approved", resolved[0].Status, "决议必须落库")
	assert.Equal(t, "updated agent a1", resolved[0].Result)
	require.Eventually(t, rig.sawDurable(approvalResolved("ctl-req-1", "approved")), 2*time.Second, time.Millisecond,
		"决议必须作为持久帧发给对端")
	require.Len(t, rig.emitter.toolApprovalEvents(), 2)
	assert.Equal(t, "approved", rig.emitter.toolApprovalEvents()[1]["status"])

	rig.feed(agentruntime.ToolResult{ToolCallID: "tu-1", Content: "ok"}, StreamToolResult)
	rig.feed(agentruntime.TextDelta{Text: "after"}, StreamChunk)
	rig.endTurn()

	rig.mu.Lock()
	final := decodeStored(t, rig.finalBlocks)
	rig.mu.Unlock()
	approvals := approvalsIn(t, final)
	require.Len(t, approvals, 1, "卡在转录里只有一张:不得在收口时再追加一份")
	assert.Equal(t, "approved", approvals[0].Status)
	assert.Equal(t, []string{"text", "tool_use", "tool_approval", "tool_result", "text"}, blockTypes(final),
		"卡停在命令发生处,不被挪到这一轮末尾")
}

// Given 同一轮里先后挂起两张审批卡(ctl 与 org 走同一条路);
// When  ctl 那张被拒绝,org 那张一直没人答、这一轮就收口了;
// Then  两张卡各一张、按登记先后留在原处;拒绝落库,挂起的那张收口时落成 expired 并发出
//
//	决议帧;收口之后再作答 / 再决议 / 再登记一律报错(卡已不可决)。
func TestToolApproval_GivenTwoCardsInOneTurn_WhenTurnEndsWhilePending_ThenPendingOneExpiresInPlace(t *testing.T) {
	rig := startApprovalTurn(t)
	ctx := context.Background()

	rig.feed(agentruntime.TextDelta{Text: "working"}, StreamChunk)
	ctlCard := &blocks.ToolApprovalBlock{ToolKey: "ctl", RequestID: "ctl-req-2", ToolName: "ctl", Status: "pending"}
	orgCard := &blocks.ToolApprovalBlock{ToolKey: "org", RequestID: "org-req-2", ToolName: "org_invite", Status: "pending"}
	_, err := rig.svc.BeginToolApproval(ctx, approvalSessionID, ctlCard)
	require.NoError(t, err)
	_, err = rig.svc.BeginToolApproval(ctx, approvalSessionID, orgCard)
	require.NoError(t, err)

	require.Len(t, approvalsIn(t, rig.lastCheckpoint()), 2)
	require.Eventually(t, rig.sawDurable(approvalRequested("org-req-2")), 2*time.Second, time.Millisecond)

	require.NoError(t, rig.svc.FinishToolApproval(ctx, approvalSessionID, "ctl-req-2", "denied", ""))
	require.Eventually(t, rig.sawDurable(approvalResolved("ctl-req-2", "denied")), 2*time.Second, time.Millisecond)

	rig.feed(agentruntime.TextDelta{Text: "tail"}, StreamChunk)
	rig.endTurn()

	rig.mu.Lock()
	final := decodeStored(t, rig.finalBlocks)
	rig.mu.Unlock()
	assert.Equal(t, []string{"text", "tool_approval", "tool_approval", "text"}, blockTypes(final))
	approvals := approvalsIn(t, final)
	require.Len(t, approvals, 2)
	assert.Equal(t, "ctl-req-2", approvals[0].RequestID)
	assert.Equal(t, "denied", approvals[0].Status)
	assert.Equal(t, "org-req-2", approvals[1].RequestID)
	assert.Equal(t, "expired", approvals[1].Status, "收口时仍挂起的卡落成 expired")
	require.Eventually(t, rig.sawDurable(approvalResolved("org-req-2", "expired")), 2*time.Second, time.Millisecond,
		"过期同样是决议,要发给对端,否则控制台上那张卡永远 pending")

	assert.Error(t, rig.svc.AnswerToolApproval(ctx, approvalSessionID, "org-req-2", true), "收口之后卡已不可答")
	assert.Error(t, rig.svc.FinishToolApproval(ctx, approvalSessionID, "org-req-2", "approved", ""), "收口之后不得改写已过期的卡")
	_, err = rig.svc.BeginToolApproval(ctx, approvalSessionID, &blocks.ToolApprovalBlock{ToolKey: "ctl", RequestID: "ctl-late", Status: "pending"})
	assert.Error(t, err, "没有在跑的一轮,卡无处可落")
}
