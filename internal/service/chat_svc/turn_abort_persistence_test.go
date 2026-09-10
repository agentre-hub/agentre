package chat_svc

import (
	"context"
	"errors"
	"sync"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
)

// 回归:用户在 runner.Run 还没返回时点「停止」(OpenClaw 要先跟网关握手 + 首个 RPC,
// 这个窗口有实测 0.5~1s),turnCtx 被 cancel → Run 带着 context.Canceled 返回 →
// failTurn 用同一个已 cancel 的 ctx 写库,GORM 直接失败且被 `_ =` 吞掉:
// agent_status 永远停在 running(侧栏一直转圈)、error_text 也是空的(前端不显示任何报错),
// 只有重启 app 靠 ResetActiveSessions 才洗得掉。实测 3/3 必现。
func TestFailTurn_PersistsWithDetachedContext(t *testing.T) {
	Convey("failTurn 在 turnCtx 已 cancel 时仍必须把终态落库", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
		msgRepo := mock_chat_repo.NewMockMessageRepo(ctrl)
		prevSess, prevMsg := chat_repo.Session(), chat_repo.Message()
		chat_repo.RegisterSession(sessRepo)
		chat_repo.RegisterMessage(msgRepo)
		t.Cleanup(func() {
			chat_repo.RegisterSession(prevSess)
			chat_repo.RegisterMessage(prevMsg)
		})

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		var persistedStatus, persistedErrText string
		sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, sess *chat_entity.Session) error {
				So(ctx.Err(), ShouldBeNil)
				persistedStatus = sess.AgentStatus
				return nil
			},
		)
		msgRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, msg *chat_entity.Message) error {
				So(ctx.Err(), ShouldBeNil)
				persistedErrText = msg.ErrorText
				return nil
			},
		)

		svc := &chatSvc{emitter: NoopEmitter{}}
		sess := &chat_entity.Session{ID: 7, AgentStatus: "running"}
		msg := &chat_entity.Message{ID: 9, SessionID: 7, Role: "assistant"}
		svc.failTurn(canceledCtx, sess, msg, "chat:event:7:9", errors.New("openclaw runtime: boom"))

		So(persistedStatus, ShouldEqual, "error")
		So(persistedErrText, ShouldNotBeEmpty)
	})
}

// 回归:用户主动点「停止」不是「出错」。Run 在注册 activeTurn 之前被 cancel 时,
// 这一轮必须收敛成 idle(和流式中途 abort 一致),而不是 agentStatus=error + 报错卡。
func TestRunTurn_AbortBeforeStreamConvergesToIdle(t *testing.T) {
	Convey("Run 返回前被用户 Stop → 收敛为 idle,不落 error", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)
		sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
		msgRepo := mock_chat_repo.NewMockMessageRepo(ctrl)
		prevSess, prevMsg := chat_repo.Session(), chat_repo.Message()
		chat_repo.RegisterSession(sessRepo)
		chat_repo.RegisterMessage(msgRepo)
		t.Cleanup(func() {
			chat_repo.RegisterSession(prevSess)
			chat_repo.RegisterMessage(prevMsg)
		})

		canceledCtx, cancel := context.WithCancel(context.Background())
		cancel()

		var persistedStatus string
		sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, sess *chat_entity.Session) error {
				So(ctx.Err(), ShouldBeNil)
				persistedStatus = sess.AgentStatus
				return nil
			},
		)
		msgRepo.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, msg *chat_entity.Message) error {
				So(ctx.Err(), ShouldBeNil)
				So(msg.ErrorText, ShouldBeEmpty)
				return nil
			},
		)

		svc := &chatSvc{emitter: NoopEmitter{}, aborted: &sync.Map{}}
		sess := &chat_entity.Session{ID: 11, AgentStatus: "running"}
		msg := &chat_entity.Message{ID: 12, SessionID: 11, Role: "assistant"}

		svc.aborted.Store(sess.ID, struct{}{})
		So(svc.turnAbortedByUser(sess.ID, context.Canceled), ShouldBeTrue)
		svc.abortTurnBeforeStream(canceledCtx, sess, msg, "chat:event:11:12")

		So(persistedStatus, ShouldEqual, "idle")
	})
}

// runtime 明确返回 ErrAborted 时同样按中止处理(不依赖 aborted 标记的时序)。
func TestTurnAbortedByUser_HonoursErrAborted(t *testing.T) {
	Convey("ErrAborted 直接判为用户中止", t, func() {
		svc := &chatSvc{emitter: NoopEmitter{}, aborted: &sync.Map{}}
		So(svc.turnAbortedByUser(3, agentruntime.ErrAborted), ShouldBeTrue)
		// 没有 abort 标记时,普通错误仍然是错误。
		So(svc.turnAbortedByUser(3, errors.New("dial tcp: connection refused")), ShouldBeFalse)
		// 有 abort 标记但错误无关 cancel —— 仍按错误处理,避免掩盖真故障。
		svc.aborted.Store(int64(4), struct{}{})
		So(svc.turnAbortedByUser(4, errors.New("boom")), ShouldBeFalse)
	})
}
