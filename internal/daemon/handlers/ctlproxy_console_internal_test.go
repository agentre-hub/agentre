package handlers

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/ctlaccount"
	transcriptblocks "github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/ctlbind"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// ctlproxy_console_internal_test.go 钉住控制台拥有的会话里的 agrctl 会话命令(spec 2026-11-25
// 「控制台会话里的 agrctl send」、Design 12/18):会话所在的这台 agentred 以账号对端身份(ctlaccount,
// 与 agrctl 账号模式同一份逻辑)发起子会话、代为查询与跟随;send --wait 照 2026-10-27 的规则把
// 父转录里那条 shell 调用升级成子代理卡;承载 send 的连接断开时停掉子会话。

const consoleChild = "00000000-0000-7000-8000-0000000000c1"

// fakeAccount 是假的账号对端:记下收到的调用,按用例给应答。
type fakeAccount struct {
	mu      sync.Mutex
	sends   []ctlaccount.SendInput
	stops   []string
	waits   []string
	closed  int
	reply   ctlaccount.Reply
	sendErr error
	// wait 给 Wait 的结果;nil = 一直等到 ctx 取消。
	wait func(ctx context.Context) (ctlaccount.Reply, error)
	// frames 是 Follow 依次交出的子会话持久帧;followed 在 Follow 交完帧之后关上。
	frames   []*agentrewire.DurableNotification
	followed chan struct{}
	// hold 非 nil 时 Follow 交完帧后等它关上(或 ctx 取消)再收口。
	hold chan struct{}

	sessions []ctlaccount.SessionRow
	filters  []ctlaccount.SessionFilter
	detail   *ctlaccount.SessionDetail
	page     ctlaccount.MessagesPage
	pages    []string
	pending  []ctlaccount.PendingItem
	tail     []string
}

func newFakeAccount() *fakeAccount { return &fakeAccount{followed: make(chan struct{})} }

func (f *fakeAccount) Send(_ context.Context, in ctlaccount.SendInput) (ctlaccount.Reply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sends = append(f.sends, in)
	return f.reply, f.sendErr
}

func (f *fakeAccount) Wait(ctx context.Context, id string, _ time.Duration) (ctlaccount.Reply, error) {
	f.mu.Lock()
	f.waits = append(f.waits, id)
	wait := f.wait
	f.mu.Unlock()
	if wait == nil {
		<-ctx.Done()
		return ctlaccount.Reply{}, ctx.Err()
	}
	return wait(ctx)
}

func (f *fakeAccount) Stop(_ context.Context, id string) (ctlaccount.StopReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, id)
	return ctlaccount.StopReply{Stopped: true, Running: true}, nil
}

func (f *fakeAccount) Follow(ctx context.Context, _ string, _ int64, fn func(*agentrewire.DurableNotification)) (string, error) {
	for _, n := range f.frames {
		fn(n)
	}
	close(f.followed)
	if f.hold != nil {
		select {
		case <-f.hold:
		case <-ctx.Done():
			return "", nil
		}
	}
	return ctlaccount.StatusIdle, nil
}

func (f *fakeAccount) ListSessions(_ context.Context, filter ctlaccount.SessionFilter) ([]ctlaccount.SessionRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.filters = append(f.filters, filter)
	return f.sessions, nil
}

func (f *fakeAccount) GetSession(_ context.Context, id string) (*ctlaccount.SessionDetail, error) {
	if f.detail == nil || f.detail.ID != id {
		return nil, ctlaccount.ErrSessionNotVisible
	}
	return f.detail, nil
}

func (f *fakeAccount) Messages(_ context.Context, id string, last int, before int64, full bool) (ctlaccount.MessagesPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages = append(f.pages, fmt.Sprintf("%s last=%d before=%d full=%v", id, last, before, full))
	return f.page, nil
}

func (f *fakeAccount) Pending(_ context.Context, id string) ([]ctlaccount.PendingItem, error) {
	return f.pending, nil
}

