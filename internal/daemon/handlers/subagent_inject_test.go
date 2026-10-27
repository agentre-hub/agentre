package handlers_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/ctlbind"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// 桌面端把 `agrctl send --wait` 子会话的事件经 runtime.injectSubagentEvents 交给 agentred,
// 由 agentred 写进它自己的父转录:父轮还在跑就写进在途那一轮(预览帧 + 持久帧),父轮已
// 结束就写回那条原消息(只发变了的持久帧)。

const (
	injShellCall = "toolu_sh"
	injChildTool = "child-bash-1"
)

// injectionEvents 是桌面端对一次 send 镜像出来的一批事件:绑定(Started)、子会话的一次
// 工具往返(经 ctlbind.NestChildEvent 转成嵌套事件)、进度、收尾。
func injectionEvents(t *testing.T, started agentruntime.SubagentInfo, withDone bool) []agentruntime.Event {
	t.Helper()
	evs := []agentruntime.Event{agentruntime.SubagentStarted{ToolCallID: injShellCall, Info: started}}
	for _, child := range []agentruntime.Event{
		agentruntime.ToolCall{ID: injChildTool, Name: "Bash", Input: json.RawMessage(`{"command":"ls"}`)},
		agentruntime.ToolResult{ToolCallID: injChildTool, Content: "a.txt"},
	} {
		nested, ok := ctlbind.NestChildEvent(injShellCall, child)
		require.True(t, ok)
		evs = append(evs, nested)
	}
	evs = append(evs, agentruntime.SubagentProgress{ToolCallID: injShellCall, Info: agentruntime.SubagentInfo{
		Origin: agentruntime.SubagentOriginAgrctl, ToolUses: 1, LastToolName: "Bash",
	}})
	if withDone {
		evs = append(evs, agentruntime.SubagentDone{ToolCallID: injShellCall, Info: agentruntime.SubagentInfo{
			Origin: agentruntime.SubagentOriginAgrctl, Status: "completed", FinalText: "child says hi",
		}})
	}
	return evs
}

func foregroundStarted() agentruntime.SubagentInfo {
	return agentruntime.SubagentInfo{
		Origin: agentruntime.SubagentOriginAgrctl, Kind: agentruntime.SubagentKindAgrctl,
		Status: "running", ChildSessionID: 9, TaskDescription: "do it",
	}
}

// injectRequest 走真实的 protobuf 线上形态:事件编成 RuntimeEventNotification,请求整条
// 序列化再反序列化一次,证明编解码往返不丢字段。
func injectRequest(t *testing.T, conversationID, toolCallID string, evs []agentruntime.Event) *agentrewire.RuntimeInjectSubagentEventsRequest {
	t.Helper()
	req := &agentrewire.RuntimeInjectSubagentEventsRequest{ConversationId: conversationID, ToolCallId: toolCallID}
	for _, ev := range evs {
		data, err := protowire.MarshalEventNotification(conversationID, 0, ev, false)
		require.NoError(t, err)
		var frame agentrewire.RpcFrame
		require.NoError(t, proto.Unmarshal(data, &frame))
		req.Events = append(req.Events, frame.GetNotification().GetRuntimeEvent())
	}
	raw, err := proto.Marshal(req)
	require.NoError(t, err)
	decoded := &agentrewire.RuntimeInjectSubagentEventsRequest{}
	require.NoError(t, proto.Unmarshal(raw, decoded))
	require.True(t, proto.Equal(req, decoded), "请求经 protobuf 往返后必须逐字段相等")
	return decoded
}

type injectRig struct {
	h     *handlers.RuntimeHandlers
	store *cardTranscript
	notif *recordingOutbound
	conv  string
	// events 是在途那一轮的事件入口(live 用例)。
	events chan agentruntime.Event
	msgID  int64
}

func (r *injectRig) frames() []*wire.EventFrame {
	var out []*wire.EventFrame
	for _, f := range r.notif.snapshot() {
		if f.method != wire.NotifyEvent {
			continue
		}
		if frame, ok := f.params.(*wire.EventFrame); ok {
			out = append(out, frame)
		}
	}
	return out
}

func (r *injectRig) durable() []agentruntime.Event {
	var out []agentruntime.Event
	for _, f := range r.frames() {
		if !f.Preview {
			out = append(out, f.Event)
		}
	}
	return out
}

func (r *injectRig) preview() []agentruntime.Event {
	var out []agentruntime.Event
	for _, f := range r.frames() {
		if f.Preview {
			out = append(out, f.Event)
		}
	}
	return out
}

