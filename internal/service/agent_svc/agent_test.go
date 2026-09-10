package agent_svc

import (
	"context"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/service/department_svc"
)

func setupSvc(t *testing.T) (
	context.Context,
	*mock_agent_repo.MockAgentRepo,
	*mock_department_repo.MockDepartmentRepo,
	*mock_agent_backend_repo.MockAgentBackendRepo,
	*agentSvc,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	agentMock := mock_agent_repo.NewMockAgentRepo(ctrl)
	deptMock := mock_department_repo.NewMockDepartmentRepo(ctrl)
	backendMock := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	agent_repo.RegisterAgent(agentMock)
	department_repo.RegisterDepartment(deptMock)
	agent_backend_repo.RegisterAgentBackend(backendMock)
	// 执行目标行是 AgentItem 里 Skills / AgentBackendID 的真相来源（R15e）：写完
	// Agent 之后一律重读一次，因此每个用例都可能问到它。默认答空列表，需要断言的
	// 用例自己覆写。
	targetMock := mock_agent_repo.NewMockAgentExecTargetRepo(ctrl)
	targetMock.EXPECT().ListByAgent(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	agent_repo.RegisterAgentExecTarget(targetMock)
	return context.Background(), agentMock, deptMock, backendMock, &agentSvc{now: func() int64 { return 1700000000 }}
}

func activeDept(id int64) *department_entity.Department {
	return &department_entity.Department{ID: id, Status: consts.ACTIVE}
}

func activeBackend(id int64) *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{ID: id, Status: consts.ACTIVE, Type: "builtin"}
}

func TestCreateAgent(t *testing.T) {
	convey.Convey("创建 Agent", t, func() {
		ctx, agentMock, deptMock, backendMock, svc := setupSvc(t)

		convey.Convey("成功", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(activeDept(2), nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
			agentMock.EXPECT().FindByName(gomock.Any(), "Eva").Return(nil, nil)
			agentMock.EXPECT().NextSortOrder(gomock.Any(), int64(2)).Return(1, nil)
			var captured *agent_entity.Agent
			agentMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
				a.ID = 99
				captured = a
				return nil
			})
			resp, err := svc.Create(ctx, &CreateAgentRequest{
				Name: "Eva", AvatarColor: "agent-2", AvatarIcon: "sparkles",
				DepartmentID: 2, AgentBackendID: 5, Prompt: []string{"hi"},
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(99), resp.Item.ID)
			assert.Equal(t, "sparkles", captured.AvatarIcon)
			assert.Equal(t, "sparkles", resp.Item.AvatarIcon)
		})

		convey.Convey("挂到上级 Agent 成功", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(1)).
				Return(&agent_entity.Agent{ID: 1, Name: "CEO 助手", SystemBadge: "DEFAULT", Status: consts.ACTIVE}, nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
			agentMock.EXPECT().FindByName(gomock.Any(), "Eva").Return(nil, nil)
			agentMock.EXPECT().NextSortOrderByParent(gomock.Any(), int64(1)).Return(1, nil)
			agentMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
				a.ID = 99
				return nil
			})
			resp, err := svc.Create(ctx, &CreateAgentRequest{
				Name: "Eva", AvatarColor: "agent-2", ParentAgentID: 1, AgentBackendID: 5,
			})
			assert.NoError(t, err)
			assert.Equal(t, int64(1), resp.Item.ParentAgentID)
		})

		convey.Convey("部门不存在", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, nil)
			_, err := svc.Create(ctx, &CreateAgentRequest{
				Name: "Eva", AvatarColor: "agent-2", DepartmentID: 99, AgentBackendID: 5,
			})
			assert.Error(t, err)
		})

		convey.Convey("backend inactive", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(activeDept(2), nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).
				Return(&agent_backend_entity.AgentBackend{ID: 5, Status: consts.DELETE}, nil)
			_, err := svc.Create(ctx, &CreateAgentRequest{
				Name: "Eva", AvatarColor: "agent-2", DepartmentID: 2, AgentBackendID: 5,
			})
			assert.Error(t, err)
		})

		convey.Convey("重名拒绝", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(activeDept(2), nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
			agentMock.EXPECT().FindByName(gomock.Any(), "Eva").
				Return(&agent_entity.Agent{ID: 1, Name: "Eva"}, nil)
			_, err := svc.Create(ctx, &CreateAgentRequest{
				Name: "Eva", AvatarColor: "agent-2", DepartmentID: 2, AgentBackendID: 5,
			})
			assert.Error(t, err)
		})
	})
}

