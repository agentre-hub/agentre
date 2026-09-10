// Package workspacefs 是 agentred daemon 端 workspacefs.* RPC 的 handler 实现。
// 薄封装 internal/pkg/workspacefs 的 ListDir / GitChanges / GitBranches /
// ReadFile / GitFileContent / GitState(设计决策 4:核心逻辑放叶子包,host 的
// 本机分支与 daemon handler 共用同一份实现),把 sentinel 错误映射到 wire 层,
// 由 register 把 sentinel 翻成 *rpc.Error 返给 dispatcher。
package workspacefs

import pkgworkspacefs "github.com/agentre-hub/agentre/internal/pkg/workspacefs"

// Options 注入测试 hook。生产用 NewHandlers(Options{}) 全用默认。
type Options struct {
	MaxEntries    int // 默认 pkgworkspacefs.DefaultMaxEntries(2000)
	MaxSearchHits int // 默认 pkgworkspacefs.DefaultMaxSearchHits(500)
	MaxSearchDirs int // 默认 pkgworkspacefs.DefaultMaxSearchDirs(20000)
}

// Handlers 持有 workspacefs RPC 方法集合,便于将来注入 dependency。
type Handlers struct {
	maxEntries    int
	maxSearchHits int
	maxSearchDirs int
}

// NewHandlers 构造 Handlers,未填字段使用安全默认值。
func NewHandlers(opts Options) *Handlers {
	h := &Handlers{
		maxEntries:    opts.MaxEntries,
		maxSearchHits: opts.MaxSearchHits,
		maxSearchDirs: opts.MaxSearchDirs,
	}
	if h.maxEntries <= 0 {
		h.maxEntries = pkgworkspacefs.DefaultMaxEntries
	}
	if h.maxSearchHits <= 0 {
		h.maxSearchHits = pkgworkspacefs.DefaultMaxSearchHits
	}
	if h.maxSearchDirs <= 0 {
		h.maxSearchDirs = pkgworkspacefs.DefaultMaxSearchDirs
	}
	return h
}