func countNested(evs []agentruntime.Event) (calls, results int) {
	for _, ev := range evs {
		switch e := ev.(type) {
		case agentruntime.ToolCall:
			if e.ParentToolCallID == injShellCall {
				calls++
			}
		case agentruntime.ToolResult:
			if e.ParentToolCallID == injShellCall {
				results++
			}
		}
	}
	return calls, results
}

// storedTypeCount 数 msgID 那条消息此刻落库的某类块有几块。
func (c *cardTranscript) storedTypeCount(t *testing.T, messageID int64, blockType string) int {
	t.Helper()
	c.mu.Lock()
	body := c.bodies[messageID]
	c.mu.Unlock()
	var stored []blocks.StoredBlock
	require.NoError(t, json.Unmarshal([]byte(body), &stored))
	n := 0
	for _, sb := range stored {
		if sb.Type == blockType {
			n++
		}
	}
	return n
}

func newInjectHandlers(store *cardTranscript, notif *recordingOutbound, rt *fullRT) *handlers.RuntimeHandlers {
	sess := newRecordingSessions()
	return handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor: notif.notifierFor, Sessions: sess, SessionQuery: sess,
		Transcript: store, SubagentCards: store,
		Ctl:        handlers.NewCtlSessions(func() string { return "" }),
		RuntimeFor: func(agent_backend_entity.BackendType) agentruntime.Runtime { return rt },
	})
}

func runRequest(t *testing.T, conv string) *agentrewire.RuntimeRunRequest {
	t.Helper()
	be := agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode), Name: "x"}
	return &agentrewire.RuntimeRunRequest{
		Backend: backendProto(t, be), ConversationId: conv, AgentId: 7, Cwd: "/tmp", UserText: "跑 agrctl send",
	}
}

// liveShellRig 起一轮并让它停在「父 shell 调用已发出、结果未回」:这是 `agrctl send --wait`
// 阻塞期间父转录的样子。
func liveShellRig(t *testing.T, command string, background bool) *injectRig {
	t.Helper()
	rig := &injectRig{
		store: newCardTranscript(), notif: newRecordingOutbound(),
		conv: convID(42), events: make(chan agentruntime.Event, 16), msgID: 1,
	}
	rt := &fullRT{}
	rt.runFn = func(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
		return rig.events, &agentruntime.RunResult{}, nil
	}
	rig.h = newInjectHandlers(rig.store, rig.notif, rt)
	_, err := rig.h.Run(context.Background(), runRequest(t, rig.conv))
	require.NoError(t, err)
	input, err := json.Marshal(map[string]any{"command": command, "run_in_background": background})
	require.NoError(t, err)
	rig.events <- agentruntime.ToolCall{ID: injShellCall, Name: "Bash", Input: input}
	// 预览帧在 observe 之后才推:看到它,父轮的累加器里就已经有这条 shell 调用了。
	require.Eventually(t, func() bool {
		for _, ev := range rig.preview() {
			if call, ok := ev.(agentruntime.ToolCall); ok && call.ID == injShellCall {
				return true
			}
		}
		return false
	}, 2*time.Second, 5*time.Millisecond)
	return rig
}

// finish 让在途那一轮收口并等它落完。
func (r *injectRig) finish(t *testing.T) {
	t.Helper()
	r.events <- agentruntime.Done{}
	close(r.events)
	r.store.awaitFinished(t)
}

// Given 父轮还在跑,agent 的 `agrctl send --wait` shell 调用正阻塞着
// When 桌面端把子会话的绑定、一次工具往返、进度与收尾交给 agentred
// Then 它们写进在途转录并落库:卡片归属 agrctl、带子会话与最终文本,嵌套步骤成块;同时
// 以预览帧和持久帧推给对端
func TestRuntime_GivenLiveParentTurn_WhenSubagentEventsInjected_ThenPersistedAndPushedAsPreviewAndDurableFrames(t *testing.T) {
	rig := liveShellRig(t, "agrctl send --wait agent hello", false)
	evs := injectionEvents(t, foregroundStarted(), true)

	_, err := rig.h.InjectSubagentEvents(context.Background(), injectRequest(t, rig.conv, injShellCall, evs))
	require.NoError(t, err)

	card := rig.store.card(t, rig.msgID, injShellCall)
	require.NotNil(t, card, "注入的 Started 必须在父转录里落下 overlay 卡并 checkpoint")
	assert.Equal(t, agentruntime.SubagentOriginAgrctl, card.Origin)
	assert.Equal(t, int64(9), card.ChildSessionID)
	assert.Equal(t, "completed", card.Status)
	assert.Equal(t, "child says hi", card.FinalText)
	assert.Equal(t, 1, rig.store.storedTypeCount(t, rig.msgID, (transcriptblocks.NestedToolUseBlock{}).Type()), "子会话的工具调用要成一块嵌套步骤")
	assert.Equal(t, 1, rig.store.storedTypeCount(t, rig.msgID, (transcriptblocks.NestedToolResultBlock{}).Type()))

	previewCalls, previewResults := countNested(rig.preview())
	assert.Equal(t, 1, previewCalls, "注入的事件要以预览帧推给对端")
	assert.Equal(t, 1, previewResults)
	var previewDone bool
	for _, ev := range rig.preview() {
		if d, ok := ev.(agentruntime.SubagentDone); ok && d.ToolCallID == injShellCall {
			previewDone = true
		}
	}
	assert.True(t, previewDone, "收尾也要有预览帧")

	durableCalls, durableResults := countNested(rig.durable())
	assert.Equal(t, 1, durableCalls, "嵌套步骤要以持久帧推给对端(带号,补齐能重放)")
	assert.Equal(t, 1, durableResults)

	rig.finish(t)
}

