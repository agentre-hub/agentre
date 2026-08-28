// Package remotefs 是 agentred daemon 端 remotefs.* RPC 的 handler 实现。
// 提供 ListDir / Mkdir 的真 fs 调用,错误映射到 wire sentinel,由
// register 把 sentinel 翻成 *rpc.Error 返给 dispatcher。
package remotefs

import "github.com/agentre-hub/agentre/internal/pkg/remotefs/pathguard"

// Options 注入测试 hook。生产用 NewHandlers(Options{}) 全用默认。
type Options struct {
	HomeFn     pathguard.HomeFunc // 默认 os.UserHomeDir
	MaxEntries int                // 默认 2000
}

const defaultMaxEntries = 2000

// Handlers 持有 remotefs RPC 方法集合,便于将来注入 dependency。
type Handlers struct {
	homeFn     pathguard.HomeFunc
	maxEntries int
}

// NewHandlers 构造 Handlers,未填字段使用安全默认值。
func NewHandlers(opts Options) *Handlers {
	h := &Handlers{
		homeFn:     opts.HomeFn,
		maxEntries: opts.MaxEntries,
	}
	if h.homeFn == nil {
		h.homeFn = osUserHomeDir
	}
	if h.maxEntries <= 0 {
		h.maxEntries = defaultMaxEntries
	}
	return h
}
