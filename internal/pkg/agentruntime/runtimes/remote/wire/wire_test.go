package wire

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/jsonrpc"
)

func TestToFromJSONRPCError_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"no active turn", agentruntime.ErrNoActiveTurn, ErrCodeNoActiveTurn},
		{"steer not found", agentruntime.ErrSteerNotFound, ErrCodeSteerNotFound},
		{"unsupported", agentruntime.ErrUnsupported, ErrCodeUnsupported},
		{"aborted", agentruntime.ErrAborted, ErrCodeAborted},
		{"session not found", agentruntime.ErrSessionNotFound, ErrCodeSessionNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := ToJSONRPCError(tc.err)
			require.NotNil(t, out, "sentinel must map to a code")
			assert.Equal(t, tc.code, out.Code)
			// Wire bytes round-trip preserves code.
			b, err := json.Marshal(out)
			require.NoError(t, err)
			var back jsonrpc.Error
			require.NoError(t, json.Unmarshal(b, &back))
			rehydrated := FromJSONRPCError(&back)
			assert.ErrorIs(t, rehydrated, tc.err)
		})
	}
}

func TestToJSONRPCError_NonSentinel(t *testing.T) {
	// 非 sentinel 错误返 nil,让 daemon 自己用 rpc.ErrInternal 包。
	assert.Nil(t, ToJSONRPCError(errors.New("random")))
	assert.Nil(t, ToJSONRPCError(nil))
}

func TestFromJSONRPCError_Passthrough(t *testing.T) {
	// 未知 code 原样返。
	in := &jsonrpc.Error{Code: -99999, Message: "weird"}
	out := FromJSONRPCError(in)
	assert.Equal(t, "weird", out.Error())
	// 完全非 jsonrpc.Error 也原样返。
	in2 := errors.New("plain error")
	out2 := FromJSONRPCError(in2)
	assert.Same(t, in2, out2)
}

// TestErrCodes_Stable pins error code values — wire protocol contract.
// Bumping these means every released agentred + agentre must upgrade in lock-step.
func TestErrCodes_Stable(t *testing.T) {
	assert.Equal(t, -32010, ErrCodeNoActiveTurn)
	assert.Equal(t, -32011, ErrCodeSteerNotFound)
	assert.Equal(t, -32012, ErrCodeUnsupported)
	assert.Equal(t, -32013, ErrCodeAborted)
	assert.Equal(t, -32014, ErrCodeSessionNotFound)
}

// TestMethodNames_Stable pins RPC method names — wire protocol contract.
func TestMethodNames_Stable(t *testing.T) {
	for k, v := range map[string]string{
		MethodCapabilities:          "runtime.capabilities",
		MethodRun:                   "runtime.run",
		MethodSteer:                 "runtime.steer",
		MethodCancelSteer:           "runtime.cancelSteer",
		MethodDrainPending:          "runtime.drainPending",
		MethodAbort:                 "runtime.abort",
		MethodSetPermissionMode:     "runtime.setPermissionMode",
		MethodSubmitAnswer:          "runtime.submitAnswer",
		MethodSubmitToolPermission:  "runtime.submitToolPermission",
		MethodGetGoal:               "runtime.goal.get",
		MethodSetGoal:               "runtime.goal.set",
		MethodClearGoal:             "runtime.goal.clear",
		NotifyEvent:                 "runtime.event",
		NotifyRunResultDone:         "runtime.runResultDone",
		NotifyAutonomousTurnStarted: "runtime.autonomousTurn.started",
		NotifyAutonomousTurnEvent:   "runtime.autonomousTurn.event",
		NotifyAutonomousTurnDone:    "runtime.autonomousTurn.done",
		MethodSessionDelete:         "runtime.session.delete",
	} {
		assert.Equal(t, v, k)
	}
}

