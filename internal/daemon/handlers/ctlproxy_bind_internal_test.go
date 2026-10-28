package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// ctlproxy_bind_internal_test.go 钉住远端父会话的绑定(spec 2026-10-27「agrctl send 子代理卡」
// 「Remote sessions」第 1 条、Binding 规则 1-5、Design 12):父轮的宿主是 agentred,所以由 ctl
// 代理在转发 `send --wait` 之前,在这条会话当前这一轮里匹配那条外层 shell 调用,把调用 ID
// (与是否后台)附在转给桌面端的请求头里;控制台拥有的会话一律不绑;调用方自带的同名头一律
// 不放行 —— 不能确定时宁可不绑,也不挂错卡。

const bindTestCommand = `agrctl send --wait --agent reviewer "审一下 PR 42"`

// recordingCtlTunnel 记下每一次经隧道转给桌面端的请求;answer 给应答(缺席 = 200)。
type recordingCtlTunnel struct {
	mu     sync.Mutex
	reqs   []wire.MCPProxyRequest
	answer func(n int) wire.MCPProxyResponse
}

func (f *recordingCtlTunnel) Notify(*agentrewire.RpcNotification) error { return nil }

func (f *recordingCtlTunnel) Request(_ context.Context, _ string, params, result any) error {
	f.mu.Lock()
	f.reqs = append(f.reqs, params.(wire.MCPProxyRequest))
	n := len(f.reqs)
	f.mu.Unlock()
	resp := wire.MCPProxyResponse{Status: http.StatusOK, Body: []byte(`{"done":true}`)}
	if f.answer != nil {
		resp = f.answer(n)
	}
	*(result.(*wire.MCPProxyResponse)) = resp
	return nil
}

func (f *recordingCtlTunnel) forwarded(t *testing.T, i int) wire.MCPProxyRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Greater(t, len(f.reqs), i, "request %d was not forwarded", i)
	return f.reqs[i]
}

// bindTurn 造这条会话此刻在跑的那一轮(真实的 *turnTranscript),外层调用经生产的
// dispatcher 落进它的累加器。
func bindTurn(t *testing.T, calls ...agentruntime.ToolCall) *turnTranscript {
	t.Helper()
	tt := &turnTranscript{
		port:       &memTranscript{},
		msg:        &transcript_entity.Message{ID: 31, SessionID: 12, Role: "assistant", Seq: 2, BlocksJSON: "[]"},
		acc:        turn.New(),
		dispatcher: transcript.NewTurnDispatcher(transcript.Adapters{}),
		done:       make(chan struct{}),
	}
	tt.turnCtx = tt.newTurnContext()
	for _, c := range calls {
		require.NoError(t, tt.dispatcher.Apply(context.Background(), c, tt.acc, discardEmitter{}, tt.turnCtx))
	}
	return tt
}

func shellCall(id, command string, background bool) agentruntime.ToolCall {
	in := map[string]any{"command": command}
	if background {
		in["run_in_background"] = true
	}
	raw, _ := json.Marshal(in)
	return agentruntime.ToolCall{ID: id, Name: "Bash", Input: raw}
}

// desktopOwnedWithTurn 是桌面端拥有、当前轮为 tt 的会话,经 tunnel 转回桌面端。找不到
// shell 调用的 send 只等一小会儿:这里的用例不关心那条调用会不会晚到。
func desktopOwnedWithTurn(t *testing.T, tt *turnTranscript, tunnel *recordingCtlTunnel) (*CtlSessions, string, *httptest.Server) {
	t.Helper()
	return desktopOwnedWithTurnWaiting(t, tt, tunnel, 50*time.Millisecond)
}

// desktopOwnedWithTurnWaiting 同上,send 等那条 shell 调用出现的上限为 wait。
func desktopOwnedWithTurnWaiting(t *testing.T, tt *turnTranscript, tunnel *recordingCtlTunnel, wait time.Duration) (*CtlSessions, string, *httptest.Server) {
	t.Helper()
	c, tok := desktopOwned(t)
	c.attachTurn(ctlTestRID, tt)
	srv := httptest.NewServer(NewCtlProxyHandler(CtlProxyDeps{
		Sessions: c, Tunnel: func(devicefp.Initiator, string) NotifierPort { return tunnel },
		SendBindWait: wait,
	}))
	t.Cleanup(func() {
		// 用例失败时还挂着的 send 随连接断开收场,不拖住 Close。
		srv.CloseClientConnections()
		srv.Close()
	})
	return c, tok, srv
}

