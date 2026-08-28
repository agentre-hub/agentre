package piagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Stream struct {
	proc      *rpcProcess
	killGrace time.Duration
	events    chan Event
	// ownsProcess 决定这一轮收尾时要不要连 RPC 进程一起终止。一次性用法(Client.Stream)
	// 归这一轮所有;跨轮复用(Session)时进程归会话,轮末只收这一轮。
	ownsProcess bool

	mu                 sync.RWMutex
	sessionID          string
	userAnchorBoundary string
	userAnchor         string
	captureUserAnchor  bool
	model              string
	contextWindow      int
	usageObserved      bool
	// usageRecorded 是「这条 assistant 消息的用量已经 emit 过」的账本,键见
	// assistantMessageKey。agent_end 重发本轮消息时靠它不重复计数。
	usageRecorded       map[string]bool
	initialStatsPending bool
	err                 error
	diagnostics         StreamDiagnostics
	cur                 Event

	closeOnce sync.Once
	abortMu   sync.Mutex
	abortSent bool

	pendingAgentEndError       *agentEndError
	pendingAssistantDeltaError error
}

type agentEndError struct {
	err     error
	event   rpcEvent
	rawLine string
}

type agentEndOutcomeKind uint8

const (
	agentEndOutcomeUnknown agentEndOutcomeKind = iota
	agentEndOutcomeSuccess
	agentEndOutcomeFailure
)

type agentEndOutcome struct {
	kind agentEndOutcomeKind
	err  error
}

// newStream 默认让这一轮拥有进程 —— 一次性用法(Client.Stream / Text / Compact)的
// 既有语义。跨轮复用由 prepareStreamOn 显式改成 false。
func newStream(proc *rpcProcess, killGrace time.Duration) *Stream {
	return &Stream{proc: proc, killGrace: killGrace, ownsProcess: true, events: make(chan Event, 64)}
}

func (s *Stream) send(ctx context.Context, cmd map[string]any) error {
	if s == nil || s.proc == nil {
		return errStreamClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	command, _ := cmd["type"].(string)
	if command != "abort" {
		return s.proc.writeJSON(cmd, ctx)
	}

	// Stop may reach the runtime interruptor just before or just after the turn
	// context is canceled. Claim the one abort frame without holding this mutex
	// across a potentially blocked pipe write; process Close or ctx cancellation
	// then remains able to interrupt the writer.
	s.abortMu.Lock()
	if s.abortSent {
		s.abortMu.Unlock()
		return nil
	}
	s.abortSent = true
	s.abortMu.Unlock()
	return s.proc.writeJSON(cmd, ctx)
}

func (s *Stream) Next() bool {
	ev, ok := <-s.events
	if !ok {
		return false
	}
	s.cur = ev
	return true
}

func (s *Stream) Event() Event { return s.cur }

func (s *Stream) SessionID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

func (s *Stream) setSessionID(sessionID string) {
	s.mu.Lock()
	s.sessionID = strings.TrimSpace(sessionID)
	s.mu.Unlock()
}

func (s *Stream) UserAnchor() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userAnchor
}

func (s *Stream) setUserAnchorBoundary(leafID string) {
	s.mu.Lock()
	s.captureUserAnchor = true
	s.userAnchorBoundary = strings.TrimSpace(leafID)
	s.mu.Unlock()
}

func (s *Stream) setUserAnchor(anchor string) {
	s.mu.Lock()
	s.userAnchor = strings.TrimSpace(anchor)
	s.mu.Unlock()
}

func (s *Stream) setContextWindow(contextWindow int) (changed, usageObserved bool) {
	if contextWindow <= 0 {
		return false, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed = s.contextWindow != contextWindow
	if changed {
		s.contextWindow = contextWindow
	}
	return changed, s.usageObserved
}

func (s *Stream) markInitialSessionStatsPending() {
	s.mu.Lock()
	s.initialStatsPending = true
	s.mu.Unlock()
}

func (s *Stream) consumeInitialSessionStatsPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialStatsPending {
		return false
	}
	s.initialStatsPending = false
	return true
}

