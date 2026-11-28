package devicefacts_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

func TestRuntimeProbe(t *testing.T) {
	Convey("RuntimeProbe 探四种已知运行时", t, func() {
		ctx := context.Background()
		installed := map[string]string{"claudecode": "/bin/claude", "antigravity": "/bin/agy", "piagent": "/bin/pi"}
		versions := map[string]string{"/bin/claude": "2.1.3 (Claude Code)", "/bin/agy": "1.4.9"}
		probe := devicefacts.RuntimeProbe{
			Resolve: func(runtime string) (string, bool) {
				path, ok := installed[runtime]
				return path, ok
			},
			Version: func(_ context.Context, path string) (string, error) {
				if v, ok := versions[path]; ok {
					return v, nil
				}
				return "", errors.New("exit 1")
			},
			Login: func(runtime string) string {
				if runtime == "claudecode" {
					return syncwire.NativeLoginLoggedIn
				}
				return syncwire.NativeLoginLoggedOut
			},
		}

		facts := map[string]syncwire.RuntimeFact{}
		for _, f := range probe.Probe(ctx) {
			facts[f.Runtime] = f
		}

		Convey("Then 每种已知运行时都有一项,没装的也在", func() {
			So(facts, ShouldHaveLength, 4)
			So(facts["codex"].Found, ShouldBeFalse)
			So(facts["codex"].Path, ShouldBeEmpty)
		})
		Convey("Then 找到的带路径与解析出的版本号", func() {
			So(facts["claudecode"], ShouldResemble, syncwire.RuntimeFact{
				Runtime: "claudecode", Found: true, Path: "/bin/claude", Version: "2.1.3",
				NativeLogin: syncwire.NativeLoginLoggedIn,
			})
		})
		Convey("Then 登录态只报 Claude Code 与 Codex", func() {
			So(facts["codex"].NativeLogin, ShouldEqual, syncwire.NativeLoginLoggedOut)
			So(facts["piagent"].NativeLogin, ShouldBeEmpty)
			So(facts["antigravity"].NativeLogin, ShouldBeEmpty)
		})
		Convey("Then 低于下限的 Antigravity 标出下限与低于下限", func() {
			So(facts["antigravity"].MinimumVersion, ShouldEqual, "1.5")
			So(facts["antigravity"].BelowMinimum, ShouldBeTrue)
		})
		Convey("Then 读不出版本时版本为空,也不判低于下限", func() {
			So(facts["piagent"].Found, ShouldBeTrue)
			So(facts["piagent"].Version, ShouldBeEmpty)
			So(facts["piagent"].BelowMinimum, ShouldBeFalse)
		})
	})
}

func TestBelowMinimum(t *testing.T) {
	Convey("BelowMinimum 按数字段比较版本", t, func() {
		cases := []struct {
			version, minimum string
			below            bool
		}{
			{"1.4.9", "1.5", true},
			{"1.5", "1.5", false},
			{"1.5.0", "1.5", false},
			{"1.10.0", "1.5", false},
			{"0.9", "1.5", true},
			{"2.0", "1.5", false},
			{"", "1.5", false},
			{"1.4", "", false},
		}
		for _, c := range cases {
			So(devicefacts.BelowMinimum(c.version, c.minimum), ShouldEqual, c.below)
		}
	})
}

func TestParseVersion(t *testing.T) {
	Convey("ParseVersion 从 --version 的输出里取版本号", t, func() {
		So(devicefacts.ParseVersion("2.1.3 (Claude Code)\n"), ShouldEqual, "2.1.3")
		So(devicefacts.ParseVersion("codex-cli 0.46.0"), ShouldEqual, "0.46.0")
		So(devicefacts.ParseVersion("v1.5.16"), ShouldEqual, "1.5.16")
		So(devicefacts.ParseVersion("no version here"), ShouldBeEmpty)
	})
}
