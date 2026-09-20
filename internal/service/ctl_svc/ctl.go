// Package ctl_svc exposes a loopback control API (mounted on the httpgateway
// under /ctl/) that the external `agrctl ctl` CLI drives to list agents /
// projects and dispatch a task to an agent by creating a chat session and
// starting a turn — the same primitives the in-turn subagent MCP uses, but
// reachable from outside a running turn so a human (or a plain shell command)
// can "@ an agent and hand it a task" without injecting an MCP server.
//
// Auth is a single process-lifetime bearer token; the desktop writes it (plus
// the gateway's actual URL) to the ctlendpoint handshake file so the CLI can
// find + authenticate against it. See internal/pkg/ctlendpoint.
package ctl_svc

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
)

type ctlSvc struct {
	token string

	mu       sync.RWMutex
	agents   AgentGateway
	projects ProjectGateway
	chat     ChatGateway
}

var defaultCtl = &ctlSvc{token: mustRandToken()}

// Default 取默认服务单例。
func Default() *ctlSvc { return defaultCtl }

// RegisterDeps bootstrap 接线(生产传 agent_repo.Agent() + ProjectSvcGateway() +
// ChatSvcGateway());测试可注 fake。
func (s *ctlSvc) RegisterDeps(agents AgentGateway, projects ProjectGateway, chat ChatGateway) {
	s.mu.Lock()
	s.agents, s.projects, s.chat = agents, projects, chat
	s.mu.Unlock()
}

// Token 返回本进程的控制 token；桌面在 gateway 起好后连同 URL 写进 ctlendpoint 握手文件。
func (s *ctlSvc) Token() string { return s.token }

// ControlHandler 返回挂到 gateway /ctl/ 的 HTTP handler。未 RegisterDeps 时各端点返 503。
//
// 返回的 handler **不持有 deps 快照**（每次请求现读）：bootstrap 起 gateway 时就要挂上它，
// 而 deps 要等 app.Startup(registerChatService → RegisterChat 之后)才 RegisterDeps。
// 若在构造时把 deps 拷进 handler，挂上去的那份永远是 nil，各端点恒 503 —— 单测直接
// newCtlHandler 传 deps 是抓不到的。
func (s *ctlSvc) ControlHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		h := newCtlHandler(s.token, s.agents, s.projects, s.chat)
		s.mu.RUnlock()
		h.ServeHTTP(w, r)
	})
}

// mustRandToken 生成 32 字节随机 token；crypto/rand 失败直接 panic(不可恢复的环境故障)。
func mustRandToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("ctl_svc: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