func (f *fakeAccount) Tail(_ context.Context, w io.Writer, id, from string, untilIdle bool) error {
	f.mu.Lock()
	f.tail = append(f.tail, fmt.Sprintf("%s from=%s untilIdle=%v", id, from, untilIdle))
	f.mu.Unlock()
	_, _ = io.WriteString(w, `{"cursor":"v1:3","type":"text","kind":"text_delta"}`+"\n")
	_, _ = io.WriteString(w, `{"cursor":"v1:3","type":"status","status":"idle"}`+"\n")
	return nil
}

func (f *fakeAccount) Close() {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
}

func (f *fakeAccount) snapshotSends() []ctlaccount.SendInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ctlaccount.SendInput(nil), f.sends...)
}

func (f *fakeAccount) snapshotStops() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.stops...)
}

// consoleOwnedWith 是控制台拥有、当前轮为 turn 的会话,会话命令交给 acct。会话键与生产一致地由
// 对话 ID 派生:卡片注入方按对话 ID 找在途那一轮。
func consoleOwnedWith(t *testing.T, turn approvalSink, acct CtlAccount) (*CtlSessions, string, *httptest.Server) {
	t.Helper()
	rid := runtimeSessionID(ctlTestConversation)
	c := NewCtlSessions(func() string { return "http://gw" }).WithAnswerAuth(anyCaller)
	c.bind(rid, "sha256:browser", ctlTestConversation, DesktopCtlSession{}, false)
	if turn != nil {
		c.attachTurn(rid, turn)
	}
	h := NewCtlProxyHandler(CtlProxyDeps{
		Sessions: c, Server: &fakeCtlServer{}, SendBindWait: 50 * time.Millisecond,
		Cards:   &SubagentCardInjector{Ctl: c},
		Account: func() (CtlAccount, error) { return acct, nil },
		Tunnel: func(devicefp.Initiator, string) NotifierPort {
			t.Error("a console-owned session is never tunneled to a desktop")
			return nil
		},
	})
	srv := httptest.NewServer(h)
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	return c, c.Credentials(7, rid).Token, srv
}

// childFrame 是子会话的一条持久运行时帧。
func childFrame(t *testing.T, seq int64, ev *agentrewire.RuntimeEventNotification) *agentrewire.DurableNotification {
	t.Helper()
	ev.ConversationId = consoleChild
	return &agentrewire.DurableNotification{Seq: seq, Payload: &agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: ev}}}
}

func TestCtlProxy_GivenConsoleOwnedSession_WhenSending_ThenTheChildStartsAsAnAccountPeerOfThisAgentred(t *testing.T) {
	acct := newFakeAccount()
	acct.reply = ctlaccount.Reply{ConversationID: consoleChild, Agent: "reviewer", Device: "build-box", Status: ctlaccount.StatusRunning}
	_, tok, srv := consoleOwnedWith(t, nil, acct)

	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok,
		`{"agent":"reviewer","projectId":3,"title":"PR","text":"审一下 PR 42","device":"build-box","model":"openrouter/glm","effort":"high","permissionMode":"plan","isolated":true,"wait":false}`)

	require.Equal(t, http.StatusOK, status, got)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &out))
	assert.Equal(t, consoleChild, out["conversationId"], "agrctl prints the child's UUID")
	assert.Equal(t, "reviewer", out["agent"])
	assert.Equal(t, "build-box", out["device"])
	assert.Equal(t, ctlaccount.StatusRunning, out["status"])
	assert.Equal(t, []ctlaccount.SendInput{{
		Text: "审一下 PR 42", Agent: "reviewer", Project: "3", Title: "PR", Device: "build-box", Isolated: true,
		Model: "openrouter/glm", Effort: "high", PermissionMode: "plan",
	}}, acct.snapshotSends(), "the same flags agrctl's account mode takes; the proxy waits itself, not inside Send")
	acct.mu.Lock()
	defer acct.mu.Unlock()
	assert.Equal(t, 1, acct.closed, "the account peer of a request without --wait ends with it")
}

