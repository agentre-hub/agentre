package skill_svc

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type fakeDisc struct{ packs []agentskill.SkillPack }

func (f fakeDisc) Discover(_ context.Context, _ agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	return f.packs, nil
}

type fakeCommandDisc struct{ commands []agentskill.SkillCommand }

func (f fakeCommandDisc) DiscoverCommands(_ context.Context, _ agentskill.CommandDiscoverQuery) ([]agentskill.SkillCommand, error) {
	return f.commands, nil
}

// fakeRemoteDisc 替身远端发现器:记录入参,回预置 daemon 包。
type fakeRemoteDisc struct {
	gotDeviceID int64
	gotBackend  string
	packs       []agentskill.SkillPack

	gotCommandDeviceID   int64
	gotCommandBackend    string
	gotCommandCwd        string
	gotCommandAuthorized []agent_entity.AgentSkillItem
	commands             []agentskill.SkillCommand
}

func (f *fakeRemoteDisc) ListSkillCommands(
	_ context.Context, deviceID int64, backendType, cwd string,
	authorized []agent_entity.AgentSkillItem,
) ([]agentskill.SkillCommand, error) {
	f.gotCommandDeviceID = deviceID
	f.gotCommandBackend = backendType
	f.gotCommandCwd = cwd
	f.gotCommandAuthorized = authorized
	return f.commands, nil
}

func (f *fakeRemoteDisc) ListSkills(_ context.Context, deviceID int64, backendType string) ([]agentskill.SkillPack, error) {
	f.gotDeviceID = deviceID
	f.gotBackend = backendType
	return f.packs, nil
}

const (
	selfFP devicefp.Carrier = "sha256:self"
	boxFP  devicefp.Carrier = "sha256:box"
)

// fixture 一名成员、它生效的引擎配置，以及它在各台设备上能不能跑；它自己充当全部端口替身
// （mockgen 替身因 Placement 返回本包类型会与内部测试成环）。
type fixture struct {
	agent   *agent_entity.Agent
	profile *agent_backend_entity.EngineProfile
	devices []Device
	cliPath string
	remote  *fakeRemoteDisc
	svc     *Service
}

func (f *fixture) Find(_ context.Context, id int64) (*agent_entity.Agent, error) {
	if id != f.agent.ID {
		return nil, nil
	}
	return f.agent, nil
}

func (f *fixture) Effective(context.Context, int64) (*agent_backend_entity.EngineProfile, error) {
	return f.profile, nil
}

func (f *fixture) MemberDevices(context.Context, int64) ([]Device, error) { return f.devices, nil }

func (f *fixture) LocalCLIPath(context.Context, string) (string, error) { return f.cliPath, nil }

// newFixture 默认：成员 1 用 Claude Code，本机与已配对的 agentred（行 ID 7）都能跑。
func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{
		agent:   &agent_entity.Agent{ID: 1},
		profile: &agent_backend_entity.EngineProfile{ID: 11, Name: "Claude", Type: string(agent_backend_entity.TypeClaudeCode)},
		devices: []Device{
			{Fingerprint: selfFP, IsSelf: true, CanRun: true},
			{Fingerprint: boxFP, Name: "机房", PairedID: 7, CanRun: true},
		},
		remote: &fakeRemoteDisc{},
	}
	f.svc = newService(f, f, f, f, f.remote)
	return f
}

func (f *fixture) grant(runtime string, items ...agent_entity.AgentSkillItem) {
	f.agent.SetSkillGrants(runtime, items)
}

func packsByID(cat MemberSkillCatalogDTO) map[string]SkillPackDTO {
	byID := map[string]SkillPackDTO{}
	for _, p := range cat.Packs {
		byID[p.ID] = p
	}
	return byID
}

