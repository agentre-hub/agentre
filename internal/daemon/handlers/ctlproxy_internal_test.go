package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
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
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 规格 2026-09-22 agrctl-resource-management 决策 4 + 「Executors / agentred」:agentred
// 在自己的 gateway 上挂一个 ctl 代理,按会话归属转发 —— 桌面端拥有的会话经反向隧道转回
// 桌面端,控制台拥有的会话交给 server(写入先在本会话里审批)。

const (
	ctlTestConversation = "00000000-0000-7000-8000-000000000077"
	ctlTestPeer         = devicefp.Initiator("sha256:desktop-a")
	ctlTestRID          = int64(9001)
	ctlTestDesktopToken = "SENTINEL_DESKTOP_SESSION_TOKEN"
)

// ── 会话凭证:只给本机拥有的会话签,只认自己签的 ─────────────────────────────

func TestCtlSessions_GivenBoundSession_WhenCredentialsAsked_ThenTokenResolvesBackToIt(t *testing.T) {
	c := NewCtlSessions(func() string { return "http://127.0.0.1:7777" })

	assert.Equal(t, "", c.Credentials(7, ctlTestRID).Token, "a session this daemon never ran gets no token")

	c.bind(ctlTestRID, ctlTestPeer, ctlTestConversation, DesktopCtlSession{}, false)
	cred := c.Credentials(7, ctlTestRID)
	require.Equal(t, "http://127.0.0.1:7777", cred.Endpoint, "the CLI dials this daemon's own gateway")
	require.NotEmpty(t, cred.Token)

	owner, ok := c.resolve(cred.Token)
	require.True(t, ok)
	assert.Equal(t, ctlTestConversation, owner.conversationID)
	assert.Equal(t, ctlTestPeer, owner.peer)

	other := NewCtlSessions(func() string { return "http://127.0.0.1:7777" })
	other.bind(ctlTestRID, ctlTestPeer, ctlTestConversation, DesktopCtlSession{}, false)
	_, ok = c.resolve(other.Credentials(7, ctlTestRID).Token)
	assert.False(t, ok, "a token signed by another daemon instance is not honored")
	_, ok = c.resolve("garbage")
	assert.False(t, ok)
}

func TestCtlSessions_GivenGatewayNotUp_WhenCredentialsAsked_ThenNothingIsInjected(t *testing.T) {
	c := NewCtlSessions(func() string { return "" })
	c.bind(ctlTestRID, ctlTestPeer, ctlTestConversation, DesktopCtlSession{}, false)
	assert.Equal(t, "", c.Credentials(7, ctlTestRID).Token)
}

func TestCtlSessions_GivenLaterRunWithoutDesktopToken_WhenBound_ThenDesktopOwnershipIsKept(t *testing.T) {
	c := NewCtlSessions(func() string { return "http://gw" })
	c.bind(ctlTestRID, ctlTestPeer, ctlTestConversation, DesktopCtlSession{DesktopSessionID: 55, Token: ctlTestDesktopToken}, true)
	c.bind(ctlTestRID, ctlTestPeer, ctlTestConversation, DesktopCtlSession{}, false)

	owner, ok := c.resolve(c.Credentials(7, ctlTestRID).Token)
	require.True(t, ok)
	assert.True(t, owner.hasDesktop, "a console turn on a desktop-owned session does not take the session over")
	assert.Equal(t, int64(55), owner.desktop.DesktopSessionID)
}

// anyCaller 是测试里放行任何作答者的闸(账号闸本身由 RequireLoggedInAccount 的测试钉住)。
func anyCaller(context.Context) error { return nil }

// 桌面端重启后签的新 token 顶掉旧的；只带会话 id、不带 token 的一轮不构成桌面端归属。
func TestCtlSessions_GivenNewDesktopTokenOrTokenlessRun_WhenBound_ThenLatestTokenWinsAndNoTokenMeansNoOwnership(t *testing.T) {
	c, tok := desktopOwned(t)
	c.bind(ctlTestRID, ctlTestPeer, ctlTestConversation, DesktopCtlSession{DesktopSessionID: 55, Token: ctlTestDesktopToken + "-2"}, true)
	owner, ok := c.resolve(tok)
	require.True(t, ok)
	assert.Equal(t, ctlTestDesktopToken+"-2", owner.desktop.Token)

	fresh := NewCtlSessions(func() string { return "http://gw" })
	fresh.bind(ctlTestRID, ctlTestPeer, ctlTestConversation, DesktopCtlSession{DesktopSessionID: 55}, false)
	owner, ok = fresh.resolve(fresh.Credentials(7, ctlTestRID).Token)
	require.True(t, ok)
	assert.False(t, owner.hasDesktop)
}

