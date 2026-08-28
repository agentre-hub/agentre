package httpgateway

import (
	"context"
	"net/http"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// GatewayStatus 是 gateway 对外暴露的状态。
//
// json tag 与前端 GatewayStatusResponse 一致；State=stopped 时 URL/Routes 为空、Reason 携
// 带原始 net.Listen 错误摘要。
type GatewayStatus struct {
	State  string   `json:"status"`    // "running" | "stopped"
	URL    string   `json:"listenURL"` // 形如 http://127.0.0.1:60080；stopped 时为空
	Reason string   `json:"reason"`    // stopped 时填错误摘要；running 时为空
	Routes []string `json:"routes"`    // 已挂载的路由列表
}

// 已挂载的路由路径常量。
const (
	RouteAnthropic       = "/v1/messages"
	RouteOpenAIResponses = "/v1/responses"
	RouteOpenAIChat      = "/v1/chat/completions"
	RouteMCPPrefix       = "/mcp/"
	RouteHookInbox       = "/hook/v1/inbox"
	// RouteCtlPrefix 本地控制 API 前缀（/ctl/*），由 RegisterControl 注册的 handler
	// 统一接管，供 `agrctl ctl` 外部 CLI 驱动（新建会话/派发任务等）。
	RouteCtlPrefix = "/ctl/"
)

// DefaultRoutes 在 Status() State=running 时回显给前端。
func DefaultRoutes() []string {
	return []string{RouteAnthropic, RouteOpenAIResponses, RouteOpenAIChat, RouteHookInbox, RouteMCPPrefix + "*"}
}

// TokenIssuer 给 Prober 和 backend 自测发临时 token、撤销 token、读 URL。
// 这里签出来的 token 路由到 backend 自身绑定的供应商。
type TokenIssuer interface {
	IssueToken(ctx context.Context, backend *agent_backend_entity.AgentBackend, ttl time.Duration) (token string, err error)
	RevokeToken(token string)
	URL() string
	Status() GatewayStatus
}

// TokenRouter 是 chat flow 用的网关口：在 TokenIssuer 之上多出「按会话有效 ModelTarget 路由」
// 的两件能力（决策 3/9）——
//   - IssueTokenFor：按本轮的 effective ProviderKey+ModelKey（会话 > agent 绑定）签发；
//   - SetTokenTarget：**不换 token 字符串**地改既有 token 的路由目标。
//
// 之所以是会话切换的唯一手段：token 是会话级常驻、首轮就烤进 CLI 子进程 env 的，重签会
// 让在跑的子进程立刻 401（被修过的事故）。*Gateway 自然满足本接口。
type TokenRouter interface {
	TokenIssuer
	IssueTokenFor(
		ctx context.Context, backend *agent_backend_entity.AgentBackend, providerKey, modelKey string, ttl time.Duration,
	) (token string, err error)
	SetTokenTarget(token, providerKey, modelKey string) (previous string, ok bool)
}

// Lifecycle 给 app_settings_svc 用：查状态、重启、注册 MCP handler。
type Lifecycle interface {
	Status() GatewayStatus
	Restart(ctx context.Context) error
	RegisterMCP(prefix string, h http.Handler)
}
