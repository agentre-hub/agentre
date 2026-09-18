// Package codexskill 用 `codex plugin list --json` 发现该安装的插件技能包。
package codexskill

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/pkg/codex"
)

func init() {
	agentskill.RegisterDiscoverer(agent_backend_entity.TypeCodex, Discoverer{})
}

type commandRunner = agentskill.CommandRunner
type skillLister func(ctx context.Context, binary, cwd string, config []string) ([]codex.Skill, error)

// Discoverer 用 codex CLI 枚举已安装插件。run 为 nil 时走真实 exec(生产默认)。
type Discoverer struct {
	run        commandRunner
	listSkills skillLister
}

func (d Discoverer) skills(ctx context.Context, binary, cwd string, config []string) ([]codex.Skill, error) {
	if d.listSkills != nil {
		return d.listSkills(ctx, binary, cwd, config)
	}
	opts := []codex.Option{codex.WithBinary(binary), codex.WithCwd(cwd)}
	for _, item := range config {
		opts = append(opts, codex.WithConfig(item))
	}
	return codex.New(opts...).ListSkills(ctx, []string{cwd}, true)
}

// runner 取命令执行器:未注入 → 真实 exec 调用(生产默认)。
func (d Discoverer) runner() commandRunner {
	if d.run != nil {
		return d.run
	}
	return agentskill.ExecRunner()
}

type rawPluginList struct {
	Installed []rawPlugin `json:"installed"`
}

type rawPlugin struct {
	PluginID string `json:"pluginId"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Source   struct {
		Path string `json:"path"`
	} `json:"source"`
}

func parsePluginList(b []byte) ([]agentskill.SkillPack, error) {
	out := []agentskill.SkillPack{}
	if len(b) == 0 {
		return out, nil
	}
	var raws rawPluginList
	if err := json.Unmarshal(b, &raws); err != nil {
		return out, nil //nolint:nilerr // 坏 JSON 视为无发现,软降级不阻断
	}
	for _, r := range raws.Installed {
		id := strings.TrimSpace(r.PluginID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = id
			if before, _, ok := strings.Cut(id, "@"); ok && before != "" {
				name = before
			}
		}
		out = append(out, agentskill.SkillPack{
			ID:              id,
			Name:            name,
			Skills:          agentskill.ScanSkills(strings.TrimSpace(r.Source.Path)),
			Source:          agentskill.SourceInstalled,
			Installed:       true,
			GloballyEnabled: r.Enabled,
		})
	}
	return out, nil
}

// Discover 调用 codex plugin list --json 枚举已安装插件。CLI 不可用时软降级返回空。
func (d Discoverer) Discover(ctx context.Context, q agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	return agentskill.DiscoverPluginList(ctx, d.runner(), q.CLIPath, "codex", parsePluginList)
}

// DiscoverCommands delegates to Codex app-server skills/list so plugin, user,
// project, and system skills follow the current CLI's own resolution rules.
func (d Discoverer) DiscoverCommands(ctx context.Context, q agentskill.CommandDiscoverQuery) ([]agentskill.SkillCommand, error) {
	bin := strings.TrimSpace(q.CLIPath)
	if bin == "" {
		bin = "codex"
	}
	cwd := strings.TrimSpace(q.Cwd)
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	skills, err := d.skills(ctx, bin, cwd, codex.PluginEnabledConfig(q.EnabledPlugins))
	if err != nil {
		return []agentskill.SkillCommand{}, nil //nolint:nilerr // Skill suggestions are optional and fail open to normal message input.
	}
	seen := map[string]struct{}{}
	commands := []agentskill.SkillCommand{}
	for _, skill := range skills {
		name := strings.TrimSpace(skill.Name)
		if !skill.Enabled || name == "" {
			continue
		}
		if !agentskill.AppendUniqueName(seen, name) {
			continue
		}
		commands = append(commands, agentskill.SkillCommand{
			Name:        name,
			Description: strings.TrimSpace(skill.Description),
		})
	}
	return commands, nil
}
