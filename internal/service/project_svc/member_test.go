package project_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
	"github.com/agentre-hub/agentre/internal/repository/project_repo/mock_project_repo"
	"github.com/agentre-hub/agentre/internal/service/project_svc"
	"github.com/agentre-hub/agentre/internal/service/project_svc/mock_project_svc"
)

// 构造 3 层项目树：
//
//	A (id=1, parent=0) ─ 直接成员 [10, 20]
//	   B (id=2, parent=1) ─ 直接成员 [30]
//	      C (id=3, parent=2) ─ 直接成员 [40, 20]  ← agent 20 既是自身也是 A 的成员
//
// 期望 Get(C) 返回:
//   - direct = [40, 20]
//   - inherited = [30 来自 B, 10 来自 A]（20 已在 direct 里去重）
func TestAggregateMembers_ThreeLevelInheritance(t *testing.T) {
	convey.Convey("3 层项目树成员聚合", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		mockProj := mock_project_repo.NewMockProjectRepo(ctrl)
		mockPA := mock_project_repo.NewMockProjectAgentRepo(ctrl)
		mockAgent := mock_project_svc.NewMockAgentPort(ctrl)
		project_repo.RegisterProject(mockProj)
		project_repo.RegisterProjectAgent(mockPA)
		svc := project_svc.New(project_svc.WithAgentPort(mockAgent))

		ctx := context.Background()
		projA := &project_entity.Project{ID: 1, Name: "A", ParentID: 0, Status: consts.ACTIVE}
		projB := &project_entity.Project{ID: 2, Name: "B", ParentID: 1, Status: consts.ACTIVE}
		projC := &project_entity.Project{ID: 3, Name: "C", ParentID: 2, Status: consts.ACTIVE}

		// Get(C) 流程：
		//   Find(3) → projC
		//   aggregateMembers 沿 parent_id 上溯找 B、A → 共调 Find(2) + Find(1)
		//   再 ListByProjects([3,2,1]) 一次拿到所有成员
		mockProj.EXPECT().Find(ctx, int64(3)).Return(projC, nil)
		mockProj.EXPECT().Find(ctx, int64(2)).Return(projB, nil)
		mockProj.EXPECT().Find(ctx, int64(1)).Return(projA, nil)
		mockPA.EXPECT().ListByProjects(ctx, []int64{3, 2, 1}).Return(map[int64][]*project_entity.ProjectAgent{
			3: {{ProjectID: 3, AgentID: 40, JoinedAt: 1}, {ProjectID: 3, AgentID: 20, JoinedAt: 2}},
			2: {{ProjectID: 2, AgentID: 30, JoinedAt: 3}},
			1: {{ProjectID: 1, AgentID: 10, JoinedAt: 4}, {ProjectID: 1, AgentID: 20, JoinedAt: 5}},
		}, nil)
		mockAgent.EXPECT().List(ctx).Return([]*agent_entity.Agent{
			{ID: 10, Name: "Alpha", AvatarColor: "agent-1"},
			{ID: 20, Name: "Builder", AvatarColor: "agent-2", AvatarIcon: "hammer"},
			{ID: 30, Name: "Coder", AvatarColor: "agent-3", AvatarDataURL: "data:image/png;base64,Yw=="},
			{ID: 40, Name: "Designer", AvatarColor: "agent-4"},
		}, nil)

		detail, err := svc.Get(ctx, 3)
		require.NoError(t, err)

		convey.Convey("direct 成员是 C 自己声明的两个 agent", func() {
			ids := agentIDs(detail.DirectMembers)
			convey.So(ids, convey.ShouldContain, int64(40))
			convey.So(ids, convey.ShouldContain, int64(20))
			convey.So(len(ids), convey.ShouldEqual, 2)
		})

		convey.Convey("inherited 成员去重后包含来自 B / A 的非重复 agent", func() {
			ids := agentIDs(detail.InheritedMembers)
			convey.So(ids, convey.ShouldContain, int64(30))
			convey.So(ids, convey.ShouldContain, int64(10))
			convey.So(ids, convey.ShouldNotContain, int64(20)) // 20 已在 direct
			convey.So(len(ids), convey.ShouldEqual, 2)
		})

		convey.Convey("inherited 成员有 FromName 标记继承来源", func() {
			byID := map[int64]*project_svc.ProjectAgentMember{}
			for _, m := range detail.InheritedMembers {
				byID[m.AgentID] = m
			}
			convey.So(byID[int64(30)].FromName, convey.ShouldEqual, "B")
			convey.So(byID[int64(10)].FromName, convey.ShouldEqual, "A")
		})

		convey.Convey("成员视图带 Agent 展示字段，前端不需要再靠 ID 猜名字", func() {
			byID := map[int64]*project_svc.ProjectAgentMember{}
			for _, m := range append(detail.DirectMembers, detail.InheritedMembers...) {
				byID[m.AgentID] = m
			}
			convey.So(byID[int64(20)].AgentName, convey.ShouldEqual, "Builder")
			convey.So(byID[int64(20)].AvatarIcon, convey.ShouldEqual, "hammer")
			convey.So(byID[int64(30)].AgentName, convey.ShouldEqual, "Coder")
			convey.So(byID[int64(30)].AvatarDataURL, convey.ShouldEqual, "data:image/png;base64,Yw==")
		})
	})
}

func TestAggregateMembers_StaleAgentRelationKeepsMemberRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	mockProj := mock_project_repo.NewMockProjectRepo(ctrl)
	mockPA := mock_project_repo.NewMockProjectAgentRepo(ctrl)
	mockAgent := mock_project_svc.NewMockAgentPort(ctrl)
	project_repo.RegisterProject(mockProj)
	project_repo.RegisterProjectAgent(mockPA)
	svc := project_svc.New(project_svc.WithAgentPort(mockAgent))

	ctx := context.Background()
	project := &project_entity.Project{ID: 1, Name: "A", ParentID: 0, Status: consts.ACTIVE}
	mockProj.EXPECT().Find(ctx, int64(1)).Return(project, nil)
	mockPA.EXPECT().ListByProjects(ctx, []int64{1}).Return(map[int64][]*project_entity.ProjectAgent{
		1: {{ProjectID: 1, AgentID: 99, JoinedAt: 1}},
	}, nil)
	mockAgent.EXPECT().List(ctx).Return([]*agent_entity.Agent{}, nil)

	detail, err := svc.Get(ctx, 1)
	require.NoError(t, err)
	require.Len(t, detail.DirectMembers, 1)
	require.Equal(t, int64(99), detail.DirectMembers[0].AgentID)
	require.Empty(t, detail.DirectMembers[0].AgentName)
}

func agentIDs(ms []*project_svc.ProjectAgentMember) []int64 {
	out := make([]int64, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.AgentID)
	}
	return out
}
