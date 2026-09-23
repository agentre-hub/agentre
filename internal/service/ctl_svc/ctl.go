// Package ctl_svc exposes a loopback control API (mounted on the httpgateway
// under /ctl/) that the external `agrctl ctl` CLI drives to list agents /
// projects and dispatch a task to an agent by creating a chat session and
// starting a turn — the same primitives the in-turn subagent MCP uses, but
// reachable from outside a running turn so a human (or a plain shell command)
// can "@ an agent and hand it a task" without injecting an MCP server.
//
// Auth recognizes two bearer tokens: the process-lifetime handshake token the
// desktop writes (plus the gateway's actual URL) to the ctlendpoint handshake
// file (see internal/pkg/ctlendpoint), and session-scoped tokens bound to one
// (agent, session) that are injected into every CLI agent subprocess. See
// caller.go for how the two map onto caller classes.
package ctl_svc

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
)

type ctlSvc struct {
	token string
	// sessions 签发并校验会话级 token；密钥 per-instance，签发与校验都走它。
	sessions *agenttool.TokenSigner

	mu        sync.RWMutex
	agents    AgentGateway
	projects  ProjectGateway
	chat      ChatGateway
	resources Resources
	// endpoint 是注入会话的控制 API base URL；空 = 还没发布，不签会话凭证。
	endpoint string
}

var defaultCtl = newCtlSvc()

func newCtlSvc() *ctlSvc {
	return &ctlSvc{token: mustRandToken(), sessions: agenttool.NewTokenSigner(), resources: ProductionResources()}
}

// Default 取默认服务单例。
func Default() *ctlSvc { return defaultCtl }

// RegisterDeps bootstrap 接线(生产传 agent_repo.Agent() + ProjectSvcGateway() +
// ChatSvcGateway());测试可注 fake。
func (s *ctlSvc) RegisterDeps(agents AgentGateway, projects ProjectGateway, chat ChatGateway) {
	s.mu.Lock()
	s.agents, s.projects, s.chat = agents, projects, chat
	s.mu.Unlock()
}

// RegisterResources 替换资源网关（默认是 ProductionResources；测试可注 fake）。
func (s *ctlSvc) RegisterResources(r Resources) {
	s.mu.Lock()
	s.resources = r
	s.mu.Unlock()
}

// Token 返回本进程的控制 token；桌面在 gateway 起好后连同 URL 写进 ctlendpoint 握手文件。
func (s *ctlSvc) Token() string { return s.token }

// SessionToken 为 (agent, session) 签会话级 token（确定性：同一对每次相同）。
func (s *ctlSvc) SessionToken(agentID, sessionID int64) string {
	return s.sessions.MintToken(agentID, sessionID)
}

// VerifySessionToken 校验本实例签的会话级 token，解出绑定的 (agent, session)。
func (s *ctlSvc) VerifySessionToken(tok string) (agenttool.Ref, bool) {
	return s.sessions.Lookup(tok)
}

// PublishSessionEndpoint 记下控制 API 的 base URL，并把本服务注册为 CLI 子进程的会话凭证
// 来源：此后每个 CLI agent 子进程都带上 AGENTRE_CTL_ENDPOINT 与会话级 AGENTRE_CTL_TOKEN。
// 桌面在 gateway 起好、拿到实际 URL 时调用。
func (s *ctlSvc) PublishSessionEndpoint(baseURL string) {
	s.mu.Lock()
	s.endpoint = baseURL
	s.mu.Unlock()
	agentruntime.RegisterCtlCredentialSource(s.SessionCredentials)
}

// SessionCredentials 是 (agent, session) 的会话凭证；端点未发布时为零值（不注入）。
func (s *ctlSvc) SessionCredentials(agentID, sessionID int64) agentruntime.CtlCredentials {
	s.mu.RLock()
	endpoint := s.endpoint
	s.mu.RUnlock()
	if endpoint == "" {
		return agentruntime.CtlCredentials{}
	}
	return agentruntime.CtlCredentials{Endpoint: endpoint, Token: s.SessionToken(agentID, sessionID)}
}

// ControlHandler 返回挂到 gateway /ctl/ 的 HTTP handler。未 RegisterDeps 时各端点返 503。
//
// 返回的 handler **不持有 deps 快照**（每次请求现读）：bootstrap 起 gateway 时就要挂上它，
// 而 deps 要等 app.Startup(registerChatService → RegisterChat 之后)才 RegisterDeps。
// 若在构造时把 deps 拷进 handler，挂上去的那份永远是 nil，各端点恒 503 —— 单测直接
// newCtlHandler 传 deps 是抓不到的。
func (s *ctlSvc) ControlHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.RLock()
		h := &ctlHandler{
			token: s.token, sessions: s.sessions,
			agents: s.agents, projects: s.projects, chat: s.chat, resources: s.resources,
		}
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
