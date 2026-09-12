package remote_device_svc_test

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	. "github.com/smartystreets/goconvey/convey"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// fakeAccountJWT 是测试用的假账号凭据(真实现是 server 签发的 RS256 JWT)。
const fakeAccountJWT = "acct-jwt"

// fakeDaemon 是一台假 agentred:升级 websocket,记录收到的每一帧,并对请求回
// 固定结果(或固定错误)。用来验证直连握手在线路上到底发了什么。
type fakeDaemon struct {
	srv *httptest.Server

	mu     sync.Mutex
	frames []*agentrewire.Request
}

// newFakeDaemonAtVersion 是一台只在「报什么协议版本」上与本机不同的 agentred。
func newFakeDaemonAtVersion(t *testing.T, instanceUUID, protocolVersion string) *fakeDaemon {
	t.Helper()
	return newFakeDaemonWith(t, instanceUUID, nil, protocolVersion)
}

func newFakeDaemon(t *testing.T, instanceUUID string, reject *rpcerror.Error) *fakeDaemon {
	t.Helper()
	return newFakeDaemonWith(t, instanceUUID, reject, wireversion.Protocol)
}

func newFakeDaemonWith(t *testing.T, instanceUUID string, reject *rpcerror.Error, protocolVersion string) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{}
	d.srv = httptest.NewServer(d.handler(instanceUUID, reject, protocolVersion))
	t.Cleanup(d.srv.Close)
	return d
}

// newTLSFakeDaemon 是只听 wss 的同一台假 agentred。cert 为 nil 时出示 httptest 自带的
// 证书;给了就出示它 —— 用来造「地址上的证书与固定值不一致」(D16)。
func newTLSFakeDaemon(t *testing.T, instanceUUID string, reject *rpcerror.Error, cert *tls.Certificate) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{}
	d.srv = httptest.NewUnstartedServer(d.handler(instanceUUID, reject, wireversion.Protocol))
	if cert != nil {
		d.srv.TLS = &tls.Config{Certificates: []tls.Certificate{*cert}, MinVersion: tls.VersionTLS12}
	}
	d.srv.StartTLS()
	t.Cleanup(d.srv.Close)
	return d
}

func (d *fakeDaemon) handler(instanceUUID string, reject *rpcerror.Error, protocolVersion string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := (&websocket.Upgrader{Subprotocols: []string{protorpc.Subprotocol}}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		for {
			kind, payload, err := ws.ReadMessage()
			if err != nil || kind != websocket.BinaryMessage {
				return
			}
			var f agentrewire.RpcFrame
			if proto.Unmarshal(payload, &f) != nil || f.GetRequest() == nil {
				return
			}
			d.record(f.GetRequest())
			out := &agentrewire.RpcFrame{Id: f.GetId()}
			if reject != nil {
				out.Body = &agentrewire.RpcFrame_Error{Error: &agentrewire.RpcError{Code: int32(reject.Code), Message: reject.Message}}
			} else {
				var response []byte
				switch agentrewire.RpcMethod(f.GetRequest().GetMethodId()) {
				case agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR:
					response, _ = proto.Marshal(&agentrewire.AuthPairResponse{DeviceToken: "device-token", DaemonFingerprint: string(identity.DaemonFingerprint(instanceUUID)), InstanceUuid: instanceUUID, ProtocolVersion: protocolVersion, MinSupportedProtocolVersion: wireversion.MinSupported})
				case agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT:
					response, _ = proto.Marshal(&agentrewire.AuthConnectResponse{Ok: true, InstanceUuid: instanceUUID, ProtocolVersion: protocolVersion, MinSupportedProtocolVersion: wireversion.MinSupported})
				case agentrewire.RpcMethod_RPC_METHOD_AUTH_DIRECT:
					response, _ = proto.Marshal(&agentrewire.AuthDirectResponse{Ok: true, InstanceUuid: instanceUUID, ProtocolVersion: protocolVersion, MinSupportedProtocolVersion: wireversion.MinSupported, PeerFingerprint: "sha256:as-the-daemon-sees-me"})
				default:
					// 回写对端认定的本端身份(决策 8):调用方在请求体里已经报不了。
					response, _ = proto.Marshal(&agentrewire.AuthAccountResponse{Ok: true, InstanceUuid: instanceUUID, ProtocolVersion: protocolVersion, MinSupportedProtocolVersion: wireversion.MinSupported, PeerFingerprint: "sha256:as-the-daemon-sees-me"})
				}
				out.Body = &agentrewire.RpcFrame_Response{Response: &agentrewire.Response{MethodId: f.GetRequest().GetMethodId(), EncodedPayload: response}}
			}
			payload, _ = proto.Marshal(out)
			_ = ws.WriteMessage(websocket.BinaryMessage, payload)
		}
	})
}

