package openclaw

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/openclawgateway"
)

const (
	approvalStatusResolved = "resolved"
	approvalStatusExpired  = "expired"

	approvalReasonAlreadyResolved = "APPROVAL_ALREADY_RESOLVED"
	approvalReasonNotFound        = "APPROVAL_NOT_FOUND"
)

var supportedApprovalDecisions = []string{"allow-once", "allow-always", "deny"}

type gatewayExecApprovalRequest struct {
	Command          string   `json:"command"`
	CommandPreview   string   `json:"commandPreview"`
	AllowedDecisions []string `json:"allowedDecisions"`
	Host             string   `json:"host"`
	NodeID           string   `json:"nodeId"`
	AgentID          string   `json:"agentId"`
	SessionKey       string   `json:"sessionKey"`
	SystemRunPlan    *struct {
		CommandText    string `json:"commandText"`
		CommandPreview string `json:"commandPreview"`
		AgentID        string `json:"agentId"`
		SessionKey     string `json:"sessionKey"`
	} `json:"systemRunPlan"`
}

type gatewayExecApprovalRecord struct {
	ID          string                     `json:"id"`
	Request     gatewayExecApprovalRequest `json:"request"`
	CreatedAtMs int64                      `json:"createdAtMs"`
	ExpiresAtMs int64                      `json:"expiresAtMs"`
}

type approvalState struct {
	request         agentruntime.ExecApprovalRequested
	terminal        agentruntime.ExecApprovalResolution
	terminalEmitted bool
	expiryCancel    context.CancelFunc
}

func listExecApprovals(ctx context.Context, client *openclawgateway.Client) ([]gatewayExecApprovalRecord, error) {
	var records []gatewayExecApprovalRecord
	if err := client.Call(ctx, "exec.approval.list", map[string]any{}, &records); err != nil {
		return nil, err
	}
	return records, nil
}

// approvalRequestForSession 把网关的审批记录翻成 AgentRE 事件。matchesSession 由
// activeTurn 提供:规范化后的 key 还没认领时按后缀认自己的会话。
func approvalRequestForSession(record gatewayExecApprovalRecord, matchesSession func(string) bool) (agentruntime.ExecApprovalRequested, bool) {
	request := record.Request
	commandText := strings.TrimSpace(request.Command)
	commandPreview := strings.TrimSpace(request.CommandPreview)
	agentID := strings.TrimSpace(request.AgentID)
	requestSessionKey := strings.TrimSpace(request.SessionKey)
	if request.SystemRunPlan != nil {
		// For host=node, OpenClaw's systemRunPlan is the canonical execution
		// context. AgentRE reads its display/session fields and never reconstructs
		// or sends the plan back to the Gateway.
		if text := strings.TrimSpace(request.SystemRunPlan.CommandText); text != "" {
			commandText = text
		}
		if preview := strings.TrimSpace(request.SystemRunPlan.CommandPreview); preview != "" {
			commandPreview = preview
		}
		if value := strings.TrimSpace(request.SystemRunPlan.AgentID); value != "" {
			agentID = value
		}
		if value := strings.TrimSpace(request.SystemRunPlan.SessionKey); value != "" {
			requestSessionKey = value
		}
	}
	if requestSessionKey == "" || !matchesSession(requestSessionKey) || strings.TrimSpace(record.ID) == "" {
		return agentruntime.ExecApprovalRequested{}, false
	}
	allowed := make([]string, 0, len(request.AllowedDecisions))
	for _, decision := range request.AllowedDecisions {
		decision = strings.TrimSpace(decision)
		if slices.Contains(supportedApprovalDecisions, decision) && !slices.Contains(allowed, decision) {
			allowed = append(allowed, decision)
		}
	}
	if len(allowed) == 0 {
		return agentruntime.ExecApprovalRequested{}, false
	}
	return agentruntime.ExecApprovalRequested{
		ID: strings.TrimSpace(record.ID), CommandText: commandText, CommandPreview: commandPreview,
		AllowedDecisions: allowed, Host: strings.TrimSpace(request.Host),
		NodeID: strings.TrimSpace(request.NodeID), AgentID: agentID,
		SessionKey: requestSessionKey, CreatedAtMs: record.CreatedAtMs, ExpiresAtMs: record.ExpiresAtMs,
	}, true
}

