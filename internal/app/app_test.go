package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/paths"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/update_svc"
)

func TestAppInfoReportsBuildChannel(t *testing.T) {
	for _, channel := range paths.AllChannels() {
		t.Run("Given a "+string(channel)+" build When Info is requested Then channel is "+string(channel), func(t *testing.T) {
			paths.SetBuildChannelForTest(t, string(channel))

			if got := NewApp(RuntimeModeInteractive).Info().Channel; got != channel {
				t.Fatalf("Channel = %q, want %q", got, channel)
			}
		})
	}
}

// updateBindingFake 记录绑定层交给更新服务的渠道。
type updateBindingFake struct {
	update_svc.Service

	checkCalls       int
	downloadChannels []paths.Channel
}

func (f *updateBindingFake) GetLastUpdateCheck(context.Context) (int64, error) { return 0, nil }
func (f *updateBindingFake) SetLastUpdateCheck(context.Context, int64) error   { return nil }
func (f *updateBindingFake) GetMirror(context.Context) (string, error)         { return "", nil }

func (f *updateBindingFake) CheckForUpdate(paths.Channel, string) (*update_svc.UpdateInfo, error) {
	f.checkCalls++
	return &update_svc.UpdateInfo{}, nil
}

func (f *updateBindingFake) DownloadAndUpdate(channel paths.Channel, _ string, _ func(int64, int64)) error {
	f.downloadChannels = append(f.downloadChannels, channel)
	return nil
}

func registerUpdateBindingFake(t *testing.T) *updateBindingFake {
	t.Helper()
	original := update_svc.Update()
	t.Cleanup(func() { update_svc.RegisterUpdate(original) })
	f := &updateBindingFake{}
	update_svc.RegisterUpdate(f)
	return f
}

func TestUpdateBindingsUseBuildChannel(t *testing.T) {
	t.Run("Given a nightly build When the user installs an update Then the nightly channel is downloaded", func(t *testing.T) {
		paths.SetBuildChannelForTest(t, string(paths.ChannelNightly))
		f := registerUpdateBindingFake(t)
		a := &App{ctx: context.Background()}

		if err := a.DownloadAndInstallUpdate(); err != nil {
			t.Fatalf("DownloadAndInstallUpdate() error = %v", err)
		}
		if len(f.downloadChannels) != 1 || f.downloadChannels[0] != paths.ChannelNightly {
			t.Fatalf("download channels = %v, want [nightly]", f.downloadChannels)
		}
	})

	t.Run("Given an invalid build channel When the user installs an update Then it fails without downloading", func(t *testing.T) {
		paths.SetBuildChannelForTest(t, "weekly")
		f := registerUpdateBindingFake(t)
		a := &App{ctx: context.Background()}

		if err := a.DownloadAndInstallUpdate(); err == nil {
			t.Fatal("DownloadAndInstallUpdate() error = nil, want invalid channel error")
		}
		if len(f.downloadChannels) != 0 {
			t.Fatalf("download channels = %v, want none", f.downloadChannels)
		}
	})

	t.Run("Given a Dev build When manual and focus checks are requested Then no check reaches the update service", func(t *testing.T) {
		paths.SetBuildChannelForTest(t, "")
		f := registerUpdateBindingFake(t)
		a := &App{ctx: context.Background()}

		manual, err := a.CheckForUpdate()
		if err != nil || manual != nil {
			t.Fatalf("CheckForUpdate() = (%v, %v), want (nil, nil)", manual, err)
		}
		focus, err := a.MaybeCheckForUpdate()
		if err != nil || focus != nil {
			t.Fatalf("MaybeCheckForUpdate() = (%v, %v), want (nil, nil)", focus, err)
		}
		if f.checkCalls != 0 {
			t.Fatalf("check calls = %d, want 0", f.checkCalls)
		}
	})
}

// onlinePoolSpy 是实现了 DeviceOnlineSink 的假 ConnPool,记录收到的在线/离线信号。
type onlinePoolSpy struct {
	remote_device_svc.ConnPool

	mu    sync.Mutex
	calls []string
}

func (p *onlinePoolSpy) SetDeviceOnline(deviceID int64, online bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, fmt.Sprintf("%d:%t", deviceID, online))
}

type onlinePoolSvcStub struct {
	remote_device_svc.RemoteDeviceSvc
	pool remote_device_svc.ConnPool
}

func (s *onlinePoolSvcStub) Pool() remote_device_svc.ConnPool { return s.pool }

// Given 设备监视报告某台设备上线又下线,when 信号到达 App,then 两条信号都原样转给连接池,
// 池子据此在线期间保温、离线后恢复空闲回收。
func TestOnRemoteDeviceOnline_ForwardsOnlineAndOfflineToThePool(t *testing.T) {
	spy := &onlinePoolSpy{}
	original := remote_device_svc.Default()
	t.Cleanup(func() { remote_device_svc.SetDefault(original) })
	remote_device_svc.SetDefault(&onlinePoolSvcStub{pool: spy})

	a := &App{}
	a.onRemoteDeviceOnline(7, true)
	a.onRemoteDeviceOnline(7, false)

	spy.mu.Lock()
	defer spy.mu.Unlock()
	if got, want := strings.Join(spy.calls, ","), "7:true,7:false"; got != want {
		t.Fatalf("pool online signals = %q, want %q", got, want)
	}
}
