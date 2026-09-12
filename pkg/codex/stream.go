package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cago-frame/agents/provider"
)

func (s *Stream) drain(ctx context.Context) {
	defer close(s.events)
	defer s.clearActiveTurn()
	defer s.closeOnce.Do(func() {
		if s.closeAppOnDrain {
			_ = s.app.terminate(context.Background(), s.killGrace)
		}
	})

	preSeen := map[string]struct{}{}
	cancelCh := ctx.Done()
	var interruptTimer *time.Timer
	var interruptDeadline <-chan time.Time
	defer func() {
		if interruptTimer != nil {
			interruptTimer.Stop()
		}
	}()
	for {
		select {
		case in, ok := <-s.app.Incoming():
			if !ok {
				if err := s.app.Err(); err != nil {
					s.failProcess(err)
				} else if !s.app.isStopping() {
					s.failProcess(ErrProcessDead)
				}
				return
			}
			done := s.handleInbound(ctx, in, preSeen)
			if done {
				return
			}
		case <-cancelCh:
			cancelCh = nil
			s.emitError(ctx.Err(), nil)
			interruptCtx, cancel := context.WithTimeout(context.Background(), s.interruptGrace())
			err := s.Interrupt(interruptCtx)
			cancel()
			if err != nil && !errors.Is(err, ErrNoActiveTurn) {
				s.emitError(fmt.Errorf("codex: interrupt after context cancellation: %w", err), nil)
				s.markReusable(false)
				_, _ = s.transitionTerminal(TurnStateFailed)
				_ = s.app.terminate(context.Background(), s.interruptGrace())
				return
			}
			if errors.Is(err, ErrNoActiveTurn) {
				changed, _ := s.beginInterrupt()
				if changed {
					s.signalInterrupt()
				}
			}
		case <-s.interruptSignal:
			if interruptTimer == nil {
				interruptTimer = time.NewTimer(s.interruptGrace())
				interruptDeadline = interruptTimer.C
			}
		case <-interruptDeadline:
			s.emitError(errors.New("codex: interrupted turn did not reach a terminal notification before grace expired"), nil)
			s.markReusable(false)
			_, _ = s.transitionTerminal(TurnStateFailed)
			_ = s.app.terminate(context.Background(), s.interruptGrace())
			return
		}
	}
}

