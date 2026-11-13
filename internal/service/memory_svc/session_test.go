package memory_svc

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// 决策 7 的硬规则之一：AI 只能写它所属的层——本会话看得到的那几层（全局、agent 所在部门
// 及其上级、agent 自己、会话所在项目）。用户（记忆页）不受这条限制。
func TestMemorySvc_AIWritesOnlyItsOwnLayers(t *testing.T) {
	convey.Convey("AI 只能写它所属的层", t, func() {
		ctx, m, svc := setupMemorySvc(t)
		expectAIScope(m)
		upperDept := memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 1}
		otherDept := memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 6}
		otherAgent := memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: 8}
		otherProject := memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 3}

		convey.Convey("写到所在部门的上级部门：照常新增", func() {
			m.memory.EXPECT().FindNormalBySummary(ctx, upperDept, "迁移只追加").Return(nil, nil)
			m.memory.EXPECT().Create(ctx, gomock.Any()).Return(nil)
			res, err := svc.Create(ctx, &CreateRequest{Owner: upperDept, Summary: "迁移只追加", Actor: aiActor})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Item.Owner, convey.ShouldResemble, upperDept)
		})

		convey.Convey("写到全局与会话所在项目：照常新增", func() {
			for _, owner := range []memory_entity.Owner{memory_entity.GlobalOwner(), projectOwner} {
				m.memory.EXPECT().FindNormalBySummary(ctx, owner, "x").Return(nil, nil)
				m.memory.EXPECT().Create(ctx, gomock.Any()).Return(nil)
				_, err := svc.Create(ctx, &CreateRequest{Owner: owner, Summary: "x", Actor: aiActor})
				convey.So(err, convey.ShouldBeNil)
			}
		})

		convey.Convey("写到不属于它的部门、别的 agent、会话以外的项目：拒绝且不落库", func() {
			for _, owner := range []memory_entity.Owner{otherDept, otherAgent, otherProject} {
				_, err := svc.Create(ctx, &CreateRequest{Owner: owner, Summary: "x", Actor: aiActor})
				convey.So(codeOf(err), convey.ShouldEqual, code.MemoryNotOwnLayer)
			}
		})

		convey.Convey("同样的层，用户写入不受限", func() {
			m.dept.EXPECT().Find(ctx, int64(6)).Return(nil, nil)
			_, err := svc.Create(ctx, &CreateRequest{Owner: otherDept, Summary: "x", Actor: memory_entity.UserActor()})
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryOwnerNotFound)
		})

		convey.Convey("修改不属于它的层里的条目：拒绝且不落库", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, otherAgent, "别人的"), nil)
			_, err := svc.Update(ctx, &UpdateRequest{ID: 4, Summary: "改掉", Actor: aiActor})
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryNotOwnLayer)
		})

		convey.Convey("归档不属于它的层里的条目：拒绝且不落库", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, otherProject, "别的项目"), nil)
			_, err := svc.Archive(ctx, 4, aiActor)
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryNotOwnLayer)
		})

		convey.Convey("把自己层里的条目移到不属于它的层：拒绝且不落库", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, agentOwner, "a"), nil)
			_, err := svc.Move(ctx, 4, otherDept, aiActor)
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryNotOwnLayer)
		})

		convey.Convey("会话已不存在时没有项目层可写", func() {
			_, m2, svc2 := setupMemorySvc(t)
			m2.session.EXPECT().Find(gomock.Any(), int64(71)).Return(nil, nil).AnyTimes()
			m2.agent.EXPECT().Find(gomock.Any(), int64(5)).Return(&agent_entity.Agent{ID: 5}, nil).AnyTimes()
			m2.project.EXPECT().Find(gomock.Any(), int64(9)).Return(&project_entity.Project{ID: 9}, nil).AnyTimes()
			_, err := svc2.Create(context.Background(), &CreateRequest{Owner: projectOwner, Summary: "x", Actor: memory_entity.AIActor(5, 71)})
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryNotOwnLayer)
		})
	})
}

func TestMemorySvc_SessionVisible(t *testing.T) {
	convey.Convey("AI 写入者所在会话看得到的记忆", t, func() {
		ctx, m, svc := setupMemorySvc(t)

		convey.Convey("项目取自会话本身：全局、部门链、agent、会话所在项目", func() {
			expectAIScope(m)
			root := memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 1}
			owners := []memory_entity.Owner{memory_entity.GlobalOwner(), root, deptOwner, agentOwner, projectOwner}
			m.memory.EXPECT().ListNormalByOwners(ctx, owners).Return([]*memory_entity.Memory{stored(7, projectOwner, "联调机在内网")}, nil)
			got, err := svc.SessionVisible(ctx, aiActor)
			convey.So(err, convey.ShouldBeNil)
			convey.So(len(got.Layers), convey.ShouldEqual, 5)
			convey.So(summaries(got.Layers[4].Carried), convey.ShouldResemble, []string{"联调机在内网"})
		})

		convey.Convey("不是 AI 写入者时拒绝", func() {
			_, err := svc.SessionVisible(ctx, memory_entity.UserActor())
			convey.So(codeOf(err), convey.ShouldEqual, code.MemoryInvalidActor)
		})
	})
}
