package chat_svc

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/turn"
)

// §1.4 WithoutCancel 抗 abort — characterization tests
//
// 关键 pin: 用户点 Stop → turnCtx 被 cancel。但「等待状态」(AgentStatus=waiting / NeedsAttention)
// 仍必须落库,否则下次 LoadSession 显示旧的 running 态、sidebar attention 也丢。
// chat.go 在所有副作用持久化处都用 context.WithoutCancel(ctx) 剥离 cancel,本测试 pin 这点。
func TestCharacterization_Persistence_MarkWaitingUsesWithoutCancel(t *testing.T) {
	Convey("§1.4 markSessionWaiting 即便 ctx 已 cancel 仍能落库", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
		prev := chat_repo.Session()
		chat_repo.RegisterSession(sessRepo)
		t.Cleanup(func() { chat_repo.RegisterSession(prev) })

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel() // 立刻 cancel —— 模拟用户点 Stop

		sess := &chat_entity.Session{ID: 1, AgentStatus: "running"}

		// 期望:Update 收到的 ctx 不是 canceled 状态
		sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, _ *chat_entity.Session) error {
				So(ctx.Err(), ShouldBeNil)
				return nil
			},
		)

		svc := &chatSvc{emitter: NoopEmitter{}}
		svc.markSessionWaiting(canceledCtx, sess, "stream-1")
		So(sess.AgentStatus, ShouldEqual, "waiting")
		So(sess.NeedsAttention, ShouldBeTrue)
	})
}

func TestCharacterization_Persistence_CheckpointAssistantSurvivesCancel(t *testing.T) {
	Convey("§1.4 checkpointAssistant 即便 ctx canceled 仍能 Update message", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		msgRepo := mock_chat_repo.NewMockMessageRepo(ctrl)
		prev := chat_repo.Message()
		chat_repo.RegisterMessage(msgRepo)
		t.Cleanup(func() { chat_repo.RegisterMessage(prev) })

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		msgRepo.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, _ *chat_entity.Message, _ string) error {
				So(ctx.Err(), ShouldBeNil)
				return nil
			},
		)

		acc := turn.New()
		acc.AddText("partial assistant output")
		msg := &chat_entity.Message{ID: 1, SessionID: 1, Role: "assistant"}
		svc := &chatSvc{emitter: NoopEmitter{}}
		svc.checkpointAssistantNew(canceledCtx, msg, acc)
	})
}
