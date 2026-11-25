package acpcmd

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/agentre-hub/agentre/internal/cli/ctlcmd"
	"github.com/agentre-hub/agentre/internal/pkg/agrctlcred"
)

const accountConv = "0193b6f0-7c5e-7d2a-9f4b-3c1d2e5f6a7c"

// fakeDriver 是账号驱动的替身：记下收到的请求，按脚本交回事件。
type fakeDriver struct {
	mu      sync.Mutex
	prompts []ctlcmd.PromptRequest
	stops   []string
	answers []fakeAnswer
	// script 是每次 Prompt 交回的事件；promptErr 非空时 Prompt 直接失败。
	script    [][]ctlcmd.TurnEvent
	promptErr error
	// hold 为 true 时事件流不收尾（等取消）。
	hold bool
}

type fakeAnswer struct {
	conv, request string
	allow         bool
}

func (f *fakeDriver) Prompt(ctx context.Context, req ctlcmd.PromptRequest) (string, <-chan ctlcmd.TurnEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompts = append(f.prompts, req)
	if f.promptErr != nil {
		return "", nil, f.promptErr
	}
	var evs []ctlcmd.TurnEvent
	if len(f.script) > 0 {
		evs, f.script = f.script[0], f.script[1:]
	}
	out := make(chan ctlcmd.TurnEvent, len(evs)+1)
	for _, e := range evs {
		out <- e
	}
	if f.hold {
		go func() { <-ctx.Done(); close(out) }()
	} else {
		close(out)
	}
	return accountConv, out, nil
}

func (f *fakeDriver) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, id)
	return nil
}

func (f *fakeDriver) Answer(_ context.Context, id, requestID string, allow bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, fakeAnswer{id, requestID, allow})
	return nil
}

func (f *fakeDriver) Close() {}

func startAccountAgent(t *testing.T, drv *fakeDriver) (*acpsdk.ClientSideConnection, *recordingClient) {
	t.Helper()
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()
	ag := newAgent(options{Account: drv, Agent: "planner", Project: 7})
	agentConn := acpsdk.NewAgentSideConnection(ag, agentToClientW, clientToAgentR)
	ag.SetAgentConnection(agentConn)
	rec := &recordingClient{}
	clientConn := acpsdk.NewClientSideConnection(rec, clientToAgentW, agentToClientR)
	t.Cleanup(func() { _ = clientToAgentW.Close(); _ = agentToClientW.Close() })
	return clientConn, rec
}

func text(s string) []acpsdk.ContentBlock { return []acpsdk.ContentBlock{acpsdk.TextBlock(s)} }

func TestAccount_GivenAPromptThenTheConversationStartsOnTheFirstPromptAndContinuesAfter(t *testing.T) {
	drv := &fakeDriver{script: [][]ctlcmd.TurnEvent{
		{{Kind: ctlcmd.TurnText, Delta: "Hello, "}, {Kind: ctlcmd.TurnText, Delta: "world"}, {Kind: ctlcmd.TurnDone}},
		{{Kind: ctlcmd.TurnDone}},
	}}
	client, rec := startAccountAgent(t, drv)
	sess := newSession(t, client)
	if len(drv.prompts) != 0 {
		t.Fatalf("session/new already reached the account: %+v", drv.prompts)
	}

	resp, err := client.Prompt(context.Background(), acpsdk.PromptRequest{SessionId: sess, Prompt: text("say hi")})
	if err != nil || resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Fatalf("prompt = %+v, %v", resp, err)
	}
	if got := rec.agentMessageText(); got != "Hello, world" {
		t.Fatalf("streamed text = %q", got)
	}
	if _, err := client.Prompt(context.Background(), acpsdk.PromptRequest{SessionId: sess, Prompt: text("more")}); err != nil {
		t.Fatalf("second prompt: %v", err)
	}
	first, second := drv.prompts[0], drv.prompts[1]
	if first.Conversation != "" || first.Agent != "planner" || first.Project != "7" || first.Text != "say hi" {
		t.Fatalf("first prompt = %+v, want a new conversation for agent planner in project 7", first)
	}
	if second.Conversation != accountConv || second.Text != "more" {
		t.Fatalf("second prompt = %+v, want it to continue %s", second, accountConv)
	}
}

