package claudeskill

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	. "github.com/smartystreets/goconvey/convey"
)

// mustSkill 在 skillsDir 下造一个合法 skill 目录(含 SKILL.md)。
func mustSkill(t *testing.T, skillsDir, name string) {
	t.Helper()
	dir := filepath.Join(skillsDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustLinkedSkill 在 skillsDir 下挂一个软链 skill 目录(真实目录在别处),
// 复刻"把技能仓库软链进技能目录"的装法。
func mustLinkedSkill(t *testing.T, skillsDir, name string) {
	t.Helper()
	target := filepath.Join(t.TempDir(), name)
	mustSkill(t, filepath.Dir(target), name)
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(skillsDir, name)); err != nil {
		t.Fatal(err)
	}
}

// mustFile 写一个文件,自动建父目录。
func mustFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mustDirectoryMarketplace 复刻 directory 型 marketplace 的真实形状:
// marketplaces/<name> 是指向本地技能仓库的软链,插件根由清单的 source 声明,
// 而 CLI 记在 installed_plugins.json 里的 cache installPath 从未落地。
// 返回 pluginsDir。
func mustDirectoryMarketplace(t *testing.T) string {
	t.Helper()
	pluginsDir := t.TempDir()
	repo := t.TempDir()

	mustSkill(t, filepath.Join(repo, "dev-kit", "skills"), "specification")
	mustSkill(t, filepath.Join(repo, "dev-kit", "skills"), "tdd")
	mustFile(t, filepath.Join(repo, ".claude-plugin", "marketplace.json"),
		`{"name":"codfrm-skills","plugins":[{"name":"dev-kit","source":"./dev-kit"}]}`)

	link := filepath.Join(pluginsDir, "marketplaces", "codfrm-skills")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	mustFile(t, filepath.Join(pluginsDir, "known_marketplaces.json"),
		`{"codfrm-skills":{"source":{"source":"directory","path":"`+link+`"},"installLocation":"`+link+`"}}`)
	return pluginsDir
}

func TestParsePluginList(t *testing.T) {
	Convey("解析 plugin list --json", t, func() {
		raw := []byte(`[
		  {"id":"superpowers@claude-plugins-official","enabled":true,"scope":"user"},
		  {"id":"opsctl@opskat","enabled":false,"scope":"user"}
		]`)
		packs, err := Discoverer{}.parsePluginList(raw)
		So(err, ShouldBeNil)
		So(len(packs), ShouldEqual, 2)
		So(packs[0].ID, ShouldEqual, "superpowers@claude-plugins-official")
		So(packs[0].Name, ShouldEqual, "superpowers") // id 取 @ 前段
		So(packs[0].Installed, ShouldBeTrue)
		So(packs[0].Source, ShouldEqual, agentskill.SourceInstalled)
		So(packs[0].GloballyEnabled, ShouldBeTrue)  // superpowers enabled:true
		So(packs[1].GloballyEnabled, ShouldBeFalse) // opsctl enabled:false
		Convey("空/坏 JSON → 空,不 panic", func() {
			p, _ := Discoverer{}.parsePluginList([]byte(""))
			So(p, ShouldResemble, []agentskill.SkillPack{})
			p2, _ := Discoverer{}.parsePluginList([]byte("not json"))
			So(p2, ShouldResemble, []agentskill.SkillPack{})
		})
		Convey("无 @ 的裸 id → Name 即 id", func() {
			p, _ := Discoverer{}.parsePluginList([]byte(`[{"id":"barepack","enabled":true,"scope":"user"}]`))
			So(len(p), ShouldEqual, 1)
			So(p[0].ID, ShouldEqual, "barepack")
			So(p[0].Name, ShouldEqual, "barepack")
		})
		Convey("installPath 命中 → Skills 填上包内 skill 名", func() {
			root := t.TempDir()
			skills := filepath.Join(root, "skills")
			mustSkill(t, skills, "alpha")
			mustSkill(t, skills, "beta")
			raw := []byte(`[{"id":"sp@x","enabled":true,"scope":"user","installPath":"` + root + `"}]`)
			p, _ := Discoverer{}.parsePluginList(raw)
			So(len(p), ShouldEqual, 1)
			So(p[0].Skills, ShouldResemble, []string{"alpha", "beta"})
		})
		Convey("无 installPath → Skills 为空(不 panic)", func() {
			So(packs[0].Skills, ShouldBeEmpty)
		})
	})
}

func TestDiscover(t *testing.T) {
	Convey("Discover 经可注入 runner 取 plugin list 并解析", t, func() {
		var gotName string
		var gotArgs []string
		d := Discoverer{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			gotName, gotArgs = name, args
			return []byte(`[{"id":"superpowers@official","enabled":true,"scope":"user"}]`), nil
		}}
		packs, err := d.Discover(context.Background(), agentskill.DiscoverQuery{})
		So(err, ShouldBeNil)
		So(gotName, ShouldEqual, "claude") // 空 CLIPath → 默认 binary
		So(gotArgs, ShouldResemble, []string{"plugin", "list", "--json"})
		So(len(packs), ShouldEqual, 1)
		So(packs[0].ID, ShouldEqual, "superpowers@official")
		So(packs[0].GloballyEnabled, ShouldBeTrue)

		Convey("CLIPath 非空(含前后空白)→ trim 后用指定 binary 定位安装", func() {
			d := Discoverer{run: func(_ context.Context, name string, _ ...string) ([]byte, error) {
				gotName = name
				return []byte("[]"), nil
			}}
			_, err := d.Discover(context.Background(), agentskill.DiscoverQuery{CLIPath: "  /opt/claude  "})
			So(err, ShouldBeNil)
			So(gotName, ShouldEqual, "/opt/claude")
		})

		Convey("CLI 报错 → 软降级空发现,不向上报错", func() {
			d := Discoverer{run: func(context.Context, string, ...string) ([]byte, error) {
				return nil, errors.New("claude: command not found")
			}}
			packs, err := d.Discover(context.Background(), agentskill.DiscoverQuery{})
			So(err, ShouldBeNil)
			So(packs, ShouldResemble, []agentskill.SkillPack{})
		})

		Convey("默认 runner(run=nil)走真实 exec;缺失 binary 时软降级、不 panic", func() {
			d := Discoverer{}
			packs, err := d.Discover(context.Background(), agentskill.DiscoverQuery{CLIPath: "agentre-no-such-binary-xyz"})
			So(err, ShouldBeNil)
			So(packs, ShouldResemble, []agentskill.SkillPack{})
		})
	})
}

