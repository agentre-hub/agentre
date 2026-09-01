package ctlskill

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestAssets_ManifestsMatchConstants 守卫：嵌入的两份清单是合法 JSON，且其中的名字
// 与注册用的 Go 常量同源——名字漂了，`claude plugin list` 就报不出 agrctl@agentre。
func TestAssets_ManifestsMatchConstants(t *testing.T) {
	var plugin struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(assetPluginJSON()), &plugin); err != nil {
		t.Fatalf("plugin.json is not valid JSON: %v", err)
	}
	if plugin.Name != PluginName {
		t.Fatalf("plugin.json name = %q, want %q", plugin.Name, PluginName)
	}
	if plugin.Version != versionPlaceholder {
		t.Fatalf("plugin.json version = %q, want the %q placeholder", plugin.Version, versionPlaceholder)
	}

	var marketplace struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name    string `json:"name"`
			Source  string `json:"source"`
			Version string `json:"version"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal([]byte(assetMarketplaceJSON()), &marketplace); err != nil {
		t.Fatalf("marketplace.json is not valid JSON: %v", err)
	}
	if marketplace.Name != MarketplaceName {
		t.Fatalf("marketplace.json name = %q, want %q", marketplace.Name, MarketplaceName)
	}
	if len(marketplace.Plugins) != 1 {
		t.Fatalf("marketplace.json declares %d plugins, want exactly 1", len(marketplace.Plugins))
	}
	if marketplace.Plugins[0].Name != PluginName {
		t.Fatalf("marketplace plugin name = %q, want %q", marketplace.Plugins[0].Name, PluginName)
	}
	if want := "./" + PluginName; marketplace.Plugins[0].Source != want {
		t.Fatalf("marketplace plugin source = %q, want %q", marketplace.Plugins[0].Source, want)
	}
}

// TestAssets_RenderSubstitutesEveryPlaceholder 守卫：占位符存在于源文件、且渲染后不残留。
func TestAssets_RenderSubstitutesEveryPlaceholder(t *testing.T) {
	sources := map[string]string{
		"plugin.json":      assetPluginJSON(),
		"marketplace.json": assetMarketplaceJSON(),
		"SKILL.md":         assetSkillMD(),
		"commands.md":      assetCommandsMD(),
	}
	if !strings.Contains(sources["SKILL.md"], agrctlPlaceholder) {
		t.Fatalf("SKILL.md carries no %s placeholder", agrctlPlaceholder)
	}
	if !strings.Contains(sources["commands.md"], agrctlPlaceholder) {
		t.Fatalf("commands.md carries no %s placeholder", agrctlPlaceholder)
	}
	for name, source := range sources {
		rendered := render(source, "/data/bin/agrctl", "v9")
		if strings.Contains(rendered, "{{") {
			t.Fatalf("%s still carries a placeholder after render:\n%s", name, rendered)
		}
	}
	skill := render(sources["SKILL.md"], "/data/bin/agrctl", "v9")
	if !strings.Contains(skill, "/data/bin/agrctl") {
		t.Fatalf("rendered SKILL.md lost the agrctl path:\n%s", skill)
	}
}

// TestAssets_SkillDocumentsTheContract 守卫 spec「SKILL.md 的行为承诺」一节。
func TestAssets_SkillDocumentsTheContract(t *testing.T) {
	skill := assetSkillMD() + assetCommandsMD()
	for _, want := range []string{
		"ctl agents", "ctl projects", "ctl send",
		"--agent", "--agent-id", "--project", "--wait", "--isolated",
		"control endpoint not found — is the desktop app running?",
	} {
		if !strings.Contains(skill, want) {
			t.Fatalf("skill content does not document %q", want)
		}
	}
}

// TestAssets_SkillLeaksNoCredential 守卫：技能里既不出现凭据也不出现端点 URL，
// 两者都由 agrctl 自己从握手文件解析。
func TestAssets_SkillLeaksNoCredential(t *testing.T) {
	for name, source := range map[string]string{"SKILL.md": assetSkillMD(), "commands.md": assetCommandsMD()} {
		lower := strings.ToLower(source)
		for _, banned := range []string{"token", "http://", "https://", "localhost:", "127.0.0.1"} {
			if strings.Contains(lower, banned) {
				t.Fatalf("%s mentions %q; credentials and endpoints must stay out of the skill", name, banned)
			}
		}
	}
}