func TestUpdateAgent(t *testing.T) {
	convey.Convey("更新 Agent", t, func() {
		ctx, agentMock, _, backendMock, svc := setupSvc(t)

		convey.Convey("AvatarIcon round-trip", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{
					ID: 42, Name: "Eva", AvatarColor: "agent-2",
					DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE,
					PromptJSON: "[]", SkillsJSON: "[]",
				}, nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
			var captured *agent_entity.Agent
			agentMock.EXPECT().UpdateWithTargets(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, a *agent_entity.Agent, _ []*agent_entity.AgentExecTarget) error {
					captured = a
					return nil
				})
			resp, err := svc.Update(ctx, &UpdateAgentRequest{
				ID: 42, Name: "Eva", AvatarColor: "agent-2", AvatarIcon: "hammer",
				ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}},
			})
			assert.NoError(t, err)
			assert.Equal(t, "hammer", captured.AvatarIcon)
			assert.Equal(t, "hammer", resp.Item.AvatarIcon)
		})

		convey.Convey("AvatarIcon 留空 → 清空字段", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{
					ID: 42, Name: "Eva", AvatarColor: "agent-2", AvatarIcon: "hammer",
					DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE,
					PromptJSON: "[]", SkillsJSON: "[]",
				}, nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
			agentMock.EXPECT().UpdateWithTargets(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, a *agent_entity.Agent, _ []*agent_entity.AgentExecTarget) error {
					assert.Equal(t, "", a.AvatarIcon)
					return nil
				})
			_, err := svc.Update(ctx, &UpdateAgentRequest{
				ID: 42, Name: "Eva", AvatarColor: "agent-2", AvatarIcon: "",
				ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}},
			})
			assert.NoError(t, err)
		})

		convey.Convey("AvatarIcon 超长拒绝", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{
					ID: 42, Name: "Eva", AvatarColor: "agent-2",
					DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE,
					PromptJSON: "[]", SkillsJSON: "[]",
				}, nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
			_, err := svc.Update(ctx, &UpdateAgentRequest{
				ID: 42, Name: "Eva", AvatarColor: "agent-2",
				AvatarIcon:  strings.Repeat("x", 33),
				ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}},
			})
			assert.Error(t, err)
		})

		convey.Convey("空执行目标列表拒绝保存（R15：列表为空的 Agent 不能起会话）", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{
					ID: 42, Name: "Eva", AvatarColor: "agent-2",
					DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE,
					PromptJSON: "[]", SkillsJSON: "[]",
				}, nil)
			_, err := svc.Update(ctx, &UpdateAgentRequest{
				ID: 42, Name: "Eva", AvatarColor: "agent-2",
			})
			assert.Error(t, err)
		})

		convey.Convey("重复 backend 拒绝保存", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{
					ID: 42, Name: "Eva", AvatarColor: "agent-2",
					DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE,
					PromptJSON: "[]", SkillsJSON: "[]",
				}, nil)
			_, err := svc.Update(ctx, &UpdateAgentRequest{
				ID: 42, Name: "Eva", AvatarColor: "agent-2",
				ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}, {AgentBackendID: 5}},
			})
			assert.Error(t, err)
		})

		convey.Convey("多档：全部写入并按顺序打平成响应", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{
					ID: 42, Name: "Eva", AvatarColor: "agent-2",
					DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE,
					PromptJSON: "[]", SkillsJSON: "[]",
				}, nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(6)).Return(activeBackend(6), nil)
			var captured []*agent_entity.AgentExecTarget
			agentMock.EXPECT().UpdateWithTargets(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ *agent_entity.Agent, targets []*agent_entity.AgentExecTarget) error {
					captured = targets
					return nil
				})
			_, err := svc.Update(ctx, &UpdateAgentRequest{
				ID: 42, Name: "Eva", AvatarColor: "agent-2",
				ExecTargets: []ExecTargetInputDTO{
					{AgentBackendID: 5, Skills: []department_svc.AgentSkillDTO{{ID: "superpowers@x", Enabled: true}}},
					{AgentBackendID: 6},
				},
			})
			assert.NoError(t, err)
			if assert.Len(t, captured, 2) {
				assert.Equal(t, int64(5), captured[0].AgentBackendID)
				assert.Equal(t, int64(6), captured[1].AgentBackendID)
			}
		})
	})
}

