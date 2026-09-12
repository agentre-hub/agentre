// Package piagentskill discovers Pi skills through its RPC get_commands command.
package piagentskill

import (
	"context"
	"os"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/pkg/piagent"
)

func init() {
	agentskill.RegisterDiscoverer(agent_backend_entity.TypePiAgent, Discoverer{})
}

type commandLister func(ctx context.Context, binary, cwd string) ([]piagent.Command, error)

type Discoverer struct {
	listCommands commandLister
}

// Discover returns no configurable packs. Pi owns packages and skills in its
// own config; Agentre only projects the effective command catalog.
func (Discoverer) Discover(context.Context, agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	return []agentskill.SkillPack{}, nil
}

func (d Discoverer) commands(ctx context.Context, binary, cwd string) ([]piagent.Command, error) {
	if d.listCommands != nil {
		return d.listCommands(ctx, binary, cwd)
	}
	return piagent.New(piagent.WithBinary(binary), piagent.WithCwd(cwd)).ListCommands(ctx)
}

// DiscoverCommands delegates to Pi so user, project, and package skill
// precedence matches the running CLI. Only source=skill entries are projected;
// their RPC names already use Pi's canonical skill:<name> form.
func (d Discoverer) DiscoverCommands(ctx context.Context, q agentskill.CommandDiscoverQuery) ([]agentskill.SkillCommand, error) {
	binary := strings.TrimSpace(q.CLIPath)
	if binary == "" {
		binary = "pi"
	}
	cwd := strings.TrimSpace(q.Cwd)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	listed, err := d.commands(ctx, binary, cwd)
	if err != nil {
		return []agentskill.SkillCommand{}, nil //nolint:nilerr // Optional suggestions fail open to normal input.
	}
	seen := map[string]struct{}{}
	commands := []agentskill.SkillCommand{}
	for _, command := range listed {
		name := strings.TrimSpace(command.Name)
		if command.Source != "skill" || name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		commands = append(commands, agentskill.SkillCommand{
			Name:        name,
			Description: strings.TrimSpace(command.Description),
		})
	}
	return commands, nil
}
