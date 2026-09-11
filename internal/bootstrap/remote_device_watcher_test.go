package bootstrap

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	watcher "github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

type recordingDial struct {
	called bool
	args   remote_device_svc.ConnectArgs

	directCalled  bool
	directArgs    remote_device_svc.DirectArgs
	directAddress string
	directErr     error
}

func (d *recordingDial) OpenDirect(_ context.Context, args remote_device_svc.DirectArgs) (client.ProtobufConnection, string, error) {
	d.directCalled = true
	d.directArgs = args
	if d.directErr != nil {
		return nil, "", d.directErr
	}
	return &client.ProtobufClient{}, d.directAddress, nil
}

type spyDirectSuccess struct {
	deviceID int64
	address  string
	calls    int
}

func (s *spyDirectSuccess) RecordDirectSuccess(_ context.Context, deviceID int64, address string) error {
	s.calls++
	s.deviceID, s.address = deviceID, address
	return nil
}

func accountDirectOpenArgs(credential string) watcher.OpenArgs {
	return watcher.OpenArgs{
		DeviceID:                  7,
		URL:                       "wss://10.0.0.5:7456/rpc",
		TLSMode:                   "pin-cert",
		TLSCertPEM:                "pinned-cert-pem",
		DeviceFingerprint:         "sha256:this-desktop",
		ExpectedDaemonFingerprint: "sha256:devbox",
		AccountDirect:             true,
		DirectURLs:                []string{"wss://10.0.0.5:7456/rpc", "wss://192.168.1.9:7456/rpc"},
		DirectCredential:          credential,
	}
}

// D7 + D9:来自账号的直连行的探活与连接池同一条规则 —— auth.direct 覆盖全部地址,与中转
// 同时发起;直连赢了就把地址记成设备面板上的最近地址。
func TestWatcherDialAdapter_GivenAccountDirectRow_ThenRacesAuthDirectAgainstTheRelayAndRecordsTheWinningAddress(t *testing.T) {
	dial := &recordingDial{directAddress: "wss://192.168.1.9:7456/rpc"}
	relay := &recordingRelay{returnErr: errors.New("server unreachable")}
	recorder := &spyDirectSuccess{}
	a := &dialAdapter{inner: dial, relay: relay, recorder: recorder}

	c, err := a.Open(context.Background(), accountDirectOpenArgs("direct-cred"))

	if err != nil || c == nil {
		t.Fatalf("account-direct dial: got (%v, %v), want a client", c, err)
	}
	if dial.called {
		t.Fatal("the direct credential must never be presented as a pairing token (auth.connect)")
	}
	want := remote_device_svc.DirectArgs{
		URLs:                      []string{"wss://10.0.0.5:7456/rpc", "wss://192.168.1.9:7456/rpc"},
		CertPEM:                   "pinned-cert-pem",
		Credential:                "direct-cred",
		ExpectedDaemonFingerprint: "sha256:devbox",
	}
	if !dial.directCalled || !reflect.DeepEqual(dial.directArgs, want) {
		t.Fatalf("OpenDirect args = %+v, want %+v", dial.directArgs, want)
	}
	if !relay.called {
		t.Fatal("the relay must be raced alongside the direct path")
	}
	if recorder.calls != 1 || recorder.deviceID != 7 || recorder.address != "wss://192.168.1.9:7456/rpc" {
		t.Fatalf("RecordDirectSuccess = (%d calls, %d, %q), want one call (7, wss://192.168.1.9:7456/rpc)", recorder.calls, recorder.deviceID, recorder.address)
	}
}

// D17:直连路由不到、中转可用 —— 中转胜出,探活成功(设备不显示异常),不改最近地址。
func TestWatcherDialAdapter_GivenAccountDirectRowWhoseDirectFails_ThenTheRelayWinsAndNoAddressIsRecorded(t *testing.T) {
	dial := &recordingDial{directErr: errors.New("no route to host")}
	relay := &recordingRelay{}
	recorder := &spyDirectSuccess{}
	a := &dialAdapter{inner: dial, relay: relay, recorder: recorder}

	c, err := a.Open(context.Background(), accountDirectOpenArgs("direct-cred"))

	if err != nil || c == nil {
		t.Fatalf("relay should win: got (%v, %v)", c, err)
	}
	if recorder.calls != 0 {
		t.Fatalf("no direct win, yet RecordDirectSuccess was called %d times", recorder.calls)
	}
}