// 桌面端的会话 token 只经交来它的那台桌面端的连接转回去:之后另一个对端(控制台 /
// 别的设备)在同一会话上派发一轮,不能把它带到那个对端的连接上。
func TestCtlSessions_GivenLaterRunFromAnotherPeer_WhenForwarding_ThenStillTunneledToTheOwningDesktop(t *testing.T) {
	c, tok := desktopOwned(t)
	c.bind(ctlTestRID, "sha256:someone-else", ctlTestConversation, DesktopCtlSession{}, false)

	var gotPeer devicefp.Initiator
	fn := &fakeTunnelNotifier{resp: wire.MCPProxyResponse{Status: 200, Body: []byte(`{"list":{}}`)}}
	srv := httptest.NewServer(NewCtlProxyHandler(CtlProxyDeps{Sessions: c, Tunnel: func(p devicefp.Initiator, _ string) NotifierPort {
		gotPeer = p
		return fn
	}}))
	defer srv.Close()
	status, _, _ := postCtl(t, srv.URL+"/ctl/v1/resources", tok, listBody(t))
	require.Equal(t, 200, status)
	assert.Equal(t, ctlTestPeer, gotPeer, "the desktop token only ever goes back to the desktop that handed it over")
}

// ── 代理:鉴权 ───────────────────────────────────────────────────────────────

func TestCtlProxy_GivenNoOrForeignToken_WhenCalled_Then401(t *testing.T) {
	c := NewCtlSessions(func() string { return "http://gw" })
	h := NewCtlProxyHandler(CtlProxyDeps{Sessions: c})

	for _, auth := range []string{"", "Bearer nope", "Bearer " + agenttool.NewTokenSigner().MintToken(7, ctlTestRID)} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/ctl/v1/resources", strings.NewReader(`{}`))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusUnauthorized, rec.Code, "auth=%q", auth)
		assert.Contains(t, rec.Body.String(), `"error"`)
	}
}

// ── 桌面端拥有的会话:经反向隧道转回桌面端 ──────────────────────────────────

func desktopOwned(t *testing.T) (*CtlSessions, string) {
	t.Helper()
	c := NewCtlSessions(func() string { return "http://gw" })
	c.bind(ctlTestRID, ctlTestPeer, ctlTestConversation, DesktopCtlSession{DesktopSessionID: 55, Token: ctlTestDesktopToken}, true)
	return c, c.Credentials(7, ctlTestRID).Token
}

func TestCtlProxy_GivenDesktopOwnedSession_WhenReading_ThenForwardedWithTheDesktopTokenAndRelayed(t *testing.T) {
	c, tok := desktopOwned(t)
	fn := &fakeTunnelNotifier{resp: wire.MCPProxyResponse{
		Status: 200, Headers: map[string][]string{"Content-Type": {"application/json"}}, Body: []byte(`{"list":{}}`),
	}}
	var gotPeer devicefp.Initiator
	var gotConversation string
	h := NewCtlProxyHandler(CtlProxyDeps{Sessions: c, Tunnel: func(p devicefp.Initiator, conv string) NotifierPort {
		gotPeer, gotConversation = p, conv
		return fn
	}})

	body := listBody(t)
	srv := httptest.NewServer(h)
	defer srv.Close()
	status, got, waiting := postCtl(t, srv.URL+"/ctl/v1/resources", tok, body)

	assert.Equal(t, 200, status)
	assert.Equal(t, `{"list":{}}`, got)
	assert.Empty(t, waiting, "reads never announce an approval")
	assert.Equal(t, ctlTestPeer, gotPeer)
	assert.Equal(t, ctlTestConversation, gotConversation)
	require.Equal(t, wire.MethodMCPProxy, fn.gotMethod)
	fwd := fn.gotParams.(wire.MCPProxyRequest)
	assert.Equal(t, "/ctl/v1/resources", fwd.Path)
	assert.Equal(t, http.MethodPost, fwd.Method)
	assert.Equal(t, []string{"Bearer " + ctlTestDesktopToken}, fwd.Headers["Authorization"],
		"the desktop validates its own session token, not the one this daemon minted")
	assert.JSONEq(t, body, string(fwd.Body))
}

