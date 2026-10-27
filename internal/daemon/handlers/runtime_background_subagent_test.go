package handlers_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 远端会话里 Claude Code 的原生后台子代理(Agent 默认后台)在第 N 轮派出去,它的完成
// 通知却是**之后**才到的:会话空闲时 CLI 另起一轮自主续轮(完成身份骑在
// AutonomousTurn.CompletedTask 上,那一帧本身不进事件流);落在别的轮里时则是一条
// SubagentDone 事件。两种情形下,派遣卡都住在第 N 轮那条早已收口的消息里。
//
// agentred 是这条转录的宿主:它落库的那张卡若停在 running,桌面端重连补齐、浏览器
// 控制台读到的就永远是「运行中」(Problem 7)。

const (
	bgAgentToolCall = "toolu_bg_agent"
	bgAgentTaskID   = "task-bg-1"
	bgAgentReport   = "子代理交回的报告"
)

// cardTranscript 是 TranscriptPort 的内存实现:每轮开一条新的 assistant 消息,按消息 id
// 记下最近一次落库的正文。它只存不改 —— 卡片怎么翻转全由被测代码决定。
type cardTranscript struct {
	mu       sync.Mutex
	nextID   int64
	seq      int64
	bodies   map[int64]string
	finished chan int64
}

func newCardTranscript() *cardTranscript {
	return &cardTranscript{bodies: map[int64]string{}, finished: make(chan int64, 8)}
}

func (c *cardTranscript) StartTurn(
	context.Context, string, string, []blocks.ContentBlock, transcript.UserSource,
) (*transcript_entity.Message, *transcript_entity.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	msg := &transcript_entity.Message{ID: c.nextID, SessionID: 5, Role: "assistant", Seq: int(c.nextID), BlocksJSON: "[]"}
	c.bodies[msg.ID] = msg.BlocksJSON
	return nil, msg, nil
}

func (c *cardTranscript) Checkpoint(_ context.Context, msg *transcript_entity.Message, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bodies[msg.ID] = msg.BlocksJSON
	return nil
}

func (c *cardTranscript) SegmentTurn(
	context.Context, *transcript_entity.Message, []agentruntime.ConsumedSteer,
) ([]*transcript_entity.Message, *transcript_entity.Message, error) {
	return nil, nil, nil
}

func (c *cardTranscript) FinishTurn(_ context.Context, msg *transcript_entity.Message) error {
	c.mu.Lock()
	c.bodies[msg.ID] = msg.BlocksJSON
	c.mu.Unlock()
	c.finished <- msg.ID
	return nil
}

func (c *cardTranscript) AllocateFrameSeqs(_ context.Context, _ int64, keys []transcript.FrameKey) ([]int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int64, len(keys))
	for i := range keys {
		c.seq++
		out[i] = c.seq
	}
	return out, nil
}

// FindSubagentCard 按落库正文找回承载这张卡的消息,交回一份**新**实体(与真仓储一样是
// 读出来的副本,被测代码改它不会绕过 Checkpoint 直接改到这里)。
func (c *cardTranscript) FindSubagentCard(_ context.Context, _, toolCallID string) (*transcript_entity.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, body := range c.bodies {
		var stored []blocks.StoredBlock
		if err := json.Unmarshal([]byte(body), &stored); err != nil {
			return nil, err
		}
		for _, sb := range stored {
			var head struct {
				Parent string `json:"parent_tool_call_id"`
			}
			if sb.Type == "subagent_state" && json.Unmarshal(sb.Data, &head) == nil && head.Parent == toolCallID {
				return &transcript_entity.Message{ID: id, SessionID: 5, Role: "assistant", Seq: int(id), BlocksJSON: body}, nil
			}
		}
	}
	return nil, nil
}

// card 解出 messageID 那条消息此刻落库的派遣卡;没有就 nil。
func (c *cardTranscript) card(t *testing.T, messageID int64, toolCallID string) *transcriptblocks.SubagentStateBlock {
	t.Helper()
	c.mu.Lock()
	body := c.bodies[messageID]
	c.mu.Unlock()
	bs, err := (&transcript_entity.Message{BlocksJSON: body}).GetBlocks()
	require.NoError(t, err)
	for _, b := range bs {
		var sb transcriptblocks.SubagentStateBlock
		switch v := b.(type) {
		case *transcriptblocks.SubagentStateBlock:
			sb = *v
		case transcriptblocks.SubagentStateBlock:
			sb = v
		default:
			continue
		}
		if sb.ParentToolCallID == toolCallID {
			return &sb
		}
	}
	return nil
}

func (c *cardTranscript) awaitFinished(t *testing.T) int64 {
	t.Helper()
	select {
	case id := <-c.finished:
		return id
	case <-time.After(5 * time.Second):
		t.Fatal("这一轮没有收口:FinishTurn 一直没被调到")
		return 0
	}
}

