package acpcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
)

const testToken = "acp-test-token"

// ---- fake ctl server ----

type fakeCtl struct {
	srv          *httptest.Server
	newSessionID int64
	// events 是每次 /send 之后推给 /stream 订阅者的 SSE 事件。
	events []streamEvent

	mu      sync.Mutex
	ensure  []map[string]any
	sends   []map[string]any
	stops   []map[string]any
	streams []string

	// order 记录 /stream 与 /send 的到达顺序,用于断言「先订阅再发 send」。
	order     atomic.Int64
	streamSeq atomic.Int64
	sendSeq   atomic.Int64

	streamCh chan streamEvent
}

func newFakeCtl(t *testing.T) *fakeCtl {
	t.Helper()
	f := &fakeCtl{newSessionID: 100, streamCh: make(chan streamEvent, 64)}
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid control token"}`)
			return false
		}
		return true
	}
	mux.HandleFunc("/ctl/v1/sessions", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.ensure = append(f.ensure, body)
		id := f.newSessionID
		f.mu.Unlock()
		_, _ = fmt.Fprintf(w, `{"sessionId":%d}`, id)
	})
	mux.HandleFunc("/ctl/v1/send", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.sends = append(f.sends, body)
		f.sendSeq.Store(f.order.Add(1))
		events := append([]streamEvent(nil), f.events...)
		f.mu.Unlock()
		go func() {
			for _, ev := range events {
				f.streamCh <- ev
			}
		}()
		_, _ = io.WriteString(w, `{"sessionId":100,"assistantMessageId":200,"done":false}`)
	})
	mux.HandleFunc("/ctl/v1/stop", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.stops = append(f.stops, body)
		f.mu.Unlock()
		_, _ = io.WriteString(w, `{"stopped":true}`)
	})
	mux.HandleFunc("/ctl/v1/stream", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		f.mu.Lock()
		f.streams = append(f.streams, r.URL.RawQuery)
		f.mu.Unlock()
		f.streamSeq.Store(f.order.Add(1))
		flusher, ok := w.(http.Flusher)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case ev := <-f.streamCh:
				b, _ := json.Marshal(ev)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
				flusher.Flush()
			}
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeCtl) setEvents(events ...streamEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = events
}

func (f *fakeCtl) streamQueries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.streams...)
}

func (f *fakeCtl) lastSend(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sends) == 0 {
		t.Fatal("no /ctl/v1/send call recorded")
	}
	return f.sends[len(f.sends)-1]
}

// ---- recording ACP client ----

type recordingClient struct {
	mu      sync.Mutex
	updates []acpsdk.SessionNotification
}

func (c *recordingClient) ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, nil
}
func (c *recordingClient) WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, nil
}
func (c *recordingClient) RequestPermission(context.Context, acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	return acpsdk.RequestPermissionResponse{}, nil
}
func (c *recordingClient) SessionUpdate(_ context.Context, n acpsdk.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, n)
	return nil
}
func (c *recordingClient) CreateTerminal(context.Context, acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, nil
}
func (c *recordingClient) KillTerminal(context.Context, acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, nil
}
func (c *recordingClient) TerminalOutput(context.Context, acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, nil
}
func (c *recordingClient) ReleaseTerminal(context.Context, acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, nil
}
func (c *recordingClient) WaitForTerminalExit(context.Context, acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, nil
}

func (c *recordingClient) agentMessageText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, n := range c.updates {
		if n.Update.AgentMessageChunk != nil && n.Update.AgentMessageChunk.Content.Text != nil {
			b.WriteString(n.Update.AgentMessageChunk.Content.Text.Text)
		}
	}
	return b.String()
}

// startAgent wires the agrctl ACP agent to the SDK's ClientSideConnection over
// in-memory pipes (no real process) and returns the client side + recorder.
func startAgent(t *testing.T, ctl *fakeCtl) (*acpsdk.ClientSideConnection, *recordingClient) {
	t.Helper()
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()

	ag := newAgent(options{
		Endpoint: ctlclient.Endpoint{Base: ctl.srv.URL, Token: testToken},
		Agent:    "planner",
		Project:  0,
	})
	agentConn := acpsdk.NewAgentSideConnection(ag, agentToClientW, clientToAgentR)
	ag.SetAgentConnection(agentConn)

	rec := &recordingClient{}
	clientConn := acpsdk.NewClientSideConnection(rec, clientToAgentW, agentToClientR)
	t.Cleanup(func() {
		_ = clientToAgentW.Close()
		_ = agentToClientW.Close()
	})
	return clientConn, rec
}

func newSession(t *testing.T, client *acpsdk.ClientSideConnection) acpsdk.SessionId {
	t.Helper()
	resp, err := client.NewSession(context.Background(), acpsdk.NewSessionRequest{Cwd: "/tmp", McpServers: []acpsdk.McpServer{}})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return resp.SessionId
}