func TestCtlProxy_GivenConsoleOwnedSession_WhenContinuingAnotherSession_ThenItSteersOrRunsThroughTheAccountPeer(t *testing.T) {
	acct := newFakeAccount()
	acct.reply = ctlaccount.Reply{ConversationID: consoleChild, Status: ctlaccount.StatusRunning, Steered: true}
	_, tok, srv := consoleOwnedWith(t, nil, acct)

	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, `{"conversationId":"`+consoleChild+`","text":"再看看","steer":true,"cliModel":"opus"}`)

	require.Equal(t, http.StatusOK, status, got)
	assert.Equal(t, []ctlaccount.SendInput{{Text: "再看看", Session: consoleChild, Steer: true, CLIModel: "opus"}}, acct.snapshotSends())
}

func TestCtlProxy_GivenConsoleOwnedSession_WhenSendingToItself_ThenUsageErrorAndNothingStarts(t *testing.T) {
	acct := newFakeAccount()
	_, tok, srv := consoleOwnedWith(t, nil, acct)

	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, `{"conversationId":"`+ctlTestConversation+`","text":"hi"}`)

	assert.Equal(t, http.StatusBadRequest, status, "agrctl maps 400 to exit 2")
	assert.JSONEq(t, `{"error":"cannot send to the session you are in"}`, got)
	assert.Empty(t, acct.snapshotSends())
}

func TestCtlProxy_GivenConsoleOwnedSession_WhenTheAccountPeerRefuses_ThenTheSameExitCodesAsTheDesktop(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
	}{
		{"usage", ctlaccount.Usagef("--device %q is not one of agent reviewer's devices (one of: laptop)", "nope"), http.StatusUnprocessableEntity},
		{"ambiguous", &ctlaccount.AmbiguousError{Err: ctlaccount.Usagef("agent \"reviewer\" is ambiguous")}, http.StatusUnprocessableEntity},
		{"busy", ctlaccount.ErrSessionBusy, http.StatusConflict},
		{"not visible", ctlaccount.ErrSessionNotVisible, http.StatusNotFound},
		{"offline", fmt.Errorf("device build-box is offline"), http.StatusBadGateway},
		{"not signed in", ErrCtlServerUnavailable, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acct := newFakeAccount()
			acct.sendErr = tc.err
			_, tok, srv := consoleOwnedWith(t, nil, acct)

			status, got, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, `{"agent":"reviewer","text":"hi"}`)

			assert.Equal(t, tc.status, status, got)
			var body map[string]string
			require.NoError(t, json.Unmarshal([]byte(got), &body))
			assert.NotEmpty(t, body["error"])
		})
	}
}

func TestCtlProxy_GivenConsoleOwnedForegroundSendWait_WhenTheChildFinishes_ThenTheShellCallBecomesASettledSubagentCard(t *testing.T) {
	tt := bindTurn(t, shellCall("sh-1", bindTestCommand, false))
	acct := newFakeAccount()
	acct.reply = ctlaccount.Reply{ConversationID: consoleChild, Agent: "reviewer", Device: "build-box", Status: ctlaccount.StatusRunning}
	acct.frames = []*agentrewire.DurableNotification{
		childFrame(t, 1, &agentrewire.RuntimeEventNotification{Event: &agentrewire.RuntimeEventNotification_ToolCall{
			ToolCall: &agentrewire.ToolCall{Id: "c-1", Name: "Read", Input: []byte(`{"path":"a.go"}`)}}}),
		childFrame(t, 2, &agentrewire.RuntimeEventNotification{Event: &agentrewire.RuntimeEventNotification_ToolResult{
			ToolResult: &agentrewire.ToolResult{ToolCallId: "c-1", Content: "package a"}}}),
		{Seq: 3, Payload: &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RunResultDone{
			RunResultDone: &agentrewire.RunResultDoneNotification{ConversationId: consoleChild, Model: "glm-5"}}}},
	}
	acct.wait = func(context.Context) (ctlaccount.Reply, error) {
		<-acct.followed // 子轮收口时,它的步骤都已经交出来了
		return ctlaccount.Reply{ConversationID: consoleChild, Status: ctlaccount.StatusIdle, Text: "LGTM"}, nil
	}
	_, tok, srv := consoleOwnedWith(t, tt, acct)

	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)

	require.Equal(t, http.StatusOK, status, got)
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(got), &out))
	assert.Equal(t, "idle", out["status"])
	assert.Equal(t, "LGTM", out["text"])
	assert.Equal(t, "reviewer", out["agent"], "the dispatch's agent and device stay on the waited reply")

	// 卡片的终态在 shell 调用拿到结果之前落定。
	card := agrctlCardOf(t, tt, "sh-1")
	assert.Equal(t, "completed", card.Status)
	assert.Equal(t, "LGTM", card.FinalText)
	assert.Equal(t, "审一下 PR 42", card.Description)
	assert.Equal(t, agentruntimeKindAgrctl, card.Kind)
	assert.Equal(t, 1, card.ToolUses)
	assert.Equal(t, "glm-5", card.Model)
	steps := nestedStepsOf(tt, "sh-1")
	assert.Equal(t, []string{"use:" + ctlbind.NestedToolCallID("sh-1", "c-1") + ":Read", "result:" + ctlbind.NestedToolCallID("sh-1", "c-1")}, steps)
}

