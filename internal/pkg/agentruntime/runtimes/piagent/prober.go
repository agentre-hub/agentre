package piagent

import (
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// DefaultBinary returns the executable name used when cli_path is empty.
func DefaultBinary() string { return "pi" }

const fallbackModelID = ""

func defaultModelForBackend(*agent_backend_entity.AgentBackend) string {
	return fallbackModelID
}

func BuildPiAgentEnv(b *agent_backend_entity.AgentBackend) (map[string]string, error) {
	return agentruntime.BuildPiAgentEnv(b)
}
