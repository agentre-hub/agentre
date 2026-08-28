package remote_device_watcher_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/model/entity/paired_agentred_entity"
)

//go:generate mockgen -source ports.go -destination mock_remote_device_watcher_svc/mock_ports.go

// DaemonDialPort 与 remote_device_svc.DaemonDialPort 同语义,watcher_svc 单独声明
// 以走自己的 consumer-side interface(避免反向依赖)。生产实现是同一份。
type DaemonDialPort interface {
	Open(ctx context.Context, args OpenArgs) (client.ProtobufConnection, error)
}

// OpenArgs 复用 remote_device_svc 的 ConnectArgs 字段集。watcher 单独命名是为了
// 防止跨包字段漂移时被静默打中。生产 adapter 把这两个结构互相平铺。
type OpenArgs struct {
	URL                       string
	TLSMode                   string
	TLSCertPEM                string
	DeviceFingerprint         string
	DeviceToken               string
	ExpectedDaemonFingerprint string
}

// KeychainPort 抽象 OS keychain 读取(与 remote_device_svc.KeychainPort 同语义)。
type KeychainPort interface {
	Get(account string) (string, error)
}

// PairedAgentredReader 是 watcher 需要的 repo 子集(读+写 last_seen)。
type PairedAgentredReader interface {
	List(ctx context.Context) ([]*paired_agentred_entity.PairedAgentred, error)
	Get(ctx context.Context, id int64) (*paired_agentred_entity.PairedAgentred, error)
	UpdateLastSeen(ctx context.Context, id int64, lastSeenAtMs int64, lastError string) error
}

// Emitter 把状态变更推给上层(生产是 Wails EventsEmit,单测是 spy)。
type Emitter interface {
	Emit(payload StateEvent)
}

// EmitterFunc 适配 func 风格 emitter。
type EmitterFunc func(payload StateEvent)

func (f EmitterFunc) Emit(p StateEvent) { f(p) }

// StateEvent 是 watcher → 前端的事件 payload(JSON 序列化通过 Wails)。
type StateEvent struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Online     bool   `json:"online"`
	LastSeenAt int64  `json:"lastSeenAt"`
	LastError  string `json:"lastError"`
}

// EventName 是 Wails event 通道名,前端 EventsOn 用同名订阅。
const EventName = "remote.device.state"

// ProviderSummary mirrors remote_device_svc.ProviderSummary so the watcher
// package stays free of a direct dependency on remote_device_svc.
type ProviderSummary struct {
	Key             string         `json:"key"`
	Name            string         `json:"name"`
	Type            string         `json:"type"`
	DefaultModelKey string         `json:"defaultModelKey,omitempty"`
	Models          []ModelSummary `json:"models,omitempty"`
}

// ModelSummary mirrors remote_device_svc.ModelSummary（非敏感模型摘要）。
type ModelSummary struct {
	Key     string `json:"key"`
	ModelID string `json:"modelId"`
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled"`
}

// ProviderRecorder is the narrow interface watcher calls after each successful
// health.ping to stash the daemon's provider list and capability bits.
// Production implementation is remote_device_svc.RemoteDeviceSvc; tests may
// inject a spy or nil (no-op).
type ProviderRecorder interface {
	RecordDeviceProviders(deviceID int64, ps []ProviderSummary)
	// RecordDeviceCapabilities 记下 daemon 公布的 llm-model-target-v1 能力位（决策 11），
	// 桌面端据此决定是否允许选择远端 fixed-model。
	RecordDeviceCapabilities(deviceID int64, caps []string)
}
