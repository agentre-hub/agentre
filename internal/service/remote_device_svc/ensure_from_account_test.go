package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// stubServerSvc 是 server_svc.ServerSvc 的最小测试实现：EnsureFromAccount 只调
// ListDevices，其余方法从不会被调到，嵌入 nil 接口即可（调用即 panic，不需要实现）。
type stubServerSvc struct {
	server_svc.ServerSvc
	devices []server_svc.Device
	err     error
}

func (s stubServerSvc) ListDevices(context.Context) ([]server_svc.Device, error) {
	return s.devices, s.err
}

// withServerSvc 把 stub 接进 server_svc 单例，t.Cleanup 时还原。
func withServerSvc(t *testing.T, stub server_svc.ServerSvc) {
	t.Helper()
	previous := server_svc.Server()
	server_svc.SetDefault(stub)
	t.Cleanup(func() { server_svc.SetDefault(previous) })
}

// 决策：EnsureFromAccount 是 AdoptAccountDevices 的编排壳——App.ServerListDevices
// （前端刷新设备面板）与 ctl 解析 --device 找不到本地记录时共用这一处实现，不必
// 各自重复「拉取账号设备 → 翻译成 AccountDevice → 收编」这一套循环。
func TestEnsureFromAccount_GivenAnUnpairedAgentred_ThenAdoptsItAndReportsSelf(t *testing.T) {
	Convey("fetching the account device list adopts unknown agentreds and reports this desktop's own name", t, func() {
		repo, w, svc := adoptFixture(t)
		withServerSvc(t, stubServerSvc{devices: []server_svc.Device{
			{Fingerprint: "sha256:devbox", Name: "devbox", Kind: "agentred"},
			{Fingerprint: "sha256:this-desktop", Name: "my-mac", Kind: "desktop", IsThisDevice: true},
		}})
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)
		var created *paired_agentred_entity.PairedAgentred
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error {
				created = p
				p.ID = 7
				return nil
			})
		w.EXPECT().Start(gomock.Any(), int64(7)).Return(nil)

		selfName, ok, err := svc.EnsureFromAccount(context.Background())

		So(err, ShouldBeNil)
		So(ok, ShouldBeTrue)
		So(selfName, ShouldEqual, "my-mac")
		So(created, ShouldNotBeNil)
		So(created.DaemonFingerprint, ShouldEqual, devicefp.Carrier("sha256:devbox"))
	})
}

func TestEnsureFromAccount_GivenNoRowIsThisDevice_ThenReportsNotOK(t *testing.T) {
	Convey("an account list without a self-marked row reports ok=false", t, func() {
		repo, w, svc := adoptFixture(t)
		_ = w
		withServerSvc(t, stubServerSvc{devices: []server_svc.Device{
			{Fingerprint: "sha256:devbox", Name: "devbox", Kind: "agentred"},
		}})
		repo.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 1, DaemonFingerprint: "sha256:devbox", Status: 1},
		}, nil)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)

		selfName, ok, err := svc.EnsureFromAccount(context.Background())

		So(err, ShouldBeNil)
		So(ok, ShouldBeFalse)
		So(selfName, ShouldBeEmpty)
	})
}

func TestEnsureFromAccount_GivenServerNotWired_ThenReturnsEmptyWithoutTouchingTheRepo(t *testing.T) {
	Convey("no server_svc singleton registered is not an error", t, func() {
		repo, w, svc := adoptFixture(t)
		_ = repo
		_ = w
		previous := server_svc.Server()
		server_svc.SetDefault(nil)
		t.Cleanup(func() { server_svc.SetDefault(previous) })
		// 没有任何 repo.EXPECT()：server 未接线时压根不该碰仓储。

		selfName, ok, err := svc.EnsureFromAccount(context.Background())

		So(err, ShouldBeNil)
		So(ok, ShouldBeFalse)
		So(selfName, ShouldBeEmpty)
	})
}

func TestEnsureFromAccount_GivenTheFetchFails_ThenTheErrorIsNotSwallowed(t *testing.T) {
	Convey("a failing account fetch is reported, not treated as an empty list", t, func() {
		repo, w, svc := adoptFixture(t)
		_ = w
		withServerSvc(t, stubServerSvc{err: errors.New("server unreachable")})
		// 没有任何 repo.EXPECT()：拉取失败时不该走到收编那一步。

		_, ok, err := svc.EnsureFromAccount(context.Background())

		So(err, ShouldNotBeNil)
		So(ok, ShouldBeFalse)
		_ = repo
	})
}

// 未登录的桌面端（spec 决策 4：离线的本地用户也要能用 agrctl）没有账号设备可看：与
// server 未接线同一口径，不是错误。否则 ctl 解析一个本地找不到的 --device 名字时，报出来
// 的是「未登录」，而不是 spec 要的 `device "<名字>" not found`。
func TestEnsureFromAccount_GivenNotLoggedIn_ThenReturnsEmptyWithoutError(t *testing.T) {
	Convey("a desktop that is not logged in has no account devices, which is not an error", t, func() {
		repo, w, svc := adoptFixture(t)
		_ = repo
		_ = w
		withServerSvc(t, stubServerSvc{err: server_svc.ErrNotLoggedIn})
		// 没有任何 repo.EXPECT()：没有账号清单就不该走到收编那一步。

		selfName, ok, err := svc.EnsureFromAccount(context.Background())

		So(err, ShouldBeNil)
		So(ok, ShouldBeFalse)
		So(selfName, ShouldBeEmpty)
	})
}
