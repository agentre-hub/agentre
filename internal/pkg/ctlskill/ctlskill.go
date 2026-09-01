// Package ctlskill lays the `agrctl ctl` control channel down on disk as an agent
// skill, in the two shapes the hosts on this machine read:
//
//   - a Claude Code plugin under ~/.claude/plugins/marketplaces/agentre/agrctl,
//     registered (globally disabled) in the CLI's three user-level JSON files so it
//     shows up in `claude plugin list --json` and can be granted per agent;
//   - a plain Agent Skills directory under ~/.agents/skills/agrctl, which codex / pi /
//     cursor and friends pick up on their next start.
//
// Both shapes are expanded from one embedded source tree, so their SKILL.md is byte
// identical, with the local agrctl absolute path injected.
//
// This is a leaf package: it takes the home directory, the agrctl path and the app
// version as inputs and touches nothing else. Callers decide *when* to install and
// degrade any error into a warning — see the service layer.
package ctlskill

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"
)

const (
	// PluginName 插件名，同时是技能目录名。
	PluginName = "agrctl"
	// MarketplaceName 承载该插件的本地 marketplace 名。
	MarketplaceName = "agentre"
	// PluginID Claude Code 用来标识插件的 `<plugin>@<marketplace>` 键。
	PluginID = PluginName + "@" + MarketplaceName
	// stampName 版本标记文件名，落在每种形态的根目录下。
	stampName = ".agentre-install.json"
)

// Options 一次安装所需的全部输入。
type Options struct {
	// Home 用户 home 目录（显式注入，安装路径不读进程环境）。
	Home string
	// AgrctlPath 本机 agrctl 的绝对路径，注入进 SKILL.md。
	AgrctlPath string
	// Version 当前应用版本，作为幂等标记与注册表里的插件版本。
	Version string
}

// Info 两种形态各自的安装态与路径。半装（只有通用目录）是合法状态。
type Info struct {
	PluginID           string `json:"pluginId"`
	PluginInstalled    bool   `json:"pluginInstalled"`
	PluginPath         string `json:"pluginPath"`
	UniversalInstalled bool   `json:"universalInstalled"`
	UniversalPath      string `json:"universalPath"`
}

// MarketplaceDir 本地 marketplace 根 ~/.claude/plugins/marketplaces/agentre。
func MarketplaceDir(home string) string {
	return filepath.Join(home, ".claude", "plugins", "marketplaces", MarketplaceName)
}

// PluginDir 插件根 ~/.claude/plugins/marketplaces/agentre/agrctl。
func PluginDir(home string) string {
	return filepath.Join(MarketplaceDir(home), PluginName)
}

// UniversalDir 通用技能目录 ~/.agents/skills/agrctl。
func UniversalDir(home string) string {
	return filepath.Join(home, ".agents", "skills", PluginName)
}

// pluginsDir ~/.claude/plugins，三份注册文件里的两份住在这里。
func pluginsDir(home string) string {
	return filepath.Join(home, ".claude", "plugins")
}

// settingsPath ~/.claude/settings.json。
func settingsPath(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}

// Install 展开两种形态并登记插件。版本标记一致的形态原样跳过；目标路径经软链的形态
// 跳过（开发机会把 marketplace 软链到仓库源码树，写回去会污染仓库）。
func Install(opts Options) error {
	home := strings.TrimSpace(opts.Home)
	if home == "" {
		return errors.New("ctlskill: home directory is required")
	}
	if strings.TrimSpace(opts.AgrctlPath) == "" {
		return errors.New("ctlskill: agrctl path is required")
	}
	if err := installUniversal(home, opts); err != nil {
		return err
	}
	return installPlugin(home, opts)
}

// installUniversal 写 ~/.agents/skills/agrctl（SKILL.md + references），不写任何注册文件。
func installUniversal(home string, opts Options) error {
	root := UniversalDir(home)
	if traversesSymlink(home, root) {
		logger.Default().Info("ctlskill.Install: skip universal skill, target traverses symlink",
			zap.String("path", root))
		return nil
	}
	if stampCurrent(root, opts) {
		return nil
	}
	files := map[string]string{
		filepath.Join(root, "SKILL.md"):                  render(assetSkillMD(), opts.AgrctlPath, opts.Version),
		filepath.Join(root, "references", "commands.md"): render(assetCommandsMD(), opts.AgrctlPath, opts.Version),
	}
	if err := writeFiles(files); err != nil {
		return err
	}
	return writeStamp(root, opts)
}

