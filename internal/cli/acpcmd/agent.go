package acpcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
)

// 本包让 agrctl 自己作为一个 ACP v1 Agent 跑在 stdio 上,把收到的 prompt(含图片)
// 经桌面端 ctl 通道派发给配置好的 Agentre agent,并把该轮事件流实时翻译成 ACP
// session/update 回传。
//
// 刻意不 import chat_svc:那个包会拖入 DB/gorm 依赖,而 agrctl 要的是精简与快速启动。
// 事件只用下面这份最小 DTO 反序列化。

// 事件 kind 的字面值(镜像 chat_svc 的 ChatStreamEventKind,但独立成常量以免耦合)。
const (
	kindChunk      = "chunk"
	kindThinking   = "thinking"
	kindToolUse    = "tool_use"
	kindToolResult = "tool_result"
	kindDone       = "done"
	kindError      = "error"
	kindAborted    = "aborted"
)

// streamEvent 是 /ctl/v1/stream 推来的 ChatStreamEvent 的最小投影:只保留翻译成
// ACP 所需字段。未知字段由 encoding/json 自然忽略;toolInput 用 RawMessage 兜住
// 任意形状,原样转成 ACP 的 rawInput。
type streamEvent struct {
	Kind       string          `json:"kind"`
	Delta      string          `json:"delta,omitempty"`
	Error      string          `json:"error,omitempty"`
	ToolCallID string          `json:"toolUseId,omitempty"`
	ToolName   string          `json:"toolName,omitempty"`
	ToolInput  json.RawMessage `json:"toolInput,omitempty"`
	ToolResult string          `json:"toolResult,omitempty"`
	IsError    bool            `json:"isError,omitempty"`
}

// sendBody 是 POST /ctl/v1/send 的请求体(acpcmd 侧投影)。
type sendBody struct {
	SessionID int64       `json:"sessionId"`
	Text      string      `json:"text"`
	Images    []sendImage `json:"images,omitempty"`
}

type sendImage struct {
	DataURL string `json:"dataUrl"`
}

// agentHandler 实现 acpsdk.Agent。
type agentHandler struct {
	endpoint ctlclient.Endpoint
	agent    string
	agentID  int64
	project  int64

	conn *acpsdk.AgentSideConnection

	mu       sync.Mutex
	sessions map[acpsdk.SessionId]int64
	seq      atomic.Int64
}

var _ acpsdk.Agent = (*agentHandler)(nil)

func newAgent(opts options) *agentHandler {
	return &agentHandler{
		endpoint: opts.Endpoint,
		agent:    opts.Agent,
		agentID:  opts.AgentID,
		project:  opts.Project,
		sessions: map[acpsdk.SessionId]int64{},
	}
}

// SetAgentConnection 记下连接(发 session/update 用)。SDK 不会自动调用,由 run 装配。
func (a *agentHandler) SetAgentConnection(c *acpsdk.AgentSideConnection) { a.conn = c }

// Initialize 协商协议版本与能力。Image:true 是本 Agent 的硬要求:Agentre 作为 ACP
// client 只在协商到图片能力后才发图片块,漏了它图片永远收不到。
func (a *agentHandler) Initialize(context.Context, acpsdk.InitializeRequest) (acpsdk.InitializeResponse, error) {
	return acpsdk.InitializeResponse{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		AgentCapabilities: acpsdk.AgentCapabilities{
			LoadSession: false,
			PromptCapabilities: acpsdk.PromptCapabilities{
				Image: true,
			},
		},
	}, nil
}

// NewSession 在桌面端建一个 Agentre 会话,并记住 ACP sessionId → Agentre sessionId 的映射。
func (a *agentHandler) NewSession(ctx context.Context, _ acpsdk.NewSessionRequest) (acpsdk.NewSessionResponse, error) {
	body := map[string]any{
		"agent":     a.agent,
		"agentId":   a.agentID,
		"projectId": a.project,
	}
	var out struct {
		SessionID int64 `json:"sessionId"`
	}
	if err := a.endpoint.Post("/ctl/v1/sessions", body, &out); err != nil {
		return acpsdk.NewSessionResponse{}, fmt.Errorf("acp: create session: %w", err)
	}
	if out.SessionID <= 0 {
		return acpsdk.NewSessionResponse{}, errors.New("acp: control service returned an empty session id")
	}
	acpID := acpsdk.SessionId(fmt.Sprintf("agentre-%d-%d", out.SessionID, a.seq.Add(1)))
	a.mu.Lock()
	a.sessions[acpID] = out.SessionID
	a.mu.Unlock()
	return acpsdk.NewSessionResponse{SessionId: acpID}, nil
}