func (s *Stream) handleInbound(ctx context.Context, in appInbound, preSeen map[string]struct{}) bool {
	if in.Kind == appInboundRequest {
		if !s.ownsServerRequest(in) {
			_ = s.app.RespondError(ctx, in.ID, -32001, "request does not belong to the active turn")
			return false
		}
		if err := s.handleServerRequest(ctx, in); err != nil {
			s.emitError(err, nil)
		}
		return false
	}
	n, err := parseNotification(in.Params)
	if err != nil {
		s.emitError(err, in.Params)
		return false
	}
	if in.Method == appMethodTurnStarted {
		s.adoptStartedTurn(n)
	}
	if !s.ownsNotification(n) {
		return false
	}
	if s.state != nil && s.state.Terminal() {
		return in.Method == appMethodTurnCompleted
	}
	if n.ThreadID != "" {
		s.setSessionID(n.ThreadID)
	}
	switch in.Method {
	case appMethodItemPlanDelta:
		s.emitPlanDelta(n, in.Params)
	case appMethodItemAgentMessageDelta:
		if n.ItemID != "" {
			// If an agentMessage later completes with full text, avoid re-emitting it.
			preSeen["partial:"+n.ItemID] = struct{}{}
		}
		s.emit(Event{Kind: EventTextDelta, SessionID: s.SessionID(), Text: n.Delta, Raw: in.Params})
	case appMethodItemReasoningTextDelta, appMethodItemReasoningSummaryTextDelta:
		s.emit(Event{Kind: EventThinkingDelta, SessionID: s.SessionID(), Text: n.Delta, Raw: in.Params})
	case appMethodThreadTokenUsageUpdated:
		usage := appUsageToProvider(n.Usage)
		s.setUsage(usage)
		if cw := appContextWindow(n); cw > 0 {
			s.setContextWindow(cw)
		}
		s.emit(Event{
			Kind:          EventUsage,
			SessionID:     s.SessionID(),
			Usage:         usage,
			ContextWindow: s.currentContextWindow(),
			Raw:           in.Params,
		})
	case appMethodTurnPlanUpdated:
		s.emitPlan(n, in.Params)
	case appMethodServerRequestResolved:
		s.handleRequestResolved(n, in.Params)
	case appMethodRawResponseItemCompleted:
		if isCompactItem(n.Item) {
			s.emitCompactBoundary(n, in.Params)
		}
	case appMethodItemStarted:
		s.handleItemStarted(n, in.Params, preSeen)
	case appMethodItemFileChangePatchUpdated:
		if n.ItemID != "" && len(n.Changes) > 0 {
			s.emitPreToolUseIfMissing(&appThreadItem{Type: appItemFileChange, ID: n.ItemID, Changes: n.Changes}, in.Params, preSeen)
		}
	case appMethodItemCompleted:
		s.handleItemCompleted(n, in.Params, preSeen)
	case appMethodThreadCompacted:
		s.emitCompactBoundary(n, in.Params)
		if s.isManualCompactStream() {
			changed, transitionErr := s.transitionTerminal(TurnStateCompleted)
			if transitionErr != nil {
				s.emitError(transitionErr, in.Params)
				return true
			}
			if !changed {
				return true
			}
			s.emit(Event{
				Kind:          EventDone,
				SessionID:     s.SessionID(),
				Usage:         s.currentUsage(),
				ContextWindow: s.currentContextWindow(),
				Raw:           in.Params,
			})
			return true
		}
	case appMethodTurnCompleted:
		return s.handleTurnCompleted(n, in.Params)
	case appMethodError:
		if n.WillRetry {
			s.emit(Event{Kind: EventRetry, SessionID: s.SessionID(), Retry: appRetryEvent(n), Raw: in.Params})
			return false
		}
		message := "app-server reported an error"
		if n.Error != nil && strings.TrimSpace(n.Error.Message) != "" {
			message = n.Error.Message
			if strings.TrimSpace(n.Error.AdditionalDetails) != "" {
				message += ": " + n.Error.AdditionalDetails
			}
		}
		s.emitError(fmt.Errorf("codex app-server: %s", message), in.Params)
	}
	return false
}

// adoptStartedTurn treats turn/started as the authoritative turn identity.
// With an active goal, Codex 0.144.4 can return a provisional id from
// turn/start and then execute the turn under a different id.
func (s *Stream) adoptStartedTurn(n appNotification) {
	if n.Turn == nil || strings.TrimSpace(n.Turn.ID) == "" {
		return
	}
	if s.history != nil && s.history.Contains(n.Turn.ID) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if n.ThreadID != "" && s.sessionID != "" && n.ThreadID != s.sessionID {
		return
	}
	if s.turnID != "" && s.turnID != n.Turn.ID && s.history != nil {
		s.history.Remember(s.turnID)
	}
	s.turnID = n.Turn.ID
	if s.state != nil {
		_, _ = s.state.Transition(TurnStateRunning)
	}
}

func (s *Stream) ownsNotification(n appNotification) bool {
	s.mu.RLock()
	sessionID := s.sessionID
	turnID := s.turnID
	s.mu.RUnlock()

	if n.ThreadID != "" && sessionID != "" && n.ThreadID != sessionID {
		return false
	}
	if n.TurnID != "" && turnID != "" && n.TurnID != turnID {
		return false
	}
	if n.Turn != nil && n.Turn.ID != "" && turnID != "" && n.Turn.ID != turnID {
		return false
	}
	return true
}

func (s *Stream) isManualCompactStream() bool {
	return s.turnID == "" && strings.TrimSpace(s.compactTrigger) != ""
}

