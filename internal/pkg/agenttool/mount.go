package agenttool

import (
	"context"
	"net/http"
	"sync"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// Mount 是三个内置工具服务(org / hook / subagent)共用的挂载脚手架:懒初始化共享
// MCP server(per-process HMAC secret 首次访问时生成)+ 记录 gateway base URL,并提供
// BuildTurnMCP 的公共骨架。各家只差 AgenttoolKey 与 NewServer 构造函数。
//
// 内嵌进宿主服务即可:Handler / SetGatewayBaseURL / BuildTurnMCP 三个方法随之提升。
type Mount struct {
	toolKey   string
	newServer func() *Server

	once    sync.Once
	server  *Server
	baseURL string
}

// NewMount 造一个挂载点。newServer 由宿主给出(通常是宿主自己的 newMCPServer 方法),
// 在首次 Handler / BuildTurnMCP 访问时调用一次。
func NewMount(toolKey string, newServer func() *Server) *Mount {
	return &Mount{toolKey: toolKey, newServer: newServer}
}

func (m *Mount) serverInit() *Server {
	m.once.Do(func() { m.server = m.newServer() })
	return m.server
}

// MCPHandler 返回挂到 gateway 的 MCP-over-HTTP handler。
func (m *Mount) MCPHandler() http.Handler { return m.serverInit() }

// Server 返回懒初始化的共享 MCP server(宿主与测试需要直接签 token / 验 token 时用)。
func (m *Mount) Server() *Server { return m.serverInit() }

// SetGatewayBaseURL 由 bootstrap 在 gateway 起好后注入(用于拼 MCP server URL)。
func (m *Mount) SetGatewayBaseURL(u string) { m.baseURL = u }

// BuildTurnMCP 实现 chat_svc.TurnMCPProvider:agent 开启本工具时返回注入 spec。
func (m *Mount) BuildTurnMCP(_ context.Context, a *agent_entity.Agent, sessionID int64) []agentruntime.MCPServerSpec {
	if a == nil || !a.ToolEnabled(m.toolKey) || m.baseURL == "" {
		return nil
	}
	def, ok := Lookup(m.toolKey)
	if !ok {
		return nil
	}
	return []agentruntime.MCPServerSpec{{
		Name:    def.Key,
		URL:     m.baseURL + def.MCPPath,
		Headers: map[string]string{"Authorization": "Bearer " + m.serverInit().MintToken(a.ID, sessionID)},
		Tools:   def.ToolNames,
	}}
}
