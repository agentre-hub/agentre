package memory_svc

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/httputils"
	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
	"github.com/agentre-hub/agentre/internal/repository/department_repo/mock_department_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo/mock_memory_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
)

const testNow int64 = 1_800_000_000_000

type svcMocks struct {
	memory  *mock_memory_repo.MockMemoryRepo
	agent   *mock_agent_repo.MockAgentRepo
	dept    *mock_department_repo.MockDepartmentRepo
	project *mock_project_repo.MockProjectRepo
	session *mock_chat_repo.MockSessionRepo
}

func setupMemorySvc(t *testing.T) (context.Context, svcMocks, *memorySvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	m := svcMocks{
		memory:  mock_memory_repo.NewMockMemoryRepo(ctrl),
		agent:   mock_agent_repo.NewMockAgentRepo(ctrl),
		dept:    mock_department_repo.NewMockDepartmentRepo(ctrl),
		project: mock_project_repo.NewMockProjectRepo(ctrl),
		session: mock_chat_repo.NewMockSessionRepo(ctrl),
	}
	memory_repo.RegisterMemory(m.memory)
	agent_repo.RegisterAgent(m.agent)
	department_repo.RegisterDepartment(m.dept)
	project_repo.RegisterProject(m.project)
	chat_repo.RegisterSession(m.session)
	return context.Background(), m, newMemorySvc(func() int64 { return testNow })
}

// expectAIScope 摆好 aiActor 的会话范围：会话 70 在项目 9；agent 5 在部门 2，部门 2 的
// 上级是部门 1。于是 AI 所属的层是全局、部门 1、部门 2、agent 5、项目 9。这些查询怎么
// 发、发几次不是契约，所以不限次数。
func expectAIScope(m svcMocks) {
	m.session.EXPECT().Find(gomock.Any(), int64(70)).Return(&chat_entity.Session{ID: 70, AgentID: 5, ProjectID: 9}, nil).AnyTimes()
	m.agent.EXPECT().Find(gomock.Any(), int64(5)).Return(&agent_entity.Agent{ID: 5, DepartmentID: 2, Status: consts.ACTIVE}, nil).AnyTimes()
	m.dept.EXPECT().Find(gomock.Any(), int64(2)).Return(&department_entity.Department{ID: 2, ParentID: 1, Status: consts.ACTIVE}, nil).AnyTimes()
	m.dept.EXPECT().Find(gomock.Any(), int64(1)).Return(&department_entity.Department{ID: 1, Status: consts.ACTIVE}, nil).AnyTimes()
	m.project.EXPECT().Find(gomock.Any(), int64(9)).Return(&project_entity.Project{ID: 9}, nil).AnyTimes()
}

func codeOf(err error) int {
	var e *httputils.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

var (
	agentOwner   = memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: 5}
	deptOwner    = memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 2}
	projectOwner = memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 9}
	aiActor      = memory_entity.AIActor(5, 70)
)

func stored(id int64, owner memory_entity.Owner, summary string) *memory_entity.Memory {
	m := memory_entity.NewMemory(owner, summary, "", memory_entity.UserActor(), 100)
	m.ID = id
	return m
}

