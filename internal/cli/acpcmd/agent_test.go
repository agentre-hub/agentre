package acpcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"reflect"
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
	permSeq   atomic.Int64

	streamCh chan streamEvent

	// afterPermission 是可选的事件尾巴:仅在收到该轮的 answer-permission 之后才推。
	// 用来模拟「目标 agent 卡在等审批、批准后才继续」的真实时序。
	afterPermission []streamEvent

	permissions []map[string]any
	permCh      chan map[string]any
	// failPermission=true 时 answer-permission 返回 500,用于模拟取消与回灌竞争。
	failPermission atomic.Bool
}

func newFakeCtl(t *testing.T) *fakeCtl {
	t.Helper()
	f := &fakeCtl{newSessionID: 100, streamCh: make(chan streamEvent, 64), permCh: make(chan map[string]any, 8)}
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
		tail := append([]streamEvent(nil), f.afterPermission...)
		f.mu.Unlock()
		go func() {
			for _, ev := range events {
				f.streamCh <- ev
			}
			if len(tail) > 0 {
				// 模拟目标 agent 在等审批:没有 answer-permission 就不继续推。
				select {
				case <-f.permCh:
				case <-time.After(5 * time.Second):
				}
			}
			for _, ev := range tail {
				f.streamCh <- ev
			}
		}()
		_, _ = io.WriteString(w, `{"sessionId":100,"assistantMessageId":200,"done":false}`)
	})
	mux.HandleFunc("/ctl/v1/answer-permission", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if f.failPermission.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"permission sink gone"}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.permissions = append(f.permissions, body)
		f.permSeq.Store(f.order.Add(1))
		f.mu.Unlock()
		f.permCh <- body
		_, _ = io.WriteString(w, `{"answered":true}`)
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

// setEventsWithTail 设置「立即推的事件」与「批准后才推的事件尾巴」。
func (f *fakeCtl) setEventsWithTail(head, tail []streamEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = head
	f.afterPermission = tail
}

func (f *fakeCtl) lastPermission(t *testing.T) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.permissions) == 0 {
		t.Fatal("no /ctl/v1/answer-permission call recorded")
	}
	return f.permissions[len(f.permissions)-1]
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
	mu       sync.Mutex
	updates  []acpsdk.SessionNotification
	permFunc func(context.Context, acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error)
	permReqs []acpsdk.RequestPermissionRequest
}

func (c *recordingClient) ReadTextFile(context.Context, acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, nil
}
func (c *recordingClient) WriteTextFile(context.Context, acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, nil
}
func (c *recordingClient) RequestPermission(ctx context.Context, req acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	c.mu.Lock()
	c.permReqs = append(c.permReqs, req)
	fn := c.permFunc
	c.mu.Unlock()
	if fn != nil {
		return fn(ctx, req)
	}
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

func (c *recordingClient) permissionRequests() []acpsdk.RequestPermissionRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acpsdk.RequestPermissionRequest(nil), c.permReqs...)
}

// updateKinds 按到达顺序给每条 session/update 打一个标签,用来断言流顺序。
func (c *recordingClient) updateKinds() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var kinds []string
	for _, n := range c.updates {
		switch {
		case n.Update.ToolCall != nil:
			kinds = append(kinds, "tool_call")
		case n.Update.ToolCallUpdate != nil:
			kinds = append(kinds, "tool_result")
		case n.Update.AgentMessageChunk != nil:
			kinds = append(kinds, "chunk")
		}
	}
	return kinds
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

// ---- resource_link / embedded resource ----