func (s *Stream) Err() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.err
}

func (s *Stream) Diagnostics() StreamDiagnostics {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.diagnostics
}

func (s *Stream) Close(ctx context.Context) error {
	var err error
	s.closeOnce.Do(func() {
		if !s.ownsProcess {
			return
		}
		err = s.proc.terminate(ctx, s.killGrace)
	})
	if err != nil {
		return err
	}
	return s.Err()
}

func (s *Stream) awaitPromptAcknowledgement(ctx context.Context) ([][]byte, error) {
	pending := make([][]byte, 0)
	for scanRPCLine(ctx, s.proc.lines) {
		line := append([]byte(nil), s.proc.lines.Bytes()...)
		emitRawFrame(s.proc, line)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		var response rpcResponse
		if err := json.Unmarshal(line, &response); err != nil || !isPromptResponse(response) {
			pending = append(pending, line)
			continue
		}
		if err := promptResponseError(response); err != nil {
			return nil, err
		}
		// Prompt acceptance closes the optional initial-stats slot; see drainLine.
		s.consumeInitialSessionStatsPending()
		return pending, nil
	}
	return nil, awaitProcessExitOrScanError(ctx, s.proc)
}

const abortSettlementGrace = 500 * time.Millisecond

func (s *Stream) drain(ctx context.Context, pendingFrames ...[][]byte) {
	defer close(s.events)
	drainCtx, stopSettlementWindow := boundedSettlementContext(ctx, abortSettlementGrace, func() {
		_ = s.send(context.Background(), map[string]any{"type": "abort"})
	})
	defer stopSettlementWindow()

	var pending [][]byte
	if len(pendingFrames) > 0 {
		pending = pendingFrames[0]
	}
	for _, line := range pending {
		if !s.drainLine(drainCtx, line, false) {
			s.terminateCanceledProcess(ctx)
			return
		}
	}
	for scanRPCLine(drainCtx, s.proc.lines) {
		line := append([]byte(nil), s.proc.lines.Bytes()...)
		if !s.drainLine(drainCtx, line, true) {
			s.terminateCanceledProcess(ctx)
			return
		}
	}
	if err := ctx.Err(); err != nil {
		s.terminateCanceledProcess(ctx)
		s.setErr(err)
		s.emit(Event{Kind: EventError, Err: err})
		return
	}
	if err := processDeadOrScanError(s.proc); err != nil {
		s.setErr(err)
		s.emit(Event{Kind: EventError, Err: err})
	}
}

func (s *Stream) terminateCanceledProcess(ctx context.Context) {
	if s == nil || s.proc == nil || ctx.Err() == nil || !s.ownsProcess {
		return
	}
	// Settlement has either completed or exhausted its bound. Kill the whole
	// process group now rather than applying the ordinary graceful Close delay,
	// so descendant tool work cannot continue beyond the Stop window.
	_ = s.proc.terminate(context.Background(), 0)
}

func boundedSettlementContext(ctx context.Context, grace time.Duration, onCancel func()) (context.Context, func()) {
	settlementCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if onCancel != nil {
				go onCancel()
			}
			timer := time.NewTimer(grace)
			defer timer.Stop()
			select {
			case <-timer.C:
				cancel()
			case <-done:
			}
		case <-done:
		}
	}()
	var once sync.Once
	return settlementCtx, func() {
		once.Do(func() {
			close(done)
			cancel()
		})
	}
}

