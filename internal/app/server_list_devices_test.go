package app

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	repomock "github.com/agentre-hub/agentre/internal/repository/remote_device_repo/mock_remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	svcmock "github.com/agentre-hub/agentre/internal/service/remote_device_svc/mock_remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// countingServerSvc 只实现 ListDevices 并数它被调了几次；其余方法调到即 panic。
type countingServerSvc struct {
	server_svc.ServerSvc
	devices []server_svc.Device
	calls   int
}

func (s *countingServerSvc) ListDevices(context.Context) ([]server_svc.Device, error) {
	s.calls++
	return s.devices, nil
}

// Given 真的 remote_device_svc（收编走生产路径），when 前端刷新一次设备清单，then 账号设备
// 只向 server 拉一次：拿到的那份清单既交给前端、也交给收编，不为收编再拉第二次。
func TestAppServerListDevices_GivenRealAdoption_WhenCalled_ThenFetchesAccountDevicesOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := repomock.NewMockPairedAgentredRepo(ctrl)
	dial := svcmock.NewMockDaemonDialPort(ctrl)
	kc := keychain.NewMemory()
	_ = kc.Set("agentre-device-fingerprint", "sha256:this-desktop")
	svc := remote_device_svc.New(repo, dial, kc, remote_device_svc.NewConnPool(repo, kc, dial))
	repo.EXPECT().List(gomock.Any()).Return([]*paired_agentred_entity.PairedAgentred{
		{ID: 1, DaemonFingerprint: "sha256:devbox", Status: 1},
	}, nil).AnyTimes()
	repo.EXPECT().ListDeleted(gomock.Any()).Return(nil, nil).AnyTimes()

	server := &countingServerSvc{devices: []server_svc.Device{
		{Fingerprint: "sha256:devbox", Name: "devbox", Kind: "agentred"},
	}}
	prevServer, prevDevices := server_svc.Server(), remote_device_svc.Default()
	t.Cleanup(func() {
		server_svc.SetDefault(prevServer)
		remote_device_svc.SetDefault(prevDevices)
	})
	server_svc.SetDefault(server)
	remote_device_svc.SetDefault(svc)

	a := &App{}
	a.ctx = context.Background()
	got, err := a.ServerListDevices()
	if err != nil {
		t.Fatalf("ServerListDevices() error = %v", err)
	}
	if len(got) != 1 || got[0].Name != "devbox" {
		t.Fatalf("devices = %+v, want the server's list", got)
	}
	if server.calls != 1 {
		t.Fatalf("server ListDevices called %d times, want 1", server.calls)
	}
}