// TestSessionDeleteCallError_SeparatesTooOldFromFailed 钉住删除的回落判据:执行端
// 不认识 runtime.session.delete 时回 JSON-RPC 标准的 -32601,调用方必须能把它与
// 「这一次没删成」分开 —— 前者重试多少次都是同一个结果(那台机器这辈子都答不了),
// 后者等一会儿再来就成了。判据与 remote.ErrCatchUpUnsupported 同形。
func TestSessionDeleteCallError_SeparatesTooOldFromFailed(t *testing.T) {
	tooOld := SessionDeleteCallError(&jsonrpc.Error{Code: jsonrpc.ErrMethodNotFound.Code, Message: "Method not found"})
	require.ErrorIs(t, tooOld, ErrSessionDeleteUnsupported)

	// 别的 JSON-RPC 错误只是这一次失败,不能被当成「对面太老」——那会让一条删不掉的
	// 会话被永久放弃。
	failed := &jsonrpc.Error{Code: ErrCodeSessionNotFound, Message: "session not found"}
	got := SessionDeleteCallError(failed)
	assert.NotErrorIs(t, got, ErrSessionDeleteUnsupported)
	assert.Same(t, failed, got)

	// 包在别的错误里的 -32601 同样算(调用栈会 wrap)。
	wrapped := SessionDeleteCallError(fmt.Errorf("delete session 7: %w", &jsonrpc.Error{Code: jsonrpc.ErrMethodNotFound.Code}))
	assert.ErrorIs(t, wrapped, ErrSessionDeleteUnsupported)

	assert.NoError(t, SessionDeleteCallError(nil))
}

func TestAutonomousTurnStartedFrame_RoundTrip(t *testing.T) {
	in := AutonomousTurnStartedFrame{SessionID: 77, Trigger: "background_task"}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	var out AutonomousTurnStartedFrame
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in, out)
}

func TestEventFrame_RoundTrip(t *testing.T) {
	// 用一个 sealed event 走完整 marshal -> EventFrame -> unmarshal -> UnmarshalEvent 链路。
	ev := agentruntime.TextDelta{Text: "hi"}
	body, err := json.Marshal(ev)
	require.NoError(t, err)

	frame := EventFrame{SessionID: 42, Event: body}
	b, err := json.Marshal(frame)
	require.NoError(t, err)

	var decoded EventFrame
	require.NoError(t, json.Unmarshal(b, &decoded))
	assert.Equal(t, int64(42), decoded.SessionID)

	out, err := agentruntime.UnmarshalEvent(decoded.Event)
	require.NoError(t, err)
	assert.Equal(t, ev, out)
}

func TestRunResultDoneFrame_RoundTrip(t *testing.T) {
	in := RunResultDoneFrame{
		SessionID:         99,
		ProviderSessionID: "sess-1",
		Usage: &UsageWire{
			PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150,
		},
		UserAnchor:    "u-1",
		Model:         "claude-sonnet-4-6",
		ContextWindow: 200000,
		StopErrMsg:    "aborted by user",
		StopErrCode:   ErrCodeAborted,
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	var out RunResultDoneFrame
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in, out)
}

