package remote_device_svc_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	repomock "github.com/agentre-hub/agentre/internal/repository/remote_device_repo/mock_remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	svcmock "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
)

// poolFixture 给 Pool 单测装好 mock + 一台 device 的标准数据。
type poolFixture struct {
	t      *testing.T
	ctrl   *gomock.Controller
	repo   *repomock.MockPairedAgentredRepo
	dial   *svcmock.MockDaemonDialPort
	kc     keychain.Keychain
	pool   remote_device_svc.ConnPool
	device *paired_agentred_entity.PairedAgentred
}

func newPoolFixture(t *testing.T, opts ...remote_device_svc.Option) *poolFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := repomock.NewMockPairedAgentredRepo(ctrl)
	dial := svcmock.NewMockDaemonDialPort(ctrl)
	kc := keychain.NewMemory()
	_ = kc.Set("agentre-daemon-token-42", "tok-42")
	_ = kc.Set("agentre-device-fingerprint", "fp-x")
	row := &paired_agentred_entity.PairedAgentred{
		ID: 42, Name: "agentred-a", URL: "wss://example/rpc",
		TLSMode: "skip-verify", DaemonFingerprint: "sha256:abc",
	}
	return &poolFixture{
		t:      t,
		ctrl:   ctrl,
		repo:   repo,
		dial:   dial,
		kc:     kc,
		pool:   remote_device_svc.NewConnPool(repo, kc, dial, opts...),
		device: row,
	}
}

// stubClient 返回一个非 nil 的 client.ProtobufConnection sentinel。Pool 不应该真的对它
// 调 Call/Close —— 这些行为应由集成测验。单测里 Pool 只持有指针。
type stubProtobufConnection struct{ closed chan struct{} }

func newStubProtobufConnection() *stubProtobufConnection {
	return &stubProtobufConnection{closed: make(chan struct{})}
}
func (c *stubProtobufConnection) Conn() *protorpc.Conn {
	return protorpc.NewConn(nil, protorpc.NewRegistry())
}
func (c *stubProtobufConnection) Closed() <-chan struct{} { return c.closed }
func (c *stubProtobufConnection) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	return nil
}
func stubClient() client.ProtobufConnection { return newStubProtobufConnection() }

func expectBorrowDialError(
	f *poolFixture,
	dialErr error,
	wantErr error,
) {
	f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
	f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(nil, dialErr)
	_, err := f.pool.Borrow(context.Background(), 42)
	So(errors.Is(err, wantErr), ShouldBeTrue)
}

func TestPool_Borrow_DeviceNotFound(t *testing.T) {
	Convey("repo returns nil row → ErrDeviceNotFound", t, func() {
		f := newPoolFixture(t)
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(nil, nil)
		_, err := f.pool.Borrow(context.Background(), 42)
		So(errors.Is(err, remote_device_svc.ErrDeviceNotFound), ShouldBeTrue)
	})
}

func TestPool_Borrow_KeychainMissingToken(t *testing.T) {
	Convey("keychain missing token → ErrDeviceUnauthorized", t, func() {
		f := newPoolFixture(t)
		_ = f.kc.Delete("agentre-daemon-token-42")
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		_, err := f.pool.Borrow(context.Background(), 42)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeTrue)
	})
}

func TestPool_Borrow_DialUnauthorizedMapped(t *testing.T) {
	Convey("dial returns ErrUnauthorized → ErrDeviceUnauthorized", t, func() {
		f := newPoolFixture(t)
		expectBorrowDialError(f, remote_device_svc.ErrUnauthorized, remote_device_svc.ErrDeviceUnauthorized)
	})
}

func TestPool_Borrow_DialTOFUMismatchPassthrough(t *testing.T) {
	Convey("dial returns ErrTOFUMismatch → propagated", t, func() {
		f := newPoolFixture(t)
		expectBorrowDialError(f, remote_device_svc.ErrTOFUMismatch, remote_device_svc.ErrTOFUMismatch)
	})
}

func TestPool_Release_RecycleBeforeTimeout(t *testing.T) {
	Convey("Borrow during idle window cancels timer and reuses entry", t, func() {
		f := newPoolFixture(t, remote_device_svc.WithIdleTimeout(200*time.Millisecond))
		c := stubClient()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil).Times(1)
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(c, nil).Times(1)

		l1, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		l1.Release()
		// 30ms 后(远小于 idle 200ms)再 Borrow
		time.Sleep(30 * time.Millisecond)
		l2, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(l2.Client(), ShouldEqual, l1.Client())

		// 静等过 idleTimeout 总长 —— 因 l2 还在,不应 evict
		time.Sleep(250 * time.Millisecond)
		select {
		case <-l2.Closed():
			t.Fatal("entry evicted even though it was re-borrowed")
		default:
		}
	})
}

