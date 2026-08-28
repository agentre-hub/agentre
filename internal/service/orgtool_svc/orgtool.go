package orgtool_svc

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
)

type orgtoolSvc struct {
	mcp             *agenttool.Server
	mcpOnce         sync.Once
	gatewayBaseURL  string
	approvalTimeout time.Duration

	orgQuery     OrgQuery
	deptCommand  DeptCommand
	agentCommand AgentCommand
	agentLookup  AgentLookup
	approval     ApprovalGateway
}

var defaultOrgtool = &orgtoolSvc{approvalTimeout: 4 * time.Minute} // spike 实测 CLI 硬顶 ~285s,留 25s 余量

// Default 取默认服务单例。
func Default() *orgtoolSvc { return defaultOrgtool }

// RegisterDeps bootstrap 接线(生产传 department_svc.Department()/agent_svc.Agent()/
// agent_repo.Agent()/chat_svc.Chat());测试注 mock。
func (s *orgtoolSvc) RegisterDeps(q OrgQuery, d DeptCommand, a AgentCommand, l AgentLookup, ap ApprovalGateway) {
	s.orgQuery, s.deptCommand, s.agentCommand, s.agentLookup, s.approval = q, d, a, l, ap
}

// mcpHandlerInit 懒初始化共享 MCP server(per-process HMAC secret 在首次访问时生成)。
func (s *orgtoolSvc) mcpHandlerInit() *agenttool.Server {
	s.mcpOnce.Do(func() { s.mcp = s.newMCPServer() })
	return s.mcp
}

// MCPHandler 返回挂到 gateway /mcp/org/ 的 HTTP handler。
func (s *orgtoolSvc) MCPHandler() http.Handler { return s.mcpHandlerInit() }

// SetGatewayBaseURL 由 bootstrap 在 gateway 起好后注入(用于拼 MCP server URL)。
func (s *orgtoolSvc) SetGatewayBaseURL(u string) { s.gatewayBaseURL = u }

// BuildTurnMCP 实现 chat_svc.TurnMCPProvider:agent 开启 org 工具时返回注入 spec。
func (s *orgtoolSvc) BuildTurnMCP(_ context.Context, a *agent_entity.Agent, sessionID int64, _ int64) []agentruntime.MCPServerSpec {
	if a == nil || !a.ToolEnabled(agenttool.KeyOrg) || s.gatewayBaseURL == "" {
		return nil
	}
	def, ok := agenttool.Lookup(agenttool.KeyOrg)
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
