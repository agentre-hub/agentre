package agentskill

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// DiscoveryTTL 是发现缓存的兜底有效期:覆盖观测不到文件变化的路径(远程机器)。
const DiscoveryTTL = 5 * time.Minute

// generation 是进程级失效代数。不经由插件注册文件的安装路径(ctlskill 安装 /
// 卸载)在写完后 BumpGeneration,所有缓存条目随之作废。
var generation atomic.Uint64

// BumpGeneration 让此前缓存的全部发现结果作废。
func BumpGeneration() { generation.Add(1) }

// Generation 返回当前失效代数(测试与缓存内部使用)。
func Generation() uint64 { return generation.Load() }

// RegistryStamp 把本机各 CLI 的插件注册 / 配置文件的 mtime+size 摊成一个指纹:
// `claude plugin install/uninstall/enable` 与 Codex 配置变化都会改动其中某个文件。
// 不存在的文件记为 "-"。
func RegistryStamp() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	files := []string{
		filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		filepath.Join(home, ".claude", "plugins", "known_marketplaces.json"),
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".codex", "config.toml"),
	}
	var b strings.Builder
	for _, f := range files {
		info, err := os.Stat(f)
		if err != nil {
			b.WriteString("-;")
			continue
		}
		b.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(info.Size(), 10))
		b.WriteByte(';')
	}
	return b.String()
}

type cacheEntry struct {
	value any
	at    time.Time
	gen   uint64
	stamp string
}

// DiscoveryCache 缓存发现结果(起 CLI 子进程很慢):按调用方给的 key 记忆,TTL 兜底,
// 调用方给的 stamp(如 RegistryStamp)或全局代数变化即失效,并发的同 key 请求合并为
// 一次加载。错误不入缓存。零值不可用,用 NewDiscoveryCache;nil 缓存等同直通。
//
// 缓存的值被多个调用方共享,调用方只读不改。
type DiscoveryCache struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	entries map[string]cacheEntry
	// epoch 是缓存自己的失效代数:InvalidatePrefix / refresh 推进它。在途的加载只有在
	// epoch 没变时才写回,singleflight 的 key 也带上它 —— 失效之后到达的请求不去搭失效
	// 之前那次加载的车,在途那次晚完成也盖不掉更新的结果。
	epoch uint64
	group singleflight.Group
}

// NewDiscoveryCache 生产默认:5 分钟 TTL、真实时钟。
func NewDiscoveryCache() *DiscoveryCache {
	return NewDiscoveryCacheWith(DiscoveryTTL, time.Now)
}

// NewDiscoveryCacheWith 注入 TTL 与时钟(测试用)。
func NewDiscoveryCacheWith(ttl time.Duration, now func() time.Time) *DiscoveryCache {
	return &DiscoveryCache{ttl: ttl, now: now, entries: map[string]cacheEntry{}}
}

// InvalidatePrefix 丢弃所有以 prefix 开头的条目(手动刷新用:按机器 + 后端作用域清)。
func (c *DiscoveryCache) InvalidatePrefix(prefix string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.epoch++
	for k := range c.entries {
		if strings.HasPrefix(k, prefix) {
			delete(c.entries, k)
		}
	}
}

func (c *DiscoveryCache) lookup(key, stamp string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if c.now().Sub(e.at) >= c.ttl || e.gen != Generation() || e.stamp != stamp {
		delete(c.entries, key)
		return nil, false
	}
	return e.value, true
}

// CachedDiscovery 取 key 对应的缓存值,未命中 / 过期 / 失效时调用 load 并缓存成功结果。
// refresh 为真时先丢弃该 key 再加载。
func CachedDiscovery[T any](c *DiscoveryCache, key, stamp string, refresh bool, load func() (T, error)) (T, error) {
	if c == nil {
		return load()
	}
	if refresh {
		c.mu.Lock()
		c.epoch++
		delete(c.entries, key)
		c.mu.Unlock()
	} else if v, ok := c.lookup(key, stamp); ok {
		return v.(T), nil
	}
	c.mu.Lock()
	epoch := c.epoch
	c.mu.Unlock()
	flight := key + "\x00" + strconv.FormatUint(epoch, 10)
	v, err, _ := c.group.Do(flight, func() (any, error) {
		// 代数 / 时间在加载**前**取:加载期间发生的失效不会被这次结果盖掉。
		gen, at := Generation(), c.now()
		val, err := load()
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.epoch == epoch {
			c.entries[key] = cacheEntry{value: val, at: at, gen: gen, stamp: stamp}
		}
		c.mu.Unlock()
		return val, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return v.(T), nil
}