func (a *activeTurn) handleApprovalRequested(record gatewayExecApprovalRecord) {
	request, ok := approvalRequestForSession(record, a.matchesSession)
	if !ok {
		return
	}
	a.approvalMu.Lock()
	state := a.approvals[request.ID]
	isNew := state == nil
	if isNew {
		state = &approvalState{}
		a.approvals[request.ID] = state
	}
	state.request = request
	terminal := state.terminal.Status != ""
	a.approvalMu.Unlock()
	if terminal {
		return
	}
	if request.ExpiresAtMs > 0 && time.Now().UnixMilli() >= request.ExpiresAtMs {
		a.markApprovalTerminal(request.ID, agentruntime.ExecApprovalResolution{Status: approvalStatusExpired}, "", 0)
		return
	}
	a.scheduleApprovalExpiry(request.ID, request.ExpiresAtMs)
	if isNew {
		a.emit(request)
	}
}

func (a *activeTurn) scheduleApprovalExpiry(id string, expiresAtMs int64) {
	if expiresAtMs <= 0 {
		return
	}
	timerCtx, cancel := context.WithCancel(a.ctx)
	a.approvalMu.Lock()
	state := a.approvals[id]
	if state == nil || state.terminal.Status != "" {
		a.approvalMu.Unlock()
		cancel()
		return
	}
	if state.expiryCancel != nil {
		state.expiryCancel()
	}
	state.expiryCancel = cancel
	a.approvalMu.Unlock()

	delay := time.Until(time.UnixMilli(expiresAtMs))
	if delay < 0 {
		delay = 0
	}
	go func() {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			a.markApprovalTerminal(id, agentruntime.ExecApprovalResolution{Status: approvalStatusExpired}, "", 0)
		case <-timerCtx.Done():
		}
	}()
}

func (a *activeTurn) handleApprovalResolved(raw json.RawMessage) {
	var payload struct {
		ID         string                     `json:"id"`
		Decision   string                     `json:"decision"`
		ResolvedBy string                     `json:"resolvedBy"`
		TS         int64                      `json:"ts"`
		Request    gatewayExecApprovalRequest `json:"request"`
	}
	if json.Unmarshal(raw, &payload) != nil || strings.TrimSpace(payload.ID) == "" {
		return
	}
	a.approvalMu.Lock()
	state := a.approvals[payload.ID]
	a.approvalMu.Unlock()
	if state == nil {
		record := gatewayExecApprovalRecord{ID: payload.ID, Request: payload.Request}
		request, ok := approvalRequestForSession(record, a.matchesSession)
		if !ok {
			return
		}
		a.approvalMu.Lock()
		state = &approvalState{request: request}
		a.approvals[payload.ID] = state
		a.approvalMu.Unlock()
	}
	a.markApprovalTerminal(payload.ID, agentruntime.ExecApprovalResolution{
		Status: approvalStatusResolved, Decision: strings.TrimSpace(payload.Decision),
	}, strings.TrimSpace(payload.ResolvedBy), payload.TS)
}

func (a *activeTurn) reconcileApprovals() {
	records, err := listExecApprovals(a.ctx, a.client)
	if err != nil || a.finished() {
		return
	}
	visible := make(map[string]struct{}, len(records))
	for _, record := range records {
		request, ok := approvalRequestForSession(record, a.matchesSession)
		if !ok {
			continue
		}
		visible[request.ID] = struct{}{}
		a.handleApprovalRequested(record)
	}
	a.approvalMu.Lock()
	missing := make([]string, 0)
	for id, state := range a.approvals {
		if state.terminal.Status == "" {
			if _, ok := visible[id]; !ok {
				missing = append(missing, id)
			}
		}
	}
	a.approvalMu.Unlock()
	for _, id := range missing {
		// 「不在 list 里」不等于「不存在」:真实网关的 exec.approval.list 只返回
		// 本连接创建的(或管理员可见的)审批,看不到是常态。仅凭缺席就判过期,会把
		// 网关那边仍在等决策的审批在 UI 上误标成「已失效」。必须由 exec.approval.get
		// 明确回 APPROVAL_NOT_FOUND 才收敛;其它错误一律保持 pending,交给
		// expiresAtMs 定时器兜底。
		if !a.approvalGoneOnGateway(id) {
			continue
		}
		a.markApprovalTerminal(id, agentruntime.ExecApprovalResolution{Status: approvalStatusExpired}, "", 0)
	}
}

