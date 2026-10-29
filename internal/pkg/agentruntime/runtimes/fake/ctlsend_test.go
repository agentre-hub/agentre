package fake

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// ctlSendProbe 是被测 fake 的对端:记下 POST /ctl/v1/send 的鉴权与请求体,并在回复前
// 阻塞到测试放行,这样测试能断言「send 在途时 shell 调用还开着」。
type ctlSendProbe struct {
	srv     *httptest.Server
	hit     chan struct{}
	release chan struct{}
	mu      sync.Mutex
	auth    string
	path    string
	body    map[string]any
}

func newCtlSendProbe(t *testing.T) *ctlSendProbe {
	t.Helper()
	p := &ctlSendProbe{hit: make(chan struct{}), release: make(chan struct{})}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		p.mu.Lock()
		p.auth, p.path, p.body = r.Header.Get("Authorization"), r.URL.Path, body
		p.mu.Unlock()
		close(p.hit)
		<-p.release
		_ = json.NewEncoder(w).Encode(map[string]any{"sessionId": 9, "text": "child-final-text", "done": true})
	}))
	t.Cleanup(p.srv.Close)
	agentruntime.RegisterCtlCredentialSource(func(_, _ int64) agentruntime.CtlCredentials {
		return agentruntime.CtlCredentials{Endpoint: p.srv.URL, Token: "sess-token"}
	})
	t.Cleanup(func() { agentruntime.RegisterCtlCredentialSource(nil) })
	return p
}

func startCtlSend(t *testing.T, directive string) (<-chan agentruntime.Event, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	events, _, err := New().Run(ctx, agentruntime.RunRequest{SessionID: 70, AgentID: 3, UserText: directive})
	require.NoError(t, err)
	return events, cancel
}

func nextToolEvent(t *testing.T, events <-chan agentruntime.Event) agentruntime.Event {
	t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			require.True(t, ok, "events closed before a tool event")
			switch ev.(type) {
			case agentruntime.ToolCall, agentruntime.ToolResult:
				return ev
			}
		case <-timeout:
			t.Fatal("timed out waiting for a tool event")
		}
	}
}

func TestRun_CtlSendDirective_ForegroundKeepsShellOpenUntilSendReturns(t *testing.T) {
	p := newCtlSendProbe(t)
	events, cancel := startCtlSend(t, "e2e-agrctl-send:fg:5:lbl")
	defer cancel()

	call, ok := nextToolEvent(t, events).(agentruntime.ToolCall)
	require.True(t, ok, "first tool event must be the Bash ToolCall")
	assert.Equal(t, "Bash", call.Name)
	var in map[string]any
	require.NoError(t, json.Unmarshal(call.Input, &in))
	cmd, _ := in["command"].(string)
	for _, want := range []string{"agrctl", "send", "5", "--wait", "lbl"} {
		assert.Contains(t, cmd, want)
	}
	assert.NotContains(t, in, "run_in_background")

	select {
	case <-p.hit:
	case <-time.After(3 * time.Second):
		t.Fatal("fake never POSTed /ctl/v1/send")
	}
	p.mu.Lock()
	assert.Equal(t, "Bearer sess-token", p.auth)
	assert.Equal(t, "/ctl/v1/send", p.path)
	assert.Equal(t, true, p.body["wait"])
	assert.EqualValues(t, 5, p.body["agentId"])
	assert.True(t, strings.Contains(p.body["text"].(string), "lbl"))
	p.mu.Unlock()

	// send 还在途:shell 调用不能已经有结果。
	select {
	case ev := <-events:
		if _, isResult := ev.(agentruntime.ToolResult); isResult {
			t.Fatal("foreground ToolResult emitted before send returned")
		}
	case <-time.After(100 * time.Millisecond):
	}
	close(p.release)

	res, ok := nextToolEvent(t, events).(agentruntime.ToolResult)
	require.True(t, ok)
	assert.Equal(t, call.ID, res.ToolCallID)
	assert.Contains(t, res.Content, "child-final-text")
	assert.False(t, res.IsError)
}

func TestRun_CtlSendDirective_BackgroundMarksCallAndResultsImmediately(t *testing.T) {
	p := newCtlSendProbe(t)
	events, cancel := startCtlSend(t, "e2e-agrctl-send:bg:5:lbl")
	defer cancel()

	call, ok := nextToolEvent(t, events).(agentruntime.ToolCall)
	require.True(t, ok)
	var in map[string]any
	require.NoError(t, json.Unmarshal(call.Input, &in))
	assert.Equal(t, true, in["run_in_background"])

	// 后台 Bash 立刻拿到结果,与 send 在途并存。
	res, ok := nextToolEvent(t, events).(agentruntime.ToolResult)
	require.True(t, ok)
	assert.Equal(t, call.ID, res.ToolCallID)
	select {
	case <-p.hit:
	case <-time.After(3 * time.Second):
		t.Fatal("fake never POSTed /ctl/v1/send")
	}
	close(p.release)
	var sawDone bool
	for ev := range events {
		if _, ok := ev.(agentruntime.Done); ok {
			sawDone = true
		}
	}
	assert.True(t, sawDone, "turn must finish only after send returns")
}

