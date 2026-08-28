package transcriptimport

import (
	"sort"
	"sync"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

var (
	registryMu sync.RWMutex
	registry   = map[agent_backend_entity.BackendType]Source{}
)

// Register 各 runtime 子包在 init() 时调用,把自己的磁盘读取器登记进来。
// 同一后端重复注册以最后一次为准。
func Register(s Source) {
	if s == nil {
		return
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[s.Backend()] = s
}

// SourceFor 按后端类型查询;未注册返回 nil。
func SourceFor(t agent_backend_entity.BackendType) Source {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[t]
}

// Sources 列出所有已注册读取器的快照,按后端类型排序保证遍历顺序稳定。
// 发现聚合遍历它,不引用任何具体后端的构造器。
func Sources() []Source {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]Source, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Backend() < out[j].Backend() })
	return out
}

// SwapSourceForTest 单元测试临时替换注册表项,返回 restore 闭包。
func SwapSourceForTest(t agent_backend_entity.BackendType, s Source) func() {
	registryMu.Lock()
	old, existed := registry[t]
	registry[t] = s
	registryMu.Unlock()
	return func() {
		registryMu.Lock()
		defer registryMu.Unlock()
		if existed {
			registry[t] = old
		} else {
			delete(registry, t)
		}
	}
}