// Cancel 把 ACP session/cancel 转成桌面端的 stop。SDK 在调用本方法之前已经取消了
// Prompt 的 ctx,所以 Prompt 会立刻以 StopReasonCancelled 返回;这里只负责让远端那一轮
// 也真的停下。
func (a *agentHandler) Cancel(_ context.Context, p acpsdk.CancelNotification) error {
	sessionID, ok := a.sessionOf(p.SessionId)
	if !ok {
		return fmt.Errorf("acp: cannot cancel unknown session %q", p.SessionId)
	}
	// 带超时:session/cancel 由 SDK 在通知处理 goroutine 上同步调用,而那条 goroutine
	// 串行处理全部通知 —— 一个挂死的 stop 请求会把后续通知全部堵住。Prompt 本身已被
	// SDK 先取消 ctx,不受这里影响。
	ctx, cancel := context.WithTimeout(context.Background(), cancelStopTimeout)
	defer cancel()
	if err := a.endpoint.PostContext(ctx, "/ctl/v1/stop", map[string]any{"sessionId": sessionID}, nil); err != nil {
		return fmt.Errorf("acp: stop session: %w", err)
	}
	return nil
}

// cancelStopTimeout 是取消时停止远端轮次的等待上限。
const cancelStopTimeout = 5 * time.Second

// Prompt 是本 Agent 的核心:先打开该会话的 SSE 流,再派发 send,然后逐条把事件翻译成
// ACP session/update,直到终态事件、错误或取消。
func (a *agentHandler) Prompt(ctx context.Context, p acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
	sessionID, ok := a.sessionOf(p.SessionId)
	if !ok {
		return acpsdk.PromptResponse{}, fmt.Errorf("acp: unknown session %q", p.SessionId)
	}
	text, images, err := promptPayload(p.Prompt)
	if err != nil {
		return acpsdk.PromptResponse{}, err
	}

	// 订阅必须先于 send:顺序反了会丢首片。
	// 另起一个可取消的 ctx:Prompt 因终态事件提前返回时,读流 goroutine 也随之退出,
	// 不靠「远端恰好不再发事件」来收尾。
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()
	stream, err := a.endpoint.OpenStream(streamCtx, "/ctl/v1/stream?sessionId="+strconv.FormatInt(sessionID, 10))
	if err != nil {
		return acpsdk.PromptResponse{}, fmt.Errorf("acp: open event stream: %w", err)
	}
	defer func() { _ = stream.Close() }()

	events, readErr := readStream(streamCtx, stream)
	if err := a.endpoint.Post("/ctl/v1/send", sendBody{SessionID: sessionID, Text: text, Images: images}, nil); err != nil {
		return acpsdk.PromptResponse{}, fmt.Errorf("acp: dispatch prompt: %w", err)
	}

	for {
		select {
		case <-ctx.Done():
			return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
		case ev, ok := <-events:
			if !ok {
				if ctx.Err() != nil {
					return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
				}
				if err := <-readErr; err != nil {
					return acpsdk.PromptResponse{}, fmt.Errorf("acp: event stream: %w", err)
				}
				return acpsdk.PromptResponse{}, errors.New("acp: event stream closed before the turn finished")
			}
			terminal, resp, err := a.handleEvent(ctx, p.SessionId, ev)
			if err != nil {
				return acpsdk.PromptResponse{}, err
			}
			if terminal {
				return resp, nil
			}
		}
	}
}