func TestPool_Release_EvictsAfterIdleTimeout(t *testing.T) {
	Convey("Release w/ no other borrowers evicts entry after idle timeout", t, func() {
		f := newPoolFixture(t, remote_device_svc.WithIdleTimeout(20*time.Millisecond))
		c := stubClient()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil).Times(1)
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(c, nil).Times(1)

		l, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		l.Release()

		// idle 到点后 Lease.Closed() 应关闭
		select {
		case <-l.Closed():
		case <-time.After(200 * time.Millisecond):
			t.Fatal("entry not evicted within 200ms after idle")
		}
	})
}

func TestPool_Borrow_TOCTOU_ConcurrentColdStart(t *testing.T) {
	Convey("Concurrent first-borrows resolve to a single entry", t, func() {
		f := newPoolFixture(t)
		var openCount int32
		clients := []client.ProtobufConnection{stubClient(), stubClient(), stubClient(), stubClient(), stubClient()}
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).
			Return(f.device, nil).AnyTimes()
		f.dial.EXPECT().
			Open(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ remote_device_svc.ConnectArgs) (client.ProtobufConnection, error) {
				i := atomic.AddInt32(&openCount, 1) - 1
				return clients[i], nil
			}).
			AnyTimes()

		const N = 5
		var wg sync.WaitGroup
		leases := make([]remote_device_svc.Lease, N)
		errs := make([]error, N)
		wg.Add(N)
		for i := 0; i < N; i++ {
			go func() {
				defer wg.Done()
				leases[i], errs[i] = f.pool.Borrow(context.Background(), 42)
			}()
		}
		wg.Wait()
		for i, err := range errs {
			So(err, ShouldBeNil)
			So(leases[i], ShouldNotBeNil)
		}
		first := leases[0].Client()
		for i := 1; i < N; i++ {
			So(leases[i].Client(), ShouldEqual, first)
		}
	})
}

func TestPool_Borrow_FastPath_ReusesEntry(t *testing.T) {
	Convey("Second Borrow on same device reuses entry, no dial", t, func() {
		f := newPoolFixture(t)
		c := stubClient()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil).Times(1)
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(c, nil).Times(1)

		l1, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		l2, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)

		So(l1.Client(), ShouldEqual, l2.Client())
	})
}

func TestPool_Borrow_ColdStart(t *testing.T) {
	Convey("Borrow on a fresh device dials and returns a lease", t, func() {
		f := newPoolFixture(t)
		c := stubClient()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().
			Open(gomock.Any(), gomock.Any()).
			Return(c, nil).
			Times(1)

		lease, err := f.pool.Borrow(context.Background(), 42)

		So(err, ShouldBeNil)
		So(lease, ShouldNotBeNil)
		So(lease.Client(), ShouldNotBeNil)
		// Closed channel should be open at this point.
		select {
		case <-lease.Closed():
			t.Fatal("Closed() fired before any drop / Release")
		default:
		}
		assert.NotNil(t, c)
	})
}

// stubRelayDial 是一个可控的 RelayDialPort 假替身,用于验证 Borrow 的并发选路。
// 未提供 open 时固定失败。
type stubRelayDial struct {
	open func(ctx context.Context, daemonFP, peerFP string) (client.ProtobufConnection, error)
}

func (s stubRelayDial) Open(ctx context.Context, daemonFP, peerFP string) (client.ProtobufConnection, error) {
	if s.open == nil {
		return nil, errors.New("relay not stubbed")
	}
	return s.open(ctx, daemonFP, peerFP)
}

func TestPool_Borrow_RelayConfigured_LANWinsWhenRelayUnavailable(t *testing.T) {
	Convey("relay configured but unavailable: LAN path wins (one path down is not failure, R6)", t, func() {
		var gotDaemonFP, gotPeerFP string
		f := newPoolFixture(t, remote_device_svc.WithRelayDial(stubRelayDial{open: func(_ context.Context, daemonFP, peerFP string) (client.ProtobufConnection, error) {
			gotDaemonFP, gotPeerFP = daemonFP, peerFP
			return nil, errors.New("relay unreachable")
		}}))
		c := stubClient()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(c, nil)

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease, ShouldNotBeNil)
		So(lease.Client(), ShouldNotBeNil)
		// R5 硬不变量:relay 目标 = daemon 指纹;对端标识 = 桌面端 keychain 指纹。
		So(gotDaemonFP, ShouldEqual, "sha256:abc")
		So(gotPeerFP, ShouldEqual, "fp-x")
	})
}