func TestCtlProxy_GivenDesktopOwnedSession_WhenWriting_ThenAgentredAnnouncesTheDesktopSessionAndRelaysTheVerdict(t *testing.T) {
	c, tok := desktopOwned(t)
	fn := &fakeTunnelNotifier{resp: wire.MCPProxyResponse{Status: 403, Body: []byte(`{"error":"rejected in session #55"}`)}}
	h := NewCtlProxyHandler(CtlProxyDeps{Sessions: c, Tunnel: func(devicefp.Initiator, string) NotifierPort { return fn }})
	srv := httptest.NewServer(h)
	defer srv.Close()

	status, got, waiting := postCtl(t, srv.URL+"/ctl/v1/resources", tok, writeBody(t))

	assert.Equal(t, []string{"session=55"}, waiting,
		"the desktop replays over http.Client which swallows 1xx, so agentred emits the waiting line itself")
	assert.Equal(t, http.StatusForbidden, status)
	assert.JSONEq(t, `{"error":"rejected in session #55"}`, got)
}

func TestCtlProxy_GivenDesktopOffline_WhenCalled_ThenClearError(t *testing.T) {
	c, tok := desktopOwned(t)
	h := NewCtlProxyHandler(CtlProxyDeps{Sessions: c, Tunnel: func(devicefp.Initiator, string) NotifierPort { return nil }})
	srv := httptest.NewServer(h)
	defer srv.Close()

	status, got, waiting := postCtl(t, srv.URL+"/ctl/v1/resources", tok, writeBody(t))
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, got, "desktop")
	assert.Empty(t, waiting, "nothing will be approved, so no waiting line")

	fn := &fakeTunnelNotifier{err: protorpc.ErrConnClosed}
	h = NewCtlProxyHandler(CtlProxyDeps{Sessions: c, Tunnel: func(devicefp.Initiator, string) NotifierPort { return fn }})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ctl/v1/resources", strings.NewReader(listBody(t)))
	req.Header.Set("Authorization", "Bearer "+tok)
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

// ── 控制台拥有的会话:交给 server,写入先在本会话里审批 ─────────────────────

type serverCall struct {
	path string
	body string
}

type fakeCtlServer struct {
	mu    sync.Mutex
	calls []serverCall
	// answer 按 path(含 query)给应答;缺席 = 200 + 空 CtlResponse。
	answer func(path string) (int, string)
	err    error
	// during 非 nil 时在每次调用里先跑(模拟 server 写入进行中)。
	during func(ctx context.Context, path string)
}

func (f *fakeCtlServer) Post(ctx context.Context, path string, body []byte) (int, []byte, error) {
	if f.during != nil {
		f.during(ctx, path)
	}
	f.mu.Lock()
	f.calls = append(f.calls, serverCall{path: path, body: string(body)})
	f.mu.Unlock()
	if f.err != nil {
		return 0, nil, f.err
	}
	if f.answer != nil {
		s, b := f.answer(path)
		return s, []byte(b), nil
	}
	return 200, []byte(`{}`), nil
}

func (f *fakeCtlServer) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.path)
	}
	return out
}

type resolvedApproval struct{ requestID, status, result string }

type fakeApprovalSink struct {
	mu        sync.Mutex
	sessionID int64
	err       error
	begun     []transcriptblocks.ToolApprovalBlock
	resolved  []resolvedApproval
	begunC    chan string
	done      chan struct{}
}

func newFakeSink() *fakeApprovalSink {
	return &fakeApprovalSink{sessionID: 9, begunC: make(chan string, 4), done: make(chan struct{})}
}

func (f *fakeApprovalSink) ended() <-chan struct{} { return f.done }

func (f *fakeApprovalSink) endTurn() { close(f.done) }

func (f *fakeApprovalSink) beginApproval(_ context.Context, blk *transcriptblocks.ToolApprovalBlock) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.mu.Lock()
	f.begun = append(f.begun, *blk)
	f.mu.Unlock()
	f.begunC <- blk.RequestID
	return f.sessionID, nil
}

