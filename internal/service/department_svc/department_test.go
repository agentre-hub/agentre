package department_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/service/department_svc/mock_deps"
)

func setupSvc(t *testing.T) (
	context.Context,
	*mock_department_repo.MockDepartmentRepo,
	*mock_deps.MockAgentPort,
	*departmentSvc,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	deptMock := mock_department_repo.NewMockDepartmentRepo(ctrl)
	agentMock := mock_deps.NewMockAgentPort(ctrl)
	department_repo.RegisterDepartment(deptMock)
	return context.Background(), deptMock, agentMock, &departmentSvc{
		now:    func() int64 { return 1700000000 },
		agents: agentMock,
		tx:     directTx{},
	}
}

// directTx 是不连库的 TxRunner：直接在同一个 ctx 上调用回调，回调的错误原样返回。
type directTx struct{}

func (directTx) RunInTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return fn(ctx)
}

func TestCreateDepartment(t *testing.T) {
	convey.Convey("创建部门", t, func() {
		ctx, deptMock, _, svc := setupSvc(t)

		convey.Convey("顶级部门成功", func() {
			deptMock.EXPECT().FindByName(gomock.Any(), "工程部", int64(0)).Return(nil, nil)
			deptMock.EXPECT().NextSortOrder(gomock.Any(), int64(0)).Return(1, nil)
			deptMock.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, d *department_entity.Department) error {
				d.ID = 7
				return nil
			})
			resp, err := svc.Create(ctx, &CreateDepartmentRequest{Name: "工程部", AccentColor: "agent-2"})
			assert.NoError(t, err)
			assert.Equal(t, int64(7), resp.Item.ID)
		})

		convey.Convey("同父重名拒绝", func() {
			deptMock.EXPECT().FindByName(gomock.Any(), "工程部", int64(0)).
				Return(&department_entity.Department{ID: 1, Name: "工程部"}, nil)
			_, err := svc.Create(ctx, &CreateDepartmentRequest{Name: "工程部", AccentColor: "agent-2"})
			assert.Error(t, err)
		})

		convey.Convey("父部门不存在", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(99)).Return(nil, nil)
			_, err := svc.Create(ctx, &CreateDepartmentRequest{Name: "x", AccentColor: "agent-1", ParentID: 99})
			assert.Error(t, err)
		})

		convey.Convey("非法颜色拒绝（entity Check）", func() {
			_, err := svc.Create(ctx, &CreateDepartmentRequest{Name: "x", AccentColor: "rainbow"})
			assert.Error(t, err)
		})
	})
}

func TestMoveDepartment(t *testing.T) {
	convey.Convey("Move 部门", t, func() {
		ctx, deptMock, _, svc := setupSvc(t)

		convey.Convey("正常 Move 到另一父", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).
				Return(&department_entity.Department{ID: 3, ParentID: 1, Status: 1}, nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(2)).
				Return(&department_entity.Department{ID: 2, ParentID: 0, Status: 1}, nil)
			deptMock.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
				{ID: 1, ParentID: 0}, {ID: 2, ParentID: 0}, {ID: 3, ParentID: 1},
			}, nil)
			deptMock.EXPECT().NextSortOrder(gomock.Any(), int64(2)).Return(1, nil)
			deptMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
			_, err := svc.Move(ctx, &MoveDepartmentRequest{ID: 3, NewParentID: 2})
			assert.NoError(t, err)
		})

		convey.Convey("环：3 → 5（5 是 3 的子）", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).
				Return(&department_entity.Department{ID: 3, ParentID: 0, Status: 1}, nil)
			deptMock.EXPECT().Find(gomock.Any(), int64(5)).
				Return(&department_entity.Department{ID: 5, ParentID: 3, Status: 1}, nil)
			deptMock.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
				{ID: 3, ParentID: 0}, {ID: 5, ParentID: 3},
			}, nil)
			_, err := svc.Move(ctx, &MoveDepartmentRequest{ID: 3, NewParentID: 5})
			assert.Error(t, err)
		})
	})
}

