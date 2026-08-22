package piagent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cago-frame/agents/provider"
)

const (
	// rpcFrameSafetyLimit is the explicit bound on a single Pi RPC line. Session
	// entries carry base64 image data, so a get_entries response for a session at
	// Agentre's supported image limits (≤ 4 images of ≤ 5 MiB per turn, inflated
	// 4/3 by base64) grows by roughly 27 MiB per image-heavy turn. The bound is
	// kept well beyond three such turns so valid pre/post-turn anchor metadata
	// keeps working, while still capping the memory a single frame can claim.
	// Past the bound the optional post-answer anchor metadata degrades (empty
	// anchor, completed answer preserved) instead of failing the turn.
	rpcFrameSafetyLimit = 128 * 1024 * 1024
	// Startup may include a 128 MiB get_entries response, so use the existing
	// 30-second RPC/probe boundary rather than the optional 2-second stats window.
	rpcStartupTimeout = 30 * time.Second
)

type Client struct {
	binary       string
	cwd          string
	env          map[string]string
	model        string
	thinking     string
	systemPrompt string
	// noSession 使用 Pi 的临时 Session 模式（--no-session），不写入 JSONL。
	noSession bool
	// sessionDir 是 Pi session JSONL 的存储目录（--session-dir）。和 cwd（工具
	// 工作目录）分开，避免把 session 文件写进用户项目里。
	sessionDir string
	// session 非空时透传 --session <path|id>，恢复指定 Pi 原生会话。
	session string
	// extensions 透传给 pi 的 --extension（可多次）。Agentre 用它加载内嵌的
	// MCP 桥扩展，把注入的 HTTP MCP server 翻成 pi 一等工具。
	extensions     []string
	killGrace      time.Duration
	startupTimeout time.Duration
	runner         processRunner

	// rawSink 若非 nil,子进程 stdout JSON-RPC 帧会先投影成不含 prompt、图片、
	// Session 内容或凭证的诊断摘要，再同步回调一次。debug 协议诊断用。
	rawSink func([]byte)
}

