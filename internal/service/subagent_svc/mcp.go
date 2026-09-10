package subagent_svc

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
)

// newMCPServer 把「调用子 agent」工具接到共享的 MCP server 骨架(挂在 gateway /mcp/subagent/)。
// 令牌签发与校验、JSON-RPC 信封与方法分支、GET 405、bootstrap 窗口 503、工具开关实时校验
// 全部归 internal/pkg/agenttool;本包只提供 schema 与两个只读处理器(无写工具)。
func (s *subagentSvc) newMCPServer() *agenttool.Server {
	return agenttool.NewServer(agenttool.ServerConfig{
		ToolKey:    agenttool.KeySubagent,
		ServerName: "agentre-subagent",
		Schemas:    subagentToolSchemas(),
		// bootstrap 窗口期(RegisterDeps 未执行)未就绪 → 503
		Ready: func() bool { return s.agents != nil },
		LookupAgent: func(ctx context.Context, agentID int64) (*agent_entity.Agent, error) {
			return s.agents.Find(ctx, agentID)
		},
		Read: map[string]agenttool.ReadHandler{
			"agent_list": s.handleAgentList,
			"agent_call": s.handleAgentCall,
		},
		Write: nil, // 子 agent 工具没有写操作
	})
}

// ---- 只读工具 ----

type agentListItem struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	SystemBadge string `json:"systemBadge,omitempty"`
}

func (s *subagentSvc) handleAgentList(ctx context.Context, _ agenttool.Ref, _ json.RawMessage) (string, error) {
	list, err := s.agents.List(ctx)
	if err != nil {
		return "", err
	}
	out := make([]agentListItem, 0, len(list))
	for _, a := range list {
		out = append(out, agentListItem{ID: a.ID, Name: a.Name, Description: a.Description, SystemBadge: a.SystemBadge})
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

func (s *subagentSvc) handleAgentCall(ctx context.Context, ref agenttool.Ref, rawArgs json.RawMessage) (string, error) {
	var args struct {
		AgentName string `json:"agent_name"`
		Prompt    string `json:"prompt"`
	}
	_ = json.Unmarshal(rawArgs, &args)
	if strings.TrimSpace(args.AgentName) == "" || strings.TrimSpace(args.Prompt) == "" {
		return "", agenttool.InvalidParams("agent_name 和 prompt 均为必填")
	}
	return s.callAgent(ctx, ref, args.AgentName, args.Prompt)
}

func subagentToolSchemas() []any {
	return []any{
		map[string]any{
			"name":        "agent_list",
			"description": "列出可作为子 agent 调用的全部已配置 agent(id/名称/描述)。无参数。",
			"inputSchema": map[string]any{"type": "object", "properties": map[string]any{}},
		},
		map[string]any{
			"name":        "agent_call",
			"description": "把一段子任务委派给指定的已配置 agent 执行,同步阻塞直至其完成,返回它的最终文本输出。子 agent 在隔离的一次性会话中运行(看不到当前对话),任务须能在数分钟内完成。",
			"inputSchema": map[string]any{
				"type":     "object",
				"required": []string{"agent_name", "prompt"},
				"properties": map[string]any{
					"agent_name": map[string]any{"type": "string", "description": "目标 agent 名称(见 agent_list)"},
					"prompt":     map[string]any{"type": "string", "description": "交给子 agent 的完整任务描述(它看不到当前对话上下文,需自包含)"},
				},
			},
		},
	}
}
