package remote_device_watcher_svc

import (
	"context"
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// WatcherConfig 控制单 watcher 行为。
type WatcherConfig struct {
	HeartbeatInterval time.Duration
	CallTimeout       time.Duration
	Backoff           BackoffConfig
}

// DefaultWatcherConfig 是生产默认值(spec §4)。
func DefaultWatcherConfig() WatcherConfig {
	return WatcherConfig{
		HeartbeatInterval: 5 * time.Second,
		CallTimeout:       3 * time.Second,
		Backoff: BackoffConfig{
			Initial:    time.Second,
			Max:        30 * time.Second,
			Multiplier: 2.0,
			Jitter:     0.2,
		},
	}
}

// Watcher 守护单台设备的连接状态。一个 Run goroutine 跑到 ctx cancel。
type Watcher struct {
	deviceID int64
	repo     PairedAgentredReader
	dial     DaemonDialPort
	keychain KeychainPort
	emit     Emitter
	cfg      WatcherConfig
	clock    Clock
	backoff  *Backoff
	done     chan struct{}
	recorder ProviderRecorder // 可空:nil 时跳过 provider cache 更新
}

// keychainTokenAccount 与 remote_device_svc.keychainAccountForToken 同步;
// watcher_svc 单独定义避免反向依赖。
func keychainTokenAccount(id int64) string {
	return "agentre-daemon-token-" + strconv.FormatInt(id, 10)
}

// keychainFingerprintAccount 与 remote_device_svc.accountForDeviceFingerprint 同步。
const keychainFingerprintAccount = "agentre-device-fingerprint"

// NewWatcher 构造一个 watcher。调用方负责 go w.Run(ctx)。
// recorder 可为 nil;非 nil 时每次心跳成功后调 RecordDeviceProviders。
func NewWatcher(
	deviceID int64,
	repo PairedAgentredReader,
	dial DaemonDialPort,
	kc KeychainPort,
	emit Emitter,
	cfg WatcherConfig,
	clock Clock,
	recorder ProviderRecorder,
) *Watcher {
	r := rand.New(rand.NewSource(time.Now().UnixNano() ^ deviceID))
	return &Watcher{
		deviceID: deviceID,
		repo:     repo,
		dial:     dial,
		keychain: kc,
		emit:     emit,
		cfg:      cfg,
		clock:    clock,
		backoff:  NewBackoff(cfg.Backoff, r),
		done:     make(chan struct{}),
		recorder: recorder,
	}
}

// Run 阻塞到 ctx cancel 才返回。done channel 在 Run 退出时关闭,Wait() 用。
func (w *Watcher) Run(ctx context.Context) {
	defer close(w.done)
	logger.Default().Info("remote_device_watcher_svc.Watcher: started",
		zap.Int64("deviceId", w.deviceID))
	defer logger.Default().Info("remote_device_watcher_svc.Watcher: stopped",
		zap.Int64("deviceId", w.deviceID))
	for {
		if ctx.Err() != nil {
			return
		}
		c, row, err := w.dialOnce(ctx)
		switch classify(err) {
		case errKindOK:
			w.backoff.Reset()
			logger.Default().Info("remote_device_watcher_svc.Watcher: online",
				zap.Int64("deviceId", w.deviceID))
			w.emitOnline(row)
			if cont := w.heartbeat(ctx, c, row); !cont {
				return
			}
			_ = c.Close()
			// 心跳错误 → 退避重连
			if !w.clock.Sleep(ctx, w.backoff.Next()) {
				return
			}
		case errKindPermanent:
			// degraded:不退避,等 ctx cancel。打 Warn 而不是 Error 是因为
			// 这是 expected 终态(用户撤销 token / 主动删 device),运维只需
			// 知道"watcher 主动停了"。
			logger.Default().Warn("remote_device_watcher_svc.Watcher: permanent failure, degraded",
				zap.Int64("deviceId", w.deviceID),
				zap.String("reason", classifyMessage(err)))
			w.emitError(row, classifyMessage(err))
			<-ctx.Done()
			return
		case errKindTransient:
			// transient = 网络 / TLS / daemon 临时不在;只在 Debug 打,避免
			// 一直断网时刷屏。
			logger.Default().Debug("remote_device_watcher_svc.Watcher: transient failure, will retry",
				zap.Int64("deviceId", w.deviceID), zap.Error(err))
			// 走 classifyMessage 而不是直接拼 "dial_failed:":协议层的拒绝要保住
			// 自己的代号,设备面板据它给出「升级远端 agentred」而不是「查网络与端口」。
			w.emitError(row, classifyMessage(err))
			if !w.clock.Sleep(ctx, w.backoff.Next()) {
				return
			}
		}
	}
}

// Wait 阻塞直到 Run 退出。Stop 后调它,确认 goroutine 释放。
func (w *Watcher) Wait() { <-w.done }

// dialOnce 加载 row + keychain,拨一次。返回的 client 已鉴权。
func (w *Watcher) dialOnce(ctx context.Context) (client.ProtobufConnection, *paired_agentred_entity.PairedAgentred, error) {
	row, err := w.repo.Get(ctx, w.deviceID)
	if err != nil {
		return nil, nil, err
	}
	if row == nil {
		return nil, nil, errPermanentDeviceGone
	}
	var token string
	if !row.IsRelayOnly() {
		token, err = w.keychain.Get(keychainTokenAccount(w.deviceID))
		if err != nil || token == "" {
			return nil, row, errPermanentUnauthorized
		}
	}
	fp, err := w.keychain.Get(keychainFingerprintAccount)
	if err != nil || fp == "" {
		return nil, row, errPermanentUnauthorized
	}
	c, err := w.dial.Open(ctx, OpenArgs{
		URL: row.URL, TLSMode: row.TLSMode, TLSCertPEM: row.TLSCertPEM,
		DeviceFingerprint: fp, DeviceToken: token,
		ExpectedDaemonFingerprint: row.DaemonFingerprint,
	})
	if err != nil {
		return nil, row, err
	}
	return c, row, nil
}

// heartbeat 跑 ticker,每次 tick 发 health.ping。返回 false 表示 ctx cancel,Run 退出;
// 返回 true 表示心跳出错需要退避重连。
func (w *Watcher) heartbeat(ctx context.Context, c client.ProtobufConnection, row *paired_agentred_entity.PairedAgentred) bool {
	t := time.NewTicker(w.cfg.HeartbeatInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = c.Close()
			return false
		case <-t.C:
			cctx, cancel := context.WithTimeout(ctx, w.cfg.CallTimeout)
			res, err := wirecall.HealthPing(cctx, c, &agentrewire.HealthPingRequest{})
			cancel()
			if err != nil {
				// 心跳失败 = daemon 死了 / 网络断了 → 走重连。Warn 一条让
				// 运维知道断在哪个 device,而不是只看到前端 banner 变灰。
				logger.Default().Warn("remote_device_watcher_svc.Watcher: heartbeat failed, will reconnect",
					zap.Int64("deviceId", w.deviceID), zap.Error(err))
				w.emitError(row, "dial_failed:"+err.Error())
				return true
			}
			_ = w.repo.UpdateLastSeen(context.Background(), w.deviceID, w.clock.NowMs(), "")
			recordHealth(w.recorder, w.deviceID, res)
			// online 状态持续:不再 emit,避免事件风暴
		}
	}
}

