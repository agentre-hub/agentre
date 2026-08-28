package wire

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
)

func TestToFromRPCError_RoundTrip(t *testing.T) {
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
			out := ToRPCError(tc.err)
			require.NotNil(t, out, "sentinel must map to a code")
			assert.EqualValues(t, tc.code, out.Code)
			// Wire bytes round-trip preserves code.
			b, err := json.Marshal(out)
			require.NoError(t, err)
			var back rpcerror.Error
			require.NoError(t, json.Unmarshal(b, &back))
			rehydrated := FromRPCError(&back)
			assert.ErrorIs(t, rehydrated, tc.err)
		})
	}
}

func TestToRPCError_NonSentinel(t *testing.T) {
	// 非 sentinel 错误返 nil,让 daemon 自己用 rpc.ErrInternal 包。
	assert.Nil(t, ToRPCError(errors.New("random")))
	assert.Nil(t, ToRPCError(nil))
}

func TestFromRPCError_Passthrough(t *testing.T) {
	// 未知 code 原样返。
	in := &rpcerror.Error{Code: -99999, Message: "weird"}
	out := FromRPCError(in)
	assert.Equal(t, "weird", out.Error())
	// 完全非 rpcerror.Error 也原样返。
	in2 := errors.New("plain error")
	out2 := FromRPCError(in2)
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

func TestAutonomousTurnStartedFrame_RoundTrip(t *testing.T) {
	in := AutonomousTurnStartedFrame{SessionID: 77, Trigger: "background_task"}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	var out AutonomousTurnStartedFrame
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in, out)
}

func TestEventFrame_RoundTrip(t *testing.T) {
	// 帧装的是密封事件本身,JSON 只是它的线上形态:marshal → unmarshal 之后
	// 必须原样还是那个 sealed 值,调用方不再自己 UnmarshalEvent。
	ev := agentruntime.TextDelta{Text: "hi"}

	b, err := json.Marshal(EventFrame{SessionID: 42, Event: ev})
	require.NoError(t, err)
	// 线上形态与「事件自己 marshal 出来的那段」逐字节一致 —— 帧换成装密封值
	// 之后,老版本对端与通知日志里的旧行读到的字节没有任何变化。
	eventJSON, err := json.Marshal(ev)
	require.NoError(t, err)
	assert.JSONEq(t, `{"sessionId":42,"event":`+string(eventJSON)+`}`, string(b))

	var decoded EventFrame
	require.NoError(t, json.Unmarshal(b, &decoded))
	assert.Equal(t, int64(42), decoded.SessionID)
	assert.Equal(t, ev, decoded.Event)
}

// Given EventFrame 上那组不驱动序列化的 json tag,When 与它自己的 MarshalJSON
// 实际落出来的键比对,Then 两者必须一致。
//
// 为什么单独守:tag 是 TS 编解码生成器唯一读得到的东西,自定义 marshaler 它看不见。
// 两处分家的后果是生成出来的 decodeEventFrame 去找一批根本不存在的键 —— Go 侧
// 编译、测试全绿,浏览器侧整条事件流解码失败。
func TestEventFrameWireTagsMatchMarshaler(t *testing.T) {
	tagged := make([]string, 0, 3)
	typ := reflect.TypeOf(EventFrame{})
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		require.NotEmpty(t, name, "EventFrame.%s 缺 json tag,生成器会读到 Go 字段名", typ.Field(i).Name)
		tagged = append(tagged, name)
	}

	// seq 带 omitempty,只有非零才落键 —— 所以这里要填满。
	b, err := json.Marshal(EventFrame{SessionID: 1, Event: agentruntime.Done{}, Seq: 2})
	require.NoError(t, err)
	var marshaled map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &marshaled))

	emitted := make([]string, 0, len(marshaled))
	for key := range marshaled {
		emitted = append(emitted, key)
	}
	sort.Strings(tagged)
	sort.Strings(emitted)
	require.Equal(t, tagged, emitted)
}

// Given JournaledNotification 上那组不驱动序列化的 json tag,When 与它自己的
// MarshalJSON 实际落出来的键比对,Then 两者必须一致 —— 理由与 EventFrame 那条
// 相同:tag 是 TS 生成器唯一读得到的东西。
func TestJournaledNotificationWireTagsMatchMarshaler(t *testing.T) {
	tagged := make([]string, 0, 3)
	typ := reflect.TypeOf(JournaledNotification{})
	for i := 0; i < typ.NumField(); i++ {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		require.NotEmpty(t, name, "JournaledNotification.%s 缺 json tag", typ.Field(i).Name)
		tagged = append(tagged, name)
	}

	b, err := json.Marshal(JournaledNotification{
		Seq: 1, Method: NotifyEvent, Params: &EventFrame{SessionID: 2, Event: agentruntime.Done{}},
	})
	require.NoError(t, err)
	var marshaled map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &marshaled))

	emitted := make([]string, 0, len(marshaled))
	for key := range marshaled {
		emitted = append(emitted, key)
	}
	sort.Strings(tagged)
	sort.Strings(emitted)
	require.Equal(t, tagged, emitted)
}

