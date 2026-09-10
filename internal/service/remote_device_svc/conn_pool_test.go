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
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	repomock "github.com/agentre-hub/agentre/internal/repository/remote_device_repo/mock_remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	svcmock "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
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
// (生产实现是 server_svc,未登录时返回空串)。换票恒成功但换回来的还是同一张,
// 于是「凭据被拒就换一张再试一次」在它身上只是多拨一次,行为不变。
type stubAccountCredential struct{ value string }

func (s stubAccountCredential) AccessToken() string           { return s.value }
func (s stubAccountCredential) Refresh(context.Context) error { return nil }

// refreshingCredential 是会真的换出新票的那种:第一次之后 AccessToken 交回 next。
type refreshingCredential struct {
	mu        sync.Mutex
	value     string
	next      string
	err       error
	refreshed int
}

func (c *refreshingCredential) AccessToken() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func (c *refreshingCredential) Refresh(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshed++
	if c.err != nil {
		return c.err
	}
	c.value = c.next
	return nil
}

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
//
// 「被拒」现在要问两遍才算数:第一次被拒会先换一张凭据重拨(见
// TestPool_Borrow_AccountRejected_RefreshesCredentialAndRetriesOnce ——
// 过期与被撤销在线上是同一个 -32001,只有换一张才分得开)。这里的假凭据换完还是
// 同一个值,于是第二次照样被拒,终止的结论不变。
func TestPool_Borrow_NoLocalPairing_AccountRejected_StaysDeviceUnauthorized(t *testing.T) {
	Convey("account handshake rejected: still ErrDeviceUnauthorized, both path reasons kept", t, func() {
		f := newPoolFixture(t,
			remote_device_svc.WithAccountCredential(stubAccountCredential{value: "acct-jwt"}),
			remote_device_svc.WithRelayDial(stubRelayDial{open: func(_ context.Context, _, _ string) (client.ProtobufConnection, error) {
				return nil, errors.New("relay down")
			}}))
		_ = f.kc.Delete("agentre-daemon-token-42")
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).Times(2).
			Return(nil, remote_device_svc.ErrUnauthorized)

		_, err := f.pool.Borrow(context.Background(), 42)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeTrue)
		So(err.Error(), ShouldContainSubstring, "direct path: unauthorized")
		So(err.Error(), ShouldContainSubstring, "relay path: relay down")
	})
}

// R5:未配对时两条路径并发,直连与中转在 daemon 眼里是同一个对端 —— 路径切换不会
// 变成另一个对端。决策 8 之后这件事的成立方式变了:身份不再由两条路径各自「出示」
// 一个字符串然后指望它们相等,而是由**同一枚账号凭据**的 pfp claim 决定,daemon 从
// 验过的凭据里取(见 daemon/auth 的 HandleAccount 测试)。所以这里钉的是:两条路径
// 用的是同一份账号材料,且直连侧已经没有任何可自报的身份字段。
func TestPool_Borrow_NoLocalPairing_BothPathsPresentSamePeerIdentity(t *testing.T) {
	Convey("account credential on both paths: one credential decides the identity on both", t, func() {
		var relayPeerFP string
		f := newPoolFixture(t,
			remote_device_svc.WithAccountCredential(stubAccountCredential{value: "acct-jwt"}),
			remote_device_svc.WithRelayDial(stubRelayDial{open: func(_ context.Context, _, peerFP string) (client.ProtobufConnection, error) {
				relayPeerFP = peerFP
				return nil, errors.New("relay down")
			}}))
		_ = f.kc.Delete("agentre-daemon-token-42")
		c := stubClient()
		var directCredential string
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, args remote_device_svc.AccountArgs) (client.ProtobufConnection, error) {
				directCredential = args.Credential
				return c, nil
			})

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
		So(directCredential, ShouldEqual, "acct-jwt")
		So(relayPeerFP, ShouldEqual, "fp-x")
	})
}

// SelfFingerprint 满足 client.ProtobufConnection:本端在这条连接上出示的设备指纹。
// 这个假连接从没握过手,所以是空 —— 与生产里未鉴权的直连一致。
func (c *stubProtobufConnection) SelfFingerprint() string { return "sha256:test-self" }