func (s *Stream) drainLine(ctx context.Context, rawLine []byte, emitDiagnostic bool) bool {
	if emitDiagnostic {
		emitRawFrame(s.proc, rawLine)
	}
	select {
	case <-ctx.Done():
		s.setErr(ctx.Err())
		s.emit(Event{Kind: EventError, Err: ctx.Err()})
		return false
	default:
	}
	line := strings.TrimSpace(string(rawLine))
	if line == "" {
		return true
	}
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rawLine, &probe); err != nil {
		return true
	}
	if probe.Type == "response" {
		var resp rpcResponse
		if err := json.Unmarshal(rawLine, &resp); err != nil {
			return true
		}
		if resp.Command == "get_session_stats" {
			if resp.ID != "" && resp.ID != initialSessionStatsRequestID {
				return true
			}
			// Initial stats are optional capability data. Retain a valid
			// authoritative window for each usage snapshot, but never fail the
			// prompt when an older or degraded Pi RPC rejects the request.
			s.consumeInitialSessionStatsPending()
			if resp.Success {
				cw := contextWindowFromSessionStats(resp.Data)
				if changed, usageObserved := s.setContextWindow(cw); changed && usageObserved {
					// A late pre-prompt correction arrived after the last usage.
					// Surface it immediately so runtime/session state cannot stay stale
					// when the round-end refresh is absent.
					s.emit(Event{Kind: EventContextWindow, ContextWindow: cw})
				}
			}
			return true
		}
		var responseErr error
		if isPromptResponse(resp) {
			responseErr = promptResponseError(resp)
			if responseErr == nil {
				// Pi guarantees correlated command responses. If the optional
				// initial stats response was omitted, prompt acceptance closes that
				// outstanding compatibility slot so a later no-ID final response is
				// not discarded as stale.
				s.consumeInitialSessionStatsPending()
			}
		} else if !resp.Success {
			responseErr = failureResponseError(resp)
		}
		if responseErr != nil {
			s.setErr(responseErr)
			s.emit(Event{Kind: EventError, Err: responseErr})
			return false
		}
		// compact turn 不发 agent_end —— compact response 即终止信号。
		if resp.Command == "compact" {
			s.finish(ctx)
			return false
		}
		return true
	}
	var ev rpcEvent
	if err := json.Unmarshal(rawLine, &ev); err != nil {
		return true
	}
	if err := s.handleRPCEvent(ctx, ev); err != nil {
		s.setErr(err)
		s.emit(Event{Kind: EventError, Err: err})
		return false
	}
	s.observeAgentEnd(ev, line)
	if isTerminalEvent(ev) {
		s.settle(ctx)
		return false
	}
	return true
}

func (s *Stream) finish(ctx context.Context) {
	s.emitTerminalMetadata(ctx)
	s.emit(Event{Kind: EventDone})
}

func (s *Stream) emitTerminalMetadata(ctx context.Context) {
	if s.captureUserAnchor {
		s.emitTrackedSessionMetadata(ctx)
	} else {
		s.emitSessionStats(ctx)
	}
}

func (s *Stream) emitFailedTurnAnchorMetadata(ctx context.Context) {
	if s.captureUserAnchor {
		// drain already replaced the canceled turn context with one bounded
		// settlement context. Keep that bound while using the same scanner/process.
		s.emitTrackedSessionMetadata(ctx)
	}
}

func (s *Stream) settle(ctx context.Context) {
	if candidate := s.pendingAgentEndError; candidate != nil {
		// Pi appends the accepted user entry before an assistant error/abort. Capture
		// that post-turn boundary even when the caller canceled its turn context.
		s.emitFailedTurnAnchorMetadata(ctx)
		s.recordFinalErrorDiagnostics(candidate.event, candidate.rawLine)
		s.setErr(candidate.err)
		s.emit(Event{Kind: EventError, Err: candidate.err})
		return
	}
	if err := s.pendingAssistantDeltaError; err != nil {
		s.emitFailedTurnAnchorMetadata(ctx)
		s.setErr(err)
		s.emit(Event{Kind: EventError, Err: err})
		return
	}
	s.finish(ctx)
}

const (
	initialSessionStatsRequestID = "initial-session-stats"
	finalSessionStatsRequestID   = "final-session-stats"
	sessionStatsTimeout          = 2 * time.Second
)

