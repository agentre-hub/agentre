// Package claudeskill 用 `claude plugin list --json` 发现该安装的技能包。
package claudeskill

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
)

func init() {
	agentskill.RegisterDiscoverer(agent_backend_entity.TypeClaudeCode, Discoverer{})
}

// commandRunner 执行 CLI 并返回 stdout。注入接缝:单测替换为假命令,免依赖真实 claude 二进制。
type commandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Discoverer 用 claude CLI 枚举已安装技能包。run 为 nil 时走真实 exec(生产默认)。
type Discoverer struct {
	run        commandRunner
	skillRoots func(cwd string) []string
	pluginsDir func() string
}

// runner 取命令执行器:未注入 → 真实 exec 调用(生产默认)。
//
// 必须经 clienv 解析 binary 并补齐 PATH,不能把裸名字丢给 exec:Finder / Dock 起的
// app bundle 只继承 launchd 的最小 PATH,而 claude 常装在 ~/.local/bin、Homebrew、
// volta 之类的目录里。本进程 PATH 查不到 → Discover 软降级成空发现 → 插件包整段
// 消失。真正跑 CLI 的 pkg/claudecode 早就是这么解析的,发现这条路必须同源。
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
		// #nosec G204 -- binary 来自 agent backend 配置的 CLIPath(或类型默认名),
		// 经 clienv 解析,不接受用户输入。
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = clienv.BuildEnv(nil, binary)
		return cmd.Output()
	}
}

// rawPlugin 映射 `claude plugin list --json` 单元素。Enabled = CLI 全局启用态
// (透出到 SkillPack.GloballyEnabled,供"继承"模型判定);Scope 暂不消费。
type rawPlugin struct {
	ID          string `json:"id"`
	Enabled     bool   `json:"enabled"`
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"` // 用于枚举包内 skill
}

// scanSkills 枚举 plugin 安装目录下 skills/*/SKILL.md,返回 skill 名(目录名,
// os.ReadDir 已按名排序)。installPath 为空 / 无 skills 目录 / 不可读 → nil,不阻断发现。
func scanSkills(installPath string) []string {
	if installPath == "" {
		return nil
	}
	return scanSkillRoot(filepath.Join(installPath, "skills"))
}

func scanSkillRoot(skillsDir string) []string {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		dir := filepath.Join(skillsDir, e.Name())
		if !isDir(dir) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			continue // 没有 SKILL.md 的子目录不是 skill
		}
		out = append(out, e.Name())
	}
	return out
}

// isDir 判断路径是否为目录。必须用 os.Stat(跟随软链):os.ReadDir 给的
// DirEntry.IsDir() 是 lstat 语义,会把软链装进来的 skill 目录判成非目录。
func isDir(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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
		for _, name := range scanSkillRoot(root) {
			if _, ok := seen[name]; ok {
				continue
			}
			seen[name] = struct{}{}
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
	bin := strings.TrimSpace(q.CLIPath)
	if bin == "" {
		bin = "claude"
	}
	b, err := d.runner()(ctx, bin, "plugin", "list", "--json")
	if err != nil {
		return []agentskill.SkillPack{}, nil // CLI 不可用 → 软降级(空发现)
	}
	return d.parsePluginList(b)
}