func TestAccount_GivenToolEventsThenTheyBecomeToolCallUpdatesInOrder(t *testing.T) {
	drv := &fakeDriver{script: [][]ctlcmd.TurnEvent{{
		{Kind: ctlcmd.TurnToolCall, ToolCallID: "tc1", ToolName: "Bash", ToolInput: []byte(`{"command":"ls"}`)},
		{Kind: ctlcmd.TurnToolResult, ToolCallID: "tc1", ToolResult: "a.txt"},
		{Kind: ctlcmd.TurnText, Delta: "done"},
		{Kind: ctlcmd.TurnDone},
	}}}
	client, rec := startAccountAgent(t, drv)
	sess := newSession(t, client)
	if _, err := client.Prompt(context.Background(), acpsdk.PromptRequest{SessionId: sess, Prompt: text("go")}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if got := strings.Join(rec.updateKinds(), ","); got != "tool_call,tool_result,chunk" {
		t.Fatalf("update kinds = %s", got)
	}
}

func TestAccount_GivenAFailedTurnThenThePromptFailsWithItsMessage(t *testing.T) {
	drv := &fakeDriver{script: [][]ctlcmd.TurnEvent{{{Kind: ctlcmd.TurnError, Error: "model exploded"}}}}
	client, _ := startAccountAgent(t, drv)
	sess := newSession(t, client)
	_, err := client.Prompt(context.Background(), acpsdk.PromptRequest{SessionId: sess, Prompt: text("go")})
	if err == nil || !strings.Contains(err.Error(), "model exploded") {
		t.Fatalf("err = %v, want the turn's failure", err)
	}
	drv.promptErr = errors.New("token lacks scope chat")
	_, err = client.Prompt(context.Background(), acpsdk.PromptRequest{SessionId: sess, Prompt: text("again")})
	if err == nil || !strings.Contains(err.Error(), "token lacks scope chat") {
		t.Fatalf("err = %v, want the account's refusal", err)
	}
}

func TestAccount_GivenAToolPermissionThenTheEditorDecidesAndTheAnswerReachesTheAccount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		option string
		want   bool
	}{{"allow", "allow-once", true}, {"reject", "reject-once", false}} {
		t.Run(tc.name, func(t *testing.T) {
			drv := &fakeDriver{script: [][]ctlcmd.TurnEvent{{
				{Kind: ctlcmd.TurnPermission, RequestID: "perm-1", ToolName: "Bash", ToolInput: []byte(`{"command":"rm x"}`)},
				{Kind: ctlcmd.TurnDone},
			}}}
			client, rec := startAccountAgent(t, drv)
			rec.permFunc = func(context.Context, acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
				return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected(acpsdk.PermissionOptionId(tc.option))}, nil
			}
			sess := newSession(t, client)
			if _, err := client.Prompt(context.Background(), acpsdk.PromptRequest{SessionId: sess, Prompt: text("go")}); err != nil {
				t.Fatalf("prompt: %v", err)
			}
			if len(drv.answers) != 1 || drv.answers[0] != (fakeAnswer{accountConv, "perm-1", tc.want}) {
				t.Fatalf("answers = %+v, want one %v answer for perm-1", drv.answers, tc.want)
			}
		})
	}
}

func TestAccount_GivenCancelThenTheConversationIsStopped(t *testing.T) {
	drv := &fakeDriver{hold: true, script: [][]ctlcmd.TurnEvent{{{Kind: ctlcmd.TurnText, Delta: "working"}}}}
	client, rec := startAccountAgent(t, drv)
	sess := newSession(t, client)
	done := make(chan acpsdk.PromptResponse, 1)
	go func() {
		resp, _ := client.Prompt(context.Background(), acpsdk.PromptRequest{SessionId: sess, Prompt: text("go")})
		done <- resp
	}()
	deadline := time.Now().Add(5 * time.Second)
	for rec.agentMessageText() == "" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if err := client.Cancel(context.Background(), acpsdk.CancelNotification{SessionId: sess}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case resp := <-done:
		if resp.StopReason != acpsdk.StopReasonCancelled {
			t.Fatalf("stop reason = %q", resp.StopReason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt did not return after cancel")
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		drv.mu.Lock()
		n := len(drv.stops)
		drv.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	drv.mu.Lock()
	defer drv.mu.Unlock()
	if len(drv.stops) != 1 || drv.stops[0] != accountConv {
		t.Fatalf("stops = %v, want [%s]", drv.stops, accountConv)
	}
}

func TestRun_GivenAnAccountLoginWithoutAPersonalTokenThenItFailsWithTheEditorHint(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTRE_DATA_DIR", dir)
	t.Setenv("AGENTRED_DATA_DIR", t.TempDir())
	if err := agrctlcred.Save(dir, &agrctlcred.Credentials{ServerURL: "http://127.0.0.1:1", AccountID: 7, Account: "a@b.c",
		DeviceID: 1, RefreshToken: "r", AccessToken: "a", AccessExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr strings.Builder
	code := run([]string{"--agent", "planner"}, strings.NewReader(""), &stdout, &stderr, func(string) (string, bool) { return "", false })
	if code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	for _, want := range []string{"AGENTRE_TOKEN", "chat", "approve", "agrctl create token"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, missing %q", stderr.String(), want)
		}
	}
}
