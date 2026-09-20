package ctl_svc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// routePrefix 控制 API 的版本化前缀；gateway 已把 /ctl/* 全部转到本 handler。
const routePrefix = "/ctl/v1/"

// ctlHandler 是 /ctl/* 的 HTTP handler：先过 bearer 鉴权，再按路径分发。
type ctlHandler struct {
	token    string
	agents   AgentGateway
	projects ProjectGateway
	chat     ChatGateway
}

func newCtlHandler(token string, agents AgentGateway, projects ProjectGateway, chat ChatGateway) *ctlHandler {
	return &ctlHandler{token: token, agents: agents, projects: projects, chat: chat}
}

func (h *ctlHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !h.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid control token")
		return
	}
	switch strings.TrimPrefix(r.URL.Path, routePrefix) {
	case "agents":
		h.serveAgents(w, r)
	case "projects":
		h.serveProjects(w, r)
	case "sessions":
		h.serveSessions(w, r)
	case "send":
		h.serveSend(w, r)
	case "stop":
		h.serveStop(w, r)
	case "answer-permission":
		h.serveAnswerPermission(w, r)
	case "stream":
		h.serveStream(w, r)
	default:
		writeErr(w, http.StatusNotFound, "unknown control endpoint")
	}
}

// authorized 常量时间比对 bearer token；token 未装配(空)时一律拒绝。
func (h *ctlHandler) authorized(r *http.Request) bool {
	if h.token == "" {
		return false
	}
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	return subtle.ConstantTimeCompare([]byte(tok), []byte(h.token)) == 1
}

type agentDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	SystemBadge  string `json:"systemBadge,omitempty"`
	DepartmentID int64  `json:"departmentId,omitempty"`
}

func (h *ctlHandler) serveAgents(w http.ResponseWriter, r *http.Request) {
	if h.agents == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	list, err := h.agents.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]agentDTO, 0, len(list))
	for _, a := range list {
		if a == nil {
			continue
		}
		out = append(out, agentDTO{
			ID:           a.ID,
			Name:         a.Name,
			Description:  a.Description,
			SystemBadge:  a.SystemBadge,
			DepartmentID: a.DepartmentID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"agents": out})
}

func (h *ctlHandler) serveProjects(w http.ResponseWriter, r *http.Request) {
	if h.projects == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	list, err := h.projects.List(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []ProjectInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"projects": list})
}

type sendRequest struct {
	// SessionID >0 = 追加到既有会话(不建新会话);0 = 按 agent/agentId 建新会话。
	SessionID int64       `json:"sessionId"`
	Agent     string      `json:"agent"`            // 目标 agent 名称(与 agentId 二选一)
	AgentID   int64       `json:"agentId"`          // 目标 agent id(优先于 agent 名称)
	ProjectID int64       `json:"projectId"`        // 可选：0 = 自由会话
	Text      string      `json:"text"`             // 任务内容
	Images    []sendImage `json:"images,omitempty"` // 附件图片(name + data URL)
	Wait      bool        `json:"wait"`             // true = 阻塞直到该轮完成并回传最终文本
	Isolated  bool        `json:"isolated"`         // true = 一次性隔离会话(不进侧栏)；默认普通可见会话
}

// sendImage 是控制 API 收下的图片附件形状,与 chat_svc.SendImage 同形但刻意不复用
// 那个类型:两个包的 JSON 契约各自独立,避免 ctl 的 wire 形状被 chat 域内部类型绑架。
type sendImage struct {
	Name    string `json:"name,omitempty"`
	DataURL string `json:"dataUrl"`
}

type sendDTO struct {
	SessionID          int64  `json:"sessionId"`
	AssistantMessageID int64  `json:"assistantMessageId,omitempty"`
	Stream             string `json:"stream,omitempty"`
	Text               string `json:"text,omitempty"`
	Done               bool   `json:"done"`
}