// handleEvent 翻译单条事件。terminal=true 表示本轮已定论(resp 即返回值)。
// 其余 kind(output_activity / message_end / retry / plan_update / subagent_* /
// steer_consumed / ask_user_question / tool_permission_request / closed)本版不翻译,
// 直接跳过 —— 它们绝不能卡住轮次。
func (a *agentHandler) handleEvent(ctx context.Context, session acpsdk.SessionId, ev streamEvent) (bool, acpsdk.PromptResponse, error) {
	switch ev.Kind {
	case kindChunk:
		if ev.Delta == "" {
			return false, acpsdk.PromptResponse{}, nil
		}
		if err := a.update(ctx, session, acpsdk.UpdateAgentMessage(acpsdk.TextBlock(ev.Delta))); err != nil {
			return true, acpsdk.PromptResponse{}, err
		}
	case kindThinking:
		if ev.Delta == "" {
			return false, acpsdk.PromptResponse{}, nil
		}
		if err := a.update(ctx, session, acpsdk.UpdateAgentThought(acpsdk.TextBlock(ev.Delta))); err != nil {
			return true, acpsdk.PromptResponse{}, err
		}
	case kindToolUse:
		if ev.ToolCallID != "" {
			if err := a.update(ctx, session, toolCallStart(ev)); err != nil {
				return true, acpsdk.PromptResponse{}, err
			}
		}
	case kindToolResult:
		if ev.ToolCallID != "" {
			if err := a.update(ctx, session, toolCallResult(ev)); err != nil {
				return true, acpsdk.PromptResponse{}, err
			}
		}
	case kindDone:
		return true, acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	case kindAborted:
		return true, acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
	case kindError:
		msg := strings.TrimSpace(ev.Error)
		if msg == "" {
			msg = "turn failed"
		}
		return true, acpsdk.PromptResponse{}, errors.New("acp: " + msg)
	}
	return false, acpsdk.PromptResponse{}, nil
}

func (a *agentHandler) update(ctx context.Context, session acpsdk.SessionId, u acpsdk.SessionUpdate) error {
	if a.conn == nil {
		return errors.New("acp: agent connection is not wired")
	}
	if err := a.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: session, Update: u}); err != nil {
		return fmt.Errorf("acp: send session update: %w", err)
	}
	return nil
}

// toolCallStart 把 tool_use 映射成 session/update(tool_call)。工具种类拿不准,
// 一律用最泛的 ToolKindOther —— 猜成具体种类比不显示更糟。
func toolCallStart(ev streamEvent) acpsdk.SessionUpdate {
	title := strings.TrimSpace(ev.ToolName)
	if title == "" {
		title = "tool"
	}
	opts := []acpsdk.ToolCallStartOpt{
		acpsdk.WithStartKind(acpsdk.ToolKindOther),
		acpsdk.WithStartStatus(acpsdk.ToolCallStatusPending),
	}
	if len(ev.ToolInput) > 0 {
		opts = append(opts, acpsdk.WithStartRawInput(ev.ToolInput))
	}
	return acpsdk.StartToolCall(acpsdk.ToolCallId(ev.ToolCallID), title, opts...)
}

// toolCallResult 把 tool_result 映射成 session/update(tool_call_update)。
func toolCallResult(ev streamEvent) acpsdk.SessionUpdate {
	status := acpsdk.ToolCallStatusCompleted
	if ev.IsError {
		status = acpsdk.ToolCallStatusFailed
	}
	opts := []acpsdk.ToolCallUpdateOpt{acpsdk.WithUpdateStatus(status)}
	if ev.ToolResult != "" {
		opts = append(opts, acpsdk.WithUpdateRawOutput(ev.ToolResult))
	}
	return acpsdk.UpdateToolCall(acpsdk.ToolCallId(ev.ToolCallID), opts...)
}