func TestMemorySvc_Create(t *testing.T) {
	convey.Convey("新增记忆", t, func() {
		ctx, m, svc := setupMemorySvc(t)

		convey.Convey("用户在全局层新增：去掉首尾空白、记下创建者为用户", func() {
			m.memory.EXPECT().FindNormalBySummary(ctx, memory_entity.GlobalOwner(), "用 pnpm").Return(nil, nil)
			m.memory.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, e *memory_entity.Memory) error {
				convey.So(e.Summary, convey.ShouldEqual, "用 pnpm")
				convey.So(e.Detail, convey.ShouldEqual, "详情")
				convey.So(e.Creator(), convey.ShouldResemble, memory_entity.UserActor())
				convey.So(e.State, convey.ShouldEqual, memory_entity.StateNormal)
				convey.So(e.ChangedAt, convey.ShouldEqual, testNow)
				e.ID = 11
				return nil
			})
			res, err := svc.Create(ctx, &CreateRequest{
				Owner: memory_entity.GlobalOwner(), Summary: "  用 pnpm \n", Detail: "详情\n", Actor: memory_entity.UserActor(),
			})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Existed, convey.ShouldBeFalse)
			convey.So(res.Item.ID, convey.ShouldEqual, 11)
			convey.So(res.Item.Creator.Kind, convey.ShouldEqual, memory_entity.ActorUser)
		})

		convey.Convey("AI 写入记下来源 agent 与会话", func() {
			expectAIScope(m)
			m.memory.EXPECT().FindNormalBySummary(ctx, agentOwner, "用 pnpm").Return(nil, nil)
			m.memory.EXPECT().Create(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, e *memory_entity.Memory) error {
				convey.So(e.Creator(), convey.ShouldResemble, aiActor)
				convey.So(e.LastChange().Actor, convey.ShouldResemble, aiActor)
				return nil
			})
			res, err := svc.Create(ctx, &CreateRequest{Owner: agentOwner, Summary: "用 pnpm", Actor: aiActor})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Item.Creator, convey.ShouldResemble, memory_entity.AIActor(5, 70))
		})

		convey.Convey("同层已有摘要完全相同的正常条目时返回已有的那条，不新增", func() {
			expectAIScope(m)
			m.memory.EXPECT().FindNormalBySummary(ctx, projectOwner, "用 pnpm").Return(stored(4, projectOwner, "用 pnpm"), nil)
			res, err := svc.Create(ctx, &CreateRequest{Owner: projectOwner, Summary: "用 pnpm", Detail: "别的详情", Actor: aiActor})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Existed, convey.ShouldBeTrue)
			convey.So(res.Item.ID, convey.ShouldEqual, 4)
		})

		convey.Convey("归属不存在时拒绝", func() {
			m.dept.EXPECT().Find(ctx, int64(2)).Return(nil, nil)
			_, err := svc.Create(ctx, &CreateRequest{Owner: deptOwner, Summary: "x", Actor: memory_entity.UserActor()})
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryOwnerNotFound)
		})

		convey.Convey("归属形状非法时拒绝", func() {
			_, err := svc.Create(ctx, &CreateRequest{Owner: memory_entity.Owner{Kind: "team", ID: 1}, Summary: "x", Actor: memory_entity.UserActor()})
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryInvalidOwner)
		})

		convey.Convey("写入者非法时拒绝", func() {
			_, err := svc.Create(ctx, &CreateRequest{Owner: memory_entity.GlobalOwner(), Summary: "x", Actor: memory_entity.AIActor(5, 0)})
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryInvalidActor)
		})

		convey.Convey("内容检查不过时不落库", func() {
			_, err := svc.Create(ctx, &CreateRequest{Owner: memory_entity.GlobalOwner(), Summary: strings.Repeat("x", 201), Actor: memory_entity.UserActor()})
			convey.So(codeOf(err), convey.ShouldEqual, code.MemorySummaryTooLong)
			_, err = svc.Create(ctx, &CreateRequest{Owner: memory_entity.GlobalOwner(), Summary: "ok", Detail: "-----BEGIN EC PRIVATE KEY-----", Actor: memory_entity.UserActor()}) //nolint:gosec // G101: 凭据形状的测试夹具，断言它被拒绝。
			convey.So(codeOf(err), convey.ShouldEqual, code.MemorySensitiveContent)
		})
	})
}

