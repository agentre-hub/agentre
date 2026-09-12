package remote_device_svc_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

var accountDirectURLs = []string{"wss://10.0.0.5:7456/rpc", "wss://192.168.1.9:7456/rpc"}

// asAccountDirect 把 fixture 的设备行改成「来自账号的直连」行(task 8):地址位是下发
// 列表的第一个、证书固定,本地直连凭据住在与配对令牌同一个钥匙串槽里。
func (f *poolFixture) asAccountDirect() {
	f.device.Origin = "account"
	f.device.URL = accountDirectURLs[0]
	f.device.TLSMode = "pin-cert"
	f.device.TLSCertPEM = "pinned-cert-pem"
	f.device.SetDirectURLs(accountDirectURLs)
	_ = f.kc.Set("agentre-daemon-token-42", "direct-cred")
}

// relaySpy 记下中转是否被发起过,并交回预设结果。
type relaySpy struct {
	mu     sync.Mutex
	called bool
	conn   client.ProtobufConnection
	err    error
}

func (r *relaySpy) Open(context.Context, devicefp.Carrier, devicefp.Initiator) (client.ProtobufConnection, error) {
	r.mu.Lock()
	r.called = true
	r.mu.Unlock()
	return r.conn, r.err
}

func (r *relaySpy) wasCalled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.called
}

func accountServerUnreachable() error {
	return &rpcerror.Error{Code: rpcerror.CodeAccountServerUnreachable, Message: "Account server unreachable"}
}

// D7 + D9:来自账号的直连行 —— 直连走 auth.direct(全部地址、固定证书、钥匙串槽里的直连
// 凭据),与中转同时发起;server 不可达时直连照样交出租约,赢下的地址记为最近地址。
func TestPool_Borrow_AccountDirectRow_RacesAuthDirectAgainstTheRelay_AndRecordsTheWinningAddress(t *testing.T) {
	Convey("account-direct row, server unreachable: auth.direct wins, winning address recorded", t, func() {
		recorder := &spyRecorder{}
		relay := &relaySpy{err: errors.New("server unreachable")}
		creds := &refreshingCredential{value: "acct-jwt", next: "fresh-jwt"}
		f := newPoolFixture(t,
			remote_device_svc.WithRelayDial(relay),
			remote_device_svc.WithAccountCredential(creds),
			remote_device_svc.WithAccountDirectRecorder(recorder))
		f.asAccountDirect()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		var got remote_device_svc.DirectArgs
		// Open / OpenAccount 没有 EXPECT:钥匙串槽里的直连凭据若被当成配对令牌或走了
		// auth.account,gomock 当场判失败 —— 那正是 task 8 context 点名的回归。
		f.dial.EXPECT().OpenDirect(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, args remote_device_svc.DirectArgs) (client.ProtobufConnection, string, error) {
				got = args
				return newStubProtobufConnection(), accountDirectURLs[1], nil
			})

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
		So(got.URLs, ShouldResemble, accountDirectURLs)
		So(got.CertPEM, ShouldEqual, "pinned-cert-pem")
		So(got.Credential, ShouldEqual, "direct-cred")
		So(got.ExpectedDaemonFingerprint, ShouldEqual, devicefp.Carrier("sha256:abc"))
		So(relay.wasCalled(), ShouldBeTrue)
		So(creds.refreshed, ShouldEqual, 0)
		So(recorder.directSuccessCalls(), ShouldResemble, []directSuccessCall{{deviceID: 42, address: accountDirectURLs[1]}})
	})
}

// D16/D17:直连失败(证书被换 / 地址路由不到)、中转可用 —— 中转胜出,设备照常可用,
// 没有直连赢过就不改最近地址。
func TestPool_Borrow_AccountDirectRow_DirectFails_RelayWins_NoAddressRecorded(t *testing.T) {
	Convey("direct fails, relay wins: lease handed out, no direct address recorded", t, func() {
		recorder := &spyRecorder{}
		relayConn := newStubProtobufConnection()
		f := newPoolFixture(t,
			remote_device_svc.WithRelayDial(&relaySpy{conn: relayConn}),
			remote_device_svc.WithAccountDirectRecorder(recorder))
		f.asAccountDirect()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenDirect(gomock.Any(), gomock.Any()).
			Return(nil, "", errors.New("tls: server cert does not match pinned cert"))

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
		So(recorder.directSuccessCalls(), ShouldBeEmpty)
	})
}

// D18:server 不可达且直连也不可达 —— 与今天仅中转设备一样是一次可重试的失败,
// 两条路径的原因都在,不是「重试也没用」。
func TestPool_Borrow_AccountDirectRow_BothPathsFail_IsARetryableFailure(t *testing.T) {
	Convey("both paths fail: retryable error naming both reasons", t, func() {
		f := newPoolFixture(t, remote_device_svc.WithRelayDial(&relaySpy{err: errors.New("relay down")}))
		f.asAccountDirect()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenDirect(gomock.Any(), gomock.Any()).Times(1).
			Return(nil, "", errors.New("no route to host"))

		_, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldNotBeNil)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeFalse)
		So(err.Error(), ShouldContainSubstring, "direct path: no route to host")
		So(err.Error(), ShouldContainSubstring, "relay path: relay down")
	})
}