func (f *fakeApprovalSink) resolveApproval(_ context.Context, requestID, status, result string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolved = append(f.resolved, resolvedApproval{requestID, status, result})
}

func (f *fakeApprovalSink) snapshot() ([]transcriptblocks.ToolApprovalBlock, []resolvedApproval) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]transcriptblocks.ToolApprovalBlock(nil), f.begun...), append([]resolvedApproval(nil), f.resolved...)
}

const previewResponse = `{"write":{"id":3,"name":"openrouter","changes":[{"op":"CTL_OP_UPDATE","kind":"CTL_KIND_PROVIDER","id":3,"name":"openrouter","fields":[{"field":"name","before":"old","after":"openrouter"},{"field":"apiKey","secret":true}]}]}}`

func consoleOwned(t *testing.T, server CtlServerPort, timeout time.Duration) (*CtlSessions, *fakeApprovalSink, string, *httptest.Server) {
	t.Helper()
	c := NewCtlSessions(func() string { return "http://gw" }).WithAnswerAuth(anyCaller)
	c.bind(ctlTestRID, "sha256:browser", ctlTestConversation, DesktopCtlSession{}, false)
	sink := newFakeSink()
	c.attachTurn(ctlTestRID, sink)
	h := NewCtlProxyHandler(CtlProxyDeps{
		Sessions: c, Server: server, ApprovalTimeout: timeout,
		Tunnel: func(devicefp.Initiator, string) NotifierPort {
			t.Error("a console-owned session is never tunneled to a desktop")
			return nil
		},
	})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return c, sink, c.Credentials(7, ctlTestRID).Token, srv
}

func TestCtlProxy_GivenConsoleOwnedSession_WhenReading_ThenServerAnswersDirectly(t *testing.T) {
	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, `{"list":{"items":[]}}` }}
	_, sink, tok, srv := consoleOwned(t, server, time.Minute)

	status, got, waiting := postCtl(t, srv.URL+"/ctl/v1/resources", tok, listBody(t))

	assert.Equal(t, 200, status)
	assert.JSONEq(t, `{"list":{"items":[]}}`, got)
	assert.Empty(t, waiting)
	assert.Equal(t, []string{"/v1/ctl/resources"}, server.paths())
	begun, _ := sink.snapshot()
	assert.Empty(t, begun, "reads need no approval")
}

func TestCtlProxy_GivenConsoleOwnedSession_WhenSending_ThenServerExplainsItIsUnsupported(t *testing.T) {
	server := &fakeCtlServer{answer: func(string) (int, string) {
		return http.StatusUnprocessableEntity, `{"error":"send is not supported"}`
	}}
	_, _, tok, srv := consoleOwned(t, server, time.Minute)

	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, `{"agentId":1,"text":"hi"}`)
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.JSONEq(t, `{"error":"send is not supported"}`, got)
	assert.Equal(t, []string{"/v1/ctl/send"}, server.paths())
}

func TestCtlProxy_GivenSessionToken_WhenCallingAControlOnlyVerb_Then403(t *testing.T) {
	_, _, tok, srv := consoleOwned(t, &fakeCtlServer{}, time.Minute)
	for _, path := range []string{"/ctl/v1/answer-permission", "/ctl/v1/stop"} {
		status, _, _ := postCtl(t, srv.URL+path, tok, `{}`)
		assert.Equal(t, http.StatusForbidden, status, path)
	}
}