func (s *Stream) handleItemStarted(n appNotification, raw json.RawMessage, preSeen map[string]struct{}) {
	item := n.Item
	if item == nil || item.ID == "" {
		return
	}
	if isCompactItem(item) {
		s.emitCompactBoundary(n, raw)
		return
	}
	switch item.Type {
	case appItemUserMessage:
		s.emitUserMessageIfMissing(item, raw, preSeen)
		return
	case appItemAgentMessage, appItemReasoning, appItemPlan,
		appItemHookPrompt, appItemEnteredReviewMode, appItemExitedReviewMode:
		return
	case appItemFileChange:
		if len(item.Changes) == 0 {
			return
		}
	default:
		if !isToolItemType(item.Type) {
			return
		}
	}
	s.emitPreToolUseIfMissing(item, raw, preSeen)
}

func (s *Stream) handleItemCompleted(n appNotification, raw json.RawMessage, preSeen map[string]struct{}) {
	item := n.Item
	if item == nil {
		return
	}
	if isCompactItem(item) {
		s.emitCompactBoundary(n, raw)
		return
	}
	if item.ID != "" && !s.markItemCompleted(item.ID) {
		return
	}
	switch item.Type {
	case appItemUserMessage:
		s.emitUserMessageIfMissing(item, raw, preSeen)
	case appItemAgentMessage:
		if text := textForItem(item); text != "" {
			if _, partial := preSeen["partial:"+item.ID]; !partial {
				s.emit(Event{Kind: EventTextDelta, SessionID: s.SessionID(), Text: text, Raw: raw})
			}
		}
	case appItemPlan:
		if strings.TrimSpace(item.Text) != "" {
			s.emit(Event{Kind: EventPlanUpdated, SessionID: s.SessionID(), PlanText: item.Text, Raw: raw})
		}
	case appItemCommandExecution, appItemFileChange, appItemMCPToolCall, appItemDynamicToolCall, appItemCollabAgentTool,
		appItemWebSearch, appItemImageView, appItemSleep, appItemImageGeneration, appItemSubAgentActivity:
		s.emitPreToolUseIfMissing(item, raw, preSeen)
		s.emit(Event{
			Kind:      EventPostToolUse,
			SessionID: s.SessionID(),
			Tool: &ToolEvent{
				ID:       item.ID,
				Name:     toolNameForItem(item),
				Input:    toolInputForItem(item),
				Response: toolResponseForItem(item),
				Err:      toolErrForItem(item),
				Source:   toolSourceForItem(item),
			},
			Raw: raw,
		})
	default:
		if _, startedAsTool := preSeen[item.ID]; startedAsTool {
			s.emit(Event{
				Kind:      EventPostToolUse,
				SessionID: s.SessionID(),
				Tool: &ToolEvent{
					ID:       item.ID,
					Name:     toolNameForItem(item),
					Input:    toolInputForItem(item),
					Response: toolResponseForItem(item),
					Err:      toolErrForItem(item),
					Source:   toolSourceForItem(item),
				},
				Raw: raw,
			})
		}
	}
}

func (s *Stream) emitCompactBoundary(n appNotification, raw json.RawMessage) {
	threadID := n.ThreadID
	if threadID == "" {
		threadID = s.SessionID()
	}
	turnID := n.TurnID
	if turnID == "" && n.Turn != nil {
		turnID = n.Turn.ID
	}
	key := threadID + ":" + turnID
	if key == ":" && n.Item != nil {
		key = "item:" + n.Item.ID
	}
	if key == ":" {
		key = string(raw)
	}
	if _, ok := s.compactSeen[key]; ok {
		return
	}
	s.compactSeen[key] = struct{}{}

	trigger := strings.TrimSpace(s.compactTrigger)
	if trigger == "" {
		trigger = "auto"
	}
	s.emit(Event{
		Kind:      EventCompactBoundary,
		SessionID: threadID,
		Compact:   &CompactEvent{Trigger: trigger},
		Raw:       raw,
	})
}

