package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

// fakeAgent 是一个脚本化的内存 ACP agent:走真实线格式(os.Pipe 上的换行分隔
// JSON-RPC),因此 SDK 的分发与我们的 client 实现跑的都是生产路径。测试直接
// 驱动它(发 update / 回 prompt / 发审批请求),读循环只自动应答握手类方法。
type fakeAgent struct {
	t    *testing.T
	opts fakeAgentOptions

	mu           sync.Mutex
	w            io.Writer // 最新一条连接的 agent→client 写端
	dials        [][4]*os.File
	nextReqID    int64
	lastPromptID json.RawMessage
	permWaiters  map[string]chan acpsdk.RequestPermissionResponse

	initReq        acpsdk.InitializeRequest
	initSeen       bool
	newSessionCall int
	loadCalls      int
	lastMCP        []acpsdk.McpServer
	// lastNewRaw / lastLoadRaw 是 session/new 与 session/load 的原始 params 字节。
	// 结构体解码抹掉了 nil 与 [] 的区别(两者都解成空切片),而线上那一字节之差
	// 正好决定真实 agent 收不收 —— 见 TestNegotiation_MCPServersFrameIsAlwaysArray。
	lastNewRaw  json.RawMessage
	lastLoadRaw json.RawMessage
	lastLoadID  string
	lastCwd     string
	promptReq   acpsdk.PromptRequest

	promptSeen chan acpsdk.PromptRequest
	cancels    chan struct{}
}

type fakeAgentOptions struct {
	protocolVersion int // 默认 1
	promptImage     bool
	mcpHTTP         bool
	loadSession     bool
	loadFails       bool
	sessionID       string // 默认 "acp-sess-1"
}

// fakeTransport 把一对内存管道适配成 agentTransport。
type fakeTransport struct {
	r *os.File // client 读(agent 写)
	w *os.File // client 写(agent 读)
}

func (t *fakeTransport) Read(p []byte) (int, error)  { return t.r.Read(p) }
func (t *fakeTransport) Write(p []byte) (int, error) { return t.w.Write(p) }
func (t *fakeTransport) Close(context.Context) error {
	_ = t.r.Close()
	_ = t.w.Close()
	return nil
}
func (t *fakeTransport) Kill() error {
	_ = t.r.Close()
	_ = t.w.Close()
	return nil
}
func (t *fakeTransport) PID() int           { return 0 }
func (t *fakeTransport) StderrTail() string { return "" }

// installFakeAgent 起 fake agent 并把 dialAgentTransport 换成内存管道,
// 返回 fake。每次 dial 各建一对新管道(生产里是一次新 spawn),全都接到同一个
// 脚本化 agent 上;测试侧的 send/respond 总是打在最新那条连接上。
func installFakeAgent(t *testing.T, opts fakeAgentOptions) *fakeAgent {
	t.Helper()
	if opts.protocolVersion == 0 {
		opts.protocolVersion = 1
	}
	if opts.sessionID == "" {
		opts.sessionID = "acp-sess-1"
	}
	fa := &fakeAgent{
		t:           t,
		opts:        opts,
		permWaiters: map[string]chan acpsdk.RequestPermissionResponse{},
		promptSeen:  make(chan acpsdk.PromptRequest, 8),
		cancels:     make(chan struct{}, 8),
	}
	orig := dialAgentTransport
	dialAgentTransport = func(context.Context, launchSpec) (agentTransport, error) {
		agentRead, clientWrite, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		clientRead, agentWrite, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		fa.mu.Lock()
		fa.w = agentWrite
		fa.dials = append(fa.dials, [4]*os.File{agentRead, agentWrite, clientRead, clientWrite})
		fa.mu.Unlock()
		go fa.run(agentRead)
		return &fakeTransport{r: clientRead, w: clientWrite}, nil
	}
	t.Cleanup(func() {
		dialAgentTransport = orig
		fa.mu.Lock()
		files := fa.dials
		fa.mu.Unlock()
		for _, set := range files {
			for _, f := range set {
				_ = f.Close()
			}
		}
	})
	return fa
}

