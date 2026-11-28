package bootstrap

import (
	"context"

	"github.com/cago-frame/cago/pkg/logger"

	"github.com/agentre-hub/agentre/internal/service/device_facts_svc"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
	"github.com/agentre-hub/agentre/internal/service/sync_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// initDeviceFacts 装配设备情况(spec 2026-11-25「设备情况上报」):本机情况经 server_svc
// 上报、带上 sync_svc 的本机路径快照,并挂到「连上账号中继」上 —— 每一次(重)连上就
// 整份报一次。两个窄口都是可选的:出入口不认识它们(测试替身)时只是不报。
func initDeviceFacts(server server_svc.ServerSvc, syncSvc sync_svc.SyncSvc) {
	deps := device_facts_svc.SelfDeps{
		Fingerprint: func() (devicefp.Carrier, error) { return remote_device_svc.Default().DeviceFingerprint() },
		CanUpload:   func() bool { return server.AccessToken() != "" },
	}
	if reporter, ok := server.(server_svc.DeviceFactsReporter); ok {
		deps.Upload = reporter.ReportDeviceFacts
	}
	if snapshotter, ok := syncSvc.(sync_svc.LocalPathSnapshotter); ok {
		deps.LocalPaths = snapshotter.LocalPathSnapshot
	}
	facts := device_facts_svc.New(device_facts_svc.WithSelf(device_facts_svc.NewSelfReporter(deps)))
	device_facts_svc.SetDefault(facts)
	if observer, ok := syncSvc.(sync_svc.AccountChannelObserver); ok {
		observer.OnAccountChannelConnected(facts.AccountChannelConnected)
	}
}

// DeviceFactsBoot 起本机设备情况的周期变化检查(变了才报)。与 SyncBoot 一样由
// app.go.startup 调用。
func DeviceFactsBoot(ctx context.Context) {
	if s := device_facts_svc.Default(); s != nil {
		logger.Ctx(ctx).Debug("bootstrap.DeviceFactsBoot: starting device facts change checks")
		s.Start(ctx)
	}
}
