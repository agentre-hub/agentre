package agent_backend_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
)

// setupReclaimTest 起 agent_backend_repo / chat_repo 的仓储 mock，注册到各自的
// 包级单例，返回 ctx / mock / svc。决策 24 的回收与巡检需要跨两个领域的引用信息
// (chat_sessions.exec_agent_backend_id、agent_exec_targets.agent_backend_id)。
func setupReclaimTest(t *testing.T) (
	context.Context,
	*mock_agent_backend_repo.MockAgentBackendRepo,
	*mock_chat_repo.MockSessionRepo,
	*agentBackendSvc,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	backendMock := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	sessionMock := mock_chat_repo.NewMockSessionRepo(ctrl)
	agent_backend_repo.RegisterAgentBackend(backendMock)
	chat_repo.RegisterSession(sessionMock)

	svc := &agentBackendSvc{
		now:    func() int64 { return 100_000_000_000_000 }, // 固定一个够大的"现在"，cutoff 计算不越界
		probes: map[string]context.CancelFunc{},
	}
	return context.Background(), backendMock, sessionMock, svc
}

// TestReclaimTombstonedBackends_ReclaimsUnreferencedPastRetention 回归决策 24:
// 墓碑 AND 无任何会话/执行目标引用 AND 超过保留期 → 被物理删除。
func TestReclaimTombstonedBackends_ReclaimsUnreferencedPastRetention(t *testing.T) {
	ctx, backendMock, sessionMock, svc := setupReclaimTest(t)

	backendMock.EXPECT().ListTombstonesOlderThan(gomock.Any(), gomock.Any()).
		Return([]*agent_backend_entity.AgentBackend{{ID: 1}, {ID: 2}}, nil)
	sessionMock.EXPECT().ListExecAgentBackendRefs(gomock.Any()).
		Return([]chat_repo.SessionBackendRef{{SessionID: 9, AgentBackendID: 2}}, nil)
	backendMock.EXPECT().ListExecTargetBackendRefs(gomock.Any()).
		Return([]agent_backend_repo.ExecTargetBackendRef{}, nil)
	backendMock.EXPECT().PurgeTombstones(gomock.Any(), []int64{1}).
		Return(int64(1), nil)

	resp, err := svc.ReclaimTombstonedBackends(ctx, &ReclaimTombstonedBackendsRequest{})
	require.NoError(t, err)
	assert.Equal(t, []int64{1}, resp.ReclaimedIDs, "无引用且超过保留期的墓碑应被回收")
	assert.Equal(t, []int64{2}, resp.KeptReferencedIDs, "仍被会话引用的墓碑不该被回收")
}

// TestReclaimTombstonedBackends_KeepsExecTargetReferenced 回归决策 24:执行目标
// (而非会话)引用同样能保住一条墓碑。
func TestReclaimTombstonedBackends_KeepsExecTargetReferenced(t *testing.T) {
	ctx, backendMock, sessionMock, svc := setupReclaimTest(t)

	backendMock.EXPECT().ListTombstonesOlderThan(gomock.Any(), gomock.Any()).
		Return([]*agent_backend_entity.AgentBackend{{ID: 7}}, nil)
	sessionMock.EXPECT().ListExecAgentBackendRefs(gomock.Any()).
		Return(nil, nil)
	backendMock.EXPECT().ListExecTargetBackendRefs(gomock.Any()).
		Return([]agent_backend_repo.ExecTargetBackendRef{{ExecTargetID: 3, AgentBackendID: 7}}, nil)

	resp, err := svc.ReclaimTombstonedBackends(ctx, &ReclaimTombstonedBackendsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.ReclaimedIDs)
	assert.Equal(t, []int64{7}, resp.KeptReferencedIDs)
}

// TestReclaimTombstonedBackends_NoTombstonesIsNoop 没有墓碑时不发起引用查询或
// 删除。
func TestReclaimTombstonedBackends_NoTombstonesIsNoop(t *testing.T) {
	ctx, backendMock, _, svc := setupReclaimTest(t)

	backendMock.EXPECT().ListTombstonesOlderThan(gomock.Any(), gomock.Any()).
		Return(nil, nil)

	resp, err := svc.ReclaimTombstonedBackends(ctx, &ReclaimTombstonedBackendsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.ReclaimedIDs)
	assert.Empty(t, resp.KeptReferencedIDs)
}

// TestSurveyDanglingBackendReferences_ReportsWithoutRewriting 回归决策 24:巡检
// 报出指向非 ACTIVE 后端的会话/执行目标引用,只报出、不改写(没有任何 Update/Delete
// 调用的 mock 期望 —— gomock 严格模式下,一旦实现顺手改写就会因未预期调用报错)。
func TestSurveyDanglingBackendReferences_ReportsWithoutRewriting(t *testing.T) {
	ctx, backendMock, sessionMock, svc := setupReclaimTest(t)

	backendMock.EXPECT().List(gomock.Any()).
		Return([]*agent_backend_entity.AgentBackend{{ID: 5}}, nil)
	sessionMock.EXPECT().ListExecAgentBackendRefs(gomock.Any()).
		Return([]chat_repo.SessionBackendRef{
			{SessionID: 10, AgentBackendID: 5},  // 指向 ACTIVE 后端,不算悬空
			{SessionID: 11, AgentBackendID: 99}, // 悬空:99 不在 ACTIVE 集合里
		}, nil)
	backendMock.EXPECT().ListExecTargetBackendRefs(gomock.Any()).
		Return([]agent_backend_repo.ExecTargetBackendRef{
			{ExecTargetID: 20, AgentBackendID: 98}, // 悬空
		}, nil)

	resp, err := svc.SurveyDanglingBackendReferences(ctx, &SurveyDanglingBackendReferencesRequest{})
	require.NoError(t, err)
	require.Len(t, resp.Dangling, 2)
	assert.Contains(t, resp.Dangling, DanglingBackendReference{Kind: "session", RefID: 11, BackendID: 99})
	assert.Contains(t, resp.Dangling, DanglingBackendReference{Kind: "exec_target", RefID: 20, BackendID: 98})
}
