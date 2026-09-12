package codex

import (
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// CLIDeps 重导出 agentruntime.CLIDeps,让子包不直接依赖顶层类型。
type CLIDeps = agentruntime.CLIDeps

// BuildCodexEnv 委托 agentruntime.BuildCodexEnv(实现在
// internal/pkg/agentruntime/clienv.go)。委托不复制,避免子进程 env 装配逻辑两份漂移。
func BuildCodexEnv(b *agent_backend_entity.AgentBackend, deps CLIDeps) (map[string]string, error) {
	return agentruntime.BuildCodexEnv(b, deps)
}

// BuildCodexConfig 委托 agentruntime.BuildCodexConfig。
func BuildCodexConfig(deps CLIDeps) []string {
	return agentruntime.BuildCodexConfig(deps)
}
