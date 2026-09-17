package agentskill

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/clienv"
)

// CommandRunner 执行 CLI 并返回 stdout。测试注入假命令,生产走 ExecRunner。
type CommandRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// ExecRunner 是真实的命令执行器:经 clienv 解析 binary 并补齐 PATH,不能把裸名字
// 丢给 exec —— Finder / Dock 起的 app bundle 只继承 launchd 的最小 PATH,而 CLI 常
// 装在 ~/.local/bin、Homebrew、volta 之类的目录里。本进程 PATH 查不到 → Discover
// 软降级成空发现 → 插件包整段消失。
func ExecRunner() CommandRunner {
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

// DiscoverPluginList 跑 `<bin> plugin list --json`,把 stdout 交给 parse。CLI 不可用
// → 软降级返回空发现,不向上报错。bin 为空时用 defaultBinary。
func DiscoverPluginList(ctx context.Context, runner CommandRunner, cliPath, defaultBinary string, parse func([]byte) ([]SkillPack, error)) ([]SkillPack, error) {
	bin := strings.TrimSpace(cliPath)
	if bin == "" {
		bin = defaultBinary
	}
	b, err := runner(ctx, bin, "plugin", "list", "--json")
	if err != nil {
		return []SkillPack{}, nil //nolint:nilerr // CLI 不可用 → 软降级(空发现)
	}
	return parse(b)
}

// ScanSkills 枚举 installPath/skills/*/SKILL.md,返回 skill 名(目录名,os.ReadDir
// 已按名排序)。installPath 为空 / 无 skills 目录 / 不可读 → nil,不阻断发现。
func ScanSkills(installPath string) []string {
	if installPath == "" {
		return nil
	}
	return ScanSkillRoot(filepath.Join(installPath, "skills"))
}

// ScanSkillRoot 枚举 skillsDir 下带 SKILL.md 的子目录。
func ScanSkillRoot(skillsDir string) []string {
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		dir := filepath.Join(skillsDir, e.Name())
		if !IsDir(dir) {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			continue // 没有 SKILL.md 的子目录不是 skill
		}
		out = append(out, e.Name())
	}
	return out
}

// IsDir 判断路径是否为目录。必须用 os.Stat(跟随软链):os.ReadDir 给的
// DirEntry.IsDir() 是 lstat 语义,会把软链装进来的 skill 目录判成非目录。
func IsDir(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// AppendUniqueName 把 name 记进 seen;首次出现返回 true。三个 DiscoverCommands
// 共用同一条「跳过重复名」的判据。
func AppendUniqueName(seen map[string]struct{}, name string) bool {
	if _, ok := seen[name]; ok {
		return false
	}
	seen[name] = struct{}{}
	return true
}