// TestRunParams_RawBackendOpaque verifies Backend is passed as raw bytes,
// keeping wire schema decoupled from agent_backend_entity layout.
func TestRunParams_RawBackendOpaque(t *testing.T) {
	in := RunParams{
		Backend:        json.RawMessage(`{"ID":1,"Type":"claudecode","Name":"x"}`),
		AgentID:        7,
		SessionID:      42,
		UserText:       "hello",
		Compact:        true,
		PermissionMode: "acceptEdits",
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	var out RunParams
	require.NoError(t, json.Unmarshal(b, &out))
	// JSONEq because key ordering inside Backend may differ.
	assert.JSONEq(t, string(in.Backend), string(out.Backend))
	assert.Equal(t, in.AgentID, out.AgentID)
	assert.Equal(t, in.SessionID, out.SessionID)
	assert.Equal(t, in.UserText, out.UserText)
	assert.Equal(t, in.Compact, out.Compact)
	assert.Equal(t, in.PermissionMode, out.PermissionMode)
}

func TestRunParams_HasNoOpenClawSecretField(t *testing.T) {
	typ := reflect.TypeOf(RunParams{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		searchable := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
		assert.NotContains(t, searchable, "token", "RunParams must not carry tokens across daemon wire")
		assert.NotContains(t, searchable, "secret", "RunParams must not carry secrets across daemon wire")
	}
}

// TestRunAck_ProviderFallbackKeyRoundTrip 钉死决策 9 信号回传:daemon 在会话所选供应
// 商缺失/非 active 回退 agent 绑定后,把被回退的 key 放进 ack.ProviderFallbackKey 随
// wire 过线;空值因 omitempty 不落字节流。
func TestRunAck_ProviderFallbackKeyRoundTrip(t *testing.T) {
	in := RunAck{SessionID: 42, ProviderFallbackKey: "session-key"}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"providerFallbackKey":"session-key"`)

	var out RunAck
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in.ProviderFallbackKey, out.ProviderFallbackKey)

	// 未回退 → 字段不出现在字节流。
	none, err := json.Marshal(RunAck{SessionID: 1})
	require.NoError(t, err)
	assert.NotContains(t, string(none), "providerFallbackKey")
	var outNone RunAck
	require.NoError(t, json.Unmarshal(none, &outNone))
	assert.Equal(t, "", outNone.ProviderFallbackKey)
}

func TestRunParams_UserBlocksRoundTrip(t *testing.T) {
	// Given a multimodal user message crossing desktop -> agentred,
	// when RunParams is marshaled, then text and inline image bytes survive.
	stored, err := cagoblocks.EncodeAll([]cagoblocks.ContentBlock{
		cagoblocks.TextBlock{Text: "what is this?"},
		cagoblocks.ImageBlock{
			MediaType: "image/png",
			Source:    cagoblocks.BlobSource{Inline: []byte{0x89, 0x50, 0x4e, 0x47}},
		},
	})
	require.NoError(t, err)

	in := RunParams{SessionID: 42, UserText: "what is this?", UserBlocks: stored}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"userBlocks"`)

	var out RunParams
	require.NoError(t, json.Unmarshal(b, &out))
	decoded, err := cagoblocks.DecodeAll(out.UserBlocks)
	require.NoError(t, err)
	require.Len(t, decoded, 2)
	assert.Equal(t, "what is this?", decoded[0].(cagoblocks.TextBlock).Text)
	img := decoded[1].(cagoblocks.ImageBlock)
	assert.Equal(t, "image/png", img.MediaType)
	assert.Equal(t, []byte{0x89, 0x50, 0x4e, 0x47}, img.Source.Inline)
}

// TestParams_FieldShape spot-checks lowerCamelCase tagging by walking a
// representative param set. Adding a struct field without a json tag is a
// common drift; an UPPER-cased key in the wire output would surface here.
func TestParams_FieldShape(t *testing.T) {
	specs := []struct {
		name string
		v    any
		want []string // each expected `"key":` substring
	}{
		{"steer", SteerParams{SessionID: 1, QueuedID: "q", Text: "t"},
			[]string{`"sessionId":1`, `"queuedId":"q"`, `"text":"t"`}},
		{"cancelSteer", CancelSteerParams{SessionID: 1, QueuedID: "q"},
			[]string{`"sessionId":1`, `"queuedId":"q"`}},
		{"drain", DrainParams{SessionID: 1}, []string{`"sessionId":1`}},
		{"abort", AbortParams{SessionID: 1}, []string{`"sessionId":1`}},
		{"setMode", SetPermissionModeParams{SessionID: 1, Mode: "plan"},
			[]string{`"sessionId":1`, `"mode":"plan"`}},
		{"submitAnswer", SubmitAnswerParams{SessionID: 1, RequestID: "r", Skipped: true},
			[]string{`"sessionId":1`, `"requestId":"r"`, `"skipped":true`}},
		{"submitToolPerm", SubmitToolPermissionParams{
			SessionID: 1, RequestID: "r", Allow: true, AlwaysAllowSession: true, DenyReason: "x",
		}, []string{`"sessionId":1`, `"requestId":"r"`, `"allow":true`, `"alwaysAllowSession":true`, `"denyReason":"x"`}},
		{"goalGet", GoalParams{SessionID: 1, AgentID: 9, ProviderSessionID: "thread-1", Backend: json.RawMessage(`{"Type":"codex"}`)},
			[]string{`"sessionId":1`, `"agentId":9`, `"providerSessionId":"thread-1"`, `"backend":`, `"Type":"codex"`}},
		{"goalSet", GoalParams{SessionID: 1, AgentID: 9, ProviderSessionID: "thread-1", Backend: json.RawMessage(`{"Type":"codex"}`), Objective: ptrString("ship"), Status: ptrString("active"), TokenBudget: ptrInt(123)},
			[]string{`"sessionId":1`, `"agentId":9`, `"providerSessionId":"thread-1"`, `"backend":`, `"Type":"codex"`, `"objective":"ship"`, `"status":"active"`, `"tokenBudget":123`}},
		{"goalResult", GoalResult{Goal: &agentruntime.Goal{ThreadID: "thread-1", Objective: "ship", Status: "active"}},
			[]string{`"goal":`, `"threadId":"thread-1"`, `"objective":"ship"`, `"status":"active"`}},
		{"goalClearResult", GoalClearResult{Cleared: true}, []string{`"cleared":true`}},
		{"capabilities", CapabilitiesParams{BackendType: "claudecode"},
			[]string{`"backendType":"claudecode"`}},
		{"runAck", RunAck{SessionID: 42}, []string{`"sessionId":42`}},
		{"runParamsCompact", RunParams{SessionID: 42, Compact: true}, []string{`"sessionId":42`, `"compact":true`}},
		{"runParamsEnabledPlugins", RunParams{SessionID: 42, EnabledPlugins: map[string]bool{"browser@openai-bundled": true}},
			[]string{`"sessionId":42`, `"enabledPlugins":`, `"browser@openai-bundled":true`}},
		{"cancelSteerResult", CancelSteerResult{Removed: []string{"a", "b"}},
			[]string{`"removed":["a","b"]`}},
	}
	for _, s := range specs {
		t.Run(s.name, func(t *testing.T) {
			b, err := json.Marshal(s.v)
			require.NoError(t, err)
			for _, w := range s.want {
				assert.Contains(t, string(b), w, "missing field: expected %s in %s", w, string(b))
			}
		})
	}
}

// 编译时确认 *jsonrpc.Error 满足 error,这样 ToJSONRPCError 返回的可以直接 return。
var _ error = (*jsonrpc.Error)(nil)

// TestRunParams_LLMTargetKeysRoundTrip 钉死决策 11:RunParams 同时携带
// LLMProviderKey 与 LLMModelKey,且空值因 omitempty 不落字节流。
func TestRunParams_LLMTargetKeysRoundTrip(t *testing.T) {
	in := RunParams{SessionID: 42, LLMProviderKey: "prov-1", LLMModelKey: "model-7"}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"llmProviderKey":"prov-1"`)
	assert.Contains(t, string(b), `"llmModelKey":"model-7"`)

	var out RunParams
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, "prov-1", out.LLMProviderKey)
	assert.Equal(t, "model-7", out.LLMModelKey)

	// provider-default:model key 空 → 字段不出现在字节流,旧 daemon 解码不受影响。
	none, err := json.Marshal(RunParams{SessionID: 1, LLMProviderKey: "prov-1"})
	require.NoError(t, err)
	assert.NotContains(t, string(none), "llmModelKey")
}

// TestGoalParams_LLMTargetKeysRoundTrip 钉死决策 11:GoalParams 补齐了
// LLMProviderKey + LLMModelKey(与 RunParams 同形),goal 与 turn 不再各自解析。
func TestGoalParams_LLMTargetKeysRoundTrip(t *testing.T) {
	in := GoalParams{
		SessionID: 42, ProviderSessionID: "thread-1",
		Backend:        json.RawMessage(`{"Type":"codex"}`),
		LLMProviderKey: "prov-1", LLMModelKey: "model-7",
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"llmProviderKey":"prov-1"`)
	assert.Contains(t, string(b), `"llmModelKey":"model-7"`)

	var out GoalParams
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, "prov-1", out.LLMProviderKey)
	assert.Equal(t, "model-7", out.LLMModelKey)
}

