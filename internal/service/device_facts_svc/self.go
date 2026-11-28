package device_facts_svc

import (
	"context"
	"errors"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/devicefacts"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// SelfDeps 是本机情况要从别的域取的东西,由 bootstrap 装配。
type SelfDeps struct {
	// Fingerprint 本机设备指纹(CLI 路径覆盖按它取)。
	Fingerprint func() (devicefp.Carrier, error)
	// LocalPaths 本机项目路径快照(sync_svc.LocalPathSnapshotter);nil = 不报。
	LocalPaths func(ctx context.Context) ([]syncwire.LocalPathItem, error)
	// Upload 把情况报给账号 server(server_svc.DeviceFactsReporter);nil = 不报。
	Upload func(ctx context.Context, report *syncwire.DeviceFactsReport) error
	// CanUpload 此刻登录了账号、报得了;nil = 总是报得了。
	CanUpload func() bool
}

// NewSelfReporter 组装这台桌面端的设备情况:运行时按本机 PATH 探(本机的 CLI 路径覆盖
// 优先);账号引擎来自引擎配置表;Hermes / OpenClaw 凭据看本机钥匙串里的槽位在不在;
// 技能包用本机的发现器;本机项目路径与本机路径上报同一份快照。
func NewSelfReporter(deps SelfDeps) *devicefacts.Reporter {
	sources := devicefacts.Sources{
		Runtimes: func(ctx context.Context) []syncwire.RuntimeFact {
			var overrides map[string]string
			if deps.Fingerprint != nil {
				if fp, err := deps.Fingerprint(); err == nil {
					overrides = selfCLIOverrides(ctx, fp)
				}
			}
			return devicefacts.DefaultRuntimeProbe(func(runtime string) string { return overrides[runtime] }).Probe(ctx)
		},
		Engines:        selfEngines,
		ResolveCommand: devicefacts.ResolveCommand,
		HasCredential:  hasLocalCredential,
		// 与 agent_backend_svc 的连接测试记在同一份进程级记录里。
		LastConnection: devicefacts.DefaultConnections().Last,
		SkillPacks:     devicefacts.DiscoverSkillPacks,
		LocalPaths:     deps.LocalPaths,
	}
	return devicefacts.NewReporter(
		func(ctx context.Context) (syncwire.DeviceFactsReport, error) {
			return devicefacts.Collect(ctx, sources)
		},
		deps.Upload,
		devicefacts.WithUploadGate(deps.CanUpload),
	)
}

// selfEngines 账号下的活引擎配置。还没有同步标识的配置在别的设备与 server 上无从对应,
// 不报。
func selfEngines(ctx context.Context) ([]devicefacts.Engine, error) {
	rows, err := agent_backend_repo.EngineProfile().List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]devicefacts.Engine, 0, len(rows))
	for _, p := range rows {
		if p == nil || p.SyncID == "" {
			continue
		}
		out = append(out, devicefacts.Engine{
			SyncID: p.SyncID, Runtime: p.Type, ACPCommand: p.ACPCommand,
			HermesURL: p.HermesURL, OpenClawGatewayURL: p.OpenClawGatewayURL,
		})
	}
	return out, nil
}

// selfCLIOverrides 这台设备上按运行时类型的 CLI 路径覆盖。读不出时按没有覆盖走 PATH:
// 覆盖只改「去哪找」,探测本身照常。
func selfCLIOverrides(ctx context.Context, self devicefp.Carrier) map[string]string {
	rows, err := device_pref_repo.CLIPath().List(ctx)
	if err != nil {
		logger.Ctx(ctx).Warn("device_facts_svc.selfCLIOverrides: list cli path overrides failed, probing PATH", zap.Error(err))
		return map[string]string{}
	}
	out := map[string]string{}
	for _, r := range rows {
		if r != nil && r.DeviceFingerprint == self && r.CLIPath != "" {
			out[r.RuntimeType] = r.CLIPath
		}
	}
	return out
}

// hasLocalCredential 本机钥匙串里有没有这份服务型引擎配置的凭据。只看在不在,值当场丢掉。
func hasLocalCredential(ctx context.Context, e devicefacts.Engine) bool {
	slot := devicefacts.CredentialSlot(e)
	store := keychain.Default()
	if slot == "" || store == nil {
		return false
	}
	_, err := store.Get(slot)
	if err != nil && !errors.Is(err, keychain.ErrNotFound) {
		logger.Ctx(ctx).Warn("device_facts_svc.hasLocalCredential: keychain read failed",
			zap.String("engineSyncId", e.SyncID), zap.Error(err))
	}
	return err == nil
}
