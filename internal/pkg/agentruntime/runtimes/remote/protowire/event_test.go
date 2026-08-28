package protowire

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/cago-frame/agents/provider"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/canonical"
)

func TestEventNotificationRoundTrip(t *testing.T) {
	// Given 一个 sealed runtime event；When 经生产 Go 转换边界编码再解码；
	// Then 字节与 TS fixture 一致，且不经过 JSON params。
	got, err := MarshalEventNotification(42, 7, agentruntime.TextDelta{Text: "hello"}, false)
	require.NoError(t, err)
	require.Equal(t, []byte{0x22, 0x0f, 0x0a, 0x0d, 0x08, 0x2a, 0x10, 0x07, 0x1a, 0x07, 0x0a, 0x05, 0x68, 0x65, 0x6c, 0x6c, 0x6f}, got)

	decoded, autonomous, err := UnmarshalEventNotification(got)
	require.NoError(t, err)
	require.False(t, autonomous)
	require.Equal(t, int64(42), decoded.SessionID)
	require.Equal(t, int64(7), decoded.Seq)
	require.Equal(t, agentruntime.TextDelta{Text: "hello"}, decoded.Event)
}

func TestEventNotificationPlanUpdatedPreservesCanonicalPayload(t *testing.T) {
	want := agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{
		Steps:   []canonical.PlanStep{{ID: "s1", Step: "inspect", Status: canonical.StepInProgress}},
		Text:    "## Plan\n- inspect",
		Actions: []canonical.PlanAction{{ID: canonical.PlanActionIDExecute, Kind: canonical.PlanActionApprove, RequiresFeedback: true}},
	}}
	encoded, err := MarshalEventNotification(42, 7, want, false)
	require.NoError(t, err)
	decoded, _, err := UnmarshalEventNotification(encoded)
	require.NoError(t, err)
	require.Equal(t, want, decoded.Event)
}

func TestEventNotificationSteerConsumedPreservesBatch(t *testing.T) {
	want := agentruntime.SteerConsumed{Steers: []agentruntime.ConsumedSteer{{
		QueuedID: "q-1", Text: "follow up", SourcePeer: "peer-1", SourceName: "Desktop",
	}}}
	encoded, err := MarshalEventNotification(42, 7, want, false)
	require.NoError(t, err)
	decoded, _, err := UnmarshalEventNotification(encoded)
	require.NoError(t, err)
	require.Equal(t, want, decoded.Event)
}

func TestEventNotificationRejectsNonNotification(t *testing.T) {
	_, _, err := UnmarshalEventNotification([]byte{0x08, 0x01})
	require.ErrorContains(t, err, "不是 runtime event 通知")
}

// TestEventNotificationAllSealedKindsRoundTrip 只证「空事件也编得出解得回」——
// 零值 specimen 对**字段保真**一个字都没说。真正守住保真的是下面那条
// populated 用例；这条留着,是因为零值本身有独立语义(nil Usage / nil Canonical /
// 空 ErrorEvent 都必须原样回来),不是前者的弱化版。
func TestEventNotificationAllSealedKindsRoundTrip(t *testing.T) {
	for _, specimen := range sealedEventSpecimens() {
		t.Run(fmt.Sprintf("%T", specimen), func(t *testing.T) {
			encoded, err := MarshalEventNotification(42, 7, specimen, true)
			require.NoError(t, err)
			decoded, autonomous, err := UnmarshalEventNotification(encoded)
			require.NoError(t, err)
			require.True(t, autonomous)
			require.Equal(t, specimen, decoded.Event)
		})
	}
}

func sealedEventSpecimens() []agentruntime.Event {
	return []agentruntime.Event{
		agentruntime.TextDelta{}, agentruntime.ThinkingDelta{}, agentruntime.OutputActivity{},
		agentruntime.ToolCall{}, agentruntime.ToolResult{}, agentruntime.SteerConsumed{},
		agentruntime.UserAskRequest{}, agentruntime.UserAskResolved{},
		agentruntime.ToolPermissionRequest{}, agentruntime.ToolPermissionResolved{},
		agentruntime.ExecApprovalRequested{}, agentruntime.ExecApprovalResolved{},
		agentruntime.PermissionModeChanged{}, agentruntime.SubagentStarted{},
		agentruntime.SubagentProgress{}, agentruntime.SubagentDone{}, agentruntime.SubagentModel{},
		agentruntime.Retry{}, agentruntime.UsageUpdate{}, agentruntime.ContextWindowUpdated{},
		agentruntime.CompactBoundary{}, agentruntime.RuntimeStatus{}, agentruntime.PlanUpdated{},
		agentruntime.Done{}, agentruntime.ErrorEvent{}, agentruntime.UserMessageEvent{},
		agentruntime.UnrecognizedBlock{},
	}
}