// TestCapLLMModelTargetV1_Stable 钉死决策 11 的能力位常量与辅助函数。
func TestCapLLMModelTargetV1_Stable(t *testing.T) {
	assert.Equal(t, "llm-model-target-v1", CapLLMModelTargetV1)
	assert.True(t, HasCapability([]string{"a", CapLLMModelTargetV1}, CapLLMModelTargetV1))
	assert.False(t, HasCapability([]string{"a"}, CapLLMModelTargetV1))
	assert.False(t, HasCapability(nil, CapLLMModelTargetV1))
}

// TestProviderSummary_ModelSummaries 钉死「远端目录只含非敏感摘要」:Provider/Model
// 稳定 key + 实际 model id + 启用态可过线,但 wire 形状里不存在 APIKey / BaseURL 字段。
func TestProviderSummary_ModelSummaries(t *testing.T) {
	typ := reflect.TypeOf(ProviderSummary{})
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		searchable := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
		assert.NotContains(t, searchable, "apikey", "ProviderSummary must never carry API keys")
		assert.NotContains(t, searchable, "baseurl", "ProviderSummary must never carry base URLs")
	}

	in := ProviderSummary{
		Key: "prov-1", Name: "Anthropic Prod", Type: "anthropic",
		DefaultModelKey: "model-1",
		Models: []ModelSummary{
			{Key: "model-1", ModelID: "claude-opus-4", Name: "Opus", Enabled: true},
			{Key: "model-2", ModelID: "claude-sonnet-4", Enabled: false},
		},
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"defaultModelKey":"model-1"`)
	assert.Contains(t, string(b), `"modelId":"claude-opus-4"`)
	assert.Contains(t, string(b), `"enabled":false`)

	var out ProviderSummary
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in, out)
}

func ptrString(v string) *string { return &v }
func ptrInt(v int) *int          { return &v }