func TestCtlProxy_GivenConsoleOwnedWrite_WhenApproved_ThenCardShowsServerPreviewAndServerWrites(t *testing.T) {
	server := &fakeCtlServer{answer: func(path string) (int, string) { return 200, previewResponse }}
	c, sink, tok, srv := consoleOwned(t, server, time.Minute)

	done := make(chan ctlResult, 1)
	go func() { done <- ctlPost(t, srv.URL+"/ctl/v1/resources", tok, writeBody(t)) }()
	requestID := <-sink.begunC

	assert.Equal(t, []string{"/v1/ctl/resources?preview=1"}, server.paths(), "nothing is written before approval")
	begun, _ := sink.snapshot()
	require.Len(t, begun, 1)
	card := begun[0]
	assert.Equal(t, agenttool.KeyCtl, card.ToolKey)
	assert.Equal(t, "pending", card.Status)
	assert.Equal(t, "ctl_update_provider", card.ToolName)
	input, err := transcriptblocks.ParseCtlApprovalInput(card.ToolInput)
	require.NoError(t, err)
	assert.Equal(t, "agrctl update provider openrouter --api-key …", input.Command,
		"a secret the client failed to redact is still masked")
	require.Len(t, input.Changes, 1)
	assert.Equal(t, "update", input.Changes[0].Op)
	assert.Equal(t, "provider", input.Changes[0].Kind)
	assert.Equal(t, "openrouter", input.Changes[0].Name)
	require.Len(t, input.Changes[0].Fields, 2)
	assert.True(t, input.Changes[0].Fields[1].Secret)

	_, err = c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: requestID, Allow: true,
	})
	require.NoError(t, err)

	res := <-done
	assert.Equal(t, []string{"session=9"}, res.waiting)
	assert.Equal(t, 200, res.status)
	assert.JSONEq(t, previewResponse, res.body)
	assert.Equal(t, []string{"/v1/ctl/resources?preview=1", "/v1/ctl/resources"}, server.paths())
	_, resolved := sink.snapshot()
	assert.Equal(t, []resolvedApproval{{requestID, "approved", "已更新 provider openrouter"}}, resolved)

	_, err = c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: requestID, Allow: true,
	})
	assert.Error(t, err, "an answered card is no longer pending")
}

// 批准之后 agrctl 被杀:已经批准的写入照样交 server 写完,卡也照样落结果。
func TestCtlProxy_GivenCallerGoneAfterApproval_ThenCommitIsNotCanceled(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan bool, 1)
	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, previewResponse }}
	server.during = func(ctx context.Context, path string) {
		if strings.Contains(path, "preview") {
			return
		}
		close(entered)
		select {
		case <-ctx.Done():
			canceled <- true
		case <-time.After(500 * time.Millisecond):
			canceled <- false
		}
	}
	c, sink, tok, srv := consoleOwned(t, server, time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/ctl/v1/resources", strings.NewReader(writeBody(t)))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+tok)
	go func() {
		requestID := <-sink.begunC
		_, _ = c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
			ConversationId: ctlTestConversation, RequestId: requestID, Allow: true,
		})
		<-entered
		cancel()
	}()
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
	assert.False(t, <-canceled, "an approved write is committed even if agrctl goes away")
}

func TestCtlProxy_GivenApprovedWriteFails_ThenCardSaysSoAndErrorIsRelayed(t *testing.T) {
	server := &fakeCtlServer{answer: func(path string) (int, string) {
		if strings.Contains(path, "preview") {
			return 200, previewResponse
		}
		return http.StatusConflict, `{"error":"name already taken"}`
	}}
	c, sink, tok, srv := consoleOwned(t, server, time.Minute)

	done := make(chan ctlResult, 1)
	go func() { done <- ctlPost(t, srv.URL+"/ctl/v1/resources", tok, writeBody(t)) }()
	requestID := <-sink.begunC
	_, err := c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: requestID, Allow: true,
	})
	require.NoError(t, err)

	res := <-done
	assert.Equal(t, http.StatusConflict, res.status)
	assert.JSONEq(t, `{"error":"name already taken"}`, res.body)
	_, resolved := sink.snapshot()
	assert.Equal(t, []resolvedApproval{{requestID, "approved", "执行失败：name already taken"}}, resolved)
}

func TestCtlProxy_GivenConsoleOwnedWrite_WhenRejected_Then403AndCardDenied(t *testing.T) {
	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, previewResponse }}
	c, sink, tok, srv := consoleOwned(t, server, time.Minute)

	done := make(chan ctlResult, 1)
	go func() { done <- ctlPost(t, srv.URL+"/ctl/v1/resources", tok, writeBody(t)) }()
	requestID := <-sink.begunC
	_, err := c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: requestID, Allow: false,
	})
	require.NoError(t, err)

	res := <-done
	assert.Equal(t, http.StatusForbidden, res.status)
	assert.JSONEq(t, `{"error":"rejected in session #9"}`, res.body)
	assert.Equal(t, []string{"/v1/ctl/resources?preview=1"}, server.paths(), "a rejected write never reaches the server")
	_, resolved := sink.snapshot()
	assert.Equal(t, []resolvedApproval{{requestID, "denied", ""}}, resolved)
}