func (s *Stream) emitSessionStats(ctx context.Context) {
	if s == nil || s.proc == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	if err := s.send(ctx, map[string]any{
		"id": finalSessionStatsRequestID, "type": "get_session_stats",
	}); err != nil {
		return
	}

	// Keep scanner ownership in the drain goroutine. The async line scanner makes
	// ScanContext interruptible, so an optional stats timeout needs no detached
	// reader goroutine that could outlive terminal delivery.
	statsCtx, cancel := context.WithTimeout(ctx, sessionStatsTimeout)
	defer cancel()
	if cw := s.readSessionStatsContextWindow(statsCtx); cw > 0 {
		s.emit(Event{Kind: EventContextWindow, ContextWindow: cw})
	}
}

func (s *Stream) readSessionStatsContextWindow(ctx context.Context) int {
	for scanRPCLine(ctx, s.proc.lines) {
		emitRawFrame(s.proc, s.proc.lines.Bytes())
		line := strings.TrimSpace(s.proc.lines.Text())
		if line == "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		if resp.Type != "response" || resp.Command != "get_session_stats" {
			continue
		}
		// A delayed pre-prompt response must not be mistaken for the
		// authoritative round-end refresh. Correlated responses are explicit;
		// for ID-stripping implementations, command order identifies the first
		// outstanding no-ID response as the initial request.
		switch resp.ID {
		case initialSessionStatsRequestID:
			s.consumeInitialSessionStatsPending()
			continue
		case finalSessionStatsRequestID:
			// Authoritative response; decode below.
		case "":
			if s.consumeInitialSessionStatsPending() {
				continue
			}
		default:
			continue
		}
		if !resp.Success {
			return 0
		}
		return contextWindowFromSessionStats(resp.Data)
	}
	return 0
}

type trackedSessionMetadata struct {
	userAnchor    string
	contextWindow int
	entriesSeen   bool
	statsSeen     bool
}

func (s *Stream) emitTrackedSessionMetadata(ctx context.Context) {
	if s == nil || s.proc == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	const entriesRequestID = "session-entries-after"
	if err := s.send(ctx, map[string]any{"id": entriesRequestID, "type": "get_entries"}); err != nil {
		return
	}
	wantStats := s.send(ctx, map[string]any{
		"id": finalSessionStatsRequestID, "type": "get_session_stats",
	}) == nil

	metadataCtx, cancel := context.WithTimeout(ctx, sessionStatsTimeout)
	defer cancel()
	s.applyTrackedSessionMetadata(s.readTrackedSessionMetadata(metadataCtx, entriesRequestID, wantStats))
}

func (s *Stream) applyTrackedSessionMetadata(metadata trackedSessionMetadata) {
	s.setUserAnchor(metadata.userAnchor)
	if metadata.contextWindow > 0 {
		s.emit(Event{Kind: EventContextWindow, ContextWindow: metadata.contextWindow})
	}
}

func (s *Stream) readTrackedSessionMetadata(
	ctx context.Context,
	entriesRequestID string,
	wantStats bool,
) trackedSessionMetadata {
	metadata := trackedSessionMetadata{statsSeen: !wantStats}
	for scanRPCLine(ctx, s.proc.lines) {
		emitRawFrame(s.proc, s.proc.lines.Bytes())
		line := strings.TrimSpace(s.proc.lines.Text())
		if line == "" {
			continue
		}
		var response rpcResponse
		if err := json.Unmarshal([]byte(line), &response); err != nil || response.Type != "response" {
			continue
		}
		switch {
		case response.Command == "get_entries" && response.ID == entriesRequestID && !metadata.entriesSeen:
			metadata.entriesSeen = true
			if response.Success {
				var entries sessionEntriesWire
				if err := json.Unmarshal(response.Data, &entries); err == nil {
					metadata.userAnchor = firstUserEntryAfterBoundary(
						entries.Entries,
						s.userAnchorBoundary,
						entries.LeafID,
					)
				}
			}
		case response.Command == "get_session_stats" && !metadata.statsSeen:
			// A delayed pre-prompt response must not stand in for the round-end
			// refresh; see readSessionStatsContextWindow for the same correlation.
			if response.ID == initialSessionStatsRequestID ||
				(response.ID == "" && s.consumeInitialSessionStatsPending()) {
				continue
			}
			if response.ID != "" && response.ID != finalSessionStatsRequestID {
				continue
			}
			metadata.statsSeen = true
			if response.Success {
				metadata.contextWindow = contextWindowFromSessionStats(response.Data)
			}
		}
		if metadata.entriesSeen && metadata.statsSeen {
			return metadata
		}
	}
	return metadata
}