// TestPromptPayload_ResourceLinkBecomesText 锁定:client 用 resource_link / resource 引用
// 文件或上下文时必须转成给目标 agent 的文本,而不是整轮报错。
func TestPromptPayload_ResourceLinkBecomesText(t *testing.T) {
	cases := []struct {
		name  string
		block acpsdk.ContentBlock
		want  string
	}{
		{
			name:  "file uri 还原为本地绝对路径并附 name",
			block: acpsdk.ResourceLinkBlock("doc.pdf", "file:///home/user/doc.pdf"),
			want:  "引用的文件：/home/user/doc.pdf（doc.pdf）",
		},
		{
			name:  "非 file uri 原样引用并附 name",
			block: acpsdk.ResourceLinkBlock("spec", "https://example.com/spec"),
			want:  "引用的资源：https://example.com/spec（spec）",
		},
		{
			name:  "无 name 时不加括号",
			block: acpsdk.ResourceLinkBlock("", "file:///tmp/notes.md"),
			want:  "引用的文件：/tmp/notes.md",
		},
		{
			name: "内嵌 text 原样并入,不包 markdown 代码块",
			block: acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
				TextResourceContents: &acpsdk.TextResourceContents{Uri: "file:///tmp/a.go", Text: "package main\n"},
			}),
			want: "package main\n",
		},
		{
			name: "无 text 的内嵌资源按资源链接规则降级",
			block: acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
				TextResourceContents: &acpsdk.TextResourceContents{Uri: "file:///tmp/empty.txt"},
			}),
			want: "引用的文件：/tmp/empty.txt",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, images, err := promptPayload([]acpsdk.ContentBlock{tc.block})
			if err != nil {
				t.Fatalf("promptPayload: %v", err)
			}
			if text != tc.want {
				t.Fatalf("text = %q, want %q", text, tc.want)
			}
			if len(images) != 0 {
				t.Fatalf("images = %v, want none", images)
			}
		})
	}
}

// TestPromptPayload_ResourceLinkOnlyHasContent 是本次修复的核心断言:只有 resource_link
// 时转换后已有文本,不能落进「no text or image content」校验。
func TestPromptPayload_ResourceLinkOnlyHasContent(t *testing.T) {
	text, _, err := promptPayload([]acpsdk.ContentBlock{
		acpsdk.ResourceLinkBlock("doc.pdf", "file:///home/user/doc.pdf"),
	})
	if err != nil {
		t.Fatalf("a lone resource_link must not fail validation: %v", err)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("resource_link must contribute text")
	}
}

// TestPromptPayload_ResourceBlocksJoinWithNewline 锁定多 block 之间仍用 \n 连接。
func TestPromptPayload_ResourceBlocksJoinWithNewline(t *testing.T) {
	text, _, err := promptPayload([]acpsdk.ContentBlock{
		acpsdk.TextBlock("看这个"),
		acpsdk.ResourceLinkBlock("doc.pdf", "file:///home/user/doc.pdf"),
	})
	if err != nil {
		t.Fatalf("promptPayload: %v", err)
	}
	want := "看这个\n引用的文件：/home/user/doc.pdf（doc.pdf）"
	if text != want {
		t.Fatalf("text = %q, want %q", text, want)
	}
}