func TestPool_Borrow_RelayConfigured_RelayWinsWhenLANUnavailable(t *testing.T) {
	Convey("relay configured and LAN down: relay path wins", t, func() {
		relayClient := stubClient()
		f := newPoolFixture(t, remote_device_svc.WithRelayDial(stubRelayDial{open: func(_ context.Context, _, _ string) (client.ProtobufConnection, error) {
			return relayClient, nil
		}}))
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(nil, errors.New("LAN down"))

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
	})
}

// 生产装配总是注入 relay(bootstrap.InitRemoteDevice),所以「LAN 的 auth.connect 被拒」
// 这条既有判定必须在 relay 已注入时依然成立 —— 否则设备令牌被撤销/解除配对后,
// chat_svc.terminalBorrowError 认不出终止条件,重连循环会永远重试一台再也不会接受
// 自己的 daemon。R6 要求的「两条路径各自的原因」同时保留。
func TestPool_Borrow_RelayConfigured_LANUnauthorized_StaysDeviceUnauthorized(t *testing.T) {
	Convey("relay configured and LAN rejects credentials: still ErrDeviceUnauthorized, both reasons kept", t, func() {
		f := newPoolFixture(t, remote_device_svc.WithRelayDial(stubRelayDial{open: func(_ context.Context, _, _ string) (client.ProtobufConnection, error) {
			return nil, errors.New("relay down")
		}}))
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(nil, remote_device_svc.ErrUnauthorized)

		_, err := f.pool.Borrow(context.Background(), 42)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeTrue)
		So(err.Error(), ShouldContainSubstring, "direct path: unauthorized")
		So(err.Error(), ShouldContainSubstring, "relay path: relay down")
	})
}

func TestPool_Borrow_RelayConfigured_BothFail_ReportsBothReasons(t *testing.T) {
	Convey("both paths fail: error names each path's reason (R6)", t, func() {
		f := newPoolFixture(t, remote_device_svc.WithRelayDial(stubRelayDial{open: func(_ context.Context, _, _ string) (client.ProtobufConnection, error) {
			return nil, errors.New("relay down")
		}}))
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).Return(nil, errors.New("LAN down"))

		_, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "direct path: LAN down")
		So(err.Error(), ShouldContainSubstring, "relay path: relay down")
	})
}

// 账号来源收编的行没有 LAN 地址（IsRelayOnly）。它的直连路径不是「拨了没通」而是
// **根本不存在**：拿空 URL 去拨只会浪费一次超时，还把「这台机器本来就没有 LAN 路径」
// 报成一条像是网络故障的错误。中转按指纹寻址，不需要地址。
func TestPool_Borrow_RelayOnlyRow_SkipsTheDirectPathEntirely(t *testing.T) {
	Convey("relay-only row: never dials direct, connects over the relay", t, func() {
		relayClient := stubClient()
		var gotDaemonFP, gotPeerFP string
		f := newPoolFixture(t, remote_device_svc.WithRelayDial(stubRelayDial{open: func(_ context.Context, daemonFP, peerFP string) (client.ProtobufConnection, error) {
			gotDaemonFP, gotPeerFP = daemonFP, peerFP
			return relayClient, nil
		}}))
		f.device.URL = "" // 收编自账号，本机从没配对过它
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		// dial.Open 没有 EXPECT：被调用一次就是失败（gomock 会报 unexpected call）。

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
		So(gotDaemonFP, ShouldEqual, "sha256:abc")
		So(gotPeerFP, ShouldEqual, "fp-x")
	})
}

// 没有 relay 又没有 LAN 地址 = 无路可走。必须当场说清楚，而不是拿空地址去拨一次。
func TestPool_Borrow_RelayOnlyRow_WithoutRelayConfigured_FailsClearly(t *testing.T) {
	Convey("relay-only row but no relay wired: fails without dialing", t, func() {
		f := newPoolFixture(t)
		f.device.URL = ""
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)

		_, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "no LAN address")
	})
}

// stubAccountCredential 是 AccountCredentialPort 的假替身:返回固定的账号凭据
// (生产实现是 server_svc,未登录时返回空串)。
type stubAccountCredential struct{ value string }

func (s stubAccountCredential) AccessToken() string { return s.value }

// 直连路径的凭据优先级:本机对这台 daemon 没有配对、但账号已登录时,直连出示
// 账号凭据(auth.account)而不是配对令牌 —— 这才让 R3「server 不可用时 daemon
// 用缓存公钥离线验签、照常接受同账号客户端」在直连上真实发生。
func TestPool_Borrow_NoLocalPairing_WithAccountCredential_DialsAccountHandshake(t *testing.T) {
	Convey("no local pairing but an account credential: the direct dial presents auth.account", t, func() {
		f := newPoolFixture(t, remote_device_svc.WithAccountCredential(stubAccountCredential{value: "acct-jwt"}))
		_ = f.kc.Delete("agentre-daemon-token-42")
		c := stubClient()
		var got remote_device_svc.AccountArgs
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		// dial.Open 没有 EXPECT:配对握手若还被调到,gomock 直接判失败(R2 的反面)。
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, args remote_device_svc.AccountArgs) (client.ProtobufConnection, error) {
				got = args
				return c, nil
			})

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
		So(got.Credential, ShouldEqual, "acct-jwt")
		So(got.URL, ShouldEqual, "wss://example/rpc")
		So(got.TLSMode, ShouldEqual, "skip-verify")
		// R5 硬不变量:账号路径复用 keychain 里的 LAN 配对指纹,不另生成。
		So(got.DeviceFingerprint, ShouldEqual, "fp-x")
		So(got.ExpectedDaemonFingerprint, ShouldEqual, "sha256:abc")
	})
}

