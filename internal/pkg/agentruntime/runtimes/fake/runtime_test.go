package fake

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

func TestRun_EchoesPromptThenDone(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r := New()
	events, result, err := r.Run(ctx, agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{ID: 1, Type: string(agent_backend_entity.TypeClaudeCode)},
		SessionID: 42,
		UserText:  "ping",
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	var text string
	var sawDone bool
	for ev := range events {
		switch e := ev.(type) {
		case agentruntime.TextDelta:
			text += e.Text
		case agentruntime.Done:
			sawDone = true
		}
	}

	assert.Equal(t, ReplyPrefix+"ping", text)
	assert.True(t, sawDone)
	assert.Equal(t, "e2e-fake-42", result.ProviderSessionID)
	assert.Equal(t, "e2e-fake-model", result.Model)
	// 上报上下文窗口,否则 chat_svc 的 resolveContextWindowWithRuntime 拿不到值
	// (fake 的 model 不在 llmcatalog 里),session.ContextWindow 留 0,
	// 前端底栏的 ContextMeter 因 max==0 整块不渲染。取 1M 顺带覆盖 formatTokens 的 M 档。
	assert.Equal(t, ContextWindowTokens, result.ContextWindow)
	assert.Equal(t, 1_000_000, ContextWindowTokens)
}

func TestRun_RespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before draining

	r := New()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{SessionID: 7, UserText: "hello world this is a long enough prompt to span several chunks"})
	require.NoError(t, err)

	// Draining a pre-canceled run must terminate (channel closes) without hanging.
	for range events { //nolint:revive // draining
	}
}

func TestRun_HonorsChunkDelayEnv(t *testing.T) {
	t.Setenv("AGENTRE_E2E_FAKE_CHUNK_DELAY_MS", "25")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r := New()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{
		SessionID: 7,
		UserText:  "hello world this is long enough to span chunks",
	})
	require.NoError(t, err)

	first := <-events
	_, ok := first.(agentruntime.TextDelta)
	require.True(t, ok)
	start := time.Now()

	second := <-events
	_, ok = second.(agentruntime.TextDelta)
	require.True(t, ok)
	assert.GreaterOrEqual(t, time.Since(start), 20*time.Millisecond)
}

// CapMCPTools 必须声明,才能让 MCP 工具注入接缝(org/subagent/hook)在 e2e 里生效。
// CapSetPermission 必须声明,否则 chat-panel 的 isModeSwitchable 为假、
// PermissionModePill 整块不渲染,底栏的极窄档降级在 e2e 里就没有观测对象。
func TestCapabilities_DeclaresMCPTools(t *testing.T) {
	caps := New().Capabilities()
	assert.True(t, caps.Has(capability.CapMCPTools))
	assert.True(t, caps.Has(capability.CapAbort))
	assert.True(t, caps.Has(capability.CapSetPermission))
}

// System prompt 断言指令只服务本地 e2e:用真实 RunRequest.SystemPrompt 证明主持人提示确实
// 注入,再经普通 fake 回复暴露成 UI/DB 可观测文本。
func TestRun_ReportsSystemPromptNeedle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r := New()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{
		SessionID:      20,
		UserText:       "(来自 用户)\ne2e-assert-system:E2E_SYSTEM_SENTINEL",
		SystemPrompt:   "主持人提示:E2E_SYSTEM_SENTINEL; .agentre/handoff/5/",
		MCPServers:     nil,
		PermissionMode: "",
	})
	require.NoError(t, err)

	var text string
	for ev := range events {
		if delta, ok := ev.(agentruntime.TextDelta); ok {
			text += delta.Text
		}
	}
	assert.Contains(t, text, "e2e-system-ok:E2E_SYSTEM_SENTINEL")
}

func TestRun_ReportsMissingSystemPromptNeedle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r := New()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{
		SessionID:    21,
		UserText:     "(来自 用户)\ne2e-assert-system:E2E_SYSTEM_SENTINEL",
		SystemPrompt: "主持人提示:别的内容",
	})
	require.NoError(t, err)

	var text string
	for ev := range events {
		if delta, ok := ev.(agentruntime.TextDelta); ok {
			text += delta.Text
		}
	}
	assert.Contains(t, text, "e2e-system-missing:E2E_SYSTEM_SENTINEL")
}

