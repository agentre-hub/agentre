package hooktool_svc

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
)

type hooktoolSvc struct {
	mcp             *agenttool.Server
	mcpOnce         sync.Once
	gatewayBaseURL  string
	approvalTimeout time.Duration

	hooks       HookService
	agentLookup AgentLookup
	approval    ApprovalGateway
}

var defaultHooktool = &hooktoolSvc{approvalTimeout: 4 * time.Minute} // 与 orgtool 一致:留 CLI 硬顶余量

// Default 取默认服务单例。
func Default() *hooktoolSvc { return defaultHooktool }

// RegisterDeps bootstrap 接线(生产传 hook_svc.Hook()/agent_repo.Agent()/chat_svc.Chat());测试注 mock。
func (s *hooktoolSvc) RegisterDeps(h HookService, l AgentLookup, ap ApprovalGateway) {
	s.hooks, s.agentLookup, s.approval = h, l, ap
}

// mcpHandlerInit 懒初始化共享 MCP server(per-process HMAC secret 首次访问时生成)。
func (s *hooktoolSvc) mcpHandlerInit() *agenttool.Server {
	s.mcpOnce.Do(func() { s.mcp = s.newMCPServer() })
	return s.mcp
}

// MCPHandler 返回挂到 gateway /mcp/hook/ 的 HTTP handler。
func (s *hooktoolSvc) MCPHandler() http.Handler { return s.mcpHandlerInit() }

// SetGatewayBaseURL 由 bootstrap 在 gateway 起好后注入(用于拼 MCP server URL)。
func (s *hooktoolSvc) SetGatewayBaseURL(u string) { s.gatewayBaseURL = u }

// BuildTurnMCP 实现 chat_svc.TurnMCPProvider:agent 开启 hook 工具时返回注入 spec。
func (s *hooktoolSvc) BuildTurnMCP(_ context.Context, a *agent_entity.Agent, sessionID int64, _ int64) []agentruntime.MCPServerSpec {
	if a == nil || !a.ToolEnabled(agenttool.KeyHook) || s.gatewayBaseURL == "" {
		return nil
	}
	def, ok := agenttool.Lookup(agenttool.KeyHook)
	if !ok {
		return nil
	}
	return []agentruntime.MCPServerSpec{{
		Name:    def.Key,
		URL:     s.gatewayBaseURL + def.MCPPath,
		Headers: map[string]string{"Authorization": "Bearer " + s.mcpHandlerInit().MintToken(a.ID, sessionID)},
		Tools:   def.ToolNames,
	}}
}
