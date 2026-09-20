package acp

import (
	"context"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

// TestACPCapabilities 钉死能力矩阵:宣告的 cap=true 与实现的子接口一致,
// 未宣告的能力一律不实现(调到只会得到 ErrUnsupported,而不是桩方法)。
func TestACPCapabilities(t *testing.T) {
	r := New()
	caps := r.Capabilities()

	want := map[capability.Capability]bool{
		capability.CapAbort:               true,
		capability.CapToolPermission:      true,
		capability.CapReportContextWindow: true,
		capability.CapImageInput:          true,
		capability.CapMCPTools:            true,
	}
	assert.Equal(t, want, caps.Set, "declared caps must be exactly the five supported channels")

	// cap=true ↔ 接口实现。
	var _ agentruntime.Aborter = r
	var _ agentruntime.ToolPermissionSink = r

	// cap=false ↔ 不实现对应接口(不写桩方法)。
	_, ok := any(r).(agentruntime.Steerer)
	assert.False(t, ok, "must not implement Steerer")
	_, ok = any(r).(agentruntime.SteerCanceler)
	assert.False(t, ok, "must not implement SteerCanceler")
	_, ok = any(r).(agentruntime.SteerDrainer)
	assert.False(t, ok, "must not implement SteerDrainer")
	_, ok = any(r).(agentruntime.PermissionModeSetter)
	assert.False(t, ok, "must not implement PermissionModeSetter")
	_, ok = any(r).(agentruntime.AskAnswerSink)
	assert.False(t, ok, "must not implement AskAnswerSink")
	_, ok = any(r).(agentruntime.ExecApprovalSink)
	assert.False(t, ok, "must not implement ExecApprovalSink")
	_, ok = any(r).(agentruntime.BackgroundTaskStopper)
	assert.False(t, ok, "must not implement BackgroundTaskStopper")
	_, ok = any(r).(agentruntime.AutonomousTurnSource)
	assert.False(t, ok, "must not implement AutonomousTurnSource")
	_, ok = any(r).(agentruntime.Rewinder)
	assert.False(t, ok, "must not implement Rewinder (fork/regenerate stays explicitly refused)")

	// Capabilities 必须稳定(同实例重复调用同结果)。
	assert.Equal(t, want, r.Capabilities().Set)
}

func acpTestBackend() *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		Type:       string(agent_backend_entity.TypeACP),
		Name:       "acp",
		ACPCommand: "fake-agent",
		EnvJSON:    "{}",
	}
}

func ptrStatus(s acpsdk.ToolCallStatus) *acpsdk.ToolCallStatus { return &s }

func drainEvents(t *testing.T, events <-chan agentruntime.Event) []agentruntime.Event {
	t.Helper()
	var out []agentruntime.Event
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out draining events; got %d so far", len(out))
		}
	}
}