func TestRun_CtlSendDirective_LateCallIsRegisteredOnlyAfterTheSendAndTheChildStarted(t *testing.T) {
	t.Setenv("AGENTRE_E2E_FAKE_STEP_DELAY_MS", "1")
	p := newCtlSendProbe(t)
	events, cancel := startCtlSend(t, "e2e-agrctl-send:late:5:lbl")
	defer cancel()

	select {
	case <-p.hit:
	case <-time.After(3 * time.Second):
		t.Fatal("fake never POSTed /ctl/v1/send")
	}
	// send 已到、子会话还没开始干活:shell 调用还不能登记(Codex 的 command_execution 在
	// 命令跑起来之后才开始)。
	select {
	case ev := <-events:
		if _, isCall := ev.(agentruntime.ToolCall); isCall {
			t.Fatal("late variant registered the shell call before the child session started")
		}
	case <-time.After(100 * time.Millisecond):
	}

	// 目标 agent 的子会话(同一个 fake)开始出步骤之后,shell 调用才登记。
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	childEvents, _, err := New().Run(ctx, agentruntime.RunRequest{SessionID: 71, UserText: "e2e-child-steps:2 lbl"})
	require.NoError(t, err)
	go func() {
		for range childEvents {
		}
	}()

	call, ok := nextToolEvent(t, events).(agentruntime.ToolCall)
	require.True(t, ok, "the shell call must follow the child's first step")
	var in map[string]any
	require.NoError(t, json.Unmarshal(call.Input, &in))
	cmd, _ := in["command"].(string)
	for _, want := range []string{"agrctl", "send", "5", "--wait", "lbl"} {
		assert.Contains(t, cmd, want)
	}
	assert.NotContains(t, in, "run_in_background")

	close(p.release)
	res, ok := nextToolEvent(t, events).(agentruntime.ToolResult)
	require.True(t, ok)
	assert.Equal(t, call.ID, res.ToolCallID)
	assert.Contains(t, res.Content, "child-final-text")
}

func TestRun_CtlSendDirective_LateCallStillResultsWhenTheSendFails(t *testing.T) {
	agentruntime.RegisterCtlCredentialSource(func(_, _ int64) agentruntime.CtlCredentials {
		return agentruntime.CtlCredentials{}
	})
	t.Cleanup(func() { agentruntime.RegisterCtlCredentialSource(nil) })
	events, cancel := startCtlSend(t, "e2e-agrctl-send:late:5:lbl")
	defer cancel()

	call, ok := nextToolEvent(t, events).(agentruntime.ToolCall)
	require.True(t, ok)
	res, ok := nextToolEvent(t, events).(agentruntime.ToolResult)
	require.True(t, ok)
	assert.Equal(t, call.ID, res.ToolCallID)
	assert.True(t, res.IsError)
}

func TestRun_CtlSendDirective_SendFailureIsAnErrorResult(t *testing.T) {
	agentruntime.RegisterCtlCredentialSource(func(_, _ int64) agentruntime.CtlCredentials {
		return agentruntime.CtlCredentials{}
	})
	t.Cleanup(func() { agentruntime.RegisterCtlCredentialSource(nil) })
	events, cancel := startCtlSend(t, "e2e-agrctl-send:fg:5:lbl")
	defer cancel()

	_, _ = nextToolEvent(t, events).(agentruntime.ToolCall)
	res, ok := nextToolEvent(t, events).(agentruntime.ToolResult)
	require.True(t, ok)
	assert.True(t, res.IsError)
}

func TestRun_ChildStepsDirective_EmitsPairedBashStepsBeforeDone(t *testing.T) {
	t.Setenv("AGENTRE_E2E_FAKE_STEP_DELAY_MS", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	events, _, err := New().Run(ctx, agentruntime.RunRequest{SessionID: 71, UserText: "e2e-child-steps:2 lbl"})
	require.NoError(t, err)

	var order []string
	ids := map[string]bool{}
	for ev := range events {
		switch e := ev.(type) {
		case agentruntime.ToolCall:
			order = append(order, "call")
			assert.Equal(t, "Bash", e.Name)
			assert.False(t, ids[e.ID], "step ids must be unique")
			ids[e.ID] = true
		case agentruntime.ToolResult:
			order = append(order, "result")
			assert.True(t, ids[e.ToolCallID])
		case agentruntime.Done:
			order = append(order, "done")
		}
	}
	assert.Equal(t, []string{"call", "result", "call", "result", "done"}, order)
}
