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
	// kindToolPermissionRequest 是目标 agent 等审批时 chat_svc emit 的事件 kind。
	// 收到它必须完成一次 ACP session/request_permission 往返,否则目标 agent
	// 会永远停在 waiting 状态,prompt 永不返回。
	kindToolPermissionRequest = "tool_permission_request"
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

	// ToolPermission 是 tool_permission_request 事件的最小投影。Agentre 不提供候选
	// 选项(选项由本包按 ACP 标准构造),所以这里只要审批句柄 + 工具信息。
	ToolPermission *streamToolPermission `json:"toolPermission,omitempty"`
}

// streamToolPermission 只解 acpcmd 需要的审批字段,刻意不 import chat_svc ——
// 那个包会拖入 DB/gorm 依赖,agrctl 要的是精简与快速启动。
type streamToolPermission struct {
	RequestID string          `json:"requestId"`
	ToolName  string          `json:"toolName"`
	ToolInput json.RawMessage `json:"toolInput,omitempty"`
	// Resolved=true 是审批完成后的状态切换事件(Agentre 同一条 kind 会再发一
	// resolved 帧)。已审批的请求不能再问 / 不能再答:目标端的 waiter 已消失,
	// 二次提交只会报错。
	Resolved bool `json:"resolved,omitempty"`
}

