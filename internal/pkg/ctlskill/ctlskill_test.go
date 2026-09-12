package ctlskill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testAgrctl = "/tmp/agentre-data/bin/agrctl"

func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) // #nosec G304 -- 测试内部构造的 temp 路径。
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func readJSONMap(t *testing.T, path string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(readFileString(t, path)), &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

func writeFileString(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeJSONMap(t *testing.T, path string, v map[string]any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode %s: %v", path, err)
	}
	writeFileString(t, path, string(data))
}

func mustInstall(t *testing.T, home, version string) {
	t.Helper()
	if err := Install(Options{Home: home, AgrctlPath: testAgrctl, Version: version}); err != nil {
		t.Fatalf("Install(%s): %v", version, err)
	}
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestInstall_WritesBothForms(t *testing.T) {
	home := t.TempDir()
	mustInstall(t, home, "v1")

	marketplaceManifest := filepath.Join(MarketplaceDir(home), ".claude-plugin", "marketplace.json")
	pluginManifest := filepath.Join(PluginDir(home), ".claude-plugin", "plugin.json")
	pluginSkill := filepath.Join(PluginDir(home), "skills", PluginName, "SKILL.md")
	pluginRef := filepath.Join(PluginDir(home), "skills", PluginName, "references", "commands.md")
	universalSkill := filepath.Join(UniversalDir(home), "SKILL.md")
	universalRef := filepath.Join(UniversalDir(home), "references", "commands.md")

	for _, path := range []string{marketplaceManifest, pluginManifest, pluginSkill, pluginRef, universalSkill, universalRef} {
		if !exists(path) {
			t.Fatalf("missing installed file %s", path)
		}
	}

	skill := readFileString(t, pluginSkill)
	if !strings.Contains(skill, testAgrctl) {
		t.Fatalf("SKILL.md does not carry the injected agrctl path:\n%s", skill)
	}
	if strings.Contains(skill, "{{") {
		t.Fatalf("SKILL.md still carries a placeholder:\n%s", skill)
	}
	if got := readFileString(t, universalSkill); got != skill {
		t.Fatal("universal SKILL.md differs from the plugin SKILL.md")
	}

	manifest := readJSONMap(t, marketplaceManifest)
	if manifest["name"] != MarketplaceName {
		t.Fatalf("marketplace name = %v, want %q", manifest["name"], MarketplaceName)
	}
	if readJSONMap(t, pluginManifest)["name"] != PluginName {
		t.Fatalf("plugin name = %v, want %q", readJSONMap(t, pluginManifest)["name"], PluginName)
	}
	// 通用形态不带插件专有的 commands/。
	if exists(filepath.Join(UniversalDir(home), "commands")) {
		t.Fatal("universal form must not carry a commands/ directory")
	}
}

func TestInstall_RegistersPluginDisabled(t *testing.T) {
	home := t.TempDir()
	mustInstall(t, home, "v1")

	installed := readJSONMap(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	plugins, _ := installed["plugins"].(map[string]any)
	entries, _ := plugins[PluginID].([]any)
	if len(entries) != 1 {
		t.Fatalf("installed_plugins entries for %s = %v, want exactly one", PluginID, plugins[PluginID])
	}
	entry, _ := entries[0].(map[string]any)
	if entry["scope"] != "user" {
		t.Fatalf("entry scope = %v, want user", entry["scope"])
	}
	if entry["installPath"] != PluginDir(home) {
		t.Fatalf("entry installPath = %v, want %q", entry["installPath"], PluginDir(home))
	}
	if entry["version"] != "v1" {
		t.Fatalf("entry version = %v, want v1", entry["version"])
	}
	for _, key := range []string{"installedAt", "lastUpdated"} {
		ts, ok := entry[key].(string)
		if !ok || ts == "" {
			t.Fatalf("entry %s missing or not a non-empty string: %v", key, entry)
		}
	}

	known := readJSONMap(t, filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"))
	mkt, _ := known[MarketplaceName].(map[string]any)
	if mkt == nil {
		t.Fatalf("known_marketplaces missing %s: %v", MarketplaceName, known)
	}
	if mkt["installLocation"] != MarketplaceDir(home) {
		t.Fatalf("installLocation = %v, want %q", mkt["installLocation"], MarketplaceDir(home))
	}
	source, _ := mkt["source"].(map[string]any)
	if source["source"] != "directory" || source["path"] != MarketplaceDir(home) {
		t.Fatalf("marketplace source = %v", mkt["source"])
	}

	settings := readJSONMap(t, filepath.Join(home, ".claude", "settings.json"))
	enabled, _ := settings["enabledPlugins"].(map[string]any)
	got, ok := enabled[PluginID]
	if !ok {
		t.Fatalf("enabledPlugins missing %s: %v", PluginID, settings["enabledPlugins"])
	}
	if got != false {
		t.Fatalf("enabledPlugins[%s] = %v, want false", PluginID, got)
	}
	extra, _ := settings["extraKnownMarketplaces"].(map[string]any)
	if extra[MarketplaceName] == nil {
		t.Fatalf("extraKnownMarketplaces missing %s: %v", MarketplaceName, settings["extraKnownMarketplaces"])
	}
}

func TestInstall_KeepsForeignRegistryEntries(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	writeFileString(t, filepath.Join(pluginsDir, "installed_plugins.json"),
		`{"version":2,"plugins":{"other@shop":[{"scope":"user","installPath":"/keep","version":"9","extraField":"keep-me"}]}}`)
	writeFileString(t, filepath.Join(pluginsDir, "known_marketplaces.json"),
		`{"shop":{"installLocation":"/keep","source":{"source":"github","repo":"a/b"}}}`)
	writeFileString(t, filepath.Join(home, ".claude", "settings.json"),
		`{"model":"opus","enabledPlugins":{"other@shop":true},"extraKnownMarketplaces":{"shop":{"source":{"source":"github","repo":"a/b"}}}}`)

	mustInstall(t, home, "v1")

	installed := readJSONMap(t, filepath.Join(pluginsDir, "installed_plugins.json"))
	plugins, _ := installed["plugins"].(map[string]any)
	foreign, _ := plugins["other@shop"].([]any)
	if len(foreign) != 1 {
		t.Fatalf("foreign plugin entries lost: %v", plugins)
	}
	foreignEntry, _ := foreign[0].(map[string]any)
	if foreignEntry["installPath"] != "/keep" || foreignEntry["extraField"] != "keep-me" {
		t.Fatalf("foreign plugin entry mutated: %v", foreignEntry)
	}
	if plugins[PluginID] == nil {
		t.Fatal("our plugin was not registered alongside the foreign one")
	}

	known := readJSONMap(t, filepath.Join(pluginsDir, "known_marketplaces.json"))
	if known["shop"] == nil || known[MarketplaceName] == nil {
		t.Fatalf("known_marketplaces = %v, want both shop and %s", known, MarketplaceName)
	}

	settings := readJSONMap(t, filepath.Join(home, ".claude", "settings.json"))
	if settings["model"] != "opus" {
		t.Fatalf("unrelated setting lost: %v", settings)
	}
	enabled, _ := settings["enabledPlugins"].(map[string]any)
	if enabled["other@shop"] != true {
		t.Fatalf("foreign enabledPlugins entry lost: %v", enabled)
	}
	extra, _ := settings["extraKnownMarketplaces"].(map[string]any)
	if extra["shop"] == nil || extra[MarketplaceName] == nil {
		t.Fatalf("extraKnownMarketplaces = %v", extra)
	}
}

func TestInstall_SameVersionIsNoop(t *testing.T) {
	home := t.TempDir()
	mustInstall(t, home, "v1")

	pluginSkill := filepath.Join(PluginDir(home), "skills", PluginName, "SKILL.md")
	universalSkill := filepath.Join(UniversalDir(home), "SKILL.md")
	writeFileString(t, pluginSkill, "TOUCHED-PLUGIN")
	writeFileString(t, universalSkill, "TOUCHED-UNIVERSAL")

	mustInstall(t, home, "v1")

	if got := readFileString(t, pluginSkill); got != "TOUCHED-PLUGIN" {
		t.Fatalf("plugin SKILL.md rewritten on same-version reinstall")
	}
	if got := readFileString(t, universalSkill); got != "TOUCHED-UNIVERSAL" {
		t.Fatalf("universal SKILL.md rewritten on same-version reinstall")
	}
}

func TestInstall_VersionChangeRewrites(t *testing.T) {
	home := t.TempDir()
	mustInstall(t, home, "v1")

	pluginSkill := filepath.Join(PluginDir(home), "skills", PluginName, "SKILL.md")
	universalSkill := filepath.Join(UniversalDir(home), "SKILL.md")
	writeFileString(t, pluginSkill, "STALE")
	writeFileString(t, universalSkill, "STALE")

	mustInstall(t, home, "v2")

	if got := readFileString(t, pluginSkill); got == "STALE" {
		t.Fatal("plugin SKILL.md not rewritten on version bump")
	}
	if got := readFileString(t, universalSkill); got == "STALE" {
		t.Fatal("universal SKILL.md not rewritten on version bump")
	}
	installed := readJSONMap(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	plugins, _ := installed["plugins"].(map[string]any)
	entries, _ := plugins[PluginID].([]any)
	if len(entries) != 1 {
		t.Fatalf("version bump duplicated the registry entry: %v", plugins[PluginID])
	}
	entry, _ := entries[0].(map[string]any)
	if entry["version"] != "v2" {
		t.Fatalf("registry version = %v, want v2", entry["version"])
	}
}

func TestInstall_SkipsSymlinkedPluginTarget(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeFileString(t, filepath.Join(repo, "marker.txt"), "REPO-SOURCE")
	if err := os.MkdirAll(filepath.Dir(MarketplaceDir(home)), 0o755); err != nil {
		t.Fatalf("mkdir marketplaces: %v", err)
	}
	if err := os.Symlink(repo, MarketplaceDir(home)); err != nil {
		t.Fatalf("symlink marketplace dir: %v", err)
	}

	mustInstall(t, home, "v1")

	if exists(filepath.Join(repo, ".claude-plugin")) || exists(filepath.Join(repo, PluginName)) {
		t.Fatal("install wrote through the symlink into the source tree")
	}
	if exists(filepath.Join(home, ".claude", "plugins", "installed_plugins.json")) {
		t.Fatal("skipped plugin form must not register anything")
	}
	// 通用形态不受影响，仍然安装。
	if !exists(filepath.Join(UniversalDir(home), "SKILL.md")) {
		t.Fatal("universal form must still install when only the plugin target is symlinked")
	}
}

func TestInstall_CorruptSettingsErrorsWithoutClobbering(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	writeFileString(t, filepath.Join(pluginsDir, "installed_plugins.json"),
		`{"version":2,"plugins":{"other@shop":[{"scope":"user","installPath":"/keep"}]}}`)
	writeFileString(t, filepath.Join(home, ".claude", "settings.json"), "{ this is not json")

	err := Install(Options{Home: home, AgrctlPath: testAgrctl, Version: "v1"})
	if err == nil {
		t.Fatal("Install with corrupt settings.json returned nil, want error")
	}

	installed := readJSONMap(t, filepath.Join(pluginsDir, "installed_plugins.json"))
	plugins, _ := installed["plugins"].(map[string]any)
	if plugins["other@shop"] == nil {
		t.Fatalf("foreign plugin entry lost after failed install: %v", installed)
	}
	if got := readFileString(t, filepath.Join(home, ".claude", "settings.json")); got != "{ this is not json" {
		t.Fatalf("corrupt settings.json was overwritten: %q", got)
	}
}

func TestInstall_UnwritableTargetErrors(t *testing.T) {
	home := t.TempDir()
	// ~/.agents 是普通文件 → 通用形态建目录必失败。
	writeFileString(t, filepath.Join(home, ".agents"), "not a directory")

	if err := Install(Options{Home: home, AgrctlPath: testAgrctl, Version: "v1"}); err == nil {
		t.Fatal("Install onto an unwritable target returned nil, want error")
	}
}

func TestInstall_RequiresHomeAndAgrctlPath(t *testing.T) {
	if err := Install(Options{Home: "", AgrctlPath: testAgrctl, Version: "v1"}); err == nil {
		t.Fatal("Install with empty home returned nil, want error")
	}
	if err := Install(Options{Home: t.TempDir(), AgrctlPath: "", Version: "v1"}); err == nil {
		t.Fatal("Install with empty agrctl path returned nil, want error")
	}
}

func TestUninstall_RemovesTreesAndOnlyOurKeys(t *testing.T) {
	home := t.TempDir()
	pluginsDir := filepath.Join(home, ".claude", "plugins")
	writeFileString(t, filepath.Join(pluginsDir, "installed_plugins.json"),
		`{"version":2,"plugins":{"other@shop":[{"scope":"user","installPath":"/keep"}]}}`)
	writeFileString(t, filepath.Join(pluginsDir, "known_marketplaces.json"), `{"shop":{"installLocation":"/keep"}}`)
	writeFileString(t, filepath.Join(home, ".claude", "settings.json"),
		`{"model":"opus","enabledPlugins":{"other@shop":true},"extraKnownMarketplaces":{"shop":{}}}`)
	mustInstall(t, home, "v1")

	if err := Uninstall(home); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if exists(MarketplaceDir(home)) {
		t.Fatal("marketplace tree still present after uninstall")
	}
	if exists(UniversalDir(home)) {
		t.Fatal("universal skill tree still present after uninstall")
	}

	installed := readJSONMap(t, filepath.Join(pluginsDir, "installed_plugins.json"))
	plugins, _ := installed["plugins"].(map[string]any)
	if plugins[PluginID] != nil {
		t.Fatalf("installed_plugins still carries %s: %v", PluginID, plugins)
	}
	if plugins["other@shop"] == nil {
		t.Fatalf("uninstall dropped the foreign plugin entry: %v", plugins)
	}

	known := readJSONMap(t, filepath.Join(pluginsDir, "known_marketplaces.json"))
	if known[MarketplaceName] != nil {
		t.Fatalf("known_marketplaces still carries %s: %v", MarketplaceName, known)
	}
	if known["shop"] == nil {
		t.Fatalf("uninstall dropped the foreign marketplace: %v", known)
	}

	settings := readJSONMap(t, filepath.Join(home, ".claude", "settings.json"))
	enabled, _ := settings["enabledPlugins"].(map[string]any)
	if _, ok := enabled[PluginID]; ok {
		t.Fatalf("enabledPlugins still carries %s: %v", PluginID, enabled)
	}
	if enabled["other@shop"] != true {
		t.Fatalf("uninstall dropped the foreign enabledPlugins entry: %v", enabled)
	}
	extra, _ := settings["extraKnownMarketplaces"].(map[string]any)
	if extra[MarketplaceName] != nil {
		t.Fatalf("extraKnownMarketplaces still carries %s: %v", MarketplaceName, extra)
	}
	if extra["shop"] == nil {
		t.Fatalf("uninstall dropped the foreign extraKnownMarketplaces entry: %v", extra)
	}
	if settings["model"] != "opus" {
		t.Fatalf("uninstall dropped an unrelated setting: %v", settings)
	}
}

func TestUninstall_OnCleanHomeIsNoop(t *testing.T) {
	home := t.TempDir()
	if err := Uninstall(home); err != nil {
		t.Fatalf("Uninstall on clean home: %v", err)
	}
}

func TestUninstall_SkipsSymlinkedTree(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	writeFileString(t, filepath.Join(repo, "marker.txt"), "REPO-SOURCE")
	if err := os.MkdirAll(filepath.Dir(MarketplaceDir(home)), 0o755); err != nil {
		t.Fatalf("mkdir marketplaces: %v", err)
	}
	if err := os.Symlink(repo, MarketplaceDir(home)); err != nil {
		t.Fatalf("symlink marketplace dir: %v", err)
	}

	if err := Uninstall(home); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}

	if !exists(filepath.Join(repo, "marker.txt")) {
		t.Fatal("uninstall deleted through the symlink into the source tree")
	}
}

func TestUninstall_CorruptRegistryErrors(t *testing.T) {
	home := t.TempDir()
	writeFileString(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), "{ nope")

	if err := Uninstall(home); err == nil {
		t.Fatal("Uninstall with corrupt installed_plugins.json returned nil, want error")
	}
}

// TestInstall_KeepsUserEnabledPluginToggle 用户在 Claude Code 里手动打开过这个插件，
// 之后的版本升级重新登记不能把他的选择拍回 false。
func TestInstall_KeepsUserEnabledPluginToggle(t *testing.T) {
	home := t.TempDir()
	mustInstall(t, home, "v1")

	settingsFile := filepath.Join(home, ".claude", "settings.json")
	config := readJSONMap(t, settingsFile)
	enabled, _ := config["enabledPlugins"].(map[string]any)
	enabled[PluginID] = true
	writeJSONMap(t, settingsFile, config)

	mustInstall(t, home, "v2")

	got, _ := readJSONMap(t, settingsFile)["enabledPlugins"].(map[string]any)
	if got[PluginID] != true {
		t.Fatalf("enabledPlugins[%s] = %v, want the user's own true preserved", PluginID, got[PluginID])
	}
}

// TestInstall_RepairsDroppedRegistryEntry 树和版本标记都还在、注册表里的键被外部摘掉了
// （用户在 CLI 里摘插件、settings.json 从备份恢复）时，同版本再装一次要把登记补回来——
// 否则设置页永远报「已安装」，而 claude 那边永远看不见这个插件。
func TestInstall_RepairsDroppedRegistryEntry(t *testing.T) {
	home := t.TempDir()
	mustInstall(t, home, "v1")

	knownPath := filepath.Join(home, ".claude", "plugins", "known_marketplaces.json")
	known := readJSONMap(t, knownPath)
	delete(known, MarketplaceName)
	writeJSONMap(t, knownPath, known)

	settingsFile := filepath.Join(home, ".claude", "settings.json")
	config := readJSONMap(t, settingsFile)
	extra, _ := config["extraKnownMarketplaces"].(map[string]any)
	delete(extra, MarketplaceName)
	writeJSONMap(t, settingsFile, config)

	mustInstall(t, home, "v1")

	if readJSONMap(t, knownPath)[MarketplaceName] == nil {
		t.Fatal("same-version install did not restore the dropped known_marketplaces entry")
	}
	restored, _ := readJSONMap(t, settingsFile)["extraKnownMarketplaces"].(map[string]any)
	if restored[MarketplaceName] == nil {
		t.Fatal("same-version install did not restore the dropped extraKnownMarketplaces entry")
	}
}

// TestInstall_RequiresVersion 版本号会原样写进 plugin.json / marketplace.json 的 version
// 字段，空串是一份坏清单——和 home / agrctl 路径一样，它是必填输入。
func TestInstall_RequiresVersion(t *testing.T) {
	if err := Install(Options{Home: t.TempDir(), AgrctlPath: testAgrctl, Version: ""}); err == nil {
		t.Fatal("Install with empty version returned nil, want error")
	}
}

func TestStatus_ReportsBothForms(t *testing.T) {
	home := t.TempDir()

	got := Status(home)
	if got.PluginInstalled || got.UniversalInstalled {
		t.Fatalf("clean home reported installed: %+v", got)
	}
	if got.PluginPath != PluginDir(home) || got.UniversalPath != UniversalDir(home) {
		t.Fatalf("Status paths = %+v", got)
	}

	// 半装：只有通用目录。
	writeFileString(t, filepath.Join(UniversalDir(home), "SKILL.md"), "x")
	got = Status(home)
	if got.PluginInstalled {
		t.Fatalf("plugin reported installed with only the universal form: %+v", got)
	}
	if !got.UniversalInstalled {
		t.Fatalf("universal form not reported installed: %+v", got)
	}

	mustInstall(t, home, "v1")
	got = Status(home)
	if !got.PluginInstalled || !got.UniversalInstalled {
		t.Fatalf("full install not reported: %+v", got)
	}
}