func TestCtlProxy_GivenConsoleOwnedWrite_WhenNobodyAnswers_Then504AndCardExpired(t *testing.T) {
	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, previewResponse }}
	c, sink, tok, srv := consoleOwned(t, server, 50*time.Millisecond)

	res := ctlPost(t, srv.URL+"/ctl/v1/resources", tok, writeBody(t))
	requestID := <-sink.begunC

	assert.Equal(t, http.StatusGatewayTimeout, res.status)
	assert.JSONEq(t, `{"error":"approval timed out"}`, res.body)
	_, resolved := sink.snapshot()
	assert.Equal(t, []resolvedApproval{{requestID, "expired", ""}}, resolved)
	_, err := c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: requestID, Allow: true,
	})
	assert.Error(t, err, "an expired card cannot be answered")
}

// 轮次收口时还挂着的卡已经在转录里记成 expired：此后控制台再批准，不能照样提交 server
// ——转录说「过期」、server 却写了。agrctl 也不该干等 4 分钟。
func TestCtlProxy_GivenTurnEndsWhileCardPending_ThenConflictAndLateAnswerIsRejected(t *testing.T) {
	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, previewResponse }}
	c, sink, tok, srv := consoleOwned(t, server, time.Minute)

	done := make(chan ctlResult, 1)
	go func() { done <- ctlPost(t, srv.URL+"/ctl/v1/resources", tok, writeBody(t)) }()
	requestID := <-sink.begunC

	sink.endTurn()
	_, err := c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: requestID, Allow: true,
	})
	assert.Error(t, err, "a card of a finished turn cannot be answered")

	select {
	case res := <-done:
		assert.Equal(t, http.StatusConflict, res.status, res.body)
	case <-time.After(5 * time.Second):
		t.Fatal("agrctl is still waiting although the turn has ended")
	}
	assert.Equal(t, []string{"/v1/ctl/resources?preview=1"}, server.paths(), "nothing is committed")
}

func TestCtlProxy_GivenPreviewRefused_ThenNoCardAndServerErrorRelayed(t *testing.T) {
	server := &fakeCtlServer{answer: func(string) (int, string) { return http.StatusNotFound, `{"error":"provider 3 not found"}` }}
	_, sink, tok, srv := consoleOwned(t, server, time.Minute)

	status, got, waiting := postCtl(t, srv.URL+"/ctl/v1/resources", tok, writeBody(t))
	assert.Equal(t, http.StatusNotFound, status)
	assert.JSONEq(t, `{"error":"provider 3 not found"}`, got)
	assert.Empty(t, waiting)
	begun, _ := sink.snapshot()
	assert.Empty(t, begun)
}

func TestCtlProxy_GivenNoActiveTurn_WhenWriting_ThenConflictWithoutAnnouncing(t *testing.T) {
	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, previewResponse }}
	c := NewCtlSessions(func() string { return "http://gw" })
	c.bind(ctlTestRID, "sha256:browser", ctlTestConversation, DesktopCtlSession{}, false)
	srv := httptest.NewServer(NewCtlProxyHandler(CtlProxyDeps{Sessions: c, Server: server}))
	defer srv.Close()

	status, got, waiting := postCtl(t, srv.URL+"/ctl/v1/resources", c.Credentials(7, ctlTestRID).Token, writeBody(t))
	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, got, "cannot ask for approval")
	assert.Empty(t, waiting)
}

func TestCtlProxy_GivenServerUnreachable_ThenServiceUnavailable(t *testing.T) {
	_, _, tok, srv := consoleOwned(t, &fakeCtlServer{err: ErrCtlServerUnavailable}, time.Minute)
	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/resources", tok, listBody(t))
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Contains(t, got, "signed in")
}

// ── 控制台作答:唤醒挂起的那一张卡,别的一律答「不挂起」─────────────────────