// TestPromptPayload_EmbeddedBlobResourceIsRejected 锁定二进制内嵌资源仍然明确报错。
func TestPromptPayload_EmbeddedBlobResourceIsRejected(t *testing.T) {
	_, _, err := promptPayload([]acpsdk.ContentBlock{
		acpsdk.ResourceBlock(acpsdk.EmbeddedResourceResource{
			BlobResourceContents: &acpsdk.BlobResourceContents{Uri: "file:///tmp/a.bin", Blob: "AAEC"},
		}),
	})
	if err == nil || err.Error() != "acp: embedded binary resource is not supported" {
		t.Fatalf("err = %v, want acp: embedded binary resource is not supported", err)
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

// ---- permission: exposed options contract ----

// TestPermissionOptions_ExposeOnlySingleShotKinds 锁定对外权限选项契约:
// Given agrctl 作为中间层不知道目标 agent 实际提供哪些 kind,且 Agentre 的
// AnswerToolPermission 只有单次语义,
// When 构造发给外部 ACP client 的权限选项,
// Then 只能出现 allow_once 与 reject_once,绝不出现 allow_always / reject_always。
func TestPermissionOptions_ExposeOnlySingleShotKinds(t *testing.T) {
	options := permissionOptions()
	got := make([]acpsdk.PermissionOptionKind, 0, len(options))
	for _, o := range options {
		got = append(got, o.Kind)
	}
	want := []acpsdk.PermissionOptionKind{
		acpsdk.PermissionOptionKindAllowOnce,
		acpsdk.PermissionOptionKindRejectOnce,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("permission option kinds = %v, want exactly %v", got, want)
	}
	for _, o := range options {
		if o.Kind == acpsdk.PermissionOptionKindAllowAlways || o.Kind == acpsdk.PermissionOptionKindRejectAlways {
			t.Fatalf("option %+v advertises an always-kind that Agentre cannot honor", o)
		}
	}
}

// ---- permission: option kind -> decision ----

// TestResolvePermissionDecision_MapsByOptionKind 覆盖纯函数的 kind → 决策映射。
// 注意:allow_always 只是本函数对调用方传入选项的通用处理;agrctl 对外绝不再
// 暴露该 kind(见 TestPermissionOptions_ExposeOnlySingleShotKinds)。
func TestResolvePermissionDecision_MapsByOptionKind(t *testing.T) {
	cases := []struct {
		name string
		kind acpsdk.PermissionOptionKind
		want permissionDecision
	}{
		{"allow_once", acpsdk.PermissionOptionKindAllowOnce, permissionDecision{Allow: true}},
		{"allow_always", acpsdk.PermissionOptionKindAllowAlways, permissionDecision{Allow: true, AlwaysAllowSession: true}},
		{"reject_once", acpsdk.PermissionOptionKindRejectOnce, permissionDecision{Allow: false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options := []acpsdk.PermissionOption{{OptionId: "opt-1", Name: "x", Kind: tc.kind}}
			got := resolvePermissionDecision(options, acpsdk.NewRequestPermissionOutcomeSelected("opt-1"))
			if got != tc.want {
				t.Fatalf("decision = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// optionId 字面与 kind 语义相反时必须按 kind 走 —— 决策语义由 kind 定义,
// optionId 只是回传句柄。
func TestResolvePermissionDecision_KindWinsOverOptionId(t *testing.T) {
	// optionId 叫 reject-once,但 kind 是 allow_once:必须放行。
	options := []acpsdk.PermissionOption{
		{OptionId: "reject-once", Name: "actually allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
	}
	got := resolvePermissionDecision(options, acpsdk.NewRequestPermissionOutcomeSelected("reject-once"))
	if !got.Allow || got.AlwaysAllowSession {
		t.Fatalf("decision = %+v, want allow once (kind governs)", got)
	}
	// 反向:optionId 叫 allow-once,但 kind 是 reject_once:必须拒绝。
	options = []acpsdk.PermissionOption{
		{OptionId: "allow-once", Name: "actually reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
	}
	got = resolvePermissionDecision(options, acpsdk.NewRequestPermissionOutcomeSelected("allow-once"))
	if got.Allow {
		t.Fatalf("decision = %+v, want deny (kind governs)", got)
	}
}

func TestResolvePermissionDecision_DeniesWhenCancelledOrUnknown(t *testing.T) {
	options := permissionOptions()
	if got := resolvePermissionDecision(options, acpsdk.NewRequestPermissionOutcomeCancelled()); got.Allow {
		t.Fatalf("canceled -> %+v, want deny", got)
	}
	// client 选了一个我们没发过的 optionId:不能猜,直接拒绝。
	if got := resolvePermissionDecision(options, acpsdk.NewRequestPermissionOutcomeSelected("bogus")); got.Allow {
		t.Fatalf("unknown option -> %+v, want deny", got)
	}
	// 未映射的 kind(reject_always)也不放行。
	weird := []acpsdk.PermissionOption{{OptionId: "ra", Name: "x", Kind: acpsdk.PermissionOptionKindRejectAlways}}
	if got := resolvePermissionDecision(weird, acpsdk.NewRequestPermissionOutcomeSelected("ra")); got.Allow {
		t.Fatalf("reject_always -> %+v, want deny", got)
	}
}

// ---- permission: round-trip through Prompt ----

func permissionEvent(requestID, tool, input string) streamEvent {
	return streamEvent{
		Kind: kindToolPermissionRequest,
		ToolPermission: &streamToolPermission{
			RequestID: requestID,
			ToolName:  tool,
			ToolInput: json.RawMessage(input),
		},
	}
}

// decideWithKind 是测试用 ACP client:在 agent 给出的选项里选指定 kind 的那一项。
func decideWithKind(kind acpsdk.PermissionOptionKind) func(context.Context, acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	return func(_ context.Context, req acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		for _, o := range req.Options {
			if o.Kind == kind {
				return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected(o.OptionId)}, nil
			}
		}
		return acpsdk.RequestPermissionResponse{}, fmt.Errorf("no option with kind %q", kind)
	}
}

func TestPrompt_PermissionApprovedForwardsAllow(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(permissionEvent("req-1", "Bash", `{"cmd":"ls"}`), streamEvent{Kind: kindDone})
	client, rec := startAgent(t, ctl)
	rec.permFunc = decideWithKind(acpsdk.PermissionOptionKindAllowOnce)

	sess := newSession(t, client)
	resp, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("run ls")},
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Fatalf("stopReason = %q, want end_turn", resp.StopReason)
	}

	body := ctl.lastPermission(t)
	if body["sessionId"] != float64(100) || body["requestId"] != "req-1" || body["allow"] != true {
		t.Fatalf("answer-permission body = %#v", body)
	}
	if _, ok := body["alwaysAllowSession"]; ok {
		t.Fatalf("allow_once must not set alwaysAllowSession: %#v", body)
	}

	// 发给 client 的 RequestPermissionRequest:绑定 ACP session,带上工具信息,
	// 且只暴露能真实落实的两种 kind。
	reqs := rec.permissionRequests()
	if len(reqs) != 1 {
		t.Fatalf("permission requests = %d, want 1", len(reqs))
	}
	got := reqs[0]
	if got.SessionId != sess {
		t.Fatalf("sessionId = %q, want %q", got.SessionId, sess)
	}
	if got.ToolCall.Title == nil || *got.ToolCall.Title != "Bash" {
		t.Fatalf("toolCall.title = %v, want Bash", got.ToolCall.Title)
	}
	kinds := map[acpsdk.PermissionOptionKind]bool{}
	for _, o := range got.Options {
		kinds[o.Kind] = true
	}
	for _, want := range []acpsdk.PermissionOptionKind{
		acpsdk.PermissionOptionKindAllowOnce,
		acpsdk.PermissionOptionKindRejectOnce,
	} {
		if !kinds[want] {
			t.Fatalf("options missing kind %q: %+v", want, got.Options)
		}
	}
	for _, bad := range []acpsdk.PermissionOptionKind{
		acpsdk.PermissionOptionKindAllowAlways,
		acpsdk.PermissionOptionKindRejectAlways,
	} {
		if kinds[bad] {
			t.Fatalf("options must not expose kind %q: %+v", bad, got.Options)
		}
	}
}

// TestPrompt_PermissionUnadvertisedAlwaysOptionIsDenied 锁定修复后的对外契约:
// Given agrctl 不再暴露 allow_always,
// When 外部 ACP client 硬塞一个未提供的 allow-always optionId,
// Then 必须收敛成一次明确拒绝,且绝不能回灌 alwaysAllowSession=true ——
// Agentre 的 AnswerToolPermission 只有单次语义,allow_always 无法落实。
func TestPrompt_PermissionUnadvertisedAlwaysOptionIsDenied(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(permissionEvent("req-always", "Write", `{}`), streamEvent{Kind: kindDone})
	client, rec := startAgent(t, ctl)
	rec.permFunc = func(_ context.Context, _ acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeSelected("allow-always")}, nil
	}

	sess := newSession(t, client)
	if _, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("write")},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}

	reqs := rec.permissionRequests()
	if len(reqs) != 1 {
		t.Fatalf("permission requests = %d, want 1", len(reqs))
	}
	for _, o := range reqs[0].Options {
		if o.Kind == acpsdk.PermissionOptionKindAllowAlways || o.Kind == acpsdk.PermissionOptionKindRejectAlways {
			t.Fatalf("advertised an always-kind that Agentre cannot honor: %+v", reqs[0].Options)
		}
	}
	body := ctl.lastPermission(t)
	if body["allow"] != false {
		t.Fatalf("answer-permission body = %#v, want allow=false for an unadvertised always option", body)
	}
	if _, ok := body["alwaysAllowSession"]; ok {
		t.Fatalf("must never forward alwaysAllowSession: %#v", body)
	}
}

func TestPrompt_PermissionClientErrorDenies(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(permissionEvent("req-err", "Bash", `{"cmd":"rm"}`), streamEvent{Kind: kindDone})
	client, rec := startAgent(t, ctl)
	rec.permFunc = func(context.Context, acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{}, errors.New("client exploded")
	}

	sess := newSession(t, client)
	resp, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("go")},
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Fatalf("stopReason = %q, want end_turn (deny must not kill the turn)", resp.StopReason)
	}
	body := ctl.lastPermission(t)
	if body["allow"] != false || body["requestId"] != "req-err" {
		t.Fatalf("answer-permission body = %#v, want allow=false", body)
	}
}

func TestPrompt_PermissionClientCancelledDenies(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(permissionEvent("req-cancel", "Bash", `{}`), streamEvent{Kind: kindDone})
	client, rec := startAgent(t, ctl)
	rec.permFunc = func(context.Context, acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		return acpsdk.RequestPermissionResponse{Outcome: acpsdk.NewRequestPermissionOutcomeCancelled()}, nil
	}

	sess := newSession(t, client)
	if _, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("go")},
	}); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if body := ctl.lastPermission(t); body["allow"] != false {
		t.Fatalf("answer-permission body = %#v, want allow=false", body)
	}
}