func TestHasCycle(t *testing.T) {
	all := []*department_entity.Department{
		{ID: 1, ParentID: 0},
		{ID: 2, ParentID: 1},
		{ID: 3, ParentID: 2},
	}
	cases := []struct {
		name          string
		startParentID int64
		selfID        int64
		expectCycle   bool
	}{
		{"move to top", 0, 1, false},
		{"move under self direct child", 2, 1, true},
		{"move under self deep descendant", 3, 1, true},
		{"move under unrelated", 1, 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expectCycle, hasCycle(all, tc.startParentID, tc.selfID))
		})
	}
}

func TestCollectSubtree(t *testing.T) {
	all := []*department_entity.Department{
		{ID: 1, ParentID: 0},
		{ID: 2, ParentID: 1},
		{ID: 3, ParentID: 2},
		{ID: 4, ParentID: 1},
		{ID: 5, ParentID: 0},
	}
	got := collectSubtree(all, 1)
	assert.ElementsMatch(t, []int64{1, 2, 3, 4}, got)
}

func TestCollectAgentsInDepartments(t *testing.T) {
	all := []*agent_entity.Agent{
		{ID: 10, DepartmentID: 1, ParentAgentID: 0},
		{ID: 11, DepartmentID: 0, ParentAgentID: 10},
		{ID: 12, DepartmentID: 0, ParentAgentID: 11},
		{ID: 20, DepartmentID: 2, ParentAgentID: 0},
	}

	got := collectAgentsInDepartments(all, []int64{1})

	assert.ElementsMatch(t, []int64{10, 11, 12}, got)
}

func TestUpdateDepartmentLeadValidation(t *testing.T) {
	convey.Convey("Update 部门 lead 校验", t, func() {
		ctx, deptMock, agentMock, svc := setupSvc(t)

		convey.Convey("lead 不属于本部门 → 拒绝", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).
				Return(&department_entity.Department{ID: 3, Name: "old", Status: 1}, nil)
			deptMock.EXPECT().FindByName(gomock.Any(), "工程部", int64(0)).Return(nil, nil)
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{ID: 42, DepartmentID: 99}, nil)
			_, err := svc.Update(ctx, &UpdateDepartmentRequest{
				ID: 3, Name: "工程部", AccentColor: "agent-2", LeadAgentID: 42,
			})
			assert.Error(t, err)
		})

		convey.Convey("lead 属于本部门 → 通过", func() {
			deptMock.EXPECT().Find(gomock.Any(), int64(3)).
				Return(&department_entity.Department{ID: 3, Name: "old", Status: 1}, nil)
			deptMock.EXPECT().FindByName(gomock.Any(), "工程部", int64(0)).Return(nil, nil)
			agentMock.EXPECT().Find(gomock.Any(), int64(42)).
				Return(&agent_entity.Agent{ID: 42, DepartmentID: 3}, nil)
			deptMock.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
			_, err := svc.Update(ctx, &UpdateDepartmentRequest{
				ID: 3, Name: "工程部", AccentColor: "agent-2", LeadAgentID: 42,
			})
			assert.NoError(t, err)
		})
	})
}

func setupLoadSvc(t *testing.T) (
	context.Context,
	*mock_department_repo.MockDepartmentRepo,
	*mock_deps.MockAgentPort,
	*mock_deps.MockAgentBackendPort,
	*mock_deps.MockLLMProviderPort,
	*mock_deps.MockAgentExecTargetPort,
	*departmentSvc,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	deptMock := mock_department_repo.NewMockDepartmentRepo(ctrl)
	agentMock := mock_deps.NewMockAgentPort(ctrl)
	backendMock := mock_deps.NewMockAgentBackendPort(ctrl)
	providerMock := mock_deps.NewMockLLMProviderPort(ctrl)
	execTargetMock := mock_deps.NewMockAgentExecTargetPort(ctrl)
	department_repo.RegisterDepartment(deptMock)
	return context.Background(), deptMock, agentMock, backendMock, providerMock, execTargetMock, &departmentSvc{
		now:              func() int64 { return 1700000000 },
		agents:           agentMock,
		agentBackends:    backendMock,
		llmProviders:     providerMock,
		agentExecTargets: execTargetMock,
		tx:               directTx{},
	}
}