// 来自账号的直连行、钥匙串槽为空:没有直连凭据可发,不拿空凭据做 auth.direct;沿用
// 「本机没有配对」的既有路径(账号凭据 + 固定证书),中转照常竞速并重新下发凭据。
func TestPool_Borrow_AccountDirectRow_EmptyCredentialSlot_NeverSendsAnEmptyAuthDirect(t *testing.T) {
	Convey("empty slot: no auth.direct, falls back to the no-pairing account path", t, func() {
		f := newPoolFixture(t,
			remote_device_svc.WithRelayDial(&relaySpy{err: errors.New("relay down")}),
			remote_device_svc.WithAccountCredential(stubAccountCredential{value: "acct-jwt"}))
		f.asAccountDirect()
		_ = f.kc.Delete("agentre-daemon-token-42")
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		var got remote_device_svc.AccountArgs
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, args remote_device_svc.AccountArgs) (client.ProtobufConnection, error) {
				got = args
				return newStubProtobufConnection(), nil
			})

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
		So(got.TLSMode, ShouldEqual, "pin-cert")
		So(got.TLSCertPEM, ShouldEqual, "pinned-cert-pem")
		So(got.Credential, ShouldEqual, "acct-jwt")
	})
}

// auth.direct 答 -32001 说明手上那张本地直连凭据过时了(agentred 对账删了它、重新登录过),
// 不是账号凭据被拒:不换账号票,也不判「重试也没用」——中转的账号握手会重新下发一张。
func TestPool_Borrow_AccountDirectRow_StaleDirectCredential_NeitherRefreshesNorIsTerminal(t *testing.T) {
	Convey("auth.direct -32001 while the relay is down: retryable, no account refresh", t, func() {
		creds := &refreshingCredential{value: "acct-jwt", next: "fresh-jwt"}
		f := newPoolFixture(t,
			remote_device_svc.WithRelayDial(&relaySpy{err: errors.New("relay down")}),
			remote_device_svc.WithAccountCredential(creds))
		f.asAccountDirect()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenDirect(gomock.Any(), gomock.Any()).Times(1).Return(nil, "", remote_device_svc.ErrUnauthorized)

		_, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldNotBeNil)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeFalse)
		So(creds.refreshed, ShouldEqual, 0)
	})
}

// 登出会把账号直连行清掉并去掉;一次登出前就已握完手、登出后才走到记录这一步的借用,不得
// 把行和钥匙串里的凭据重新建回来——已登出(没有账号凭据)时,中转握手带回的下发一律不记。
func TestPool_Borrow_LoggedOut_DoesNotRecordTheDelivery(t *testing.T) {
	Convey("relay-won borrow finishing after logout: the delivery is not recorded", t, func() {
		recorder := &spyRecorder{}
		relayConn := &deliveringConnection{
			stubProtobufConnection: newStubProtobufConnection(),
			urls:                   accountDirectURLs, certPEM: "pinned-cert-pem", credential: "direct-cred",
		}
		f := newPoolFixture(t,
			remote_device_svc.WithRelayDial(&relaySpy{conn: relayConn}),
			remote_device_svc.WithAccountDirectRecorder(recorder),
			remote_device_svc.WithAccountCredential(stubAccountCredential{value: ""}))
		f.asAccountDirect()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenDirect(gomock.Any(), gomock.Any()).Return(nil, "", errors.New("no route to host"))

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
		So(recorder.callCount(), ShouldEqual, 0)
	})
}

// H3(调用侧):「账号服务不可达」-32007 不是凭据被拒 —— 直连与中转两条路径上都不换票、
// 不判终止,交回可重试的失败。
func TestPool_Borrow_AccountServerUnreachable_NeitherRefreshesNorIsTerminal(t *testing.T) {
	Convey("account-direct row: auth.direct and relay both answer -32007", t, func() {
		creds := &refreshingCredential{value: "acct-jwt", next: "fresh-jwt"}
		f := newPoolFixture(t,
			remote_device_svc.WithRelayDial(&relaySpy{err: fmt.Errorf("server_svc: relay handshake: %w", accountServerUnreachable())}),
			remote_device_svc.WithAccountCredential(creds))
		f.asAccountDirect()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenDirect(gomock.Any(), gomock.Any()).Times(1).Return(nil, "", accountServerUnreachable())

		_, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldNotBeNil)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeFalse)
		So(creds.refreshed, ShouldEqual, 0)
	})

	Convey("relay-only row: the relay's auth.account answers -32007", t, func() {
		creds := &refreshingCredential{value: "acct-jwt", next: "fresh-jwt"}
		relay := &relaySpy{err: fmt.Errorf("server_svc: relay handshake: %w", accountServerUnreachable())}
		f := newPoolFixture(t,
			remote_device_svc.WithRelayDial(relay),
			remote_device_svc.WithAccountCredential(creds))
		f.device.URL = ""
		_ = f.kc.Delete("agentre-daemon-token-42")
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)

		_, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldNotBeNil)
		So(relay.wasCalled(), ShouldBeTrue)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeFalse)
		So(creds.refreshed, ShouldEqual, 0)
	})

	Convey("no-pairing row: the direct auth.account answers -32007", t, func() {
		creds := &refreshingCredential{value: "acct-jwt", next: "fresh-jwt"}
		f := newPoolFixture(t,
			remote_device_svc.WithRelayDial(&relaySpy{err: errors.New("relay down")}),
			remote_device_svc.WithAccountCredential(creds))
		_ = f.kc.Delete("agentre-daemon-token-42")
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().OpenAccount(gomock.Any(), gomock.Any()).Times(1).Return(nil, accountServerUnreachable())

		_, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldNotBeNil)
		So(errors.Is(err, remote_device_svc.ErrDeviceUnauthorized), ShouldBeFalse)
		So(creds.refreshed, ShouldEqual, 0)
	})
}