// TestUpdateAgent_SkillsComeFromExecTargets R15e：「`agents.skills_json` 不再被
// 读取」。写完之后回给前端的那份 AgentItem 里，技能授权必须来自执行目标行（档 ①），
// 不是 Agent 行上那份已经停止维护的旧列。
func TestUpdateAgent_SkillsComeFromExecTargets(t *testing.T) {
	ctx, agentMock, _, backendMock, svc := setupSvc(t)
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	agentMock.EXPECT().Find(gomock.Any(), int64(42)).Return(&agent_entity.Agent{
		ID: 42, Name: "Eva", Status: consts.ACTIVE, AvatarColor: "agent-2",
		DepartmentID: 2, AgentBackendID: 5, PromptJSON: "[]",
		SkillsJSON: `[{"id":"stale@row","enabled":true}]`,
	}, nil)
	backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil).AnyTimes()
	agentMock.EXPECT().UpdateWithTargets(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	targetMock := mock_agent_repo.NewMockAgentExecTargetRepo(ctrl)
	targetMock.EXPECT().ListByAgent(gomock.Any(), int64(42)).Return([]*agent_entity.AgentExecTarget{
		{ID: 1, AgentID: 42, AgentBackendID: 5, SortOrder: 0,
			SkillsJSON: `[{"id":"fresh@target","enabled":true}]`},
	}, nil).AnyTimes()
	agent_repo.RegisterAgentExecTarget(targetMock)

	resp, err := svc.Update(ctx, &UpdateAgentRequest{
		ID: 42, Name: "Eva", AvatarColor: "agent-2",
		ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}},
	})
	assert.NoError(t, err)
	if assert.Len(t, resp.Item.ExecTargets, 1) && assert.Len(t, resp.Item.ExecTargets[0].Skills, 1) {
		assert.Equal(t, "fresh@target", resp.Item.ExecTargets[0].Skills[0].ID)
	}
}

func TestMoveAgent(t *testing.T) {
	convey.Convey("Move Agent", t, func() {
		ctx, agentMock, deptMock, _, svc := setupSvc(t)

		convey.Convey("CEO 拒绝 Move", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(1)).
				Return(&agent_entity.Agent{ID: 1, Name: "CEO 助手", SystemBadge: "DEFAULT", Status: consts.ACTIVE}, nil)
			_, err := svc.Move(ctx, &MoveAgentRequest{ID: 1, NewDepartmentID: 3})
			assert.Error(t, err)
		})

		convey.Convey("普通 Agent 移到新部门", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(8)).Return(activeDept(8), nil)
			agentMock.EXPECT().NextSortOrder(gomock.Any(), int64(8)).Return(3, nil)
			agentMock.EXPECT().UpdatePlacement(gomock.Any(), int64(42), int64(8), int64(0), 3).Return(nil)
			resp, err := svc.Move(ctx, &MoveAgentRequest{ID: 42, NewDepartmentID: 8})
			assert.NoError(t, err)
			assert.Equal(t, int64(8), resp.Item.DepartmentID)
		})

		convey.Convey("普通 Agent 移到上级 Agent 下", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			agentMock.EXPECT().Find(gomock.Any(), int64(8)).
				Return(&agent_entity.Agent{ID: 8, Name: "Boris", DepartmentID: 3, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
				{ID: 8, ParentAgentID: 0},
				{ID: 42, ParentAgentID: 0},
			}, nil)
			agentMock.EXPECT().NextSortOrderByParent(gomock.Any(), int64(8)).Return(3, nil)
			agentMock.EXPECT().UpdatePlacement(gomock.Any(), int64(42), int64(0), int64(8), 3).Return(nil)
			resp, err := svc.Move(ctx, &MoveAgentRequest{ID: 42, NewParentAgentID: 8})
			assert.NoError(t, err)
			assert.Equal(t, int64(8), resp.Item.ParentAgentID)
			assert.Equal(t, int64(0), resp.Item.DepartmentID)
		})

		convey.Convey("环：不能移到自己的下级 Agent 下", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			agentMock.EXPECT().Find(gomock.Any(), int64(8)).
				Return(&agent_entity.Agent{ID: 8, Name: "Boris", ParentAgentID: 42, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
				{ID: 42, ParentAgentID: 0},
				{ID: 8, ParentAgentID: 42},
			}, nil)
			_, err := svc.Move(ctx, &MoveAgentRequest{ID: 42, NewParentAgentID: 8})
			assert.Error(t, err)
		})
	})
}

func TestHasAgentCycle(t *testing.T) {
	all := []*agent_entity.Agent{
		{ID: 1, ParentAgentID: 0},
		{ID: 2, ParentAgentID: 1},
		{ID: 3, ParentAgentID: 2},
	}
	assert.False(t, hasAgentCycle(all, 0, 1))
	assert.True(t, hasAgentCycle(all, 2, 1))
	assert.True(t, hasAgentCycle(all, 3, 1))
	assert.False(t, hasAgentCycle(all, 1, 3))
}

