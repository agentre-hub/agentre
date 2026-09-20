package app

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
)

// upgradeSvcStub 只关心 Upgrade 收到的参数;其余方法靠内嵌接口满足签名(用不到)。
type upgradeSvcStub struct {
	remote_device_svc.RemoteDeviceSvc

	gotDeviceID int64
	gotForce    bool
	result      *remote_device_svc.UpgradeResult
	err         error
}

func (s *upgradeSvcStub) Upgrade(_ context.Context, deviceID int64, force bool) (*remote_device_svc.UpgradeResult, error) {
	s.gotDeviceID = deviceID
	s.gotForce = force
	return s.result, s.err
}

// Given the binding no longer accepts a channel argument (task 5: the desktop's
// own build channel is the only channel source, resolved inside remote_device_svc),
// when App.RemoteDeviceUpgrade is called, then it passes deviceID/force through
// unchanged and returns the service's result verbatim — a thin passthrough with
// no channel parameter left to forward.
func TestAppRemoteDeviceUpgrade_GivenWiredService_WhenCalled_ThenPassesThroughWithoutChannel(t *testing.T) {
	stub := &upgradeSvcStub{result: &remote_device_svc.UpgradeResult{Accepted: true, TargetVersion: "0.6.0"}}
	original := remote_device_svc.Default()
	t.Cleanup(func() { remote_device_svc.SetDefault(original) })
	remote_device_svc.SetDefault(stub)

	a := &App{}
	a.ctx = context.Background()

	got, err := a.RemoteDeviceUpgrade(42, true)
	if err != nil {
		t.Fatalf("RemoteDeviceUpgrade() error = %v, want nil", err)
	}
	if stub.gotDeviceID != 42 {
		t.Fatalf("deviceID passed to service = %d, want 42", stub.gotDeviceID)
	}
	if !stub.gotForce {
		t.Fatalf("force passed to service = false, want true")
	}
	if got != stub.result {
		t.Fatalf("result = %v, want the service's result returned verbatim", got)
	}
}

// Given no remote_device_svc is wired, when App.RemoteDeviceUpgrade is called,
// then it fails closed with the desktop-not-running sentinel instead of a nil-
// pointer panic.
func TestAppRemoteDeviceUpgrade_GivenNoService_WhenCalled_ThenFailsClosed(t *testing.T) {
	original := remote_device_svc.Default()
	t.Cleanup(func() { remote_device_svc.SetDefault(original) })
	remote_device_svc.SetDefault(nil)

	a := &App{}
	a.ctx = context.Background()

	got, err := a.RemoteDeviceUpgrade(42, false)
	if got != nil {
		t.Fatalf("result = %v, want nil", got)
	}
	if err != errRemoteDeviceServiceUnavailable {
		t.Fatalf("err = %v, want errRemoteDeviceServiceUnavailable", err)
	}
}