func (h *ctlHandler) serveSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "send requires POST")
		return
	}
	if h.agents == nil || h.chat == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return
	}

	// 解析/定位目标会话:既有会话直接用,否则按 agent 解析后新建。
	var (
		sessionID int64
		agentID   int64
	)
	if req.SessionID > 0 {
		sessionID = req.SessionID
	} else {
		a, err := h.resolveAgent(r, req.Agent, req.AgentID)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		purpose := chat_svc.SessionPurposeUserChat
		if req.Isolated {
			purpose = chat_svc.SessionPurposeSubagentCall
		}
		ensured, err := h.chat.EnsureSession(r.Context(), &chat_svc.EnsureSessionRequest{
			Purpose:   purpose,
			AgentID:   a.ID,
			ProjectID: req.ProjectID,
			Title:     "ctl: " + a.Name,
		})
		if err != nil || ensured == nil || ensured.SessionID <= 0 {
			writeErr(w, http.StatusInternalServerError, "create session failed")
			return
		}
		sessionID = ensured.SessionID
		agentID = a.ID
	}

	// --wait：订阅必须在 Send 之前(快 turn 的回执会丢)。
	var turnCh <-chan chat_svc.TurnResult
	if req.Wait {
		var cancel func()
		turnCh, cancel = h.chat.ObserveTurn(sessionID)
		defer cancel()
	}

	sendResp, err := h.chat.Send(r.Context(), &chat_svc.SendRequest{
		SessionID:             sessionID,
		AgentID:               agentID,
		Text:                  req.Text,
		Images:                toSendImages(req.Images),
		EmitTurnStartedBypass: req.Isolated, // 隔离会话不需要前端正常轮起始事件；可见会话要
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "start turn failed")
		return
	}
	logger.Ctx(r.Context()).Info("ctl_svc.serveSend: dispatched",
		zap.Int64("agentId", agentID),
		zap.Int64("sessionId", sessionID),
		zap.Bool("wait", req.Wait))

	if !req.Wait {
		writeJSON(w, http.StatusOK, sendDTO{
			SessionID:          sessionID,
			AssistantMessageID: sendResp.AssistantMessageID,
			Stream:             sendResp.Stream,
			Done:               false,
		})
		return
	}

	select {
	case res := <-turnCh:
		if res.Err != nil {
			writeErr(w, http.StatusInternalServerError, "turn error: "+res.Err.Error())
			return
		}
		text, terr := h.chat.FinalAssistantText(r.Context(), res.AssistantMessageID)
		if terr != nil {
			writeErr(w, http.StatusInternalServerError, "read final text failed")
			return
		}
		writeJSON(w, http.StatusOK, sendDTO{
			SessionID:          sessionID,
			AssistantMessageID: res.AssistantMessageID,
			Text:               text,
			Done:               true,
		})
	case <-r.Context().Done():
		// CLI 断开/取消 → 中止该轮，不留悬空 turn（用 Background：请求 ctx 已取消）。
		_, _ = h.chat.Stop(context.Background(), &chat_svc.StopRequest{SessionID: sessionID})
		writeErr(w, http.StatusRequestTimeout, "canceled")
	}
}

// resolveAgent 优先按 id，否则按名称；找不到返回错误(供上层转 404)。
func (h *ctlHandler) resolveAgent(r *http.Request, agentName string, agentID int64) (agentResolved, error) {
	if agentID > 0 {
		a, err := h.agents.Find(r.Context(), agentID)
		if err != nil || a == nil {
			return agentResolved{}, errAgentNotFound(agentName, agentID)
		}
		return agentResolved{ID: a.ID, Name: a.Name}, nil
	}
	name := strings.TrimSpace(agentName)
	if name == "" {
		return agentResolved{}, errAgentNotFound(agentName, agentID)
	}
	a, err := h.agents.FindByName(r.Context(), name)
	if err != nil || a == nil {
		return agentResolved{}, errAgentNotFound(agentName, agentID)
	}
	return agentResolved{ID: a.ID, Name: a.Name}, nil
}