func observeHandlerLogs(t *testing.T) *observer.ObservedLogs {
	core, logs := observer.New(zapcore.InfoLevel)
	old := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(old) })
	return logs
}

func ctlProxyNotBoundReasons(logs *observer.ObservedLogs) []string {
	entries := logs.FilterMessage("handlers.ctlProxy.forwardToDesktop: subagent card not bound").All()
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ContextMap()["reason"].(string))
	}
	return out
}

const reviewerWaitBody = `{"agent":"reviewer","agentId":0,"text":"审一下 PR 42","wait":true}`

func TestCtlProxy_GivenDesktopOwnedForegroundShellCall_WhenSendWaitIsForwarded_ThenTheCallIdTravelsToTheDesktopOnlyOnce(t *testing.T) {
	logs := observeHandlerLogs(t)
	tunnel := &recordingCtlTunnel{}
	_, tok, srv := desktopOwnedWithTurn(t, bindTurn(t, shellCall("sh-1", bindTestCommand, false)), tunnel)

	status, _, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusOK, status)

	fwd := tunnel.forwarded(t, 0)
	assert.Equal(t, []string{"sh-1"}, fwd.Headers[ctlBindToolCallHeader], "the desktop learns which shell call agentred bound")
	assert.Empty(t, fwd.Headers[ctlBindBackgroundHeader], "a foreground call carries no background flag")
	assert.JSONEq(t, reviewerWaitBody, string(fwd.Body), "the request body itself is untouched")

	// 同一条命令里第二个 send --wait:这条调用已经绑过,不再绑定(规则 5)。
	status, _, _ = postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, tunnel.forwarded(t, 1).Headers[ctlBindToolCallHeader])
	assert.Equal(t, []string{"already_bound"}, ctlProxyNotBoundReasons(logs))
	assert.NotContains(t, logText(logs), ctlTestDesktopToken)
	assert.NotContains(t, logText(logs), tok)
}

func TestCtlProxy_GivenDesktopOwnedBackgroundShellCall_WhenSendWaitIsForwarded_ThenTheBackgroundFlagTravelsToo(t *testing.T) {
	tunnel := &recordingCtlTunnel{}
	_, tok, srv := desktopOwnedWithTurn(t, bindTurn(t, shellCall("sh-bg", bindTestCommand, true)), tunnel)

	status, _, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusOK, status)

	fwd := tunnel.forwarded(t, 0)
	assert.Equal(t, []string{"sh-bg"}, fwd.Headers[ctlBindToolCallHeader])
	assert.Equal(t, []string{"true"}, fwd.Headers[ctlBindBackgroundHeader])
}

func TestCtlProxy_GivenNoSafeBinding_WhenSendIsForwarded_ThenNoBindingTravelsAndForgedHeadersAreDropped(t *testing.T) {
	cases := []struct {
		name   string
		calls  []agentruntime.ToolCall
		body   string
		reason string
	}{
		{"no wait", []agentruntime.ToolCall{shellCall("sh-1", bindTestCommand, false)},
			`{"agent":"reviewer","text":"审一下 PR 42","wait":false}`, "no_wait"},
		{"no candidate", []agentruntime.ToolCall{shellCall("sh-1", "ls -la", false)}, reviewerWaitBody, "no_candidate"},
		{"ambiguous", []agentruntime.ToolCall{
			shellCall("sh-1", bindTestCommand, false), shellCall("sh-2", bindTestCommand, false),
		}, reviewerWaitBody, "ambiguous"},
		{"existing session", []agentruntime.ToolCall{shellCall("sh-1", bindTestCommand, false)},
			`{"sessionId":12,"agent":"reviewer","text":"审一下 PR 42","wait":true}`, "existing_session"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := observeHandlerLogs(t)
			tunnel := &recordingCtlTunnel{}
			_, tok, srv := desktopOwnedWithTurn(t, bindTurn(t, tc.calls...), tunnel)

			req, err := http.NewRequest(http.MethodPost, srv.URL+"/ctl/v1/send", strings.NewReader(tc.body))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+tok)
			req.Header.Set(ctlBindToolCallHeader, "sh-forged")
			req.Header.Set(ctlBindBackgroundHeader, "true")
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			_ = resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			fwd := tunnel.forwarded(t, 0)
			assert.Empty(t, fwd.Headers[ctlBindToolCallHeader], "a caller cannot name the shell call itself")
			assert.Empty(t, fwd.Headers[ctlBindBackgroundHeader])
			assert.Equal(t, []string{tc.reason}, ctlProxyNotBoundReasons(logs))
		})
	}
}