func TestScanSkills(t *testing.T) {
	Convey("扫描 plugin installPath 下的 skills/*/SKILL.md", t, func() {
		root := t.TempDir()
		skills := filepath.Join(root, "skills")
		mustSkill(t, skills, "brainstorming")
		mustSkill(t, skills, "tdd")
		// 没有 SKILL.md 的目录不算 skill
		So(os.MkdirAll(filepath.Join(skills, "not-a-skill"), 0o755), ShouldBeNil)
		// skills 下的散落文件不算
		So(os.WriteFile(filepath.Join(skills, "README.md"), []byte("x"), 0o644), ShouldBeNil)

		So(scanSkills(root), ShouldResemble, []string{"brainstorming", "tdd"})

		Convey("skills/ 下软链到别处的 skill 目录也算数(软链安装)", func() {
			mustLinkedSkill(t, skills, "linked")
			So(scanSkills(root), ShouldResemble, []string{"brainstorming", "linked", "tdd"})
		})
		Convey("指向已删除目标的死软链不算 skill", func() {
			So(os.Symlink(filepath.Join(root, "gone"), filepath.Join(skills, "dangling")), ShouldBeNil)
			So(scanSkills(root), ShouldResemble, []string{"brainstorming", "tdd"})
		})
		Convey("installPath 为空 → nil", func() {
			So(scanSkills(""), ShouldBeNil)
		})
		Convey("没有 skills 目录(纯命令插件)→ nil", func() {
			cmdOnly := t.TempDir()
			So(os.MkdirAll(filepath.Join(cmdOnly, "commands"), 0o755), ShouldBeNil)
			So(scanSkills(cmdOnly), ShouldBeNil)
		})
	})
}

