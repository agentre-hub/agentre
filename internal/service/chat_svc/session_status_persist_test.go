package chat_svc

import (
	"context"
	"errors"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
)

// Regression: session 162 — turn 在 abort 时 finalize 成 idle 并调了 Update,
// 但那一刀写库失败、错误被 `_ =` 静默吞掉,session 永久停在 running
// (只能等下次启动 ResetActiveSessions 翻成 error)。
//
// 修复:finalize 的状态写入走 persistSessionStatus —— 写失败时重试一次,
// 仍失败则返回错误(不再静默),让上层至少能记日志/感知。
func TestPersistSessionStatus_RetriesOnTransientFailure(t *testing.T) {
	Convey("写库瞬时失败时重试一次,第二次成功则收尾状态不丢", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
		prev := chat_repo.Session()
		chat_repo.RegisterSession(sessRepo)
		t.Cleanup(func() { chat_repo.RegisterSession(prev) })

		sess := &chat_entity.Session{ID: 162, AgentStatus: "idle"}

		// 第一次失败(模拟 database is locked / 收尾期写竞争),第二次成功。
		gomock.InOrder(
			sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(errors.New("database is locked")),
			sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil),
		)

		svc := &chatSvc{emitter: NoopEmitter{}}
		err := svc.persistSessionStatus(context.Background(), sess)
		So(err, ShouldBeNil)
	})
}

func TestPersistSessionStatus_SurfacesPersistentFailure(t *testing.T) {
	Convey("重试后仍失败时把错误返回出来,不再静默吞掉", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
		prev := chat_repo.Session()
		chat_repo.RegisterSession(sessRepo)
		t.Cleanup(func() { chat_repo.RegisterSession(prev) })

		sess := &chat_entity.Session{ID: 162, AgentStatus: "idle"}

		boom := errors.New("database is locked")
		sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(boom).Times(2)

		svc := &chatSvc{emitter: NoopEmitter{}}
		err := svc.persistSessionStatus(context.Background(), sess)
		So(err, ShouldNotBeNil)
	})
}