// R2 硬约束:已配对的对端继续走 auth.connect,账号凭据在场也不改路。
func TestPool_Borrow_LocalPairing_KeepsConnectHandshake(t *testing.T) {
	Convey("a locally paired daemon keeps using auth.connect even when an account credential exists", t, func() {
		f := newPoolFixture(t, remote_device_svc.WithAccountCredential(stubAccountCredential{value: "acct-jwt"}))
		c := stubClient()
		var got remote_device_svc.ConnectArgs
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		// dial.OpenAccount 没有 EXPECT:账号握手若被调到就是 R2 回归。
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, args remote_device_svc.ConnectArgs) (client.ProtobufConnection, error) {
				got = args
				return c, nil
			})

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
		So(got.DeviceToken, ShouldEqual, "tok-42")
		So(got.DeviceFingerprint, ShouldEqual, "fp-x")
		So(got.ExpectedDaemonFingerprint, ShouldEqual, "sha256:abc")
	})
}

// 既无配对令牌、账号也未登录(AccessToken 空)→ 无从出示身份,维持既有拒绝。
func TestPool_Borrow_NoLocalPairing_NoAccountCredential_StaysUnauthorized(t *testing.T) {
	Convey("no pairing and no account credential → ErrDeviceUnauthorized, nothing is dialed", t, func() {
		f := newPoolFixture(t, remote_device_svc.WithAccountCredential(stubAccountCredential{value: ""}))
		_ = f.kc.Delete("agentre-daemon-token-42")
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)

		_, err := f.pool.Borrow(context.Background(), 42)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeTrue)
	})
}

// 账号握手被 daemon 拒(凭据过期 / 被吊销 / 账号不符)同样是终止条件:
// chat_svc.terminalBorrowError 靠 ErrDeviceUnauthorized 判定「重试也没用」,
// 同时保留 R6 要求的两条路径各自原因。
func TestPool_Borrow_NoLocalPairing_AccountRejected_StaysDeviceUnauthorized(t *testing.T) {
	Convey("account handshake rejected: still ErrDeviceUnauthorized, both path reasons kept", t, func() {
		f := newPoolFixture(t,
			remote_device_svc.WithAccountCredential(stubAccountCredential{value: "acct-jwt"}),
			remote_device_svc.WithRelayDial(stubRelayDial{open: func(_ context.Context, _, _ string) (client.ProtobufConnection, error) {
				return nil, errors.New("relay down")
			}}))
		_ = f.kc.Delete("agentre-daemon-token-42")
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).Return(nil, remote_device_svc.ErrUnauthorized)

		_, err := f.pool.Borrow(context.Background(), 42)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeTrue)
		So(err.Error(), ShouldContainSubstring, "direct path: unauthorized")
		So(err.Error(), ShouldContainSubstring, "relay path: relay down")
	})
}

// R5:未配对时两条路径并发,直连的 auth.account 与中转的 auth.account 呈现的
// 是同一个对端标识 —— 路径切换不会在 daemon 眼里变成另一个对端。
func TestPool_Borrow_NoLocalPairing_BothPathsPresentSamePeerIdentity(t *testing.T) {
	Convey("account credential on both paths: direct and relay present the same peer fingerprint", t, func() {
		var relayPeerFP string
		f := newPoolFixture(t,
			remote_device_svc.WithAccountCredential(stubAccountCredential{value: "acct-jwt"}),
			remote_device_svc.WithRelayDial(stubRelayDial{open: func(_ context.Context, _, peerFP string) (client.ProtobufConnection, error) {
				relayPeerFP = peerFP
				return nil, errors.New("relay down")
			}}))
		_ = f.kc.Delete("agentre-daemon-token-42")
		c := stubClient()
		var directPeerFP string
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, args remote_device_svc.AccountArgs) (client.ProtobufConnection, error) {
				directPeerFP = args.DeviceFingerprint
				return c, nil
			})

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
		So(directPeerFP, ShouldEqual, "fp-x")
		So(relayPeerFP, ShouldEqual, directPeerFP)
	})
}