func TestMemorySvc_Update(t *testing.T) {
	convey.Convey("修改记忆", t, func() {
		ctx, m, svc := setupMemorySvc(t)

		convey.Convey("AI 可以修改用户写的条目，上一版被保留", func() {
			expectAIScope(m)
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, agentOwner, "用 npm"), nil)
			m.memory.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, e *memory_entity.Memory) error {
				convey.So(e.Summary, convey.ShouldEqual, "用 pnpm")
				convey.So(e.PrevSummary, convey.ShouldEqual, "用 npm")
				convey.So(e.LastChange(), convey.ShouldResemble, memory_entity.Change{Action: memory_entity.ActionEdited, Actor: aiActor, At: testNow})
				convey.So(e.Creator(), convey.ShouldResemble, memory_entity.UserActor())
				return nil
			})
			item, err := svc.Update(ctx, &UpdateRequest{ID: 4, Summary: "用 pnpm", Actor: aiActor})
			convey.So(err, convey.ShouldBeNil)
			convey.So(item.HasPrevious, convey.ShouldBeTrue)
		})

		convey.Convey("内容没变时不写库", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, agentOwner, "用 npm"), nil)
			item, err := svc.Update(ctx, &UpdateRequest{ID: 4, Summary: "用 npm", Actor: memory_entity.UserActor()})
			convey.So(err, convey.ShouldBeNil)
			convey.So(item.HasPrevious, convey.ShouldBeFalse)
		})

		convey.Convey("条目不存在", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(nil, nil)
			_, err := svc.Update(ctx, &UpdateRequest{ID: 4, Summary: "x", Actor: memory_entity.UserActor()})
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryNotFound)
		})

		convey.Convey("新内容不合规时拒绝且不查库", func() {
			_, err := svc.Update(ctx, &UpdateRequest{ID: 4, Summary: "a\nb", Actor: memory_entity.UserActor()})
			convey.So(codeOf(err), convey.ShouldEqual, code.MemorySummaryMultiline)
		})
	})
}

func TestMemorySvc_StateChanges(t *testing.T) {
	convey.Convey("置顶、归档、恢复上一版、移层、彻底删除", t, func() {
		ctx, m, svc := setupMemorySvc(t)

		convey.Convey("置顶不算内容变更", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, agentOwner, "a"), nil)
			m.memory.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, e *memory_entity.Memory) error {
				convey.So(e.Pinned, convey.ShouldBeTrue)
				convey.So(e.ChangedAt, convey.ShouldEqual, 100)
				return nil
			})
			item, err := svc.SetPinned(ctx, 4, true)
			convey.So(err, convey.ShouldBeNil)
			convey.So(item.Pinned, convey.ShouldBeTrue)
		})

		convey.Convey("AI 归档记下归档者", func() {
			expectAIScope(m)
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, agentOwner, "a"), nil)
			m.memory.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, e *memory_entity.Memory) error {
				convey.So(e.State, convey.ShouldEqual, memory_entity.StateArchived)
				convey.So(e.LastChange().Actor, convey.ShouldResemble, aiActor)
				return nil
			})
			item, err := svc.Archive(ctx, 4, aiActor)
			convey.So(err, convey.ShouldBeNil)
			convey.So(item.State, convey.ShouldEqual, memory_entity.StateArchived)
		})

		convey.Convey("恢复已归档的条目", func() {
			e := stored(4, agentOwner, "a")
			e.Archive(aiActor, 150)
			m.memory.EXPECT().Find(ctx, int64(4)).Return(e, nil)
			m.memory.EXPECT().Update(ctx, gomock.Any()).Return(nil)
			item, err := svc.Unarchive(ctx, 4, memory_entity.UserActor())
			convey.So(err, convey.ShouldBeNil)
			convey.So(item.State, convey.ShouldEqual, memory_entity.StateNormal)
			convey.So(item.LastChange.Action, convey.ShouldEqual, memory_entity.ActionUnarchived)
		})

		convey.Convey("恢复上一版把当前与上一版对调", func() {
			e := stored(4, agentOwner, "旧")
			e.Edit("新", "", aiActor, 150)
			m.memory.EXPECT().Find(ctx, int64(4)).Return(e, nil)
			m.memory.EXPECT().Update(ctx, gomock.Any()).Return(nil)
			item, err := svc.RestorePrevious(ctx, 4, memory_entity.UserActor())
			convey.So(err, convey.ShouldBeNil)
			convey.So(item.Summary, convey.ShouldEqual, "旧")
			convey.So(item.PrevSummary, convey.ShouldEqual, "新")
		})

		convey.Convey("没有上一版时恢复被拒", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, agentOwner, "a"), nil)
			_, err := svc.RestorePrevious(ctx, 4, memory_entity.UserActor())
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryNoPrevious)
		})

		convey.Convey("移到另一层先校验目标归属", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, agentOwner, "a"), nil)
			m.dept.EXPECT().Find(ctx, int64(2)).Return(&department_entity.Department{ID: 2, Status: consts.ACTIVE}, nil)
			m.memory.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, e *memory_entity.Memory) error {
				convey.So(e.Owner(), convey.ShouldResemble, deptOwner)
				convey.So(e.LastChange().Action, convey.ShouldEqual, memory_entity.ActionMoved)
				return nil
			})
			item, err := svc.Move(ctx, 4, deptOwner, memory_entity.UserActor())
			convey.So(err, convey.ShouldBeNil)
			convey.So(item.Owner, convey.ShouldResemble, deptOwner)
		})

		convey.Convey("移到不存在的归属被拒", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, agentOwner, "a"), nil)
			m.project.EXPECT().Find(ctx, int64(9)).Return(nil, nil)
			_, err := svc.Move(ctx, 4, projectOwner, memory_entity.UserActor())
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryOwnerNotFound)
		})

		convey.Convey("彻底删除走仓储硬删", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, agentOwner, "a"), nil)
			m.memory.EXPECT().Delete(ctx, int64(4)).Return(nil)
			convey.So(svc.Delete(ctx, 4), convey.ShouldBeNil)
		})

		convey.Convey("删除不存在的条目", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(nil, nil)
			convey.So(codeOf(svc.Delete(ctx, 4)), convey.ShouldEqual, code.MemoryNotFound)
		})
	})
}

