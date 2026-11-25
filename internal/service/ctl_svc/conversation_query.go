package ctl_svc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// localDeviceName 是本机桌面在 agrctl 输出里的设备名。
const localDeviceName = "this desktop"

// listStatuses 是 --status 接受的值(与会话运行态同值)。
var listStatuses = []chat_svc.CtlConversationStatus{
	chat_svc.CtlStatusRunning, chat_svc.CtlStatusIdle, chat_svc.CtlStatusWaiting, chat_svc.CtlStatusFailed,
}

func (h *ctlHandler) queryReady(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "requires POST")
		return false
	}
	if h.agents == nil || h.chat == nil || h.conversations == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return false
	}
	return true
}

func rfc3339Ms(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

// ---- list ----

type conversationsRequest struct {
	Agent     string `json:"agent,omitempty"`
	AgentID   int64  `json:"agentId,omitempty"`
	ProjectID int64  `json:"projectId,omitempty"`
	Device    string `json:"device,omitempty"`
	Status    string `json:"status,omitempty"`
	// Mine 只列调用方自己发起的会话:桌面不记发起方,拒绝而不是假装过滤。
	Mine  bool `json:"mine,omitempty"`
	Limit int  `json:"limit,omitempty"`
}

// sessionListItem 是 list sessions 的一行(JSON 字段名稳定)。
type sessionListItem struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Agent          string `json:"agent"`
	Device         string `json:"device"`
	Status         string `json:"status"`
	LastActivityAt string `json:"lastActivityAt,omitempty"`
}

func deviceOrLocal(d string) string {
	if d == "" {
		return localDeviceName
	}
	return d
}

