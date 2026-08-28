package agentruntime

import (
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
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

func piAgentThinking(b *agent_backend_entity.AgentBackend) string {
	if b == nil {
		return ""
	}
	switch strings.TrimSpace(b.ReasoningEffort) {
	case "low", "medium", "high", "xhigh":
		return strings.TrimSpace(b.ReasoningEffort)
	case "max":
		return "xhigh"
	default:
		return ""
	}
}
