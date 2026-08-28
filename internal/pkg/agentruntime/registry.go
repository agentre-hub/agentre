package agentruntime

import (
	"context"
	"maps"
	"sync"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

// Runtime 是 agent backend 跑一次 turn 的统一入口。各 backend 实现(claudecode /
// codex / builtin / remote)在 runtimes/<name>/runtime.go 提供 *Runtime 类型,
// init() 时调 RegisterRuntime 自注册。
//
// 控制接口(Steerer / Aborter / SteerCanceler / SteerDrainer /
// PermissionModeSetter / AskAnswerSink / ToolPermissionSink)仍由调用方
// 按需 type assert,不强制写进 Runtime;每个 backend 按能力实现。
type Runtime interface {
	// Capabilities 返回 backend 静态能力矩阵,供前端 capability gating / PermissionModePill
	// 等 UI 投影用。同一 runtime 多次调用返回相同结果,无需 ctx。
	Capabilities() capability.Capabilities

	// Run 启动一轮发送;返回事件流 + 一个会异步被填充的 *RunResult。
	// 调用方 drain 完 channel 后才允许读 *RunResult。
	Run(ctx context.Context, req RunRequest) (<-chan Event, *RunResult, error)
}

var (
	registryMu sync.RWMutex
	registry   = map[agent_backend_entity.BackendType]Runtime{}
)

// RuntimeFor 查询;未注册返 nil。
func RuntimeFor(t agent_backend_entity.BackendType) Runtime {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registry[t]
}

// RegisteredRuntimes 列出所有已注册 runtime 的快照副本;供 chat_svc / daemon
// 做 capability 投影和运行时能力预校验。
func RegisteredRuntimes() map[agent_backend_entity.BackendType]Runtime {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make(map[agent_backend_entity.BackendType]Runtime, len(registry))
	maps.Copy(out, registry)
	return out
}

// RegisterRuntime 各 runtime 子包在 init() 时调用,把 *Runtime 实例登记进注册表。
func RegisterRuntime(t agent_backend_entity.BackendType, r Runtime) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[t] = r
}

// SwapRuntimeForTest 单元测试临时替换 RuntimeFor 注册表项,返回 restore 闭包。
func SwapRuntimeForTest(t agent_backend_entity.BackendType, r Runtime) func() {
	registryMu.Lock()
	old, existed := registry[t]
	registry[t] = r
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