// client 完全不回时,靠 prompt 的 ctx 取消把等待打断,并且必须补一条 deny ——
// 目标 agent 不能因为 client 装死而永远停在审批。
func TestPrompt_PermissionClientSilentDeniesOnCancel(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(permissionEvent("req-silent", "Bash", `{}`))
	client, rec := startAgent(t, ctl)
	started := make(chan struct{})
	rec.permFunc = func(ctx context.Context, _ acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		close(started)
		<-ctx.Done()
		return acpsdk.RequestPermissionResponse{}, ctx.Err()
	}
	sess := newSession(t, client)

	done := make(chan error, 1)
	go func() {
		_, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
			SessionId: sess,
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("go")},
		})
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("client never received the permission request")
	}
	if err := client.Cancel(context.Background(), acpsdk.CancelNotification{SessionId: sess}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("prompt hung after cancel with a silent permission client")
	}
	body := ctl.lastPermission(t)
	if body["allow"] != false || body["requestId"] != "req-silent" {
		t.Fatalf("answer-permission body = %#v, want allow=false", body)
	}
}

// 取消与 deny 回灌会竞争:即使回灌本身失败(目标端 waiter 已被 stop 拆掉),
// prompt 也必须以 canceled 收尾而不是报错 —— 否则 client 收到的是 JSON-RPC 错误
// 而不是 stopReason=canceled。
func TestPrompt_PermissionAnswerFailureOnCancelStillReturnsCancelled(t *testing.T) {
	ctl := newFakeCtl(t)
	ctl.setEvents(permissionEvent("req-race", "Bash", `{}`))
	ctl.failPermission.Store(true)
	client, rec := startAgent(t, ctl)
	started := make(chan struct{})
	rec.permFunc = func(ctx context.Context, _ acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
		close(started)
		<-ctx.Done()
		return acpsdk.RequestPermissionResponse{}, ctx.Err()
	}
	sess := newSession(t, client)

	type result struct {
		resp acpsdk.PromptResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
			SessionId: sess,
			Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("go")},
		})
		done <- result{resp, err}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("client never received the permission request")
	}
	if err := client.Cancel(context.Background(), acpsdk.CancelNotification{SessionId: sess}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("cancel with a failed deny must not surface an error: %v", r.err)
		}
		if r.resp.StopReason != acpsdk.StopReasonCancelled {
			t.Fatalf("stopReason = %q, want canceled", r.resp.StopReason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("prompt hung")
	}
}

