package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

const (
	approvalStatusResolved = "resolved"
	approvalStatusExpired  = "expired"

	serverRequestApproval = "approval"

	// requestCancelResolved is the request.cancel reason Hermes uses when the
	// approval was answered on another surface; every other reason (timeout,
	// interrupted, shutdown, session_closed) withdraws it unanswered.
	requestCancelResolved = "resolved"
)

// hermesChoices maps each normalized decision onto Hermes' approval choice, in
// the order the card presents them.
var hermesChoices = []struct{ decision, choice string }{
	{agentruntime.ApprovalDecisionAllowOnce, "once"},
	{agentruntime.ApprovalDecisionAllowSession, "session"},
	{agentruntime.ApprovalDecisionAllowAlways, "always"},
	{agentruntime.ApprovalDecisionDeny, "deny"},
}

// approvalParams is the `approval` server request's params. The command arrives
// already redacted by Hermes.
type approvalParams struct {
	Command        string   `json:"command"`
	Description    string   `json:"description"`
	Choices        []string `json:"choices"`
	AllowSession   *bool    `json:"allow_session"`
	AllowPermanent *bool    `json:"allow_permanent"`
	ToolName       string   `json:"tool_name"`
}

type requestCancelPayload struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Reason string `json:"reason"`
}

type approvalState struct {
	allowed  []string
	terminal agentruntime.ExecApprovalResolution
}

// allowedDecisions keeps only the choices Hermes listed and allows, as
// normalized decisions in presentation order.
func (p approvalParams) allowedDecisions() []string {
	allowed := make([]string, 0, len(hermesChoices))
	for _, c := range hermesChoices {
		if !slices.Contains(p.Choices, c.choice) {
			continue
		}
		if c.choice == "session" && p.AllowSession != nil && !*p.AllowSession {
			continue
		}
		if c.choice == "always" && p.AllowPermanent != nil && !*p.AllowPermanent {
			continue
		}
		allowed = append(allowed, c.decision)
	}
	return allowed
}

func hermesChoice(decision string) string {
	for _, c := range hermesChoices {
		if c.decision == decision {
			return c.choice
		}
	}
	return ""
}

// handleServerRequest dispatches a server->client request to its card
// builder by method. The codec only routes methods this runtime owns
// (routedServerRequests in frame.go); anything else is declined here too so
// Hermes never waits on it.
func (a *activeTurn) handleServerRequest(ctx context.Context, req *ServerRequest) {
	if req == nil {
		return
	}
	switch req.Method {
	case serverRequestApproval:
		a.handleApprovalRequest(ctx, req)
	case serverRequestClarify:
		a.handleClarifyRequest(ctx, req)
	default:
		a.reject(ctx, req)
	}
}

// handleUnsupportedRequest turns an already-answered EventUnsupportedRequest
// into a transcript notice naming the purpose frame.go's
// unsupportedServerRequests maps the method onto — only the method is resolved
// here; params never reach this far. A method outside that closed vocabulary
// was answered "method not found" and reads as an unknown request.
func (a *activeTurn) handleUnsupportedRequest(method string) {
	purpose, ok := unsupportedServerRequests[method]
	if !ok {
		purpose = agentruntime.UnsupportedRequestUnknown
	}
	a.emit(agentruntime.UnsupportedRequestNotice{Purpose: purpose})
}

// handleApprovalRequest turns an `approval` server->client request into its
// card.
func (a *activeTurn) handleApprovalRequest(ctx context.Context, req *ServerRequest) {
	var params approvalParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		a.reject(ctx, req)
		return
	}
	allowed := params.allowedDecisions()
	if len(allowed) == 0 {
		a.reject(ctx, req)
		return
	}
	a.approvalMu.Lock()
	if _, seen := a.approvals[req.ID]; seen {
		a.approvalMu.Unlock()
		return
	}
	a.approvals[req.ID] = &approvalState{allowed: allowed}
	a.approvalMu.Unlock()
	logger.Ctx(ctx).Info("hermes.Runtime: approval requested",
		zap.Int64("sessionID", a.sessionID), zap.String("approvalID", req.ID),
		zap.Strings("allowedDecisions", allowed))
	a.emit(agentruntime.ExecApprovalRequested{
		ID:               req.ID,
		ApprovalKind:     agentruntime.ApprovalKindHermes,
		CommandText:      strings.TrimSpace(params.Command),
		Description:      strings.TrimSpace(params.Description),
		ToolName:         strings.TrimSpace(params.ToolName),
		AllowedDecisions: allowed,
	})
}