func isCompactItem(item *appThreadItem) bool {
	if item == nil {
		return false
	}
	switch item.Type {
	case appItemContextCompaction, "context_compaction", "compaction":
		return true
	default:
		return false
	}
}

func (s *Stream) emitUserMessageIfMissing(item *appThreadItem, raw json.RawMessage, seen map[string]struct{}) {
	if item == nil || item.ID == "" {
		return
	}
	text := textForItem(item)
	if strings.TrimSpace(text) == "" {
		return
	}
	key := "user:" + item.ID
	if _, ok := seen[key]; ok {
		return
	}
	seen[key] = struct{}{}
	s.emit(Event{Kind: EventUserMessage, SessionID: s.SessionID(), Text: text, Raw: raw})
}

func (s *Stream) emitPreToolUseIfMissing(item *appThreadItem, raw json.RawMessage, preSeen map[string]struct{}) {
	if item == nil || item.ID == "" {
		return
	}
	if _, ok := preSeen[item.ID]; ok {
		return
	}
	preSeen[item.ID] = struct{}{}
	s.emit(Event{
		Kind:      EventPreToolUse,
		SessionID: s.SessionID(),
		Tool: &ToolEvent{
			ID:     item.ID,
			Name:   toolNameForItem(item),
			Input:  toolInputForItem(item),
			Source: toolSourceForItem(item),
		},
		Raw: raw,
	})
}

func (s *Stream) emitPlan(n appNotification, raw json.RawMessage) {
	if len(n.Plan) == 0 {
		return
	}
	steps := make([]PlanStep, 0, len(n.Plan))
	for _, p := range n.Plan {
		steps = append(steps, PlanStep(p))
	}
	s.emit(Event{Kind: EventPlanUpdated, SessionID: s.SessionID(), Plan: steps, Raw: raw})

	id := n.TurnID + ":plan"
	tool := &ToolEvent{
		ID:       id,
		Name:     appToolUpdatePlan,
		Input:    append(json.RawMessage(nil), raw...),
		Response: append(json.RawMessage(nil), raw...),
		Source:   ToolSourceBuiltin,
	}
	s.emit(Event{Kind: EventPreToolUse, SessionID: s.SessionID(), Tool: tool, Raw: raw})
	s.emit(Event{Kind: EventPostToolUse, SessionID: s.SessionID(), Tool: tool, Raw: raw})
}

func (s *Stream) handleServerRequest(ctx context.Context, in appInbound) error {
	app := s.app
	if app == nil {
		return ErrNoActiveTurn
	}
	switch in.Method {
	case appMethodItemCommandApprovalRequest, appMethodItemFileApprovalRequest:
		return s.handleApprovalRequest(ctx, in)
	case appMethodItemPermissionsRequest:
		return s.handleApprovalRequest(ctx, in)
	case appMethodItemToolRequestUserInput:
		ev, err := parseRequestUserInputParams(in.Params)
		if err != nil {
			s.emitError(err, in.Params)
			return app.Respond(ctx, in.ID, map[string]any{"answers": map[string]any{}})
		}
		ev.RequestID = s.registerUserInputRequest(in.ID)
		if ev.RequestID == "" {
			err := ErrNoActiveTurn
			s.emitError(err, in.Params)
			return app.Respond(ctx, in.ID, map[string]any{"answers": map[string]any{}})
		}
		s.emit(Event{
			Kind:             EventRequestUserInput,
			SessionID:        s.SessionID(),
			RequestUserInput: &ev,
			Raw:              in.Params,
		})
		return nil
	case appMethodItemToolCall:
		return app.Respond(ctx, in.ID, map[string]any{"contentItems": []any{}, "success": false})
	default:
		return app.RespondError(ctx, in.ID, -32601, "Method not found")
	}
}