func firstUserEntryAfterBoundary(entries []sessionEntryWire, boundaryLeafID, currentLeafID string) string {
	boundaryLeafID = strings.TrimSpace(boundaryLeafID)
	currentLeafID = strings.TrimSpace(currentLeafID)
	if currentLeafID == "" || currentLeafID == boundaryLeafID {
		return ""
	}

	byID := make(map[string]sessionEntryWire, len(entries))
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		if _, duplicate := byID[id]; duplicate {
			return ""
		}
		entry.ID = id
		entry.ParentID = strings.TrimSpace(entry.ParentID)
		byID[id] = entry
	}

	path := make([]sessionEntryWire, 0)
	seen := make(map[string]struct{})
	for currentLeafID != boundaryLeafID {
		if currentLeafID == "" {
			if boundaryLeafID != "" {
				return ""
			}
			break
		}
		if _, cycle := seen[currentLeafID]; cycle {
			return ""
		}
		seen[currentLeafID] = struct{}{}
		entry, ok := byID[currentLeafID]
		if !ok {
			return ""
		}
		path = append(path, entry)
		currentLeafID = entry.ParentID
	}

	for i := len(path) - 1; i >= 0; i-- {
		entry := path[i]
		if entry.Type == "message" && entry.Message.Role == "user" {
			return entry.ID
		}
	}
	return ""
}

func contextWindowFromSessionStats(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var stats sessionStatsWire
	if err := json.Unmarshal(raw, &stats); err != nil || stats.ContextUsage == nil {
		return 0
	}
	return stats.ContextUsage.ContextWindow
}

func (s *Stream) handleRPCEvent(ctx context.Context, ev rpcEvent) error {
	switch ev.Type {
	case "message_start":
		// 只有 user 消息回显才 surface（首条 prompt + mid-turn steer 注入）；
		// runtime 据此对照 pending steer emit SteerConsumed。
		if text, ok := userEchoText(ev.Message); ok {
			s.emit(Event{Kind: EventUserMessage, Text: text})
		}
	case "message_update":
		s.handleAssistantDelta(ev.AssistantMessageEvent)
	case "message_end":
		s.handleMessageEnd(ev.Message)
	case "agent_end":
		if msg := lastAssistantFromAgentEnd(ev.Messages); msg != nil {
			s.recordAssistantMessage(msg)
		}
	case "tool_execution_start":
		s.emit(Event{Kind: EventPreToolUse, Tool: ToolEvent{ID: ev.ToolCallID, Name: ev.ToolName, Input: ev.Args}})
	case "tool_execution_update":
		s.emit(Event{Kind: EventToolUseUpdate, Tool: ToolEvent{ID: ev.ToolCallID, Name: ev.ToolName, PartialResult: ev.PartialResult}})
	case "tool_execution_end":
		content, details := toolResult(ev.Result)
		s.emit(Event{Kind: EventPostToolUse, Tool: ToolEvent{ID: ev.ToolCallID, Name: ev.ToolName, Content: content, Details: details, IsError: ev.IsError}})
	case "extension_ui_request":
		if isBlockingExtensionUIMethod(ev.Method) {
			if err := s.send(ctx, map[string]any{
				"type": "extension_ui_response", "id": ev.ID, "cancelled": true, //nolint:misspell // Pi RPC wire contract requires this JSON key.
			}); err != nil {
				return err
			}
		}
	case "compaction_start":
		s.emit(Event{Kind: EventRuntimeStatus, Text: "compacting"})
	case "compaction_end":
		s.emit(Event{Kind: EventCompactBoundary})
	case "auto_retry_start":
		// errorMessage is an untrusted provider payload and can echo prompt or
		// credentials. The retry state itself is the useful downstream signal.
		s.emit(Event{Kind: EventRuntimeStatus, Text: "retrying"})
	}
	return nil
}

