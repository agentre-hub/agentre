package chat_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// TurnMCPProvider 按 (agent, session) 给 turn 注入额外 MCP server —— agent 级
// 内置工具体系的接缝。bootstrap 注册 orgtool_svc 的实现;空列表 = 不注入。
// groupID 形参为历史残留(恒传 0),现有 provider 均忽略它。
// 在 runTurn 单点生效,单聊/Regenerate 全覆盖。
type TurnMCPProvider func(ctx context.Context, a *agent_entity.Agent, sessionID, groupID int64) []agentruntime.MCPServerSpec

var turnMCPProviders []TurnMCPProvider

// RegisterTurnMCPProvider bootstrap 接线入口(可多次,按注册序拼接)。
func RegisterTurnMCPProvider(p TurnMCPProvider) { turnMCPProviders = append(turnMCPProviders, p) }

// ResetTurnMCPProviders 测试清理,防用例间串台;仅测试使用,生产代码勿调。
func ResetTurnMCPProviders() { turnMCPProviders = nil }

// appendTurnMCP runTurn 在组装 RunRequest 时调用;capOK = runner 声明 CapMCPTools。
func appendTurnMCP(ctx context.Context, base []agentruntime.MCPServerSpec, a *agent_entity.Agent, sessionID int64, capOK bool) []agentruntime.MCPServerSpec {
	if !capOK {
		return base
	}
	for _, p := range turnMCPProviders {
		base = append(base, p(ctx, a, sessionID, 0)...)
	}
	return base
}
