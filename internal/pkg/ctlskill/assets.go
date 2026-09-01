package ctlskill

import (
	_ "embed"
	"strings"
)

// 嵌入的插件源码树。两种落地形态（Claude Code 插件 / 通用 ~/.agents/skills 目录）
// 都从这一份源展开，SKILL.md 与 references 因此永远一致。
var (
	//go:embed assets/plugin/.claude-plugin/plugin.json
	pluginJSONSource string
	//go:embed assets/marketplace/.claude-plugin/marketplace.json
	marketplaceJSONSource string
	//go:embed assets/plugin/skills/agrctl/SKILL.md
	skillMDSource string
	//go:embed assets/plugin/skills/agrctl/references/commands.md
	commandsMDSource string
)

// 渲染占位符：安装时注入 agrctl 的绝对路径与当前应用版本。
const (
	agrctlPlaceholder  = "{{AGRCTL_PATH}}"
	versionPlaceholder = "{{VERSION}}"
)

func assetPluginJSON() string      { return pluginJSONSource }
func assetMarketplaceJSON() string { return marketplaceJSONSource }
func assetSkillMD() string         { return skillMDSource }
func assetCommandsMD() string      { return commandsMDSource }

// render 把源文件里的占位符换成本机的真实值。
func render(source, agrctlPath, version string) string {
	return strings.NewReplacer(
		agrctlPlaceholder, agrctlPath,
		versionPlaceholder, version,
	).Replace(source)
}
