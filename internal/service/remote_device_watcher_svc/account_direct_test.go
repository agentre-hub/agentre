package remote_device_watcher_svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
)

// accountDirectRow 是「来自账号的直连」行（task 8）：地址位是下发列表之一、证书固定，
// 本地直连凭据放在与手动配对令牌同一个钥匙串槽 agentre-daemon-token-<id> 里。
func accountDirectRow() *paired_agentred_entity.PairedAgentred {
	row := fixtureRow()
	row.URL = "wss://10.0.0.5:7456/rpc"
	row.TLSMode = "pin-cert"
	row.TLSCertPEM = "pinned-cert-pem"
	row.Origin = "account"
	row.SetDirectURLs([]string{"wss://10.0.0.5:7456/rpc", "wss://192.168.1.9:7456/rpc"})
	return row
}

// Given 一台来自账号的直连设备、钥匙串槽里是本地直连凭据, When watcher 拨号,
// Then 凭据作为直连凭据（auth.direct）交给拨号端口、带上全部下发地址，而不是被当成
// 配对令牌走 auth.connect —— 否则 agentred 必然拒掉，设备永远离线。
func TestWatcher_GivenAccountDirectRowWithCredential_WhenDialing_ThenProbesWithTheDirectCredentialAndAllAddresses(t *testing.T) {
	Convey("来自账号的直连行:按直连凭据与全部地址探活,不走配对令牌", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(accountDirectRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-7").Return("direct-cred", nil)
		kc.EXPECT().Get("agentre-device-fingerprint").Return("desktop-fp", nil)
		opened := make(chan remote_device_watcher_svc.OpenArgs, 1)
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, args remote_device_watcher_svc.OpenArgs) (client.ProtobufConnection, error) {
				opened <- args
				return newWatcherTestConnection(), nil
			})
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), int64(1_000_000), "").Return(nil)

		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, testCfg, clock, nil)
		go w.Run(ctx)

		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		args := <-opened
		So(args.AccountDirect, ShouldBeTrue)
		So(args.DeviceID, ShouldEqual, int64(7))
		So(args.DirectCredential, ShouldEqual, "direct-cred")
		So(args.DeviceToken, ShouldEqual, "")
		So(args.DirectURLs, ShouldResemble, []string{"wss://10.0.0.5:7456/rpc", "wss://192.168.1.9:7456/rpc"})
		So(args.TLSCertPEM, ShouldEqual, "pinned-cert-pem")
		So(args.DeviceFingerprint, ShouldEqual, "desktop-fp")
		So(emit.snapshot()[0].Online, ShouldBeTrue)
		cancel()
		w.Wait()
	})
}

// Given 一台来自账号的直连设备、钥匙串槽是空的, When watcher 拨号, Then 不判「未授权」
// 永久降级 —— 缺的只是直连这条路，中转照样能探活并重新下发凭据。
func TestWatcher_GivenAccountDirectRowWithEmptyCredentialSlot_WhenDialing_ThenStillProbesInsteadOfMarkingUnauthorized(t *testing.T) {
	Convey("来自账号的直连行、钥匙串槽为空:仍然探活,不落 unauthorized", t, func() {
		repo, dial, kc, emit, clock := setupWatcher(t)
		repo.EXPECT().Get(gomock.Any(), int64(7)).Return(accountDirectRow(), nil)
		kc.EXPECT().Get("agentre-daemon-token-7").Return("", errors.New("keychain: not found"))
		kc.EXPECT().Get("agentre-device-fingerprint").Return("desktop-fp", nil)
		opened := make(chan remote_device_watcher_svc.OpenArgs, 1)
		dial.EXPECT().Open(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, args remote_device_watcher_svc.OpenArgs) (client.ProtobufConnection, error) {
				opened <- args
				return newWatcherTestConnection(), nil
			})
		repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), int64(1_000_000), "").Return(nil)

		ctx, cancel := context.WithCancel(context.Background())
		w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, testCfg, clock, nil)
		go w.Run(ctx)

		waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
		So(emit.snapshot()[0].LastError, ShouldEqual, "")
		So(emit.snapshot()[0].Online, ShouldBeTrue)
		args := <-opened
		So(args.AccountDirect, ShouldBeTrue)
		So(args.DirectCredential, ShouldEqual, "")
		So(args.DeviceToken, ShouldEqual, "")
		cancel()
		w.Wait()
	})
}

// Given 来自账号的直连行拨号失败（D16 证书被换 / D17 地址路由不到 / H3 账号服务不可达）,
// When watcher 分类, Then 一律是可重试的连接失败:不落 tofu_mismatch / unauthorized,
// 退避后重拨 —— 证书不符不是今天的 TOFU 告警。
func TestWatcher_GivenAccountDirectDialFails_WhenClassifying_ThenRetriesAsAConnectionFailureWithoutTOFUAlarm(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"pin mismatch on every address, relay down", errors.New("direct path: wss://10.0.0.5:7456/rpc path: tls: server cert does not match pinned cert; relay path: relay down")},
		{"account server unreachable", errors.New("relay path: Account server unreachable")},
	}
	for _, tc := range cases {
		Convey(tc.name, t, func() {
			repo, dial, kc, emit, clock := setupWatcher(t)
			repo.EXPECT().Get(gomock.Any(), int64(7)).Return(accountDirectRow(), nil).Times(2)
			kc.EXPECT().Get("agentre-daemon-token-7").Return("direct-cred", nil).Times(2)
			kc.EXPECT().Get("agentre-device-fingerprint").Return("desktop-fp", nil).Times(2)
			gomock.InOrder(
				dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(nil, tc.err),
				dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(newWatcherTestConnection(), nil),
			)
			repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), int64(0), gomock.Any()).Return(nil)
			repo.EXPECT().UpdateLastSeen(gomock.Any(), int64(7), gomock.Any(), "").Return(nil)

			ctx, cancel := context.WithCancel(context.Background())
			w := remote_device_watcher_svc.NewWatcher(7, repo, dial, kc, emit, testCfg, clock, nil)
			go w.Run(ctx)

			waitFor(t, func() bool { return len(emit.snapshot()) >= 1 })
			So(emit.snapshot()[0].Online, ShouldBeFalse)
			So(emit.snapshot()[0].LastError, ShouldStartWith, "dial_failed:")
			clock.Advance(time.Second)
			waitFor(t, func() bool { return len(emit.snapshot()) >= 2 })
			So(emit.snapshot()[1].Online, ShouldBeTrue)
			cancel()
			w.Wait()
		})
	}
}