// TestRun_HappyPath 一轮完整对话:文本增量 → 工具 → 计划 → usage → Done,
// RunResult 只在事件流关闭后落定。
func TestRun_HappyPath(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	cwd := t.TempDir()

	events, result, err := r.Run(context.Background(), agentruntime.RunRequest{
		Backend:   acpTestBackend(),
		SessionID: 42,
		UserText:  "hello",
		Cwd:       cwd,
	})
	require.NoError(t, err)

	prompt := fa.waitPrompt(2 * time.Second)
	assert.Equal(t, "acp-sess-1", string(prompt.SessionId))
	require.Len(t, prompt.Prompt, 1)
	require.NotNil(t, prompt.Prompt[0].Text)
	assert.Equal(t, "hello", prompt.Prompt[0].Text.Text)
	assert.Equal(t, cwd, fa.lastSessionCwd(), "cwd must reach session/new")

	fa.sendUpdate(acpsdk.UpdateAgentMessageText("hi "))
	fa.sendUpdate(acpsdk.UpdateAgentMessageText("there"))
	fa.sendUpdate(acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		ToolCallId: "tc-1",
		Title:      "Run tests",
		Kind:       acpsdk.ToolKindExecute,
		Status:     acpsdk.ToolCallStatusPending,
		RawInput:   map[string]any{"command": "go test"},
	}})
	fa.sendUpdate(acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
		ToolCallId: "tc-1",
		Status:     ptrStatus(acpsdk.ToolCallStatusCompleted),
		Content:    []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock("ok"))},
	}})
	fa.sendUpdate(acpsdk.SessionUpdate{Plan: &acpsdk.SessionUpdatePlan{Entries: []acpsdk.PlanEntry{
		{Content: "step one", Status: acpsdk.PlanEntryStatusPending},
	}}})
	fa.sendUpdate(acpsdk.SessionUpdate{UsageUpdate: &acpsdk.SessionUsageUpdate{Used: 500, Size: 200000}})
	fa.respondPrompt(acpsdk.StopReasonEndTurn, &acpsdk.Usage{
		InputTokens:  100,
		OutputTokens: 20,
		TotalTokens:  120,
	})

	got := drainEvents(t, events)
	require.NotEmpty(t, got)
	last := got[len(got)-1]
	assert.Equal(t, agentruntime.Done{}, last, "stream must end with Done, got %T", last)

	var kinds []string
	for _, ev := range got {
		switch e := ev.(type) {
		case agentruntime.TextDelta:
			kinds = append(kinds, "text")
		case agentruntime.ToolCall:
			kinds = append(kinds, "tool_call")
			assert.Equal(t, "tc-1", e.ID)
		case agentruntime.ToolResult:
			kinds = append(kinds, "tool_result")
			assert.Equal(t, "ok", e.Content)
		case agentruntime.PlanUpdated:
			kinds = append(kinds, "plan")
		case agentruntime.UsageUpdate:
			kinds = append(kinds, "usage")
		case agentruntime.ContextWindowUpdated:
			kinds = append(kinds, "context_window")
			assert.Equal(t, 200000, e.Tokens)
		case agentruntime.Done:
			kinds = append(kinds, "done")
		default:
			t.Fatalf("unexpected event %T", ev)
		}
	}
	assert.Equal(t, []string{"text", "text", "tool_call", "tool_result", "plan", "context_window", "usage", "done"}, kinds)

	assert.NoError(t, result.StopErr)
	assert.Equal(t, "acp-sess-1", result.ProviderSessionID)
	require.NotNil(t, result.Usage)
	assert.Equal(t, 100, result.Usage.PromptTokens)
	assert.Equal(t, 20, result.Usage.CompletionTokens)
	assert.Equal(t, 200000, result.ContextWindow)
	assert.NotZero(t, result.TurnToken)
	assert.Empty(t, result.Model, "ACP has no model reporting channel; Model stays empty")
}

// TestRun_ReusesAgentProcessAcrossTurns 一个 chat session 的两轮复用同一个
// agent 进程:session/new 只发生一次,第二轮 prompt 直接打在同一 sessionId 上。
func TestRun_ReusesAgentProcessAcrossTurns(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	req := agentruntime.RunRequest{Backend: acpTestBackend(), SessionID: 7, UserText: "one", Cwd: t.TempDir()}

	events, result, err := r.Run(context.Background(), req)
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	drainEvents(t, events)
	assert.Equal(t, "acp-sess-1", result.ProviderSessionID)

	req.UserText = "two"
	events2, result2, err := r.Run(context.Background(), req)
	require.NoError(t, err)
	prompt := fa.waitPrompt(2 * time.Second)
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	drainEvents(t, events2)

	assert.Equal(t, 1, fa.newSessionCalls(), "second turn must reuse the resident agent process")
	assert.Equal(t, "acp-sess-1", string(prompt.SessionId))
	assert.Equal(t, "acp-sess-1", result2.ProviderSessionID)
}

// TestRun_MCPListChangeRestartsProcess MCP 清单变了必须重开:启动身份含
// mcpServers 指纹,池会驱逐旧进程。
func TestRun_MCPListChangeRestartsProcess(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{mcpHTTP: true})
	r := New()
	base := agentruntime.RunRequest{Backend: acpTestBackend(), SessionID: 9, UserText: "one", Cwd: t.TempDir()}

	events, _, err := r.Run(context.Background(), base)
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	drainEvents(t, events)
	assert.Equal(t, 1, fa.newSessionCalls())

	base.MCPServers = []agentruntime.MCPServerSpec{{Name: "org", URL: "http://127.0.0.1:1/mcp/"}}
	events2, _, err := r.Run(context.Background(), base)
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	drainEvents(t, events2)
	assert.Equal(t, 2, fa.newSessionCalls(), "changed MCP identity must restart the agent process")
}

