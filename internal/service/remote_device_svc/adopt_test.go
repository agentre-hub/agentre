package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	repomock "github.com/agentre-hub/agentre/internal/repository/remote_device_repo/mock_remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	svcmock "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// adoptFixture 只装 AdoptAccountDevices 需要的东西：仓储 + keychain（本机指纹）+ watcher。
func adoptFixture(t *testing.T) (*repomock.MockPairedAgentredRepo, *svcmock.MockWatcherPort, remote_device_svc.RemoteDeviceSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := repomock.NewMockPairedAgentredRepo(ctrl)
	dial := svcmock.NewMockDaemonDialPort(ctrl)
	w := svcmock.NewMockWatcherPort(ctrl)
	kc := keychain.NewMemory()
	_ = kc.Set("agentre-device-fingerprint", "sha256:this-desktop")
	pool := remote_device_svc.NewConnPool(repo, kc, dial)
	svc := remote_device_svc.New(repo, dial, kc, pool)
	svc.SetWatcher(w)
	return repo, w, svc
}

func accountDevice(fp, name, kind string) remote_device_svc.AccountDevice {
	return remote_device_svc.AccountDevice{Fingerprint: fp, Name: name, Kind: kind}
}

// 决策 1「账号即信任边界」：账号里已经有、本机从没配对过的 agentred 必须能当执行
// 设备。收编成一行没有 LAN 地址的记录（IsRelayOnly），远端执行链路那把主键钥匙
// 因此不必改，中转按指纹寻址照常跑。
func TestAdoptAccountDevices_GivenAnUnpairedAgentred_ThenAdoptsItAsRelayOnly(t *testing.T) {
	Convey("an account agentred this desktop never paired becomes a relay-only row", t, func() {
		repo, w, svc := adoptFixture(t)
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)
		var created *paired_agentred_entity.PairedAgentred
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error {
				created = p
				p.ID = 3
				return nil
			})
		// 收编完必须把长连状态机拉起来：不然这一行的 last_seen_at 永远是 0，
		// DeviceView.online 恒 false，「运行设备」下拉会把它渲染成灰的不可选项——
		// 收编等于白做。启动时的 StartAll 只管存量行，运行时新收的这一行没人管。
		w.EXPECT().Start(gomock.Any(), int64(3)).Return(nil)

		n, err := svc.AdoptAccountDevices(context.Background(),
			[]remote_device_svc.AccountDevice{accountDevice("sha256:coding", "coding", "agentred")})

		So(err, ShouldBeNil)
		So(n, ShouldEqual, 1)
		So(created, ShouldNotBeNil)
		So(created.DaemonFingerprint, ShouldEqual, "sha256:coding")
		So(created.Name, ShouldEqual, "coding")
		So(created.URL, ShouldBeEmpty)
		So(created.IsRelayOnly(), ShouldBeTrue)
		So(created.Check(context.Background()), ShouldBeNil)
	})
}

// 用户在设备面板上「解除配对」删掉的那一行是软删（remove.go），行还留在表里。
// 那一行就是用户的删除意图本身：收编不得把它当成「本机还没有这台机器」而重建一行，
// 否则删除看起来像随机失效 —— 删掉了，切个窗口回来它又在了。
func TestAdoptAccountDevices_GivenAMachineTheUserRemoved_ThenDoesNotResurrectIt(t *testing.T) {
	Convey("a machine the user unpaired is not adopted back while it is still in the account", t, func() {
		repo, w, svc := adoptFixture(t)
		_ = w
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		repo.EXPECT().ListDeleted(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 4, Name: "coding", DaemonFingerprint: "sha256:coding", Status: consts.DELETE},
		}, nil)
		// 没有 Create 的 EXPECT：重建这一行就是复活，正是本用例要挡住的。

		n, err := svc.AdoptAccountDevices(context.Background(),
			[]remote_device_svc.AccountDevice{accountDevice("sha256:coding", "coding", "agentred")})

		So(err, ShouldBeNil)
		So(n, ShouldEqual, 0)
	})
}

// 墓碑记的是「我不要这台机器出现在这台桌面上」，它绑在**这一次账号归属**上。
// 机器整个离开了账号（账号侧撤销授权 / 从设备清单里消失），它下次回来（重新
// agentred login）是一次新的加入，旧的拒绝随之作废 —— 否则用户永远没法把它请回来。
// 只清中转收编来的那种墓碑：手工 LAN 配对过又解除的行另有含义（见下一个用例）。
func TestAdoptAccountDevices_GivenATombstoneWhoseMachineLeftTheAccount_ThenRetiresIt(t *testing.T) {
	Convey("a relay-only tombstone is retired once its machine is gone from the account", t, func() {
		repo, w, svc := adoptFixture(t)
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		repo.EXPECT().ListDeleted(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 4, Name: "gone", DaemonFingerprint: "sha256:gone", Status: consts.DELETE},
		}, nil)
		repo.EXPECT().Purge(gomock.Any(), int64(4)).Return(nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error {
				p.ID = 11
				return nil
			})
		w.EXPECT().Start(gomock.Any(), int64(11)).Return(nil)

		n, err := svc.AdoptAccountDevices(context.Background(),
			[]remote_device_svc.AccountDevice{accountDevice("sha256:still-here", "still-here", "agentred")})

		So(err, ShouldBeNil)
		// 这一轮账号里没有 sha256:gone，所以本轮不收它；清墓碑是为了它下次真的回来时能收。
		So(n, ShouldEqual, 1)
	})
}