func TestRealDial_PairAndConnectUseTypedProtobufMethods(t *testing.T) {
	Convey("pair and connect send their stable typed protobuf payloads", t, func() {
		d := newFakeDaemon(t, "uuid-1", nil)
		dial := remote_device_svc.NewDaemonDial()
		pair, err := dial.Pair(context.Background(), remote_device_svc.PairArgs{URL: d.url(), Code: "123456", DeviceName: "desktop", DeviceFingerprint: "sha256:desktop"})
		So(err, ShouldBeNil)
		So(pair.DeviceToken, ShouldEqual, "device-token")
		So(pair.InstanceUUID, ShouldEqual, "uuid-1")
		connected, err := dial.Connect(context.Background(), remote_device_svc.ConnectArgs{URL: d.url(), DeviceFingerprint: "sha256:desktop", DeviceToken: "device-token", ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1")})
		So(err, ShouldBeNil)
		So(connected.InstanceUUID, ShouldEqual, "uuid-1")

		frames := d.received()
		So(len(frames), ShouldEqual, 2)
		So(frames[0].GetMethodId(), ShouldEqual, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR))
		var pairRequest agentrewire.AuthPairRequest
		So(proto.Unmarshal(frames[0].GetEncodedPayload(), &pairRequest), ShouldBeNil)
		So(pairRequest.GetCode(), ShouldEqual, "123456")
		So(frames[1].GetMethodId(), ShouldEqual, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT))
		var connectRequest agentrewire.AuthConnectRequest
		So(proto.Unmarshal(frames[1].GetEncodedPayload(), &connectRequest), ShouldBeNil)
		So(connectRequest.GetExpectedDaemonFingerprint(), ShouldEqual, string(identity.DaemonFingerprint("uuid-1")))
	})
}

func (d *fakeDaemon) url() string { return "ws" + strings.TrimPrefix(d.srv.URL, "http") + "/rpc" }

func (d *fakeDaemon) record(f *agentrewire.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frames = append(d.frames, f)
}

func (d *fakeDaemon) received() []*agentrewire.Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]*agentrewire.Request(nil), d.frames...)
}

// 直连的账号握手就是一次 auth.account:daemon 侧全靠缓存的公钥本地验签(R3),
// 客户端不为此多跑任何一轮。
func TestRealDial_OpenAccount_PresentsCredentialInOneRoundTrip(t *testing.T) {
	Convey("OpenAccount completes with a single auth.account request over the direct connection", t, func() {
		d := newFakeDaemon(t, "uuid-1", nil)

		c, err := remote_device_svc.NewDaemonDial().OpenAccount(context.Background(), remote_device_svc.AccountArgs{
			URL:                       d.url(),
			TLSMode:                   "default",
			Credential:                fakeAccountJWT,
			ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1"),
		})
		So(err, ShouldBeNil)
		So(c, ShouldNotBeNil)
		defer func() { _ = c.Close() }()

		frames := d.received()
		So(len(frames), ShouldEqual, 1)
		So(frames[0].GetMethodId(), ShouldEqual, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT))
		var p agentrewire.AuthAccountRequest
		So(proto.Unmarshal(frames[0].GetEncodedPayload(), &p), ShouldBeNil)
		So(p.GetCredential(), ShouldEqual, "acct-jwt")
		// 决策 8:请求体里没有对端身份可报了 —— daemon 从这枚凭据里取,并在应答里
		// 回写它认定的那个值。
		So(c.SelfFingerprint(), ShouldEqual, "sha256:as-the-daemon-sees-me")
	})
}