func TestCtlProxy_GivenConsoleOwnedBackgroundSendWait_WhenTheChildFinishes_ThenTheCardKeepsTheCLIsStatusButGetsTheResult(t *testing.T) {
	tt := bindTurn(t, shellCall("sh-1", bindTestCommand, true))
	acct := newFakeAccount()
	acct.reply = ctlaccount.Reply{ConversationID: consoleChild, Agent: "reviewer", Status: ctlaccount.StatusRunning}
	acct.wait = func(context.Context) (ctlaccount.Reply, error) {
		<-acct.followed
		return ctlaccount.Reply{ConversationID: consoleChild, Status: ctlaccount.StatusIdle, Text: "LGTM"}, nil
	}
	_, tok, srv := consoleOwnedWith(t, tt, acct)

	status, _, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)

	require.Equal(t, http.StatusOK, status)
	card := agrctlCardOf(t, tt, "sh-1")
	assert.Equal(t, "local_bash", card.Kind, "a background card stays a background task")
	assert.Equal(t, "LGTM", card.FinalText)
}

func TestCtlProxy_GivenConsoleOwnedSendWait_WhenTheChildWaitsForInput_ThenTheReplyReturnsAndTheCardFollowsTheChildToItsEnd(t *testing.T) {
	tt := bindTurn(t, shellCall("sh-1", bindTestCommand, false))
	acct := newFakeAccount()
	acct.reply = ctlaccount.Reply{ConversationID: consoleChild, Agent: "reviewer", Status: ctlaccount.StatusRunning}
	acct.hold = make(chan struct{})
	waited := 0
	acct.wait = func(context.Context) (ctlaccount.Reply, error) {
		waited++
		if waited == 1 {
			return ctlaccount.Reply{ConversationID: consoleChild, Status: ctlaccount.StatusWaiting, Pending: json.RawMessage(`{"askUserQuestions":[]}`)}, nil
		}
		return ctlaccount.Reply{ConversationID: consoleChild, Status: ctlaccount.StatusFailed, Error: "model exploded"}, nil
	}
	_, tok, srv := consoleOwnedWith(t, tt, acct)

	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)

	require.Equal(t, http.StatusOK, status, got)
	assert.Contains(t, got, `"status":"waiting"`)
	assert.Equal(t, "running", agrctlCardOf(t, tt, "sh-1").Status, "the child is still running: the card keeps following it")

	close(acct.hold) // 子会话这一轮真正收口
	require.Eventually(t, func() bool {
		return agrctlCardStatus(tt, "sh-1") == "failed"
	}, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, "model exploded", agrctlCardOf(t, tt, "sh-1").FinalText)
	require.Eventually(t, func() bool {
		acct.mu.Lock()
		defer acct.mu.Unlock()
		return acct.closed == 1
	}, 5*time.Second, 5*time.Millisecond, "the detached card ends the account peer when it settles")
}