// backgroundAgentLaunch 是第 N 轮里派出一个后台 Agent 的那几帧:tool_use(Agent 不带
// run_in_background = 后台)→ task_started → 立即返回的 tool_result。
func backgroundAgentLaunch() []agentruntime.Event {
	return []agentruntime.Event{
		agentruntime.ToolCall{ID: bgAgentToolCall, Name: "Agent", Input: json.RawMessage(`{"description":"调研","prompt":"去查一下"}`)},
		agentruntime.SubagentStarted{ToolCallID: bgAgentToolCall, Info: agentruntime.SubagentInfo{
			TaskID: bgAgentTaskID, Kind: "local_agent", TaskDescription: "调研", Status: "running",
		}},
		agentruntime.ToolResult{ToolCallID: bgAgentToolCall, Content: "Async agent launched successfully."},
		agentruntime.Done{},
	}
}

// backgroundCardRig 跑完第 N 轮(派出后台 Agent 并收口),交回之后投递自主续轮的入口。
type backgroundCardRig struct {
	logs     *lockedLogBuffer
	store    *cardTranscript
	notif    *recordingOutbound
	autoCh   chan agentruntime.AutonomousTurn
	launchID int64
}

func runBackgroundAgentLaunchTurn(t *testing.T) *backgroundCardRig {
	t.Helper()
	captured := captureRuntimeLogs(t)
	rig := &backgroundCardRig{
		logs:   captured,
		store:  newCardTranscript(),
		notif:  newRecordingOutbound(),
		autoCh: make(chan agentruntime.AutonomousTurn, 1),
	}
	t.Cleanup(func() { close(rig.autoCh) })
	rt := &fullRT{}
	rt.runFn = func(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		launch := backgroundAgentLaunch()
		ch := make(chan agentruntime.Event, len(launch))
		for _, ev := range launch {
			ch <- ev
		}
		close(ch)
		return ch, &agentruntime.RunResult{}, nil
	}
	rt.autoFn = func(int64) <-chan agentruntime.AutonomousTurn { return rig.autoCh }
	sess := newRecordingSessions()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor: rig.notif.notifierFor, Sessions: sess, SessionQuery: sess,
		Transcript: rig.store, SubagentCards: rig.store,
		RuntimeFor: func(agent_backend_entity.BackendType) agentruntime.Runtime { return rt },
	})
	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode), Name: "x"}
	_, err := h.Run(context.Background(), &agentrewire.RuntimeRunRequest{
		Backend: backendProto(t, be), ConversationId: convID(42), AgentId: 7, Cwd: "/tmp", UserText: "派个后台子代理",
	})
	require.NoError(t, err)
	rig.launchID = rig.store.awaitFinished(t)
	// 收工日志落在终态帧之后;等到它,第 N 轮的 fanout 协程才算真的退场。
	require.Eventually(t, func() bool {
		return strings.Contains(captured.String(), "handlers.RuntimeHandlers.fanout: session ended")
	}, 2*time.Second, 10*time.Millisecond)

	// 前提:第 N 轮收口时后台卡如实停在 running(它有权活过发起它的那一轮)。
	launched := rig.store.card(t, rig.launchID, bgAgentToolCall)
	require.NotNil(t, launched, "第 N 轮应落下这张后台派遣卡")
	require.Equal(t, "running", launched.Status)
	return rig
}

// deliverAutonomousTurn 投递一轮自主续轮并等它收口。
func (r *backgroundCardRig) deliverAutonomousTurn(t *testing.T, completed *agentruntime.CompletedBackgroundTask, events ...agentruntime.Event) {
	t.Helper()
	evs := make(chan agentruntime.Event, len(events))
	for _, ev := range events {
		evs <- ev
	}
	close(evs)
	r.autoCh <- agentruntime.AutonomousTurn{
		Events: evs, Result: &agentruntime.RunResult{}, Trigger: "background_task", CompletedTask: completed,
	}
	autoID := r.store.awaitFinished(t)
	require.NotEqual(t, r.launchID, autoID, "自主续轮应开出自己的那条消息")
	// 收工日志落在终态帧之后;等到它,这一轮的转发协程才算真的退场(否则它的尾巴会与
	// 用例收尾时换回全局 logger 的那一下撞上)。
	require.Eventually(t, func() bool {
		return strings.Contains(r.logs.String(), "runtime.autonomousTurn: forwarded")
	}, 2*time.Second, 10*time.Millisecond)
}

// durableEvents 挑出推出去的持久帧(预览帧不算)。
func (r *backgroundCardRig) durableEvents() []agentruntime.Event {
	var out []agentruntime.Event
	for _, f := range r.notif.snapshot() {
		if f.method != wire.NotifyEvent {
			continue
		}
		if frame, ok := f.params.(*wire.EventFrame); ok && !frame.Preview {
			out = append(out, frame.Event)
		}
	}
	return out
}