// daemon 的六种拒绝理由都以 -32001 返回(桌面端按 code 分类,不看文案),
// 统一映射成 ErrUnauthorized,让 ConnPool 继续把它判成终止条件。
func TestRealDial_OpenAccount_DaemonRejects_MapsToUnauthorized(t *testing.T) {
	Convey("daemon rejects the account credential → ErrUnauthorized", t, func() {
		d := newFakeDaemon(t, "uuid-1", &rpcerror.Error{Code: rpcerror.CodeUnauthorized, Message: "account credential revoked"})

		c, err := remote_device_svc.NewDaemonDial().OpenAccount(context.Background(), remote_device_svc.AccountArgs{
			URL:                       d.url(),
			TLSMode:                   "default",
			Credential:                fakeAccountJWT,
			ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1"),
		})
		So(c, ShouldBeNil)
		So(errors.Is(err, remote_device_svc.ErrUnauthorized), ShouldBeTrue)
	})
}

// 账号握手同样受 TOFU 约束:接到的 daemon 不是本地登记的那台就断开,
// 否则连接会被 ConnPool 按 deviceID 缓存成「那台机器」。
func TestRealDial_OpenAccount_OtherDaemon_MapsToTOFUMismatch(t *testing.T) {
	Convey("the answering daemon is not the pinned one → ErrTOFUMismatch", t, func() {
		d := newFakeDaemon(t, "uuid-other", nil)

		c, err := remote_device_svc.NewDaemonDial().OpenAccount(context.Background(), remote_device_svc.AccountArgs{
			URL:                       d.url(),
			TLSMode:                   "default",
			Credential:                fakeAccountJWT,
			ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1"),
		})
		So(c, ShouldBeNil)
		So(errors.Is(err, remote_device_svc.ErrTOFUMismatch), ShouldBeTrue)
	})

	Convey("no pinned daemon fingerprint at all → still refused, never silently accepted", t, func() {
		d := newFakeDaemon(t, "uuid-1", nil)

		c, err := remote_device_svc.NewDaemonDial().OpenAccount(context.Background(), remote_device_svc.AccountArgs{
			URL:        d.url(),
			TLSMode:    "default",
			Credential: fakeAccountJWT,
		})
		So(c, ShouldBeNil)
		So(errors.Is(err, remote_device_svc.ErrTOFUMismatch), ShouldBeTrue)
	})
}

// Given a real agentred that refuses the subprotocol outright (426), When the
// desktop pairs, Then the dial port must hand the service its protocol sentinel
// — otherwise the panel says "check network and port" about a version problem.
func TestRealDial_GivenDaemonRefusesTheSubprotocol_WhenPairing_ThenReturnsProtocolUnsupported(t *testing.T) {
	Convey("426 折成 ErrProtocolUnsupported", t, func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUpgradeRequired)
		}))
		defer server.Close()
		url := "ws" + strings.TrimPrefix(server.URL, "http") + "/rpc"

		dial := remote_device_svc.NewDaemonDial()
		_, err := dial.Pair(context.Background(), remote_device_svc.PairArgs{URL: url, Code: "123456", DeviceFingerprint: "sha256:desktop"})

		So(errors.Is(err, remote_device_svc.ErrProtocolUnsupported), ShouldBeTrue)
	})
}