func summaries(items []*MemoryItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Summary)
	}
	return out
}

// budgetRows 造 n 条 200 字的正常条目，按修改时间从新到旧（id 1 最新）。
func budgetRows(owner memory_entity.Owner, n int) []*memory_entity.Memory {
	rows := make([]*memory_entity.Memory, 0, n)
	for i := 1; i <= n; i++ {
		e := stored(int64(i), owner, strings.Repeat(string(rune('a'+i-1)), memory_entity.SummaryMaxChars))
		e.ChangedAt = int64(1000 - i)
		rows = append(rows, e)
	}
	return rows
}

func TestMemorySvc_ListByOwner(t *testing.T) {
	convey.Convey("按归属列出", t, func() {
		ctx, m, svc := setupMemorySvc(t)

		convey.Convey("分成本次带上、超出预算、已归档三组，用量按全部正常条目计", func() {
			rows := budgetRows(deptOwner, 11)
			archived := stored(99, deptOwner, "旧约定")
			archived.Archive(memory_entity.UserActor(), 5000)
			rows = append(rows, archived)
			m.dept.EXPECT().Find(ctx, int64(2)).Return(&department_entity.Department{ID: 2, Status: consts.ACTIVE}, nil)
			m.memory.EXPECT().ListByOwner(ctx, deptOwner).Return(rows, nil)

			res, err := svc.ListByOwner(ctx, deptOwner)
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Budget, convey.ShouldEqual, memory_entity.LayerBudgetChars)
			convey.So(res.UsedChars, convey.ShouldEqual, 11*memory_entity.SummaryMaxChars)
			convey.So(len(res.Carried), convey.ShouldEqual, 10)
			convey.So(summaries(res.OverBudget), convey.ShouldResemble, []string{strings.Repeat("k", 200)})
			convey.So(summaries(res.Archived), convey.ShouldResemble, []string{"旧约定"})
		})

		convey.Convey("agent 归属带出它的记忆开关", func() {
			m.agent.EXPECT().Find(ctx, int64(5)).Return(&agent_entity.Agent{ID: 5, MemoryEnabled: false}, nil)
			m.memory.EXPECT().ListByOwner(ctx, agentOwner).Return(nil, nil)
			res, err := svc.ListByOwner(ctx, agentOwner)
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.MemoryEnabled, convey.ShouldBeFalse)
			convey.So(res.Carried, convey.ShouldBeEmpty)
		})

		convey.Convey("归属不存在", func() {
			m.agent.EXPECT().Find(ctx, int64(5)).Return(nil, nil)
			_, err := svc.ListByOwner(ctx, agentOwner)
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryOwnerNotFound)
		})
	})
}

