package bootstrap

import (
	"context"
	"fmt"
	"strings"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/remote_device_repo"
	"github.com/agentre-hub/agentre/internal/service/remote_device_svc"
	watcher "github.com/agentre-hub/agentre/internal/service/remote_device_watcher_svc"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// dialAdapter 把 remote_device_svc.DaemonDialPort 的 Open 桥到 watcher port，
// 并在这里选路——watcher 自己不必知道中转的存在。
type dialAdapter struct {
	inner remote_device_svc.DaemonDialPort
	// relay 可空(未登录 / 单机构建):此时只有 LAN 一条路。
	relay remote_device_svc.RelayDialPort
}

func (a *dialAdapter) Open(ctx context.Context, args watcher.OpenArgs) (client.ProtobufConnection, error) {
	// URL 为空 = 账号来源收编的行(paired_agentred_entity.IsRelayOnly),没有 LAN 路径。
	// 与 ConnPool.openAny 同一条判定:拿空地址去拨只会换来一句
	// 「malformed ws or wss URL」,而 watcher 更新的 last_seen_at 决定 DeviceView.online,
	// 「运行设备」下拉又按 online 禁用选项 —— 收编来的机器会永远是灰的。
	if strings.TrimSpace(args.URL) == "" {
		if a.relay == nil {
			return nil, fmt.Errorf("device %s has no LAN address and no relay is configured",
				args.ExpectedDaemonFingerprint)
		}
		return a.relay.Open(ctx, args.ExpectedDaemonFingerprint, args.DeviceFingerprint)
	}
	return a.inner.Open(ctx, remote_device_svc.ConnectArgs{
		URL:                       args.URL,
		TLSMode:                   args.TLSMode,
		TLSCertPEM:                args.TLSCertPEM,
		DeviceFingerprint:         args.DeviceFingerprint,
		DeviceToken:               args.DeviceToken,
		ExpectedDaemonFingerprint: args.ExpectedDaemonFingerprint,
	})
}

// repoAdapter 把 remote_device_repo.PairedAgentredRepo 的方法集暴露给 watcher
// (watcher 只需要 List/Get/UpdateLastSeen,不需要 Insert/Update/Delete)。
type repoAdapter struct {
	inner remote_device_repo.PairedAgentredRepo
}

func (a *repoAdapter) List(ctx context.Context) ([]*paired_agentred_entity.PairedAgentred, error) {
	return a.inner.List(ctx)
}

func (a *repoAdapter) Get(ctx context.Context, id int64) (*paired_agentred_entity.PairedAgentred, error) {
	return a.inner.Get(ctx, id)
}

func (a *repoAdapter) UpdateLastSeen(ctx context.Context, id int64, lastSeenAtMs int64, lastError string) error {
	return a.inner.UpdateLastSeen(ctx, id, lastSeenAtMs, lastError)
}

// providerRecorderAdapter 把 remote_device_svc.RemoteDeviceSvc 的 provider cache
// API 适配成 watcher.ProviderRecorder(两边的 ProviderSummary 类型同形但不同包)。
type providerRecorderAdapter struct {
	inner remote_device_svc.RemoteDeviceSvc
}

func (a *providerRecorderAdapter) RecordDeviceProviders(deviceID int64, ps []watcher.ProviderSummary) {
	translated := make([]remote_device_svc.ProviderSummary, len(ps))
	for i, p := range ps {
		models := make([]remote_device_svc.ModelSummary, 0, len(p.Models))
		for _, m := range p.Models {
			models = append(models, remote_device_svc.ModelSummary{
				Key: m.Key, ModelID: m.ModelID, Name: m.Name, Enabled: m.Enabled,
			})
		}
		translated[i] = remote_device_svc.ProviderSummary{
			Key: p.Key, Name: p.Name, Type: p.Type,
			DefaultModelKey: p.DefaultModelKey, Models: models,
		}
	}
	a.inner.RecordDeviceProviders(deviceID, translated)
}

func (a *providerRecorderAdapter) RecordDeviceCapabilities(deviceID int64, caps []string) {
	a.inner.RecordDeviceCapabilities(deviceID, caps)
}

// InitRemoteDeviceWatcher 必须在 InitRemoteDevice 之后调。注入 watcher,
// 顺带把 watcher 反向挂到 remote_device_svc。emit 必须非 nil(调用方强契约)。
func InitRemoteDeviceWatcher(_ context.Context, emit watcher.Emitter) {
	devSvc := remote_device_svc.Default()
	svc := watcher.New(
		&repoAdapter{inner: remote_device_repo.PairedAgentred()},
		// 中转拨号与 InitRemoteDevice 注入连接池的是同一个适配器：收编来的行没有
		// LAN 地址，健康探活只能走中转（见 dialAdapter.Open）。server_svc 未装配
		// （单机构建 / 未登录）时留 nil，行为退回纯 LAN。
		&dialAdapter{inner: remote_device_svc.NewDaemonDial(), relay: watcherRelayDial()},
		keychain.Default(),
		emit,
		watcher.DefaultWatcherConfig(),
		watcher.NewRealClock(),
		&providerRecorderAdapter{inner: devSvc},
	)
	watcher.SetDefault(svc)

	// 反向挂到 remote_device_svc:Add/Remove/UpdateTLS 触发 watcher 生命周期。
	devSvc.SetWatcher(&watcherPortAdapter{inner: svc})
}

// watcherPortAdapter 把 watcher.Service 适配成 remote_device_svc.WatcherPort
// (两个接口签名 1:1,但定义在各自的包里,所以要 thin adapter)。
type watcherPortAdapter struct {
	inner watcher.Service
}

func (a *watcherPortAdapter) Start(ctx context.Context, deviceID int64) error {
	return a.inner.Start(ctx, deviceID)
}

func (a *watcherPortAdapter) Stop(deviceID int64) { a.inner.Stop(deviceID) }

func (a *watcherPortAdapter) Restart(ctx context.Context, deviceID int64) error {
	return a.inner.Restart(ctx, deviceID)
}

// RemoteDeviceWatcherBoot 替代旧 RemoteDeviceBoot:对所有 ACTIVE 设备启 watcher。
// 用 Background ctx 让 watcher 生命周期跟 svc 走,不被 startup ctx 提前 cancel。
func RemoteDeviceWatcherBoot(_ context.Context) {
	if err := watcher.Default().StartAll(context.Background()); err != nil {
		logger.Default().Warn("remote_device_watcher StartAll", zap.Error(err))
	}
}

// watcherRelayDial 交出账号中转拨号端口，server_svc 未装配时为 nil。
// 与 InitRemoteDevice 给连接池注入的是同一个适配器类型，两条路径因此不会漂移。
func watcherRelayDial() remote_device_svc.RelayDialPort {
	svc := server_svc.Server()
	if svc == nil {
		return nil
	}
	return relayDialAdapter{inner: svc}
}