// promptPayload 拆 prompt content block:text 按顺序拼成任务文本,image 转 data URL。
// audio / resource / resource_link 等本版不支持的块一律返回可读错误 —— 不静默丢弃,
// 也不悄悄降级。
func promptPayload(blocks []acpsdk.ContentBlock) (string, []sendImage, error) {
	var texts []string
	var images []sendImage
	for _, b := range blocks {
		switch {
		case b.Text != nil:
			texts = append(texts, b.Text.Text)
		case b.Image != nil:
			mime := strings.TrimSpace(b.Image.MimeType)
			if mime == "" {
				return "", nil, errors.New("acp: image content block is missing mimeType")
			}
			data := strings.TrimSpace(b.Image.Data)
			if data == "" {
				return "", nil, errors.New("acp: image content block is missing data")
			}
			images = append(images, sendImage{DataURL: "data:" + mime + ";base64," + data})
		case b.Audio != nil:
			return "", nil, errors.New("acp: audio content blocks are not supported")
		case b.ResourceLink != nil:
			return "", nil, errors.New("acp: resource_link content blocks are not supported")
		case b.Resource != nil:
			return "", nil, errors.New("acp: embedded resource content blocks are not supported")
		default:
			return "", nil, errors.New("acp: unsupported content block")
		}
	}
	text := strings.Join(texts, "\n")
	if strings.TrimSpace(text) == "" && len(images) == 0 {
		return "", nil, errors.New("acp: prompt has no text or image content")
	}
	return text, images, nil
}

func (a *agentHandler) sessionOf(id acpsdk.SessionId) (int64, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sessionID, ok := a.sessions[id]
	return sessionID, ok
}

// readStream 在独立 goroutine 里逐行读 SSE,把事件经缓冲通道交给 Prompt。通道满时
// 阻塞读循环(而不是丢事件):本轮事件的完整性比吞吐重要,而调用方会持续消费。
func readStream(ctx context.Context, body io.Reader) (<-chan streamEvent, <-chan error) {
	events := make(chan streamEvent, 64)
	errCh := make(chan error, 1)
	go func() {
		defer close(events)
		errCh <- readSSE(body, func(ev streamEvent) bool {
			select {
			case events <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		})
	}()
	return events, errCh
}

// readSSE 解析 text/event-stream:`data:` 行累积,空行触发一条事件。
func readSSE(r io.Reader, emit func(streamEvent) bool) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var data strings.Builder
	flush := func() bool {
		if data.Len() == 0 {
			return true
		}
		payload := strings.TrimSpace(data.String())
		data.Reset()
		if payload == "" {
			return true
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			return true // 跳过畸形帧,不因此卡住整轮
		}
		return emit(ev)
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if !flush() {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue // SSE 注释/心跳
		}
		if rest, ok := strings.CutPrefix(line, "data:"); ok {
			data.WriteString(strings.TrimPrefix(rest, " "))
			data.WriteByte('\n')
		}
	}
	if !flush() {
		return nil
	}
	return scanner.Err()
}

// ---- 未实现方法的诚实答复 ----

func unsupported(method string) error {
	return fmt.Errorf("acp: %s is not supported by agrctl acp", method)
}

func (a *agentHandler) Authenticate(context.Context, acpsdk.AuthenticateRequest) (acpsdk.AuthenticateResponse, error) {
	return acpsdk.AuthenticateResponse{}, unsupported("authenticate")
}

func (a *agentHandler) Logout(context.Context, acpsdk.LogoutRequest) (acpsdk.LogoutResponse, error) {
	return acpsdk.LogoutResponse{}, unsupported("logout")
}

func (a *agentHandler) ListSessions(context.Context, acpsdk.ListSessionsRequest) (acpsdk.ListSessionsResponse, error) {
	return acpsdk.ListSessionsResponse{}, unsupported("session/list")
}

func (a *agentHandler) ResumeSession(context.Context, acpsdk.ResumeSessionRequest) (acpsdk.ResumeSessionResponse, error) {
	return acpsdk.ResumeSessionResponse{}, unsupported("session/resume")
}

func (a *agentHandler) CloseSession(context.Context, acpsdk.CloseSessionRequest) (acpsdk.CloseSessionResponse, error) {
	return acpsdk.CloseSessionResponse{}, unsupported("session/close")
}

func (a *agentHandler) SetSessionMode(context.Context, acpsdk.SetSessionModeRequest) (acpsdk.SetSessionModeResponse, error) {
	return acpsdk.SetSessionModeResponse{}, unsupported("session/set_mode")
}

func (a *agentHandler) SetSessionConfigOption(context.Context, acpsdk.SetSessionConfigOptionRequest) (acpsdk.SetSessionConfigOptionResponse, error) {
	return acpsdk.SetSessionConfigOptionResponse{}, unsupported("session/set_config_option")
}