// 手工 LAN 配对过、又被解除的行不是中转收编的产物：它的重新进入通路是再配对一次
// （Add 只看存活行，墓碑挡不住它）。账号清单里此刻没有它，也不该被当成「这次归属
// 结束了」而清掉 —— 它本来就可能从来不属于任何账号。
func TestAdoptAccountDevices_GivenALanPairingTombstone_ThenKeepsItAndStillRefusesToAdopt(t *testing.T) {
	Convey("a LAN pairing tombstone is neither purged nor overridden", t, func() {
		repo, w, svc := adoptFixture(t)
		_ = w
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		repo.EXPECT().ListDeleted(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{
				ID: 5, Name: "lan-box", URL: "ws://192.168.8.9:7456/rpc",
				DaemonFingerprint: "sha256:lan-box", Status: consts.DELETE,
			},
		}, nil)
		// 没有 Purge 的 EXPECT，也没有 Create 的 EXPECT。

		n, err := svc.AdoptAccountDevices(context.Background(),
			[]remote_device_svc.AccountDevice{accountDevice("sha256:lan-box", "lan-box", "agentred")})

		So(err, ShouldBeNil)
		So(n, ShouldEqual, 0)
	})
}

// 指纹为空的软删行无从指认任何一台机器：拿空串当判据会把「所有还没握过手的旧配对」
// 混成同一台，进而把一台从没删过的机器永久挡在外面。
func TestAdoptAccountDevices_GivenAFingerprintlessTombstone_ThenIgnoresIt(t *testing.T) {
	Convey("a tombstone without a fingerprint blocks nothing", t, func() {
		repo, w, svc := adoptFixture(t)
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		repo.EXPECT().ListDeleted(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 6, Name: "never handshook", URL: "ws://10.0.0.2:7456/rpc", Status: consts.DELETE},
		}, nil)
		repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, p *paired_agentred_entity.PairedAgentred) error {
				p.ID = 9
				return nil
			})
		w.EXPECT().Start(gomock.Any(), int64(9)).Return(nil)

		n, err := svc.AdoptAccountDevices(context.Background(),
			[]remote_device_svc.AccountDevice{accountDevice("sha256:coding", "coding", "agentred")})

		So(err, ShouldBeNil)
		So(n, ShouldEqual, 1)
	})
}

func TestAdoptAccountDevices_GivenAlreadyKnownMachines_ThenAdoptsNothing(t *testing.T) {
	Convey("idempotent by fingerprint, and never shadows an existing LAN pairing", t, func() {
		repo, w, svc := adoptFixture(t)
		_ = w
		repo.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
			{ID: 1, Name: "agentred-1", URL: "ws://192.168.8.188:7456/rpc", DaemonFingerprint: "sha256:coding", Status: 1},
			{ID: 2, Name: "adopted", DaemonFingerprint: "sha256:other", Status: 1},
		}, nil)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)
		// 没有 Create 的 EXPECT：建了任何一行都是失败。

		n, err := svc.AdoptAccountDevices(context.Background(), []remote_device_svc.AccountDevice{
			accountDevice("sha256:coding", "coding", "agentred"),
			accountDevice("sha256:other", "other", "agentred"),
		})

		So(err, ShouldBeNil)
		So(n, ShouldEqual, 0)
	})
}

// 桌面端不是执行设备（它没有 agentred 的执行面），本机自己更不是「远端」。
// 收编它们会在设备面板上凭空多出一行，还会让「运行设备」下拉列出一台跑不了活的机器。
func TestAdoptAccountDevices_GivenDesktopsAndSelf_ThenSkipsThem(t *testing.T) {
	Convey("desktops and this very machine are never adopted", t, func() {
		repo, w, svc := adoptFixture(t)
		_ = w
		repo.EXPECT().List(gomock.Any()).Return(nil, nil)
		repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil)

		n, err := svc.AdoptAccountDevices(context.Background(), []remote_device_svc.AccountDevice{
			accountDevice("sha256:someones-laptop", "MacBook", "desktop"),
			accountDevice("sha256:this-desktop", "me", "agentred"),
			accountDevice("", "no fingerprint", "agentred"),
		})

		So(err, ShouldBeNil)
		So(n, ShouldEqual, 0)
	})
}

func TestAdoptAccountDevices_GivenRepositoryFailure_ThenReportsIt(t *testing.T) {
	Convey("a failing repository is not swallowed", t, func() {
		repo, w, svc := adoptFixture(t)
		_ = w
		repo.EXPECT().List(gomock.Any()).Return(nil, errors.New("db is gone"))

		_, err := svc.AdoptAccountDevices(context.Background(),
			[]remote_device_svc.AccountDevice{accountDevice("sha256:coding", "coding", "agentred")})

		So(err, ShouldNotBeNil)
	})
}
