package chat_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// TestListAgents_PinnedDerivation: ChatAgentItem.Pinned = a.Pinned（DB 列直读）。
// 系统 agent 不再被 IsSystem() 强制浮顶（R: ceo-unpin）：置顶与否完全由 DB 的
// pinned 列承载，系统 agent 同样可以置顶或取消。用户置顶的普通 agent 置顶；
// 未置顶的普通 agent 不置顶。
func TestListAgents_PinnedDerivation(t *testing.T) {
	m := setupChatTest(t)
	ctx := context.Background()

	m.agent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
		{ID: 1, Name: "SysUnpinned", SystemBadge: agent_entity.SystemBadgeDefault, AgentBackendID: 9, Pinned: false, Status: consts.ACTIVE},
		{ID: 2, Name: "PinnedUser", AgentBackendID: 9, Pinned: true, Status: consts.ACTIVE},
		{ID: 3, Name: "Plain", AgentBackendID: 9, Pinned: false, Status: consts.ACTIVE},
		{ID: 4, Name: "SysPinned", SystemBadge: agent_entity.SystemBadgeDefault, AgentBackendID: 9, Pinned: true, Status: consts.ACTIVE},
	}, nil)
	m.backend.EXPECT().BatchFind(ctx, []int64{9}).Return(map[int64]*agent_backend_entity.AgentBackend{
		9: {ID: 9, Type: string(agent_backend_entity.TypeBuiltin), Status: consts.ACTIVE},
	}, nil)
	m.provider.EXPECT().BatchFindByKey(ctx, []string{}).Return(map[string]*llm_provider_entity.LLMProvider{}, nil)
	ids := []int64{1, 2, 3, 4}
	m.session.EXPECT().CountRunningByAgents(ctx, ids).Return(map[int64]int{}, nil)
	m.session.EXPECT().CountByAgents(ctx, ids).Return(map[int64]int64{}, nil)
	m.session.EXPECT().ListIDsByAgents(ctx, ids).Return(map[int64][]int64{}, nil)
	for _, id := range ids {
		m.session.EXPECT().ListByAgent(ctx, id, 5).Return(nil, nil)
		m.session.EXPECT().ListAttentionByAgent(ctx, id, 20).Return(nil, nil)
	}

	resp, err := m.svc.ListAgents(ctx, &chat_svc.ListAgentsRequest{})
	assert.NoError(t, err)
	byID := map[int64]bool{}
	for _, a := range resp.Agents {
		byID[a.ID] = a.Pinned
	}
	assert.False(t, byID[1], "system agent with DB pinned=false is unpinned (unpin respected)")
	assert.True(t, byID[2], "user-pinned agent is pinned")
	assert.False(t, byID[3], "plain agent not pinned")
	assert.True(t, byID[4], "system agent default pinned via DB pinned=true")
}