// Given 同上,桌面端因重试把同一批事件交来第二遍
// When 再次注入
// Then 卡片的嵌套步骤仍只有一份,也不重复推持久帧
func TestRuntime_GivenLiveParentTurn_WhenSameBatchInjectedTwice_ThenNestedStepsAreNotDuplicated(t *testing.T) {
	rig := liveShellRig(t, "agrctl send --wait agent hello", false)
	req := injectRequest(t, rig.conv, injShellCall, injectionEvents(t, foregroundStarted(), true))

	_, err := rig.h.InjectSubagentEvents(context.Background(), req)
	require.NoError(t, err)
	durableAfterFirst := len(rig.durable())
	_, err = rig.h.InjectSubagentEvents(context.Background(), req)
	require.NoError(t, err)

	assert.Equal(t, 1, rig.store.storedTypeCount(t, rig.msgID, (transcriptblocks.NestedToolUseBlock{}).Type()))
	assert.Equal(t, 1, rig.store.storedTypeCount(t, rig.msgID, (transcriptblocks.NestedToolResultBlock{}).Type()))
	calls, results := countNested(rig.durable())
	assert.Equal(t, 1, calls)
	assert.Equal(t, 1, results)
	assert.Equal(t, durableAfterFirst, len(rig.durable()), "重复注入不得多发任何持久帧")

	rig.finish(t)
}

// Given 父轮在跑
// When 注入点名了一个父转录里没有的 toolCallId
// Then 不报错、不写任何块、不推任何帧(桌面端不能因为找不到卡就让子会话失败)
func TestRuntime_GivenLiveParentTurn_WhenInjectionNamesUnknownToolCall_ThenSilentNoOp(t *testing.T) {
	rig := liveShellRig(t, "agrctl send --wait agent hello", false)
	before := len(rig.frames())
	evs := injectionEvents(t, foregroundStarted(), true)

	_, err := rig.h.InjectSubagentEvents(context.Background(), injectRequest(t, rig.conv, "toolu_nope", evs))

	require.NoError(t, err)
	assert.Nil(t, rig.store.card(t, rig.msgID, "toolu_nope"))
	assert.Nil(t, rig.store.card(t, rig.msgID, injShellCall))
	assert.Equal(t, 0, rig.store.storedTypeCount(t, rig.msgID, (transcriptblocks.NestedToolUseBlock{}).Type()))
	assert.Len(t, rig.frames(), before)

	rig.finish(t)
}

// Given 会话里根本没有在跑的轮,也没有这张卡
// When 注入
// Then 同样是不报错的空操作
func TestRuntime_GivenNoTurnAndNoCard_WhenSubagentEventsInjected_ThenSilentNoOp(t *testing.T) {
	store, notif := newCardTranscript(), newRecordingOutbound()
	h := newInjectHandlers(store, notif, &fullRT{})

	_, err := h.InjectSubagentEvents(context.Background(),
		injectRequest(t, convID(7), injShellCall, injectionEvents(t, foregroundStarted(), true)))

	require.NoError(t, err)
	assert.Empty(t, notif.snapshot())
}

