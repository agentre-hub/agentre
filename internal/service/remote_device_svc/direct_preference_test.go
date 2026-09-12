package remote_device_svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	svcmock "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func directArgsFixture() remote_device_svc.DirectArgs {
	return remote_device_svc.DirectArgs{
		URLs:                      accountDirectURLs,
		CertPEM:                   "pinned-cert-pem",
		Credential:                "direct-cred",
		ExpectedDaemonFingerprint: "sha256:abc",
	}
}

func isClosed(c *stubProtobufConnection) bool {
	select {
	case <-c.Closed():
		return true
	default:
		return false
	}
}

// Given 一台同网段的已配对 agentred:直连要做一次完整的 TLS 握手(几十毫秒),中转
// 却是在一条早已连着的常驻链路上开一条虚拟通道(几乎不花时间)。When 两条路一起
// 发起,Then 在用的那条必须是直连 —— 先答的那条赢,等于把每一台局域网里的机器都
// 钉死在账号服务上:账号服务一停,同网段的机器跟着一起不可用(F11)。
func TestRaceAccountDirect_GivenTheRelayAnswersFirst_WhenTheDirectPathIsReachable_ThenTheDirectConnectionIsUsed(t *testing.T) {
	Convey("中转更快答上来,但直连也通:用直连,不把设备钉在账号服务上", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		dial := svcmock.NewMockDaemonDialPort(ctrl)
		directConn := newStubProtobufConnection()
		dial.EXPECT().OpenDirect(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ remote_device_svc.DirectArgs) (client.ProtobufConnection, string, error) {
				time.Sleep(30 * time.Millisecond) // 一次真实的 LAN TLS 握手
				return directConn, accountDirectURLs[1], nil
			})
		relayConn := newStubProtobufConnection()
		relay := &relaySpy{conn: relayConn}

		conn, address, err := remote_device_svc.RaceAccountDirect(
			context.Background(), dial, relay, directArgsFixture(), devicefp.Initiator("fp-x"))

		So(err, ShouldBeNil)
		So(conn, ShouldEqual, client.ProtobufConnection(directConn))
		So(address, ShouldEqual, accountDirectURLs[1])
		// 中转照样发起(R6:两条路径同时试),只是输了就把通道关掉,不留一条挂在
		// 账号服务上的连接。
		So(relay.wasCalled(), ShouldBeTrue)
		So(isClosed(relayConn), ShouldBeTrue)
		So(isClosed(directConn), ShouldBeFalse)
	})
}

// 直连拨不通时,中转照样是这台机器的出路:偏好不等于「只走直连」。
func TestRaceAccountDirect_GivenTheDirectPathIsUnreachable_ThenTheRelayStillWins(t *testing.T) {
	Convey("直连不通:中转胜出,地址位不动", t, func() {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		dial := svcmock.NewMockDaemonDialPort(ctrl)
		dial.EXPECT().OpenDirect(gomock.Any(), gomock.Any()).
			Return(nil, "", errors.New("no route to host"))
		relayConn := newStubProtobufConnection()
		relay := &relaySpy{conn: relayConn}

		conn, address, err := remote_device_svc.RaceAccountDirect(
			context.Background(), dial, relay, directArgsFixture(), devicefp.Initiator("fp-x"))

		So(err, ShouldBeNil)
		So(conn, ShouldEqual, client.ProtobufConnection(relayConn))
		So(address, ShouldEqual, "")
		So(isClosed(relayConn), ShouldBeFalse)
	})
}

// Given 一台本机 LAN 配对过的 agentred:中转与直连都答得上来,When 账号服务随后停掉、
// 那条中转通道被关掉,Then 池子里的连接不该跟着失效 —— 在用的本来就该是直连,账号
// 服务只负责发现机器与发凭证,连上之后不再参与(F11 的方向)。
func TestPool_Borrow_GivenBothPathsAnswer_WhenTheRelayDies_ThenTheEntrySurvives(t *testing.T) {
	Convey("直连与中转都通:用直连;中转随账号服务一起断掉时,这条连接照常活着", t, func() {
		relayConn := newStubProtobufConnection()
		f := newPoolFixture(t, remote_device_svc.WithRelayDial(&relaySpy{conn: relayConn}))
		directConn := newStubProtobufConnection()
		f.repo.EXPECT().Get(gomock.Any(), int64(42)).Return(f.device, nil)
		f.dial.EXPECT().Open(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, _ remote_device_svc.ConnectArgs) (client.ProtobufConnection, error) {
				time.Sleep(30 * time.Millisecond) // 一次真实的 LAN TLS 握手
				return directConn, nil
			})

		lease, err := f.pool.Borrow(context.Background(), 42)
		So(err, ShouldBeNil)
		So(lease.Client(), ShouldNotBeNil)
		// 落选的中转通道就地关掉,不留一条挂在账号服务上的连接。
		So(isClosed(relayConn), ShouldBeTrue)

		// 账号服务停掉:中转那条没了。在用的是直连,池子里的租约不受影响。
		_ = relayConn.Close()
		time.Sleep(50 * time.Millisecond)
		select {
		case <-lease.Closed():
			t.Fatal("中转断开不该驱逐直连连接")
		default:
		}
		So(isClosed(directConn), ShouldBeFalse)
	})
}