// TestEventNotificationPreservesEveryField 是这条边界的主守卫:每个 sealed
// event 的**每一个字段**都填上可区分的非零值,往返之后必须逐字段相等。
//
// 为什么非这样不可:上一版转换走的是 Event → JSON → protojson → proto。嵌套的
// AskQuestion / AskAnswer 没有 json tag,Go 序列化出来是 `"Question"` 这样的
// 字段名,protojson 认的是 `question`,配上 DiscardUnknown 便**静默丢光**;而
// protojson 反向把 int64 写成字符串,喂回 encoding/json 的 int64 直接报错。
// 四条事件因此在远端全坏 —— 而当时的「全部 kind 都往返」用例用的是零值
// specimen,一条都没红。零值 specimen 在这条边界上等于没有断言。
//
// 新增字段时这条用例必须同步填上。漏填不会变红(零值往返照样相等),所以这里
// 的纪律是「加字段就来这里加一行」,不是「等测试提醒你」。
func TestEventNotificationPreservesEveryField(t *testing.T) {
	info := agentruntime.SubagentInfo{
		TaskID: "task-1", SubagentType: "explore", Kind: "local_agent",
		TaskDescription: "look", Prompt: "go look", LastToolName: "Read",
		ToolUses: 3, TotalTokens: 900, DurationMs: 1200, Status: "running", Mode: "parallel",
		Runs: []agentruntime.SubagentRun{{
			ID: "r1", Index: 2, Agent: "explore", Profile: "p", AgentSource: "src",
			Task: "t", RequestedModel: "opus", Model: "opus-5", Status: "running",
			LastToolName: "Grep", ToolUses: 4, Summary: "sum", ErrorMessage: "err",
		}},
	}
	specimens := []agentruntime.Event{
		agentruntime.TextDelta{Text: "hi"},
		agentruntime.ThinkingDelta{Text: "think"},
		agentruntime.OutputActivity{},
		agentruntime.ToolCall{ID: "t1", Name: "Read", Input: json.RawMessage(`{"a":1}`), ParentToolCallID: "p1", SubagentRunID: "r1"},
		agentruntime.ToolResult{ToolCallID: "t1", Content: "out", IsError: true, ParentToolCallID: "p1", SubagentRunID: "r1", Meta: json.RawMessage(`{"m":2}`)},
		agentruntime.SteerConsumed{Steers: []agentruntime.ConsumedSteer{{QueuedID: "q", Text: "x", SourcePeer: "pe", SourceName: "na"}}},
		agentruntime.UserAskRequest{RequestID: "rq", ToolCallID: "tc", ParentToolCallID: "pt", Questions: []agentruntime.AskQuestion{{
			ID: "q1", Question: "去哪", Header: "路线", MultiSelect: true, IsOther: true, IsSecret: true,
			Options: []agentruntime.AskOption{{Label: "左", Description: "d", Preview: "pv"}},
		}}},
		agentruntime.UserAskResolved{RequestID: "rq", ParentToolCallID: "pt", Skipped: true, Answers: []agentruntime.AskAnswer{{QuestionIndex: 1, Labels: []string{"左"}, OtherText: "ot"}}},
		agentruntime.ToolPermissionRequest{RequestID: "pr", ToolCallID: "tc", ToolName: "Bash", Input: json.RawMessage(`{"cmd":"ls"}`)},
		agentruntime.ToolPermissionResolved{RequestID: "pr", Allowed: true, AlwaysAllow: true, DenyReason: "no"},
		agentruntime.ExecApprovalRequested{ID: "e1", CommandText: "ls", CommandPreview: "ls -l", AllowedDecisions: []string{"allow"}, Host: "h", NodeID: "n", AgentID: "a", SessionKey: "sk", CreatedAtMs: 111, ExpiresAtMs: 222},
		agentruntime.ExecApprovalResolved{ID: "e1", Status: "resolved", Decision: "allow", ResolvedBy: "me", ResolvedAtMs: 333},
		agentruntime.PermissionModeChanged{Mode: "plan"},
		agentruntime.SubagentStarted{ToolCallID: "s1", Info: info},
		agentruntime.SubagentProgress{ToolCallID: "s1", Info: info},
		agentruntime.SubagentDone{ToolCallID: "s1", Info: info},
		agentruntime.SubagentModel{ToolCallID: "s1", Model: "opus"},
		agentruntime.Retry{Message: "m", Details: "d", Attempt: 1, Max: 3},
		agentruntime.UsageUpdate{Usage: &provider.Usage{PromptTokens: 1, CompletionTokens: 2, ReasoningTokens: 3, CachedTokens: 4, CacheCreationTokens: 5, TotalTokens: 6}, TotalInputTokens: 10, ContextWindow: 200000},
		agentruntime.ContextWindowUpdated{Tokens: 4242},
		agentruntime.CompactBoundary{PreTokens: 10, PostTokens: 5, Trigger: "auto", DurationMs: 77},
		agentruntime.RuntimeStatus{Status: "compacting"},
		agentruntime.PlanUpdated{Plan: canonical.PlanUpdate{Text: "p", Steps: []canonical.PlanStep{{ID: "s", Step: "st", Status: canonical.StepInProgress}}}},
		agentruntime.Done{},
		agentruntime.ErrorEvent{Err: errors.New("boom")},
		agentruntime.UserMessageEvent{Text: "hello", SourceDevice: "fp", SourceDeviceName: "Mac"},
		agentruntime.UnrecognizedBlock{BlockType: "future_block", Data: json.RawMessage(`{"nested":{"keep":true}}`)},
	}
	require.Len(t, specimens, len(sealedEventSpecimens()),
		"每个 sealed event 都要有一份填满字段的 specimen")

	for _, specimen := range specimens {
		t.Run(fmt.Sprintf("%T", specimen), func(t *testing.T) {
			encoded, err := MarshalEventNotification(42, 7, specimen, false)
			require.NoError(t, err)
			decoded, _, err := UnmarshalEventNotification(encoded)
			require.NoError(t, err)
			require.Equal(t, specimen, decoded.Event)
		})
	}
}