func TestMemberSkillCatalog_DiscoverySource(t *testing.T) {
	ctx := context.Background()
	Convey("技能目录从一台能运行引擎配置的在线设备读取，本机优先", t, func() {
		f := newFixture(t)
		restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, fakeDisc{[]agentskill.SkillPack{
			{ID: "local-only@desktop", Name: "local-only", Installed: true, Source: agentskill.SourceInstalled},
		}})
		defer restore()
		f.remote.packs = []agentskill.SkillPack{
			{ID: "superpowers@claude-plugins-official", Name: "superpowers", Installed: true, Source: agentskill.SourceInstalled, GloballyEnabled: true},
		}

		Convey("本机能跑：读本机，不问 agentred", func() {
			f.devices[0], f.devices[1] = f.devices[1], f.devices[0] // 派发顺序 agentred 在前也一样本机优先
			cat, err := f.svc.MemberSkillCatalog(ctx, 1, false)
			So(err, ShouldBeNil)
			So(cat.Supported, ShouldBeTrue)
			So(cat.Runtime, ShouldEqual, "claudecode")
			So(cat.SourceDevice, ShouldEqual, selfFP)
			So(packsByID(cat)["local-only@desktop"].Installed, ShouldBeTrue)
			So(f.remote.gotDeviceID, ShouldEqual, int64(0))
		})

		Convey("本机跑不了：读第一台能跑的已配对 agentred，不混入本机发现", func() {
			f.devices[0].CanRun = false
			cat, err := f.svc.MemberSkillCatalog(ctx, 1, false)
			So(err, ShouldBeNil)
			So(cat.SourceDevice, ShouldEqual, boxFP)
			So(cat.SourceDeviceName, ShouldEqual, "机房")
			So(f.remote.gotDeviceID, ShouldEqual, int64(7))
			So(f.remote.gotBackend, ShouldEqual, "claudecode")
			byID := packsByID(cat)
			So(byID["superpowers@claude-plugins-official"].GloballyEnabled, ShouldBeTrue)
			_, hasLocal := byID["local-only@desktop"]
			So(hasLocal, ShouldBeFalse)
		})

		Convey("哪台都跑不了：不发现，只列推荐与授权，来源设备为空", func() {
			f.devices[0].CanRun, f.devices[1].CanRun = false, false
			cat, err := f.svc.MemberSkillCatalog(ctx, 1, false)
			So(err, ShouldBeNil)
			So(cat.SourceDevice, ShouldEqual, devicefp.Carrier(""))
			_, hasLocal := packsByID(cat)["local-only@desktop"]
			So(hasLocal, ShouldBeFalse)
			So(packsByID(cat)["code-review@claude-plugins-official"].Recommended, ShouldBeTrue)
		})
	})
}

func TestMemberSkillCatalog_Grants(t *testing.T) {
	ctx := context.Background()
	Convey("授权挂在成员上，按引擎配置的运行时校验", t, func() {
		f := newFixture(t)
		restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, fakeDisc{[]agentskill.SkillPack{
			{ID: "superpowers@claude-plugins-official", Name: "superpowers", Installed: true, Source: agentskill.SourceInstalled, GloballyEnabled: true},
			{ID: "opsctl@opskat", Name: "opsctl", Installed: true, Source: agentskill.SourceInstalled},
		}})
		defer restore()

		Convey("同一运行时的授权生效：显式开/关决定 EffectiveEnabled", func() {
			f.grant("claudecode",
				agent_entity.AgentSkillItem{ID: "superpowers@claude-plugins-official", Enabled: false},
				agent_entity.AgentSkillItem{ID: "opsctl@opskat", Enabled: true},
			)
			cat, err := f.svc.MemberSkillCatalog(ctx, 1, false)
			So(err, ShouldBeNil)
			byID := packsByID(cat)
			So(byID["superpowers@claude-plugins-official"].EffectiveEnabled, ShouldBeFalse)
			So(byID["opsctl@opskat"].Enabled, ShouldBeTrue)
			So(byID["opsctl@opskat"].EffectiveEnabled, ShouldBeTrue)
			So(byID["superpowers@claude-plugins-official"].Recommended, ShouldBeTrue)
			So(cat.InvalidGrants, ShouldBeEmpty)
		})

		Convey("授权是对另一种运行时做的：整份失效，单独列出供一键移除", func() {
			f.grant("codex", agent_entity.AgentSkillItem{ID: "browser@openai-bundled", Enabled: true})
			cat, err := f.svc.MemberSkillCatalog(ctx, 1, false)
			So(err, ShouldBeNil)
			So(cat.InvalidGrants, ShouldResemble, []agent_entity.AgentSkillItem{{ID: "browser@openai-bundled", Enabled: true}})
			_, has := packsByID(cat)["browser@openai-bundled"]
			So(has, ShouldBeFalse)
			So(packsByID(cat)["opsctl@opskat"].Enabled, ShouldBeFalse)
		})

		Convey("一轮注入的覆盖 map 只发同运行时的显式授权", func() {
			f.grant("claudecode",
				agent_entity.AgentSkillItem{ID: "superpowers@claude-plugins-official", Enabled: true},
				agent_entity.AgentSkillItem{ID: "frontend-design@claude-plugins-official", Enabled: false},
			)
			m, err := f.svc.EnabledPluginsMapFor(ctx, f.agent, "claudecode")
			So(err, ShouldBeNil)
			So(m, ShouldResemble, map[string]bool{
				"superpowers@claude-plugins-official":     true,
				"frontend-design@claude-plugins-official": false,
			})
			m, err = f.svc.EnabledPluginsMapFor(ctx, f.agent, "codex")
			So(err, ShouldBeNil)
			So(m, ShouldBeEmpty)
		})

		Convey("没给运行时：按成员生效的引擎配置的运行时取", func() {
			f.grant("claudecode", agent_entity.AgentSkillItem{ID: "opsctl@opskat", Enabled: true})
			m, err := f.svc.EnabledPluginsMapFor(ctx, f.agent, "")
			So(err, ShouldBeNil)
			So(m, ShouldResemble, map[string]bool{"opsctl@opskat": true})
		})
	})
}

