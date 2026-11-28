package devicefacts_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

var collectedAt = time.UnixMilli(1700000000000)

func baseSources() devicefacts.Sources {
	return devicefacts.Sources{
		Runtimes: func(context.Context) []syncwire.RuntimeFact {
			return []syncwire.RuntimeFact{
				{Runtime: "claudecode", Found: true, Path: "/bin/claude", Version: "2.1.0", NativeLogin: syncwire.NativeLoginLoggedIn},
				{Runtime: "codex", Found: false, NativeLogin: syncwire.NativeLoginUnknown},
			}
		},
		Engines: func(context.Context) ([]devicefacts.Engine, error) {
			return []devicefacts.Engine{
				{SyncID: "eng-acp-ok", Runtime: "acp", ACPCommand: "gemini"},
				{SyncID: "eng-acp-missing", Runtime: "acp", ACPCommand: "no-such-agent"},
				{SyncID: "eng-hermes", Runtime: "hermes", HermesURL: "http://hermes.local"},
				{SyncID: "eng-openclaw", Runtime: "openclaw", OpenClawGatewayURL: "ws://gw.local"},
				{SyncID: "eng-claude", Runtime: "claudecode"},
			}, nil
		},
		ResolveCommand: func(command string) bool { return command == "gemini" },
		HasCredential:  func(_ context.Context, e devicefacts.Engine) bool { return e.Runtime == "hermes" },
		LastConnection: func(e devicefacts.Engine) (string, int64) {
			if e.Runtime == "openclaw" {
				return syncwire.ConnectionFailed, 1699999999000
			}
			return syncwire.ConnectionNever, 0
		},
		SkillPacks: func(_ context.Context, runtime, cliPath string) ([]string, error) {
			if runtime == "claudecode" && cliPath == "/bin/claude" {
				return []string{"superpowers@claude-plugins-official"}, nil
			}
			return nil, nil
		},
		LocalPaths: func(context.Context) ([]syncwire.LocalPathItem, error) {
			return []syncwire.LocalPathItem{{ProjectSyncID: "p-1", Path: "/work/p1"}}, nil
		},
		Now: func() time.Time { return collectedAt },
	}
}

func TestCollect(t *testing.T) {
	Convey("Collect 汇总一台设备的情况", t, func() {
		ctx := context.Background()

		Convey("Given 运行时、引擎配置、凭据、技能包与本机路径都读得到, When 采集, Then 每一类事实各就其位", func() {
			report, err := devicefacts.Collect(ctx, baseSources())
			So(err, ShouldBeNil)
			So(report.CollectedAt, ShouldEqual, collectedAt.UnixMilli())
			So(report.Runtimes, ShouldHaveLength, 2)
			So(report.ACPEngines, ShouldResemble, []syncwire.ACPEngineFact{
				{EngineSyncID: "eng-acp-ok", Resolvable: true},
				{EngineSyncID: "eng-acp-missing", Resolvable: false},
			})
			// Claude Code 那份引擎配置没有设备事实:它的情况已经在运行时那一项里。
			So(report.ServiceEngines, ShouldResemble, []syncwire.ServiceEngineFact{
				{EngineSyncID: "eng-hermes", Runtime: "hermes", CredentialPresent: true, LastConnection: syncwire.ConnectionNever},
				{EngineSyncID: "eng-openclaw", Runtime: "openclaw", CredentialPresent: false,
					LastConnection: syncwire.ConnectionFailed, LastConnectionAt: 1699999999000},
			})
			// 技能包只问找到了的运行时,并且用找到的那个可执行文件去问。
			So(report.SkillPacks, ShouldResemble, []syncwire.RuntimeSkillPacks{
				{Runtime: "claudecode", PackIDs: []string{"superpowers@claude-plugins-official"}},
			})
			So(report.LocalPaths, ShouldResemble, []syncwire.LocalPathItem{{ProjectSyncID: "p-1", Path: "/work/p1"}})
		})

		Convey("Given 读不出引擎配置, When 采集, Then 报错而不是交出一份「没有任何 ACP 能解析」的空情况", func() {
			src := baseSources()
			src.Engines = func(context.Context) ([]devicefacts.Engine, error) { return nil, errors.New("db down") }
			_, err := devicefacts.Collect(ctx, src)
			So(err, ShouldNotBeNil)
		})

		Convey("Given 读不出本机路径, When 采集, Then 同样报错", func() {
			src := baseSources()
			src.LocalPaths = func(context.Context) ([]syncwire.LocalPathItem, error) { return nil, errors.New("db down") }
			_, err := devicefacts.Collect(ctx, src)
			So(err, ShouldNotBeNil)
		})

		Convey("Given 某个运行时的技能包列不出来, When 采集, Then 只是少了那一组,其余照常", func() {
			src := baseSources()
			src.SkillPacks = func(context.Context, string, string) ([]string, error) { return nil, errors.New("plugin list failed") }
			report, err := devicefacts.Collect(ctx, src)
			So(err, ShouldBeNil)
			So(report.SkillPacks, ShouldBeEmpty)
			So(report.ACPEngines, ShouldHaveLength, 2)
		})

		Convey("Given 宿主不记连接结果, When 采集, Then 服务型引擎的上次连接是 never", func() {
			src := baseSources()
			src.LastConnection = nil
			report, err := devicefacts.Collect(ctx, src)
			So(err, ShouldBeNil)
			for _, e := range report.ServiceEngines {
				So(e.LastConnection, ShouldEqual, syncwire.ConnectionNever)
			}
		})

		Convey("Given 宿主没有本机路径与技能包来源(agentred), When 采集, Then 两组为空而不是报错", func() {
			src := baseSources()
			src.LocalPaths = nil
			src.SkillPacks = nil
			report, err := devicefacts.Collect(ctx, src)
			So(err, ShouldBeNil)
			So(report.LocalPaths, ShouldBeEmpty)
			So(report.SkillPacks, ShouldBeEmpty)
		})
	})
}
