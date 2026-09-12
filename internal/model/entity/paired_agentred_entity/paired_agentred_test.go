package paired_agentred_entity

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"

	"github.com/agentre-hub/agentre/internal/pkg/code"
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
		p := &PairedAgentred{Name: "devbox", DaemonFingerprint: "sha256:abc", TLSMode: "default"}
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

// D6：一行「来自账号的直连」只由 Origin=="account" 判定，与 IsRelayOnly 正交——
// 一台账号下发过地址的机器 url 非空、IsRelayOnly()==false，但它不是手动配对来的。
func TestPairedAgentred_IsAccountDirect(t *testing.T) {
	Convey("Origin==account is an account-direct row", t, func() {
		p := &PairedAgentred{Origin: "account", URL: "wss://h:7456/rpc"}
		So(p.IsAccountDirect(), ShouldBeTrue)
	})
	Convey("Origin==manual (today's LAN-paired shape) is not account-direct", t, func() {
		p := &PairedAgentred{Origin: "manual", URL: "ws://h/rpc"}
		So(p.IsAccountDirect(), ShouldBeFalse)
	})
	Convey("zero-value Origin (legacy rows built before this column existed) is not account-direct", t, func() {
		p := &PairedAgentred{URL: "ws://h/rpc"}
		So(p.IsAccountDirect(), ShouldBeFalse)
	})
	Convey("nil receiver is not account-direct", t, func() {
		var p *PairedAgentred
		So(p.IsAccountDirect(), ShouldBeFalse)
	})
}

// D6：DirectURLsJSON 装的是账号一次下发的全部地址；地址位（URL 字段）单独跟着
// 「最近一次直连成功」的语义走，不与这个列表混同。
func TestPairedAgentred_DirectURLs(t *testing.T) {
	Convey("round-trips through Set/Get", t, func() {
		p := &PairedAgentred{}
		p.SetDirectURLs([]string{"wss://a:7456/rpc", "wss://b:7456/rpc"})
		So(p.DirectURLs(), ShouldResemble, []string{"wss://a:7456/rpc", "wss://b:7456/rpc"})
	})
	Convey("empty JSON gives an empty (non-nil) slice", t, func() {
		p := &PairedAgentred{}
		So(p.DirectURLs(), ShouldResemble, []string{})
	})
	Convey("garbage JSON gives an empty (non-nil) slice instead of panicking", t, func() {
		p := &PairedAgentred{DirectURLsJSON: "not json"}
		So(p.DirectURLs(), ShouldResemble, []string{})
	})
	Convey("SetDirectURLs(nil) stores an empty array, not null", t, func() {
		p := &PairedAgentred{}
		p.SetDirectURLs(nil)
		So(p.DirectURLsJSON, ShouldEqual, "[]")
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
