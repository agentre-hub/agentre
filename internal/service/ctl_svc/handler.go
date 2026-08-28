package ctl_svc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
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
	case "send":
		h.serveSend(w, r)
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
	Agent     string `json:"agent"`     // 目标 agent 名称(与 agentId 二选一)
	AgentID   int64  `json:"agentId"`   // 目标 agent id(优先于 agent 名称)
	ProjectID int64  `json:"projectId"` // 可选：0 = 自由会话
	Text      string `json:"text"`      // 任务内容
	Wait      bool   `json:"wait"`      // true = 阻塞直到该轮完成并回传最终文本
	Isolated  bool   `json:"isolated"`  // true = 一次性隔离会话(不进侧栏)；默认普通可见会话
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

	// 解析目标 agent：优先 agentId，否则按名称。
	a, err := h.resolveAgent(r, req)
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
	sessionID := ensured.SessionID

	// --wait：订阅必须在 Send 之前(快 turn 的回执会丢)。
	var turnCh <-chan chat_svc.TurnResult
	if req.Wait {
		var cancel func()
		turnCh, cancel = h.chat.ObserveTurn(sessionID)
		defer cancel()
	}

	sendResp, err := h.chat.Send(r.Context(), &chat_svc.SendRequest{
		SessionID:             sessionID,
		AgentID:               a.ID,
		Text:                  req.Text,
		EmitTurnStartedBypass: req.Isolated, // 隔离会话不需要前端正常轮起始事件；可见会话要
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "start turn failed")
		return
	}
	logger.Ctx(r.Context()).Info("ctl_svc.serveSend: dispatched",
		zap.Int64("agentId", a.ID),
		zap.String("agent", a.Name),
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
func (h *ctlHandler) resolveAgent(r *http.Request, req sendRequest) (agentResolved, error) {
	if req.AgentID > 0 {
		a, err := h.agents.Find(r.Context(), req.AgentID)
		if err != nil || a == nil {
			return agentResolved{}, errAgentNotFound(req)
		}
		return agentResolved{ID: a.ID, Name: a.Name}, nil
	}
	name := strings.TrimSpace(req.Agent)
	if name == "" {
		return agentResolved{}, errAgentNotFound(req)
	}
	a, err := h.agents.FindByName(r.Context(), name)
	if err != nil || a == nil {
		return agentResolved{}, errAgentNotFound(req)
	}
	return agentResolved{ID: a.ID, Name: a.Name}, nil
}

type agentResolved struct {
	ID   int64
	Name string
}