// recordHealth 把一次成功心跳的应答翻成缓存里的三样东西:provider 目录、能力位,
// 以及 daemon 自报的构建标识(决策 4 的版本号 + 短 commit)。recorder 为 nil 时是
// no-op —— 单测里不关心缓存的用例就传 nil。
func recordHealth(recorder ProviderRecorder, deviceID int64, res *agentrewire.HealthPingResponse) {
	if recorder == nil {
		return
	}
	providers := make([]ProviderSummary, 0, len(res.GetProviders()))
	for _, provider := range res.GetProviders() {
		models := make([]ModelSummary, 0, len(provider.GetModels()))
		for _, model := range provider.GetModels() {
			models = append(models, ModelSummary{Key: model.GetKey(), ModelID: model.GetModelId(), Name: model.GetName(), Enabled: model.GetEnabled()})
		}
		providers = append(providers, ProviderSummary{Key: provider.GetKey(), Name: provider.GetName(), Type: provider.GetType(), DefaultModelKey: provider.GetDefaultModelKey(), Models: models})
	}
	recorder.RecordDeviceProviders(deviceID, providers)
	recorder.RecordDeviceCapabilities(deviceID, res.GetCapabilities())
	// 短 commit 为空串是有意义的取值(非发布构建),照记不误:漏掉它,本地构建的机器
	// 会被当成发布构建劝升(决策 5)。
	recorder.RecordDeviceBuild(deviceID, res.GetDaemonVersion(), res.GetDaemonCommit())
}

