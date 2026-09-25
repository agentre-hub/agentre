package ctl_svc

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// TestClassifyCaller 钉死 spec「Routing and approval」的三类调用方：握手 token 按 agrctl
// 上报区分人工（TTY）与外部（非 TTY）；会话级 token 一律是会话调用，上报值不能把它抬成
// 人工。握手 token 上报「会话」或没上报时按外部处理（要审批的那一边）。
func TestClassifyCaller(t *testing.T) {
	handshake := credential{}
	session := credential{session: true, ref: agenttool.Ref{AgentID: 7, SessionID: 42}}
	cases := []struct {
		name     string
		cred     credential
		reported agentrewire.CtlCaller
		want     Caller
	}{
		{"握手 token + TTY → 人工", handshake, agentrewire.CtlCaller_CTL_CALLER_HUMAN, Caller{Class: CallerHuman}},
		{"握手 token + 非 TTY → 外部", handshake, agentrewire.CtlCaller_CTL_CALLER_EXTERNAL, Caller{Class: CallerExternal}},
		{"握手 token + 未上报 → 外部", handshake, agentrewire.CtlCaller_CTL_CALLER_UNSPECIFIED, Caller{Class: CallerExternal}},
		{"握手 token + 自称会话 → 外部", handshake, agentrewire.CtlCaller_CTL_CALLER_SESSION, Caller{Class: CallerExternal}},
		{"会话 token → 会话，带绑定的 (agent, session)", session, agentrewire.CtlCaller_CTL_CALLER_SESSION,
			Caller{Class: CallerSession, Session: agenttool.Ref{AgentID: 7, SessionID: 42}}},
		{"会话 token 自称人工 → 仍是会话", session, agentrewire.CtlCaller_CTL_CALLER_HUMAN,
			Caller{Class: CallerSession, Session: agenttool.Ref{AgentID: 7, SessionID: 42}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.cred.caller(c.reported))
		})
	}
}

// TestSessionToken_Permissions 钉死 spec「会话级 token」：能读、能 send；stop 与
// answer-permission 一律 403（AI 不能批准自己、不能停会话），且在碰到 chat 之前就拒。
func TestSessionToken_Permissions(t *testing.T) {
	signer := agenttool.NewTokenSigner()
	sessionTok := signer.MintToken(7, 42)
	chat := &fakeChat{sendResp: &chat_svc.SendResponse{}}
	h := &ctlHandler{
		token: testToken, sessions: signer,
		agents: &fakeAgents{}, projects: &fakeProjects{}, chat: chat, resources: fakeResources(),
	}

	t.Run("stop → 403，不调 chat.Stop", func(t *testing.T) {
		rec := do(t, h, http.MethodPost, "/ctl/v1/stop", sessionTok, `{"sessionId":42}`)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.False(t, chat.stopped)
	})
	t.Run("answer-permission → 403，不调 chat.AnswerToolPermission", func(t *testing.T) {
		rec := do(t, h, http.MethodPost, "/ctl/v1/answer-permission", sessionTok, `{"sessionId":42,"requestId":"r","allow":true}`)
		assert.Equal(t, http.StatusForbidden, rec.Code)
		assert.Nil(t, chat.answerPermissionReq)
	})
	t.Run("同样的请求用握手 token → 放行", func(t *testing.T) {
		rec := do(t, h, http.MethodPost, "/ctl/v1/stop", testToken, `{"sessionId":42}`)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
	t.Run("读资源 → 200", func(t *testing.T) {
		rec := postResources(t, h, sessionTok, `{"list":{"kind":"CTL_KIND_AGENT"}}`)
		assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	})
	t.Run("读 agents → 200", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/ctl/v1/agents", sessionTok, "")
		assert.Equal(t, http.StatusOK, rec.Code)
	})
	t.Run("send → 放行（不是 401/403）", func(t *testing.T) {
		rec := do(t, h, http.MethodPost, "/ctl/v1/send", sessionTok, `{"sessionId":42,"text":"hi"}`)
		assert.NotEqual(t, http.StatusForbidden, rec.Code)
		assert.NotEqual(t, http.StatusUnauthorized, rec.Code)
		assert.NotNil(t, chat.lastSend)
	})
	t.Run("另一个签名器签的 token → 401", func(t *testing.T) {
		rec := do(t, h, http.MethodGet, "/ctl/v1/agents", agenttool.NewTokenSigner().MintToken(7, 42), "")
		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}