// installPlugin 写 marketplace 清单 + 插件根，再把插件登记进三份用户级 JSON。
func installPlugin(home string, opts Options) error {
	marketplace := MarketplaceDir(home)
	root := PluginDir(home)
	if traversesSymlink(home, marketplace) || traversesSymlink(home, root) {
		logger.Default().Info("ctlskill.Install: skip claude plugin, target traverses symlink",
			zap.String("path", marketplace))
		return nil
	}
	if stampCurrent(root, opts) {
		return nil
	}
	skillDir := filepath.Join(root, "skills", PluginName)
	files := map[string]string{
		filepath.Join(marketplace, ".claude-plugin", "marketplace.json"): render(assetMarketplaceJSON(), opts.AgrctlPath, opts.Version),
		filepath.Join(root, ".claude-plugin", "plugin.json"):             render(assetPluginJSON(), opts.AgrctlPath, opts.Version),
		filepath.Join(skillDir, "SKILL.md"):                              render(assetSkillMD(), opts.AgrctlPath, opts.Version),
		filepath.Join(skillDir, "references", "commands.md"):             render(assetCommandsMD(), opts.AgrctlPath, opts.Version),
	}
	if err := writeFiles(files); err != nil {
		return err
	}
	if err := registerPlugin(home, opts); err != nil {
		return err
	}
	return writeStamp(root, opts)
}

// Uninstall 删掉两棵已铺的树，并从三份注册文件里只摘掉本插件/本 marketplace 的键。
// 用户已有的逐档授权（agents 的 skills_json）不由这里处理。
func Uninstall(home string) error {
	home = strings.TrimSpace(home)
	if home == "" {
		return errors.New("ctlskill: home directory is required")
	}
	if err := removeOwnedDir(home, MarketplaceDir(home), MarketplaceName); err != nil {
		return err
	}
	if err := removeOwnedDir(home, UniversalDir(home), PluginName); err != nil {
		return err
	}
	return unregisterPlugin(home)
}

// Status 报出两种形态各自的安装态与路径。
func Status(home string) Info {
	home = strings.TrimSpace(home)
	info := Info{
		PluginID:      PluginID,
		PluginPath:    PluginDir(home),
		UniversalPath: UniversalDir(home),
	}
	if home == "" {
		return info
	}
	info.PluginInstalled = fileExists(filepath.Join(PluginDir(home), ".claude-plugin", "plugin.json"))
	info.UniversalInstalled = fileExists(filepath.Join(UniversalDir(home), "SKILL.md"))
	return info
}

// stamp 版本标记文件的内容：版本或 agrctl 路径变了都要重写整棵树。
type stamp struct {
	Version    string `json:"version"`
	AgrctlPath string `json:"agrctlPath"`
}

// stampCurrent 该形态已按同一版本、同一 agrctl 路径铺好。
func stampCurrent(root string, opts Options) bool {
	b, err := os.ReadFile(filepath.Join(root, stampName)) // #nosec G304 -- root 由 home 拼接的固定安装路径，非用户输入。
	if err != nil {
		return false
	}
	var got stamp
	if err := json.Unmarshal(b, &got); err != nil {
		return false
	}
	return got.Version == opts.Version && got.AgrctlPath == opts.AgrctlPath
}

func writeStamp(root string, opts Options) error {
	return writeJSONFile(filepath.Join(root, stampName), stamp{Version: opts.Version, AgrctlPath: opts.AgrctlPath})
}

// writeFiles 建目录并写下全部文件。
func writeFiles(files map[string]string) error {
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("ctlskill: create %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("ctlskill: write %s: %w", path, err)
		}
	}
	return nil
}

func writeJSONFile(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("ctlskill: encode %s: %w", filepath.Base(path), err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ctlskill: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("ctlskill: write %s: %w", path, err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// traversesSymlink 判断 root 与 path 之间（含 path 自身、不含 root）是否有软链接。
// 只从 root 往下看：home 之上的祖先常常本身就是软链（macOS 的 /var → /private/var），
// 那与「开发者把安装目标软链到源码树」无关。
func traversesSymlink(root, path string) bool {
	root = filepath.Clean(root)
	p := filepath.Clean(path)
	for p != root && strings.HasPrefix(p, root+string(os.PathSeparator)) {
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return false
}

// removeOwnedDir 删掉一棵本包铺出来的树。删之前确认它就是我们那个名字、且在 home 之内；
// 目标经软链时跳过删除，否则会删穿到开发者的源码树。
func removeOwnedDir(home, path, expectedBase string) error {
	clean := filepath.Clean(path)
	if filepath.Base(clean) != expectedBase {
		return fmt.Errorf("ctlskill: refuse to remove non-%s path: %s", expectedBase, clean)
	}
	rel, err := filepath.Rel(filepath.Clean(home), clean)
	if err != nil {
		return fmt.Errorf("ctlskill: check remove target %s: %w", clean, err)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("ctlskill: refuse to remove path outside home: %s", clean)
	}
	if traversesSymlink(home, clean) {
		logger.Default().Info("ctlskill.Uninstall: skip removal, target traverses symlink",
			zap.String("path", clean))
		return nil
	}
	if err := os.RemoveAll(clean); err != nil {
		return fmt.Errorf("ctlskill: remove %s: %w", clean, err)
	}
	return nil
}