func TestRun_ReportsWhetherCwdMatchesDirective(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cwd      string
		expected string
	}{
		{name: "when cwd matches, then reports the resolved project path", cwd: "/work/project", expected: "e2e-cwd-ok:/work/project"},
		{name: "when cwd differs, then reports the actual runtime path", cwd: "/work/other", expected: "e2e-cwd-mismatch:/work/other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			events, _, err := New().Run(ctx, agentruntime.RunRequest{
				SessionID: 22,
				UserText:  "e2e-assert-cwd:/work/project",
				Cwd:       tc.cwd,
			})
			require.NoError(t, err)

			var text string
			for ev := range events {
				if delta, ok := ev.(agentruntime.TextDelta); ok {
					text += delta.Text
				}
			}
			assert.Contains(t, text, tc.expected)
		})
	}
}

// e2e-ask:<question> → fake emit 一条未答的 UserAskRequest(带问题/选项)后 Done。
// chat_svc finalize 据此把 ask 标 expired(失效终态 e2e 的产出端)。
func TestRun_EmitsUserAskRequestOnDirective(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r := New()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{
		SessionID: 30,
		UserText:  "(来自 用户)\ne2e-ask:要继续吗?",
	})
	require.NoError(t, err)

	var ask *agentruntime.UserAskRequest
	var sawDone bool
	for ev := range events {
		switch e := ev.(type) {
		case agentruntime.UserAskRequest:
			cp := e
			ask = &cp
		case agentruntime.Done:
			sawDone = true
		}
	}
	require.NotNil(t, ask, "must emit a UserAskRequest")
	require.Len(t, ask.Questions, 1)
	assert.Equal(t, "要继续吗?", ask.Questions[0].Question)
	assert.NotEmpty(t, ask.RequestID)
	assert.True(t, sawDone, "Done must follow the ask (turn finalizes unanswered)")
}

// 无 e2e-ask 指令 → 不 emit UserAskRequest(普通回显轮不应误触发失效卡)。
func TestRun_NoUserAskWithoutDirective(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r := New()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{SessionID: 31, UserText: "ping"})
	require.NoError(t, err)
	for ev := range events {
		if _, ok := ev.(agentruntime.UserAskRequest); ok {
			t.Fatal("must not emit UserAskRequest without e2e-ask directive")
		}
	}
}

// e2e-hook-create:<name> + 注入 hook 工具 → fake 调一次 hook_create(必填四段齐全)。
func TestRun_PostsHookCreateOnDirective(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	srv, snapshot := toolCaptureServer(t)

	r := New()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{
		SessionID: 26,
		UserText:  "e2e-hook-create:夜间巡检",
		MCPServers: []agentruntime.MCPServerSpec{{
			Name:    "hook",
			URL:     srv.URL + "/mcp/hook/",
			Headers: map[string]string{"Authorization": "Bearer tok"},
			Tools:   []string{"hook_create"},
		}},
	})
	require.NoError(t, err)
	for range events { //nolint:revive // draining
	}

	calls := snapshot()
	require.Len(t, calls["hook_create"], 1)
	args := calls["hook_create"][0]
	assert.Equal(t, "夜间巡检", args["name"])
	assert.Equal(t, "bash", args["interpreter"])
	assert.NotEmpty(t, args["command"])
	assert.NotEmpty(t, args["scheduleExpr"])
}

