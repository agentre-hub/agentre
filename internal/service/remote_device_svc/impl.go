package remote_device_svc

import (
	"sync"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/repository/remote_device_repo"
)

type service struct {
	repo     remote_device_repo.PairedAgentredRepo
	dial     DaemonDialPort
	keychain KeychainPort
	pool     ConnPool
	watcher  WatcherPort // 可空:测试不注入时 Add/Remove/UpdateTLS 跳过 watcher 调用

	providerCacheMu sync.RWMutex
	providerCache   map[int64][]ProviderSummary

	// capabilitiesMu / capabilities 是 watcher 从 health.ping 记下的能力位（决策 11）。
	// 与 providerCache 一样是进程内缓存:描述当前那个 daemon 进程,重启桌面后重新探。
	capabilitiesMu sync.RWMutex
	capabilities   map[int64][]string
}

// New constructs a service. Production wiring lives in bootstrap; tests
// inject mock ports directly. pool 由 bootstrap 用 NewConnPool 构造后注入。
func New(repo remote_device_repo.PairedAgentredRepo, dial DaemonDialPort, kc KeychainPort, pool ConnPool) RemoteDeviceSvc {
	return &service{
		repo:          repo,
		dial:          dial,
		keychain:      kc,
		pool:          pool,
		providerCache: make(map[int64][]ProviderSummary),
		capabilities:  make(map[int64][]string),
	}
}

// RecordDeviceProviders overwrites the cached provider list for deviceID.
func (s *service) RecordDeviceProviders(deviceID int64, ps []ProviderSummary) {
	cp := make([]ProviderSummary, len(ps))
	copy(cp, ps)
	s.providerCacheMu.Lock()
	s.providerCache[deviceID] = cp
	s.providerCacheMu.Unlock()
}

// RecordDeviceCapabilities overwrites the cached capability list for deviceID.
func (s *service) RecordDeviceCapabilities(deviceID int64, caps []string) {
	cp := make([]string, len(caps))
	copy(cp, caps)
	s.capabilitiesMu.Lock()
	s.capabilities[deviceID] = cp
	s.capabilitiesMu.Unlock()
}

// SupportsLLMModelTarget reports whether deviceID's daemon advertises the
// llm-model-target-v1 capability（决策 11）。未探过 → false（保守禁用 fixed-model）。
func (s *service) SupportsLLMModelTarget(deviceID int64) bool {
	s.capabilitiesMu.RLock()
	caps, ok := s.capabilities[deviceID]
	s.capabilitiesMu.RUnlock()
	if !ok {
		return false
	}
	for _, c := range caps {
		if c == wire.CapLLMModelTargetV1 {
			return true
		}
	}
	return false
}

// ListDeviceProviders returns a defensive copy of the cached provider list for
// deviceID, or nil if none has been recorded.
func (s *service) ListDeviceProviders(deviceID int64) []ProviderSummary {
	s.providerCacheMu.RLock()
	ps, ok := s.providerCache[deviceID]
	s.providerCacheMu.RUnlock()
	if !ok {
		return nil
	}
	cp := make([]ProviderSummary, len(ps))
	copy(cp, ps)
	return cp
}

// SetWatcher 注入 watcher port。
func (s *service) SetWatcher(w WatcherPort) {
	s.watcher = w
}

// DeviceFingerprint 交出本机设备指纹。与 Add 共用 ensureDeviceFingerprint:同一个
// keychain 账号、同一把生成逻辑,保证 R5 硬不变量(两条路径解析出同一对端标识)。
func (s *service) DeviceFingerprint() (string, error) {
	return s.ensureDeviceFingerprint()
}

// Pool 返回 chat_svc / agent_backend_svc 共享的 per-device 连接池。
func (s *service) Pool() ConnPool { return s.pool }

// keychainAccountForToken returns the keychain account name for a paired
// device's deviceToken.
func keychainAccountForToken(id int64) string {
	return "agentre-daemon-token-" + itoa(id)
}

// accountForDeviceFingerprint is the app-level singleton keychain account.
const accountForDeviceFingerprint = "agentre-device-fingerprint"