func (w *Watcher) emitOnline(row *paired_agentred_entity.PairedAgentred) {
	now := w.clock.NowMs()
	_ = w.repo.UpdateLastSeen(context.Background(), w.deviceID, now, "")
	w.emit.Emit(StateEvent{
		ID: w.deviceID, Name: row.Name, Online: true,
		LastSeenAt: now, LastError: "",
	})
}

func (w *Watcher) emitError(row *paired_agentred_entity.PairedAgentred, errMsg string) {
	var name string
	var lastSeen int64
	if row != nil {
		name = row.Name
		lastSeen = row.LastSeenAt
	}
	_ = w.repo.UpdateLastSeen(context.Background(), w.deviceID, lastSeen, errMsg)
	w.emit.Emit(StateEvent{
		ID: w.deviceID, Name: name, Online: false,
		LastSeenAt: lastSeen, LastError: errMsg,
	})
}

// 错误分类。
type errKind int

const (
	errKindOK errKind = iota
	errKindTransient
	errKindPermanent
)

var (
	errPermanentUnauthorized = errors.New("unauthorized")
	errPermanentDeviceGone   = errors.New("device_gone")
)

func classify(err error) errKind {
	if err == nil {
		return errKindOK
	}
	if errors.Is(err, errPermanentUnauthorized) ||
		errors.Is(err, errPermanentDeviceGone) {
		return errKindPermanent
	}
	// TOFU mismatch 来自 dial.Open(remote_device_svc.ErrTOFUMismatch)。
	// 我们通过字符串匹配避免反向引用 remote_device_svc。
	if strings.Contains(err.Error(), "tofu mismatch") {
		return errKindPermanent
	}
	return errKindTransient
}

// 协议层拒绝的判据。与 tofu mismatch 同一手法(字符串匹配,避免反向依赖
// remote_device_svc),对应它的 ErrProtocolVersionMismatch / ErrProtocolUnsupported。
const (
	msgProtocolMismatch    = "speaks another agentre protocol version"
	msgProtocolUnsupported = "speaks no agentre protobuf protocol"
)

func classifyMessage(err error) string {
	switch {
	case errors.Is(err, errPermanentUnauthorized):
		return "unauthorized"
	case errors.Is(err, errPermanentDeviceGone):
		return "device_gone"
	case strings.Contains(err.Error(), "tofu mismatch"):
		return "tofu_mismatch"
	// 心跳与重连是 lastError 的常驻写者:把协议层的拒绝折进 "dial_failed:" 会
	// 盖掉 refresh.go 刚落下的 protocol_mismatch,设备面板于是把「这台 agentred
	// 太旧」说成一次普通的连接失败,把用户指去查网络与端口。
	case strings.Contains(err.Error(), msgProtocolMismatch):
		return "protocol_mismatch"
	case strings.Contains(err.Error(), msgProtocolUnsupported):
		return "protocol_unsupported"
	default:
		return "dial_failed:" + err.Error()
	}
}