func TestCtlProxy_GivenConsoleOwnedSendWait_WhenTheConnectionCarryingItDrops_ThenTheChildIsStoppedAndTheCardCanceled(t *testing.T) {
	tt := bindTurn(t, shellCall("sh-1", bindTestCommand, false))
	acct := newFakeAccount()
	acct.reply = ctlaccount.Reply{ConversationID: consoleChild, Agent: "reviewer", Status: ctlaccount.StatusRunning}
	acct.hold = make(chan struct{}) // 子会话一直在跑
	_, tok, srv := consoleOwnedWith(t, tt, acct)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/ctl/v1/send", strings.NewReader(reviewerWaitBody))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+tok)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
	}()
	<-acct.followed
	cancel() // agrctl 被杀 / 父轮被停
	<-done

	require.Eventually(t, func() bool { return len(acct.snapshotStops()) == 1 }, 5*time.Second, 5*time.Millisecond)
	assert.Equal(t, []string{consoleChild}, acct.snapshotStops())
	require.Eventually(t, func() bool { return agrctlCardStatus(tt, "sh-1") == "canceled" }, 5*time.Second, 5*time.Millisecond)
}

func TestCtlProxy_GivenConsoleOwnedSendWithoutAShellCall_ThenItRunsUnboundAndWaits(t *testing.T) {
	logs := observeHandlerLogs(t)
	tt := bindTurn(t, shellCall("sh-1", "ls -la", false))
	acct := newFakeAccount()
	acct.reply = ctlaccount.Reply{ConversationID: consoleChild, Agent: "reviewer", Status: ctlaccount.StatusRunning}
	acct.wait = func(context.Context) (ctlaccount.Reply, error) {
		return ctlaccount.Reply{ConversationID: consoleChild, Status: ctlaccount.StatusIdle, Text: "ok"}, nil
	}
	_, tok, srv := consoleOwnedWith(t, tt, acct)

	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/send", tok, reviewerWaitBody)

	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, got, `"text":"ok"`)
	assert.Contains(t, logText(logs), "reason=no_candidate")
	assert.Empty(t, nestedStepsOf(tt, "sh-1"))
}