// ---- initialize ----

func TestInitialize_AdvertisesImagePromptCapability(t *testing.T) {
	ctl := newFakeCtl(t)
	client, _ := startAgent(t, ctl)

	resp, err := client.Initialize(context.Background(), acpsdk.InitializeRequest{ProtocolVersion: acpsdk.ProtocolVersionNumber})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if resp.ProtocolVersion != acpsdk.ProtocolVersionNumber {
		t.Fatalf("protocolVersion = %d, want %d", resp.ProtocolVersion, acpsdk.ProtocolVersionNumber)
	}
	if !resp.AgentCapabilities.PromptCapabilities.Image {
		t.Fatal("promptCapabilities.image must be true so the client sends images")
	}
	if resp.AgentCapabilities.LoadSession {
		t.Fatal("loadSession must not be advertised (not implemented)")
	}
}

// ---- session/new + session/prompt ----

func TestPrompt_StreamsChunksAndEndsTurn(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(
		streamEvent{Kind: kindChunk, Delta: "Hello, "},
		streamEvent{Kind: kindChunk, Delta: "world"},
		streamEvent{Kind: kindDone},
	)
	client, rec := startAgent(t, ctl)
	sess := newSession(t, client)

	resp, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("say hi")},
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Fatalf("stopReason = %q, want end_turn", resp.StopReason)
	}
	if got := rec.agentMessageText(); got != "Hello, world" {
		t.Fatalf("streamed text = %q, want %q", got, "Hello, world")
	}
	// 会话映射:新建的 ACP session → 同一个 Agentre sessionId 的 send/stream。
	if q := ctl.streamQueries(); len(q) != 1 || q[0] != "sessionId=100" {
		t.Fatalf("stream queries = %v, want [sessionId=100]", q)
	}
	if got := ctl.lastSend(t)["sessionId"]; got != float64(100) {
		t.Fatalf("send sessionId = %v, want 100", got)
	}
	// 「先订阅再发 send」是协议硬要求:顺序反了会在桌面端丢掉首片。
	if s, e := ctl.streamSeq.Load(), ctl.sendSeq.Load(); s == 0 || e == 0 || s > e {
		t.Fatalf("stream/send order = (%d,%d), want stream before send", s, e)
	}
}

func TestPrompt_ThinkingBecomesThoughtChunk(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(
		streamEvent{Kind: kindThinking, Delta: "pondering"},
		streamEvent{Kind: kindDone},
	)
	client, rec := startAgent(t, ctl)
	sess := newSession(t, client)

	if _, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	found := false
	for _, n := range rec.updates {
		if n.Update.AgentThoughtChunk != nil && n.Update.AgentThoughtChunk.Content.Text != nil &&
			n.Update.AgentThoughtChunk.Content.Text.Text == "pondering" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no thought chunk in %+v", rec.updates)
	}
}

func TestPrompt_ToolUseAndResultBecomeToolCallUpdates(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(
		streamEvent{Kind: kindToolUse, ToolCallID: "call_1", ToolName: "Bash", ToolInput: json.RawMessage(`{"cmd":"ls"}`)},
		streamEvent{Kind: kindToolResult, ToolCallID: "call_1", ToolResult: "ok"},
		streamEvent{Kind: kindDone},
	)
	client, rec := startAgent(t, ctl)
	sess := newSession(t, client)

	if _, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("run")},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	var sawCall, sawUpdate bool
	for _, n := range rec.updates {
		if tl := n.Update.ToolCall; tl != nil {
			sawCall = true
			if tl.ToolCallId != "call_1" || tl.Title != "Bash" {
				t.Fatalf("tool call = %+v", tl)
			}
			if tl.Kind != acpsdk.ToolKindOther {
				t.Fatalf("kind = %q, want %q (most generic)", tl.Kind, acpsdk.ToolKindOther)
			}
		}
		if tu := n.Update.ToolCallUpdate; tu != nil {
			sawUpdate = true
			if tu.ToolCallId != "call_1" {
				t.Fatalf("tool update = %+v", tu)
			}
		}
	}
	if !sawCall || !sawUpdate {
		t.Fatalf("tool frames missing: %+v", rec.updates)
	}
}

// ---- images ----

func TestPrompt_ImageBlockBecomesDataURLSendImage(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(streamEvent{Kind: kindDone})
	client, _ := startAgent(t, ctl)
	sess := newSession(t, client)

	const b64 = "aGVsbG8="
	if _, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt: []acpsdk.ContentBlock{
			acpsdk.TextBlock("look at this"),
			acpsdk.ImageBlock(b64, "image/png"),
		},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	body := ctl.lastSend(t)
	imgs, ok := body["images"].([]any)
	if !ok || len(imgs) != 1 {
		t.Fatalf("images = %#v, want one entry", body["images"])
	}
	first := imgs[0].(map[string]any)
	if got := first["dataUrl"]; got != "data:image/png;base64,"+b64 {
		t.Fatalf("dataUrl = %v, want data:image/png;base64,%s", got, b64)
	}
	if got := body["text"]; got != "look at this" {
		t.Fatalf("text = %v, want %q", got, "look at this")
	}
}