func TestMemberSkillCatalog_MissingPacks(t *testing.T) {
	ctx := context.Background()
	Convey("授权开着的包在某台能跑的设备上没装：照常列出并标出哪台没装", t, func() {
		f := newFixture(t)
		restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, fakeDisc{})
		defer restore()
		f.grant("claudecode",
			agent_entity.AgentSkillItem{ID: "superpowers@claude-plugins-official", Enabled: true},
			agent_entity.AgentSkillItem{ID: "opsctl@opskat", Enabled: false},
		)
		f.devices = []Device{
			{Fingerprint: selfFP, IsSelf: true, CanRun: true, InstalledPacks: []string{"superpowers@claude-plugins-official"}, PacksKnown: true},
			{Fingerprint: boxFP, Name: "机房", PairedID: 7, CanRun: true, InstalledPacks: []string{}, PacksKnown: true},
			{Fingerprint: "sha256:nofacts", Name: "没报情况", CanRun: true},
			{Fingerprint: "sha256:cannot", Name: "跑不了", CanRun: false, InstalledPacks: []string{}, PacksKnown: true},
		}
		cat, err := f.svc.MemberSkillCatalog(ctx, 1, false)
		So(err, ShouldBeNil)
		So(cat.MissingPacks, ShouldResemble, []MissingPackDTO{{
			PackID:  "superpowers@claude-plugins-official",
			Devices: []DeviceRefDTO{{Fingerprint: boxFP, Name: "机房"}},
		}})
	})
}

func TestMemberSkillCatalog_EdgeCases(t *testing.T) {
	ctx := context.Background()
	Convey("目录的边界情形", t, func() {
		f := newFixture(t)

		Convey("运行时不支持技能包：Supported=false，不发现", func() {
			f.profile = &agent_backend_entity.EngineProfile{ID: 12, Name: "Hermes", Type: string(agent_backend_entity.TypeHermes)}
			cat, err := f.svc.MemberSkillCatalog(ctx, 1, false)
			So(err, ShouldBeNil)
			So(cat.Supported, ShouldBeFalse)
			So(cat.Runtime, ShouldEqual, "hermes")
			So(cat.Packs, ShouldBeEmpty)
		})

		Convey("成员没有生效的引擎配置：报需要引擎配置", func() {
			f.profile = nil
			_, err := f.svc.MemberSkillCatalog(ctx, 1, false)
			So(err, ShouldNotBeNil)
		})

		Convey("成员不存在：空目录不报错", func() {
			cat, err := f.svc.MemberSkillCatalog(ctx, 404, false)
			So(err, ShouldBeNil)
			So(cat.Packs, ShouldBeEmpty)
		})
	})
}

