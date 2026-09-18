// Package claudeskill 用 `claude plugin list --json` 发现该安装的技能包。
package claudeskill

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
)

func init() {
	agentskill.RegisterDiscoverer(agent_backend_entity.TypeClaudeCode, Discoverer{})
}

// commandRunner 执行 CLI 并返回 stdout。注入接缝:单测替换为假命令,免依赖真实 claude 二进制。
type commandRunner = agentskill.CommandRunner

// Discoverer 用 claude CLI 枚举已安装技能包。run 为 nil 时走真实 exec(生产默认)。
type Discoverer struct {
	run        commandRunner
	skillRoots func(cwd string) []string
	pluginsDir func() string
}

// runner 取命令执行器:未注入 → 真实 exec 调用(生产默认)。
func (d Discoverer) runner() commandRunner {
	if d.run != nil {
		return d.run
	}
	return agentskill.ExecRunner()
}

// rawPlugin 映射 `claude plugin list --json` 单元素。Enabled = CLI 全局启用态
// (透出到 SkillPack.GloballyEnabled,供"继承"模型判定)。
type rawPlugin struct {
	ID          string `json:"id"`
	Enabled     bool   `json:"enabled"`
	InstallPath string `json:"installPath"` // 用于枚举包内 skill
}

// scanSkills 枚举 plugin 安装目录下 skills/*/SKILL.md。
func scanSkills(installPath string) []string {
	return agentskill.ScanSkills(installPath)
}

func defaultSkillRoots(cwd string) []string {
	var roots []string
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		roots = append(roots, filepath.Join(home, ".claude", "skills"))
	}
	if strings.TrimSpace(cwd) != "" {
		roots = append(roots, filepath.Join(strings.TrimSpace(cwd), ".claude", "skills"))
	}
	return roots
}

// DiscoverCommands enumerates standalone Claude Code skills. Plugin skills are
// merged separately by skill_svc so per-agent plugin overrides remain authoritative.
func (d Discoverer) DiscoverCommands(_ context.Context, q agentskill.CommandDiscoverQuery) ([]agentskill.SkillCommand, error) {
	roots := defaultSkillRoots(q.Cwd)
	if d.skillRoots != nil {
		roots = d.skillRoots(q.Cwd)
	}
	seen := map[string]struct{}{}
	commands := []agentskill.SkillCommand{}
	for _, root := range roots {
		for _, name := range agentskill.ScanSkillRoot(root) {
			if !agentskill.AppendUniqueName(seen, name) {
				continue
			}
			commands = append(commands, agentskill.SkillCommand{Name: name})
		}
	}
	return commands, nil
}

func (d Discoverer) parsePluginList(b []byte) ([]agentskill.SkillPack, error) {
	out := []agentskill.SkillPack{}
	if len(b) == 0 {
		return out, nil
	}
	var raws []rawPlugin
	if err := json.Unmarshal(b, &raws); err != nil {
		return out, nil // 坏 JSON 视为无发现,不阻断
	}
	for _, r := range raws {
		name, _ := splitPluginID(r.ID)
		out = append(out, agentskill.SkillPack{
			ID:              r.ID,
			Name:            name,
			Skills:          scanSkills(d.pluginRoot(r)),
			Source:          agentskill.SourceInstalled,
			Installed:       true,
			GloballyEnabled: r.Enabled,
		})
	}
	return out, nil
}

// Discover 调用 claude plugin list --json 枚举已安装技能包。CLI 不可用时软降级返回空。
func (d Discoverer) Discover(ctx context.Context, q agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	return agentskill.DiscoverPluginList(ctx, d.runner(), q.CLIPath, "claude", d.parsePluginList)
}