func TestCtlSessions_GivenUnknownOrForeignCard_WhenAnswered_ThenNoPendingError(t *testing.T) {
	c := NewCtlSessions(func() string { return "http://gw" }).WithAnswerAuth(anyCaller)
	ch := c.beginWait(ctlTestConversation, "req-1", nil)

	_, err := c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: "req-unknown", Allow: true,
	})
	var perr *protorpc.Error
	require.True(t, errors.As(err, &perr))
	assert.Contains(t, perr.Message, "no pending tool approval")

	_, err = c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: "00000000-0000-7000-8000-000000000999", RequestId: "req-1", Allow: true,
	})
	require.Error(t, err, "a card is answered only within its own session")

	_, err = c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: "req-1", Allow: false,
	})
	require.NoError(t, err)
	assert.False(t, <-ch)
}

// toolApproval.answer 用 agentred 自己的设备凭据提交写入:只收已登录账号的调用方
// (控制台),光是配对过的对端答不了(与 SubmitToolPermission 等同族方法同一道闸)。
func TestCtlSessions_GivenUnauthorizedCaller_WhenAnswering_ThenRefusedAndCardStaysPending(t *testing.T) {
	deny := errors.New("unauthorized")
	c := NewCtlSessions(func() string { return "http://gw" }).WithAnswerAuth(func(context.Context) error { return deny })
	ch := c.beginWait(ctlTestConversation, "req-1", nil)
	_, err := c.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: "req-1", Allow: true,
	})
	require.ErrorIs(t, err, deny)
	select {
	case <-ch:
		t.Fatal("an unauthorized answer reached the waiting write")
	default:
	}

	unwired := NewCtlSessions(func() string { return "http://gw" })
	unwired.beginWait(ctlTestConversation, "req-2", nil)
	_, err = unwired.AnswerToolApproval(context.Background(), &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: ctlTestConversation, RequestId: "req-2", Allow: true,
	})
	assert.Error(t, err, "no gate wired = no answers (fail closed)")
}

// ── 服务端客户端:设备 Bearer,401 刷新一次 ───────────────────────────────────

func TestCtlServerClient_GivenExpiredToken_WhenPosting_ThenRefreshesOnceAndRetries(t *testing.T) {
	token := "old"
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		assert.Equal(t, "/v1/ctl/resources", r.URL.Path)
		assert.Equal(t, "1", r.URL.Query().Get("preview"))
		if r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write(b)
	}))
	defer srv.Close()
	refreshed := 0
	client := &CtlServerClient{
		ServerURL:   func() string { return srv.URL + "/" },
		AccessToken: func() string { return token },
		Refresh:     func(context.Context) error { refreshed++; token = "new"; return nil },
	}

	status, body, err := client.Post(context.Background(), "/v1/ctl/resources?preview=1", []byte(`{"x":1}`))
	require.NoError(t, err)
	assert.Equal(t, 200, status)
	assert.Equal(t, `{"x":1}`, string(body))
	assert.Equal(t, 1, refreshed)
	assert.Equal(t, []string{"Bearer old", "Bearer new"}, auths)
}

func TestCtlServerClient_GivenNotSignedIn_ThenUnavailable(t *testing.T) {
	client := &CtlServerClient{ServerURL: func() string { return "" }, AccessToken: func() string { return "" }}
	_, _, err := client.Post(context.Background(), "/v1/ctl/resources", nil)
	assert.ErrorIs(t, err, ErrCtlServerUnavailable)
}

// ── 小工具 ───────────────────────────────────────────────────────────────────

func listBody(t *testing.T) string {
	t.Helper()
	b, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_List{
		List: &agentrewire.CtlListRequest{Kind: agentrewire.CtlKind_CTL_KIND_PROVIDER},
	}})
	require.NoError(t, err)
	return string(b)
}

func writeBody(t *testing.T) string {
	t.Helper()
	b, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_Write{Write: &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_UPDATE, Kind: agentrewire.CtlKind_CTL_KIND_PROVIDER, Id: 3,
		Resource: &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Provider{Provider: &agentrewire.CtlProvider{Name: "openrouter", ApiKey: "sk-live-SECRET"}}},
		Fields:   []string{"name", "apiKey"},
		Caller:   agentrewire.CtlCaller_CTL_CALLER_SESSION,
		Command:  "agrctl update provider openrouter --api-key sk-live-SECRET",
	}}})
	require.NoError(t, err)
	return string(b)
}