// ---- unsupported content ----

func TestPrompt_UnsupportedContentBlockReturnsReadableError(t *testing.T) {
	ctl := newFakeCtl(t)
	client, _ := startAgent(t, ctl)
	sess := newSession(t, client)

	_, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.AudioBlock("AAAA", "audio/wav")},
	})
	if err == nil {
		t.Fatal("want an error for unsupported audio content")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "audio") {
		t.Fatalf("error = %v, want it to mention audio", err)
	}
	ctl.mu.Lock()
	sends := len(ctl.sends)
	ctl.mu.Unlock()
	if sends != 0 {
		t.Fatalf("unsupported prompt must not dispatch a send (sends=%d)", sends)
	}
}

// ---- cancel ----

func TestPrompt_CancelReturnsCanceledAndStopsSession(t *testing.T) {
	ctl := newFakeCtl(t)
	// 不推任何终态事件:Prompt 会一直等,直到取消。
	client, _ := startAgent(t, ctl)
	sess := newSession(t, client)

	type result struct {
		resp acpsdk.PromptResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
			SessionId: sess,
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hang")},
		})
		done <- result{resp, err}
	}()

	// 等 send 真的发出(说明订阅已建立),再取消。
	waitFor(t, func() bool {
		ctl.mu.Lock()
		defer ctl.mu.Unlock()
		return len(ctl.sends) == 1
	})

	if err := client.Cancel(context.Background(), acpsdk.CancelNotification{SessionId: sess}); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("prompt returned error on cancel: %v", r.err)
		}
		if r.resp.StopReason != acpsdk.StopReasonCancelled {
			t.Fatalf("stopReason = %q, want acpsdk.StopReasonCancelled", r.resp.StopReason)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not return after cancel")
	}

	// SDK 取消 ctx 先于调用本 Agent 的 Cancel,所以 Prompt 返回可能早于 stop POST
	// 落定;等到 stop 真的记录到。(生产语义:取消第一时间让在途 prompt 返回。)
	waitFor(t, func() bool {
		ctl.mu.Lock()
		defer ctl.mu.Unlock()
		return len(ctl.stops) == 1
	})
	ctl.mu.Lock()
	stops := append([]map[string]any(nil), ctl.stops...)
	ctl.mu.Unlock()
	if stops[0]["sessionId"] != float64(100) {
		t.Fatalf("stop calls = %v, want one for session 100", stops)
	}
}

// ---- error event ----

func TestPrompt_ErrorEventReturnsError(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(
		streamEvent{Kind: kindChunk, Delta: "partial"},
		streamEvent{Kind: kindError, Error: "boom"},
	)
	client, _ := startAgent(t, ctl)
	sess := newSession(t, client)

	_, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("go")},
	})
	if err == nil {
		t.Fatal("want an error for an error event")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want it to carry the event error", err)
	}
}

func TestPrompt_AbortedEventReturnsCanceled(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(
		streamEvent{Kind: kindChunk, Delta: "partial"},
		streamEvent{Kind: kindAborted},
	)
	client, _ := startAgent(t, ctl)
	sess := newSession(t, client)

	resp, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("go")},
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonCancelled {
		t.Fatalf("stopReason = %q, want acpsdk.StopReasonCancelled", resp.StopReason)
	}
}

func TestPrompt_UnknownSessionReturnsError(t *testing.T) {
	ctl := newFakeCtl(t)
	client, _ := startAgent(t, ctl)

	_, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: acpsdk.SessionId("nope"),
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("hi")},
	})
	if err == nil {
		t.Fatal("want an error for an unknown session")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Fatalf("error = %v, want it to name the unknown session", err)
	}
}

// ---- startup validation ----

func TestRun_MissingTargetAgentFailsAtStartup(t *testing.T) {
	var stdout, stderr strings.Builder
	code := run(nil, strings.NewReader(""), &stdout, &stderr, func(string) (string, bool) { return "", false })
	if code == 0 {
		t.Fatal("run must not succeed without a target agent")
	}
	if !strings.Contains(stderr.String(), "agent") {
		t.Fatalf("stderr = %q, want a readable agent hint", stderr.String())
	}
}

func TestRun_UnknownFlagFails(t *testing.T) {
	var stdout, stderr strings.Builder
	code := run([]string{"--bogus"}, strings.NewReader(""), &stdout, &stderr, func(string) (string, bool) { return "", false })
	if code == 0 {
		t.Fatal("run must not succeed on an unknown flag")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