func TestLoad_ToolsProjectionAndAvailableTools(t *testing.T) {
	convey.Convey("Load 部门+Agent", t, func() {
		ctx, deptMock, agentMock, backendMock, providerMock, execTargetMock, svc := setupLoadSvc(t)

		convey.Convey("AgentItem.Tools 投影 + LoadOrgResponse.AvailableTools == agenttool.Keys()", func() {
			deptMock.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
				{ID: 1, Name: "工程部", Status: 1},
			}, nil)
			agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
				{
					ID: 10, Name: "Eva", DepartmentID: 1, Status: 1,
					PromptJSON: "[]", SkillsJSON: "[]",
					ToolsJSON: `[{"key":"org","enabled":true}]`,
				},
			}, nil)
			backendMock.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil)
			providerMock.EXPECT().List(gomock.Any()).Return(nil, nil)
			execTargetMock.EXPECT().ListByAgents(gomock.Any(), []int64{10}).Return(nil, nil)

			resp, err := svc.Load(ctx, &LoadOrgRequest{})
			assert.NoError(t, err)
			assert.Len(t, resp.Agents, 1)
			assert.Len(t, resp.Agents[0].Tools, 1)
			assert.Equal(t, "org", resp.Agents[0].Tools[0].Key)
			assert.True(t, resp.Agents[0].Tools[0].Enabled)
			assert.Equal(t, agenttool.Keys(), resp.AvailableTools)
		})
	})
}

// TestLoad_CarriesPinned 锁住 AgentItem.Pinned：agrctl 的 agent 视图读的就是它。
func TestLoad_CarriesPinned(t *testing.T) {
	convey.Convey("Given 一个置顶、一个未置顶的 Agent", t, func() {
		ctx, deptMock, agentMock, backendMock, providerMock, execTargetMock, svc := setupLoadSvc(t)
		deptMock.EXPECT().List(gomock.Any()).Return(nil, nil)
		agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
			{ID: 10, Name: "Eva", Status: 1, Pinned: true, PromptJSON: "[]", SkillsJSON: "[]", ToolsJSON: "[]"},
			{ID: 11, Name: "Bob", Status: 1, PromptJSON: "[]", SkillsJSON: "[]", ToolsJSON: "[]"},
		}, nil)
		backendMock.EXPECT().List(gomock.Any()).Return(nil, nil)
		providerMock.EXPECT().List(gomock.Any()).Return(nil, nil)
		execTargetMock.EXPECT().ListByAgents(gomock.Any(), []int64{10, 11}).Return(nil, nil)

		convey.Convey("When Load, Then 各自带出置顶状态", func() {
			resp, err := svc.Load(ctx, &LoadOrgRequest{})
			assert.NoError(t, err)
			assert.Len(t, resp.Agents, 2)
			assert.True(t, resp.Agents[0].Pinned)
			assert.False(t, resp.Agents[1].Pinned)
		})
	})
}

// TestLoad_BackendSummaryModel 锁住组织页 Agent 卡片上的摘要模型：写供应商默认模型的
// 展示名，没填展示名才回落 ModelID。
func TestLoad_BackendSummaryModel(t *testing.T) {
	convey.Convey("Load 的 BackendSummary.LLMProviderModel", t, func() {
		ctx, deptMock, agentMock, backendMock, providerMock, execTargetMock, svc := setupLoadSvc(t)

		expectLoad := func(model *llm_provider_model_entity.LLMProviderModel) {
			deptMock.EXPECT().List(gomock.Any()).Return(nil, nil)
			agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
				{ID: 10, Name: "Eva", Status: 1, AgentBackendID: 51, PromptJSON: "[]", SkillsJSON: "[]", ToolsJSON: "[]"},
			}, nil)
			backendMock.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{
				{ID: 51, Name: "cc", Type: string(agent_backend_entity.TypeClaudeCode), LLMProviderKey: "pk-1"},
			}, nil)
			providerMock.EXPECT().List(gomock.Any()).Return([]*llm_provider_entity.LLMProvider{
				{ProviderKey: "pk-1", Name: "Anthropic", DefaultModelKey: "mk-1", Status: consts.ACTIVE},
			}, nil)
			providerMock.EXPECT().FindModelByKey(gomock.Any(), "mk-1").Return(model, nil)
			execTargetMock.EXPECT().ListByAgents(gomock.Any(), []int64{10}).Return(nil, nil)
		}

		convey.Convey("默认模型有展示名 → 写展示名", func() {
			expectLoad(&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-1", ModelID: "claude-sonnet-4-6", Name: "Sonnet 4.6"})

			resp, err := svc.Load(ctx, &LoadOrgRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) && assert.NotNil(t, resp.Agents[0].Backend) {
				assert.Equal(t, "Anthropic", resp.Agents[0].Backend.LLMProviderName)
				assert.Equal(t, "Sonnet 4.6", resp.Agents[0].Backend.LLMProviderModel)
			}
		})

		convey.Convey("默认模型没填展示名 → 回落 ModelID", func() {
			expectLoad(&llm_provider_model_entity.LLMProviderModel{ModelKey: "mk-1", ModelID: "claude-sonnet-4-6"})

			resp, err := svc.Load(ctx, &LoadOrgRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) && assert.NotNil(t, resp.Agents[0].Backend) {
				assert.Equal(t, "claude-sonnet-4-6", resp.Agents[0].Backend.LLMProviderModel)
			}
		})
	})
}