func TestParsePluginListMarketplaceFallback(t *testing.T) {
	Convey("Given a plugin whose recorded installPath never landed (directory-type marketplace)", t, func() {
		pluginsDir := mustDirectoryMarketplace(t)
		d := Discoverer{pluginsDir: func() string { return pluginsDir }}
		missing := filepath.Join(pluginsDir, "cache", "codfrm-skills", "dev-kit", "0.2.0")

		Convey("When the plugin list is parsed, Then skills come from the marketplace manifest source", func() {
			packs, err := d.parsePluginList([]byte(
				`[{"id":"dev-kit@codfrm-skills","enabled":true,"scope":"user","installPath":"` + missing + `"}]`))
			So(err, ShouldBeNil)
			So(len(packs), ShouldEqual, 1)
			So(packs[0].Skills, ShouldResemble, []string{"specification", "tdd"})
		})

		Convey("When installPath does exist, Then it stays authoritative over the marketplace", func() {
			landed := t.TempDir()
			mustSkill(t, filepath.Join(landed, "skills"), "from-install-path")
			packs, err := d.parsePluginList([]byte(
				`[{"id":"dev-kit@codfrm-skills","enabled":true,"scope":"user","installPath":"` + landed + `"}]`))
			So(err, ShouldBeNil)
			So(packs[0].Skills, ShouldResemble, []string{"from-install-path"})
		})

		Convey("When the marketplace is unknown, Then discovery degrades to no skills instead of failing", func() {
			packs, err := d.parsePluginList([]byte(
				`[{"id":"ghost@nowhere","enabled":true,"scope":"user","installPath":"` + missing + `"}]`))
			So(err, ShouldBeNil)
			So(len(packs), ShouldEqual, 1)
			So(packs[0].Skills, ShouldBeEmpty)
		})
	})
}

func TestDiscoverCommands(t *testing.T) {
	Convey("Given Claude user and project skill roots", t, func() {
		userRoot := t.TempDir()
		projectRoot := t.TempDir()
		mustSkill(t, userRoot, "cago")
		mustSkill(t, projectRoot, "frontend-design")
		So(os.MkdirAll(filepath.Join(userRoot, "not-a-skill"), 0o755), ShouldBeNil)

		d := Discoverer{skillRoots: func(cwd string) []string {
			So(cwd, ShouldEqual, "/tmp/project")
			return []string{userRoot, projectRoot}
		}}

		Convey("When commands are discovered, Then standalone /skill names are returned and invalid directories are ignored", func() {
			commands, err := d.DiscoverCommands(context.Background(), agentskill.CommandDiscoverQuery{Cwd: "/tmp/project"})
			So(err, ShouldBeNil)
			So(commands, ShouldResemble, []agentskill.SkillCommand{
				{Name: "cago"},
				{Name: "frontend-design"},
			})
		})

		Convey("When a user skill is installed as a symlink, Then it is still discovered", func() {
			mustLinkedSkill(t, userRoot, "shadcn")
			commands, err := d.DiscoverCommands(context.Background(), agentskill.CommandDiscoverQuery{Cwd: "/tmp/project"})
			So(err, ShouldBeNil)
			So(commands, ShouldResemble, []agentskill.SkillCommand{
				{Name: "cago"},
				{Name: "shadcn"},
				{Name: "frontend-design"},
			})
		})
	})
}

// TestDiscover_FindsCLIOutsideProcessPath 复现「plugin 技能一个都不剩」:
// 从 Finder / Dock 起的 app bundle 只继承 launchd 的最小 PATH
// (/usr/bin:/bin:/usr/sbin:/sbin),而 claude 常装在 ~/.local/bin、Homebrew、
// volta 之类的地方。把裸名字直接交给 exec 查的是**本进程**的 PATH,必然 not
// found,而 Discover 对 CLI 不可用是软降级成空发现 —— 于是插件包整段消失,只剩
// 文件系统扫出来的 standalone skill,用户看到的正是这个。真正跑 CLI 的那条路
// (clienv)早就为此把 PATH 补齐了,发现这条路必须用同一套解析。
func TestDiscover_FindsCLIOutsideProcessPath(t *testing.T) {
	Convey("Given the CLI lives in a user tool dir that a GUI-launched app's PATH does not contain", t, func() {
		home := t.TempDir()
		binDir := filepath.Join(home, ".local", "bin")
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// 名字取唯一值,免得撞上开发机上真装着的同名 CLI。
		const binary = "agentre-skilltest-claude"
		script := "#!/bin/sh\nprintf '%s' '[{\"id\":\"dev-kit@skills-dir\",\"enabled\":true}]'\n"
		if err := os.WriteFile(filepath.Join(binDir, binary), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("HOME", home)
		t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin") // launchd 给 app bundle 的那份

		Convey("When packs are discovered, Then the CLI is still found and its plugins come back", func() {
			packs, err := Discoverer{}.Discover(context.Background(), agentskill.DiscoverQuery{CLIPath: binary})
			So(err, ShouldBeNil)
			So(len(packs), ShouldEqual, 1)
			So(packs[0].ID, ShouldEqual, "dev-kit@skills-dir")
			So(packs[0].GloballyEnabled, ShouldBeTrue)
		})
	})
}
