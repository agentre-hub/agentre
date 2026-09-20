package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// TestBackendRuntimesRegistered 钉死 daemon 进程启动后 agentruntime 注册表里
// 至少有 claudecode + codex + piagent + acp(builtin 也 import 但 Run 时被拒绝)。
// register 触发器住在 runtime_imports.go,该测试防止后人删掉那个空 init 文件,
// 不然 runtime.run 一调就 "backend not registered"。
func TestBackendRuntimesRegistered(t *testing.T) {
	for _, bt := range []agent_backend_entity.BackendType{
		agent_backend_entity.TypeClaudeCode,
		agent_backend_entity.TypeCodex,
		agent_backend_entity.TypePiAgent,
		agent_backend_entity.TypeACP,
		agent_backend_entity.TypeBuiltin,
	} {
		assert.NotNil(t, agentruntime.RuntimeFor(bt),
			"backend %q must be registered by runtime_imports.go", bt)
	}
	// agentred 登记 Hermes / OpenClaw 的设备本地凭据并能测试连接(handlers 经无副作用的
	// backendcred / hermesgateway / openclawgateway),但并不在本机跑这两种 runtime:
	// 这条守卫钉住凭据那一族没有把 runtime 包连同它的 init 一起拖进来。
	for _, bt := range []agent_backend_entity.BackendType{
		agent_backend_entity.TypeOpenClaw,
		agent_backend_entity.TypeHermes,
	} {
		assert.Nil(t, agentruntime.RuntimeFor(bt),
			"backend %q must not be registered in agentred: its credential handlers must stay runtime-free", bt)
	}
}
