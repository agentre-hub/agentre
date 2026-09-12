package agentskill

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

// stubCommandDiscoverer 记下入参并回一份固定的原生命令。
type stubCommandDiscoverer struct {
	got  CommandDiscoverQuery
	out  []SkillCommand
	fail error
}

func (s *stubCommandDiscoverer) DiscoverCommands(
	_ context.Context, q CommandDiscoverQuery,
) ([]SkillCommand, error) {
	s.got = q
	return s.out, s.fail
}

// TestBuildCommands 是「已装包 + 这一档的授权 + 本机 CLI 自己解析的 skill
// → 一份可调用命令清单」这件事的唯一实现。
//
// 它住在 agentskill 而不是 skill_svc,判据与隔壁 MergeCatalog 逐字相同:**三个
// 调用方要答同一份清单** —— 桌面端对本机档(拿得到组织架构库)、agentred 经
// skills.commands RPC 对远端档(拿不到库,授权由调用方带上)、以及浏览器控制台经
// 中继问同一个 RPC。三边各写一份合并就是三份会各自漂开的真相。
func TestBuildCommands(t *testing.T) {
	const bt = agent_backend_entity.TypeClaudeCode

	installed := []SkillPack{
		{ID: "superpowers@official", Name: "superpowers", Description: "TDD 那一套",
			Skills: []string{"brainstorming", "writing-plans"}, Installed: true, GloballyEnabled: true},
		{ID: "muted@mine", Name: "muted", Skills: []string{"never"}, Installed: true, GloballyEnabled: true},
	}

	Convey("包内 skill 冠上包名,未生效的包整包不出命令", t, func() {
		restore := SwapCommandDiscovererForTest(bt, &stubCommandDiscoverer{})
		defer restore()

		got, err := BuildCommands(context.Background(), CommandsQuery{
			BackendType: bt,
			Installed:   installed,
			// 强制关掉 muted:它已装且全局开着,只有显式授权能把它关下去。
			Authorized: []agent_entity.AgentSkillItem{{ID: "muted@mine", Enabled: false}},
		})
		So(err, ShouldBeNil)

		names := commandNames(got)
		So(names, ShouldResemble, []string{"superpowers:brainstorming", "superpowers:writing-plans"})
		So(got[0].Description, ShouldEqual, "TDD 那一套")
	})

	Convey("已经带冒号的 skill 名不再冠一次包名", t, func() {
		restore := SwapCommandDiscovererForTest(bt, &stubCommandDiscoverer{})
		defer restore()

		got, err := BuildCommands(context.Background(), CommandsQuery{
			BackendType: bt,
			Installed: []SkillPack{{
				ID: "p@m", Name: "pack", Skills: []string{"pack:already", "bare"},
				Installed: true, GloballyEnabled: true,
			}},
		})
		So(err, ShouldBeNil)
		So(commandNames(got), ShouldResemble, []string{"pack:already", "pack:bare"})
	})

	Convey("原生命令并进来,且拿得到生效插件表与 cwd", t, func() {
		stub := &stubCommandDiscoverer{out: []SkillCommand{
			{Name: "cago", Description: "cago 框架"},
			{Name: "superpowers:brainstorming", Description: "重复的一条"},
		}}
		restore := SwapCommandDiscovererForTest(bt, stub)
		defer restore()

		got, err := BuildCommands(context.Background(), CommandsQuery{
			BackendType: bt,
			CLIPath:     "/usr/local/bin/claude",
			Cwd:         "/tmp/project",
			Installed:   installed,
			Authorized:  []agent_entity.AgentSkillItem{{ID: "muted@mine", Enabled: false}},
		})
		So(err, ShouldBeNil)

		// 包里那两条在前、原生的续在后;与包内重名的那条不重复出现。
		So(commandNames(got), ShouldResemble, []string{
			"superpowers:brainstorming", "superpowers:writing-plans", "cago",
		})
		So(stub.got.Cwd, ShouldEqual, "/tmp/project")
		So(stub.got.CLIPath, ShouldEqual, "/usr/local/bin/claude")
		So(stub.got.BackendType, ShouldEqual, bt)
		// 授权表逐条透传:CLI 要靠它决定这一轮把哪些 plugin 挂上去。
		So(stub.got.EnabledPlugins, ShouldResemble, map[string]bool{"muted@mine": false})
	})

	Convey("这个 backend 没有原生发现器时只出包里的命令,不是错误", t, func() {
		got, err := BuildCommands(context.Background(), CommandsQuery{
			BackendType: agent_backend_entity.BackendType("nonesuch"),
			Installed: []SkillPack{{
				ID: "p@m", Name: "pack", Skills: []string{"one"},
				Installed: true, GloballyEnabled: true,
			}},
		})
		So(err, ShouldBeNil)
		So(commandNames(got), ShouldResemble, []string{"pack:one"})
	})

	Convey("原生发现失败时整件事失败 —— 不拿半份清单冒充答案", t, func() {
		restore := SwapCommandDiscovererForTest(bt, &stubCommandDiscoverer{
			fail: errors.New("cli 起不来"),
		})
		defer restore()

		_, err := BuildCommands(context.Background(), CommandsQuery{
			BackendType: bt, Installed: installed,
		})
		So(err, ShouldNotBeNil)
	})

	Convey("命令清单永远非 nil:一个都没有时是空切片", t, func() {
		restore := SwapCommandDiscovererForTest(bt, &stubCommandDiscoverer{})
		defer restore()

		got, err := BuildCommands(context.Background(), CommandsQuery{BackendType: bt})
		So(err, ShouldBeNil)
		So(got, ShouldNotBeNil)
		So(got, ShouldBeEmpty)
	})
}

func commandNames(commands []SkillCommand) []string {
	out := make([]string, 0, len(commands))
	for _, c := range commands {
		out = append(out, c.Name)
	}
	return out
}
