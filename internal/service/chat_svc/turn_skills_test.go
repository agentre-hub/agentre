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
	require.Nil(t, enabledPluginsForTurn(context.Background(), a, "codex", true))

	// 注册后 + 支持 CapSkills → 注入 provider 结果;provider 收到的是**这一轮引擎配置的运行时
	// 类型**(技能授权按运行时校验),不是某一行后端的 id —— 引擎配置视图没有那个 id。
	RegisterEnabledPluginsProvider(func(_ context.Context, ag *agent_entity.Agent, runtime string) map[string]bool {
		require.Equal(t, int64(5), ag.ID)
		require.Equal(t, "codex", runtime)
		return map[string]bool{"x@m": true}
	})
	require.Equal(t, map[string]bool{"x@m": true}, enabledPluginsForTurn(context.Background(), a, "codex", true))

	// runner 不支持 CapSkills → 不注入(软降级)
	require.Nil(t, enabledPluginsForTurn(context.Background(), a, "codex", false))
	RegisterEnabledPluginsProvider(nil) // 清理,防测试间串台
}
