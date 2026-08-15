package bootstrap

import (
	"context"
	"errors"
	"testing"

	"github.com/agentre-ai/agentre/internal/daemon/client"
	"github.com/agentre-ai/agentre/internal/service/remote_device_svc"
	watcher "github.com/agentre-ai/agentre/internal/service/remote_device_watcher_svc"
)

type recordingDial struct {
	called bool
	args   remote_device_svc.ConnectArgs
}

func (d *recordingDial) Open(_ context.Context, args remote_device_svc.ConnectArgs) (*client.Client, error) {
	d.called = true
	d.args = args
	return &client.Client{}, nil
}

func (d *recordingDial) OpenAccount(context.Context, remote_device_svc.AccountArgs) (*client.Client, error) {
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
	daemonFP  string
	peerFP    string
	returnErr error
}

func (r *recordingRelay) Open(_ context.Context, daemonFingerprint, peerFingerprint string) (*client.Client, error) {
	r.called = true
	r.daemonFP, r.peerFP = daemonFingerprint, peerFingerprint
	if r.returnErr != nil {
		return nil, r.returnErr
	}
	return &client.Client{}, nil
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
		ExpectedDaemonFingerprint: "sha256:coding",
	})

	if err != nil || c == nil {
		t.Fatalf("relay-only dial: got (%v, %v), want a client", c, err)
	}
	if dial.called {
		t.Fatal("must not attempt a direct dial for a row that has no LAN address")
	}
	if !relay.called || relay.daemonFP != "sha256:coding" || relay.peerFP != "sha256:this-desktop" {
		t.Fatalf("relay dial got (%q, %q), want (sha256:coding, sha256:this-desktop)", relay.daemonFP, relay.peerFP)
	}
}

func TestWatcherDialAdapter_GivenLANRow_ThenKeepsDialingDirect(t *testing.T) {
	dial := &recordingDial{}
	relay := &recordingRelay{}
	a := &dialAdapter{inner: dial, relay: relay}

	if _, err := a.Open(context.Background(), watcher.OpenArgs{
		URL: "ws://192.168.8.188:7456/rpc", TLSMode: "default",
		ExpectedDaemonFingerprint: "sha256:coding",
	}); err != nil {
		t.Fatalf("direct dial: %v", err)
	}
	if !dial.called || dial.args.URL != "ws://192.168.8.188:7456/rpc" {
		t.Fatalf("direct dial args = %+v", dial.args)
	}
	if relay.called {
		t.Fatal("a row with a LAN address keeps its existing direct health path")
	}
}

func TestWatcherDialAdapter_GivenRelayOnlyRowWithoutRelay_ThenFailsWithoutDialing(t *testing.T) {
	dial := &recordingDial{}
	a := &dialAdapter{inner: dial}

	if _, err := a.Open(context.Background(), watcher.OpenArgs{URL: "", ExpectedDaemonFingerprint: "sha256:coding"}); err == nil {
		t.Fatal("no LAN address and no relay must fail loudly")
	}
	if dial.called {
		t.Fatal("must not dial an empty URL")
	}
}
