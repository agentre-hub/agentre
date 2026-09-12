package agentruntime

import (
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/pkg/piagent"
)

func buildPiAgentShellCommand(spec LaunchCommandSpec, cwd string) (string, error) {
	env, err := BuildPiAgentEnv(spec.Backend)
	if err != nil {
		return "", err
	}
	binary := strings.TrimSpace(spec.Backend.CLIPath)
	if binary == "" {
		binary = "pi"
	}
	argv := []string{binary}
	if sessionID := strings.TrimSpace(spec.ProviderSessionID); sessionID != "" {
		argv = append(argv, "--session", sessionID)
	}
	if model := piAgentModel(spec.Backend, spec.ProviderSessionID); model != "" {
		argv = append(argv, "--model", model)
	}
	if eff := piAgentThinking(spec.Backend); eff != "" {
		argv = append(argv, "--thinking", eff)
	}
	return assembleShellLine(cwd, env, argv), nil
}

func piAgentModel(b *agent_backend_entity.AgentBackend, _ string) string {
	return ""
}

// piAgentThinking 把落库的 reasoning_effort 映射为「复制启动命令」里的 pi CLI
// --thinking 值。与真正 spawn 路径共用同一份映射(pkg/piagent.NormalizeThinkingLevel),
// 不另写一份。
func piAgentThinking(b *agent_backend_entity.AgentBackend) string {
	if b == nil {
		return ""
	}
	return piagent.NormalizeThinkingLevel(b.ReasoningEffort)
}