func TestCtlProxy_GivenConsoleOwnedSessionToken_WhenReadingConversations_ThenServedAccountWide(t *testing.T) {
	acct := newFakeAccount()
	acct.sessions = []ctlaccount.SessionRow{{ID: consoleChild, Title: "PR", Agent: "reviewer", Device: "build-box", Status: "idle", LastActivityAt: "2026-11-25T01:02:03Z"}}
	acct.detail = &ctlaccount.SessionDetail{ID: consoleChild, Title: "PR", Agent: "reviewer", Status: "idle"}
	acct.page = ctlaccount.MessagesPage{Session: consoleChild, Messages: []ctlaccount.MessageRow{{Role: "user", Text: "hi"}}, HasMore: true, Before: "7"}
	acct.pending = []ctlaccount.PendingItem{{ID: "q-1", Session: consoleChild, Kind: "question"}}
	acct.wait = func(context.Context) (ctlaccount.Reply, error) {
		return ctlaccount.Reply{ConversationID: consoleChild, Agent: "reviewer", Status: "idle", Text: "done"}, nil
	}
	_, tok, srv := consoleOwnedWith(t, nil, acct)

	status, got, _ := postCtl(t, srv.URL+"/ctl/v1/conversations", tok, `{"agentId":4,"projectId":3,"device":"build-box","status":"idle","mine":true,"limit":5}`)
	require.Equal(t, http.StatusOK, status, got)
	assert.JSONEq(t, `{"sessions":[{"id":"`+consoleChild+`","title":"PR","agent":"reviewer","device":"build-box","status":"idle","lastActivityAt":"2026-11-25T01:02:03Z"}]}`, got)
	assert.Equal(t, []ctlaccount.SessionFilter{{Agent: "4", Project: "3", Device: "build-box", Status: "idle", Mine: true, Limit: 5}}, acct.filters)

	status, got, _ = postCtl(t, srv.URL+"/ctl/v1/conversation", tok, `{"conversationId":"`+consoleChild+`"}`)
	require.Equal(t, http.StatusOK, status, got)
	assert.JSONEq(t, `{"id":"`+consoleChild+`","title":"PR","agent":"reviewer","status":"idle"}`, got)

	status, _, _ = postCtl(t, srv.URL+"/ctl/v1/conversation", tok, `{"conversationId":"00000000-0000-7000-8000-0000000000ff"}`)
	assert.Equal(t, http.StatusNotFound, status)

	status, got, _ = postCtl(t, srv.URL+"/ctl/v1/messages", tok, `{"conversationId":"`+consoleChild+`","last":3,"before":"9","full":true}`)
	require.Equal(t, http.StatusOK, status, got)
	assert.JSONEq(t, `{"session":"`+consoleChild+`","messages":[{"role":"user","text":"hi"}],"hasMore":true,"before":"7"}`, got)
	assert.Equal(t, []string{consoleChild + " last=3 before=9 full=true"}, acct.pages)

	status, got, _ = postCtl(t, srv.URL+"/ctl/v1/pending", tok, `{"conversationId":"`+consoleChild+`"}`)
	require.Equal(t, http.StatusOK, status, got)
	assert.JSONEq(t, `{"items":[{"id":"q-1","session":"`+consoleChild+`","kind":"question"}]}`, got)

	status, got, _ = postCtl(t, srv.URL+"/ctl/v1/wait", tok, `{"conversationId":"`+consoleChild+`","timeoutMs":1000}`)
	require.Equal(t, http.StatusOK, status, got)
	assert.JSONEq(t, `{"conversationId":"`+consoleChild+`","status":"idle","text":"done"}`, got)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/ctl/v1/tail?conversationId="+consoleChild+"&from=v1:2&untilIdle=true", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var lines []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	assert.Equal(t, []string{`{"cursor":"v1:3","type":"text","kind":"text_delta"}`, `{"cursor":"v1:3","type":"status","status":"idle"}`}, lines)
	assert.Equal(t, []string{consoleChild + " from=v1:2 untilIdle=true"}, acct.tail)
}

func TestCtlProxy_GivenConsoleOwnedSessionToken_WhenAnsweringOrStopping_ThenStill403(t *testing.T) {
	acct := newFakeAccount()
	_, tok, srv := consoleOwnedWith(t, nil, acct)
	for _, path := range []string{"/ctl/v1/stop", "/ctl/v1/answer", "/ctl/v1/answer-permission"} {
		status, _, _ := postCtl(t, srv.URL+path, tok, `{"conversationId":"`+consoleChild+`"}`)
		assert.Equal(t, http.StatusForbidden, status, path)
	}
	assert.Empty(t, acct.snapshotStops())
}

// ── 小工具 ──────────────────────────────────────────────────────────────────

const agentruntimeKindAgrctl = "agrctl_send"

func agrctlCardStatus(tt *turnTranscript, toolCallID string) string {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	for _, b := range tt.acc.Snapshot() {
		if sb, ok := b.(*transcriptblocks.SubagentStateBlock); ok && sb.ParentToolCallID == toolCallID {
			return sb.Status
		}
	}
	return ""
}

// nestedStepsOf 列出 tt 里挂在 toolCallID 下的嵌套步骤。
func nestedStepsOf(tt *turnTranscript, toolCallID string) []string {
	tt.mu.Lock()
	defer tt.mu.Unlock()
	var out []string
	for _, b := range tt.acc.Snapshot() {
		switch nb := b.(type) {
		case *transcriptblocks.NestedToolUseBlock:
			if nb.ParentToolCallID == toolCallID {
				out = append(out, "use:"+nb.ID+":"+nb.Name)
			}
		case *transcriptblocks.NestedToolResultBlock:
			if nb.ParentToolCallID == toolCallID {
				out = append(out, "result:"+nb.ToolCallID)
			}
		}
	}
	return out
}
