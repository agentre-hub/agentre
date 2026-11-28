package device_facts_svc

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo/mock_device_pref_repo"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func profile(syncID, typ string, mutate func(*agent_backend_entity.EngineProfile)) *agent_backend_entity.EngineProfile {
	p := &agent_backend_entity.EngineProfile{Type: typ, SyncMeta: syncmeta_entity.SyncMeta{SyncID: syncID}}
	if mutate != nil {
		mutate(p)
	}
	return p
}

func TestSelfEngines(t *testing.T) {
	Convey("本机情况核对的是账号下的活引擎配置", t, func() {
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		repo := mock_agent_backend_repo.NewMockEngineProfileRepo(ctrl)
		agent_backend_repo.RegisterEngineProfile(repo)

		Convey("Given 各类引擎配置, Then 交出它们的同步标识、类型与本机核对要用的 ACP 命令 / Hermes 地址", func() {
			repo.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.EngineProfile{
				profile("acp-1", "acp", func(p *agent_backend_entity.EngineProfile) { p.ACPCommand = "gemini" }),
				profile("hermes-1", "hermes", func(p *agent_backend_entity.EngineProfile) { p.HermesURL = "http://h:8642" }),
				profile("claude-1", "claudecode", nil),
				profile("", "acp", nil), // 还没有同步标识的配置无从对应,不报
			}, nil)
			engines, err := selfEngines(ctx)
			So(err, ShouldBeNil)
			So(engines, ShouldResemble, []devicefacts.Engine{
				{SyncID: "acp-1", Runtime: "acp", ACPCommand: "gemini"},
				{SyncID: "hermes-1", Runtime: "hermes", HermesURL: "http://h:8642"},
				{SyncID: "claude-1", Runtime: "claudecode"},
			})
		})
		Convey("Given 读不出, Then 报错", func() {
			repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db down"))
			_, err := selfEngines(ctx)
			So(err, ShouldNotBeNil)
		})
	})
}

func TestSelfCLIOverrides(t *testing.T) {
	Convey("本机的运行时探测用这台设备上的 CLI 路径覆盖", t, func() {
		ctx := context.Background()
		ctrl := gomock.NewController(t)
		repo := mock_device_pref_repo.NewMockCLIPathRepo(ctrl)
		device_pref_repo.RegisterCLIPath(repo)
		const self devicefp.Carrier = "sha256:self"

		Convey("Then 只取本机那几行,按运行时类型", func() {
			repo.EXPECT().List(gomock.Any()).Return([]*device_pref_entity.CLIPathOverride{
				{RuntimeType: "claudecode", DeviceFingerprint: self, CLIPath: "/opt/claude"},
				{RuntimeType: "codex", DeviceFingerprint: "sha256:other", CLIPath: "/other/codex"},
			}, nil)
			So(selfCLIOverrides(ctx, self), ShouldResemble, map[string]string{"claudecode": "/opt/claude"})
		})
		Convey("Given 读不出覆盖, Then 按没有覆盖走 PATH", func() {
			repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db down"))
			So(selfCLIOverrides(ctx, self), ShouldBeEmpty)
		})
	})
}