// durableCardFrames 挑出推出去的、这张卡的持久帧(SubagentDone 投影)。
func (r *backgroundCardRig) durableCardFrames() []agentruntime.SubagentDone {
	var out []agentruntime.SubagentDone
	for _, ev := range r.durableEvents() {
		if done, ok := ev.(agentruntime.SubagentDone); ok && done.ToolCallID == bgAgentToolCall {
			out = append(out, done)
		}
	}
	return out
}

// durableLaunchCalls 数推出去的、派出这张卡的那次 Agent 调用的持久帧。它在第 N 轮收口时
// 已经发过一次;轮外改卡只该补发变了的那一帧,再发一次就是对端转录里多出一张卡。
func (r *backgroundCardRig) durableLaunchCalls() int {
	n := 0
	for _, ev := range r.durableEvents() {
		if call, ok := ev.(agentruntime.ToolCall); ok && call.ID == bgAgentToolCall {
			n++
		}
	}
	return n
}

// Given 第 N 轮派出的后台 Agent 已随那一轮收口落库(running)
// When 会话空闲时它完成了,CLI 起一轮自主续轮,完成身份骑在 CompletedTask 上
// Then 第 N 轮那条消息里落库的卡翻成 completed 并带上报告,且作为持久帧推给对端
func TestRuntime_GivenBackgroundAgentFromEarlierTurn_WhenAutonomousTurnCarriesItsCompletion_ThenStoredCardFinalizes(t *testing.T) {
	rig := runBackgroundAgentLaunchTurn(t)

	rig.deliverAutonomousTurn(t, &agentruntime.CompletedBackgroundTask{
		ToolCallID: bgAgentToolCall, TaskID: bgAgentTaskID, Status: "completed", Summary: bgAgentReport,
	}, agentruntime.TextDelta{Text: "子代理已完成"}, agentruntime.Done{})

	card := rig.store.card(t, rig.launchID, bgAgentToolCall)
	require.NotNil(t, card)
	assert.Equal(t, "completed", card.Status, "后台卡收不了尾:落库的派遣卡仍停在 running")
	assert.Equal(t, bgAgentReport, card.Summary)
	frames := rig.durableCardFrames()
	require.NotEmpty(t, frames)
	assert.Equal(t, "completed", frames[len(frames)-1].Info.Status, "翻转必须作为持久帧推给对端,否则补齐永远拿不到它")
	assert.Equal(t, 1, rig.durableLaunchCalls(), "那条消息里没变的帧不得重发")
}

// Given 同上
// When 自主续轮带来的完成身份指向一张这条会话里没有的卡(任务不属于它 / 从没建过卡)
// Then 这一轮照常收口,第 N 轮那张卡原样不动,也不凭空发出任何卡片帧
func TestRuntime_GivenBackgroundAgentFromEarlierTurn_WhenCompletionNamesAnUnknownCard_ThenNothingIsRewritten(t *testing.T) {
	rig := runBackgroundAgentLaunchTurn(t)
	before := len(rig.durableCardFrames())

	rig.deliverAutonomousTurn(t, &agentruntime.CompletedBackgroundTask{
		ToolCallID: "toolu_not_here", TaskID: "task-elsewhere", Status: "completed", Summary: "别处的报告",
	}, agentruntime.Done{})

	card := rig.store.card(t, rig.launchID, bgAgentToolCall)
	require.NotNil(t, card)
	assert.Equal(t, "running", card.Status)
	assert.Empty(t, card.Summary)
	assert.Len(t, rig.durableCardFrames(), before)
}

// Given 同上
// When 完成通知落进了之后的另一轮(CLI 把那一帧并进当前活跃轮,是一条 SubagentDone 事件)
// Then 同样翻到第 N 轮那张卡上,而不是因为本轮找不到它而静默丢弃
func TestRuntime_GivenBackgroundAgentFromEarlierTurn_WhenItsDoneFrameLandsInALaterTurn_ThenStoredCardFinalizes(t *testing.T) {
	rig := runBackgroundAgentLaunchTurn(t)

	rig.deliverAutonomousTurn(t, nil,
		agentruntime.TextDelta{Text: "另一件事"},
		agentruntime.SubagentDone{ToolCallID: bgAgentToolCall, Info: agentruntime.SubagentInfo{
			TaskID: bgAgentTaskID, Status: "failed", Summary: "中断原因",
		}},
		agentruntime.Done{},
	)

	card := rig.store.card(t, rig.launchID, bgAgentToolCall)
	require.NotNil(t, card)
	assert.Equal(t, "failed", card.Status, "后台卡收不了尾:落库的派遣卡仍停在 running")
	assert.Equal(t, "中断原因", card.Summary)
	frames := rig.durableCardFrames()
	require.NotEmpty(t, frames)
	assert.Equal(t, "failed", frames[len(frames)-1].Info.Status)
}