// TestLoad_ExecTargets 锁住 R15/R15e：AgentItem.ExecTargets 按 sort_order 给出有序
// 执行目标列表，每一档带着自己的技能授权（存放位置已下沉到执行目标行）。
func TestLoad_ExecTargets(t *testing.T) {
	convey.Convey("Load 部门+Agent 的执行目标列表", t, func() {
		ctx, deptMock, agentMock, backendMock, providerMock, execTargetMock, svc := setupLoadSvc(t)

		convey.Convey("多档 → AgentItem.ExecTargets 按 sort_order 给出，各自带技能", func() {
			deptMock.EXPECT().List(gomock.Any()).Return(nil, nil)
			agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
				{ID: 10, Name: "Eva", Status: 1, PromptJSON: "[]", SkillsJSON: "[]", ToolsJSON: "[]"},
			}, nil)
			backendMock.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil)
			providerMock.EXPECT().List(gomock.Any()).Return(nil, nil)
			execTargetMock.EXPECT().ListByAgents(gomock.Any(), []int64{10}).Return(
				map[int64][]*agent_entity.AgentExecTarget{
					10: {
						{ID: 1, AgentID: 10, AgentBackendID: 51, SortOrder: 0, SkillsJSON: `[{"id":"superpowers@x","enabled":true}]`},
						{ID: 2, AgentID: 10, AgentBackendID: 52, SortOrder: 1, SkillsJSON: `[]`},
					},
				}, nil)

			resp, err := svc.Load(ctx, &LoadOrgRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) && assert.Len(t, resp.Agents[0].ExecTargets, 2) {
				assert.Equal(t, int64(51), resp.Agents[0].ExecTargets[0].AgentBackendID)
				if assert.Len(t, resp.Agents[0].ExecTargets[0].Skills, 1) {
					assert.Equal(t, "superpowers@x", resp.Agents[0].ExecTargets[0].Skills[0].ID)
					assert.True(t, resp.Agents[0].ExecTargets[0].Skills[0].Enabled)
				}
				assert.Equal(t, int64(52), resp.Agents[0].ExecTargets[1].AgentBackendID)
				assert.Empty(t, resp.Agents[0].ExecTargets[1].Skills)
			}
		})

		// 守卫（R15e）：授权只能从执行目标行上读出来——Agent 实体上的 SkillsJSON 不落库，
		// 可能与执行目标行不一致（同步落地走 UpdateRow / UpsertFromSync，只改执行目标行）。
		convey.Convey("Agent 行的 skills_json 已过期 → 授权取执行目标行 ①", func() {
			deptMock.EXPECT().List(gomock.Any()).Return(nil, nil)
			agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
				{
					ID: 10, Name: "Eva", Status: 1, PromptJSON: "[]", ToolsJSON: "[]",
					SkillsJSON: `[{"id":"stale@row","enabled":true}]`,
				},
			}, nil)
			backendMock.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil)
			providerMock.EXPECT().List(gomock.Any()).Return(nil, nil)
			execTargetMock.EXPECT().ListByAgents(gomock.Any(), []int64{10}).Return(
				map[int64][]*agent_entity.AgentExecTarget{
					10: {
						{ID: 1, AgentID: 10, AgentBackendID: 51, SortOrder: 0, SkillsJSON: `[{"id":"fresh@target","enabled":true}]`},
						{ID: 2, AgentID: 10, AgentBackendID: 52, SortOrder: 1, SkillsJSON: `[{"id":"other@target","enabled":true}]`},
					},
				}, nil)

			resp, err := svc.Load(ctx, &LoadOrgRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) && assert.Len(t, resp.Agents[0].ExecTargets, 2) &&
				assert.Len(t, resp.Agents[0].ExecTargets[0].Skills, 1) {
				assert.Equal(t, "fresh@target", resp.Agents[0].ExecTargets[0].Skills[0].ID)
			}
		})

		// 执行目标列表为空 → 一档授权都读不出来，而不是回落到 Agent 行的遗留列。
		convey.Convey("没有执行目标行 → 一档授权都没有", func() {
			deptMock.EXPECT().List(gomock.Any()).Return(nil, nil)
			agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
				{
					ID: 11, Name: "Solo", Status: 1, PromptJSON: "[]", ToolsJSON: "[]",
					SkillsJSON: `[{"id":"stale@row","enabled":true}]`,
				},
			}, nil)
			backendMock.EXPECT().List(gomock.Any()).Return([]*agent_backend_entity.AgentBackend{}, nil)
			providerMock.EXPECT().List(gomock.Any()).Return(nil, nil)
			execTargetMock.EXPECT().ListByAgents(gomock.Any(), []int64{11}).Return(nil, nil)

			resp, err := svc.Load(ctx, &LoadOrgRequest{})
			assert.NoError(t, err)
			if assert.Len(t, resp.Agents, 1) {
				assert.Empty(t, resp.Agents[0].ExecTargets)
			}
		})
	})
}