// run 是 fake 的读循环:自动应答 initialize / session/new / session/load,
// 记录 session/prompt、session/cancel 与客户端对审批请求的应答。
func (fa *fakeAgent) run(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var msg struct {
			ID     *json.RawMessage     `json:"id,omitempty"`
			Method string               `json:"method,omitempty"`
			Params json.RawMessage      `json:"params,omitempty"`
			Result json.RawMessage      `json:"result,omitempty"`
			Error  *acpsdk.RequestError `json:"error,omitempty"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		switch msg.Method {
		case "":
			// 客户端对我们发出的 session/request_permission 的应答。
			if msg.ID == nil {
				continue
			}
			var key string
			_ = json.Unmarshal(*msg.ID, &key)
			fa.mu.Lock()
			ch := fa.permWaiters[key]
			delete(fa.permWaiters, key)
			fa.mu.Unlock()
			if ch == nil {
				continue
			}
			var resp acpsdk.RequestPermissionResponse
			if len(msg.Result) > 0 {
				_ = json.Unmarshal(msg.Result, &resp)
			}
			ch <- resp
		case "initialize":
			var req acpsdk.InitializeRequest
			_ = json.Unmarshal(msg.Params, &req)
			fa.mu.Lock()
			fa.initReq = req
			fa.initSeen = true
			fa.mu.Unlock()
			fa.respond(msg.ID, acpsdk.InitializeResponse{
				ProtocolVersion: acpsdk.ProtocolVersion(fa.opts.protocolVersion),
				AgentCapabilities: acpsdk.AgentCapabilities{
					LoadSession: fa.opts.loadSession,
					PromptCapabilities: acpsdk.PromptCapabilities{
						Image: fa.opts.promptImage,
					},
					McpCapabilities: acpsdk.McpCapabilities{Http: fa.opts.mcpHTTP},
				},
				AgentInfo: &acpsdk.Implementation{Name: "fake-agent", Version: "0.0.1"},
			})
		case "session/new":
			var req acpsdk.NewSessionRequest
			_ = json.Unmarshal(msg.Params, &req)
			fa.mu.Lock()
			fa.newSessionCall++
			fa.lastMCP = req.McpServers
			fa.lastNewRaw = append(json.RawMessage(nil), msg.Params...)
			fa.lastCwd = req.Cwd
			fa.mu.Unlock()
			fa.respond(msg.ID, acpsdk.NewSessionResponse{SessionId: acpsdk.SessionId(fa.opts.sessionID)})
		case "session/load":
			var req acpsdk.LoadSessionRequest
			_ = json.Unmarshal(msg.Params, &req)
			fa.mu.Lock()
			fa.loadCalls++
			fa.lastLoadID = string(req.SessionId)
			fa.lastMCP = req.McpServers
			fa.lastLoadRaw = append(json.RawMessage(nil), msg.Params...)
			fa.lastCwd = req.Cwd
			fa.mu.Unlock()
			if fa.opts.loadFails {
				fa.respondError(msg.ID, -32001, "session not found")
				return
			}
			fa.respond(msg.ID, acpsdk.LoadSessionResponse{})
		case "session/prompt":
			var req acpsdk.PromptRequest
			_ = json.Unmarshal(msg.Params, &req)
			fa.mu.Lock()
			fa.promptReq = req
			fa.lastPromptID = append(json.RawMessage(nil), *msg.ID...)
			fa.mu.Unlock()
			fa.promptSeen <- req
		case "session/cancel":
			fa.cancels <- struct{}{}
		case "$/cancel_request":
			// client 侧取消在途请求的协议内通知,忽略即可。
		default:
			if msg.ID != nil {
				fa.respondError(msg.ID, -32601, "method not found: "+msg.Method)
			}
		}
	}
}

func (fa *fakeAgent) writeLine(payload any) {
	// 写失败(对端已关)静默返回:测试结束后残留的 goroutine 不该把已结束的
	// test 拉挂;正确性由各 wait* 显式断言。
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fa.mu.Lock()
	_, werr := fa.w.Write(append(b, '\n'))
	fa.mu.Unlock()
	_ = werr
}

func (fa *fakeAgent) respond(id *json.RawMessage, result any) {
	fa.writeLine(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (fa *fakeAgent) respondError(id *json.RawMessage, code int, message string) {
	fa.writeLine(map[string]any{"jsonrpc": "2.0", "id": id,
		"error": map[string]any{"code": code, "message": message}})
}

// sendUpdate 推一条 session/update 通知。
func (fa *fakeAgent) sendUpdate(u acpsdk.SessionUpdate) {
	fa.writeLine(map[string]any{
		"jsonrpc": "2.0",
		"method":  "session/update",
		"params":  acpsdk.SessionNotification{SessionId: acpsdk.SessionId(fa.opts.sessionID), Update: u},
	})
}

// respondPromptAnswer 用给定 stopReason / usage 应答最近的 session/prompt。
func (fa *fakeAgent) respondPrompt(stopReason acpsdk.StopReason, usage *acpsdk.Usage) {
	fa.mu.Lock()
	id := fa.lastPromptID
	fa.mu.Unlock()
	if id == nil {
		fa.t.Fatal("fake agent: no prompt to respond to")
	}
	fa.respond(&id, acpsdk.PromptResponse{StopReason: stopReason, Usage: usage})
}

// requestPermission 发一条 session/request_permission 请求,交回应答通道。
func (fa *fakeAgent) requestPermission(options []acpsdk.PermissionOption, toolCall acpsdk.ToolCallUpdate) <-chan acpsdk.RequestPermissionResponse {
	fa.mu.Lock()
	fa.nextReqID++
	id := fmt.Sprintf("p%d", fa.nextReqID)
	ch := make(chan acpsdk.RequestPermissionResponse, 1)
	fa.permWaiters[id] = ch
	fa.mu.Unlock()
	rawID := json.RawMessage(strconv.Quote(id))
	fa.writeLine(map[string]any{
		"jsonrpc": "2.0",
		"id":      &rawID,
		"method":  "session/request_permission",
		"params": acpsdk.RequestPermissionRequest{
			SessionId: acpsdk.SessionId(fa.opts.sessionID),
			Options:   options,
			ToolCall:  toolCall,
		},
	})
	return ch
}

// ── 读侧访问器 ───────────────────────────────────────────────────────────────

func (fa *fakeAgent) waitPrompt(timeout time.Duration) acpsdk.PromptRequest {
	fa.t.Helper()
	select {
	case req := <-fa.promptSeen:
		return req
	case <-time.After(timeout):
		fa.t.Fatal("fake agent: timed out waiting for session/prompt")
		return acpsdk.PromptRequest{}
	}
}

func (fa *fakeAgent) waitCancel(timeout time.Duration) bool {
	fa.t.Helper()
	select {
	case <-fa.cancels:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (fa *fakeAgent) waitPermissionReply(ch <-chan acpsdk.RequestPermissionResponse, timeout time.Duration) acpsdk.RequestPermissionResponse {
	fa.t.Helper()
	select {
	case resp := <-ch:
		return resp
	case <-time.After(timeout):
		fa.t.Fatal("fake agent: timed out waiting for permission response")
		return acpsdk.RequestPermissionResponse{}
	}
}

func (fa *fakeAgent) initializeRequest() acpsdk.InitializeRequest {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return fa.initReq
}

func (fa *fakeAgent) newSessionCalls() int {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return fa.newSessionCall
}

func (fa *fakeAgent) loadSessionCalls() int {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return fa.loadCalls
}

func (fa *fakeAgent) lastMCPServers() []acpsdk.McpServer {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return append([]acpsdk.McpServer(nil), fa.lastMCP...)
}

// lastNewSessionParams / lastLoadSessionParams 回原始 params 字节。协议上 nil 与 []
// 在结构体里无法区分,只有原始字节能钉住线格式。
func (fa *fakeAgent) lastNewSessionParams() json.RawMessage {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return append(json.RawMessage(nil), fa.lastNewRaw...)
}

func (fa *fakeAgent) lastLoadSessionParams() json.RawMessage {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return append(json.RawMessage(nil), fa.lastLoadRaw...)
}

func (fa *fakeAgent) lastSessionCwd() string {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return fa.lastCwd
}

func (fa *fakeAgent) lastLoadedSessionID() string {
	fa.mu.Lock()
	defer fa.mu.Unlock()
	return fa.lastLoadID
}