// TestAbort_UnblocksTurn Abort 后:挂起轮收到 Done、StopErr=ErrAborted、
// agent 收到 session/cancel。
func TestAbort_UnblocksTurn(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	events, result, err := r.Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 11, UserText: "work", Cwd: t.TempDir(),
	})
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	fa.sendUpdate(acpsdk.UpdateAgentMessageText("partial "))

	outcome, err := r.Abort(context.Background(), 11, 0)
	require.NoError(t, err)
	assert.Equal(t, agentruntime.TurnKindUser, outcome.TurnKind)
	assert.True(t, fa.waitCancel(2*time.Second), "agent must receive session/cancel")
	fa.respondPrompt(acpsdk.StopReasonCancelled, nil)

	got := drainEvents(t, events)
	require.NotEmpty(t, got)
	assert.Equal(t, agentruntime.Done{}, got[len(got)-1])
	assert.ErrorIs(t, result.StopErr, agentruntime.ErrAborted)
}

// TestAbort_EscalatesWhenAgentIgnoresCancel agent 不回 prompt response 时,
// Abort 宽限期后整进程收掉,轮次仍要收尾(绝不停在「生成中」)。
func TestAbort_EscalatesWhenAgentIgnoresCancel(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	restore := r.SetAbortGraceForTest(100 * time.Millisecond)
	defer restore()

	events, result, err := r.Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 12, UserText: "hang", Cwd: t.TempDir(),
	})
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)

	_, err = r.Abort(context.Background(), 12, 0)
	require.NoError(t, err)

	got := drainEvents(t, events)
	require.NotEmpty(t, got)
	assert.Equal(t, agentruntime.Done{}, got[len(got)-1])
	assert.ErrorIs(t, result.StopErr, agentruntime.ErrAborted)
}

// TestAbort_NoActiveTurn 无进行中轮次 / 未知会话 → ErrNoActiveTurn;Abort 幂等。
func TestAbort_NoActiveTurn(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	_, err := r.Abort(context.Background(), 404, 0)
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)

	// 一轮正常结束后再 Abort 也是无轮可中断。
	events, _, err := r.Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 13, UserText: "done", Cwd: t.TempDir(),
	})
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)
	fa.respondPrompt(acpsdk.StopReasonEndTurn, nil)
	drainEvents(t, events)
	_, err = r.Abort(context.Background(), 13, 0)
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

// TestRun_ContextCancel ctx 取消要能解阻塞整轮。
func TestRun_ContextCancel(t *testing.T) {
	fa := installFakeAgent(t, fakeAgentOptions{})
	r := New()
	ctx, cancel := context.WithCancel(context.Background())
	events, result, err := r.Run(ctx, agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 14, UserText: "x", Cwd: t.TempDir(),
	})
	require.NoError(t, err)
	fa.waitPrompt(2 * time.Second)

	cancel()
	got := drainEvents(t, events)
	require.NotEmpty(t, got)
	_, isErr := got[len(got)-1].(agentruntime.ErrorEvent)
	assert.True(t, isErr, "ctx cancel must end the turn with an error event, got %T", got[len(got)-1])
	assert.ErrorIs(t, result.StopErr, context.Canceled)
}

// TestRun_LoadFailurePropagatesReadableError session/load 失败(agent 侧会话
// 已不在)要带 ErrSessionNotFound 哨兵透传,chat_svc 才能清掉落库的
// provider_session_id。
func TestRun_LoadFailurePropagatesReadableError(t *testing.T) {
	installFakeAgent(t, fakeAgentOptions{loadSession: true, loadFails: true})
	r := New()
	_, _, err := r.Run(context.Background(), agentruntime.RunRequest{
		Backend: acpTestBackend(), SessionID: 15, UserText: "x", Cwd: t.TempDir(),
		ProviderSessionID: "gone-session",
	})
	require.Error(t, err)
	assert.ErrorIs(t, err, agentruntime.ErrSessionNotFound)
	assert.Contains(t, err.Error(), "session not found")
}

// TestRun_NilBackend nil backend / 空 acpCommand 直接报错。
func TestRun_NilBackend(t *testing.T) {
	installFakeAgent(t, fakeAgentOptions{})
	r := New()
	_, _, err := r.Run(context.Background(), agentruntime.RunRequest{SessionID: 1})
	assert.Error(t, err)

	b := acpTestBackend()
	b.ACPCommand = ""
	_, _, err = r.Run(context.Background(), agentruntime.RunRequest{Backend: b, SessionID: 1})
	assert.Error(t, err)
}
