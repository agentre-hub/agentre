package paired_agentred_entity

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-ai/agentre/internal/pkg/code"
)

func TestPairedAgentred_Check(t *testing.T) {
	Convey("nil receiver returns RemoteDeviceNotFound", t, func() {
		var p *PairedAgentred
		err := p.Check(context.Background())
		So(err, ShouldNotBeNil)
		So(err.Error(), ShouldContainSubstring, "Remote device not found")
		_ = code.RemoteDeviceNotFound // ensure const exists
	})
	Convey("empty name returns InvalidParameter", t, func() {
		p := &PairedAgentred{Name: "  ", URL: "ws://h/rpc", DaemonFingerprint: "fp", TLSMode: "default"}
		So(p.Check(context.Background()), ShouldNotBeNil)
	})
	Convey("non-ws URL returns RemoteDeviceURLInvalid", t, func() {
		p := &PairedAgentred{Name: "x", URL: "http://h/rpc", DaemonFingerprint: "fp", TLSMode: "default"}
		So(p.Check(context.Background()), ShouldNotBeNil)
	})
	Convey("unknown tls_mode returns RemoteDeviceTLSConfigInvalid", t, func() {
		p := &PairedAgentred{Name: "x", URL: "ws://h/rpc", DaemonFingerprint: "fp", TLSMode: "bogus"}
		So(p.Check(context.Background()), ShouldNotBeNil)
	})
	Convey("pin-cert without PEM returns RemoteDeviceTLSConfigInvalid", t, func() {
		p := &PairedAgentred{Name: "x", URL: "ws://h/rpc", DaemonFingerprint: "fp", TLSMode: "pin-cert"}
		So(p.Check(context.Background()), ShouldNotBeNil)
	})
	Convey("ca-bundle without PEM returns RemoteDeviceTLSConfigInvalid", t, func() {
		p := &PairedAgentred{Name: "x", URL: "ws://h/rpc", DaemonFingerprint: "fp", TLSMode: "ca-bundle"}
		So(p.Check(context.Background()), ShouldNotBeNil)
	})
	Convey("default with PEM returns RemoteDeviceTLSConfigInvalid (dirty data)", t, func() {
		p := &PairedAgentred{Name: "x", URL: "ws://h/rpc", DaemonFingerprint: "fp", TLSMode: "default", TLSCertPEM: "X"}
		So(p.Check(context.Background()), ShouldNotBeNil)
	})
	Convey("skip-verify with PEM returns RemoteDeviceTLSConfigInvalid (dirty data)", t, func() {
		p := &PairedAgentred{Name: "x", URL: "ws://h/rpc", DaemonFingerprint: "fp", TLSMode: "skip-verify", TLSCertPEM: "X"}
		So(p.Check(context.Background()), ShouldNotBeNil)
	})
	Convey("empty daemon_fingerprint returns InvalidParameter", t, func() {
		p := &PairedAgentred{Name: "x", URL: "ws://h/rpc", TLSMode: "default"}
		So(p.Check(context.Background()), ShouldNotBeNil)
	})
	Convey("happy path: default mode, all fields valid", t, func() {
		p := &PairedAgentred{Name: "linux-srv", URL: "ws://192.168.1.1:7456/rpc", DaemonFingerprint: "sha256:abc", TLSMode: "default"}
		So(p.Check(context.Background()), ShouldBeNil)
	})
	Convey("happy path: pin-cert with PEM", t, func() {
		p := &PairedAgentred{Name: "x", URL: "wss://h/rpc", DaemonFingerprint: "fp", TLSMode: "pin-cert", TLSCertPEM: "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----"}
		So(p.Check(context.Background()), ShouldBeNil)
	})
	// 账号来源收编的行：这台机器在账号里，但本机从没 LAN 配对过它，所以没有 LAN
	// 地址可填。空 URL 就是「只有中转路径」的标记——中转按指纹寻址，不需要地址。
	Convey("relay-only row: empty URL is valid when it is the marker for having no LAN path", t, func() {
		p := &PairedAgentred{Name: "coding", DaemonFingerprint: "sha256:abc", TLSMode: "default"}
		So(p.Check(context.Background()), ShouldBeNil)
		So(p.IsRelayOnly(), ShouldBeTrue)
	})
	Convey("a LAN row is not relay-only", t, func() {
		p := &PairedAgentred{Name: "x", URL: "ws://h/rpc", DaemonFingerprint: "fp", TLSMode: "default"}
		So(p.IsRelayOnly(), ShouldBeFalse)
	})
	// 中转按指纹寻址，没有指纹就既连不上也认不出——比缺地址更致命。
	Convey("relay-only row without a fingerprint is still invalid", t, func() {
		p := &PairedAgentred{Name: "x", TLSMode: "default"}
		So(p.Check(context.Background()), ShouldNotBeNil)
	})
}

func TestPairedAgentred_IsOnline(t *testing.T) {
	Convey("IsOnline true when last_seen within 5 min", t, func() {
		p := &PairedAgentred{LastSeenAt: 1_000_000}
		So(p.IsOnline(1_000_000+4*60*1000), ShouldBeTrue)
	})
	Convey("IsOnline false when last_seen older than 5 min", t, func() {
		p := &PairedAgentred{LastSeenAt: 1_000_000}
		So(p.IsOnline(1_000_000+6*60*1000), ShouldBeFalse)
	})
	Convey("IsOnline false when last_seen is zero", t, func() {
		p := &PairedAgentred{LastSeenAt: 0}
		So(p.IsOnline(1_000_000), ShouldBeFalse)
	})
}

func TestPairedAgentred_IsActive(t *testing.T) {
	Convey("IsActive true when status == ACTIVE (1)", t, func() {
		So((&PairedAgentred{Status: 1}).IsActive(), ShouldBeTrue)
	})
	Convey("IsActive false when status != ACTIVE", t, func() {
		So((&PairedAgentred{Status: 2}).IsActive(), ShouldBeFalse)
	})
	Convey("nil receiver: IsActive false", t, func() {
		var p *PairedAgentred
		So(p.IsActive(), ShouldBeFalse)
	})
}
