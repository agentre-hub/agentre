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

// desktopOwnedWithTurn 是桌面端拥有、当前轮为 tt 的会话,经 tunnel 转回桌面端。
func desktopOwnedWithTurn(t *testing.T, tt *turnTranscript, tunnel *recordingCtlTunnel) (*CtlSessions, string, *httptest.Server) {
	t.Helper()
	c, tok := desktopOwned(t)
	c.attachTurn(ctlTestRID, tt)
	srv := httptest.NewServer(NewCtlProxyHandler(CtlProxyDeps{
		Sessions: c, Tunnel: func(devicefp.Initiator, string) NotifierPort { return tunnel },
	}))
	t.Cleanup(srv.Close)
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
