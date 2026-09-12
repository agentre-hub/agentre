package codexskill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/pkg/codex"
)

func mustCodexSkill(t *testing.T, skillsDir, name string) {
	t.Helper()
	dir := filepath.Join(skillsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParsePluginList(t *testing.T) {
	Convey("解析 codex plugin list --json", t, func() {
		root := t.TempDir()
		skills := filepath.Join(root, "skills")
		mustCodexSkill(t, skills, "browser")
		mustCodexSkill(t, skills, "qa")
		raw := []byte(`{
		  "installed": [
		    {
		      "pluginId": "browser@openai-bundled",
		      "name": "browser",
		      "enabled": true,
		      "source": {"path": "` + root + `"}
		    },
		    {
		      "pluginId": "superpowers@openai-curated",
		      "name": "superpowers",
		      "enabled": false
		    }
		  ],
		  "available": [
		    {"pluginId": "future@openai-curated", "name": "future", "installed": false}
		  ]
		}`)

		packs, err := parsePluginList(raw)
		So(err, ShouldBeNil)
		So(len(packs), ShouldEqual, 2)
		So(packs[0].ID, ShouldEqual, "browser@openai-bundled")
		So(packs[0].Name, ShouldEqual, "browser")
		So(packs[0].Installed, ShouldBeTrue)
		So(packs[0].Source, ShouldEqual, agentskill.SourceInstalled)
		So(packs[0].GloballyEnabled, ShouldBeTrue)
		So(packs[0].Skills, ShouldResemble, []string{"browser", "qa"})
		So(packs[1].ID, ShouldEqual, "superpowers@openai-curated")
		So(packs[1].GloballyEnabled, ShouldBeFalse)

		Convey("空/坏 JSON → 空,不 panic", func() {
			p, _ := parsePluginList([]byte(""))
			So(p, ShouldResemble, []agentskill.SkillPack{})
			p2, _ := parsePluginList([]byte("not json"))
			So(p2, ShouldResemble, []agentskill.SkillPack{})
		})

		Convey("缺 name 时用 pluginId 的 @ 前段展示", func() {
			p, _ := parsePluginList([]byte(`{"installed":[{"pluginId":"docs@openai-primary-runtime","enabled":true}]}`))
			So(len(p), ShouldEqual, 1)
			So(p[0].Name, ShouldEqual, "docs")
		})

		Convey("skills/ 下软链到别处的 skill 目录也算数(软链安装)", func() {
			target := filepath.Join(t.TempDir(), "linked")
			mustCodexSkill(t, filepath.Dir(target), "linked")
			So(os.Symlink(target, filepath.Join(skills, "linked")), ShouldBeNil)

			p, _ := parsePluginList([]byte(
				`{"installed":[{"pluginId":"browser@openai-bundled","name":"browser","enabled":true,"source":{"path":"` + root + `"}}]}`))
			So(len(p), ShouldEqual, 1)
			So(p[0].Skills, ShouldResemble, []string{"browser", "linked", "qa"})
		})
	})
}

func TestDiscover(t *testing.T) {
	Convey("Discover 经可注入 runner 取 codex plugin list 并解析", t, func() {
		var gotName string
		var gotArgs []string
		d := Discoverer{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			gotName, gotArgs = name, args
			return []byte(`{"installed":[{"pluginId":"browser@openai-bundled","name":"browser","enabled":true}]}`), nil
		}}

		packs, err := d.Discover(context.Background(), agentskill.DiscoverQuery{})
		So(err, ShouldBeNil)
		So(gotName, ShouldEqual, "codex")
		So(gotArgs, ShouldResemble, []string{"plugin", "list", "--json"})
		So(len(packs), ShouldEqual, 1)
		So(packs[0].ID, ShouldEqual, "browser@openai-bundled")
		So(packs[0].GloballyEnabled, ShouldBeTrue)

		Convey("CLIPath 非空(含前后空白)→ trim 后用指定 binary 定位安装", func() {
			d := Discoverer{run: func(_ context.Context, name string, _ ...string) ([]byte, error) {
				gotName = name
				return []byte(`{"installed":[]}`), nil
			}}
			_, err := d.Discover(context.Background(), agentskill.DiscoverQuery{CLIPath: "  /opt/codex  "})
			So(err, ShouldBeNil)
			So(gotName, ShouldEqual, "/opt/codex")
		})

		Convey("CLI 报错 → 软降级空发现,不向上报错", func() {
			d := Discoverer{run: func(context.Context, string, ...string) ([]byte, error) {
				return nil, errors.New("codex: command not found")
			}}
			packs, err := d.Discover(context.Background(), agentskill.DiscoverQuery{})
			So(err, ShouldBeNil)
			So(packs, ShouldResemble, []agentskill.SkillPack{})
		})
	})
}

func TestDiscoverCommands(t *testing.T) {
	Convey("Given Codex app-server skill metadata and per-agent plugin overrides", t, func() {
		var gotBinary, gotCwd string
		var gotConfig []string
		d := Discoverer{listSkills: func(_ context.Context, binary, cwd string, config []string) ([]codex.Skill, error) {
			gotBinary, gotCwd, gotConfig = binary, cwd, config
			return []codex.Skill{
				{Name: "lore:lore-memory", Description: "Recall memory", Enabled: true},
				{Name: "disabled-skill", Description: "Off", Enabled: false},
			}, nil
		}}

		commands, err := d.DiscoverCommands(context.Background(), agentskill.CommandDiscoverQuery{
			CLIPath: " /opt/codex ",
			Cwd:     "/tmp/project",
			EnabledPlugins: map[string]bool{
				"browser@openai-bundled": true,
			},
		})

		Convey("Then only enabled $skill candidates are returned and launch overrides reach app-server", func() {
			So(err, ShouldBeNil)
			So(gotBinary, ShouldEqual, "/opt/codex")
			So(gotCwd, ShouldEqual, "/tmp/project")
			So(gotConfig, ShouldResemble, []string{`plugins."browser@openai-bundled".enabled=true`})
			So(commands, ShouldResemble, []agentskill.SkillCommand{{Name: "lore:lore-memory", Description: "Recall memory"}})
		})
	})
}

// TestDiscover_FindsCLIOutsideProcessPath 与 claudeskill 同一根问题:从 Finder /
// Dock 起的 app bundle 只继承 launchd 的最小 PATH,codex 常装在 ~/.local/bin、
// Homebrew、volta 之类的目录里。裸名字交给 exec 查的是本进程 PATH,查不到就被
// 软降级成空发现,插件技能整段消失。跑 CLI 的那条路(cliprocess → clienv)早就补齐
// 了 PATH,发现这条路必须同源。
func TestDiscover_FindsCLIOutsideProcessPath(t *testing.T) {
	Convey("Given the CLI lives in a user tool dir that a GUI-launched app's PATH does not contain", t, func() {
		home := t.TempDir()
		binDir := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		const binary = "agentre-skilltest-codex"
		script := "#!/bin/sh\nprintf '%s' '{\"installed\":[{\"pluginId\":\"browser\",\"name\":\"browser\",\"enabled\":true}]}'\n"
		if err := os.WriteFile(filepath.Join(binDir, binary), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")

		Convey("When packs are discovered, Then the CLI is still found and its plugins come back", func() {
			packs, err := Discoverer{}.Discover(context.Background(), agentskill.DiscoverQuery{CLIPath: binary})
			So(err, ShouldBeNil)
			So(len(packs), ShouldEqual, 1)
			So(packs[0].Name, ShouldEqual, "browser")
			So(packs[0].GloballyEnabled, ShouldBeTrue)
		})
	})
}
