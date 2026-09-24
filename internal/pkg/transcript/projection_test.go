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
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
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
// TestProjectMessages_GivenUserImageBlock_ThenProjectsAnImageEvent 钉住一条带图的
// 用户消息投影出什么。
//
// 它此前落 R8 兜底(UnrecognizedBlock):EventForStoredBlock 的 role==user 支只认
// text / display_text。兜底本身没错 —— 它保住了字节 —— 但消费方那一侧只知道「有一块
// 读不懂的东西」,既不知道这是图,也不知道它是用户贴的,于是画成助手名下的一段 base64。
//
// 块序照两个宿主落库的那一份:先文本、后附件。
func TestProjectMessages_GivenUserImageBlock_ThenProjectsAnImageEvent(t *testing.T) {
	t.Parallel()

	messages := []*transcript_entity.Message{
		{SessionID: 42, Role: "user", Seq: 1, BlocksJSON: `[{"type":"text","data":{"text":"这张图哪里不对"}},{"type":"image","data":{"media_type":"image/png","source":{"inline":"AQID"}}}]`},
	}

	frames, _, err := transcript.ProjectMessages("conv-42", messages)
	require.NoError(t, err)
	require.Len(t, frames, 2)

	assert.Equal(t, agentruntime.EventUserMessage, projectedEventKind(t, frames[0].Event))
	assert.Equal(t, agentruntime.EventImage, projectedEventKind(t, frames[1].Event))
	// 字节不重新编码:块里存的是 base64("AQID" = 0x01 0x02 0x03),Go 侧 []byte 解出来
	// 就是那三个字节,过 wire 时再由 protobuf 的 bytes 原样带走。
	assert.Equal(t, agentruntime.ImageBlockEvent{
		MediaType: "image/png",
		Inline:    []byte{0x01, 0x02, 0x03},
	}, frames[1].Event)
}

// TestProjectMessages_GivenImageBlockWithoutBytes_ThenStillProjectsTheBlock 两格来源
// 都空是合法的坏数据:画不出图,但**不静默跳过** —— 转录里凭空少一块比一块画不出来
// 更难解释,而消费方拿到这一帧才说得出「这里有一张取不到的图」。
func TestProjectMessages_GivenImageBlockWithoutBytes_ThenStillProjectsTheBlock(t *testing.T) {
	t.Parallel()

	messages := []*transcript_entity.Message{
		{SessionID: 43, Role: "user", Seq: 1, BlocksJSON: `[{"type":"image","data":{"media_type":"image/jpeg","source":{}}}]`},
	}

	frames, _, err := transcript.ProjectMessages("conv-43", messages)
	require.NoError(t, err)
	require.Len(t, frames, 1)
	assert.Equal(t, agentruntime.ImageBlockEvent{MediaType: "image/jpeg"}, frames[0].Event)
}

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

