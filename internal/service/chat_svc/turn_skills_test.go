package chat_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

func TestEnabledPluginsForTurn(t *testing.T) {
	a := &agent_entity.Agent{ID: 5}

	// provider 未注册 → nil
	RegisterEnabledPluginsProvider(nil)
	require.Nil(t, enabledPluginsForTurn(context.Background(), a, 22, true))

	// 注册后 + 支持 CapSkills → 注入 provider 结果;provider 必须收到**这一轮落到的
	// 那一档**的 backend id(R15b/R15e),不是 Agent 的主档。
	RegisterEnabledPluginsProvider(func(_ context.Context, ag *agent_entity.Agent, agentBackendID int64) map[string]bool {
		require.Equal(t, int64(5), ag.ID)
		require.Equal(t, int64(22), agentBackendID)
		return map[string]bool{"x@m": true}
	})
	require.Equal(t, map[string]bool{"x@m": true}, enabledPluginsForTurn(context.Background(), a, 22, true))

	// runner 不支持 CapSkills → 不注入(软降级)
	require.Nil(t, enabledPluginsForTurn(context.Background(), a, 22, false))
	RegisterEnabledPluginsProvider(nil) // 清理,防测试间串台
}