func TestCtlProxy_GivenNoTurnRunning_WhenSendWaitIsForwarded_ThenNothingIsBound(t *testing.T) {
	logs := observeHandlerLogs(t)
	tunnel := &recordingCtlTunnel{}
	c, tok := desktopOwned(t)
	srv := httptest.NewServer(NewCtlProxyHandler(CtlProxyDeps{
		Sessions: c, Tunnel: func(devicefp.Initiator, string) NotifierPort { return tunnel },
	}))
	defer srv.Close()

	status, _, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, tunnel.forwarded(t, 0).Headers[ctlBindToolCallHeader])
	assert.Equal(t, []string{"no_active_turn"}, ctlProxyNotBoundReasons(logs))
}

func TestCtlProxy_GivenFirstSendFails_WhenTheSameCallSendsAgain_ThenTheSecondOneBinds(t *testing.T) {
	// 绑定以 send 成功为准(规则 4):agent 名写错导致桌面端拒绝时,这条调用没有绑上子会话,
	// 同一条命令里的下一个 send --wait 照样可以绑。
	tunnel := &recordingCtlTunnel{answer: func(n int) wire.MCPProxyResponse {
		if n == 1 {
			return wire.MCPProxyResponse{Status: http.StatusNotFound, Body: []byte(`{"error":"agent not found"}`)}
		}
		return wire.MCPProxyResponse{Status: http.StatusOK, Body: []byte(`{"done":true}`)}
	}}
	_, tok, srv := desktopOwnedWithTurn(t, bindTurn(t, shellCall("sh-1", bindTestCommand, false)), tunnel)

	status, _, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusNotFound, status)
	status, _, _ = postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, []string{"sh-1"}, tunnel.forwarded(t, 0).Headers[ctlBindToolCallHeader])
	assert.Equal(t, []string{"sh-1"}, tunnel.forwarded(t, 1).Headers[ctlBindToolCallHeader])
}

func TestCtlProxy_GivenNextTurnStarted_WhenSendWaitArrives_ThenOnlyTheNewTurnIsMatched(t *testing.T) {
	tunnel := &recordingCtlTunnel{}
	first := bindTurn(t, shellCall("sh-1", bindTestCommand, true))
	c, tok, srv := desktopOwnedWithTurn(t, first, tunnel)

	status, _, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusOK, status)

	c.detachTurn(ctlTestRID, first)
	c.attachTurn(ctlTestRID, bindTurn(t, shellCall("sh-2", bindTestCommand, false)))
	status, _, _ = postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusOK, status)

	assert.Equal(t, []string{"sh-1"}, tunnel.forwarded(t, 0).Headers[ctlBindToolCallHeader])
	assert.Equal(t, []string{"sh-2"}, tunnel.forwarded(t, 1).Headers[ctlBindToolCallHeader])
}

func TestCtlProxy_GivenConsoleOwnedSession_WhenSendWaitArrives_ThenItIsNeverBound(t *testing.T) {
	logs := observeHandlerLogs(t)
	server := &fakeCtlServer{answer: func(string) (int, string) { return http.StatusOK, `{"done":true}` }}
	c, _, tok, srv := consoleOwned(t, server, time.Minute)
	c.attachTurn(ctlTestRID, bindTurn(t, shellCall("sh-1", bindTestCommand, false)))

	status, _, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, []string{"/v1/ctl/send"}, server.paths())
	assert.Empty(t, logs.FilterMessage("handlers.ctlProxy.forwardToDesktop: subagent card bound").All())

	// 那条调用也没有被占住:之后若这条会话改由桌面端拥有,它照样能绑。
	tunnel := &recordingCtlTunnel{}
	c.bind(ctlTestRID, ctlTestPeer, ctlTestConversation, DesktopCtlSession{DesktopSessionID: 55, Token: ctlTestDesktopToken}, true)
	h := NewCtlProxyHandler(CtlProxyDeps{Sessions: c, Tunnel: func(devicefp.Initiator, string) NotifierPort { return tunnel }})
	srv2 := httptest.NewServer(h)
	defer srv2.Close()
	status, _, _ = postCtl(t, srv2.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, []string{"sh-1"}, tunnel.forwarded(t, 0).Headers[ctlBindToolCallHeader])
}