func (h *ctlHandler) serveConversations(w http.ResponseWriter, r *http.Request) {
	if !h.queryReady(w, r) {
		return
	}
	var req conversationsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.Mine {
		writeErr(w, http.StatusUnprocessableEntity, "--mine needs an account (agrctl login): the desktop does not record who started a session")
		return
	}
	status := chat_svc.CtlConversationStatus(strings.TrimSpace(req.Status))
	if status != "" && !slices.Contains(listStatuses, status) {
		writeErr(w, http.StatusUnprocessableEntity, fmt.Sprintf("--status %q is not supported (one of: running, idle, waiting, failed)", req.Status))
		return
	}
	filter := chat_svc.CtlListFilter{ProjectID: req.ProjectID, Device: strings.TrimSpace(req.Device), Status: status, Limit: req.Limit}
	if strings.TrimSpace(req.Agent) != "" || req.AgentID > 0 {
		a, err := h.resolveAgent(r, req.Agent, req.AgentID)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		filter.AgentID = a.ID
	}
	list, err := h.conversations.ListCtlConversations(r.Context(), filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]sessionListItem, 0, len(list))
	for _, c := range list {
		out = append(out, sessionListItem{
			ID: c.ConversationID, Title: c.Title, Agent: c.AgentName, Device: deviceOrLocal(c.Device),
			Status: string(c.Status), LastActivityAt: rfc3339Ms(c.LastMessageAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

// ---- get ----

type conversationRequest struct {
	ConversationID string `json:"conversationId"`
}

type usageDTO struct {
	Input  int `json:"input"`
	Output int `json:"output"`
}

// sessionDetail 是 get session 的响应;拿不到的字段省略,不编造。
type sessionDetail struct {
	ID             string    `json:"id"`
	Title          string    `json:"title"`
	Agent          string    `json:"agent"`
	Project        string    `json:"project,omitempty"`
	Device         string    `json:"device"`
	Backend        string    `json:"backend,omitempty"`
	Model          string    `json:"model,omitempty"`
	Status         string    `json:"status"`
	CreatedAt      string    `json:"createdAt,omitempty"`
	LastActivityAt string    `json:"lastActivityAt,omitempty"`
	Usage          *usageDTO `json:"usage,omitempty"`
	Pending        int       `json:"pending"`
	LastText       string    `json:"lastText,omitempty"`
}

func (h *ctlHandler) serveConversation(w http.ResponseWriter, r *http.Request) {
	if !h.queryReady(w, r) {
		return
	}
	var req conversationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	c, ok := h.lookupConversation(w, r, req.ConversationID)
	if !ok {
		return
	}
	d, err := h.conversations.DetailCtlConversation(r.Context(), c.SessionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if d == nil {
		writeErr(w, http.StatusNotFound, errSessionNotVisible)
		return
	}
	pending, err := h.pendingOf(r.Context(), c.SessionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := sessionDetail{
		ID: d.ConversationID, Title: d.Title, Agent: d.AgentName, Device: deviceOrLocal(d.Device),
		Backend: d.Backend, Model: d.ModelKey, Status: string(d.Status),
		CreatedAt: rfc3339Ms(d.CreatedAt), LastActivityAt: rfc3339Ms(d.LastMessageAt),
		Pending: len(pending.ToolPermissions) + len(pending.AskUserQuestions) + len(pending.Approvals), LastText: d.LastText,
	}
	if d.InputTokens > 0 || d.OutputTokens > 0 {
		out.Usage = &usageDTO{Input: d.InputTokens, Output: d.OutputTokens}
	}
	if d.ProjectID > 0 && h.projects != nil {
		if list, err := h.projects.List(r.Context()); err == nil {
			for _, p := range list {
				if p.ID == d.ProjectID {
					out.Project = p.Name
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- messages ----

type messagesRequest struct {
	ConversationID string `json:"conversationId"`
	Last           int    `json:"last,omitempty"`
	// Before 是上一页给出的不透明游标(现在是十进制 seq)。
	Before string `json:"before,omitempty"`
	Full   bool   `json:"full,omitempty"`
}

type messageDTO struct {
	Role   string                     `json:"role"`
	At     string                     `json:"at,omitempty"`
	Text   string                     `json:"text"`
	Blocks []chat_svc.CtlMessageBlock `json:"blocks,omitempty"`
}

func (h *ctlHandler) serveMessages(w http.ResponseWriter, r *http.Request) {
	if !h.queryReady(w, r) {
		return
	}
	var req messagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	q := chat_svc.CtlMessagesQuery{Last: req.Last, Full: req.Full}
	if req.Last < 0 {
		writeErr(w, http.StatusBadRequest, "last must be positive")
		return
	}
	if before := strings.TrimSpace(req.Before); before != "" {
		seq, err := strconv.Atoi(before)
		if err != nil || seq <= 0 {
			writeErr(w, http.StatusBadRequest, "invalid --before cursor "+strconv.Quote(before))
			return
		}
		q.BeforeSeq = seq
	}
	c, ok := h.lookupConversation(w, r, req.ConversationID)
	if !ok {
		return
	}
	page, err := h.conversations.ListCtlMessages(r.Context(), c.SessionID, q)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	msgs := make([]messageDTO, 0, len(page.Messages))
	for _, m := range page.Messages {
		msgs = append(msgs, messageDTO{Role: m.Role, At: rfc3339Ms(m.At), Text: m.Text, Blocks: m.Blocks})
	}
	out := map[string]any{"session": c.ConversationID, "messages": msgs, "hasMore": page.HasMore}
	if page.HasMore && page.BeforeSeq > 0 {
		out["before"] = strconv.Itoa(page.BeforeSeq)
	}
	writeJSON(w, http.StatusOK, out)
}

// ---- pending ----

type pendingRequest struct {
	ConversationID string `json:"conversationId,omitempty"`
}

type pendingOptionDTO struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type pendingQuestionDTO struct {
	Header      string             `json:"header,omitempty"`
	Question    string             `json:"question"`
	MultiSelect bool               `json:"multiSelect,omitempty"`
	Options     []pendingOptionDTO `json:"options,omitempty"`
}

// pendingItemDTO 是一条待处理项:kind 为 question(提问卡)、tool_permission(工具权限 /
// 计划审批 / 执行审批)或 approval(会话内 agent 调 agrctl 写资源或组织操作出的审批卡)。
// 外部调用的写审批仍由桌面弹窗的人决定,不在这里。
type pendingItemDTO struct {
	ID        string               `json:"id"`
	Session   string               `json:"session"`
	Kind      string               `json:"kind"`
	Tool      string               `json:"tool,omitempty"`
	Input     json.RawMessage      `json:"input,omitempty"`
	Questions []pendingQuestionDTO `json:"questions,omitempty"`
	// Title / Summary 只有审批卡带:工具名与变更清单(与账号执行者同形)。
	Title   string          `json:"title,omitempty"`
	Summary json.RawMessage `json:"summary,omitempty"`
}

const (
	pendingKindQuestion       = "question"
	pendingKindToolPermission = "tool_permission"
	pendingKindApproval       = "approval"
)

// pendingSnapshot 是一条会话此刻的全部待处理项:运行时的工具权限与提问,加上这一轮上挂着的
// 审批卡(wait 的 pending 与账号执行者同形)。
type pendingSnapshot struct {
	wire.SessionPendingWaitersResult
	Approvals []chat_svc.CtlPendingApproval `json:"approvals,omitempty"`
}

func (h *ctlHandler) pendingOf(ctx context.Context, sessionID int64) (pendingSnapshot, error) {
	p, err := h.conversations.PendingCtlWaiters(ctx, sessionID)
	if err != nil {
		return pendingSnapshot{}, err
	}
	return pendingSnapshot{SessionPendingWaitersResult: p, Approvals: h.conversations.PendingCtlApprovals(ctx, sessionID)}, nil
}

func pendingItems(session string, p pendingSnapshot) []pendingItemDTO {
	out := make([]pendingItemDTO, 0, len(p.ToolPermissions)+len(p.AskUserQuestions)+len(p.Approvals))
	for _, t := range p.ToolPermissions {
		it := pendingItemDTO{ID: t.RequestID, Session: session, Kind: pendingKindToolPermission, Tool: t.ToolName}
		if json.Valid(t.Input) {
			it.Input = json.RawMessage(t.Input)
		}
		out = append(out, it)
	}
	for _, q := range p.AskUserQuestions {
		it := pendingItemDTO{ID: q.RequestID, Session: session, Kind: pendingKindQuestion}
		for _, aq := range q.Questions {
			dto := pendingQuestionDTO{Header: aq.Header, Question: aq.Question, MultiSelect: aq.MultiSelect}
			for _, o := range aq.Options {
				dto.Options = append(dto.Options, pendingOptionDTO{Label: o.Label, Description: o.Description})
			}
			it.Questions = append(it.Questions, dto)
		}
		out = append(out, it)
	}
	for _, a := range p.Approvals {
		it := pendingItemDTO{ID: a.RequestID, Session: session, Kind: pendingKindApproval, Title: a.Title}
		if json.Valid(a.Summary) {
			it.Summary = a.Summary
		}
		out = append(out, it)
	}
	return out
}

func (h *ctlHandler) servePending(w http.ResponseWriter, r *http.Request) {
	if !h.queryReady(w, r) {
		return
	}
	var req pendingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	ctx := r.Context()
	items := []pendingItemDTO{}
	if strings.TrimSpace(req.ConversationID) != "" {
		c, ok := h.lookupConversation(w, r, req.ConversationID)
		if !ok {
			return
		}
		p, err := h.pendingOf(ctx, c.SessionID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = append(items, pendingItems(c.ConversationID, p)...)
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	waiting, err := h.conversations.ListCtlConversations(ctx, chat_svc.CtlListFilter{Status: chat_svc.CtlStatusWaiting, Limit: 500})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, c := range waiting {
		p, err := h.pendingOf(ctx, c.SessionID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		items = append(items, pendingItems(c.ConversationID, p)...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// ---- answer ----

type answerRequest struct {
	ConversationID string `json:"conversationId"`
	RequestID      string `json:"requestId"`
	// Options / Text 回答提问卡:选项文本或 1 起的序号(多选可给多个),或自由文本。
	Options []string `json:"options,omitempty"`
	Text    string   `json:"text,omitempty"`
	// Decision 批准或拒绝工具权限:allow / deny;Message 是拒绝理由。
	Decision string `json:"decision,omitempty"`
	Message  string `json:"message,omitempty"`
}

const errPendingGone = "the pending item no longer exists (already answered elsewhere or timed out)"

func (h *ctlHandler) serveAnswer(w http.ResponseWriter, r *http.Request) {
	if !h.queryReady(w, r) {
		return
	}
	var req answerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.RequestID) == "" {
		writeErr(w, http.StatusBadRequest, "requestId is required")
		return
	}
	c, ok := h.lookupConversation(w, r, req.ConversationID)
	if !ok {
		return
	}
	ctx := r.Context()
	pending, err := h.pendingOf(ctx, c.SessionID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, q := range pending.AskUserQuestions {
		if q.RequestID != req.RequestID {
			continue
		}
		answers, msg := questionAnswers(q, req)
		if msg != "" {
			writeErr(w, http.StatusUnprocessableEntity, msg)
			return
		}
		if err := h.conversations.AnswerCtlQuestion(ctx, c.SessionID, q.RequestID, answers); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"answered": true, "kind": pendingKindQuestion})
		return
	}
	for _, t := range pending.ToolPermissions {
		if t.RequestID != req.RequestID {
			continue
		}
		if len(req.Options) > 0 || req.Text != "" {
			writeErr(w, http.StatusUnprocessableEntity, "a tool permission is answered with --allow or --deny, not --option / --text")
			return
		}
		if req.Decision != "allow" && req.Decision != "deny" {
			writeErr(w, http.StatusUnprocessableEntity, "a tool permission needs --allow or --deny")
			return
		}
		if _, err := h.chat.AnswerToolPermission(ctx, &chat_svc.AnswerToolPermissionRequest{
			SessionID: c.SessionID, RequestID: t.RequestID, Allow: req.Decision == "allow", DenyReason: req.Message,
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"answered": true, "kind": pendingKindToolPermission})
		return
	}
	for _, a := range pending.Approvals {
		if a.RequestID != req.RequestID {
			continue
		}
		if len(req.Options) > 0 || req.Text != "" {
			writeErr(w, http.StatusUnprocessableEntity, "an approval card is answered with --allow or --deny, not --option / --text")
			return
		}
		if req.Decision != "allow" && req.Decision != "deny" {
			writeErr(w, http.StatusUnprocessableEntity, "an approval card needs --allow or --deny")
			return
		}
		if err := h.conversations.AnswerToolApproval(ctx, c.SessionID, a.RequestID, req.Decision == "allow"); err != nil {
			// 列出之后、作答之前被别处答掉或超时了。
			writeErr(w, http.StatusNotFound, errPendingGone)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"answered": true, "kind": pendingKindApproval})
		return
	}
	writeErr(w, http.StatusNotFound, errPendingGone)
}

// questionAnswers 把 --option / --text 映射成对这张提问卡的答案;不合法时交回用法错误文案。
// 一张卡有多道题时无法用一个 --option / --text 说清是哪一道,拒绝并指引到桌面端作答。
func questionAnswers(q agentruntime.PendingAskUserQuestion, req answerRequest) ([]blocks.AskAnswerDTO, string) {
	if req.Decision != "" {
		return nil, "a question is answered with --option or --text, not --allow / --deny"
	}
	if len(req.Options) > 0 && req.Text != "" {
		return nil, "give --option or --text, not both"
	}
	if len(req.Options) == 0 && req.Text == "" {
		return nil, "a question needs --option <text|number> or --text <answer>"
	}
	if len(q.Questions) != 1 {
		return nil, fmt.Sprintf("this card asks %d questions; answer it in the desktop app", len(q.Questions))
	}
	one := q.Questions[0]
	if req.Text != "" {
		if one.DisallowOther {
			return nil, "this question only accepts its listed options (--option)"
		}
		return []blocks.AskAnswerDTO{{QuestionIndex: 0, Labels: []string{agentruntime.OtherAnswerLabel}, OtherText: req.Text}}, ""
	}
	if len(req.Options) > 1 && !one.MultiSelect {
		return nil, "this question takes a single option"
	}
	labels := make([]string, 0, len(req.Options))
	for _, o := range req.Options {
		label, ok := matchOption(one.Options, o)
		if !ok {
			names := make([]string, 0, len(one.Options))
			for _, opt := range one.Options {
				names = append(names, opt.Label)
			}
			return nil, fmt.Sprintf("--option %q is not one of: %s", o, strings.Join(names, ", "))
		}
		labels = append(labels, label)
	}
	return []blocks.AskAnswerDTO{{QuestionIndex: 0, Labels: labels}}, ""
}

// matchOption 先按选项文本精确匹配,再按 1 起的序号。
func matchOption(options []agentruntime.AskOption, v string) (string, bool) {
	v = strings.TrimSpace(v)
	for _, o := range options {
		if o.Label == v {
			return o.Label, true
		}
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= len(options) {
		return options[n-1].Label, true
	}
	return "", false
}