func (s *Stream) handleApprovalRequest(ctx context.Context, in appInbound) error {
	app := s.app
	if app == nil {
		return ErrNoActiveTurn
	}
	ev, err := parseApprovalRequest(in.Method, in.Params)
	if err != nil {
		s.emitError(err, in.Params)
		return app.Respond(ctx, in.ID, approvalResponse(approvalRequest{method: in.Method, params: in.Params}, false, false))
	}
	ev.RequestID = s.registerApprovalRequest(in.ID, in.Method, in.Params)
	if ev.RequestID == "" {
		err := ErrNoActiveTurn
		s.emitError(err, in.Params)
		return app.Respond(ctx, in.ID, approvalResponse(approvalRequest{method: in.Method, params: in.Params}, false, false))
	}
	s.emit(Event{
		Kind:      EventApprovalRequest,
		SessionID: s.SessionID(),
		Approval:  &ev,
		Raw:       in.Params,
	})
	return nil
}

func (s *Stream) emit(ev Event) {
	select {
	case s.events <- ev:
	default:
		s.events <- ev
	}
}

func (s *Stream) emitError(err error, raw json.RawMessage) {
	if err == nil {
		return
	}
	s.setErr(err)
	s.emit(Event{Kind: EventError, SessionID: s.SessionID(), Err: err, Raw: raw})
}

func (s *Stream) setSessionID(id string) {
	s.mu.Lock()
	s.sessionID = id
	s.mu.Unlock()
}

func (s *Stream) setErr(err error) {
	s.mu.Lock()
	if s.err == nil {
		s.err = err
	}
	s.mu.Unlock()
}

func (s *Stream) setUsage(u provider.Usage) {
	s.mu.Lock()
	s.usage = u
	s.mu.Unlock()
}

func (s *Stream) clearActiveTurn() {
	s.mu.Lock()
	s.turnID = ""
	s.mu.Unlock()
}

func (s *Stream) currentUsage() provider.Usage {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.usage
}

func (s *Stream) setContextWindow(cw int) {
	s.mu.Lock()
	s.contextWindow = cw
	s.mu.Unlock()
}

func (s *Stream) currentContextWindow() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.contextWindow
}

func (s *Stream) transition(next TurnState) (bool, error) {
	if s.state == nil {
		s.state = newTurnStateMachine()
	}
	return s.state.Transition(next)
}

func (s *Stream) interruptGrace() time.Duration {
	if s.killGrace > 0 {
		return s.killGrace
	}
	return 100 * time.Millisecond
}

func (s *Stream) markReusable(reusable bool) {
	s.mu.Lock()
	s.reusable = reusable
	s.mu.Unlock()
}

func (s *Stream) failProcess(err error) {
	if err == nil {
		err = ErrProcessDead
	}
	s.markReusable(false)
	if s.state == nil || !s.state.Terminal() {
		_, _ = s.transitionTerminal(TurnStateFailed)
		s.emitError(err, nil)
	}
}

func (s *Stream) handleTurnCompleted(n appNotification, raw json.RawMessage) bool {
	status := ""
	turnID := n.TurnID
	if n.Turn != nil {
		status = n.Turn.Status
		if n.Turn.ID != "" {
			turnID = n.Turn.ID
		}
	}

	target := TurnStateFailed
	var terminalErr error
	switch status {
	case appStatusCompleted:
		target = TurnStateCompleted
	case appStatusInterrupted:
		target = TurnStateCanceled
	case appStatusFailed:
		target = TurnStateFailed
		terminalErr = appTurnErr(n.Turn)
	default:
		terminalErr = fmt.Errorf("codex: turn/completed carried non-terminal status %q", status)
	}
	changed, transitionErr := s.transitionTerminal(target)
	if transitionErr != nil {
		s.emitError(transitionErr, raw)
		return true
	}
	if !changed {
		return true
	}
	if s.history != nil {
		s.history.Remember(turnID)
	}
	if terminalErr != nil {
		s.emitError(terminalErr, raw)
	}
	s.emit(Event{
		Kind:          EventDone,
		SessionID:     s.SessionID(),
		Usage:         s.currentUsage(),
		ContextWindow: s.currentContextWindow(),
		Raw:           raw,
	})
	return true
}

