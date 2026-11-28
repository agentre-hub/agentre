package agent_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
)

func setupEngineSvc(t *testing.T) (
	context.Context,
	*mock_agent_repo.MockAgentRepo,
	*mock_department_repo.MockDepartmentRepo,
	*mock_agent_backend_repo.MockEngineProfileRepo,
	*agentSvc,
) {
	t.Helper()
	ctx, agentMock, deptMock, _, svc := setupSvc(t)
	profiles := mock_agent_backend_repo.NewMockEngineProfileRepo(gomock.NewController(t))
	agent_backend_repo.RegisterEngineProfile(profiles)
	return ctx, agentMock, deptMock, profiles, svc
}

func requireAgentCode(t *testing.T, err error, want int) {
	t.Helper()
	var httpErr *httputils.Error
	require.ErrorAs(t, err, &httpErr)
	assert.Equal(t, want, httpErr.Code)
}

func engineProfile(id int64, name, typ string) *agent_backend_entity.EngineProfile {
	return &agent_backend_entity.EngineProfile{ID: id, Name: name, Type: typ, Status: consts.ACTIVE}
}

func member(id, deptID, profileID int64) *agent_entity.Agent {
	return &agent_entity.Agent{ID: id, Name: "Eva", DepartmentID: deptID, EngineProfileID: profileID, Status: consts.ACTIVE, PromptJSON: "[]"}
}

func TestResolveMemberEngine(t *testing.T) {
	convey.Convey("成员的生效引擎", t, func() {
		ctx, agentMock, deptMock, profiles, svc := setupEngineSvc(t)

		convey.Convey("Given the member picks its own profile When resolved Then it comes from the member", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(member(42, 2, 5), nil)
			profiles.EXPECT().Find(gomock.Any(), int64(5)).Return(engineProfile(5, "Codex", "codex"), nil)

			resp, err := svc.ResolveMemberEngine(ctx, &ResolveMemberEngineRequest{AgentID: 42})
			require.NoError(t, err)
			require.NotNil(t, resp.Effective)
			assert.Equal(t, int64(5), resp.Effective.EngineProfileID)
			assert.Equal(t, department_svc.EngineSourceMember, resp.Effective.Source)
		})

		convey.Convey("Given the member follows When resolved Then its department chain decides and the department is named", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(member(42, 2, 0), nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(&department_entity.Department{ID: 2, Name: "工程部", DefaultEngineProfileID: 8, Status: consts.ACTIVE}, nil)
			profiles.EXPECT().Find(gomock.Any(), int64(8)).Return(engineProfile(8, "Claude Code", "claudecode"), nil)

			resp, err := svc.ResolveMemberEngine(ctx, &ResolveMemberEngineRequest{AgentID: 42})
			require.NoError(t, err)
			assert.Equal(t, department_svc.EngineSourceDepartment, resp.Effective.Source)
			assert.Equal(t, "工程部", resp.Effective.SourceDepartmentName)
		})

		convey.Convey("Given the CEO has no department and no profile When resolved Then the account default is used", func() {
			ceo := member(1, 0, 0)
			ceo.SystemBadge = agent_entity.SystemBadgeDefault
			agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(ceo, nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(engineProfile(3, "Default", "claudecode"), nil)

			resp, err := svc.ResolveMemberEngine(ctx, &ResolveMemberEngineRequest{AgentID: 1})
			require.NoError(t, err)
			assert.Equal(t, department_svc.EngineSourceAccountDefault, resp.Effective.Source)
		})

		convey.Convey("Given the member's own profile was deleted When resolved Then it falls back to the next level", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(member(42, 2, 5), nil)
			profiles.EXPECT().Find(gomock.Any(), int64(5)).Return(nil, nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(&department_entity.Department{ID: 2, Status: consts.ACTIVE}, nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(engineProfile(3, "Default", "claudecode"), nil)

			resp, err := svc.ResolveMemberEngine(ctx, &ResolveMemberEngineRequest{AgentID: 42})
			require.NoError(t, err)
			assert.Equal(t, int64(3), resp.Effective.EngineProfileID)
		})

		convey.Convey("Given a missing member When resolved Then it reports not found", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(nil, nil)
			_, err := svc.ResolveMemberEngine(ctx, &ResolveMemberEngineRequest{AgentID: 42})
			requireAgentCode(t, err, code.AgentNotFound)
		})
	})
}