func TestMemorySvc_CountsRecentSearch(t *testing.T) {
	convey.Convey("计数、最近变更、搜索", t, func() {
		ctx, m, svc := setupMemorySvc(t)

		convey.Convey("各归属的正常条目数", func() {
			m.memory.EXPECT().CountNormalByOwner(ctx).Return([]*memory_repo.OwnerCount{
				{Owner: memory_entity.GlobalOwner(), Count: 3},
				{Owner: agentOwner, Count: 2},
			}, nil)
			got, err := svc.CountsByOwner(ctx)
			convey.So(err, convey.ShouldBeNil)
			convey.So(got, convey.ShouldResemble, []*OwnerCount{
				{Owner: memory_entity.GlobalOwner(), Count: 3},
				{Owner: agentOwner, Count: 2},
			})
		})

		convey.Convey("最近变更取近 14 天", func() {
			m.memory.EXPECT().ListChangedSince(ctx, testNow-14*24*60*60*1000).Return([]*memory_entity.Memory{stored(1, agentOwner, "a")}, nil)
			got, err := svc.RecentChanges(ctx)
			convey.So(err, convey.ShouldBeNil)
			convey.So(summaries(got), convey.ShouldResemble, []string{"a"})
		})

		convey.Convey("搜索去掉首尾空白后交给仓储", func() {
			m.memory.EXPECT().Search(ctx, "pnpm").Return([]*memory_entity.Memory{stored(1, agentOwner, "用 pnpm")}, nil)
			got, err := svc.Search(ctx, "  pnpm ")
			convey.So(err, convey.ShouldBeNil)
			convey.So(summaries(got), convey.ShouldResemble, []string{"用 pnpm"})
		})

		convey.Convey("空搜索词不查库", func() {
			got, err := svc.Search(ctx, "   ")
			convey.So(err, convey.ShouldBeNil)
			convey.So(got, convey.ShouldBeEmpty)
		})
	})
}

func TestMemorySvc_Visible(t *testing.T) {
	convey.Convey("会话可见的记忆", t, func() {
		ctx, m, svc := setupMemorySvc(t)

		convey.Convey("全局 + 沿上级 agent 找到的部门及其上级 + agent + 项目，逐层按预算取舍", func() {
			// agent 5 挂在 agent 4 之下，agent 4 在部门 2，部门 2 的上级是部门 1。
			m.agent.EXPECT().Find(ctx, int64(5)).Return(&agent_entity.Agent{ID: 5, ParentAgentID: 4}, nil)
			m.agent.EXPECT().Find(ctx, int64(4)).Return(&agent_entity.Agent{ID: 4, DepartmentID: 2}, nil)
			m.dept.EXPECT().Find(ctx, int64(2)).Return(&department_entity.Department{ID: 2, ParentID: 1}, nil)
			m.dept.EXPECT().Find(ctx, int64(1)).Return(&department_entity.Department{ID: 1}, nil)
			root := memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 1}
			owners := []memory_entity.Owner{memory_entity.GlobalOwner(), root, deptOwner, agentOwner, projectOwner}
			rows := append(budgetRows(agentOwner, 11), stored(50, memory_entity.GlobalOwner(), "全局"), stored(51, root, "根部门"))
			m.memory.EXPECT().ListNormalByOwners(ctx, owners).Return(rows, nil)

			got, err := svc.Visible(ctx, 5, 9)
			convey.So(err, convey.ShouldBeNil)
			layerOwners := make([]memory_entity.Owner, 0, len(got.Layers))
			for _, l := range got.Layers {
				layerOwners = append(layerOwners, l.Owner)
			}
			convey.So(layerOwners, convey.ShouldResemble, owners)
			convey.So(summaries(got.Layers[0].Carried), convey.ShouldResemble, []string{"全局"})
			convey.So(summaries(got.Layers[1].Carried), convey.ShouldResemble, []string{"根部门"})
			convey.So(got.Layers[2].Carried, convey.ShouldBeEmpty)
			convey.So(len(got.Layers[3].Carried), convey.ShouldEqual, 10)
			convey.So(len(got.Layers[3].OverBudget), convey.ShouldEqual, 1)
			convey.So(got.Layers[3].Budget, convey.ShouldEqual, memory_entity.LayerBudgetChars)
			convey.So(got.Layers[4].Carried, convey.ShouldBeEmpty)
		})

		convey.Convey("没有项目、没有部门（系统 agent）时只有全局与 agent 两层", func() {
			m.agent.EXPECT().Find(ctx, int64(5)).Return(&agent_entity.Agent{ID: 5, SystemBadge: agent_entity.SystemBadgeDefault}, nil)
			m.memory.EXPECT().ListNormalByOwners(ctx, []memory_entity.Owner{memory_entity.GlobalOwner(), agentOwner}).Return(nil, nil)
			got, err := svc.Visible(ctx, 5, 0)
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(got.Layers), convey.ShouldEqual, 2)
		})

		convey.Convey("上级链成环时不死循环", func() {
			m.agent.EXPECT().Find(ctx, int64(5)).Return(&agent_entity.Agent{ID: 5, ParentAgentID: 4}, nil)
			m.agent.EXPECT().Find(ctx, int64(4)).Return(&agent_entity.Agent{ID: 4, ParentAgentID: 5}, nil)
			m.memory.EXPECT().ListNormalByOwners(ctx, []memory_entity.Owner{memory_entity.GlobalOwner(), agentOwner}).Return(nil, nil)
			_, err := svc.Visible(ctx, 5, 0)
			convey.So(err, convey.ShouldBeNil)
		})

		convey.Convey("agent 不存在", func() {
			m.agent.EXPECT().Find(ctx, int64(5)).Return(nil, nil)
			_, err := svc.Visible(ctx, 5, 9)
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryOwnerNotFound)
		})
	})
}