func (a *activeTurn) reject(ctx context.Context, req *ServerRequest) {
	if err := a.sess.RejectServerRequest(req.ID); err != nil && !errors.Is(err, errSessionClosed) {
		logger.Ctx(ctx).Warn("hermes.Runtime: decline server request failed",
			zap.Int64("sessionID", a.sessionID), zap.String("requestID", req.ID),
			zap.String("method", req.Method), zap.Error(err))
	}
}

// handleRequestCancel converges a card Hermes withdrew. Clarify withdrawals
// always converge on skipped (see markAskTerminal / the clarify protocol
// notes in clarify.go); approval withdrawals keep the resolved/expired split.
func (a *activeTurn) handleRequestCancel(payload json.RawMessage) {
	p, ok := decodePayload[requestCancelPayload](payload)
	if !ok || strings.TrimSpace(p.ID) == "" {
		return
	}
	id := strings.TrimSpace(p.ID)
	if strings.TrimSpace(p.Method) == serverRequestClarify {
		a.markAskTerminal(id, agentruntime.UserAskResolved{Skipped: true})
		return
	}
	status := approvalStatusExpired
	if strings.TrimSpace(p.Reason) == requestCancelResolved {
		status = approvalStatusResolved
	}
	a.markApprovalTerminal(id, agentruntime.ExecApprovalResolution{Status: status})
}

// resolveApproval answers a pending card. Answers are serialized, so racing or
// repeated calls reach Hermes once and all return the same terminal state.
func (a *activeTurn) resolveApproval(ctx context.Context, approvalID, decision string) (agentruntime.ExecApprovalResolution, error) {
	approvalID = strings.TrimSpace(approvalID)
	a.approvalResolveMu.Lock()
	defer a.approvalResolveMu.Unlock()

	a.approvalMu.Lock()
	state := a.approvals[approvalID]
	if state == nil {
		a.approvalMu.Unlock()
		return agentruntime.ExecApprovalResolution{}, fmt.Errorf("hermes approval: approval not found")
	}
	if state.terminal.Status != "" {
		terminal := state.terminal
		a.approvalMu.Unlock()
		return terminal, nil
	}
	allowed := state.allowed
	a.approvalMu.Unlock()
	if !slices.Contains(allowed, decision) {
		return agentruntime.ExecApprovalResolution{}, fmt.Errorf("hermes approval: decision %q is not allowed", decision)
	}

	if err := a.sess.RespondServerRequest(approvalID, map[string]any{"choice": hermesChoice(decision)}); err != nil {
		if errors.Is(err, errSessionClosed) || errors.Is(err, errServerRequestNotOpen) {
			// The connection (and so the request) is gone: nothing can answer it now.
			return a.markApprovalTerminal(approvalID, agentruntime.ExecApprovalResolution{Status: approvalStatusExpired}), nil
		}
		logger.Ctx(ctx).Warn("hermes.Runtime: answer approval failed",
			zap.Int64("sessionID", a.sessionID), zap.String("approvalID", approvalID), zap.Error(err))
		return agentruntime.ExecApprovalResolution{}, err
	}
	logger.Ctx(ctx).Info("hermes.Runtime: approval answered",
		zap.Int64("sessionID", a.sessionID), zap.String("approvalID", approvalID), zap.String("decision", decision))
	return a.markApprovalTerminal(approvalID, agentruntime.ExecApprovalResolution{Status: approvalStatusResolved, Decision: decision}), nil
}

// markApprovalTerminal records the first terminal state of a known card and
// emits it once; it returns the terminal state that won.
func (a *activeTurn) markApprovalTerminal(id string, terminal agentruntime.ExecApprovalResolution) agentruntime.ExecApprovalResolution {
	a.approvalMu.Lock()
	state := a.approvals[id]
	if state == nil {
		a.approvalMu.Unlock()
		return agentruntime.ExecApprovalResolution{}
	}
	if state.terminal.Status != "" {
		won := state.terminal
		a.approvalMu.Unlock()
		return won
	}
	state.terminal = terminal
	a.approvalMu.Unlock()
	a.emit(agentruntime.ExecApprovalResolved{ID: id, Status: terminal.Status, Decision: terminal.Decision})
	return terminal
}

// expirePendingApprovals expires every unanswered card; the turn is ending.
func (a *activeTurn) expirePendingApprovals() {
	a.approvalMu.Lock()
	pending := make([]string, 0, len(a.approvals))
	for id, state := range a.approvals {
		if state.terminal.Status == "" {
			pending = append(pending, id)
		}
	}
	a.approvalMu.Unlock()
	slices.Sort(pending)
	for _, id := range pending {
		a.markApprovalTerminal(id, agentruntime.ExecApprovalResolution{Status: approvalStatusExpired})
	}
}

// expirePending converges every unanswered approval and clarify card as the
// turn ends; nothing will answer either kind now.
func (a *activeTurn) expirePending() {
	a.expirePendingApprovals()
	a.expirePendingAsks()
}
