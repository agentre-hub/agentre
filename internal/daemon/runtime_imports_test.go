package daemon

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// TestBackendRuntimesRegistered 钉死 daemon 进程启动后 agentruntime 注册表里
// 至少有 claudecode + codex + piagent(builtin 也 import 但 Run 时被拒绝)。register 触发器
// 住在 runtime_imports.go,该测试防止后人删掉那个空 init 文件,
// 不然 runtime.run 一调就 "backend not registered"。
func TestBackendRuntimesRegistered(t *testing.T) {
	for _, bt := range []agent_backend_entity.BackendType{
		agent_backend_entity.TypeClaudeCode,
		agent_backend_entity.TypeCodex,
		agent_backend_entity.TypePiAgent,
		agent_backend_entity.TypeBuiltin,
	} {
		assert.NotNil(t, agentruntime.RuntimeFor(bt),
			"backend %q must be registered by runtime_imports.go", bt)
	}
	assert.Nil(t, agentruntime.RuntimeFor(agent_backend_entity.TypeOpenClaw),
		"OpenClaw must remain unavailable in agentred until secret enrollment/reference is implemented")
}
