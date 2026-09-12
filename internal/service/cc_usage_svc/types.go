// Package cc_usage_svc 维护 per-device 的 Claude Code 5h/7d 配额状态,
// 桌面 chat composer 底栏的 QuotaMeter 通过 wails event "cc_usage:update"
// 订阅它的推送。
//
// 数据维度:
//   - "local"      → 桌面所在机器的 OAuth 凭证(走 ccoauth.NewLocalFetcher)
//   - "remote:<id>" → 某台已配对 agentred 上的凭证(走 daemon RPC claudecode.usage)
//
// 不入库:状态只在内存里 60s 周期刷新;进程重启重拉。
package cc_usage_svc

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

// DeviceKey 是 per-device 状态的索引。约定:
//   - "local"
//   - "remote:<int64 deviceID>"
type DeviceKey string

// LocalKey 是桌面本地 device 的固定 key,导出常量供调用方避免拼写错误。
const LocalKey DeviceKey = "local"

// UsageState 描述某个 device 当前最新一次 probe 的结果。
//
// Reason enum:
//
//	ok / no_credentials / auth_expired / network / rate_limited / device_offline
//
// Stale=true 表示当前 Reason 是错误(network/rate_limited),
// 但 Data 仍是先前成功 probe 的 cache,前端可继续展示。
type UsageState struct {
	Reason      string              `json:"reason"`
	Data        *ccoauth.RateLimits `json:"data,omitempty"`
	Stale       bool                `json:"stale,omitempty"`
	FetchedAtMs int64               `json:"fetchedAtMs"`
}

// EmitPayload 是推送给前端的事件载荷。前端 store 按 DeviceKey 分桶存。
type EmitPayload struct {
	DeviceKey DeviceKey  `json:"deviceKey"`
	State     UsageState `json:"state"`
}

// EmitterFunc 由 App.Startup 注入(包成 wails EventsEmit("cc_usage:update", payload))。
type EmitterFunc func(EmitPayload)

// Fetcher 是单次"读凭证 + 调 API"的函数。本地版直接走 ccoauth.NewLocalFetcher;
// 远端版包成一次 RPC 调用。Manager 不直接依赖 ccoauth 之外的具体实现,
// 通过 FetcherResolver 拿到调好的 Fetcher。
type Fetcher func(ctx context.Context) (*ccoauth.RateLimits, error)

// FetcherResolver 给定 deviceKey,返回对应的 Fetcher,或返回错误(意味着
// "无法为这个 device 拉数据" —— 比如 remote device 已离线 / 不存在)。
// 返回错误时 Manager 标记该 device state.Reason="device_offline"。
type FetcherResolver func(DeviceKey) (Fetcher, error)