func TestReorderDepartments(t *testing.T) {
	convey.Convey("部门同级排序", t, func() {
		ctx, deptMock, _, svc := setupSvc(t)

		convey.Convey("成功重排顶层", func() {
			deptMock.EXPECT().ListByParent(gomock.Any(), int64(0)).
				Return([]*department_entity.Department{{ID: 4}, {ID: 5}}, nil)
			deptMock.EXPECT().ReorderSiblings(gomock.Any(), int64(0), []int64{5, 4}).Return(nil)
			err := svc.Reorder(ctx, &ReorderDepartmentsRequest{ParentID: 0, OrderedIDs: []int64{5, 4}})
			convey.So(err, convey.ShouldBeNil)
		})

		convey.Convey("集合不全 → 参数错误", func() {
			deptMock.EXPECT().ListByParent(gomock.Any(), int64(0)).
				Return([]*department_entity.Department{{ID: 4}, {ID: 5}}, nil)
			err := svc.Reorder(ctx, &ReorderDepartmentsRequest{ParentID: 0, OrderedIDs: []int64{5}})
			convey.So(err, convey.ShouldNotBeNil)
		})
	})
}

var _ = code.DepartmentLeadNotInDepartment

// CascadeImpact 与级联删除同一口径：子部门（不含自己），以及挂在子树部门上的顶层 Agent
// 连同它们的下级 Agent（下级自己的部门不论）。审批卡上的数量就是真删掉的数量。
func TestCascadeImpact(t *testing.T) {
	convey.Convey("级联删除影响计数", t, func() {
		ctx, deptMock, agentMock, svc := setupSvc(t)

		convey.Convey("子树两层、子树外的部门与 Agent 不计", func() {
			deptMock.EXPECT().List(gomock.Any()).Return([]*department_entity.Department{
				{ID: 2}, {ID: 7, ParentID: 2}, {ID: 8, ParentID: 7}, {ID: 9},
			}, nil)
			agentMock.EXPECT().List(gomock.Any()).Return([]*agent_entity.Agent{
				{ID: 1, DepartmentID: 2}, {ID: 2, DepartmentID: 8}, {ID: 3, ParentAgentID: 2, DepartmentID: 9},
				{ID: 4, DepartmentID: 9},
			}, nil)
			depts, agents, err := svc.CascadeImpact(ctx, 2)
			convey.So(err, convey.ShouldBeNil)
			convey.So(depts, convey.ShouldEqual, 2)
			convey.So(agents, convey.ShouldEqual, 3)
		})

		convey.Convey("读部门失败 → 原样返回错误", func() {
			deptMock.EXPECT().List(gomock.Any()).Return(nil, errors.New("db down"))
			_, _, err := svc.CascadeImpact(ctx, 2)
			convey.So(err, convey.ShouldNotBeNil)
		})
	})
}