type agentResolved struct {
	ID   int64
	Name string
}

// toSendImages 把控制 API 的图片 DTO 映射到 chat_svc 的 SendImage(同形,显式转换)。
func toSendImages(images []sendImage) []chat_svc.SendImage {
	if len(images) == 0 {
		return nil
	}
	out := make([]chat_svc.SendImage, 0, len(images))
	for _, img := range images {
		out = append(out, chat_svc.SendImage{Name: img.Name, DataURL: img.DataURL})
	}
	return out
}

// ---- sessions ----

// sessionsRequest 是「新建一个 Agentre 会话」的入参(ACP session/new 的落脚点)。
type sessionsRequest struct {
	Agent     string `json:"agent"`
	AgentID   int64  `json:"agentId"`
	ProjectID int64  `json:"projectId"`
}

func (h *ctlHandler) serveSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "sessions requires POST")
		return
	}
	if h.agents == nil || h.chat == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	var req sessionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	a, err := h.resolveAgent(r, req.Agent, req.AgentID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	ensured, err := h.chat.EnsureSession(r.Context(), &chat_svc.EnsureSessionRequest{
		Purpose:   chat_svc.SessionPurposeUserChat,
		AgentID:   a.ID,
		ProjectID: req.ProjectID,
		Title:     "ctl: " + a.Name,
	})
	if err != nil || ensured == nil || ensured.SessionID <= 0 {
		writeErr(w, http.StatusInternalServerError, "create session failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessionId": ensured.SessionID})
}

// ---- stop ----

type stopRequest struct {
	SessionID int64 `json:"sessionId"`
}

func (h *ctlHandler) serveStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "stop requires POST")
		return
	}
	if h.chat == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	var req stopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.SessionID <= 0 {
		writeErr(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	if _, err := h.chat.Stop(r.Context(), &chat_svc.StopRequest{SessionID: req.SessionID}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stopped": true})
}

// ---- answer-permission ----

// answerPermissionRequest 是 agrctl acp 把 ACP client 的权限决策回灌给目标 agent 的
// 入参。Allow=false 时 AlwaysAllowSession 被忽略(deny 永远单次);DenyReason 非空时
// 作为用户反馈注入目标 agent。
type answerPermissionRequest struct {
	SessionID          int64  `json:"sessionId"`
	RequestID          string `json:"requestId"`
	Allow              bool   `json:"allow"`
	AlwaysAllowSession bool   `json:"alwaysAllowSession,omitempty"`
	DenyReason         string `json:"denyReason,omitempty"`
}

func (h *ctlHandler) serveAnswerPermission(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "answer-permission requires POST")
		return
	}
	if h.chat == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	var req answerPermissionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if req.SessionID <= 0 {
		writeErr(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	if strings.TrimSpace(req.RequestID) == "" {
		writeErr(w, http.StatusBadRequest, "requestId is required")
		return
	}
	if _, err := h.chat.AnswerToolPermission(r.Context(), &chat_svc.AnswerToolPermissionRequest{
		SessionID:          req.SessionID,
		RequestID:          req.RequestID,
		Allow:              req.Allow,
		AlwaysAllowSession: req.AlwaysAllowSession,
		DenyReason:         req.DenyReason,
	}); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"answered": true})
}

// ---- stream (SSE) ----

// serveStream 把某会话的流事件逐条以 SSE 推给订阅者(agrctl acp 消费)。
//
// 订阅必须先于响应头:调用方的顺序是「先打开这条流、再发 send」,它把「收到响应头」
// 当作「订阅已生效」。若先写头后订阅,首片会在那个窗口里丢掉。
func (h *ctlHandler) serveStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "stream requires GET")
		return
	}
	if h.chat == nil {
		writeErr(w, http.StatusServiceUnavailable, "control service not ready")
		return
	}
	sessionID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("sessionId")), 10, 64)
	if err != nil || sessionID <= 0 {
		writeErr(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	ch, cancel := h.chat.SubscribeSessionEvents(sessionID)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
