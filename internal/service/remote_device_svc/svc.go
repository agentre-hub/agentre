package remote_device_svc

import (
	"context"
)

//go:generate mockgen -source svc.go -destination mock_remote_device_svc/mock_svc.go

// ProviderSummary describes a single LLM provider configured on a remote daemon.
// Populated from health.ping responses; wire types are translated at the watcher layer.
// 非敏感：只含稳定 key + 展示名 + 类型 + 默认模型 key + 模型摘要，绝不含凭证。
type ProviderSummary struct {
	Key             string         `json:"key"`
	Name            string         `json:"name"`
	Type            string         `json:"type"`
	DefaultModelKey string         `json:"defaultModelKey,omitempty"`
	Models          []ModelSummary `json:"models,omitempty"`
}

// ModelSummary 是 daemon 目录里一条模型的非敏感摘要（task 6 多模型）。
type ModelSummary struct {
	Key     string `json:"key"`
	ModelID string `json:"modelId"`
	Name    string `json:"name,omitempty"`
	Enabled bool   `json:"enabled"`
}

// AddRequest 是 Add 的入参。DisplayName 可空，svc 自动派生。
type AddRequest struct {
	URL         string `json:"url"`
	PairingCode string `json:"pairingCode"`
	DisplayName string `json:"displayName,omitempty"`
	TLSMode     string `json:"tlsMode"`
	TLSCertPEM  string `json:"tlsCertPEM,omitempty"`
}

// DeviceView 返回给前端的视图（不含 keychain 秘密）。
type DeviceView struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	URL               string `json:"url"`
	DaemonFingerprint string `json:"daemonFingerprint"`
	InstanceUUID      string `json:"instanceUUID"`
	TLSMode           string `json:"tlsMode"`
	TLSCertPEM        string `json:"tlsCertPEM,omitempty"`
	PairedAt          int64  `json:"pairedAt"`
	LastSeenAt        int64  `json:"lastSeenAt"`
	LastError         string `json:"lastError"`
	Online            bool   `json:"online"`
	// SupportsLLMModelTarget 说明这台 daemon 是否公布 llm-model-target-v1 能力位
	// （决策 11）：不支持时远端 Picker 必须禁用 fixed-model，避免旧 daemon 静默降级。
	// 来自 watcher 最近一次 health.ping 的能力位,进程内缓存,不落库。
	SupportsLLMModelTarget bool `json:"supportsLLMModelTarget,omitempty"`
	// DaemonVersion / DaemonCommit 是远端 agentred 自报的构建标识,来自 watcher 最近
	// 一次 health.ping(与能力位同一条路:进程内缓存,不落库 —— 它描述的是当前那个
	// daemon 进程,机器换了版本下一次心跳就覆盖)。
	//
	// 两者分开而不是一个展示串(决策 4):版本号要能比较,短 commit 为空才是「非发布
	// 构建」的判据(决策 5) —— 未注入版本的构建自称 1.0.0,比任何 0.x 正式版都「新」,
	// 缺了这道闸就会把本地构建的机器判成最新、把正式版判成过期。
	DaemonVersion string `json:"daemonVersion,omitempty"`
	DaemonCommit  string `json:"daemonCommit,omitempty"`
}

// RemoteDeviceSvc 单例接口。
type RemoteDeviceSvc interface {
	List(ctx context.Context) ([]*DeviceView, error)
	Get(ctx context.Context, id int64) (*DeviceView, error)
	Add(ctx context.Context, req AddRequest) (*DeviceView, error)
	// AdoptAccountDevices 收编账号里已有、本机没有本地记录的 agentred（见 adopt.go）。
	AdoptAccountDevices(ctx context.Context, devices []AccountDevice) (int, error)
	// DiscardAdoptedDevices 去掉全部收编来的行，返回台数。登出时调用：那些行的依据
	// 就是「账号说有这台机器」，账号断了依据就没了（见 adopt.go）。
	DiscardAdoptedDevices(ctx context.Context) (int, error)
	Remove(ctx context.Context, id int64) error
	UpdateTLS(ctx context.Context, id int64, mode, pem string) (*DeviceView, error)
	Refresh(ctx context.Context, id int64) (*DeviceView, error)
	Rename(ctx context.Context, id int64, name string) error
	// DeviceFingerprint 交出本机设备指纹(agentre-device-fingerprint keychain 账号),
	// 与 LAN 配对 / 账号登录共用同一指纹(R5 硬不变量)。前端拿它与消息上的 sourceDevice
	// 比对,相等就不渲染来源标识(R17 本机不带)。keychain 缺失时惰性生成(与 Add 同源)。
	DeviceFingerprint() (string, error)
	// SetWatcher 注入 watcher port。生产由 bootstrap 在 watcher_svc 就绪后调用;
	// nil 注入也允许(单测里不关心 watcher 时跳过)。
	SetWatcher(w WatcherPort)
	// Pool 返回 chat_svc / agent_backend_svc 共享的 per-device 连接池。
	// 借出后必须 defer Lease.Release();池子负责 idle 回收 / daemon drop evict。
	Pool() ConnPool
	// RecordDeviceProviders overwrites the in-memory provider cache for deviceID.
	// Called by the watcher on each successful health.ping.
	RecordDeviceProviders(deviceID int64, ps []ProviderSummary)
	// ListDeviceProviders returns the cached provider list for deviceID.
	// Returns nil if no data has been recorded yet. Safe for concurrent use.
	ListDeviceProviders(deviceID int64) []ProviderSummary
	// RecordDeviceCapabilities overwrites the in-memory daemon capability list for
	// deviceID（决策 11）。Called by the watcher on each successful health.ping.
	RecordDeviceCapabilities(deviceID int64, caps []string)
	// SupportsLLMModelTarget reports whether deviceID's daemon advertises the
	// llm-model-target-v1 capability（fixed-model 可被安全选择）。未探过 → false。
	SupportsLLMModelTarget(deviceID int64) bool
	// RecordDeviceBuild overwrites the in-memory build identity for deviceID
	// （版本号 + 短 commit,来自 health.ping）。Called by the watcher on each
	// successful heartbeat. commit 为空串是有意义的取值(非发布构建),不当成缺失。
	RecordDeviceBuild(deviceID int64, version, commit string)
	// DeviceBuild returns the cached build identity for deviceID.
	// 未探过 → 两个空串（界面据此什么都不说,而不是编一个版本）。
	DeviceBuild(deviceID int64) (version, commit string)
	// SyncProvider copies one local LLM provider to the remote daemon state.
	// The raw API key is sent only for this explicit sync operation.
	SyncProvider(ctx context.Context, deviceID int64, providerKey string) error
	// Upgrade triggers the remote one-click upgrade RPC on deviceID(spec「远程
	// 一键升级」). channel 留空按 daemon 当前配置的通道解读;force 越过活跃轮次
	// 闸门,必须由调用方在得到 UpgradeRejectActiveTurns 之后、经过一次显式确认
	// 才能置真(决策 8/21:二次确认承担拦截,主动作本身不禁用)。应答只回受理
	// 结果,升级过程由调用方从版本号变化推断(不在这次调用里等重启)。
	Upgrade(ctx context.Context, deviceID int64, channel string, force bool) (*UpgradeResult, error)
}

var defaultSvc RemoteDeviceSvc

// Default 返回默认实现单例。
func Default() RemoteDeviceSvc { return defaultSvc }

// SetDefault 由 bootstrap 注入实现。
func SetDefault(s RemoteDeviceSvc) { defaultSvc = s }
