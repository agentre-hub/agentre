package claudeskill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
)

// knownMarketplaces 映射 ~/.claude/plugins/known_marketplaces.json:
// marketplace 名 → 本地安装位置。
type knownMarketplaces map[string]struct {
	InstallLocation string `json:"installLocation"`
}

// marketplaceManifest 映射 <marketplace>/.claude-plugin/marketplace.json,
// 只取本地插件源:source 为字符串时即插件根(相对 marketplace 目录)。
type marketplaceManifest struct {
	Plugins []struct {
		Name   string          `json:"name"`
		Source json.RawMessage `json:"source"`
	} `json:"plugins"`
}

// splitPluginID 拆 `<plugin>@<marketplace>`。裸 id(无 @)→ 名即 id,marketplace 为空。
func splitPluginID(id string) (name, marketplace string) {
	id = strings.TrimSpace(id)
	if name, marketplace, ok := strings.Cut(id, "@"); ok && name != "" {
		return name, marketplace
	}
	return id, ""
}

// defaultPluginsDir 返回 ~/.claude/plugins。
func defaultPluginsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "plugins")
}

func (d Discoverer) pluginsRoot() string {
	if d.pluginsDir != nil {
		return d.pluginsDir()
	}
	return defaultPluginsDir()
}

// pluginRoot 定位插件真实的安装根目录。CLI 报的 installPath 优先;它没落地时
// (directory 型 marketplace 装出来的插件,CLI 只记了一个从未写入的 cache 路径)
// 按 id 的 @marketplace 段回落到 marketplace 清单声明的 source 目录 —— 与 CLI
// 自己的加载口径一致。都不成立 → 空,由调用方降级成无 skill。
func (d Discoverer) pluginRoot(r rawPlugin) string {
	if installPath := strings.TrimSpace(r.InstallPath); agentskill.IsDir(installPath) {
		return installPath
	}
	return d.marketplacePluginRoot(r.ID)
}

func (d Discoverer) marketplacePluginRoot(id string) string {
	name, marketplace := splitPluginID(id)
	if name == "" || marketplace == "" {
		return ""
	}
	location := d.marketplaceLocation(marketplace)
	if location == "" {
		return ""
	}
	source := manifestSource(location, name)
	switch {
	case source == "":
		return ""
	case filepath.IsAbs(source):
		return source
	default:
		return filepath.Join(location, source)
	}
}

// marketplaceLocation 查 marketplace 的本地安装位置(本身可能是软链)。
func (d Discoverer) marketplaceLocation(marketplace string) string {
	pluginsDir := strings.TrimSpace(d.pluginsRoot())
	if pluginsDir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(pluginsDir, "known_marketplaces.json"))
	if err != nil {
		return ""
	}
	var known knownMarketplaces
	if err := json.Unmarshal(b, &known); err != nil {
		return ""
	}
	return strings.TrimSpace(known[marketplace].InstallLocation)
}

// manifestSource 取 marketplace 清单里该插件声明的源路径。远端 source(对象形式)
// 没有本地路径 → 空;那类插件的 cache installPath 本来就落地了,走不到这里。
func manifestSource(location, plugin string) string {
	b, err := os.ReadFile(filepath.Join(location, ".claude-plugin", "marketplace.json"))
	if err != nil {
		return ""
	}
	var manifest marketplaceManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return ""
	}
	for _, p := range manifest.Plugins {
		if strings.TrimSpace(p.Name) != plugin {
			continue
		}
		var source string
		if err := json.Unmarshal(p.Source, &source); err != nil {
			return ""
		}
		return strings.TrimSpace(source)
	}
	return ""
}
