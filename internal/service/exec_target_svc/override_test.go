package exec_target_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
)

// 本文件锁住 R15a「手动指定执行目标」的校验半边（ValidateExecTargetOverride）：
// 指定的档必须在 Agent 的执行目标列表里且此刻可用，拒绝指定一个不可用的档。
// 写回半边不新增写口——chat_svc.send() 把校验通过的 agentBackendID 塞进一个只填了
// ExecAgentBackendID 的探针 session 喂给 resolveAgentBackend（见 chat_svc 里
// end-to-end 的 TestSend_NewSessionWithExecTargetOverride_* 系列），resolveAgentBackend
// 命中"已钉住"分支的行为已经被 chat_svc 的 exec_target_pin_test.go 锁住，这里不重复。

type overrideMocks struct {
	execTarget         *mock_agent_repo.MockAgentExecTargetRepo
	execTargetOverride *mock_agent_repo.MockAgentExecTargetOverrideRepo
	backend            *mock_agent_backend_repo.MockAgentBackendRepo
}

func setupOverrideTest(t *testing.T) (context.Context, *overrideMocks, *execTargetSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	m := &overrideMocks{
		execTarget:         mock_agent_repo.NewMockAgentExecTargetRepo(ctrl),
		execTargetOverride: mock_agent_repo.NewMockAgentExecTargetOverrideRepo(ctrl),
		backend:            mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl),
	}

	prevExecTarget := agent_repo.AgentExecTarget()
	prevOverride := agent_repo.AgentExecTargetOverride()
	prevBackend := agent_backend_repo.AgentBackend()
	agent_repo.RegisterAgentExecTarget(m.execTarget)
	agent_repo.RegisterAgentExecTargetOverride(m.execTargetOverride)
	agent_backend_repo.RegisterAgentBackend(m.backend)
	// R14 顺序解析的宽松桩：这批用例不关心本端覆盖（默认无覆盖）。
	m.execTargetOverride.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	t.Cleanup(func() {
		agent_repo.RegisterAgentExecTarget(prevExecTarget)
		agent_repo.RegisterAgentExecTargetOverride(prevOverride)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
	})

	return context.Background(), m, NewExecTarget(nil).(*execTargetSvc)
}

// localClaudeCode 是一个"CLI 自身 login 状态"的本地 claudecode 后端：LLMProviderKey
// 为空短路判可用（既有行为),不需要额外 mock provider/gateway。
func localClaudeCode(id int64) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{ID: id, Type: string(agent_backend_entity.TypeClaudeCode)}
}

func TestValidateExecTargetOverride_GivenTargetInListAndAvailable_ThenNoError(t *testing.T) {
	ctx, m, svc := setupOverrideTest(t)

	m.execTarget.EXPECT().ListByAgent(ctx, int64(601)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 601, AgentBackendID: 51, SortOrder: 0},
		{ID: 2, AgentID: 601, AgentBackendID: 52, SortOrder: 1},
	}, nil)
	// 手动指定排第二的 52（非默认第一档），必须能通过校验——不是只能选排最前的那个。
	m.backend.EXPECT().Find(ctx, int64(52)).Return(localClaudeCode(52), nil)

	err := svc.ValidateExecTargetOverride(ctx, 601, 0, 52)
	require.NoError(t, err)
}

func TestValidateExecTargetOverride_GivenTargetNotInAgentsList_ThenRejects(t *testing.T) {
	ctx, m, svc := setupOverrideTest(t)

	m.execTarget.EXPECT().ListByAgent(ctx, int64(602)).Return([]*agent_entity.AgentExecTarget{
		{ID: 3, AgentID: 602, AgentBackendID: 51, SortOrder: 0},
	}, nil)

	// 999 压根不在这个 Agent 的执行目标列表里：必须拒绝，不查 backend。
	err := svc.ValidateExecTargetOverride(ctx, 602, 0, 999)
	require.Error(t, err)
}

func TestValidateExecTargetOverride_GivenTargetCurrentlyUnavailable_ThenRejects(t *testing.T) {
	ctx, m, svc := setupOverrideTest(t)

	m.execTarget.EXPECT().ListByAgent(ctx, int64(603)).Return([]*agent_entity.AgentExecTarget{
		{ID: 4, AgentID: 603, AgentBackendID: 61, SortOrder: 0},
	}, nil)
	m.backend.EXPECT().Find(ctx, int64(61)).Return(&agent_backend_entity.AgentBackend{
		ID: 61, Type: "totally-unknown-type",
	}, nil)

	err := svc.ValidateExecTargetOverride(ctx, 603, 0, 61)
	require.Error(t, err, "当前不可用的档必须被拒绝")
}