func TestDeleteAgentCEORejected(t *testing.T) {
	ctx, agentMock, _, _, svc := setupSvc(t)
	agentMock.EXPECT().Find(gomock.Any(), int64(1)).
		Return(&agent_entity.Agent{ID: 1, SystemBadge: "DEFAULT", Status: consts.ACTIVE}, nil)
	_, err := svc.Delete(ctx, &DeleteAgentRequest{ID: 1})
	assert.Error(t, err)
}

// 1x1 透明 PNG（70 字节左右），保证落在 2MB 限制内。
const pngDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkAAIAAAoAAv/lxKUAAAAASUVORK5CYII="

func TestUploadAgentAvatar(t *testing.T) {
	convey.Convey("上传 Agent 头像", t, func() {
		ctx, agentMock, _, _, svc := setupSvc(t)

		convey.Convey("成功写入 data URL", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			agentMock.EXPECT().UpdateAvatar(gomock.Any(), int64(42), pngDataURL, int64(1700000000)).Return(nil)
			resp, err := svc.UploadAvatar(ctx, &UploadAvatarRequest{ID: 42, DataURL: pngDataURL})
			assert.NoError(t, err)
			assert.Equal(t, pngDataURL, resp.Item.AvatarDataURL)
		})

		convey.Convey("不存在的 Agent 拒绝", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, nil)
			_, err := svc.UploadAvatar(ctx, &UploadAvatarRequest{ID: 99, DataURL: pngDataURL})
			assert.Error(t, err)
		})

		convey.Convey("非图片 data URL 拒绝", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			_, err := svc.UploadAvatar(ctx, &UploadAvatarRequest{ID: 42, DataURL: "data:text/plain;base64,aGVsbG8="})
			assert.Error(t, err)
		})

		convey.Convey("空 data URL 拒绝", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			_, err := svc.UploadAvatar(ctx, &UploadAvatarRequest{ID: 42, DataURL: ""})
			assert.Error(t, err)
		})

		convey.Convey("解码后超过 2MB 拒绝", func() {
			big := strings.Repeat("A", (2*1024*1024+1+2)/3*4) // ≈ 2MB base64
			payload := "data:image/png;base64," + big
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			_, err := svc.UploadAvatar(ctx, &UploadAvatarRequest{ID: 42, DataURL: payload})
			assert.Error(t, err)
		})
	})
}

func TestDeleteAgentAvatar(t *testing.T) {
	convey.Convey("删除 Agent 头像", t, func() {
		ctx, agentMock, _, _, svc := setupSvc(t)

		convey.Convey("成功清空 data URL", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{ID: 42, Name: "Eva", AvatarDataURL: pngDataURL, DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE}, nil)
			agentMock.EXPECT().UpdateAvatar(gomock.Any(), int64(42), "", int64(1700000000)).Return(nil)
			resp, err := svc.DeleteAvatar(ctx, &DeleteAvatarRequest{ID: 42})
			assert.NoError(t, err)
			assert.Equal(t, "", resp.Item.AvatarDataURL)
		})

		convey.Convey("不存在的 Agent 拒绝", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, nil)
			_, err := svc.DeleteAvatar(ctx, &DeleteAvatarRequest{ID: 99})
			assert.Error(t, err)
		})
	})
}

func TestCreateAgent_WithTools(t *testing.T) {
	convey.Convey("Create Agent 带 Tools", t, func() {
		ctx, agentMock, deptMock, backendMock, svc := setupSvc(t)

		convey.Convey("请求带 Tools → entity ToolsJSON 落值 + 响应 Item.Tools 回传", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(2)).Return(activeDept(2), nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
			agentMock.EXPECT().FindByName(gomock.Any(), "Eva").Return(nil, nil)
			agentMock.EXPECT().NextSortOrder(gomock.Any(), int64(2)).Return(1, nil)
			var captured *agent_entity.Agent
			agentMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a *agent_entity.Agent) error {
				a.ID = 99
				captured = a
				return nil
			})
			resp, err := svc.Create(ctx, &CreateAgentRequest{
				Name: "Eva", AvatarColor: "agent-2", DepartmentID: 2, AgentBackendID: 5,
				Tools: []department_svc.AgentToolDTO{{Key: "org", Enabled: true}},
			})
			assert.NoError(t, err)
			tools := captured.GetTools()
			assert.Len(t, tools, 1)
			assert.Equal(t, "org", tools[0].Key)
			assert.True(t, tools[0].Enabled)
			assert.Len(t, resp.Item.Tools, 1)
			assert.Equal(t, "org", resp.Item.Tools[0].Key)
			assert.True(t, resp.Item.Tools[0].Enabled)
		})
	})
}