// e2e-bg-task:<label> → 本轮正常收尾后,fake 经 AutonomousTurns(sessionID) 推一轮
// 「自主续轮」(镜像 claudecode 后台任务完成后 CLI 自主跑的一轮),其事件流带
// AutonomousOutputPrefix+<label> 标记文本。
func TestAutonomousTurns_DeliversTurnOnBackgroundTaskDirective(t *testing.T) {
	t.Setenv("AGENTRE_E2E_FAKE_AUTOTURN_DELAY_MS", "10")
	t.Setenv("AGENTRE_E2E_FAKE_AUTOTURN_CHUNK_MS", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := New()
	// 订阅先于 Run —— 镜像 chat_svc:runTurn 拿到 Run 的 channel 后立刻 startAutonomousWatcher。
	turns := r.AutonomousTurns(50)

	events, _, err := r.Run(ctx, agentruntime.RunRequest{
		SessionID: 50,
		UserText:  "(来自 用户)\ne2e-bg-task:nightly",
	})
	require.NoError(t, err)
	for range events { //nolint:revive // draining
	}

	var at agentruntime.AutonomousTurn
	select {
	case at = <-turns:
	case <-time.After(3 * time.Second):
		t.Fatal("expected an AutonomousTurn after the e2e-bg-task directive")
	}
	assert.Equal(t, "background_task", at.Trigger)
	require.NotNil(t, at.Result)
	require.NotNil(t, at.Events)

	var text string
	var sawDone bool
	for ev := range at.Events {
		switch e := ev.(type) {
		case agentruntime.TextDelta:
			text += e.Text
		case agentruntime.Done:
			sawDone = true
		}
	}
	assert.Contains(t, text, AutonomousOutputPrefix+"nightly")
	assert.True(t, sawDone, "autonomous turn must finish with Done")
}

// 无 e2e-bg-task 指令 → 不推自主续轮(普通回显轮不应凭空多出一条 assistant 轮)。
func TestAutonomousTurns_NoTurnWithoutDirective(t *testing.T) {
	t.Setenv("AGENTRE_E2E_FAKE_AUTOTURN_DELAY_MS", "10")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	r := New()
	turns := r.AutonomousTurns(51)
	events, _, err := r.Run(ctx, agentruntime.RunRequest{SessionID: 51, UserText: "ping"})
	require.NoError(t, err)
	for range events { //nolint:revive // draining
	}

	select {
	case at, ok := <-turns:
		if ok {
			t.Fatalf("must not deliver an AutonomousTurn without the directive: %+v", at)
		}
	case <-time.After(300 * time.Millisecond):
	}
}

// 从未跑过 turn 的会话调 AutonomousTurns 必须安全:拿到一个空闲 channel,不 panic 不阻塞。
func TestAutonomousTurns_IdleForUnknownSession(t *testing.T) {
	r := New()
	turns := r.AutonomousTurns(9999)
	require.NotNil(t, turns)
	select {
	case at, ok := <-turns:
		if ok {
			t.Fatalf("unknown session must stay idle, got %+v", at)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

// fake 必须真正满足 chat_svc 惰性类型断言的接口,否则 watcher 根本不会挂上。
func TestRuntime_ImplementsAutonomousTurnSource(t *testing.T) {
	var _ agentruntime.AutonomousTurnSource = New()
}

// 自主续轮进行中(没有用户轮)时 Steer 必须报 ErrNoActiveTurn —— 镜像真实 claudecode
// (a.inTurn=false)。chat_svc 把它翻成 ChatSteerNoActive,前端据此从"排队"回退成
// "另起一轮 Send",这正是"自主轮流式中用户再发一条"的真实链路。
func TestSteer_WithoutActiveUserTurn_ReturnsErrNoActiveTurn(t *testing.T) {
	r := New()
	err := r.Steer(context.Background(), 60, "q1", "hi")
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

// 用户轮进行中 → 不是 ErrNoActiveTurn(fake 不支持真正的 mid-turn 注入,报别的错),
// 否则前端会在正常流式中被误导成"另起一轮"。
func TestSteer_DuringUserTurn_IsNotNoActiveTurn(t *testing.T) {
	t.Setenv("AGENTRE_E2E_FAKE_CHUNK_DELAY_MS", "50")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := New()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{
		SessionID: 61,
		UserText:  "hello world this is long enough to span several chunks",
	})
	require.NoError(t, err)
	<-events // 轮已开始

	steerErr := r.Steer(ctx, 61, "q1", "hi")
	assert.Error(t, steerErr)
	assert.NotErrorIs(t, steerErr, agentruntime.ErrNoActiveTurn)

	cancel()
	for range events { //nolint:revive // draining
	}
}

// toolCaptureServer 收集本轮 fake 发出的全部 tools/call,按 tool 名归档参数。
func toolCaptureServer(t *testing.T) (*httptest.Server, func() map[string][]map[string]any) {
	t.Helper()
	var mu sync.Mutex
	calls := map[string][]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var rpc struct {
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		require.NoError(t, json.Unmarshal(b, &rpc))
		mu.Lock()
		calls[rpc.Params.Name] = append(calls[rpc.Params.Name], rpc.Params.Arguments)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"ok"}]}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, func() map[string][]map[string]any {
		mu.Lock()
		defer mu.Unlock()
		out := map[string][]map[string]any{}
		for k, v := range calls {
			out[k] = append([]map[string]any(nil), v...)
		}
		return out
	}
}

// ToolPermission 审批接缝(web 端到端「批准一次工具调用」的接缝):
// e2e-tool-permission:<tool> → ToolPermissionRequest → 阻塞 → submitToolPermission
// 投回 → ToolPermissionResolved → Done。pendingWaiters 在这期间要报得出这条 waiter。
func TestRun_ToolPermissionBlockingRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r := New()
	events, _, err := r.Run(ctx, agentruntime.RunRequest{
		SessionID: 42,
		UserText:  "e2e-tool-permission:bash",
	})
	require.NoError(t, err)

	var text string
	var sawRequest, sawResolved, sawDone bool
	var request agentruntime.ToolPermissionRequest
	// 先收文本回复 + ToolPermissionRequest;此时不应有 Done。
	for !sawRequest {
		select {
		case ev := <-events:
			switch e := ev.(type) {
			case agentruntime.TextDelta:
				text += e.Text
			case agentruntime.ToolPermissionRequest:
				sawRequest = true
				request = e
			case agentruntime.Done:
				t.Fatal("Done 不该在审批通过前到达")
			}
		case <-ctx.Done():
			t.Fatal("等待 ToolPermissionRequest 超时")
		}
	}
	assert.Equal(t, "e2e-tp-42", request.RequestID)
	assert.Equal(t, "bash", request.ToolName)

	// 阻塞期间 pendingWaiters 必须报得出这条 waiter(R10 的「正在等待输入」)。
	snap := r.PendingWaiters(ctx, 42)
	require.Len(t, snap.ToolPermissions, 1)
	assert.Equal(t, "e2e-tp-42", snap.ToolPermissions[0].RequestID)
	assert.Equal(t, "bash", snap.ToolPermissions[0].ToolName)

	// 投回允许决策 → ToolPermissionResolved + Done 随后来。
	require.NoError(t, r.SubmitToolPermission(ctx, 42, "e2e-tp-42", true, false, ""))
	for !sawDone {
		select {
		case ev := <-events:
			switch e := ev.(type) {
			case agentruntime.ToolPermissionResolved:
				sawResolved = true
				assert.Equal(t, "e2e-tp-42", e.RequestID)
				assert.True(t, e.Allowed)
				assert.False(t, e.AlwaysAllow)
			case agentruntime.Done:
				sawDone = true
			}
		case <-ctx.Done():
			t.Fatal("等待 ToolPermissionResolved/Done 超时")
		}
	}
	assert.True(t, sawResolved)
	assert.Equal(t, ReplyPrefix+"e2e-tool-permission:bash", text)
	// 决策已投回,pending 应已清空。
	assert.Empty(t, r.PendingWaiters(ctx, 42).ToolPermissions)
}

// 同一 requestID 重复提交(daemon 的 idempotentSubmitResult 语义)与「waiter 已消失」
// 都必须幂等成功,不能把一轮已经收尾的会话报成错误(R8)。
func TestRun_SubmitToolPermission_IdempotentWhenWaiterGone(t *testing.T) {
	ctx := context.Background()
	r := New()
	require.NoError(t, r.SubmitToolPermission(ctx, 999, "e2e-tp-999", true, false, ""),
		"没有 pending 的会话提交审批必须幂等成功")
	assert.Empty(t, r.PendingWaiters(ctx, 999).ToolPermissions)
}