func New(opts ...Option) *Client {
	c := &Client{
		binary:         "pi",
		killGrace:      10 * time.Second,
		startupTimeout: rpcStartupTimeout,
		runner:         execProcessRunner{},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

type PreparedStream struct {
	stream         *Stream
	frame          map[string]any
	startupTimeout time.Duration

	mu      sync.Mutex
	started bool
	closed  bool
}

func (c *Client) Stream(ctx context.Context, prompt string, opts ...RunOption) (*Stream, error) {
	prepared, err := c.prepareStream(ctx, prompt, false, opts...)
	if err != nil {
		return nil, err
	}
	// Ordinary turns retain the streaming API's asynchronous startup contract;
	// callers that stage durable state use PreparedStream.Start as the ack gate.
	return prepared.start(ctx, false)
}

// PrepareStream starts/restores the Pi RPC process, completes any requested
// native fork, and requires an exact pre-prompt tree boundary without sending
// the prompt. Start must be called only after the caller has durably recorded
// the turn. Ordinary Stream calls retain the R6 metadata-degradation behavior
// for non-replacement turns that cannot capture a boundary.
func (c *Client) PrepareStream(ctx context.Context, prompt string, opts ...RunOption) (*PreparedStream, error) {
	return c.prepareStream(ctx, prompt, true, opts...)
}

func (c *Client) prepareStream(ctx context.Context, prompt string, requireExactBoundary bool, opts ...RunOption) (*PreparedStream, error) {
	// 参数校验必须在起进程之前:非法的 fork anchor 不该先把一个进程拉起来再拒。
	if err := validateRunOptions(opts...); err != nil {
		return nil, err
	}
	proc, err := c.startRPC(ctx)
	if err != nil {
		return nil, err
	}
	// 一次性用法:进程归这一轮所有,轮末由 Stream.Close 终止。
	return c.prepareStreamOn(ctx, proc, true, prompt, requireExactBoundary, opts...)
}

// prepareStreamOn 在一个**已经起来的** RPC 进程上开一轮。ownsProcess 决定这一轮结束时
// 要不要连进程一起收掉:一次性用法(Client.Stream)归它自己,跨轮复用(Session)归会话。
func (c *Client) prepareStreamOn(
	ctx context.Context,
	proc *rpcProcess,
	ownsProcess bool,
	prompt string,
	requireExactBoundary bool,
	opts ...RunOption,
) (*PreparedStream, error) {
	spec := runSpec{}
	for _, o := range opts {
		o(&spec)
	}
	// Session resume is wired at the Client level (WithSession → --session); the
	// per-turn spec carries multimodal images透传到 prompt 帧。
	if err := validateRunOptions(opts...); err != nil {
		return nil, err
	}
	startupCtx, cancelStartup := c.startupContext(ctx)
	defer cancelStartup()
	state, err := readSessionState(startupCtx, proc, c.session)
	if err != nil {
		_ = proc.terminate(context.Background(), c.killGrace)
		return nil, err
	}
	sessionID := state.SessionID
	if spec.forkAnchor != "" {
		if err := forkSession(startupCtx, proc, spec.forkAnchor); err != nil {
			_ = proc.terminate(context.Background(), c.killGrace)
			return nil, err
		}
		forkedState, err := readSessionState(startupCtx, proc, "")
		if err != nil {
			_ = proc.terminate(context.Background(), c.killGrace)
			return nil, err
		}
		if forkedState.SessionID == sessionID {
			_ = proc.terminate(context.Background(), c.killGrace)
			return nil, fmt.Errorf("piagent: fork did not change session id %q", sessionID)
		}
		sessionID = forkedState.SessionID
		if forkedState.Model != nil {
			state.Model = forkedState.Model
		}
	}
	stream := newStream(proc, c.killGrace)
	stream.ownsProcess = ownsProcess
	stream.setSessionID(sessionID)
	if state.Model != nil {
		stream.setContextWindow(state.Model.ContextWindow)
	}
	if spec.captureUserAnchor {
		// The anchor boundary is read straight off the process scanner, so it has
		// to settle before the stream owns the reader via the optional stats probe.
		entries, err := readSessionEntries(startupCtx, proc, "session-entries-before")
		if err != nil {
			_ = stream.Close(context.Background())
			return nil, err
		}
		leafID, ok := validSessionEntriesLeaf(entries)
		if !ok {
			if requireExactBoundary {
				_ = stream.Close(context.Background())
				return nil, errors.New("piagent: invalid pre-prompt tree boundary")
			}
		} else {
			stream.setUserAnchorBoundary(leafID)
		}
	}
	// Ask Pi for its authoritative model window before the first prompt. The
	// response is optional and intentionally not awaited: older/degraded RPC
	// implementations must not delay or block the actual turn.
	stream.markInitialSessionStatsPending()
	if err := stream.send(startupCtx, map[string]any{
		"id": initialSessionStatsRequestID, "type": "get_session_stats",
	}); err != nil {
		_ = stream.Close(context.Background())
		return nil, err
	}
	frame := map[string]any{"type": "prompt", "message": prompt}
	if imgs := imagesToWire(spec.images); len(imgs) > 0 {
		frame["images"] = imgs
	}
	return &PreparedStream{stream: stream, frame: frame, startupTimeout: c.startupTimeout}, nil
}

// validateRunOptions 校验一轮的选项。单独拆出来是因为它必须能在起进程之前跑一遍:
// 非法参数不该先把一个 RPC 进程拉起来再拒。
func validateRunOptions(opts ...RunOption) error {
	spec := runSpec{}
	for _, o := range opts {
		o(&spec)
	}
	if spec.forkAnchor != "" && strings.TrimSpace(spec.forkAnchor) != spec.forkAnchor {
		return errors.New("piagent: invalid fork anchor")
	}
	return nil
}

func (p *PreparedStream) Start(ctx context.Context) (*Stream, error) {
	return p.start(ctx, true)
}

func (p *PreparedStream) start(ctx context.Context, waitForAcknowledgement bool) (*Stream, error) {
	if p == nil || p.stream == nil {
		return nil, errStreamClosed
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, errStreamClosed
	}
	if p.started {
		p.mu.Unlock()
		return nil, errors.New("piagent: prepared stream already started")
	}
	p.started = true
	p.mu.Unlock()

	startupCtx := ctx
	cancelStartup := func() {}
	if waitForAcknowledgement {
		startupCtx, cancelStartup = startupContextWithTimeout(ctx, p.startupTimeout)
	}
	defer cancelStartup()
	if err := p.stream.send(startupCtx, p.frame); err != nil {
		_ = p.Close(context.Background())
		return nil, err
	}
	if !waitForAcknowledgement {
		go p.stream.drain(ctx, nil)
		return p.stream, nil
	}
	pending, err := p.stream.awaitPromptAcknowledgement(startupCtx)
	if err != nil {
		_ = p.Close(context.Background())
		return nil, err
	}
	go p.stream.drain(ctx, pending)
	return p.stream, nil
}

func (p *PreparedStream) SessionID() string {
	if p == nil || p.stream == nil {
		return ""
	}
	return p.stream.SessionID()
}

func (p *PreparedStream) Close(ctx context.Context) error {
	if p == nil || p.stream == nil {
		return nil
	}
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	return p.stream.Close(ctx)
}

func (c *Client) Text(ctx context.Context, prompt string, opts ...RunOption) (string, error) {
	stream, err := c.Stream(ctx, prompt, opts...)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	var stopErr error
	for stream.Next() {
		ev := stream.Event()
		switch ev.Kind {
		case EventTextDelta:
			b.WriteString(ev.Text)
		case EventError:
			if ev.Err != nil {
				stopErr = ev.Err
			}
		}
	}
	if err := stream.Close(ctx); err != nil && stopErr == nil {
		stopErr = err
	}
	if stopErr != nil {
		return "", stopErr
	}
	return b.String(), nil
}

func (c *Client) Compact(ctx context.Context, _ string) (*Stream, error) {
	proc, err := c.startRPC(ctx)
	if err != nil {
		return nil, err
	}
	startupCtx, cancelStartup := c.startupContext(ctx)
	defer cancelStartup()
	state, err := readSessionState(startupCtx, proc, c.session)
	if err != nil {
		_ = proc.terminate(context.Background(), c.killGrace)
		return nil, err
	}
	stream := newStream(proc, c.killGrace)
	stream.setSessionID(state.SessionID)
	if err := stream.send(ctx, map[string]any{"type": "compact"}); err != nil {
		_ = stream.Close(context.Background())
		return nil, err
	}
	go stream.drain(ctx, nil)
	return stream, nil
}

func (c *Client) Close(_ context.Context) error { return nil }

func readSessionState(ctx context.Context, proc *rpcProcess, expected string) (sessionStateWire, error) {
	const requestID = "session-state"
	response, err := callRPC(ctx, proc, map[string]any{"id": requestID, "type": "get_state"}, "get_state", requestID)
	if err != nil {
		return sessionStateWire{}, err
	}
	var state sessionStateWire
	if err := json.Unmarshal(response.Data, &state); err != nil {
		return sessionStateWire{}, fmt.Errorf("piagent decode get_state data: %w", err)
	}
	state.SessionID = strings.TrimSpace(state.SessionID)
	if state.SessionID == "" {
		return sessionStateWire{}, errors.New("piagent: get_state returned empty session id")
	}
	expected = strings.TrimSpace(expected)
	if expected != "" && !looksLikeSessionPath(expected) && state.SessionID != expected {
		return sessionStateWire{}, fmt.Errorf("piagent: get_state returned unexpected session id %q, want %q", state.SessionID, expected)
	}
	return state, nil
}

func forkSession(ctx context.Context, proc *rpcProcess, entryID string) error {
	const requestID = "session-fork"
	response, err := callRPC(
		ctx,
		proc,
		map[string]any{"id": requestID, "type": "fork", "entryId": entryID},
		"fork",
		requestID,
	)
	if err != nil {
		return err
	}
	var result forkResultWire
	if err := json.Unmarshal(response.Data, &result); err != nil {
		return fmt.Errorf("piagent decode fork data: %w", err)
	}
	if result.Canceled == nil {
		return errors.New("piagent: fork response omitted cancellation state")
	}
	if *result.Canceled {
		return errors.New("piagent: fork was canceled")
	}
	return nil
}

func readSessionEntries(ctx context.Context, proc *rpcProcess, requestID string) (sessionEntriesWire, error) {
	response, err := callRPC(
		ctx,
		proc,
		map[string]any{"id": requestID, "type": "get_entries"},
		"get_entries",
		requestID,
	)
	if err != nil {
		return sessionEntriesWire{}, err
	}
	var entries sessionEntriesWire
	if err := json.Unmarshal(response.Data, &entries); err != nil {
		return sessionEntriesWire{}, fmt.Errorf("piagent decode get_entries data: %w", err)
	}
	return entries, nil
}

func callRPC(
	ctx context.Context,
	proc *rpcProcess,
	request map[string]any,
	command string,
	requestID string,
) (rpcResponse, error) {
	if err := proc.writeJSON(request, ctx); err != nil {
		return rpcResponse{}, err
	}
	for scanRPCLine(ctx, proc.lines) {
		line := proc.lines.Bytes()
		select {
		case <-ctx.Done():
			return rpcResponse{}, ctx.Err()
		default:
		}
		var envelope struct {
			Type   string `json:"type"`
			Method string `json:"method"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			emitRawFrame(proc, line)
			continue
		}
		if command == "fork" && envelope.Type == "extension_ui_request" && extensionUIRequiresResponse(envelope.Method) {
			// A session_before_fork dialog may contain selected prompt text. Return
			// only the method name and keep the full request out of diagnostics.
			return rpcResponse{}, fmt.Errorf(
				"piagent rpc fork requires unsupported extension UI response for method %q",
				envelope.Method,
			)
		}
		emitRawFrame(proc, line)
		var response rpcResponse
		if err := json.Unmarshal(line, &response); err != nil {
			continue
		}
		if response.Type != "response" || response.Command != command || response.ID != requestID {
			continue
		}
		if !response.Success {
			return rpcResponse{}, failureResponseError(response)
		}
		return response, nil
	}
	return rpcResponse{}, awaitProcessExitOrScanError(ctx, proc)
}

func scanRPCLine(ctx context.Context, scanner rpcLineScanner) bool {
	if scanner == nil {
		return false
	}
	if contextual, ok := scanner.(interface {
		ScanContext(context.Context) bool
	}); ok {
		return contextual.ScanContext(ctx)
	}
	return scanner.Scan()
}

func validSessionEntriesLeaf(entries sessionEntriesWire) (string, bool) {
	leafID := entries.LeafID
	if len(entries.Entries) == 0 {
		return leafID, leafID == ""
	}
	if leafID == "" || strings.TrimSpace(leafID) != leafID {
		return "", false
	}
	seen := make(map[string]struct{}, len(entries.Entries))
	for _, entry := range entries.Entries {
		id := entry.ID
		if id == "" || strings.TrimSpace(id) != id {
			return "", false
		}
		if _, duplicate := seen[id]; duplicate {
			return "", false
		}
		seen[id] = struct{}{}
	}
	_, ok := seen[leafID]
	return leafID, ok
}

func isExtensionUIRequestFrame(frame []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(frame, &probe) == nil && probe.Type == "extension_ui_request"
}

func extensionUIRequiresResponse(method string) bool {
	switch strings.TrimSpace(method) {
	case "select", "confirm", "input", "editor":
		return true
	default:
		return false
	}
}

func emitRawFrame(proc *rpcProcess, line []byte) {
	if proc == nil || proc.rawSink == nil {
		return
	}
	if diagnostic := sanitizeDiagnosticFrame(line); len(diagnostic) > 0 {
		proc.rawSink(diagnostic)
	}
}

func sanitizeDiagnosticFrame(line []byte) []byte {
	// get_entries can legitimately carry tens of MiB of base64 image/session data.
	// Recognize and replace it before JSON decoding so diagnostics do not duplicate
	// that payload in memory merely to report the command name.
	if bytes.Contains(line, []byte(`"type":"response"`)) &&
		bytes.Contains(line, []byte(`"command":"get_entries"`)) {
		return []byte(`{"command":"get_entries","payload":"redacted","type":"response"}`)
	}
	// extension_ui_request carries interaction copy the user typed or was shown.
	// It is excluded from diagnostics entirely, not projected.
	if isExtensionUIRequestFrame(line) {
		return nil
	}

	var envelope struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Command string `json:"command"`
		Success bool   `json:"success"`
		Data    struct {
			SessionID    string            `json:"sessionId"`
			Canceled     *bool             `json:"cancelled"` //nolint:misspell // Pi RPC field uses British spelling.
			ContextUsage *contextUsageWire `json:"contextUsage"`
		} `json:"data"`
		Method   string          `json:"method"`
		Message  json.RawMessage `json:"message"`
		Messages []struct {
			Role       string `json:"role"`
			StopReason string `json:"stopReason"`
		} `json:"messages"`
		AssistantMessageEvent struct {
			Type string `json:"type"`
		} `json:"assistantMessageEvent"`
		ToolCallID string `json:"toolCallId"`
		ToolName   string `json:"toolName"`
		IsError    bool   `json:"isError"`
		WillRetry  bool   `json:"willRetry"`
	}
	if err := json.Unmarshal(line, &envelope); err != nil || strings.TrimSpace(envelope.Type) == "" {
		return nil
	}

	out := map[string]any{"type": envelope.Type}
	if envelope.ID != "" {
		out["id"] = envelope.ID
	}
	switch envelope.Type {
	case "response":
		out["command"] = envelope.Command
		out["success"] = envelope.Success
		switch envelope.Command {
		case "get_state":
			if sessionID := strings.TrimSpace(envelope.Data.SessionID); sessionID != "" {
				out["sessionId"] = sessionID
			}
		case "fork":
			if envelope.Data.Canceled != nil {
				out["cancelled"] = *envelope.Data.Canceled //nolint:misspell // Pi RPC field uses British spelling.
			}
		case "get_session_stats":
			if envelope.Data.ContextUsage != nil && envelope.Data.ContextUsage.ContextWindow > 0 {
				out["contextWindow"] = envelope.Data.ContextUsage.ContextWindow
			}
		}
	case "message_start", "message_end":
		var message struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(envelope.Message, &message) == nil && message.Role != "" {
			out["role"] = message.Role
		}
	case "message_update":
		if envelope.AssistantMessageEvent.Type != "" {
			out["assistantEventType"] = envelope.AssistantMessageEvent.Type
		}
	case "tool_execution_start", "tool_execution_end":
		if envelope.ToolCallID != "" {
			out["toolCallId"] = envelope.ToolCallID
		}
		if envelope.ToolName != "" {
			out["toolName"] = envelope.ToolName
		}
		if envelope.Type == "tool_execution_end" {
			out["isError"] = envelope.IsError
		}
	case "agent_end":
		out["willRetry"] = envelope.WillRetry
		for i := len(envelope.Messages) - 1; i >= 0; i-- {
			message := envelope.Messages[i]
			if message.Role == "assistant" && message.StopReason != "" {
				out["stopReason"] = message.StopReason
				break
			}
		}
	}
	diagnostic, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return diagnostic
}

func looksLikeSessionPath(value string) bool {
	return strings.ContainsAny(value, `/\\`) || strings.HasSuffix(value, ".jsonl")
}

func (c *Client) startupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return startupContextWithTimeout(ctx, c.startupTimeout)
}

func startupContextWithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}

func (c *Client) startRPC(ctx context.Context) (*rpcProcess, error) {
	h, err := c.runner.Start(ctx, procOptions{
		Binary: c.binary,
		Args:   buildRPCArgs(c),
		Cwd:    c.cwd,
		Env:    buildEnv(c.env),
	})
	if err != nil {
		return nil, processBoundaryError("start", "request", err)
	}
	lines := newAsyncRPCLineScanner(h.Stdout())
	p := &rpcProcess{
		handle:     h,
		stdin:      h.Stdin(),
		writeCtx:   ctx,
		lines:      lines,
		linesDone:  lines.Done(),
		rawSink:    c.rawSink,
		stderr:     &lockedBuffer{},
		stderrDone: make(chan struct{}),
		done:       make(chan struct{}),
	}
	go func() {
		defer close(p.stderrDone)
		_, _ = io.Copy(p.stderr, h.Stderr())
	}()
	go p.awaitExit()
	return p, nil
}

type rpcLineScanner interface {
	Scan() bool
	Bytes() []byte
	Text() string
	Err() error
}

type asyncRPCLineScanner struct {
	lines       chan []byte
	stop        chan struct{}
	done        chan struct{}
	closeReader func()

	stopOnce sync.Once
	current  []byte
	ctxErr   error
	scanErr  error
}

func newAsyncRPCLineScanner(reader io.Reader) *asyncRPCLineScanner {
	s := &asyncRPCLineScanner{
		lines: make(chan []byte),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	switch closer := reader.(type) {
	case io.Closer:
		s.closeReader = func() { _ = closer.Close() }
	case interface{ Close() }:
		s.closeReader = closer.Close
	}
	go s.scan(reader)
	return s
}

func (s *asyncRPCLineScanner) scan(reader io.Reader) {
	defer close(s.done)
	defer close(s.lines)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), rpcFrameSafetyLimit)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		select {
		case s.lines <- line:
		case <-s.stop:
			return
		}
	}
	s.scanErr = scanner.Err()
}

func (s *asyncRPCLineScanner) Scan() bool {
	return s.ScanContext(context.Background())
}

func (s *asyncRPCLineScanner) ScanContext(ctx context.Context) bool {
	select {
	case line, ok := <-s.lines:
		if !ok {
			s.current = nil
			return false
		}
		s.current = line
		return true
	case <-ctx.Done():
		s.ctxErr = ctx.Err()
		s.Stop()
		return false
	}
}

func (s *asyncRPCLineScanner) Bytes() []byte { return s.current }
func (s *asyncRPCLineScanner) Text() string  { return string(s.current) }
func (s *asyncRPCLineScanner) Err() error {
	if s.ctxErr != nil {
		return s.ctxErr
	}
	return s.scanErr
}
func (s *asyncRPCLineScanner) Stop() {
	s.stopOnce.Do(func() {
		close(s.stop)
		if s.closeReader != nil {
			s.closeReader()
		}
	})
}
func (s *asyncRPCLineScanner) Done() <-chan struct{} { return s.done }

type rpcProcess struct {
	handle     processHandle
	stdin      io.Writer
	writeCtx   context.Context
	lines      rpcLineScanner
	linesDone  <-chan struct{}
	rawSink    func([]byte) // 非 nil时回调不含敏感 payload 的 stdout 诊断摘要
	stderr     *lockedBuffer
	stderrDone chan struct{}
	done       chan struct{} // closed when waitErr is available to every observer
	waitErr    error         // immutable after done is closed

	writerOnce     sync.Once
	writerStopOnce sync.Once
	stdinCloseOnce sync.Once
	writeGate      chan struct{}
	writerStop     chan struct{}
}

func (p *rpcProcess) awaitExit() {
	// StdoutPipe/StderrPipe 的契约要求 Wait 不能与管道读取并发：Wait 会在
	// 进程退出后关闭管道。先让 stderr reader 读到 EOF，避免短命进程的最后一行
	// 被 "file already closed" 抢走，进而丢失可分类的退出原因。
	<-p.stderrDone
	p.waitErr = p.handle.Wait()
	if p.linesDone != nil {
		<-p.linesDone
	}
	close(p.done)
}

func (p *rpcProcess) waitResult() error {
	<-p.done
	return p.waitErr
}

func (p *rpcProcess) writeJSON(v any, contexts ...context.Context) error {
	if p == nil {
		return errStreamClosed
	}
	command := requestCommand(v)
	buf, err := json.Marshal(v)
	if err != nil {
		return processBoundaryError("encode", command, err)
	}
	buf = append(buf, '\n')
	ctx := context.Background()
	if p.writeCtx != nil {
		ctx = p.writeCtx
	}
	if len(contexts) > 0 && contexts[0] != nil {
		ctx = contexts[0]
	}
	p.ensureWriter()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.writerStop:
		return processBoundaryError("write", command, io.ErrClosedPipe)
	case <-p.writeGate:
	}
	defer func() { p.writeGate <- struct{}{} }()
	select {
	case <-p.writerStop:
		return processBoundaryError("write", command, io.ErrClosedPipe)
	default:
	}

	writeDone := make(chan error, 1)
	go func() {
		if p.stdin == nil {
			writeDone <- io.ErrClosedPipe
			return
		}
		_, err := p.stdin.Write(buf)
		writeDone <- err
	}()

	select {
	case err := <-writeDone:
		if err != nil {
			return p.classifyDeadWrite(command, err)
		}
		return nil
	case <-ctx.Done():
		p.stopWriter()
		<-writeDone
		return ctx.Err()
	case <-p.writerStop:
		// A deliberate local shutdown (Close / terminate) already explains the
		// failed write; classifying it against process exit would only stall.
		err := <-writeDone
		if err != nil {
			return processBoundaryError("write", command, err)
		}
		return processBoundaryError("write", command, io.ErrClosedPipe)
	}
}

// deadWriteProbeTimeout 限制 classifyDeadWrite 等待一个已关管道的进程退出多久。
// 正常情况下 broken pipe 意味着对端正在退出,p.done 在毫秒级关闭;这个超时只是
// 防御对端关了 stdin 却不退出的病态情形,避免写路径无限挂起。
const deadWriteProbeTimeout = 5 * time.Second

// classifyDeadWrite 在一次失败的 stdin 写之后,等 RPC 进程退出并按 stderr 分类
// 退出原因,把 "broken pipe" 翻译成调用方能据以行动的错误(如 ErrSessionNotFound)。
// 分类不出东西(进程没在窗口内退出 / 退出码 0)时回退规范化后的写边界错误。
func (p *rpcProcess) classifyDeadWrite(command string, writeErr error) error {
	select {
	case <-p.done:
		if p.waitErr != nil {
			return wrapExitError(p.waitErr, p.stderr.String())
		}
	case <-time.After(deadWriteProbeTimeout):
	}
	return processBoundaryError("write", command, writeErr)
}

func (p *rpcProcess) ensureWriter() {
	p.writerOnce.Do(func() {
		p.writeGate = make(chan struct{}, 1)
		p.writeGate <- struct{}{}
		p.writerStop = make(chan struct{})
	})
}

func (p *rpcProcess) stopWriter() {
	if p == nil {
		return
	}
	p.ensureWriter()
	p.writerStopOnce.Do(func() { close(p.writerStop) })
	p.stdinCloseOnce.Do(func() {
		switch closer := p.stdin.(type) {
		case io.Closer:
			_ = closer.Close()
		case interface{ Close() }:
			closer.Close()
		}
	})
}

func (p *rpcProcess) waitForWrites() {
	p.ensureWriter()
	<-p.writeGate
	p.writeGate <- struct{}{}
}

func (p *rpcProcess) terminate(ctx context.Context, grace time.Duration) error {
	if p == nil {
		return nil
	}
	stopLines := func() {
		if stopper, ok := p.lines.(interface{ Stop() }); ok {
			stopper.Stop()
		}
	}
	if p.handle == nil {
		stopLines()
		p.stopWriter()
		p.waitForWrites()
		return nil
	}
	if grace == 0 {
		// Cancellation settlement has already exhausted its grace window. Kill the
		// tree while both process pipes still keep the group leader addressable;
		// tearing either pipe down first can strand a descendant in the old group.
		_ = p.handle.Signal(interruptSignal())
		_ = p.handle.Kill()
		stopLines()
		p.stopWriter()
		p.waitForWrites()
		select {
		case <-p.done:
			return wrapExitError(p.waitErr, p.stderr.String())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	stopLines()
	_ = p.handle.Signal(interruptSignal())
	p.stopWriter()
	p.waitForWrites()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-p.done:
		// The group leader can exit cleanly after stdin closes while a tool child
		// remains in the same process group. Reap the exact tree even on this branch.
		_ = p.handle.Kill()
		return wrapTerminateExitError(p.waitErr, p.stderr.String())
	case <-timer.C:
		_ = p.handle.Kill()
		return wrapExitError(p.waitResult(), p.stderr.String())
	case <-ctx.Done():
		_ = p.handle.Kill()
		return ctx.Err()
	}
}

func wrapExitError(err error, stderr string) error {
	if err == nil {
		return nil
	}
	if sessionID, missing := missingSessionIdentity(stderr); missing {
		if sessionID == "" {
			return ErrSessionNotFound
		}
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	return &ExitError{Err: normalizeProcessExitCause(err)}
}

func wrapTerminateExitError(err error, stderr string) error {
	if err == nil || isInterruptExit(err) {
		return nil
	}
	return wrapExitError(err, stderr)
}

func isInterruptExit(err error) bool {
	if err == nil {
		return false
	}
	return strings.TrimSpace(err.Error()) == "signal: interrupt"
}

func failureResponseError(r rpcResponse) error {
	command := safeRPCCommand(r.Command)
	// Pi does not expose stable machine-readable failure codes. Keep only the
	// command classification; Error and Data are untrusted payloads that can echo
	// prompts, session entries, images, credentials, or provider response bodies.
	switch command {
	case "fork":
		return errors.New("piagent rpc fork failed: Invalid entry ID for forking")
	case "get_commands":
		return errors.New("piagent rpc get_commands failed: unavailable")
	default:
		return fmt.Errorf("piagent rpc %s failed", command)
	}
}

func processDeadOrScanError(p *rpcProcess) error {
	scanErr := p.lines.Err()
	if isFrameSafetyLimitError(scanErr) {
		return processBoundaryError("read", "request", scanErr)
	}
	select {
	case <-p.done:
		if p.waitErr == nil {
			if scanErr != nil {
				return processBoundaryError("read", "request", scanErr)
			}
			return ErrProcessDead
		}
		return wrapExitError(p.waitErr, p.stderr.String())
	case <-time.After(100 * time.Millisecond):
		if scanErr != nil {
			return processBoundaryError("read", "request", scanErr)
		}
		return ErrProcessDead
	}
}

func awaitProcessExitOrScanError(ctx context.Context, p *rpcProcess) error {
	scanErr := p.lines.Err()
	if isFrameSafetyLimitError(scanErr) {
		return processBoundaryError("read", "request", scanErr)
	}
	// During the startup handshake, stdout EOF or a close-related scanner error
	// can arrive before Wait and stderr collection finish. Their result is
	// authoritative for classifying a missing resumed session, so wait unless the
	// startup context is canceled.
	select {
	case <-p.done:
		if p.waitErr == nil {
			if scanErr != nil {
				return processBoundaryError("read", "request", scanErr)
			}
			return ErrProcessDead
		}
		return wrapExitError(p.waitErr, p.stderr.String())
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isPromptResponse(r rpcResponse) bool {
	return r.Type == "response" && r.Command == "prompt"
}

func promptResponseError(r rpcResponse) error {
	if !r.Success {
		return failureResponseError(r)
	}
	var result struct {
		Canceled *bool `json:"cancelled"` //nolint:misspell // Pi RPC field uses British spelling.
	}
	if len(r.Data) > 0 && json.Unmarshal(r.Data, &result) == nil && result.Canceled != nil && *result.Canceled {
		return errors.New("piagent: prompt was canceled")
	}
	return nil
}

func isTerminalEvent(ev rpcEvent) bool {
	return ev.Type == "agent_settled"
}

func parseAssistantMessage(raw json.RawMessage) (*assistantMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var msg assistantMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	if msg.Role != "assistant" {
		return nil, nil
	}
	return &msg, nil
}

func usageFromMessage(msg *assistantMessage) provider.Usage {
	if msg == nil || msg.Usage == nil {
		return provider.Usage{}
	}
	return provider.Usage{
		PromptTokens:        msg.Usage.Input,
		CompletionTokens:    msg.Usage.Output,
		CachedTokens:        msg.Usage.CacheRead,
		CacheCreationTokens: msg.Usage.CacheWrite,
	}
}

func lastAssistantFromAgentEnd(raw json.RawMessage) *assistantMessage {
	var msgs []json.RawMessage
	if err := json.Unmarshal(raw, &msgs); err != nil {
		return nil
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		msg, err := parseAssistantMessage(msgs[i])
		if err == nil && msg != nil {
			return msg
		}
	}
	return nil
}

// userEchoText 从 message_start/message_end 的 message 里取出 user 角色的文本。
// 非 user 角色返回 ok=false。content 可能是字符串或 content block 数组，统一交给
// contentText 抽取。
func userEchoText(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var m struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", false
	}
	if m.Role != "user" {
		return "", false
	}
	return contentText(m.Content), true
}

func contentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var b strings.Builder
	for _, blk := range blocks {
		if blk.Type == "text" {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

var errStreamClosed = errors.New("piagent: stream closed")
