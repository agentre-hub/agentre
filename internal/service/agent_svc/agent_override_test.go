package agent_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
)

// setupSvcWithOverride 与 setupSvc 相同，但额外注册并交回本端顺序覆盖仓储的 mock
// （R14：Update 的 orderOverride 写路径只碰这张纯本地表）。
type svcWithOverride struct {
	agentMock    *mock_agent_repo.MockAgentRepo
	backendMock  *mock_agent_backend_repo.MockAgentBackendRepo
	overrideMock *mock_agent_repo.MockAgentExecTargetOverrideRepo
	targetMock   *mock_agent_repo.MockAgentExecTargetRepo
	svc          *agentSvc
}

func setupSvcWithOverride(t *testing.T) (context.Context, *svcWithOverride) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	agentMock := mock_agent_repo.NewMockAgentRepo(ctrl)
	backendMock := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	overrideMock := mock_agent_repo.NewMockAgentExecTargetOverrideRepo(ctrl)
	targetMock := mock_agent_repo.NewMockAgentExecTargetRepo(ctrl)

	prevAgent := agent_repo.Agent()
	prevBackend := agent_backend_repo.AgentBackend()
	prevOverride := agent_repo.AgentExecTargetOverride()
	prevTarget := agent_repo.AgentExecTarget()

	agent_repo.RegisterAgent(agentMock)
	agent_backend_repo.RegisterAgentBackend(backendMock)
	agent_repo.RegisterAgentExecTargetOverride(overrideMock)
	agent_repo.RegisterAgentExecTarget(targetMock)

	t.Cleanup(func() {
		agent_repo.RegisterAgent(prevAgent)
		agent_backend_repo.RegisterAgentBackend(prevBackend)
		agent_repo.RegisterAgentExecTargetOverride(prevOverride)
		agent_repo.RegisterAgentExecTarget(prevTarget)
	})
	return context.Background(), &svcWithOverride{
		agentMock: agentMock, backendMock: backendMock, overrideMock: overrideMock,
		targetMock: targetMock, svc: &agentSvc{now: func() int64 { return 1700000000 }},
	}
}

func existingAgent(id int64) *agent_entity.Agent {
	return &agent_entity.Agent{
		ID: id, Name: "Eva", AvatarColor: "agent-2", DepartmentID: 2,
		AgentBackendID: 5, Status: consts.ACTIVE, PromptJSON: "[]", SkillsJSON: "[]",
	}
}

// ── R14 / R16：orderOverride 写本端覆盖，不碰账号默认、不同步 ───────────────

// TestUpdate_GivenOrderOverride_ThenWritesOverrideOnlyNotAccountDefault 是 R14 的
// 核心守卫：带 orderOverride 的 Update 只写本端覆盖，绝不写账号默认执行目标列表
// （不给 UpdateWithTargets 设期望，一旦触发 gomock 判败）——「任一端调整只改这一端」。
func TestUpdate_GivenOrderOverride_ThenWritesOverrideOnlyNotAccountDefault(t *testing.T) {
	convey.Convey("Given 一个已有 Agent 与一段本端覆盖顺序", t, func() {
		ctx, m := setupSvcWithOverride(t)
		m.agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(existingAgent(42), nil)
		// 写回响应需要当前执行目标快照（账号默认，未变）。
		m.targetMock.EXPECT().ListByAgent(gomock.Any(), int64(42)).Return([]*agent_entity.AgentExecTarget{
			{ID: 1, AgentID: 42, AgentBackendID: 5, SortOrder: 0},
		}, nil)
		m.overrideMock.EXPECT().Save(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, o *agent_entity.AgentExecTargetOverride) error {
				assert.Equal(t, int64(42), o.AgentID)
				assert.Equal(t, []int64{5}, o.GetOrder())
				return nil
			})

		convey.Convey("When 带 orderOverride 调 Update", func() {
			resp, err := m.svc.Update(ctx, &UpdateAgentRequest{
				ID: 42, Name: "Eva", ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}},
				OrderOverride: []int64{5},
			})

			convey.Convey("Then 只写了覆盖，返回当前项，账号默认没被动", func() {
				require.NoError(t, err)
				require.NotNil(t, resp)
				assert.Equal(t, int64(42), resp.Item.ID)
			})
		})
	})
}

// TestUpdate_GivenEmptyOrderOverride_ThenClearsOverride 是「恢复为账号默认顺序」的
// 落点：空数组 = 清掉本端覆盖（Delete），账号默认原样不动。
func TestUpdate_GivenEmptyOrderOverride_ThenClearsOverride(t *testing.T) {
	convey.Convey("Given 一个已有 Agent 与空覆盖（= 恢复默认）", t, func() {
		ctx, m := setupSvcWithOverride(t)
		m.agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(existingAgent(42), nil)
		m.targetMock.EXPECT().ListByAgent(gomock.Any(), int64(42)).Return(nil, nil)
		m.overrideMock.EXPECT().Delete(gomock.Any(), int64(42)).Return(nil)

		convey.Convey("When 带空 orderOverride 调 Update", func() {
			_, err := m.svc.Update(ctx, &UpdateAgentRequest{
				ID: 42, Name: "Eva", ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}},
				OrderOverride: []int64{},
			})

			convey.Convey("Then 清掉覆盖，无错误", func() {
				require.NoError(t, err)
			})
		})
	})
}

// TestUpdate_WithoutOrderOverride_KeepsExistingAccountWrite 反向守卫：不带
// orderOverride 的 Update 仍是账号默认写入（既有行为不回退）。
func TestUpdate_WithoutOrderOverride_KeepsExistingAccountWrite(t *testing.T) {
	convey.Convey("Given 一个已有 Agent", t, func() {
		ctx, m := setupSvcWithOverride(t)
		m.agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(existingAgent(42), nil)
		m.backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
		m.targetMock.EXPECT().ListByAgent(gomock.Any(), int64(42)).Return(nil, nil).AnyTimes()
		m.agentMock.EXPECT().UpdateWithTargets(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(nil)

		convey.Convey("When 不带 orderOverride 调 Update", func() {
			resp, err := m.svc.Update(ctx, &UpdateAgentRequest{
				ID: 42, Name: "Eva", ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}},
			})

			convey.Convey("Then 走既有账号默认写入", func() {
				require.NoError(t, err)
				require.NotNil(t, resp)
			})
		})
	})
}