// Given 一张落库的 tool_approval 卡(org / ctl 这些 agent 内置写工具的服务端审批),
// When 折成对端的持久帧,Then 它是一张独立的审批卡帧,而不是 unrecognized_block ——
// 控制台与远端视图靠这一帧画出审批卡;终态(approved / denied / expired)再补一帧
// 决议回填同一张卡,与 tool_permission 的「请求 + 决议」同一形态。
func TestProjectMessages_GivenToolApprovalBlock_ThenProjectsApprovalFrames(t *testing.T) {
	t.Parallel()

	input := `{"command":"agrctl update provider openrouter --base-url https://x","changes":[{"op":"update","kind":"provider","id":4}]}`
	block := func(status, result string) string {
		data := `{"tool_key":"ctl","request_id":"ctl-1","tool_name":"ctl_update_provider","tool_input":` + input + `,"status":"` + status + `"`
		if result != "" {
			data += `,"result":"` + result + `"`
		}
		return `[{"type":"tool_approval","data":` + data + `}}]`
	}
	requested := agentruntime.ToolApprovalRequested{
		ToolKey: "ctl", RequestID: "ctl-1", ToolName: "ctl_update_provider", ToolInput: json.RawMessage(input),
	}

	cases := []struct {
		name   string
		status string
		result string
		want   []agentruntime.Event
	}{
		{name: "pending 只有请求帧", status: "pending", want: []agentruntime.Event{requested}},
		{name: "approved 带执行结果", status: "approved", result: "updated provider #4", want: []agentruntime.Event{
			requested, agentruntime.ToolApprovalResolved{RequestID: "ctl-1", Status: "approved", Result: "updated provider #4"},
		}},
		{name: "denied", status: "denied", want: []agentruntime.Event{
			requested, agentruntime.ToolApprovalResolved{RequestID: "ctl-1", Status: "denied"},
		}},
		{name: "expired", status: "expired", want: []agentruntime.Event{
			requested, agentruntime.ToolApprovalResolved{RequestID: "ctl-1", Status: "expired"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			messages := []*transcript_entity.Message{{SessionID: 7, Role: "assistant", Seq: 1, BlocksJSON: block(tc.status, tc.result)}}

			frames, _, err := transcript.ProjectMessages("conv-7", messages)
			require.NoError(t, err)
			require.Len(t, frames, len(tc.want)+1, "审批帧之后只剩收口的 Done")
			for i, want := range tc.want {
				assert.Equal(t, want, frames[i].Event)
				// 每一帧都要真能过协议边界,并原样解回来。
				_, err := protowire.WireNotificationToProto(wire.NotifyEvent, frames[i])
				require.NoErrorf(t, err, "第 %d 帧送不出去", i)
			}
		})
	}
}

// Given 同一张审批卡先以 pending 落库、再被原地修补成 approved,When 按位置投影,
// Then 请求帧的位置与指纹不变(对端不会收到第二张卡),决议是同一块的下一帧 ——
// 远端那张卡正是靠这一帧切到终态。
func TestProjectKeyedMessage_GivenToolApprovalPatchedToApproved_ThenOnlyTheResolutionIsNew(t *testing.T) {
	t.Parallel()

	msg := func(status string) *transcript_entity.Message {
		return &transcript_entity.Message{ID: 9, SessionID: 7, Role: "assistant", Seq: 1,
			BlocksJSON: `[{"type":"tool_approval","data":{"tool_key":"org","request_id":"org-1","tool_name":"org_create_project","tool_input":{"name":"x"},"status":"` + status + `","result":"ok"}}]`}
	}
	pending, err := transcript.ProjectKeyedMessage("conv-7", msg("pending"))
	require.NoError(t, err)
	approved, err := transcript.ProjectKeyedMessage("conv-7", msg("approved"))
	require.NoError(t, err)

	require.Len(t, pending, 2) // 请求 + Done
	require.Len(t, approved, 3)
	assert.Equal(t, pending[0].Key, approved[0].Key)
	assert.Equal(t, pending[0].Fingerprint, approved[0].Fingerprint)
	assert.Equal(t, transcript.FrameKey{MessageID: 9, BlockIdx: 0, Ordinal: 1}, approved[1].Key)
	assert.Equal(t, agentruntime.ToolApprovalResolved{RequestID: "org-1", Status: "approved", Result: "ok"}, approved[1].Frame.Event)
}

// 坏载荷不静默吞掉:与其它交互卡同一条纪律,报错而不是投影出一张空卡。
func TestProjectMessages_GivenMalformedToolApprovalBlock_ThenErrors(t *testing.T) {
	t.Parallel()

	messages := []*transcript_entity.Message{{SessionID: 7, Role: "assistant", Seq: 1, BlocksJSON: `[{"type":"tool_approval","data":{"request_id":7}}]`}}
	_, _, err := transcript.ProjectMessages("conv-7", messages)
	require.Error(t, err)
}

// Given 一张已终态的后端无关审批卡落了库,When 折成对端持久帧,Then 先还原带全部
// 已脱敏内容的请求、再给终态 —— 对端折叠器只认「先有请求卡、再有终态」,只发
// 终态会让整张卡在对端消失;请求里少一格,对端卡片就少一行内容。
func TestProjectMessages_GivenTerminalApprovalBlock_ThenReplaysRequestContentBeforeTerminal(t *testing.T) {
	t.Parallel()

	request := agentruntime.ExecApprovalRequested{
		ID: "s-1", ApprovalKind: agentruntime.ApprovalKindSystemAgent, SessionKey: "agentre:12:41",
		CommandText: "sync --all", CommandPreview: "sync", Description: "d", ToolName: "tool", PluginName: "plug",
		Warnings: []string{"w"}, ActionCategory: agentruntime.ApprovalActionMessage,
		MessageTargets: []string{"#general", "alice"}, RecipientCount: 2, PaymentAmount: "9 USD",
		PaymentPayee: "ACME", PublishTarget: "blog", PublishVisibility: "public", AutomationName: "nightly",
		AllowedDecisions: []string{"allow-once", "allow-session", "deny"},
		Host:             "gateway", NodeID: "node-1", AgentID: "main", CreatedAtMs: 100, ExpiresAtMs: 200,
	}
	blocksJSON, err := json.Marshal([]map[string]any{{
		"type": "exec_approval",
		"data": map[string]any{
			"id": "s-1", "kind": "system-agent", "session_key": "agentre:12:41",
			"command_text": "sync --all", "command_preview": "sync", "description": "d",
			"tool_name": "tool", "plugin_name": "plug", "warnings": []string{"w"},
			"action_category": "message", "message_targets": []string{"#general", "alice"},
			"recipient_count": 2, "payment_amount": "9 USD", "payment_payee": "ACME",
			"publish_target": "blog", "publish_visibility": "public", "automation_name": "nightly",
			"allowed_decisions": []string{"allow-once", "allow-session", "deny"},
			"host":              "gateway", "node_id": "node-1", "agent_id": "main",
			"created_at_ms": 100, "expires_at_ms": 200,
			"status": "resolved", "decision": "allow-session", "resolved_by": "device-2", "resolved_at_ms": 150,
		},
	}})
	require.NoError(t, err)

	frames, _, err := transcript.ProjectMessages("conv-41", []*transcript_entity.Message{{
		SessionID: 41, Role: "assistant", Seq: 1, BlocksJSON: string(blocksJSON),
	}})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(frames), 2)
	assert.Equal(t, request, frames[0].Event)
	assert.Equal(t, agentruntime.ExecApprovalResolved{
		ID: "s-1", Status: "resolved", Decision: "allow-session", ResolvedBy: "device-2", ResolvedAtMs: 150,
	}, frames[1].Event)
}