// approvalGoneOnGateway 只在网关明确说「这个审批 ID 不认识/已过期」时返回 true。
func (a *activeTurn) approvalGoneOnGateway(id string) bool {
	var payload json.RawMessage
	err := a.client.Call(a.ctx, "exec.approval.get", map[string]any{"id": id}, &payload)
	if err == nil {
		return false
	}
	var rpcErr *openclawgateway.RPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(rpcErr.Reason), approvalReasonNotFound) {
		return true
	}
	return strings.Contains(strings.ToLower(rpcErr.Message), "unknown or expired approval")
}

func (a *activeTurn) resolveApproval(ctx context.Context, approvalID, decision string) (agentruntime.ExecApprovalResolution, error) {
	approvalID = strings.TrimSpace(approvalID)
	decision = strings.TrimSpace(decision)
	if approvalID == "" || decision == "" {
		return agentruntime.ExecApprovalResolution{}, fmt.Errorf("openclaw exec approval: id and decision are required")
	}
	a.approvalResolveMu.Lock()
	defer a.approvalResolveMu.Unlock()

	a.approvalMu.Lock()
	state := a.approvals[approvalID]
	if state == nil {
		a.approvalMu.Unlock()
		return agentruntime.ExecApprovalResolution{}, fmt.Errorf("openclaw exec approval: approval not found")
	}
	if state.terminal.Status != "" {
		terminal := state.terminal
		a.approvalMu.Unlock()
		return terminal, nil
	}
	request := state.request
	a.approvalMu.Unlock()
	if !slices.Contains(request.AllowedDecisions, decision) {
		return agentruntime.ExecApprovalResolution{}, fmt.Errorf("openclaw exec approval: decision %q is not allowed", decision)
	}
	if request.ExpiresAtMs > 0 && time.Now().UnixMilli() >= request.ExpiresAtMs {
		terminal := agentruntime.ExecApprovalResolution{Status: approvalStatusExpired}
		a.markApprovalTerminal(approvalID, terminal, "", 0)
		return terminal, nil
	}

	var response struct {
		OK bool `json:"ok"`
	}
	err := a.client.Call(ctx, "exec.approval.resolve", map[string]any{
		"id": approvalID, "decision": decision,
	}, &response)
	if err != nil {
		var rpcErr *openclawgateway.RPCError
		if errors.As(err, &rpcErr) {
			switch rpcErr.Reason {
			case approvalReasonAlreadyResolved:
				terminal := agentruntime.ExecApprovalResolution{Status: approvalStatusResolved}
				a.markApprovalTerminal(approvalID, terminal, "", 0)
				return terminal, nil
			case approvalReasonNotFound:
				terminal := agentruntime.ExecApprovalResolution{Status: approvalStatusExpired}
				a.markApprovalTerminal(approvalID, terminal, "", 0)
				return terminal, nil
			}
		}
		return agentruntime.ExecApprovalResolution{}, err
	}
	terminal := agentruntime.ExecApprovalResolution{Status: approvalStatusResolved, Decision: decision}
	a.markApprovalTerminal(approvalID, terminal, "", time.Now().UnixMilli())
	return terminal, nil
}

func (a *activeTurn) markApprovalTerminal(id string, terminal agentruntime.ExecApprovalResolution, resolvedBy string, resolvedAtMs int64) {
	a.approvalMu.Lock()
	state := a.approvals[id]
	if state == nil {
		state = &approvalState{}
		a.approvals[id] = state
	}
	if state.terminal.Status == "" {
		state.terminal = terminal
	}
	if state.expiryCancel != nil {
		state.expiryCancel()
		state.expiryCancel = nil
	}
	terminal = state.terminal
	if state.terminalEmitted {
		a.approvalMu.Unlock()
		return
	}
	state.terminalEmitted = true
	a.approvalMu.Unlock()
	a.emit(agentruntime.ExecApprovalResolved{
		ID: id, Status: terminal.Status, Decision: terminal.Decision,
		ResolvedBy: resolvedBy, ResolvedAtMs: resolvedAtMs,
	})
}

func (a *activeTurn) expirePendingApprovals() {
	a.approvalMu.Lock()
	ids := make([]string, 0)
	for id, state := range a.approvals {
		if state.terminal.Status == "" {
			ids = append(ids, id)
		}
	}
	a.approvalMu.Unlock()
	for _, id := range ids {
		a.markApprovalTerminal(id, agentruntime.ExecApprovalResolution{Status: approvalStatusExpired}, "", 0)
	}
}
