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

	execApprovalGetMethod     = "exec.approval.get"
	execApprovalResolveMethod = "exec.approval.resolve"

	pluginApprovalListMethod    = "plugin.approval.list"
	pluginApprovalResolveMethod = "plugin.approval.resolve"

	systemAgentApprovalListMethod = "openclaw.approval.list"

	// Gateway has no per-kind resolve/get RPC for system-agent approvals; the
	// kind-agnostic approval.resolve (whose closed schema requires kind) and
	// approval.get look the record up by ID across all three managers
	// (exec/plugin/system-agent). Plugin uses
	// its own plugin.approval.resolve for symmetry with exec, but could equally
	// use the generic one.
	genericApprovalResolveMethod = "approval.resolve"
	genericApprovalGetMethod     = "approval.get"
)

// OpenClaw has no per-session grant, so allow-session is never offered here.
var supportedApprovalDecisions = []string{
	agentruntime.ApprovalDecisionAllowOnce, agentruntime.ApprovalDecisionAllowAlways, agentruntime.ApprovalDecisionDeny,
}

type gatewayExecApprovalRequest struct {
	Command          string                `json:"command"`
	CommandPreview   string                `json:"commandPreview"`
	AllowedDecisions []string              `json:"allowedDecisions"`
	Host             string                `json:"host"`
	NodeID           string                `json:"nodeId"`
	AgentID          string                `json:"agentId"`
	SessionKey       string                `json:"sessionKey"`
	WarningText      string                `json:"warningText"`
	Scope            *gatewayApprovalScope `json:"scope"`
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

// gatewayPluginApprovalRequest / gatewaySystemAgentApprovalRequest are the
// plugin.approval.request / openclaw.approval.request payload shapes
// (plugin-approval-Od69bXoa.mjs, system-agent-Dch3-EM8.mjs in OpenClaw
// 2026.9.5). Unlike exec, neither carries a systemRunPlan or host/nodeId.
type gatewayPluginApprovalRequest struct {
	PluginID         string                `json:"pluginId"`
	Scope            *gatewayApprovalScope `json:"scope"`
	ToolName         string                `json:"toolName"`
	Title            string                `json:"title"`
	Description      string                `json:"description"`
	AllowedDecisions []string              `json:"allowedDecisions"`
	AgentID          string                `json:"agentId"`
	SessionKey       string                `json:"sessionKey"`
}

type gatewayPluginApprovalRecord struct {
	ID          string                       `json:"id"`
	Request     gatewayPluginApprovalRequest `json:"request"`
	CreatedAtMs int64                        `json:"createdAtMs"`
	ExpiresAtMs int64                        `json:"expiresAtMs"`
}

type gatewaySystemAgentApprovalRequest struct {
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	AllowedDecisions []string `json:"allowedDecisions"`
	AgentID          string   `json:"agentId"`
	SessionKey       string   `json:"sessionKey"`
}

type gatewaySystemAgentApprovalRecord struct {
	ID          string                            `json:"id"`
	Request     gatewaySystemAgentApprovalRequest `json:"request"`
	CreatedAtMs int64                             `json:"createdAtMs"`
	ExpiresAtMs int64                             `json:"expiresAtMs"`
}

// gatewayApprovalScope 是网关 ApprovalScope(schema/approvals.ts)的并集形态:
// 审批发起方声明的影响范围,kind 决定哪几格有值。exec 与 plugin 请求可带,
// system-agent 请求没有。
type gatewayApprovalScope struct {
	Kind           string   `json:"kind"`
	Target         string   `json:"target"`
	RecipientCount int      `json:"recipientCount"`
	Recipients     []string `json:"recipients"`
	Amount         string   `json:"amount"`
	Currency       string   `json:"currency"`
	Visibility     string   `json:"visibility"`
	Automation     string   `json:"automation"`
	Command        string   `json:"command"`
}

// applyApprovalScope 把网关声明的影响范围落到审批卡的动作类别及其范围字段;
// 不认识的 kind 不猜。standing-grant 的命令只在卡片还没有命令时补上。
func applyApprovalScope(request *agentruntime.ExecApprovalRequested, scope *gatewayApprovalScope) {
	if scope == nil {
		return
	}
	target := strings.TrimSpace(scope.Target)
	switch strings.TrimSpace(scope.Kind) {
	case "message-send":
		request.ActionCategory = agentruntime.ApprovalActionMessage
		var targets []string
		for _, value := range append([]string{target}, scope.Recipients...) {
			if value = strings.TrimSpace(value); value != "" && !slices.Contains(targets, value) {
				targets = append(targets, value)
			}
		}
		request.MessageTargets = targets
		request.RecipientCount = scope.RecipientCount
	case "payment":
		request.ActionCategory = agentruntime.ApprovalActionPayment
		request.PaymentAmount = strings.TrimSpace(strings.TrimSpace(scope.Amount) + " " + strings.TrimSpace(scope.Currency))
		request.PaymentPayee = target
	case "external-post":
		request.ActionCategory = agentruntime.ApprovalActionPublish
		request.PublishTarget = target
		request.PublishVisibility = strings.TrimSpace(scope.Visibility)
	case "standing-grant":
		request.ActionCategory = agentruntime.ApprovalActionAutomation
		request.AutomationName = strings.TrimSpace(scope.Automation)
		if request.CommandText == "" {
			request.CommandText = strings.TrimSpace(scope.Command)
		}
	}
}

// filterSupportedApprovalDecisions 只保留 AgentRE 认识、且未重复的决定 —— OpenClaw
// 没有 Hermes 的会话级授权,allow-session 永远不会出现在这里。
func filterSupportedApprovalDecisions(decisions []string) []string {
	allowed := make([]string, 0, len(decisions))
	for _, decision := range decisions {
		decision = strings.TrimSpace(decision)
		if slices.Contains(supportedApprovalDecisions, decision) && !slices.Contains(allowed, decision) {
			allowed = append(allowed, decision)
		}
	}
	return allowed
}

// pluginApprovalRequestForSession 与 approvalRequestForSession 对应,把
// plugin.approval.* 的记录翻成 AgentRE 事件。
func pluginApprovalRequestForSession(record gatewayPluginApprovalRecord, matchesSession func(string) bool) (agentruntime.ExecApprovalRequested, bool) {
	request := record.Request
	sessionKey := strings.TrimSpace(request.SessionKey)
	if sessionKey == "" || !matchesSession(sessionKey) || strings.TrimSpace(record.ID) == "" {
		return agentruntime.ExecApprovalRequested{}, false
	}
	allowed := filterSupportedApprovalDecisions(request.AllowedDecisions)
	if len(allowed) == 0 {
		return agentruntime.ExecApprovalRequested{}, false
	}
	description := strings.TrimSpace(request.Description)
	if description == "" {
		description = strings.TrimSpace(request.Title)
	}
	approval := agentruntime.ExecApprovalRequested{
		ID: strings.TrimSpace(record.ID), ApprovalKind: agentruntime.ApprovalKindPlugin,
		PluginName: strings.TrimSpace(request.PluginID), ToolName: strings.TrimSpace(request.ToolName),
		Description: description, AllowedDecisions: allowed, AgentID: strings.TrimSpace(request.AgentID),
		SessionKey: sessionKey, CreatedAtMs: record.CreatedAtMs, ExpiresAtMs: record.ExpiresAtMs,
	}
	applyApprovalScope(&approval, request.Scope)
	return approval, true
}

// systemAgentApprovalRequestForSession 与 approvalRequestForSession 对应,把
// openclaw.approval.*(Gateway 侧持久变更,如配置写入/重启)的记录翻成 AgentRE 事件。
// 上游 system-agent 请求不带 scope(ApprovalScope 只挂在 exec / plugin 上),
// Description 是唯一已脱敏内容。
func systemAgentApprovalRequestForSession(record gatewaySystemAgentApprovalRecord, matchesSession func(string) bool) (agentruntime.ExecApprovalRequested, bool) {
	request := record.Request
	sessionKey := strings.TrimSpace(request.SessionKey)
	if sessionKey == "" || !matchesSession(sessionKey) || strings.TrimSpace(record.ID) == "" {
		return agentruntime.ExecApprovalRequested{}, false
	}
	allowed := filterSupportedApprovalDecisions(request.AllowedDecisions)
	if len(allowed) == 0 {
		return agentruntime.ExecApprovalRequested{}, false
	}
	description := strings.TrimSpace(request.Description)
	if description == "" {
		description = strings.TrimSpace(request.Title)
	}
	return agentruntime.ExecApprovalRequested{
		ID: strings.TrimSpace(record.ID), ApprovalKind: agentruntime.ApprovalKindSystemAgent,
		Description: description, AllowedDecisions: allowed, AgentID: strings.TrimSpace(request.AgentID),
		SessionKey: sessionKey, CreatedAtMs: record.CreatedAtMs, ExpiresAtMs: record.ExpiresAtMs,
	}, true
}

// decodeApprovalListItem 按 kind 译一条 *.approval.list 的记录(完整 record:
// id/request/createdAtMs/expiresAtMs 都在同一个 JSON 对象里)。
func decodeApprovalListItem(kind string, raw json.RawMessage, matchesSession func(string) bool) (agentruntime.ExecApprovalRequested, bool) {
	switch kind {
	case agentruntime.ApprovalKindPlugin:
		var record gatewayPluginApprovalRecord
		if json.Unmarshal(raw, &record) != nil {
			return agentruntime.ExecApprovalRequested{}, false
		}
		return pluginApprovalRequestForSession(record, matchesSession)
	case agentruntime.ApprovalKindSystemAgent:
		var record gatewaySystemAgentApprovalRecord
		if json.Unmarshal(raw, &record) != nil {
			return agentruntime.ExecApprovalRequested{}, false
		}
		return systemAgentApprovalRequestForSession(record, matchesSession)
	default:
		var record gatewayExecApprovalRecord
		if json.Unmarshal(raw, &record) != nil {
			return agentruntime.ExecApprovalRequested{}, false
		}
		return approvalRequestForSession(record, matchesSession)
	}
}

// decodeApprovalRequestForSession 按 kind 译 *.approval.resolved 事件里的
// request 子对象(不含 id/createdAtMs/expiresAtMs —— resolved 事件的 id 在 payload
// 顶层单独传,createdAtMs/expiresAtMs 那份帧根本不带,和 exec 原有的重建逻辑一致)。
func decodeApprovalRequestForSession(
	kind, id string, rawRequest json.RawMessage, matchesSession func(string) bool,
) (agentruntime.ExecApprovalRequested, bool) {
	switch kind {
	case agentruntime.ApprovalKindPlugin:
		var request gatewayPluginApprovalRequest
		if json.Unmarshal(rawRequest, &request) != nil {
			return agentruntime.ExecApprovalRequested{}, false
		}
		return pluginApprovalRequestForSession(gatewayPluginApprovalRecord{ID: id, Request: request}, matchesSession)
	case agentruntime.ApprovalKindSystemAgent:
		var request gatewaySystemAgentApprovalRequest
		if json.Unmarshal(rawRequest, &request) != nil {
			return agentruntime.ExecApprovalRequested{}, false
		}
		return systemAgentApprovalRequestForSession(gatewaySystemAgentApprovalRecord{ID: id, Request: request}, matchesSession)
	default:
		var request gatewayExecApprovalRequest
		if json.Unmarshal(rawRequest, &request) != nil {
			return agentruntime.ExecApprovalRequested{}, false
		}
		return approvalRequestForSession(gatewayExecApprovalRecord{ID: id, Request: request}, matchesSession)
	}
}

// approvalResolveMethod 挑决 approval 的 resolve RPC:exec/plugin 各有专属方法,
// system-agent 网关只提供不区分类别的 approval.resolve(按 ID 自己认出所属 kind)。
func approvalResolveMethod(kind string) string {
	switch kind {
	case agentruntime.ApprovalKindPlugin:
		return pluginApprovalResolveMethod
	case agentruntime.ApprovalKindSystemAgent:
		return genericApprovalResolveMethod
	default:
		return execApprovalResolveMethod
	}
}

// approvalResolveParams 是 resolve RPC 的参数:exec/plugin 的专属方法只收
// {id, decision};不区分类别的 approval.resolve 的 closed schema 还要求 kind。
func approvalResolveParams(kind, id, decision string) map[string]any {
	params := map[string]any{"id": id, "decision": decision}
	if approvalResolveMethod(kind) == genericApprovalResolveMethod {
		params["kind"] = kind
	}
	return params
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
	allowed := filterSupportedApprovalDecisions(request.AllowedDecisions)
	if len(allowed) == 0 {
		return agentruntime.ExecApprovalRequested{}, false
	}
	approval := agentruntime.ExecApprovalRequested{
		ID: strings.TrimSpace(record.ID), ApprovalKind: agentruntime.ApprovalKindExec,
		CommandText: commandText, CommandPreview: commandPreview,
		AllowedDecisions: allowed, Host: strings.TrimSpace(request.Host),
		NodeID: strings.TrimSpace(request.NodeID), AgentID: agentID,
		SessionKey: requestSessionKey, CreatedAtMs: record.CreatedAtMs, ExpiresAtMs: record.ExpiresAtMs,
	}
	if warning := strings.TrimSpace(request.WarningText); warning != "" {
		approval.Warnings = []string{warning}
	}
	applyApprovalScope(&approval, request.Scope)
	return approval, true
}

func (a *activeTurn) handleApprovalRequested(record gatewayExecApprovalRecord) {
	request, ok := approvalRequestForSession(record, a.matchesSession)
	if !ok {
		return
	}
	a.handleApprovalRequestedEvent(request)
}

// handleApprovalRequestedEvent 是三类审批共用的落地点:exec 由
// handleApprovalRequested 解出 request 后转到这里,plugin/system-agent 的
// requested 事件与重连对账直接调用它。
func (a *activeTurn) handleApprovalRequestedEvent(request agentruntime.ExecApprovalRequested) {
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

func (a *activeTurn) handleApprovalResolved(kind string, raw json.RawMessage) {
	var payload struct {
		ID         string          `json:"id"`
		Decision   string          `json:"decision"`
		ResolvedBy string          `json:"resolvedBy"`
		TS         int64           `json:"ts"`
		Request    json.RawMessage `json:"request"`
	}
	if json.Unmarshal(raw, &payload) != nil || strings.TrimSpace(payload.ID) == "" {
		return
	}
	a.approvalMu.Lock()
	state := a.approvals[payload.ID]
	a.approvalMu.Unlock()
	if state == nil {
		request, ok := decodeApprovalRequestForSession(kind, payload.ID, payload.Request, a.matchesSession)
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
		a.handleApprovalRequestedEvent(request)
	}
	// plugin/system-agent 对账只在网关 hello 广播了对应 list 方法时才问 —— 老网关
	// 没有这两个方法,盲问会把方法名当成硬错误,反而搅坏本来能用的 exec 对账。
	if a.pluginApprovalsSupported {
		a.reconcileApprovalKindList(agentruntime.ApprovalKindPlugin, pluginApprovalListMethod, visible)
	}
	if a.systemAgentApprovalsSupported {
		a.reconcileApprovalKindList(agentruntime.ApprovalKindSystemAgent, systemAgentApprovalListMethod, visible)
	}
	if a.finished() {
		return
	}
	a.approvalMu.Lock()
	missing := make([]string, 0)
	pendingKinds := make(map[string]string, len(a.approvals))
	for id, state := range a.approvals {
		if state.terminal.Status == "" {
			if _, ok := visible[id]; !ok {
				missing = append(missing, id)
				pendingKinds[id] = state.request.ApprovalKind
			}
		}
	}
	a.approvalMu.Unlock()
	for _, id := range missing {
		// 「不在 list 里」不等于「不存在」:真实网关的 *.approval.list 只返回
		// 本连接创建的(或管理员可见的)审批,看不到是常态。仅凭缺席就判过期,会把
		// 网关那边仍在等决策的审批在 UI 上误标成「已失效」。必须由 *.approval.get
		// 明确回 APPROVAL_NOT_FOUND 才收敛;其它错误一律保持 pending,交给
		// expiresAtMs 定时器兜底。
		if !a.approvalGoneOnGateway(id, pendingKinds[id]) {
			continue
		}
		a.markApprovalTerminal(id, agentruntime.ExecApprovalResolution{Status: approvalStatusExpired}, "", 0)
	}
}

// reconcileApprovalKindList 补 plugin/system-agent 的重连对账:两者都没有
// exec.approval.list 那样按类型专属的 record 结构体入口,直接按泛化的
// ExecApprovalRequested 落地。
func (a *activeTurn) reconcileApprovalKindList(kind, method string, visible map[string]struct{}) {
	var raws []json.RawMessage
	if err := a.client.Call(a.ctx, method, map[string]any{}, &raws); err != nil {
		return
	}
	for _, raw := range raws {
		request, ok := decodeApprovalListItem(kind, raw, a.matchesSession)
		if !ok {
			continue
		}
		visible[request.ID] = struct{}{}
		a.handleApprovalRequestedEvent(request)
	}
}

// approvalGoneOnGateway 只在网关明确说「这个审批 ID 不认识/已过期」时返回 true。
// exec 走它自己专属的 exec.approval.get;plugin/system-agent 没有专属 get,走
// 不区分类别的 approval.get(网关按 ID 自己认出所属 kind)。
func (a *activeTurn) approvalGoneOnGateway(id, kind string) bool {
	method := execApprovalGetMethod
	if kind != "" && kind != agentruntime.ApprovalKindExec {
		method = genericApprovalGetMethod
	}
	var payload json.RawMessage
	err := a.client.Call(a.ctx, method, map[string]any{"id": id}, &payload)
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
	err := a.client.Call(ctx, approvalResolveMethod(request.ApprovalKind), approvalResolveParams(request.ApprovalKind, approvalID, decision), &response)
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
