package cc_usage_svc

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
)

// ManagerOpts 是 NewManager 的可选配置。
//   - Now 注入时钟,测试用;留空走 time.Now。
//   - RateLimitBackoffMin 是命中 429 后的最小退避窗口;每次连续 429 翻倍,
//     封顶 RateLimitBackoffMax(默认 5min)。留空走 60s。
//   - RateLimitBackoffMax 封顶值,留空走 5min。
type ManagerOpts struct {
	Now                 func() time.Time
	RateLimitBackoffMin time.Duration
	RateLimitBackoffMax time.Duration
}

// Manager 是 cc_usage_svc 的核心。线程安全,所有 Probe / Get / SetXxx 可并发。
type Manager struct {
	mu         sync.RWMutex
	states     map[DeviceKey]UsageState
	backoffs   map[DeviceKey]*backoffState
	emitter    EmitterFunc
	resolver   FetcherResolver
	now        func() time.Time
	backoffMin time.Duration
	backoffMax time.Duration

	tickerMu sync.Mutex
	tickers  map[DeviceKey]*tickerEntry
}

// backoffState 跟踪一个 device 的 429 退避。successCount 重置 attempts;
// nextAllowed 在 attempts>=1 时设置,Probe 之前会比较。
type backoffState struct {
	attempts    int
	nextAllowed time.Time
}

// NewManager 构造一个全新的 Manager。生产由 Default() 返回单例。
func NewManager(opts ManagerOpts) *Manager {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	backoffMin := opts.RateLimitBackoffMin
	if backoffMin <= 0 {
		backoffMin = 60 * time.Second
	}
	backoffMax := opts.RateLimitBackoffMax
	if backoffMax <= 0 {
		backoffMax = 5 * time.Minute
	}
	return &Manager{
		states:     map[DeviceKey]UsageState{},
		backoffs:   map[DeviceKey]*backoffState{},
		tickers:    map[DeviceKey]*tickerEntry{},
		now:        now,
		backoffMin: backoffMin,
		backoffMax: backoffMax,
	}
}

// SetEmitter 注入推送函数;为 nil 表示禁用推送(probe 仍会更新内存 state)。
func (m *Manager) SetEmitter(e EmitterFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.emitter = e
}

// SetFetcherResolver 注入 deviceKey → Fetcher 的解析器。
// 未设置 resolver 时 Probe 是 no-op,避免在 wire-up 完成前误触发空状态。
func (m *Manager) SetFetcherResolver(r FetcherResolver) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resolver = r
}

// Get 返回当前缓存状态;若该 deviceKey 从未 probe 过,ok=false。
func (m *Manager) Get(key DeviceKey) (UsageState, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st, ok := m.states[key]
	return st, ok
}

// Probe 同步执行一次 fetcher 调用,更新 state 并 emit。
// 没有 resolver 时静默 no-op(还没被 wire-up 起来,不要发出空状态)。
// 429 backoff 窗口内的 Probe 直接返回(既不调 fetcher 也不 emit),
// 避免连续 429 时压垮 endpoint 或触发更严的速率限制。
func (m *Manager) Probe(ctx context.Context, key DeviceKey) {
	m.mu.RLock()
	resolver := m.resolver
	emitter := m.emitter
	prev := m.states[key]
	bo := m.backoffs[key]
	m.mu.RUnlock()

	if resolver == nil {
		return
	}
	now := m.now()
	if bo != nil && bo.attempts > 0 && now.Before(bo.nextAllowed) {
		return
	}

	fetcher, rerr := resolver(key)
	if rerr != nil {
		st := UsageState{Reason: "device_offline", FetchedAtMs: now.UnixMilli()}
		m.commit(key, st, emitter, nil)
		return
	}

	rl, err := fetcher(ctx)
	nowMs := now.UnixMilli()
	if err == nil {
		st := UsageState{Reason: "ok", Data: rl, FetchedAtMs: nowMs}
		m.commit(key, st, emitter, &backoffState{}) // 清空 backoff
		return
	}

	reason := classifyErr(err)
	st := UsageState{Reason: reason, FetchedAtMs: nowMs}
	// 凭证失效:旧数据无意义;清空 Data。
	// no_credentials / device_offline 同理:这台机器没有有效配额,旧值不再有效。
	// 仅 network / rate_limited(瞬态错误)保留 stale Data,让前端继续展示。
	if (reason == "network" || reason == "rate_limited") && prev.Data != nil {
		st.Data = prev.Data
		st.Stale = true
	}
	var newBo *backoffState
	if reason == "rate_limited" {
		attempts := 1
		if bo != nil {
			attempts = bo.attempts + 1
		}
		newBo = &backoffState{
			attempts:    attempts,
			nextAllowed: now.Add(m.computeBackoff(attempts)),
		}
	}
	m.commit(key, st, emitter, newBo)
}

// computeBackoff 计算第 attempts 次 429 应该退避多久。
//
//	delay = min(backoffMin * 2^(attempts-1), backoffMax)
func (m *Manager) computeBackoff(attempts int) time.Duration {
	if attempts <= 0 {
		return 0
	}
	d := m.backoffMin
	for i := 1; i < attempts; i++ {
		d *= 2
		if d >= m.backoffMax {
			return m.backoffMax
		}
	}
	if d > m.backoffMax {
		return m.backoffMax
	}
	return d
}

// commit 写状态 + 可选地更新 backoff + 推送 emitter。
// nextBo=nil 表示不动 backoff(用于无关错误如 network);传 &backoffState{} 清空;
// 传非空 backoffState 设置新窗口。
func (m *Manager) commit(key DeviceKey, st UsageState, emitter EmitterFunc, nextBo *backoffState) {
	m.mu.Lock()
	m.states[key] = st
	if nextBo != nil {
		if nextBo.attempts == 0 {
			delete(m.backoffs, key)
		} else {
			m.backoffs[key] = nextBo
		}
	}
	m.mu.Unlock()
	if emitter != nil {
		emitter(EmitPayload{DeviceKey: key, State: st})
	}
}

// classifyErr 把 ccoauth sentinel + 未知 error 映射成稳定 reason enum。
// 与 daemon handlers/ccusage.go 的 classifyFetchErr 对齐,
// 加上 device_offline(daemon handler 不会产出这个;它是远端 RPC 失败时才出现)。
func classifyErr(err error) string {
	switch {
	case errors.Is(err, ccoauth.ErrNoCredentials):
		return "no_credentials"
	case errors.Is(err, ccoauth.ErrAuthExpired):
		return "auth_expired"
	case errors.Is(err, ccoauth.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, ErrDeviceOffline):
		return "device_offline"
	default:
		return "network"
	}
}

// ErrDeviceOffline 由 remote_fetcher 返回(device 未配对 / 离线 / pool.Borrow 失败)。
// Manager 把它映射成 reason="device_offline"。
var ErrDeviceOffline = errors.New("cc_usage_svc: device offline")