func isBlockingExtensionUIMethod(method string) bool {
	switch strings.TrimSpace(method) {
	case "confirm", "select", "input", "editor":
		return true
	default:
		return false
	}
}

func (s *Stream) handleAssistantDelta(delta assistantDelta) {
	switch delta.Type {
	case "text_delta":
		s.emit(Event{Kind: EventTextDelta, Text: delta.Delta})
	case "thinking_delta":
		s.emit(Event{Kind: EventThinkingDelta, Text: delta.Delta})
	// 注意：toolcall_end 不再 emit PreToolUse。Pi 对一次工具调用会同时发
	// message_update/toolcall_end 和后续的 tool_execution_start（同一个
	// toolCallId），PreToolUse 只从 tool_execution_start 出，避免下游工具卡重复。
	case "error":
		if s.pendingAgentEndError == nil {
			// reason is an untrusted provider failure string. Preserve only the
			// stable stream-failure classification until agent_end settles it.
			s.pendingAssistantDeltaError = errors.New("piagent: assistant stream failed")
		}
	}
}

func (s *Stream) handleMessageEnd(raw json.RawMessage) {
	msg, err := parseAssistantMessage(raw)
	if err != nil || msg == nil {
		return
	}
	s.recordAssistantMessage(msg)
}

// recordAssistantMessage 把一条 assistant 消息的 model / usage 收下,并按「每次内部
// API call 一条 usage」的口径 emit。
//
// 同一条消息会到达两次:message_end 一次,agent_end.messages 把本轮每条 assistant
// 消息连同 usage 原样重发时又一次。下游对 completion / reasoning 是累加的,重发那条
// 会把最后一跳的 output 记两遍(单跳的轮直接翻倍)。按 responseId 认身份,记过的不再
// emit —— agent_end 仍是兜底:message_end 没带 usage 时,用量只能从它那里捡回来。
func (s *Stream) recordAssistantMessage(msg *assistantMessage) {
	s.mu.Lock()
	if strings.TrimSpace(msg.Model) != "" {
		s.model = strings.TrimSpace(msg.Model)
	}
	u := usageFromMessage(msg)
	contextWindow := s.contextWindow
	hasUsage := u.PromptTokens > 0 || u.CompletionTokens > 0
	if hasUsage && s.usageRecorded[assistantMessageKey(msg)] {
		s.mu.Unlock()
		return
	}
	if hasUsage {
		s.usageObserved = true
		if s.usageRecorded == nil {
			s.usageRecorded = map[string]bool{}
		}
		s.usageRecorded[assistantMessageKey(msg)] = true
	}
	s.mu.Unlock()
	if hasUsage {
		// Keep the standalone event for older desktop consumers, then carry the
		// same denominator atomically on usage for current clients. Both repeat at
		// each API-call boundary so a later snapshot repairs an earlier Wails miss.
		if contextWindow > 0 {
			s.emit(Event{Kind: EventContextWindow, ContextWindow: contextWindow})
		}
		s.emit(Event{Kind: EventUsage, Usage: u, Model: msg.Model, ContextWindow: contextWindow})
	}
}