func logText(logs *observer.ObservedLogs) string {
	var b strings.Builder
	for _, e := range logs.All() {
		b.WriteString(e.Message)
		for k, v := range e.ContextMap() {
			b.WriteString(" " + k + "=")
			if s, ok := v.(string); ok {
				b.WriteString(s)
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

// ── send 先于它那条 shell 调用到达(Codex,运行时验证 V10)───────────────────

// longBindWait 远长于任何一个用例:转发若是等到期才发生,用例会在 promptly 上失败。
const longBindWait = time.Minute

// promptly 是「没有等到期」的上限。
const promptly = 2 * time.Second

func postCtlAsync(t *testing.T, url, token, body string) <-chan int {
	done := make(chan int, 1)
	go func() {
		status, _, _ := postCtl(t, url, token, body)
		done <- status
	}()
	return done
}

func awaitCtlStatus(t *testing.T, done <-chan int, within time.Duration) int {
	t.Helper()
	select {
	case status := <-done:
		return status
	case <-time.After(within):
		t.Fatalf("the send was not forwarded within %s", within)
		return 0
	}
}

// heldSends 是此刻停在「等那条 shell 调用出现」上的 send 数。
func heldSends(c *CtlSessions) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s := c.sessions[ctlTestRID]; s != nil && s.sends != nil {
		return len(s.sends.waiting)
	}
	return 0
}

// awaitHeldSends 等到恰好 n 个 send 停在「等那条 shell 调用出现」上。
func awaitHeldSends(t *testing.T, c *CtlSessions, n int) {
	t.Helper()
	require.Eventually(t, func() bool { return heldSends(c) == n },
		5*time.Second, 5*time.Millisecond, "expected %d sends waiting for their shell call", n)
}

func TestCtlProxy_GivenSendArrivesBeforeItsShellCall_ThenTheForwardCarriesTheBinding(t *testing.T) {
	// Codex 的 command_execution 在 send 到达之后才开始:send 先到时父轮里还没有那条调用,
	// agentred 要等它出现再带着绑定转发,而不是当场按「没有候选」放行。
	tunnel := &recordingCtlTunnel{}
	tt := bindTurn(t)
	c, tok, srv := desktopOwnedWithTurnWaiting(t, tt, tunnel, longBindWait)

	done := postCtlAsync(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	awaitHeldSends(t, c, 1)
	// 先来一条不相干的调用:它不是候选,send 重新匹配之后接着等下一条。
	tt.observe(context.Background(), shellCall("sh-other", "ls -la", false))
	require.Eventually(t, func() bool {
		tt.mu.Lock()
		rearmed := tt.outerCallSeen != nil
		tt.mu.Unlock()
		return rearmed && heldSends(c) == 1
	}, 5*time.Second, 5*time.Millisecond, "the send re-matched and kept waiting")
	tt.observe(context.Background(), shellCall("sh-late", bindTestCommand, false))

	require.Equal(t, http.StatusOK, awaitCtlStatus(t, done, promptly))
	assert.Equal(t, []string{"sh-late"}, tunnel.forwarded(t, 0).Headers[ctlBindToolCallHeader],
		"the call that showed up while the send waited is the one bound")
}

func TestCtlProxy_GivenSendsShellCallNeverShowsUp_WhenTheWaitRunsOut_ThenItIsForwardedUnbound(t *testing.T) {
	logs := observeHandlerLogs(t)
	tunnel := &recordingCtlTunnel{}
	const wait = 150 * time.Millisecond
	_, tok, srv := desktopOwnedWithTurnWaiting(t, bindTurn(t), tunnel, wait)

	start := time.Now()
	status, _, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, status)
	assert.GreaterOrEqual(t, elapsed, wait, "a send without a candidate waits for its shell call first")
	assert.Less(t, elapsed, promptly, "and is forwarded as soon as the wait runs out")
	assert.Empty(t, tunnel.forwarded(t, 0).Headers[ctlBindToolCallHeader])
	assert.Equal(t, []string{"no_candidate"}, ctlProxyNotBoundReasons(logs))
}

func TestCtlProxy_GivenSendWaitingForItsShellCall_WhenTheTurnEnds_ThenItIsForwardedUnboundAtOnce(t *testing.T) {
	logs := observeHandlerLogs(t)
	tunnel := &recordingCtlTunnel{}
	tt := bindTurn(t)
	c, tok, srv := desktopOwnedWithTurnWaiting(t, tt, tunnel, longBindWait)

	done := postCtlAsync(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	awaitHeldSends(t, c, 1)
	tt.finish(context.Background(), wire.RunResultDoneFrame{ConversationID: ctlTestConversation})

	require.Equal(t, http.StatusOK, awaitCtlStatus(t, done, promptly))
	assert.Empty(t, tunnel.forwarded(t, 0).Headers[ctlBindToolCallHeader])
	assert.Equal(t, []string{"no_active_turn"}, ctlProxyNotBoundReasons(logs))
}

func TestCtlProxy_GivenTwoSendsWaiting_WhenOneShellCallShowsUpThatFitsBoth_ThenNeitherIsBound(t *testing.T) {
	// 两个 send 都在等,出现的那一条调用两边都认得:它是谁发的说不准 —— 宁可都不升级,
	// 也不能把子会话挂到另一个 send 的调用上(Hard invariant「不挂错卡」、规则 5)。
	logs := observeHandlerLogs(t)
	tunnel := &recordingCtlTunnel{}
	tt := bindTurn(t)
	c, tok, srv := desktopOwnedWithTurnWaiting(t, tt, tunnel, longBindWait)

	first := postCtlAsync(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	second := postCtlAsync(t, srv.URL+"/ctl/v1/send", tok, `{"agent":"reviewer","text":"再审一下 PR 43","wait":true}`)
	awaitHeldSends(t, c, 2)
	tt.observe(context.Background(), shellCall("sh-1", bindTestCommand, false))

	require.Equal(t, http.StatusOK, awaitCtlStatus(t, first, promptly))
	require.Equal(t, http.StatusOK, awaitCtlStatus(t, second, promptly))
	assert.Empty(t, tunnel.forwarded(t, 0).Headers[ctlBindToolCallHeader])
	assert.Empty(t, tunnel.forwarded(t, 1).Headers[ctlBindToolCallHeader])
	assert.Equal(t, []string{"ambiguous", "ambiguous"}, ctlProxyNotBoundReasons(logs))

	// 之后同一条调用里再来一个 send,也不会再把它绑出去。
	status, _, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)
	require.Equal(t, http.StatusOK, status)
	assert.Empty(t, tunnel.forwarded(t, 2).Headers[ctlBindToolCallHeader])
}

func TestCtlProxy_GivenAmbiguousOrAlreadyBoundSend_ThenItIsForwardedWithoutWaiting(t *testing.T) {
	// 只有「还没有候选」的 send 才等;有歧义、已绑过的,等下去结论也不会变。
	cases := []struct {
		name   string
		calls  []agentruntime.ToolCall
		sends  int
		reason string
	}{
		{"ambiguous", []agentruntime.ToolCall{
			shellCall("sh-1", bindTestCommand, false), shellCall("sh-2", bindTestCommand, false),
		}, 1, "ambiguous"},
		{"already bound", []agentruntime.ToolCall{shellCall("sh-1", bindTestCommand, false)}, 2, "already_bound"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logs := observeHandlerLogs(t)
			tunnel := &recordingCtlTunnel{}
			_, tok, srv := desktopOwnedWithTurnWaiting(t, bindTurn(t, tc.calls...), tunnel, longBindWait)

			var status int
			for range tc.sends {
				status = awaitCtlStatus(t, postCtlAsync(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody), promptly)
			}
			require.Equal(t, http.StatusOK, status)
			assert.Empty(t, tunnel.forwarded(t, tc.sends-1).Headers[ctlBindToolCallHeader])
			reasons := ctlProxyNotBoundReasons(logs)
			assert.Equal(t, tc.reason, reasons[len(reasons)-1])
		})
	}
}
