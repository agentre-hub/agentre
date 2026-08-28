package chat_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

func TestAppendTurnMCP(t *testing.T) {
	a := &agent_entity.Agent{ID: 5}
	base := []agentruntime.MCPServerSpec{{Name: "group"}}

	// provider 未注册 → 原样
	ResetTurnMCPProviders()
	defer ResetTurnMCPProviders()
	require.Equal(t, base, appendTurnMCP(context.Background(), base, a, 42, true))

	// 注册后 + 支持 CapMCPTools → 追加
	RegisterTurnMCPProvider(func(_ context.Context, ag *agent_entity.Agent, sid int64, _ int64) []agentruntime.MCPServerSpec {
		require.Equal(t, int64(5), ag.ID)
		require.Equal(t, int64(42), sid)
		return []agentruntime.MCPServerSpec{{Name: "org"}}
	})
	got := appendTurnMCP(context.Background(), base, a, 42, true)
	require.Len(t, got, 2)
	require.Equal(t, "org", got[1].Name)

	// runner 不支持 CapMCPTools → 不追加(软降级)
	require.Equal(t, base, appendTurnMCP(context.Background(), base, a, 42, false))
}

func TestAppendTurnMCP_MultiProvider(t *testing.T) {
	ResetTurnMCPProviders()
	defer ResetTurnMCPProviders()
	var gotGroupID int64
	RegisterTurnMCPProvider(func(_ context.Context, _ *agent_entity.Agent, _ int64, groupID int64) []agentruntime.MCPServerSpec {
		gotGroupID = groupID
		return []agentruntime.MCPServerSpec{{Name: "org"}}
	})
	RegisterTurnMCPProvider(func(_ context.Context, _ *agent_entity.Agent, _ int64, _ int64) []agentruntime.MCPServerSpec {
		return []agentruntime.MCPServerSpec{{Name: "group"}}
	})
	base := []agentruntime.MCPServerSpec{{Name: "base"}}
	out := appendTurnMCP(context.Background(), base, &agent_entity.Agent{}, 9, true)
	require.Len(t, out, 3)
	require.Equal(t, "base", out[0].Name)
	require.Equal(t, "org", out[1].Name)
	require.Equal(t, "group", out[2].Name)
	require.Equal(t, int64(0), gotGroupID, "appendTurnMCP 恒传 groupID=0(历史残留形参)")
	require.Len(t, appendTurnMCP(context.Background(), base, &agent_entity.Agent{}, 9, false), 1, "capOK=false must not append")
}