// Given 一张已作答的提问卡落了库,其中一题后端不接受「其他」、一题是秘密问题且答案
// 已被生产者剥掉内容,When 折成对端持久帧再过 proto 边界,Then 两个开关逐题保真,
// 秘密那题的答复仍只有题号、不带任何内容 —— 少搬一个开关,对端卡片就会给 Hermes
// 画出它必然拒绝的自由输入,或把秘密问题画成明文输入框。
func TestProjectMessages_GivenAnsweredQuestionContract_ThenPeerFramesKeepPerQuestionFlags(t *testing.T) {
	t.Parallel()

	blocksJSON, err := json.Marshal([]map[string]any{{
		"type": "user_ask",
		"data": map[string]any{
			"request_id": "ask-7",
			"questions": []map[string]any{
				{"id": "q1", "question": "Pick", "disallowOther": true, "options": []map[string]any{{"label": "A", "description": "first"}}},
				{"id": "q2", "question": "Token?", "isOther": true, "isSecret": true, "options": []map[string]any{}},
			},
			"answered": true,
			"answers":  []map[string]any{{"questionIndex": 0, "labels": []string{"A"}}, {"questionIndex": 1, "labels": nil}},
		},
	}})
	require.NoError(t, err)

	frames, _, err := transcript.ProjectMessages("conv-41", []*transcript_entity.Message{{
		SessionID: 41, Role: "assistant", Seq: 1, BlocksJSON: string(blocksJSON),
	}})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(frames), 2)

	wantQuestions := []agentruntime.AskQuestion{
		{ID: "q1", Question: "Pick", DisallowOther: true, Options: []agentruntime.AskOption{{Label: "A", Description: "first"}}},
		{ID: "q2", Question: "Token?", IsOther: true, IsSecret: true, Options: []agentruntime.AskOption{}},
	}
	for i, frame := range frames[:2] {
		encoded, err := protowire.MarshalEventNotification("00000000-0000-7000-8000-000000000041", int64(i+1), frame.Event, false)
		require.NoError(t, err)
		decoded, _, err := protowire.UnmarshalEventNotification(encoded)
		require.NoError(t, err)
		switch event := decoded.Event.(type) {
		case agentruntime.UserAskRequest:
			require.Len(t, event.Questions, 2)
			for q := range wantQuestions {
				assert.Equal(t, wantQuestions[q].DisallowOther, event.Questions[q].DisallowOther)
				assert.Equal(t, wantQuestions[q].IsSecret, event.Questions[q].IsSecret)
				assert.Equal(t, wantQuestions[q].IsOther, event.Questions[q].IsOther)
				assert.Equal(t, wantQuestions[q].Options, append([]agentruntime.AskOption{}, event.Questions[q].Options...))
			}
		case agentruntime.UserAskResolved:
			require.Len(t, event.Answers, 2)
			assert.Equal(t, 1, event.Answers[1].QuestionIndex)
			assert.Empty(t, event.Answers[1].Labels)
			assert.Empty(t, event.Answers[1].OtherText)
		default:
			t.Fatalf("frame %d: unexpected event %T", i, decoded.Event)
		}
	}
}

// Given 一条 Hermes 不支持反向请求的提示落了库(结构化 NoticeBlock),When 折成对端
// 持久帧(session.pull / 补齐),Then 它重放成与实时同一个 UnsupportedRequestNotice,
// 只带用途分类 —— 落成 UnrecognizedBlock 的话,对端刷新后会把原始 JSON 当调试文本
// 画出来,而实时看到的是可读的一句话。其它 notice 仍原样走 UnrecognizedBlock。
func TestProjectMessages_GivenUnsupportedRequestNotice_ThenReplaysTheLiveNoticeEvent(t *testing.T) {
	t.Parallel()

	notice, err := json.Marshal(map[string]any{"level": "info", "text": transcriptblocks.EncodeUnsupportedRequestNotice("vault_code")})
	require.NoError(t, err)
	messages := []*transcript_entity.Message{{
		SessionID: 41, Role: "assistant", Seq: 1,
		BlocksJSON: `[{"type":"notice","data":` + string(notice) + `},{"type":"notice","data":{"level":"info","text":"legacy free text"}}]`,
	}}

	frames, _, err := transcript.ProjectMessages("conv-41", messages)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(frames), 2)
	assert.Equal(t, agentruntime.UnsupportedRequestNotice{Purpose: agentruntime.UnsupportedRequestVaultCode}, frames[0].Event)
	assert.Equal(t, agentruntime.EventUnrecognizedBlock, projectedEventKind(t, frames[1].Event))
}