// answerPermissionBody 是 POST /ctl/v1/answer-permission 的请求体(acpcmd 侧投影)。
type answerPermissionBody struct {
	SessionID          int64  `json:"sessionId"`
	RequestID          string `json:"requestId"`
	Allow              bool   `json:"allow"`
	AlwaysAllowSession bool   `json:"alwaysAllowSession,omitempty"`
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
// steer_consumed / ask_user_question / closed)本版不翻译,直接跳过 ——
// 它们绝不能卡住轮次。
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
	case kindToolPermissionRequest:
		if err := a.handleToolPermission(ctx, session, ev.ToolPermission); err != nil {
			return true, acpsdk.PromptResponse{}, err
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

// ---- tool permission (session/request_permission round-trip) ----

// answerPermissionTimeout 是回灌决策的等待上限。即使 prompt ctx 已被取消, deny
// 也必须送出去,否则目标 agent 会永远等审批。
const answerPermissionTimeout = 5 * time.Second

// permissionDecision 是本次审批映射出的目标 agent 决策。
type permissionDecision struct {
	Allow              bool
	AlwaysAllowSession bool
}

// permissionOptions 是发给 ACP client 的标准三选项:一次允许 / 始终允许 / 拒绝。
//
// kind 定义语义,optionId 只是回传句柄 —— 决策映射永远按 kind 走。
// 不给 reject_always:Agentre 的 AnswerToolPermission 只有单次决策语义
// (Allow=false 永远单次),没有「永久拒绝」这一档。
func permissionOptions() []acpsdk.PermissionOption {
	return []acpsdk.PermissionOption{
		{OptionId: "allow-once", Name: "Allow once", Kind: acpsdk.PermissionOptionKindAllowOnce},
		{OptionId: "allow-always", Name: "Allow always", Kind: acpsdk.PermissionOptionKindAllowAlways},
		{OptionId: "reject-once", Name: "Reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
	}
}

// decisionForOptionKind 按 ACP 选项 kind 映射;未知 kind 返回 ok=false。
func decisionForOptionKind(kind acpsdk.PermissionOptionKind) (permissionDecision, bool) {
	switch kind {
	case acpsdk.PermissionOptionKindAllowOnce:
		return permissionDecision{Allow: true}, true
	case acpsdk.PermissionOptionKindAllowAlways:
		return permissionDecision{Allow: true, AlwaysAllowSession: true}, true
	case acpsdk.PermissionOptionKindRejectOnce:
		return permissionDecision{Allow: false}, true
	default:
		return permissionDecision{}, false
	}
}

// resolvePermissionDecision 把 client 的 outcome 解析成决策。选中的 option 先按
// optionId 找回我们发出的那一项,再按它的 kind 定语义 —— 不信任 optionId 的字面。
// canceled / 未知 optionId / 未映射 kind 一律 deny:宁可拒绝,也不能让目标 agent
// 永远等。
func resolvePermissionDecision(options []acpsdk.PermissionOption, outcome acpsdk.RequestPermissionOutcome) permissionDecision {
	deny := permissionDecision{Allow: false}
	if outcome.Selected == nil {
		return deny
	}
	for _, opt := range options {
		if opt.OptionId != outcome.Selected.OptionId {
			continue
		}
		if d, ok := decisionForOptionKind(opt.Kind); ok {
			return d
		}
		return deny
	}
	return deny
}

// handleToolPermission 完成一次 ACP 权限往返:向 client 发 session/request_permission,
// 按选中 option 的 kind 得到决策,再回灌 /ctl/v1/answer-permission。目标 agent 会一直
// 停在等审批,直到这条回答到达。
func (a *agentHandler) handleToolPermission(ctx context.Context, session acpsdk.SessionId, perm *streamToolPermission) error {
	// 没有句柄 / 已审批:无从严回答,跳过即可,不卡住读循环。resolved 帧尤其不能
	// 再回灌 —— 目标端 waiter 已消失,二次提交会直接报错。
	if perm == nil || perm.Resolved || strings.TrimSpace(perm.RequestID) == "" {
		return nil
	}
	sessionID, ok := a.sessionOf(session)
	if !ok {
		return fmt.Errorf("acp: unknown session %q", session)
	}
	decision := a.requestPermission(ctx, session, perm)
	if err := a.answerPermission(ctx, sessionID, perm.RequestID, decision); err != nil {
		// 本轮已被取消:目标轮的收尾由 Cancel 的 /ctl/v1/stop 负责。deny 已尽力
		// 投递,投递失败不该把「取消」变成「错误」—— SDK 会把错误当 JSON-RPC
		// 错误回给 client,而不是 stopReason=canceled。
		if ctx.Err() != nil {
			return nil //nolint:nilerr // 取消路径:deny 回灌失败必须吞掉,让 Prompt 以 canceled 收尾
		}
		return fmt.Errorf("acp: answer tool permission: %w", err)
	}
	return nil
}

// requestPermission 向 ACP client 发起 session/request_permission 并等待决策。
// client 报错/取消/prompt ctx 结束时返回 deny,把「拿不到回答」收敛成一次明确的拒绝。
func (a *agentHandler) requestPermission(ctx context.Context, session acpsdk.SessionId, perm *streamToolPermission) permissionDecision {
	if a.conn == nil {
		return permissionDecision{Allow: false}
	}
	options := permissionOptions()
	title := strings.TrimSpace(perm.ToolName)
	if title == "" {
		title = "tool"
	}
	// ToolCall 用已有信息尽力填:requestId 是唯一句柄,拿它当 toolCallId;
	// title/rawInput 来自事件,SDK 不强制校验这些字段。
	callID := strings.TrimSpace(perm.RequestID)
	var rawInput any
	if len(perm.ToolInput) > 0 {
		rawInput = perm.ToolInput
	}
	resp, err := a.conn.RequestPermission(ctx, acpsdk.RequestPermissionRequest{
		SessionId: session,
		ToolCall: acpsdk.ToolCallUpdate{
			ToolCallId: acpsdk.ToolCallId(callID),
			Title:      &title,
			RawInput:   rawInput,
		},
		Options: options,
	})
	if err != nil {
		return permissionDecision{Allow: false}
	}
	return resolvePermissionDecision(options, resp.Outcome)
}

// answerPermission 把决策回灌目标 agent。用「去取消但带超时」的 ctx:即便本轮刚被
// Cancel(prompt ctx 已 done),这次 deny 也必须发出去,否则目标 agent 卡在等审批。
func (a *agentHandler) answerPermission(ctx context.Context, sessionID int64, requestID string, d permissionDecision) error {
	postCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), answerPermissionTimeout)
	defer cancel()
	return a.endpoint.PostContext(postCtx, "/ctl/v1/answer-permission", answerPermissionBody{
		SessionID:          sessionID,
		RequestID:          requestID,
		Allow:              d.Allow,
		AlwaysAllowSession: d.AlwaysAllowSession,
	}, nil)
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