type ctlResult struct {
	status  int
	body    string
	waiting []string
}

func ctlPost(t *testing.T, url, token, body string) ctlResult {
	status, got, waiting := postCtl(t, url, token, body)
	return ctlResult{status, got, waiting}
}

// postCtl 像 agrctl 一样发请求:记下 102 里的审批去向,返回最终状态与正文。
func postCtl(t *testing.T, url, token, body string) (int, string, []string) {
	var mu sync.Mutex
	var waiting []string
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
		Got1xxResponse: func(code int, header textproto.MIMEHeader) error {
			if code == http.StatusProcessing {
				mu.Lock()
				waiting = append(waiting, header.Get("Agentre-Ctl-Approval"))
				mu.Unlock()
			}
			return nil
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Error(err)
		return 0, "", nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Error(err)
		return 0, "", nil
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	mu.Lock()
	defer mu.Unlock()
	return resp.StatusCode, string(raw), waiting
}

// 红线:两种 token(daemon 签的会话 token、桌面端的会话 token)与请求正文里的密钥都不进日志。
func TestCtlProxy_GivenFailuresOnEveryRoute_ThenNoTokenOrSecretIsLogged(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	old := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(old) })

	c, tok := desktopOwned(t)
	for _, tunnel := range []NotifierPort{nil, &fakeTunnelNotifier{err: protorpc.ErrConnClosed}} {
		h := NewCtlProxyHandler(CtlProxyDeps{Sessions: c, Tunnel: func(devicefp.Initiator, string) NotifierPort { return tunnel }})
		req := httptest.NewRequest(http.MethodPost, "/ctl/v1/resources", strings.NewReader(writeBody(t)))
		req.Header.Set("Authorization", "Bearer "+tok)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	_, _, consoleTok, srv := consoleOwned(t, &fakeCtlServer{err: errors.New("dial tcp: refused")}, time.Minute)
	_, _, _ = postCtl(t, srv.URL+"/ctl/v1/resources", consoleTok, writeBody(t))

	require.NotZero(t, logs.Len(), "the failures are logged")
	for _, entry := range logs.All() {
		line := entry.Message
		for k, v := range entry.ContextMap() {
			line += " " + k + "=" + fmt.Sprint(v)
		}
		for _, secret := range []string{tok, consoleTok, ctlTestDesktopToken, "sk-live-SECRET"} {
			assert.NotContains(t, line, secret)
		}
	}
}

// spec「审批卡」:带 --cascade 的删除写明连带删除的子部门和 Agent 数量。控制台路径上数量由
// server 的变更清单附注带来,卡上要还原成结构化的 cascade。
func TestCtlProxy_GivenConsoleOwnedCascadeDelete_ThenCardStatesCascadeCounts(t *testing.T) {
	const cascadePreview = `{"write":{"id":4,"name":"infra","changes":[{"op":"CTL_OP_DELETE","kind":"CTL_KIND_DEPARTMENT","id":4,"name":"infra","note":"also deletes 2 sub-departments and 1 agent"}]}}`
	server := &fakeCtlServer{answer: func(string) (int, string) { return 200, cascadePreview }}
	_, sink, tok, srv := consoleOwned(t, server, 500*time.Millisecond)
	body, err := protojson.Marshal(&agentrewire.CtlRequest{Op: &agentrewire.CtlRequest_Write{Write: &agentrewire.CtlWriteRequest{
		Op: agentrewire.CtlOp_CTL_OP_DELETE, Kind: agentrewire.CtlKind_CTL_KIND_DEPARTMENT, Id: 4, Cascade: true,
		Caller: agentrewire.CtlCaller_CTL_CALLER_SESSION, Command: "agrctl delete department infra --cascade",
	}}})
	require.NoError(t, err)

	go func() { _, _, _ = postCtl(t, srv.URL+"/ctl/v1/resources", tok, string(body)) }()
	<-sink.begunC

	begun, _ := sink.snapshot()
	input, err := transcriptblocks.ParseCtlApprovalInput(begun[0].ToolInput)
	require.NoError(t, err)
	require.Len(t, input.Changes, 1)
	assert.Equal(t, &transcriptblocks.CtlApprovalCascade{Departments: 2, Agents: 1}, input.Changes[0].Cascade)
}
