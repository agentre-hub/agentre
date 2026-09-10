// Package codexskill 用 `codex plugin list --json` 发现该安装的插件技能包。
package codexskill

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/internal/pkg/clienv"
	"github.com/agentre-hub/agentre/pkg/codex"
)

func init() {
	agentskill.RegisterDiscoverer(agent_backend_entity.TypeCodex, Discoverer{})
}

type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)
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
//
// 必须经 clienv 解析 binary 并补齐 PATH,不能把裸名字丢给 exec:Finder / Dock 起的
// app bundle 只继承 launchd 的最小 PATH,而 codex 常装在 ~/.local/bin、Homebrew、
// volta 之类的目录里。本进程 PATH 查不到 → Discover 软降级成空发现 → 插件包整段
// 消失。跑 CLI 的 cliprocess 早就是这么解析的,发现这条路必须同源。
func (d Discoverer) runner() commandRunner {
	if d.run != nil {
		return d.run
	}
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		searchEnv := clienv.BuildEnv(nil, name)
		binary, ok := clienv.ResolveBinaryForEnv(name, searchEnv)
		if !ok {
			return nil, exec.ErrNotFound
		}
		//nolint:gosec // G204: binary 来自 agent backend 配置的 CLIPath(或类型默认名),经 clienv 解析,非请求输入
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = clienv.BuildEnv(nil, binary)
		return cmd.Output()
	}
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

func scanSkills(installPath string) []string {
	if installPath == "" {
		return nil
	}
	skillsDir := filepath.Join(installPath, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		dir := filepath.Join(skillsDir, e.Name())
		// 用 os.Stat(跟随软链):DirEntry.IsDir() 是 lstat 语义,会漏掉软链装进来的 skill。
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	return out
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
			if i := strings.Index(id, "@"); i > 0 {
				name = id[:i]
			}
		}
		out = append(out, agentskill.SkillPack{
			ID:              id,
			Name:            name,
			Skills:          scanSkills(strings.TrimSpace(r.Source.Path)),
			Source:          agentskill.SourceInstalled,
			Installed:       true,
			GloballyEnabled: r.Enabled,
		})
	}
	return out, nil
}

// Discover 调用 codex plugin list --json 枚举已安装插件。CLI 不可用时软降级返回空。
func (d Discoverer) Discover(ctx context.Context, q agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	bin := strings.TrimSpace(q.CLIPath)
	if bin == "" {
		bin = "codex"
	}
	b, err := d.runner()(ctx, bin, "plugin", "list", "--json")
	if err != nil {
		return []agentskill.SkillPack{}, nil //nolint:nilerr // CLI 不可用 → 软降级(空发现)
	}
	return parsePluginList(b)
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
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		commands = append(commands, agentskill.SkillCommand{
			Name:        name,
			Description: strings.TrimSpace(skill.Description),
		})
	}
	return commands, nil
}
