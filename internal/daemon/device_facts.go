package daemon

import (
	"context"

	"github.com/agentre-hub/agentre/internal/daemon/devicefactsreport"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// newDeviceFactsReporter 组装这台 agentred 的设备情况:运行时按本机 PATH 探;账号引擎
// 来自引擎快照(未登录时为空);Hermes / OpenClaw 凭据看 state.json 里的槽位在不在;
// agentred 没有本机项目路径。登录了账号才向 server 上报,未登录时只供已配对桌面端
// 经 device.facts 直读。
func (d *Daemon) newDeviceFactsReporter() *devicefacts.Reporter {
	runtimes := d.opts.DeviceFactsRuntimes
	if runtimes == nil {
		runtimes = devicefacts.DefaultRuntimeProbe(nil).Probe
	}
	sources := devicefacts.Sources{
		Runtimes: runtimes,
		Engines: func(context.Context) ([]devicefacts.Engine, error) {
			return d.engineSnapshot.Engines(), nil
		},
		ResolveCommand: devicefacts.ResolveCommand,
		HasCredential: func(_ context.Context, e devicefacts.Engine) bool {
			slot := devicefacts.CredentialSlot(e)
			if slot == "" {
				return false
			}
			_, ok := d.state.BackendCredential(slot)
			return ok
		},
		SkillPacks: devicefacts.DiscoverSkillPacks,
	}
	uploader := devicefactsreport.Uploader{
		LoggedIn:    d.state.IsLoggedIn,
		ServerURL:   d.relayServerURL,
		AccessToken: d.currentAccessToken,
	}
	return devicefacts.NewReporter(
		func(ctx context.Context) (syncwire.DeviceFactsReport, error) {
			return devicefacts.Collect(ctx, sources)
		},
		uploader.Upload,
		devicefacts.WithUploadGate(d.state.IsLoggedIn),
	)
}
