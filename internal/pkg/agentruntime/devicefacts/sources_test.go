package devicefacts_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/internal/pkg/agentskill"
	"github.com/agentre-hub/agentre/internal/pkg/backendcred"
)

type fakeDiscoverer struct {
	packs []agentskill.SkillPack
	err   error
	query agentskill.DiscoverQuery
}

func (f *fakeDiscoverer) Discover(_ context.Context, q agentskill.DiscoverQuery) ([]agentskill.SkillPack, error) {
	f.query = q
	return f.packs, f.err
}

func TestDiscoverSkillPacks(t *testing.T) {
	Convey("DiscoverSkillPacks 用本机的发现器列已装包", t, func() {
		ctx := context.Background()
		d := &fakeDiscoverer{packs: []agentskill.SkillPack{
			{ID: "superpowers@claude-plugins-official", Installed: true},
			{ID: "code-review@claude-plugins-official", Installed: true},
		}}
		restore := agentskill.SwapDiscovererForTest(agent_backend_entity.TypeClaudeCode, d)
		defer restore()

		Convey("Then 交回包 ID,并用找到的那个可执行文件去问", func() {
			ids, err := devicefacts.DiscoverSkillPacks(ctx, "claudecode", "/bin/claude")
			So(err, ShouldBeNil)
			So(ids, ShouldResemble, []string{"code-review@claude-plugins-official", "superpowers@claude-plugins-official"})
			So(d.query.CLIPath, ShouldEqual, "/bin/claude")
		})
		Convey("Given 发现器出错, Then 错误原样交回", func() {
			d.err = errors.New("plugin list failed")
			_, err := devicefacts.DiscoverSkillPacks(ctx, "claudecode", "/bin/claude")
			So(err, ShouldNotBeNil)
		})
		Convey("Given 这种运行时没有发现器, Then 没有包也不报错", func() {
			ids, err := devicefacts.DiscoverSkillPacks(ctx, "antigravity-not-registered", "")
			So(err, ShouldBeNil)
			So(ids, ShouldBeEmpty)
		})
	})
}

func TestResolveCommand(t *testing.T) {
	Convey("ResolveCommand 判一条 ACP 命令在本机能否解析", t, func() {
		dir := t.TempDir()
		exe := filepath.Join(dir, "my-agent")
		So(os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755), ShouldBeNil)
		t.Setenv("PATH", dir)

		So(devicefacts.ResolveCommand("my-agent"), ShouldBeTrue)
		So(devicefacts.ResolveCommand(exe), ShouldBeTrue)
		So(devicefacts.ResolveCommand(filepath.Join(dir, "missing")), ShouldBeFalse)
		So(devicefacts.ResolveCommand("definitely-not-installed-agent"), ShouldBeFalse)
		So(devicefacts.ResolveCommand("  "), ShouldBeFalse)
	})
}

func TestCredentialSlot(t *testing.T) {
	Convey("CredentialSlot 指出一份服务型引擎配置的凭据存在本机哪个槽位", t, func() {
		Convey("Then OpenClaw 按配置的 sync_id", func() {
			So(devicefacts.CredentialSlot(devicefacts.Engine{SyncID: "eng-1", Runtime: "openclaw"}),
				ShouldEqual, backendcred.OpenClawTokenAccount("eng-1"))
		})
		Convey("Then Hermes 按规范化后的 serve 地址(同一个 serve 共用一份登录)", func() {
			So(devicefacts.CredentialSlot(devicefacts.Engine{SyncID: "eng-2", Runtime: "hermes", HermesURL: "http://hermes.local:8642/"}),
				ShouldEqual, devicefacts.CredentialSlot(devicefacts.Engine{SyncID: "eng-3", Runtime: "hermes", HermesURL: "http://hermes.local:8642"}))
			base, err := agent_backend_entity.NormalizeHermesURL("http://hermes.local:8642")
			So(err, ShouldBeNil)
			So(devicefacts.CredentialSlot(devicefacts.Engine{Runtime: "hermes", HermesURL: "http://hermes.local:8642"}),
				ShouldEqual, backendcred.HermesAccount(base))
		})
		Convey("Then 地址坏了 / 没有 sync_id / 不是服务型引擎时没有槽位", func() {
			So(devicefacts.CredentialSlot(devicefacts.Engine{Runtime: "hermes", HermesURL: "::bad"}), ShouldBeEmpty)
			So(devicefacts.CredentialSlot(devicefacts.Engine{Runtime: "openclaw"}), ShouldBeEmpty)
			So(devicefacts.CredentialSlot(devicefacts.Engine{SyncID: "x", Runtime: "acp"}), ShouldBeEmpty)
		})
	})
}