// resolved 帧是审批完成后的状态切换:必须跳过,绝不能拿它再问 client。
// 目标端 waiter 已消失,二次 answer-permission 会直接报错把整轮弄挂。
func TestPrompt_ResolvedPermissionEventIsSkipped(t *testing.T) {
	ctl := newFakeCtl(t)
	resolved := permissionEvent("req-done", "Bash", `{}`)
	resolved.ToolPermission.Resolved = true
	ctl.setEvents(resolved, streamEvent{Kind: kindDone})
	client, rec := startAgent(t, ctl)
	sess := newSession(t, client)

	resp, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("go")},
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Fatalf("stopReason = %q, want end_turn", resp.StopReason)
	}
	if got := rec.permissionRequests(); len(got) != 0 {
		t.Fatalf("resolved permission must not be re-asked: %+v", got)
	}
	ctl.mu.Lock()
	answered := len(ctl.permissions)
	ctl.mu.Unlock()
	if answered != 0 {
		t.Fatalf("resolved permission must not be answered again (answered=%d)", answered)
	}
}

// ---- subprocess integration: real agrctl acp process ----

func TestIntegration_AgrctlSubprocessForwardsToolPermission(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess integration test skipped in -short mode")
	}
	bin := buildAgrctl(t)
	ctl := newFakeCtl(t)
	ctl.setEventsWithTail(
		[]streamEvent{
			{Kind: kindToolUse, ToolCallID: "call-1", ToolName: "Bash", ToolInput: json.RawMessage(`{"cmd":"ls"}`)},
			permissionEvent("req-1", "Bash", `{"cmd":"ls"}`),
		},
		[]streamEvent{
			// 审批完成后 chat_svc 会再推一条 resolved 帧:必须被跳过,不能二次提问。
			{Kind: kindToolPermissionRequest, ToolPermission: &streamToolPermission{RequestID: "req-1", ToolName: "Bash", Resolved: true}},
			{Kind: kindToolResult, ToolCallID: "call-1", ToolResult: "file.txt"},
			{Kind: kindChunk, Delta: "all done"},
			{Kind: kindDone},
		},
	)

	client, rec, _ := startSubprocessAgent(t, bin, ctl)
	rec.permFunc = decideWithKind(acpsdk.PermissionOptionKindAllowOnce)
	sess := newSession(t, client)

	resp, err := client.Prompt(context.Background(), acpsdk.PromptRequest{
		SessionId: sess,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock("list files")},
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Fatalf("stopReason = %q, want end_turn", resp.StopReason)
	}
	if got := rec.agentMessageText(); got != "all done" {
		t.Fatalf("streamed text = %q, want %q", got, "all done")
	}
	if got := rec.updateKinds(); !reflect.DeepEqual(got, []string{"tool_call", "tool_result", "chunk"}) {
		t.Fatalf("update order = %v, want [tool_call tool_result chunk]", got)
	}
	body := ctl.lastPermission(t)
	if body["sessionId"] != float64(100) || body["requestId"] != "req-1" || body["allow"] != true {
		t.Fatalf("answer-permission body = %#v", body)
	}
	if _, ok := body["alwaysAllowSession"]; ok {
		t.Fatalf("allow_once must not set alwaysAllowSession: %#v", body)
	}
	if s, e, p := ctl.streamSeq.Load(), ctl.sendSeq.Load(), ctl.permSeq.Load(); s <= 0 || s >= e || e >= p {
		t.Fatalf("arrival order stream/send/permission = %d/%d/%d, want increasing", s, e, p)
	}
	// resolved 帧不得触发第二次 ACP 提问 / 回灌。
	if got := rec.permissionRequests(); len(got) != 1 {
		t.Fatalf("permission requests = %d, want exactly 1 (resolved frame must be skipped)", len(got))
	}
	ctl.mu.Lock()
	answered := len(ctl.permissions)
	ctl.mu.Unlock()
	if answered != 1 {
		t.Fatalf("answer-permission calls = %d, want exactly 1", answered)
	}
}