func (s *Stream) emitPlanDelta(n appNotification, raw json.RawMessage) {
	if n.Delta == "" {
		return
	}
	key := strings.TrimSpace(n.ItemID)
	if key == "" {
		key = strings.TrimSpace(n.TurnID) + ":plan"
	}
	if s.planText == nil {
		s.planText = map[string]string{}
	}
	s.planText[key] += n.Delta
	s.emit(Event{
		Kind:      EventPlanUpdated,
		SessionID: s.SessionID(),
		PlanText:  s.planText[key],
		Raw:       raw,
	})
}

func (s *Stream) markItemCompleted(itemID string) bool {
	if s.completedItems == nil {
		s.completedItems = map[string]struct{}{}
	}
	if _, exists := s.completedItems[itemID]; exists {
		return false
	}
	s.completedItems[itemID] = struct{}{}
	return true
}

func isToolItemType(itemType string) bool {
	switch itemType {
	case appItemCommandExecution, appItemFileChange, appItemMCPToolCall,
		appItemDynamicToolCall, appItemCollabAgentTool, appItemWebSearch,
		appItemImageView, appItemSleep, appItemImageGeneration, appItemSubAgentActivity:
		return true
	default:
		return false
	}
}

func (s *Stream) ownsServerRequest(in appInbound) bool {
	if s == nil || s.app == nil || (s.state != nil && (s.state.Terminal() || s.state.State() == TurnStateInterrupting)) {
		return false
	}
	var identity struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
	}
	if len(in.Params) > 0 && json.Unmarshal(in.Params, &identity) != nil {
		return false
	}
	return s.ownsNotification(appNotification{ThreadID: identity.ThreadID, TurnID: identity.TurnID})
}

func (s *Stream) handleRequestResolved(n appNotification, raw json.RawMessage) {
	requestID := requestIDKey(n.RequestID)
	if requestID == "" {
		return
	}
	kind, found := s.removePendingRequest(requestID)
	if !found {
		return
	}
	s.emit(Event{
		Kind:      EventRequestResolved,
		SessionID: s.SessionID(),
		RequestResolved: &RequestResolvedEvent{
			RequestID: requestID,
			Kind:      kind,
		},
		Raw: raw,
	})
}

func (s *Stream) removePendingRequest(requestID string) (RequestKind, bool) {
	s.userInputMu.Lock()
	defer s.userInputMu.Unlock()
	if _, exists := s.userInputRequests[requestID]; exists {
		delete(s.userInputRequests, requestID)
		s.updateWaitStateLocked()
		return RequestKindUserInput, true
	}
	if _, exists := s.approvalRequests[requestID]; exists {
		delete(s.approvalRequests, requestID)
		s.updateWaitStateLocked()
		return RequestKindApproval, true
	}
	return "", false
}

func (s *Stream) transitionTerminal(target TurnState) (bool, error) {
	s.userInputMu.Lock()
	defer s.userInputMu.Unlock()
	changed, err := s.transition(target)
	if changed {
		clear(s.userInputRequests)
		clear(s.approvalRequests)
	}
	return changed, err
}

func (s *Stream) beginInterrupt() (bool, error) {
	s.userInputMu.Lock()
	defer s.userInputMu.Unlock()
	changed, err := s.transition(TurnStateInterrupting)
	if changed {
		clear(s.userInputRequests)
		clear(s.approvalRequests)
	}
	return changed, err
}

func (s *Stream) signalInterrupt() {
	if s == nil || s.interruptSignal == nil {
		return
	}
	select {
	case s.interruptSignal <- struct{}{}:
	default:
	}
}

func (s *Stream) updateWaitStateLocked() {
	if s.state == nil || s.state.Terminal() || s.state.State() == TurnStateInterrupting {
		return
	}
	target := TurnStateRunning
	if len(s.userInputRequests)+len(s.approvalRequests) > 0 {
		target = TurnStateWaiting
	}
	_, _ = s.state.Transition(target)
}