func TestMemorySvc_AllLayers(t *testing.T) {
	convey.Convey("会话以外看到的记忆：所有有正常条目的层", t, func() {
		ctx, m, svc := setupMemorySvc(t)

		convey.Convey("全局恒在最前，其余按部门、agent、项目再按编号排，逐层按预算取舍", func() {
			m.memory.EXPECT().CountNormalByOwner(ctx).Return([]*memory_repo.OwnerCount{
				{Owner: projectOwner, Count: 1},
				{Owner: agentOwner, Count: 11},
				{Owner: memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 7}, Count: 1},
				{Owner: deptOwner, Count: 1},
			}, nil)
			dept7 := memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 7}
			owners := []memory_entity.Owner{memory_entity.GlobalOwner(), deptOwner, dept7, agentOwner, projectOwner}
			rows := append(budgetRows(agentOwner, 11), stored(51, deptOwner, "部门"), stored(52, dept7, "另一个部门"), stored(53, projectOwner, "项目"))
			m.memory.EXPECT().ListNormalByOwners(ctx, owners).Return(rows, nil)

			got, err := svc.AllLayers(ctx)
			convey.So(err, convey.ShouldBeNil)
			layerOwners := make([]memory_entity.Owner, 0, len(got.Layers))
			for _, l := range got.Layers {
				layerOwners = append(layerOwners, l.Owner)
			}
			convey.So(layerOwners, convey.ShouldResemble, owners)
			convey.So(got.Layers[0].Carried, convey.ShouldBeEmpty)
			convey.So(summaries(got.Layers[1].Carried), convey.ShouldResemble, []string{"部门"})
			convey.So(len(got.Layers[3].Carried), convey.ShouldEqual, 10)
			convey.So(len(got.Layers[3].OverBudget), convey.ShouldEqual, 1)
		})

		convey.Convey("计数读不出来时报错", func() {
			m.memory.EXPECT().CountNormalByOwner(ctx).Return(nil, errors.New("db down"))
			_, err := svc.AllLayers(ctx)
			convey.So(err, convey.ShouldNotBeNil)
		})
	})
}