func TestMemberEngineSettings(t *testing.T) {
	convey.Convey("成员的执行设置", t, func() {
		ctx, agentMock, _, profiles, svc := setupEngineSvc(t)
		ceo := func() *agent_entity.Agent {
			a := member(1, 0, 0)
			a.SystemBadge = agent_entity.SystemBadgeDefault
			return a
		}

		convey.Convey("Given grants made under claudecode When the member switches to a codex profile Then the reference is saved and the old grants are reported invalid, not dropped", func() {
			a := ceo()
			a.SetSkillGrants("claudecode", []agent_entity.AgentSkillItem{{ID: "superpowers@claude-plugins-official", Enabled: true}})
			agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(a, nil)
			profiles.EXPECT().Find(gomock.Any(), int64(5)).Return(engineProfile(5, "Codex", "codex"), nil).AnyTimes()
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(engineProfile(3, "Default", "claudecode"), nil)
			var saved *agent_entity.Agent
			agentMock.EXPECT().UpdateEngineSettings(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
				saved = a
				return nil
			})

			view, err := svc.SetMemberEngine(ctx, &SetMemberEngineRequest{AgentID: 1, EngineProfileID: 5})
			require.NoError(t, err)
			assert.Equal(t, int64(5), saved.EngineProfileID)
			assert.Equal(t, "claudecode", saved.SkillGrantsRuntime, "switching the engine must not rewrite the grants")
			assert.Empty(t, view.SkillGrants)
			assert.Equal(t, []department_svc.AgentSkillDTO{{ID: "superpowers@claude-plugins-official", Enabled: true}}, view.InvalidSkillGrants)
			assert.Equal(t, department_svc.EngineSourceMember, view.Effective.Source)
			assert.Equal(t, department_svc.EngineSourceAccountDefault, view.Inherited.Source)
		})

		convey.Convey("Given a deleted profile When picked Then it is rejected and nothing is written", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(ceo(), nil)
			profiles.EXPECT().Find(gomock.Any(), int64(5)).Return(nil, nil)

			_, err := svc.SetMemberEngine(ctx, &SetMemberEngineRequest{AgentID: 1, EngineProfileID: 5})
			requireAgentCode(t, err, code.EngineProfileNotFound)
		})

		convey.Convey("Given a labels constraint When set Then it is normalized and stored", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(ceo(), nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(engineProfile(3, "Default", "claudecode"), nil).AnyTimes()
			var saved *agent_entity.Agent
			agentMock.EXPECT().UpdateEngineSettings(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
				saved = a
				return nil
			})

			view, err := svc.SetMemberExecConstraint(ctx, &SetMemberExecConstraintRequest{
				AgentID: 1, Constraint: ExecConstraintDTO{Mode: "labels", Labels: []string{" gpu ", "gpu"}},
			})
			require.NoError(t, err)
			assert.JSONEq(t, `{"mode":"labels","labels":["gpu"]}`, saved.ExecConstraintJSON)
			assert.Equal(t, ExecConstraintDTO{Mode: "labels", Labels: []string{"gpu"}, Devices: []string{}}, view.Constraint)
		})

		convey.Convey("Given a devices constraint with no device When set Then it is rejected and nothing is written", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(ceo(), nil)

			_, err := svc.SetMemberExecConstraint(ctx, &SetMemberExecConstraintRequest{AgentID: 1, Constraint: ExecConstraintDTO{Mode: "devices"}})
			requireAgentCode(t, err, code.EngineExecConstraintInvalid)
		})

		convey.Convey("Given an effective codex profile When grants are set Then they are stamped with codex and read back as valid", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(ceo(), nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(engineProfile(3, "Default", "codex"), nil).AnyTimes()
			var saved *agent_entity.Agent
			agentMock.EXPECT().UpdateEngineSettings(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
				saved = a
				return nil
			})

			view, err := svc.SetMemberSkillGrants(ctx, &SetMemberSkillGrantsRequest{
				AgentID: 1, Skills: []department_svc.AgentSkillDTO{{ID: "pack@codex", Enabled: false}},
			})
			require.NoError(t, err)
			assert.Equal(t, "codex", saved.SkillGrantsRuntime)
			assert.Equal(t, []department_svc.AgentSkillDTO{{ID: "pack@codex", Enabled: false}}, view.SkillGrants)
			assert.Empty(t, view.InvalidSkillGrants)
		})

		convey.Convey("Given no engine profile anywhere When grants are set Then they are rejected because there is no runtime to check them against", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(1)).Return(ceo(), nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(nil, nil)

			_, err := svc.SetMemberSkillGrants(ctx, &SetMemberSkillGrantsRequest{AgentID: 1, Skills: []department_svc.AgentSkillDTO{{ID: "x", Enabled: true}}})
			requireAgentCode(t, err, code.EngineProfileRequired)
		})
	})
}

// 成员不再因「没有执行目标」而无法保存：只要它（或继承到的）引擎配置存在即可保存。
func TestUpdateAgent_WithoutExecTargets(t *testing.T) {
	convey.Convey("不带执行目标保存成员", t, func() {
		ctx, agentMock, deptMock, profiles, svc := setupEngineSvc(t)

		convey.Convey("Given the member inherits an engine profile When saved with no exec targets Then it is saved with an empty target list", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(member(42, 2, 0), nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(&department_entity.Department{ID: 2, Status: consts.ACTIVE}, nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(engineProfile(3, "Default", "claudecode"), nil)
			var targets []*agent_entity.AgentExecTarget
			agentMock.EXPECT().UpdateWithTargets(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *agent_entity.Agent, ts []*agent_entity.AgentExecTarget) error {
					targets = ts
					return nil
				})

			_, err := svc.Update(ctx, &UpdateAgentRequest{ID: 42, Name: "Eva", AvatarColor: "agent-2"})
			require.NoError(t, err)
			assert.Empty(t, targets)
		})

		convey.Convey("Given no engine profile anywhere When saved with no exec targets Then it is refused", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(member(42, 2, 0), nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(&department_entity.Department{ID: 2, Status: consts.ACTIVE}, nil)
			profiles.EXPECT().FindAccountDefault(gomock.Any()).Return(nil, nil)

			_, err := svc.Update(ctx, &UpdateAgentRequest{ID: 42, Name: "Eva", AvatarColor: "agent-2"})
			requireAgentCode(t, err, code.EngineProfileRequired)
		})
	})
}