// buildAgrctl 编译真实的 agrctl 二进制,供子进程集成测试使用。
func buildAgrctl(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}").Output()
	if err != nil {
		t.Fatalf("locate module root: %v", err)
	}
	root := strings.TrimSpace(string(out))
	bin := filepath.Join(t.TempDir(), "agrctl")
	build := exec.Command("go", "build", "-o", bin, "./cmd/agrctl") //nolint:gosec // G204: fixed args, bin is a t.TempDir path
	build.Dir = root
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build agrctl: %v\n%s", err, b)
	}
	return bin
}

// startSubprocessAgent 以 "agrctl acp" 形态起真实子进程,并把它的 stdio 接到
// SDK 的 ClientSideConnection 上。
func startSubprocessAgent(t *testing.T, bin string, ctl *fakeCtl) (*acpsdk.ClientSideConnection, *recordingClient, *exec.Cmd) {
	t.Helper()
	clientToAgentR, clientToAgentW := io.Pipe()
	agentToClientR, agentToClientW := io.Pipe()

	cmd := exec.Command(bin, "acp", "--agent", "planner", "--endpoint", ctl.srv.URL, "--token", testToken) //nolint:gosec // G204: bin is the test-built agrctl binary
	cmd.Stdin = clientToAgentR
	cmd.Stdout = agentToClientW
	cmd.WaitDelay = 3 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start agrctl acp: %v", err)
	}

	rec := &recordingClient{}
	clientConn := acpsdk.NewClientSideConnection(rec, clientToAgentW, agentToClientR)
	t.Cleanup(func() {
		// 先关管道再等子进程:否则 exec 的 stdin/stdout 拷贝 goroutine 会卡在
		// 永不 EOF 的 io.Pipe 上,cmd.Wait 永不返回。
		_ = clientToAgentW.Close()
		_ = clientToAgentR.Close()
		_ = agentToClientW.Close()
		_ = agentToClientR.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() {
			t.Logf("agrctl acp stderr:\n%s", stderr.String())
		}
	})
	return clientConn, rec, cmd
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