func TestUpdateAgent_WithTools(t *testing.T) {
	convey.Convey("Update Agent 带 Tools", t, func() {
		ctx, agentMock, _, backendMock, svc := setupSvc(t)

		convey.Convey("请求带 Tools → entity ToolsJSON 落值 + 响应 Item.Tools 回传", func() {
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{
					ID: 42, Name: "Eva", AvatarColor: "agent-2",
					DepartmentID: 2, AgentBackendID: 5, Status: consts.ACTIVE,
					PromptJSON: "[]", SkillsJSON: "[]", ToolsJSON: "[]",
				}, nil)
			backendMock.EXPECT().Find(gomock.Any(), int64(5)).Return(activeBackend(5), nil)
			var captured *agent_entity.Agent
			agentMock.EXPECT().UpdateWithTargets(gomock.Any(), gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, a *agent_entity.Agent, _ []*agent_entity.AgentExecTarget) error {
					captured = a
					return nil
				})
			resp, err := svc.Update(ctx, &UpdateAgentRequest{
				ID: 42, Name: "Eva", AvatarColor: "agent-2",
				ExecTargets: []ExecTargetInputDTO{{AgentBackendID: 5}},
				Tools:       []department_svc.AgentToolDTO{{Key: "org", Enabled: false}},
			})
			assert.NoError(t, err)
			tools := captured.GetTools()
			assert.Len(t, tools, 1)
			assert.Equal(t, "org", tools[0].Key)
			assert.False(t, tools[0].Enabled)
			assert.Len(t, resp.Item.Tools, 1)
			assert.Equal(t, "org", resp.Item.Tools[0].Key)
			assert.False(t, resp.Item.Tools[0].Enabled)
		})
	})
}

func TestReorderAgents(t *testing.T) {
	convey.Convey("Agent 同级排序", t, func() {
		ctx, agentMock, _, _, svc := setupSvc(t)

		convey.Convey("部门下成功重排", func() {
			agentMock.EXPECT().ListByDepartment(gomock.Any(), int64(2)).
				Return([]*agent_entity.Agent{{ID: 1}, {ID: 3}}, nil)
			agentMock.EXPECT().ReorderSiblings(gomock.Any(), int64(2), int64(0), []int64{3, 1}).Return(nil)
			err := svc.Reorder(ctx, &ReorderAgentsRequest{DepartmentID: 2, OrderedIDs: []int64{3, 1}})
			convey.So(err, convey.ShouldBeNil)
		})

		convey.Convey("外来 id 拒绝", func() {
			agentMock.EXPECT().ListByDepartment(gomock.Any(), int64(2)).
				Return([]*agent_entity.Agent{{ID: 1}, {ID: 3}}, nil)
			err := svc.Reorder(ctx, &ReorderAgentsRequest{DepartmentID: 2, OrderedIDs: []int64{3, 9}})
			convey.So(err, convey.ShouldNotBeNil)
		})

		convey.Convey("既没部门也没上级 → 参数错误", func() {
			err := svc.Reorder(ctx, &ReorderAgentsRequest{OrderedIDs: []int64{1}})
			convey.So(err, convey.ShouldNotBeNil)
		})
	})
}

func TestAgentSvc_SetPinned(t *testing.T) {
	convey.Convey("SetPinned 透传到 repo", t, func() {
		ctx, agentMock, _, _, svc := setupSvc(t)

		convey.Convey("存在的 Agent 置顶", func() {
			agentMock.EXPECT().Find(ctx, int64(7)).Return(&agent_entity.Agent{ID: 7, Status: consts.ACTIVE}, nil)
			agentMock.EXPECT().SetPinned(ctx, int64(7), true).Return(nil)

			resp, err := svc.SetPinned(ctx, &SetPinnedRequest{ID: 7, Pinned: true})
			convey.So(err, convey.ShouldBeNil)
			convey.So(resp.ID, convey.ShouldEqual, int64(7))
			convey.So(resp.Pinned, convey.ShouldBeTrue)
		})

		convey.Convey("不存在的 Agent 拒绝", func() {
			agentMock.EXPECT().Find(ctx, int64(99)).Return(nil, nil)
			_, err := svc.SetPinned(ctx, &SetPinnedRequest{ID: 99, Pinned: true})
			assert.Error(t, err)
		})
	})
}