// 账号凭据被拒之后，先换一张再试一次 —— 而不是当场判「重试也没用」。
//
// 起因：daemon 对**过期**与**被撤销**回同一个 -32001（daemon/auth 的
// accountCredentialError 两者都用 ErrUnauthorized.Code），而桌面端的 access token
// 只活 15 分钟、刷新是被动的（撞上 HTTP 401 才刷）。一次必然自愈的过期因此会被翻成
// ErrDeviceUnauthorized，chat_svc.terminalBorrowError 据它翻成 ErrReconnectAbandoned，
// 重连循环当场放弃（runtimes/remote/reconnect.go 的 abandoned 分支）。
//
// 这里不去猜是过期还是撤销 —— 那要靠比对错误文案，等于把 daemon 的措辞变成契约。
// 换一张再试一次能同时答对两种：过期的换完就连上，撤销的换完照样被拒。
func TestPool_Borrow_AccountRejected_RefreshesCredentialAndRetriesOnce(t *testing.T) {
	Convey("凭据被拒：换一张新的再试一次，成功就正常交出租约", t, func() {
		creds := &refreshingCredential{value: "stale-jwt", next: "fresh-jwt"}
		f := newPoolFixture(t, remote_device_svc.WithAccountCredential(creds))
		_ = f.kc.Delete("agentre-daemon-token-42")
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)

		presented := []string{}
		conn := newStubProtobufConnection()
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).Times(2).
			DoAndReturn(func(_ context.Context, args remote_device_svc.AccountArgs) (client.ProtobufConnection, error) {
				presented = append(presented, args.Credential)
				if args.Credential == "fresh-jwt" {
					return conn, nil
				}
				return nil, remote_device_svc.ErrUnauthorized
			})

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease, ShouldNotBeNil)
		So(presented, ShouldResemble, []string{"stale-jwt", "fresh-jwt"})
		So(creds.refreshed, ShouldEqual, 1)
	})
}

// 换完还被拒才是真的被拒：这时 ErrDeviceUnauthorized 说的是实话，上层据它停掉重连。
func TestPool_Borrow_AccountRejectedAfterRefresh_IsTerminal(t *testing.T) {
	Convey("换过一张仍被拒：才是 ErrDeviceUnauthorized", t, func() {
		creds := &refreshingCredential{value: "old-jwt", next: "new-jwt"}
		f := newPoolFixture(t, remote_device_svc.WithAccountCredential(creds))
		_ = f.kc.Delete("agentre-daemon-token-42")
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).Times(2).
			Return(nil, remote_device_svc.ErrUnauthorized)

		_, err := f.pool.Borrow(context.Background(), 42)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeTrue)
		So(creds.refreshed, ShouldEqual, 1)
	})
}

// 换不到新票（server 够不着）时**不许**宣告终止。
//
// 这正是 R3 那条路径最要紧的一段：server 挂了，轮询刷不动 token，十几分钟后账号
// 凭据过期。此时说「重试也没用」是错的 —— server 回来就好了。判成终止的话，会话在
// server 恢复之前再也接不回去，而且没有任何东西会重新尝试。
func TestPool_Borrow_AccountRejected_RefreshUnavailable_StaysRetryable(t *testing.T) {
	Convey("换票失败：交回可重试的失败，不是终止哨兵", t, func() {
		creds := &refreshingCredential{value: "stale-jwt", err: errors.New("server: refresh failed")}
		f := newPoolFixture(t, remote_device_svc.WithAccountCredential(creds))
		_ = f.kc.Delete("agentre-daemon-token-42")
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).Times(1).
			Return(nil, remote_device_svc.ErrUnauthorized)

		_, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldNotBeNil)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeFalse)
		So(creds.refreshed, ShouldEqual, 1)
	})
}

// 同时有几台设备被拒时，只换一张票。
//
// server_svc.refresh 会**轮换 refresh_token**（响应里带新的、覆盖 keychain），而它
// 自己没有串行化。同时发起两次刷新，后写的那次可能把已经作废的那张存回 keychain
// ——下一次刷新被服务端拒，本地登录被清掉。重连风暴（几台远端设备同时重连）正好
// 是这种并发的来源，所以换票这件事在池子这一侧必须是单飞的。
func TestPool_Borrow_ConcurrentRejections_RefreshOnlyOnce(t *testing.T) {
	Convey("两台设备同时被拒:只换一张票,两边都用上新的", t, func() {
		creds := &refreshingCredential{value: "stale-jwt", next: "fresh-jwt"}
		f := newPoolFixture(t, remote_device_svc.WithAccountCredential(creds))
		_ = f.kc.Delete("agentre-daemon-token-42")
		_ = f.kc.Delete("agentre-daemon-token-43")
		other := *f.device
		other.ID = 43
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.repo.EXPECT().Get(gomock.Any(), int64(43)).Return(&other, nil)

		var mu sync.Mutex
		presented := map[string]int{}
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).AnyTimes().
			DoAndReturn(func(_ context.Context, args remote_device_svc.AccountArgs) (client.ProtobufConnection, error) {
				mu.Lock()
				presented[args.Credential]++
				mu.Unlock()
				if args.Credential == "fresh-jwt" {
					return newStubProtobufConnection(), nil
				}
				return nil, remote_device_svc.ErrUnauthorized
			})

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, id := range []int64{42, 43} {
			wg.Add(1)
			go func(i int, id int64) {
				defer wg.Done()
				_, errs[i] = f.pool.Borrow(context.Background(), id)
			}(i, id)
		}
		wg.Wait()

		So(errs[0], ShouldBeNil)
		So(errs[1], ShouldBeNil)
		So(creds.refreshed, ShouldEqual, 1)
		So(presented["fresh-jwt"], ShouldEqual, 2)
	})
}