// assistantMessageKey 是一条 assistant 消息的身份。responseId 优先;缺省时退回
// 「时间戳 + 这次调用的用量」——agent_end 重发的是同一个对象,逐字段相同。
func assistantMessageKey(msg *assistantMessage) string {
	if id := strings.TrimSpace(msg.ResponseID); id != "" {
		return id
	}
	u := msg.Usage
	if u == nil {
		return strconv.FormatInt(msg.Timestamp, 10)
	}
	return fmt.Sprintf("%d/%d/%d/%d/%d", msg.Timestamp, u.Input, u.Output, u.CacheRead, u.CacheWrite)
}

func (s *Stream) observeAgentEnd(ev rpcEvent, rawLine string) {
	if ev.Type != "agent_end" {
		return
	}
	outcome := classifyFinalAgentEnd(ev)
	switch outcome.kind {
	case agentEndOutcomeFailure:
		// agent_end carries the authoritative terminal message. It supersedes
		// any preceding streaming error delta and is confirmed at settlement.
		s.pendingAgentEndError = &agentEndError{err: outcome.err, event: ev, rawLine: rawLine}
		s.pendingAssistantDeltaError = nil
	case agentEndOutcomeSuccess:
		// A completed low-level run clears retry candidates. Other outcomes
		// deliberately leave a staged delta intact until agent_settled decides it.
		s.pendingAgentEndError = nil
		s.pendingAssistantDeltaError = nil
	}
}

func classifyFinalAgentEnd(ev rpcEvent) agentEndOutcome {
	msg := lastAssistantFromAgentEnd(ev.Messages)
	if msg == nil {
		return agentEndOutcome{kind: agentEndOutcomeUnknown}
	}
	switch strings.TrimSpace(msg.StopReason) {
	case "stop", "length", "toolUse":
		return agentEndOutcome{kind: agentEndOutcomeSuccess}
	case "error":
		return agentEndFailure(msg, "error")
	case "aborted":
		return agentEndFailure(msg, "aborted")
	default:
		return agentEndOutcome{kind: agentEndOutcomeUnknown}
	}
}

func agentEndFailure(msg *assistantMessage, classification string) agentEndOutcome {
	var err error
	switch classification {
	case "aborted":
		if strings.TrimSpace(msg.ErrorMessage) == "" {
			err = errors.New("piagent: aborted")
		} else {
			err = errors.New("piagent: user requested abort")
		}
	case "error":
		if strings.EqualFold(strings.TrimSpace(msg.ErrorMessage), "terminated") {
			err = errors.New("piagent: terminated")
		} else {
			err = errors.New("piagent: final provider failure")
		}
	default:
		err = errors.New("piagent: agent failed")
	}
	return agentEndOutcome{kind: agentEndOutcomeFailure, err: err}
}

func (s *Stream) emit(ev Event) {
	select {
	case s.events <- ev:
	default:
		s.events <- ev
	}
}

func (s *Stream) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err == nil {
		s.err = err
	}
}

func (s *Stream) recordFinalErrorDiagnostics(ev rpcEvent, rawLine string) {
	msg := lastAssistantFromAgentEnd(ev.Messages)
	if msg == nil {
		return
	}
	safeFrame := sanitizeDiagnosticFrame([]byte(rawLine))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.diagnostics.FinalErrorEventType = ev.Type
	s.diagnostics.FinalErrorStopReason = strings.TrimSpace(msg.StopReason)
	s.diagnostics.FinalErrorFrame = string(safeFrame)
}

func toolResult(raw json.RawMessage) (string, json.RawMessage) {
	if len(raw) == 0 {
		return "", nil
	}
	var obj struct {
		Content []contentBlock  `json:"content"`
		Details json.RawMessage `json:"details"`
	}
	if err := json.Unmarshal(raw, &obj); err != nil {
		return string(raw), nil
	}
	var b strings.Builder
	for _, c := range obj.Content {
		if c.Type == "text" {
			b.WriteString(c.Text)
		}
	}
	return b.String(), obj.Details
}