func TestMemorySvc_CountOwned(t *testing.T) {
	convey.Convey("删除归属时会一并删掉的记忆条数", t, func() {
		ctx, m, svc := setupMemorySvc(t)

		convey.Convey("数的是这些归属名下的全部条目，已归档的也算（它们同样被删）", func() {
			owners := []memory_entity.Owner{agentOwner, deptOwner}
			m.memory.EXPECT().CountByOwners(ctx, owners).Return(int64(6), nil)
			got, err := svc.CountOwned(ctx, owners)
			convey.So(err, convey.ShouldBeNil)
			convey.So(got, convey.ShouldEqual, 6)
		})

		convey.Convey("形状不合法的归属被拒，不查库", func() {
			_, err := svc.CountOwned(ctx, []memory_entity.Owner{{Kind: memory_entity.OwnerAgent}})
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryInvalidOwner)
		})
	})
}

// 记忆的来源会话被删之后，记忆页的来源链接必须不可点：存在性由本机会话表回答，
// 且一次批量查询、软删的会话算不存在。
func TestMemorySvc_CreatorSessionExists(t *testing.T) {
	convey.Convey("来源会话是否还在本机", t, func() {
		ctx, m, svc := setupMemorySvc(t)
		aiMemory := func(id, agentID, sessionID int64) *memory_entity.Memory {
			mem := memory_entity.NewMemory(agentOwner, "s", "", memory_entity.AIActor(agentID, sessionID), 100)
			mem.ID = id
			return mem
		}

		convey.Convey("列表只发一次会话查询：还在的为真，已删的为假，用户写的为假", func() {
			rows := []*memory_entity.Memory{aiMemory(1, 5, 70), aiMemory(2, 5, 71), aiMemory(3, 6, 80), stored(4, agentOwner, "u")}
			m.agent.EXPECT().Find(ctx, int64(5)).Return(&agent_entity.Agent{ID: 5}, nil)
			m.memory.EXPECT().ListByOwner(ctx, agentOwner).Return(rows, nil)
			m.session.EXPECT().ListIDsByAgents(ctx, gomock.InAnyOrder([]int64{5, 6})).
				Return(map[int64][]int64{5: {70}}, nil).Times(1)

			res, err := svc.ListByOwner(ctx, agentOwner)
			convey.So(err, convey.ShouldBeNil)
			got := map[int64]bool{}
			for _, it := range res.Carried {
				got[it.ID] = it.CreatorSessionExists
			}
			convey.So(got, convey.ShouldResemble, map[int64]bool{1: true, 2: false, 3: false, 4: false})
		})

		convey.Convey("没有 AI 来源时不查会话", func() {
			m.memory.EXPECT().Search(ctx, "x").Return([]*memory_entity.Memory{stored(1, agentOwner, "x")}, nil)
			got, err := svc.Search(ctx, "x")
			convey.So(err, convey.ShouldBeNil)
			convey.So(got[0].CreatorSessionExists, convey.ShouldBeFalse)
		})

		convey.Convey("最近变更与单条读取同样带存在性", func() {
			m.memory.EXPECT().ListChangedSince(ctx, gomock.Any()).Return([]*memory_entity.Memory{aiMemory(1, 5, 70)}, nil)
			m.session.EXPECT().ListIDsByAgents(ctx, []int64{5}).Return(map[int64][]int64{5: {70}}, nil)
			recent, err := svc.RecentChanges(ctx)
			convey.So(err, convey.ShouldBeNil)
			convey.So(recent[0].CreatorSessionExists, convey.ShouldBeTrue)

			m.memory.EXPECT().Find(ctx, int64(2)).Return(aiMemory(2, 5, 71), nil)
			m.session.EXPECT().ListIDsByAgents(ctx, []int64{5}).Return(map[int64][]int64{5: {70}}, nil)
			one, err := svc.Get(ctx, 2)
			convey.So(err, convey.ShouldBeNil)
			convey.So(one.CreatorSessionExists, convey.ShouldBeFalse)
		})

		convey.Convey("会话查询失败时整个读取失败，不把未知当成存在", func() {
			m.memory.EXPECT().Search(ctx, "x").Return([]*memory_entity.Memory{aiMemory(1, 5, 70)}, nil)
			m.session.EXPECT().ListIDsByAgents(ctx, []int64{5}).Return(nil, errors.New("db closed"))
			_, err := svc.Search(ctx, "x")
			convey.So(err, convey.ShouldNotBeNil)
		})
	})
}
