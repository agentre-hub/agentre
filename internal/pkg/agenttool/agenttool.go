// Package agenttool 维护 agent 级内置工具注册表(静态元数据)。leaf 层:
// 只描述 key/挂载路径/MCP tool 名,不 import service —— handler 实现在
// internal/service/orgtool_svc,由 bootstrap 按 MCPPath 挂到 gateway。
package agenttool

// Definition 一个内置 agent 工具(以 MCP server 形态注入会话)。
type Definition struct {
	Key       string   // agents.tools_json 的 key,也是 MCPServerSpec.Name
	MCPPath   string   // gateway 挂载路径
	ToolNames []string // server 暴露的 MCP tool 名(全部进 allowedTools,审批在服务端)
}

// KeyOrg 组织架构读写工具。
const KeyOrg = "org"

// KeySubagent 调用子 agent 工具(把子任务委派给另一具名 agent,同步拿回输出)。
const KeySubagent = "subagent"

// KeyHook 脚本 Hook 读写/试运行工具(对话里让 agent 起草脚本、dry-run 验证、注册 cron 调度)。
const KeyHook = "hook"

var registry = []Definition{
	{Key: KeyOrg, MCPPath: "/mcp/org/", ToolNames: []string{
		"org_get",
		"org_create_department", "org_update_department", "org_delete_department",
		"org_create_agent", "org_update_agent", "org_delete_agent",
	}},
	{Key: KeySubagent, MCPPath: "/mcp/subagent/", ToolNames: []string{"agent_list", "agent_call"}},
	{Key: KeyHook, MCPPath: "/mcp/hook/", ToolNames: []string{
		"hook_list", "hook_get", "hook_create", "hook_update", "hook_delete", "hook_run",
	}},
}

// Lookup 按 key 找定义。
func Lookup(key string) (Definition, bool) {
	for _, d := range registry {
		if d.Key == key {
			return d, true
		}
	}
	return Definition{}, false
}

// Keys 返回全部工具 key(给前端可用工具清单)。
func Keys() []string {
	out := make([]string, 0, len(registry))
	for _, d := range registry {
		out = append(out, d.Key)
	}
	return out
}
