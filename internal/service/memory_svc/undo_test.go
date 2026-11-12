package memory_svc

import (
	"context"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
)

// 对话里记忆卡片的撤销：新增 → 彻底删除，修改 → 恢复上一版，归档 → 恢复为正常。
// 条目在这次写入之后又被改过（最近一次变更不再是「这个会话里的 AI 做的这种写入」）时拒绝，
// 什么都不动。
func TestMemorySvc_UndoWrite(t *testing.T) {
	convey.Convey("撤销一次 AI 写入", t, func() {
		ctx, m, svc := setupMemorySvc(t)
		other := memory_entity.AIActor(5, 99)

		aiCreated := func() *memory_entity.Memory {
			return memory_entity.NewMemory(agentOwner, "用 pnpm", "", aiActor, 100)
		}
		aiEdited := func() *memory_entity.Memory {
			e := stored(4, agentOwner, "旧")
			e.Edit("新", "", aiActor, 200)
			return e
		}
		aiArchived := func() *memory_entity.Memory {
			e := stored(4, agentOwner, "旧")
			e.Archive(aiActor, 200)
			return e
		}

		convey.Convey("新增的：彻底删除", func() {
			e := aiCreated()
			e.ID = 4
			m.memory.EXPECT().Find(ctx, int64(4)).Return(e, nil).Times(2)
			m.memory.EXPECT().Delete(ctx, int64(4)).Return(nil)
			res, err := svc.UndoWrite(ctx, &UndoWriteRequest{ID: 4, SessionID: 70, Action: WriteCreated})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Status, convey.ShouldEqual, UndoDone)
		})

		convey.Convey("修改的：恢复上一版，由用户记名", func() {
			e := aiEdited()
			e.ID = 4
			m.memory.EXPECT().Find(ctx, int64(4)).Return(e, nil).Times(2)
			m.memory.EXPECT().Update(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, got *memory_entity.Memory) error {
				convey.So(got.Summary, convey.ShouldEqual, "旧")
				convey.So(got.LastChange().Actor, convey.ShouldResemble, memory_entity.UserActor())
				return nil
			})
			res, err := svc.UndoWrite(ctx, &UndoWriteRequest{ID: 4, SessionID: 70, Action: WriteUpdated})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Status, convey.ShouldEqual, UndoDone)
			convey.So(res.Item.Summary, convey.ShouldEqual, "旧")
		})

		convey.Convey("归档的：恢复为正常", func() {
			e := aiArchived()
			e.ID = 4
			m.memory.EXPECT().Find(ctx, int64(4)).Return(e, nil).Times(2)
			m.memory.EXPECT().Update(ctx, gomock.Any()).Return(nil)
			res, err := svc.UndoWrite(ctx, &UndoWriteRequest{ID: 4, SessionID: 70, Action: WriteArchived})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Status, convey.ShouldEqual, UndoDone)
			convey.So(res.Item.State, convey.ShouldEqual, memory_entity.StateNormal)
		})

		convey.Convey("写入之后用户又改过：拒绝，不落库", func() {
			e := aiCreated()
			e.ID = 4
			e.Edit("用 pnpm 10", "", memory_entity.UserActor(), 300)
			m.memory.EXPECT().Find(ctx, int64(4)).Return(e, nil)
			res, err := svc.UndoWrite(ctx, &UndoWriteRequest{ID: 4, SessionID: 70, Action: WriteCreated})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Status, convey.ShouldEqual, UndoChanged)
		})

		convey.Convey("最近一次变更是别的会话里的 AI：拒绝", func() {
			e := aiEdited()
			e.ID = 4
			e.Edit("再改", "", other, 300)
			m.memory.EXPECT().Find(ctx, int64(4)).Return(e, nil)
			res, err := svc.UndoWrite(ctx, &UndoWriteRequest{ID: 4, SessionID: 70, Action: WriteUpdated})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Status, convey.ShouldEqual, UndoChanged)
		})

		convey.Convey("新增后又被归档：拒绝，不删除", func() {
			e := aiCreated()
			e.ID = 4
			e.Archive(memory_entity.UserActor(), 300)
			m.memory.EXPECT().Find(ctx, int64(4)).Return(e, nil)
			res, err := svc.UndoWrite(ctx, &UndoWriteRequest{ID: 4, SessionID: 70, Action: WriteCreated})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Status, convey.ShouldEqual, UndoChanged)
		})

		convey.Convey("条目已不存在：报 gone，不当作错误", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(nil, nil)
			res, err := svc.UndoWrite(ctx, &UndoWriteRequest{ID: 4, SessionID: 70, Action: WriteCreated})
			convey.So(err, convey.ShouldBeNil)
			convey.So(res.Status, convey.ShouldEqual, UndoGone)
		})

		convey.Convey("未知的写入动作或缺会话：参数错误", func() {
			_, err := svc.UndoWrite(ctx, &UndoWriteRequest{ID: 4, SessionID: 70, Action: "bogus"})
			convey.So(err, convey.ShouldNotBeNil)
			_, err = svc.UndoWrite(ctx, &UndoWriteRequest{ID: 4, Action: WriteCreated})
			convey.So(err, convey.ShouldNotBeNil)
		})
	})
}

func TestMemorySvc_Get(t *testing.T) {
	convey.Convey("按编号读一条记忆", t, func() {
		ctx, m, svc := setupMemorySvc(t)
		convey.Convey("存在：返回视图", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(stored(4, agentOwner, "x"), nil)
			item, err := svc.Get(ctx, 4)
			convey.So(err, convey.ShouldBeNil)
			convey.So(item.Summary, convey.ShouldEqual, "x")
		})
		convey.Convey("不存在：返回 nil 而不是错误", func() {
			m.memory.EXPECT().Find(ctx, int64(4)).Return(nil, nil)
			item, err := svc.Get(ctx, 4)
			convey.So(err, convey.ShouldBeNil)
			convey.So(item, convey.ShouldBeNil)
		})
	})
}