// Given 父轮是 Claude 后台 shell(run_in_background),卡片随那一轮收口落库(running)
// When 父轮结束之后桌面端才把子会话的事件交来
// Then 事件写回原消息:嵌套步骤追加、overlay 补上归属与最终文本;只发变了的持久帧
func TestRuntime_GivenParentTurnAlreadyEnded_WhenSubagentEventsInjected_ThenOriginalMessageIsPatched(t *testing.T) {
	rig := liveShellRig(t, "agrctl send --wait agent hello", true)
	// 后台 shell:CLI 自己发的 local_bash 卡,随后 tool_result 立即回来,轮收口。
	rig.events <- agentruntime.SubagentStarted{ToolCallID: injShellCall, Info: agentruntime.SubagentInfo{
		TaskID: "bash-1", Kind: agentruntime.SubagentKindLocalBash, TaskDescription: "agrctl send", Status: "running",
	}}
	rig.events <- agentruntime.ToolResult{ToolCallID: injShellCall, Content: "Command running in background"}
	rig.finish(t)
	launched := rig.store.card(t, rig.msgID, injShellCall)
	require.NotNil(t, launched)
	require.Equal(t, "running", launched.Status)
	launchCalls := 0
	for _, ev := range rig.durable() {
		if c, ok := ev.(agentruntime.ToolCall); ok && c.ID == injShellCall {
			launchCalls++
		}
	}
	require.Equal(t, 1, launchCalls)

	_, err := rig.h.InjectSubagentEvents(context.Background(),
		injectRequest(t, rig.conv, injShellCall, injectionEvents(t, agentruntime.SubagentInfo{
			Origin: agentruntime.SubagentOriginAgrctl, Kind: agentruntime.SubagentKindLocalBash,
			Status: "running", ChildSessionID: 9,
		}, true)))
	require.NoError(t, err)

	card := rig.store.card(t, rig.msgID, injShellCall)
	require.NotNil(t, card)
	assert.Equal(t, agentruntime.SubagentOriginAgrctl, card.Origin)
	assert.Equal(t, int64(9), card.ChildSessionID)
	assert.Equal(t, "child says hi", card.FinalText)
	assert.Equal(t, "running", card.Status, "后台卡的状态归 CLI,注入方不能替它收尾")
	assert.Equal(t, 1, rig.store.storedTypeCount(t, rig.msgID, (transcriptblocks.NestedToolUseBlock{}).Type()), "嵌套步骤追加进原消息")
	assert.Equal(t, 1, rig.store.storedTypeCount(t, rig.msgID, (transcriptblocks.NestedToolResultBlock{}).Type()))
	calls, results := countNested(rig.durable())
	assert.Equal(t, 1, calls, "轮外写回要把嵌套步骤作为持久帧推出去")
	assert.Equal(t, 1, results)
	launchCalls = 0
	for _, ev := range rig.durable() {
		if c, ok := ev.(agentruntime.ToolCall); ok && c.ID == injShellCall {
			launchCalls++
		}
	}
	assert.Equal(t, 1, launchCalls, "那条消息里没变的帧不得重发")

	// 重复投递:幂等,不再追加、不再发帧。
	durable := len(rig.durable())
	_, err = rig.h.InjectSubagentEvents(context.Background(),
		injectRequest(t, rig.conv, injShellCall, injectionEvents(t, agentruntime.SubagentInfo{
			Origin: agentruntime.SubagentOriginAgrctl, Kind: agentruntime.SubagentKindLocalBash,
			Status: "running", ChildSessionID: 9,
		}, true)))
	require.NoError(t, err)
	assert.Equal(t, 1, rig.store.storedTypeCount(t, rig.msgID, (transcriptblocks.NestedToolUseBlock{}).Type()))
	assert.Equal(t, durable, len(rig.durable()))
}

// Given 父轮在跑
// When 批里混进了注入方无权写的事件(文本、没有 agrctl 归属的 Subagent 帧、挂在别的调用下的嵌套步骤)
// Then 只有合规的那几条落进转录,其余被丢弃
func TestRuntime_GivenLiveParentTurn_WhenBatchCarriesEventsInjectorMayNotWrite_ThenTheyAreDropped(t *testing.T) {
	rig := liveShellRig(t, "agrctl send --wait agent hello", false)
	foreign, ok := ctlbind.NestChildEvent("toolu_other", agentruntime.ToolCall{ID: "x", Name: "Bash"})
	require.True(t, ok)
	evs := []agentruntime.Event{
		agentruntime.TextDelta{Text: "伪造的正文"},
		agentruntime.SubagentStarted{ToolCallID: injShellCall, Info: agentruntime.SubagentInfo{Kind: agentruntime.SubagentKindAgrctl, Status: "running"}},
		foreign,
	}

	_, err := rig.h.InjectSubagentEvents(context.Background(), injectRequest(t, rig.conv, injShellCall, evs))

	require.NoError(t, err)
	assert.Nil(t, rig.store.card(t, rig.msgID, injShellCall), "没有 agrctl 归属的 Subagent 帧不得建卡")
	assert.Equal(t, 0, rig.store.storedTypeCount(t, rig.msgID, (transcriptblocks.NestedToolUseBlock{}).Type()))
	for _, ev := range rig.preview() {
		_, isText := ev.(agentruntime.TextDelta)
		assert.False(t, isText)
	}

	rig.finish(t)
}
