package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/memory_svc"
)

func memoryCaps() capability.Capabilities {
	return capability.Capabilities{Set: map[capability.Capability]bool{capability.CapMemory: true}}
}

// 记忆页头部：判档看成员生效的引擎配置的运行时能力，不再逐档看执行目标。
func TestMemoryTargetSource_ExecTargets(t *testing.T) {
	ctx := context.Background()
	profiles := map[int64]*agent_backend_entity.EngineProfile{
		5: {ID: 21, Name: "Claude", Type: string(agent_backend_entity.TypeClaudeCode)},
		6: {ID: 22, Name: "Hermes", Type: string(agent_backend_entity.TypeHermes)},
		7: {ID: 23, Name: "Mystery", Type: "mystery"},
	}
	src := &memoryTargetSource{
		effective: func(_ context.Context, agentID int64) (*agent_backend_entity.EngineProfile, error) {
			return profiles[agentID], nil
		},
		caps: func(runtimeType string) (capability.Capabilities, bool) {
			switch runtimeType {
			case string(agent_backend_entity.TypeClaudeCode):
				return memoryCaps(), true
			case string(agent_backend_entity.TypeHermes):
				return capability.Capabilities{}, true
			}
			return capability.Capabilities{}, false
		},
	}

	got, err := src.ExecTargets(ctx, 5)
	require.NoError(t, err)
	require.Len(t, got, 1, "一名成员只有一份生效的引擎配置")
	assert.Equal(t, memory_svc.ExecTarget{
		BackendID: 21, BackendName: "Claude", BackendType: "claudecode", Caps: memoryCaps(), TypeSupportsMemory: true,
	}, got[0])

	got, err = src.ExecTargets(ctx, 6)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Caps.Has(capability.CapMemory))
	assert.False(t, got[0].TypeSupportsMemory)

	got, err = src.ExecTargets(ctx, 7)
	require.NoError(t, err)
	assert.Empty(t, got, "能力未知的运行时不下结论")

	got, err = src.ExecTargets(ctx, 8)
	require.NoError(t, err)
	assert.Empty(t, got, "没有生效的引擎配置：什么都不列")
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
