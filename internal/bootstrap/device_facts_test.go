package bootstrap

import (
	"context"
	"testing"

	"github.com/agentre-hub/agentre/internal/service/device_facts_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// observingSync 截获「连上账号中继」的旁听登记与本机路径快照的取用。
type observingSync struct {
	sync_svc.SyncSvc
	listeners []func(context.Context)
}

func (o *observingSync) OnAccountChannelConnected(fn func(context.Context)) {
	o.listeners = append(o.listeners, fn)
}

func (o *observingSync) LocalPathSnapshot(context.Context) ([]syncwire.LocalPathItem, error) {
	return []syncwire.LocalPathItem{}, nil
}

type reportingServer struct {
	server_svc.ServerSvc
}

func (reportingServer) ReportDeviceFacts(context.Context, *syncwire.DeviceFactsReport) error {
	return nil
}

// Given 同步引擎与 server 出入口都已装配,When 装配设备情况,Then 默认服务就位,且它
// 登记成了「连上账号中继」的旁听者 —— 连上就整份上报这一条触发是接线接出来的,不接
// 就永远不会发生。
func TestInitDeviceFacts_GivenSyncAndServer_ThenServiceIsInstalledAndListensForRelayConnects(t *testing.T) {
	prev := device_facts_svc.Default()
	t.Cleanup(func() { device_facts_svc.SetDefault(prev) })
	syncSvc := &observingSync{}

	initDeviceFacts(reportingServer{}, syncSvc)

	if device_facts_svc.Default() == nil {
		t.Fatal("device facts service was not installed")
	}
	if len(syncSvc.listeners) != 1 {
		t.Fatalf("relay-connect listeners = %d, want 1", len(syncSvc.listeners))
	}
}