func TestListAgentSkillCommands_Local(t *testing.T) {
	Convey("Given the member runs locally with plugin skills plus CLI-native skills", t, func() {
		f := newFixture(t)
		f.profile = &agent_backend_entity.EngineProfile{ID: 13, Name: "Codex", Type: string(agent_backend_entity.TypeCodex)}
		restorePacks := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeCodex, fakeDisc{[]agentskill.SkillPack{
			{ID: "browser@openai-bundled", Name: "browser", Skills: []string{"browser"}, Installed: true, Source: agentskill.SourceInstalled, GloballyEnabled: true},
			{ID: "superpowers@openai-curated", Name: "superpowers", Skills: []string{"tdd"}, Installed: true, Source: agentskill.SourceInstalled, GloballyEnabled: false},
		}})
		defer restorePacks()
		restoreCommands := agentskill.SwapCommandDiscovererForTest(agent_backend_entity.TypeCodex, fakeCommandDisc{[]agentskill.SkillCommand{
			{Name: "browser:browser"},
			{Name: "shadcn", Description: "Compose shadcn UI"},
		}})
		defer restoreCommands()

		Convey("When commands are listed, Then enabled plugin and standalone names merge once while disabled packs stay hidden", func() {
			catalog, err := f.svc.ListAgentSkillCommands(context.Background(), 1, "/tmp/project")
			So(err, ShouldBeNil)
			So(catalog.Commands, ShouldResemble, []SkillCommandDTO{
				{Name: "browser:browser"},
				{Name: "shadcn", Description: "Compose shadcn UI"},
			})
		})
	})
}

// 本机跑不了时，命令清单整份由那台 agentred 答（插件包与 CLI 原生 skill 都在里面），授权由
// 这一侧带过去；本机发现器不被问到。
func TestListAgentSkillCommands_RemoteDeviceAnswers(t *testing.T) {
	Convey("Given the member can only run on a paired agentred", t, func() {
		f := newFixture(t)
		f.devices[0].CanRun = false
		f.grant("claudecode", agent_entity.AgentSkillItem{ID: "superpowers@official", Enabled: true})
		restorePacks := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode,
			fakeDisc{[]agentskill.SkillPack{{ID: "only@here", Name: "onlyhere", Skills: []string{"nope"}, Installed: true, GloballyEnabled: true}}})
		defer restorePacks()
		restoreCommands := agentskill.SwapCommandDiscovererForTest(agent_backend_entity.TypeClaudeCode,
			fakeCommandDisc{[]agentskill.SkillCommand{{Name: "local-only"}}})
		defer restoreCommands()
		f.remote.commands = []agentskill.SkillCommand{
			{Name: "superpowers:brainstorming", Description: "先想清楚"},
			{Name: "cago", Description: "cago 框架"},
		}

		catalog, err := f.svc.ListAgentSkillCommands(context.Background(), 1, "/srv/project")
		So(err, ShouldBeNil)
		So(catalog.Commands, ShouldResemble, []SkillCommandDTO{
			{Name: "superpowers:brainstorming", Description: "先想清楚"},
			{Name: "cago", Description: "cago 框架"},
		})
		So(f.remote.gotCommandDeviceID, ShouldEqual, int64(7))
		So(f.remote.gotCommandBackend, ShouldEqual, "claudecode")
		So(f.remote.gotCommandCwd, ShouldEqual, "/srv/project")
		So(f.remote.gotCommandAuthorized, ShouldResemble, []agent_entity.AgentSkillItem{{ID: "superpowers@official", Enabled: true}})
	})

	Convey("Given no device can run the member", t, func() {
		f := newFixture(t)
		f.devices[0].CanRun, f.devices[1].CanRun = false, false
		f.remote.commands = []agentskill.SkillCommand{{Name: "never"}}
		catalog, err := f.svc.ListAgentSkillCommands(context.Background(), 1, "/srv/project")
		So(err, ShouldBeNil)
		So(catalog.Commands, ShouldBeEmpty)
		So(f.remote.gotCommandDeviceID, ShouldEqual, int64(0))
	})
}