// Given 一行补齐日志,When 走完 marshal → unmarshal,Then Params 回来仍是那个帧
// 本身 —— 线上形态不变(黄金样本守),但进程内不再是一段待解析的字节。
func TestJournaledNotification_RoundTripsTypedParams(t *testing.T) {
	in := JournaledNotification{
		Seq: 11, Method: NotifyEvent,
		Params: &EventFrame{SessionID: 42, Event: agentruntime.TextDelta{Text: "你好"}},
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)

	var out JournaledNotification
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in, out)
}

// Given 一条本客户端还不认识的 method,When 解码,Then 得到 nil 帧而不是错误 ——
// 整段补齐不该因为一条新通知而失败,那会把它后面每一条已知通知也一起丢掉。
func TestJournaledNotification_UnknownMethodDecodesToNilFrame(t *testing.T) {
	var out JournaledNotification
	require.NoError(t, json.Unmarshal(
		[]byte(`{"seq":1,"method":"runtime.somethingNew","params":{"a":1}}`), &out))
	require.Nil(t, out.Params)
}

// Given 一段解不出的事件载荷,When 解码 EventFrame,Then 整帧解码失败而不是
// 留下一个装着半截数据的帧 —— 帧一旦声称自己装的是密封事件,就不能悄悄装 nil。
func TestEventFrame_RejectsUndecodableEvent(t *testing.T) {
	var decoded EventFrame
	err := json.Unmarshal([]byte(`{"sessionId":42,"event":{"kind":"no_such_kind"}}`), &decoded)
	require.ErrorContains(t, err, "unknown kind")
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

// 编译时确认 *rpcerror.Error 满足 error,这样 ToRPCError 返回的可以直接 return。
var _ error = (*rpcerror.Error)(nil)

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

// TestSkillsCatalogMethod_Stable 钉住技能目录方法名。浏览器要用它替掉「手打 skill id」,
// 名字一旦发布就不能改 —— 老浏览器会拿着旧名字打过来。
func TestSkillsCatalogMethod_Stable(t *testing.T) {
	assert.Equal(t, "skills.catalog", MethodSkillsCatalog)
	assert.Equal(t, "ok", SkillDiscoveryOK)
	assert.Equal(t, "unavailable", SkillDiscoveryUnavailable)
	assert.Equal(t, "unsupported", SkillDiscoveryUnsupported)
}

// TestSkillCatalogParams_FieldShape 钉住请求的字节形状:一次问的是**一档**执行目标
// (R15e「一档一块」),所以授权集随请求一起来 —— 执行端上没有组织架构库,答不出
// 「这一档授权了什么」,那份真相在调用方手里。
func TestSkillCatalogParams_FieldShape(t *testing.T) {
	b, err := json.Marshal(SkillCatalogParams{
		BackendType: "claudecode",
		CLIPath:     "/usr/local/bin/claude",
		Authorized: []SkillAuthorization{
			{ID: "superpowers@claude-plugins-official", Enabled: true},
		},
	})
	require.NoError(t, err)
	for _, want := range []string{
		`"backendType":"claudecode"`,
		`"cliPath":"/usr/local/bin/claude"`,
		`"authorized":[{"id":"superpowers@claude-plugins-official","enabled":true}]`,
	} {
		assert.Contains(t, string(b), want)
	}
}

// TestSkillCatalogResult_DiscoveryIsAlwaysOnTheWire 是本方法最要紧的一条:发现失败 /
// 机器离线时**不能用空目录冒充「这台机器上没有技能」**。discovery 因此没有 omitempty,
// 空目录必须自带一个说明它为什么空的判别值。
func TestSkillCatalogResult_DiscoveryIsAlwaysOnTheWire(t *testing.T) {
	b, err := json.Marshal(SkillCatalogResult{Packs: []SkillPackSummary{}, Discovery: SkillDiscoveryUnavailable})
	require.NoError(t, err)
	assert.Contains(t, string(b), `"discovery":"unavailable"`)
	assert.Contains(t, string(b), `"packs":[]`)

	// 零值同样带着 discovery 键出场 —— 解码方永远不必猜「没这个键算什么」。
	b2, err := json.Marshal(SkillCatalogResult{})
	require.NoError(t, err)
	assert.Contains(t, string(b2), `"discovery":`)
}

// TestSkillPackSummary_RoundTrip 钉住浏览器画一行要读的那几格(见 agentre 的
// skillPacksToCatalog → CatalogItem):id / name / description / 包内内容 /
// 是否已装 / 这一档是否已授权 / 全局是否已启用。
func TestSkillPackSummary_RoundTrip(t *testing.T) {
	in := SkillPackSummary{
		ID:              "superpowers@claude-plugins-official",
		Name:            "superpowers",
		Description:     "brainstorming and TDD",
		Skills:          []string{"brainstorming", "test-driven-development"},
		Installed:       true,
		Enabled:         true,
		GloballyEnabled: true,
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	var out SkillPackSummary
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in, out)
	assert.Contains(t, string(b), `"skills":["brainstorming","test-driven-development"]`)
}
