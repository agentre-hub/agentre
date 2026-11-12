package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/memory_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func memoryCaps() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{capability.CapMemory: true}}
}

// 记忆页头部：执行目标来源把 agent 的各档后端翻成判档所需的事实；
// 能力集未知的远端档不下结论，直接略去。
func TestMemoryTargetSource_ExecTargets(t *testing.T) {
	ctx := context.Background()
	remoteFP := devicefp.Carrier("sha256:remote")
	backends := map[int64]*agent_backend_entity.AgentBackend{
		1: {ID: 1, Name: "Claude Code", Type: string(agent_backend_entity.TypeClaudeCode)},
		2: {ID: 2, Name: "Claude Code", Type: string(agent_backend_entity.TypeClaudeCode), DeviceFingerprint: remoteFP},
		3: {ID: 3, Name: "Codex", Type: string(agent_backend_entity.TypeCodex), DeviceFingerprint: remoteFP},
	}
	src := &memoryTargetSource{
		resolve: func(context.Context, int64) ([]*agent_entity.AgentExecTarget, error) {
			return []*agent_entity.AgentExecTarget{{AgentBackendID: 1}, {AgentBackendID: 2}, {AgentBackendID: 3}, {AgentBackendID: 99}}, nil
		},
		backends: func(context.Context, []int64) (map[int64]*agent_backend_entity.AgentBackend, error) {
			return backends, nil
		},
		caps: func(_ context.Context, be *agent_backend_entity.AgentBackend) (capability.Capabilities, bool, error) {
			switch be.ID {
			case 1:
				return memoryCaps(), true, nil
			case 2:
				return capability.Capabilities{}, true, nil // 设备在线、报了能力、没有记忆
			}
			return capability.Capabilities{}, false, nil // 3 号设备没连着：未知
		},
		deviceName:         func(context.Context, *agent_backend_entity.AgentBackend) string { return "机房 2 号机" },
		typeSupportsMemory: func(bt string) bool { return bt == string(agent_backend_entity.TypeClaudeCode) },
	}

	got, err := src.ExecTargets(ctx, 5)
	require.NoError(t, err)
	require.Len(t, got, 2, "未知能力的远端档与已删除的后端不列出")
	assert.Equal(t, int64(1), got[0].BackendID)
	assert.False(t, got[0].Remote)
	assert.Empty(t, got[0].DeviceName)
	assert.True(t, got[0].Caps.Has(capability.CapMemory))
	assert.Equal(t, int64(2), got[1].BackendID)
	assert.True(t, got[1].Remote)
	assert.Equal(t, "机房 2 号机", got[1].DeviceName)
	assert.True(t, got[1].TypeSupportsMemory)
}

// 注入给 memory_svc 的判档就是 chat_svc.MemoryTier，不另写一套规则。
func TestMemoryTierFunc_DelegatesToChatTier(t *testing.T) {
	assert.Equal(t, memory_svc.TierFull, memoryTier(memoryCaps(), "claudecode"))
	assert.Equal(t, memory_svc.TierSummaries, memoryTier(capability.Capabilities{}, string(agent_backend_entity.TypeBuiltin)))
	assert.Equal(t, memory_svc.TierNone, memoryTier(capability.Capabilities{}, "hermes"))
	assert.EqualValues(t, chat_svc.MemoryLevelFull, memory_svc.TierFull)
	assert.EqualValues(t, chat_svc.MemoryLevelSummaries, memory_svc.TierSummaries)
	assert.EqualValues(t, chat_svc.MemoryLevelNone, memory_svc.TierNone)
}