// 钥匙串槽为空:没有直连凭据可发,只经中转探活(中转的 auth.account 会重新下发)。
func TestWatcherDialAdapter_GivenAccountDirectRowWithoutCredential_ThenProbesOverTheRelayOnly(t *testing.T) {
	dial := &recordingDial{}
	relay := &recordingRelay{}
	a := &dialAdapter{inner: dial, relay: relay, recorder: &spyDirectSuccess{}}

	if _, err := a.Open(context.Background(), accountDirectOpenArgs("")); err != nil {
		t.Fatalf("relay probe: %v", err)
	}
	if dial.directCalled || dial.called {
		t.Fatal("no direct credential: must not dial direct at all")
	}
	if !relay.called || relay.daemonFP != "sha256:devbox" || relay.peerFP != "sha256:this-desktop" {
		t.Fatalf("relay dial got (%q, %q), want (sha256:devbox, sha256:this-desktop)", relay.daemonFP, relay.peerFP)
	}
}

func (d *recordingDial) Open(_ context.Context, args remote_device_svc.ConnectArgs) (client.ProtobufConnection, error) {
	d.called = true
	d.args = args
	return &client.ProtobufClient{}, nil
}

func (d *recordingDial) OpenAccount(context.Context, remote_device_svc.AccountArgs) (client.ProtobufConnection, error) {
	return nil, errors.New("not used")
}

func (d *recordingDial) Pair(context.Context, remote_device_svc.PairArgs) (remote_device_svc.PairResult, error) {
	return remote_device_svc.PairResult{}, errors.New("not used")
}

func (d *recordingDial) Connect(context.Context, remote_device_svc.ConnectArgs) (remote_device_svc.ConnectResult, error) {
	return remote_device_svc.ConnectResult{}, errors.New("not used")
}

type recordingRelay struct {
	called    bool
	daemonFP  devicefp.Carrier
	peerFP    devicefp.Initiator
	returnErr error
}

func (r *recordingRelay) Open(_ context.Context, daemonFingerprint devicefp.Carrier, peerFingerprint devicefp.Initiator) (client.ProtobufConnection, error) {
	r.called = true
	r.daemonFP, r.peerFP = daemonFingerprint, peerFingerprint
	if r.returnErr != nil {
		return nil, r.returnErr
	}
	return &client.ProtobufClient{}, nil
}

// watcher 是长连状态机，它更新的 last_seen_at 决定 DeviceView.online，而「运行设备」
// 下拉按 online 禁用选项。收编来的行没有 LAN 地址，照直连拨只会拿到
// 「malformed ws or wss URL」，于是那台机器在下拉里永远是灰的 —— 收编等于白做。
func TestWatcherDialAdapter_GivenRelayOnlyRow_ThenDialsOverTheRelay(t *testing.T) {
	dial := &recordingDial{}
	relay := &recordingRelay{}
	a := &dialAdapter{inner: dial, relay: relay}

	c, err := a.Open(context.Background(), watcher.OpenArgs{
		URL:                       "",
		DeviceFingerprint:         "sha256:this-desktop",
		ExpectedDaemonFingerprint: "sha256:devbox",
	})

	if err != nil || c == nil {
		t.Fatalf("relay-only dial: got (%v, %v), want a client", c, err)
	}
	if dial.called {
		t.Fatal("must not attempt a direct dial for a row that has no LAN address")
	}
	if !relay.called || relay.daemonFP != "sha256:devbox" || relay.peerFP != "sha256:this-desktop" {
		t.Fatalf("relay dial got (%q, %q), want (sha256:devbox, sha256:this-desktop)", relay.daemonFP, relay.peerFP)
	}
}

func TestWatcherDialAdapter_GivenLANRow_ThenKeepsDialingDirect(t *testing.T) {
	dial := &recordingDial{}
	relay := &recordingRelay{}
	a := &dialAdapter{inner: dial, relay: relay}

	if _, err := a.Open(context.Background(), watcher.OpenArgs{
		URL: "ws://192.168.1.100:7456/rpc", TLSMode: "default",
		ExpectedDaemonFingerprint: "sha256:devbox",
	}); err != nil {
		t.Fatalf("direct dial: %v", err)
	}
	if !dial.called || dial.args.URL != "ws://192.168.1.100:7456/rpc" {
		t.Fatalf("direct dial args = %+v", dial.args)
	}
	if relay.called {
		t.Fatal("a row with a LAN address keeps its existing direct health path")
	}
}

func TestWatcherDialAdapter_GivenRelayOnlyRowWithoutRelay_ThenFailsWithoutDialing(t *testing.T) {
	dial := &recordingDial{}
	a := &dialAdapter{inner: dial}

	if _, err := a.Open(context.Background(), watcher.OpenArgs{URL: "", ExpectedDaemonFingerprint: "sha256:devbox"}); err == nil {
		t.Fatal("no LAN address and no relay must fail loudly")
	}
	if dial.called {
		t.Fatal("must not dial an empty URL")
	}
}
