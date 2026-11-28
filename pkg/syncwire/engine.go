package syncwire

import "slices"

// ── 引擎快照与供应商租约(agentred ↔ server)───────────────────────────────
//
// agentred 不参与 sync_objects:它从 server 拉一份**快照**(账号下的供应商元数据、引擎
// 配置、这台设备的 CLI 路径覆盖),需要某个供应商的 key 时再用设备凭据**租**一份,只放
// 内存(规格「凭据 › agentred:按需租用」)。
//
// **快照里没有 key。** 整个 module 里键名是 api_key 的字段只有两处:发给桌面端的
// LLMProviderPayload,与发给 agentred 的 LeaseResponse —— engine_test.go 按源码钉住这一条。

// PathEngineSnapshot 是 agentred 拉引擎快照的 server 端点:GET,用设备自己的账号凭据
// 认证,应答体是 EngineSnapshot(经 cago 信封)。
const PathEngineSnapshot = "/v1/engine/snapshot"

// PathProviderLease 是 agentred 申请(与续租)供应商租约的 server 端点:POST,请求体是
// LeaseRequest,应答体是 LeaseResponse。续租就是再申请一次。server 还不提供它时(404)
// 或连不上时,agentred 一律按「拿不到租约」处理。
const PathProviderLease = "/v1/engine/leases"

// AccountSignalLeaseRevoked 是账号通道上「立即丢弃已缓存租约」的信号种类:设备被移除、
// 供应商被删除或改了 key 时由 server 发出。线上由账号通道通知 account_lease_revoked
// 承载(pkg/wire),带 provider_keys;为空表示这台设备的全部租约。
const AccountSignalLeaseRevoked = "lease_revoked"

// 申请租约的用途:转发一轮对话的请求、供应商测试、模型发现。
const (
	LeasePurposeTurn     = "turn"
	LeasePurposeTest     = "test"
	LeasePurposeDiscover = "discover"
)

var leasePurposes = []string{LeasePurposeTurn, LeasePurposeTest, LeasePurposeDiscover}

// LeasePurposeValid 报告 purpose 是否属于申请用途的闭合词表。
func LeasePurposeValid(purpose string) bool {
	return slices.Contains(leasePurposes, purpose)
}

// LeaseRequest 申请某个供应商的租约。申请的是哪台设备由凭据决定,不由请求体决定。
type LeaseRequest struct {
	ProviderKey string `json:"provider_key" binding:"required,max=128"`
	Purpose     string `json:"purpose"      binding:"required,oneof=turn test discover"`
}

// LeaseResponse 是一份租约:这台设备此刻可以用的 key 与端点,以及到期时刻(Unix 毫秒)。
// 它是 module 里除 LLMProviderPayload 外**唯一**携带 key 的结构;接收方只把它放在内存里。
type LeaseResponse struct {
	ProviderKey string `json:"provider_key"`
	APIKey      string `json:"api_key"`
	BaseURL     string `json:"base_url"`
	ExpiresAt   int64  `json:"expires_at"`
}

// EngineSnapshot 是 agentred 拉到的账号引擎子集。每次都是**整份**:接收方整体替换上一份。
type EngineSnapshot struct {
	// Providers 是账号下的供应商元数据,不含 key。
	Providers []SnapshotProvider `json:"providers"`
	// EngineProfiles 是账号下的引擎配置(带类型与 config),按 sync_id 寻址。
	EngineProfiles []SnapshotEngineProfile `json:"engine_profiles"`
	// CLIPaths 是**这台设备**按运行时类型的 CLI 路径覆盖;没有的运行时走 PATH。
	CLIPaths []SnapshotCLIPath `json:"cli_paths"`
}

// SnapshotProvider 是一个供应商的元数据:名称、类型、端点、模型目录与「谁能连到」。
type SnapshotProvider struct {
	ProviderKey     string             `json:"provider_key"`
	Name            string             `json:"name"`
	Type            string             `json:"type"`
	BaseURL         string             `json:"base_url"`
	DefaultModelKey string             `json:"default_model_key"`
	Models          []LLMProviderModel `json:"models"`
	Reach           *LLMProviderReach  `json:"reach,omitempty"`
}

// SnapshotEngineProfile 是一份引擎配置:同步标识加上 engine_profile 同步载荷的全部字段
// (内嵌,线上是同一层的键)。
type SnapshotEngineProfile struct {
	SyncID string `json:"sync_id"`
	EngineProfilePayload
}

// SnapshotCLIPath 是这台设备上某种运行时的 CLI 路径覆盖。
type SnapshotCLIPath struct {
	Runtime string `json:"runtime"`
	CLIPath string `json:"cli_path"`
}
