package remote_device_svc_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

func (d *fakeDaemon) wssURL() string { return "wss" + strings.TrimPrefix(d.srv.URL, "https") + "/rpc" }

// pinnedPEM 是这台假 agentred 实际出示的证书 —— 账号下发、设备行固定的那一张。
func (d *fakeDaemon) pinnedPEM() string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d.srv.Certificate().Raw}))
}

// strangerCert 是另一张自签证书:同一地址上换了证书的 agentred(D16)、或者 IP 变了之后
// 占着这个地址的别的机器。
func strangerCert(t *testing.T) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// silentAddress 接受 TCP 连接但永远不回 TLS 握手 —— 一个路由得到、却不应答的地址。
func silentAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			_ = c.Close()
		}
	})
	return "wss://" + ln.Addr().String() + "/rpc"
}

// closedAddress 是一个没人监听的地址(连接被拒)。
func closedAddress(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return "wss://" + addr + "/rpc"
}

func directArgs(pinnedPEM string, urls ...string) remote_device_svc.DirectArgs {
	return remote_device_svc.DirectArgs{
		URLs:                      urls,
		CertPEM:                   pinnedPEM,
		Credential:                "direct-cred",
		ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1"),
	}
}

func isTOFUAlarm(err error) bool {
	return errors.Is(err, remote_device_svc.ErrTOFUMismatch) || strings.Contains(err.Error(), "tofu mismatch")
}

// D7:证书与固定值一致之后,本地直连凭据经一次 auth.direct 发出;交回赢下的地址。
func TestRealDial_OpenDirect_GivenThePinnedCertMatches_ThenPresentsTheCredentialOnceOverAuthDirect(t *testing.T) {
	Convey("pinned cert matches: one auth.direct carrying the credential, winning address returned", t, func() {
		d := newTLSFakeDaemon(t, "uuid-1", nil, nil)

		c, address, err := remote_device_svc.NewDaemonDial().OpenDirect(context.Background(), directArgs(d.pinnedPEM(), d.wssURL()))
		So(err, ShouldBeNil)
		So(c, ShouldNotBeNil)
		defer func() { _ = c.Close() }()
		So(address, ShouldEqual, d.wssURL())

		frames := d.received()
		So(len(frames), ShouldEqual, 1)
		So(frames[0].GetMethodId(), ShouldEqual, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_DIRECT))
		var p agentrewire.AuthDirectRequest
		So(proto.Unmarshal(frames[0].GetEncodedPayload(), &p), ShouldBeNil)
		So(p.GetCredential(), ShouldEqual, "direct-cred")
		So(p.GetProtocolVersion(), ShouldEqual, wireversion.Protocol)
		So(c.SelfFingerprint(), ShouldEqual, "sha256:as-the-daemon-sees-me")
	})
}

// D16:地址上的证书与固定值不一致 —— TLS 就地失败,凭据一个字节都不发出去,也不是 TOFU 告警。
func TestRealDial_OpenDirect_GivenTheCertDiffersFromThePin_ThenFailsWithoutSendingTheCredentialOrRaisingTOFU(t *testing.T) {
	Convey("cert differs from the pin: connection failure, no RPC reaches the server, no TOFU", t, func() {
		d := newTLSFakeDaemon(t, "uuid-1", nil, strangerCert(t))
		pinnedElsewhere := newTLSFakeDaemon(t, "uuid-1", nil, nil).pinnedPEM()

		c, address, err := remote_device_svc.NewDaemonDial().OpenDirect(context.Background(), directArgs(pinnedElsewhere, d.wssURL()))
		So(c, ShouldBeNil)
		So(address, ShouldEqual, "")
		So(err, ShouldNotBeNil)
		So(d.received(), ShouldBeEmpty)
		So(isTOFUAlarm(err), ShouldBeFalse)
		So(errors.Is(err, remote_device_svc.ErrUnauthorized), ShouldBeFalse)
	})
}

// D7 + D16:一个地址证书不符、另一个一致 —— 一致的那个赢,不符的那个依旧没收到任何东西。
func TestRealDial_OpenDirect_GivenOneAddressPinsWrongAndAnotherMatches_ThenTheMatchingAddressWins(t *testing.T) {
	Convey("mixed addresses: the pinned one wins, the other never receives the credential", t, func() {
		wrong := newTLSFakeDaemon(t, "uuid-1", nil, strangerCert(t))
		right := newTLSFakeDaemon(t, "uuid-1", nil, nil)

		c, address, err := remote_device_svc.NewDaemonDial().OpenDirect(context.Background(), directArgs(right.pinnedPEM(), wrong.wssURL(), right.wssURL()))
		So(err, ShouldBeNil)
		defer func() { _ = c.Close() }()
		So(address, ShouldEqual, right.wssURL())
		So(wrong.received(), ShouldBeEmpty)
		So(len(right.received()), ShouldEqual, 1)
	})
}

// D7「对保存的全部地址并发尝试」:排在前面的地址路由得到却不应答,后面的地址照样马上连上
// —— 逐个尝试的实现会卡到前一个超时为止。
func TestRealDial_OpenDirect_GivenAnAddressThatNeverAnswers_ThenAnotherAddressWinsWithoutWaitingForIt(t *testing.T) {
	Convey("a silent first address does not hold back a reachable second one", t, func() {
		right := newTLSFakeDaemon(t, "uuid-1", nil, nil)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		start := time.Now()
		c, address, err := remote_device_svc.NewDaemonDial().OpenDirect(ctx, directArgs(right.pinnedPEM(), silentAddress(t), right.wssURL()))
		So(err, ShouldBeNil)
		defer func() { _ = c.Close() }()
		So(address, ShouldEqual, right.wssURL())
		So(time.Since(start), ShouldBeLessThan, 3*time.Second)
	})
}

