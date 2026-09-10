package transcript_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
)

// Given 一段里夹着本仓认不出的转录块的持久化记录,When 折成对端的持久帧,Then 认不出
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
func TestProjectMessages_GivenPersistedBlocks_ThenForwardsUnrecognizedBlockVerbatim(t *testing.T) {
	t.Parallel()

	messages := []*transcript_entity.Message{
		{SessionID: 41, Role: "user", Seq: 1, BlocksJSON: `[{"type":"text","data":{"text":"ship it"}}]`},
		{SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: `[{"type":"thinking","data":{"text":"checking"}},{"type":"tool_use","data":{"id":"tool-1","name":"Read","input":{"path":"README.md"}}},{"type":"tool_result","data":{"tool_use_id":"tool-1","content":[{"type":"text","data":{"text":"ok"}}]}},{"type":"future_block","data":{"nested":{"keep":true}}}]`, ErrorText: "provider stopped"},
	}

	frames, _, err := transcript.ProjectMessages("conv-41", messages)
	require.NoError(t, err)

	kinds := make([]agentruntime.EventKind, 0, len(frames))
	for _, frame := range frames {
		kinds = append(kinds, projectedEventKind(t, frame.Event))
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
	}, frames[4].Event)

	// 每一帧都必须真能过协议边界 —— 从前那条伪造事件正是卡在这里,而当时没有
	// 任何用例走到这一步。
	for i, frame := range frames {
		_, err := protowire.WireNotificationToProto(wire.NotifyEvent, frame)
		require.NoErrorf(t, err, "第 %d 帧送不出去,整条实时流会被摘掉", i)
	}
}

// Given persisted final control-card state, when the shared projection folds it
// into frames, then it reconstructs both the card creation and its final update
// so the existing reducer can reach the stored readable state.
func TestProjectMessages_GivenFinalControlAndSnapshotBlocks_ThenEmitsReducerCompleteEvents(t *testing.T) {
	t.Parallel()

	messages := []*transcript_entity.Message{{
		SessionID: 41, Role: "assistant", Seq: 1, PromptTokens: 10, TotalInputTokens: 10,
		BlocksJSON: `[` +
			`{"type":"user_ask","data":{"request_id":"ask-1","tool_call_id":"tool-1","questions":[{"question":"continue?","options":[]}],"answered":true,"answers":[{"questionIndex":0,"labels":["yes"]}]}},` +
			`{"type":"tool_permission","data":{"request_id":"permission-1","tool_call_id":"tool-2","tool_name":"Bash","tool_input":{"command":"pwd"},"resolved":true,"allowed":true}},` +
			`{"type":"permission_mode_change","data":{"to":"plan"}},` +
			`{"type":"subagent_state","data":{"parent_tool_call_id":"agent-1","status":"completed","total_tokens":7,"model":"claude"}},` +
			`{"type":"plan","data":{"steps":[{"step":"inspect","status":"completed"}],"text":"# Plan"}}` +
			`]`,
	}}

	frames, _, err := transcript.ProjectMessages("conv-41", messages)
	require.NoError(t, err)
	kinds := make([]agentruntime.EventKind, 0, len(frames))
	for _, frame := range frames {
		kinds = append(kinds, projectedEventKind(t, frame.Event))
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

	// 计划卡的块类型归宿主（chat_svc.PlanBlock），投影只按 JSON 契约读它 ——
	// 所以这里要钉住读到的**内容**，光看帧的种类接不住「字段名读错了」。
	plan, ok := frames[7].Event.(agentruntime.PlanUpdated)
	require.True(t, ok)
	assert.Equal(t, "# Plan", plan.Plan.Text)
	assert.Equal(t, []canonical.PlanStep{{Step: "inspect", Status: canonical.StepCompleted}}, plan.Plan.Steps)
}

// Given 一条落库的助手消息带着本轮统计,When 折成帧,Then 收口的 Done 把它们一并带上
// —— 对端 Peer Tab 那一行 meta(模型 · 耗时 · 首字 · 速率)读的正是这几格。
func TestProjectMessages_GivenTurnStats_ThenDoneCarriesThem(t *testing.T) {
	t.Parallel()

	messages := []*transcript_entity.Message{
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

	frames, _, err := transcript.ProjectMessages("conv-41", messages)
	require.NoError(t, err)

	var done agentruntime.Done
	var found bool
	for _, frame := range frames {
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

// Given 两条时刻不同的消息,When 折成帧,Then 每一帧都配到它所属消息的 createtime ——
// 对端的转录才有 HH:mm 可显示。
func TestProjectMessages_CarriesEachMessagesCreatetime(t *testing.T) {
	t.Parallel()

	messages := []*transcript_entity.Message{
		{SessionID: 41, Role: "user", Seq: 1, Createtime: 1700000000111, BlocksJSON: `[{"type":"text","data":{"text":"ship it"}}]`},
		{SessionID: 41, Role: "assistant", Seq: 2, Createtime: 1700000009222, BlocksJSON: `[{"type":"thinking","data":{"text":"checking"}},{"type":"text","data":{"text":"done"}}]`},
	}

	frames, createtimes, err := transcript.ProjectMessages("conv-41", messages)
	require.NoError(t, err)
	require.Len(t, createtimes, len(frames), "每一帧都要有一个时刻,不能只有一部分")
	// user 一帧,assistant 两帧 + 收口的 done 一帧。
	require.Len(t, frames, 4)
	assert.Equal(t, []int64{1700000000111, 1700000009222, 1700000009222, 1700000009222}, createtimes)
}

// projectedEventKind 读出一条密封事件在 wire 上的判别值 —— 走的是事件自己的
// MarshalJSON,与真正推出去的那份字节同源。
func projectedEventKind(t *testing.T, event agentruntime.Event) agentruntime.EventKind {
	t.Helper()
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	var head struct {
		Kind agentruntime.EventKind `json:"kind"`
	}
	require.NoError(t, json.Unmarshal(raw, &head))
	return head.Kind
}
