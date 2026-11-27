package ctl_svc

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/utils/httputils"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// Conversations 是控制 API 对「按 conversation_id 定位并操作一条会话」的窄依赖(chat_svc 实现)。
type Conversations = chat_svc.CtlConversationGateway

// ChatConversations 生产用会话端口;chat_svc 还没注册时为 nil(会话端点 503)。
func ChatConversations() Conversations {
	gw := chat_svc.CtlConversationSvcGateway()
	if gw == nil {
		return nil
	}
	return gw
}

// RegisterConversations 替换会话端口(默认按请求现取 chat_svc;测试可注 fake)。
func (s *ctlSvc) RegisterConversations(c Conversations) {
	s.mu.Lock()
	s.conversations = c
	s.mu.Unlock()
}

// conversationsPort 是本次请求用的会话端口:注册过的优先,否则现取 chat_svc。
func (s *ctlSvc) conversationsPort() Conversations {
	if s.conversations != nil {
		return s.conversations
	}
	return ChatConversations()
}

const (
	// errSessionNotVisible 不区分「不存在」与「看不到」,以免泄露存在性(spec「执行者与会话引用」)。
	errSessionNotVisible = "session not found or not accessible"
	errSessionRunning    = "session is running; use --steer to interject, or agrctl wait first"
)

// lookupConversation 把请求里的 conversation_id 解析成本机会话;失败时已写好响应并交回 false。
func (h *ctlHandler) lookupConversation(w http.ResponseWriter, r *http.Request, id string) (*chat_svc.CtlConversation, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		writeErr(w, http.StatusBadRequest, "conversationId is required")
		return nil, false
	}
	c, err := h.conversations.LookupCtlConversation(r.Context(), id)
	switch {
	case errors.Is(err, chat_svc.ErrCtlConversationInvalidID):
		writeErr(w, http.StatusBadRequest, err.Error())
		return nil, false
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err.Error())
		return nil, false
	case c == nil:
		writeErr(w, http.StatusNotFound, errSessionNotVisible)
		return nil, false
	}
	return c, true
}

// isActive 报告会话此刻是否有一轮在跑(含等待输入)。
func isActive(s chat_svc.CtlConversationStatus) bool {
	return s == chat_svc.CtlStatusRunning || s == chat_svc.CtlStatusWaiting
}

// isSessionRunningErr 认出两种「会话运行中」:桌面端本机这一轮还在飞,或执行它的
// agentred 拒绝在运行中的会话上再开一轮(wire.ErrSessionRunning,-32016)。
func isSessionRunningErr(err error) bool {
	return errors.Is(err, wire.ErrSessionRunning) || hasErrCode(err, code.ChatSendInFlight)
}

func hasErrCode(err error, want int) bool {
	var he *httputils.Error
	return errors.As(err, &he) && he.Code == want
}

// writeOverrideErr:覆盖值不被支持是用法错误(422,agrctl 退出码 2),其余照常 500。
func writeOverrideErr(w http.ResponseWriter, err error) {
	var oe *chat_svc.CtlOverrideError
	if errors.As(err, &oe) {
		writeErr(w, http.StatusUnprocessableEntity, oe.Error())
		return
	}
	writeErr(w, http.StatusInternalServerError, "apply session settings failed: "+err.Error())
}

func millis(ms int64) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}