// D17/D18:所有地址都失败 —— 一次普通的连接失败,各地址原因都在,不是未授权也不是 TOFU。
func TestRealDial_OpenDirect_GivenEveryAddressFails_ThenFailsAsAConnectionFailure(t *testing.T) {
	Convey("all addresses fail: plain connection failure naming each address", t, func() {
		wrong := newTLSFakeDaemon(t, "uuid-1", nil, strangerCert(t))
		pin := newTLSFakeDaemon(t, "uuid-1", nil, nil).pinnedPEM()
		closed := closedAddress(t)

		c, address, err := remote_device_svc.NewDaemonDial().OpenDirect(context.Background(), directArgs(pin, wrong.wssURL(), closed))
		So(c, ShouldBeNil)
		So(address, ShouldEqual, "")
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, wrong.wssURL())
		So(err.Error(), ShouldContainSubstring, closed)
		So(wrong.received(), ShouldBeEmpty)
		So(isTOFUAlarm(err), ShouldBeFalse)
		So(errors.Is(err, remote_device_svc.ErrUnauthorized), ShouldBeFalse)
	})

	Convey("no address at all: fails without dialing", t, func() {
		pin := newTLSFakeDaemon(t, "uuid-1", nil, nil).pinnedPEM()
		c, _, err := remote_device_svc.NewDaemonDial().OpenDirect(context.Background(), directArgs(pin))
		So(c, ShouldBeNil)
		So(err, ShouldNotBeNil)
	})
}

// 「每个地址都先完成 TLS」:一个 ws:// 地址没有 TLS 可校验,凭据绝不能明文发给它。
func TestRealDial_OpenDirect_GivenAPlainWsAddress_ThenNeverSendsTheCredential(t *testing.T) {
	Convey("ws:// address: refused before any RPC", t, func() {
		plain := newFakeDaemon(t, "uuid-1", nil)
		pin := newTLSFakeDaemon(t, "uuid-1", nil, nil).pinnedPEM()

		c, _, err := remote_device_svc.NewDaemonDial().OpenDirect(context.Background(), directArgs(pin, plain.url()))
		So(c, ShouldBeNil)
		So(err, ShouldNotBeNil)
		So(plain.received(), ShouldBeEmpty)
	})
}

// H3(直连一侧):agentred 答 -32007 —— 不是「凭据被拒」,不能折成 ErrUnauthorized,
// 原码保留给调用方判「可重试」。
func TestRealDial_GivenTheDaemonCannotReachTheAccountServer_ThenTheFailureIsNotUnauthorized(t *testing.T) {
	unreachable := &rpcerror.Error{Code: rpcerror.CodeAccountServerUnreachable, Message: "Account server unreachable"}

	Convey("auth.direct answers -32007", t, func() {
		d := newTLSFakeDaemon(t, "uuid-1", unreachable, nil)
		c, _, err := remote_device_svc.NewDaemonDial().OpenDirect(context.Background(), directArgs(d.pinnedPEM(), d.wssURL()))
		So(c, ShouldBeNil)
		So(errors.Is(err, remote_device_svc.ErrUnauthorized), ShouldBeFalse)
		var rpcErr *rpcerror.Error
		So(errors.As(err, &rpcErr), ShouldBeTrue)
		So(rpcErr.Code, ShouldEqual, rpcerror.CodeAccountServerUnreachable)
	})

	Convey("auth.account answers -32007", t, func() {
		d := newFakeDaemon(t, "uuid-1", unreachable)
		c, err := remote_device_svc.NewDaemonDial().OpenAccount(context.Background(), remote_device_svc.AccountArgs{
			URL: d.url(), TLSMode: "default", Credential: fakeAccountJWT,
			ExpectedDaemonFingerprint: identity.DaemonFingerprint("uuid-1"),
		})
		So(c, ShouldBeNil)
		So(errors.Is(err, remote_device_svc.ErrUnauthorized), ShouldBeFalse)
		var rpcErr *rpcerror.Error
		So(errors.As(err, &rpcErr), ShouldBeTrue)
		So(rpcErr.Code, ShouldEqual, rpcerror.CodeAccountServerUnreachable)
	})
}

// auth.direct 拒绝凭据(-32001)与 auth.account 同一个分类:ErrUnauthorized。
func TestRealDial_OpenDirect_GivenTheDaemonRejectsTheCredential_ThenMapsToUnauthorized(t *testing.T) {
	Convey("auth.direct answers -32001 → ErrUnauthorized", t, func() {
		d := newTLSFakeDaemon(t, "uuid-1", &rpcerror.Error{Code: rpcerror.CodeUnauthorized, Message: "direct credential unknown"}, nil)
		c, _, err := remote_device_svc.NewDaemonDial().OpenDirect(context.Background(), directArgs(d.pinnedPEM(), d.wssURL()))
		So(c, ShouldBeNil)
		So(errors.Is(err, remote_device_svc.ErrUnauthorized), ShouldBeTrue)
	})
}

// 与今天的 TOFU 复核同一条:证书对得上、应答的实例却不是本地登记的那台 → ErrTOFUMismatch。
func TestRealDial_OpenDirect_GivenTheAnsweringDaemonIsNotThePinnedOne_ThenTOFUMismatch(t *testing.T) {
	Convey("auth.direct answered by another instance → ErrTOFUMismatch", t, func() {
		d := newTLSFakeDaemon(t, "uuid-other", nil, nil)
		c, _, err := remote_device_svc.NewDaemonDial().OpenDirect(context.Background(), directArgs(d.pinnedPEM(), d.wssURL()))
		So(c, ShouldBeNil)
		So(errors.Is(err, remote_device_svc.ErrTOFUMismatch), ShouldBeTrue)
	})
}