// Given a real agentred from another revision, When the desktop connects, Then
// the version disagreement arrives as its own sentinel rather than as a generic
// dial failure.
func TestRealDial_GivenDaemonSpeaksAnotherVersion_WhenConnecting_ThenReturnsProtocolVersionMismatch(t *testing.T) {
	Convey("版本对不上折成 ErrProtocolVersionMismatch", t, func() {
		d := newFakeDaemonAtVersion(t, "uuid-1", "0.0.9")
		dial := remote_device_svc.NewDaemonDial()

		_, err := dial.Connect(context.Background(), remote_device_svc.ConnectArgs{
			URL: d.url(), DeviceFingerprint: "sha256:desktop", DeviceToken: "device-token",
			ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1"),
		})

		So(errors.Is(err, remote_device_svc.ErrProtocolVersionMismatch), ShouldBeTrue)
	})

	Convey("对端压根不报版本,同样折成 ErrProtocolVersionMismatch", t, func() {
		d := newFakeDaemonAtVersion(t, "uuid-1", "")
		dial := remote_device_svc.NewDaemonDial()

		_, err := dial.Connect(context.Background(), remote_device_svc.ConnectArgs{
			URL: d.url(), DeviceFingerprint: "sha256:desktop", DeviceToken: "device-token",
			ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1"),
		})

		So(errors.Is(err, remote_device_svc.ErrProtocolVersionMismatch), ShouldBeTrue)
	})
}

// Given 一台冒充的 agentred（局域网里抢到地址、拿到过配对令牌，或干脆只是同一个
// 地址上换了一台机器），它在 auth.connect 里回自己的 instance uuid，When 桌面端
// 握手，Then 桌面端必须自己按这个 uuid 重算 TOFU 指纹并与本地登记值核对、不一致就
// 拒绝 —— 只把 expectedDaemonFingerprint 发过去、由对端自己判自己，等于没有校验。
func TestRealDial_GivenAnotherDaemonAnswers_WhenConnecting_ThenRefusesWithTOFUMismatch(t *testing.T) {
	Convey("auth.connect 由另一台 daemon 应答 → ErrTOFUMismatch", t, func() {
		d := newFakeDaemon(t, "uuid-impostor", nil)
		dial := remote_device_svc.NewDaemonDial()
		args := remote_device_svc.ConnectArgs{
			URL: d.url(), DeviceFingerprint: "sha256:desktop", DeviceToken: "device-token",
			ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1"),
		}

		_, err := dial.Connect(context.Background(), args)
		So(errors.Is(err, remote_device_svc.ErrTOFUMismatch), ShouldBeTrue)
	})

	Convey("auth.connect 的长连接版本(Open)同样拒绝", t, func() {
		d := newFakeDaemon(t, "uuid-impostor", nil)
		dial := remote_device_svc.NewDaemonDial()

		c, err := dial.Open(context.Background(), remote_device_svc.ConnectArgs{
			URL: d.url(), DeviceFingerprint: "sha256:desktop", DeviceToken: "device-token",
			ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1"),
		})
		So(c, ShouldBeNil)
		So(errors.Is(err, remote_device_svc.ErrTOFUMismatch), ShouldBeTrue)
	})

	Convey("本地登记的那台 daemon 应答 → 照常连通，ActualFingerprint 是实测值", t, func() {
		d := newFakeDaemon(t, "uuid-1", nil)
		dial := remote_device_svc.NewDaemonDial()
		args := remote_device_svc.ConnectArgs{
			URL: d.url(), DeviceFingerprint: "sha256:desktop", DeviceToken: "device-token",
			ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1"),
		}

		res, err := dial.Connect(context.Background(), args)
		So(err, ShouldBeNil)
		So(res.InstanceUUID, ShouldEqual, "uuid-1")
		So(res.ActualFingerprint, ShouldEqual, identity.DaemonFingerprint("uuid-1"))

		c, err := dial.Open(context.Background(), args)
		So(err, ShouldBeNil)
		So(c, ShouldNotBeNil)
		_ = c.Close()
	})

	Convey("本地压根没有登记指纹 → 同样拒绝，不静默接受", t, func() {
		d := newFakeDaemon(t, "uuid-1", nil)
		dial := remote_device_svc.NewDaemonDial()

		_, err := dial.Connect(context.Background(), remote_device_svc.ConnectArgs{
			URL: d.url(), DeviceFingerprint: "sha256:desktop", DeviceToken: "device-token",
		})
		So(errors.Is(err, remote_device_svc.ErrTOFUMismatch), ShouldBeTrue)
	})
}
