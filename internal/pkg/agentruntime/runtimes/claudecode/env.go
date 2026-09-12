package claudecode

import (
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// CLIDeps 重导出 agentruntime.CLIDeps,让子包不直接依赖顶层类型。
type CLIDeps = agentruntime.CLIDeps

// BuildClaudeCodeEnv 委托 agentruntime.BuildClaudeCodeEnv(实现在
// internal/pkg/agentruntime/clienv.go)。委托不复制,避免子进程 env 装配
// (gateway 路由 + model_routes alias + env_json 用户追加)出现两份漂移。
func BuildClaudeCodeEnv(b *agent_backend_entity.AgentBackend, deps CLIDeps) (map[string]string, error) {
	return agentruntime.BuildClaudeCodeEnv(b, deps)
}
