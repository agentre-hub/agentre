package chat_svc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

func TestObserveTurn_ReceivesTerminalOnce(t *testing.T) {
	Convey("订阅后, publish 的终态应被收到恰好一条", t, func() {
		svc := chat_svc.NewChatForTest(chat_svc.NoopEmitter{})
		ch, cancel := svc.ObserveTurn(42)
		defer cancel()
		svc.PublishTurnResultForTest(42, chat_svc.TurnResult{SessionID: 42, Aborted: true})

		select {
		case r := <-ch:
			So(r.SessionID, ShouldEqual, 42)
			So(r.Aborted, ShouldBeTrue)
		case <-time.After(time.Second):
			t.Fatal("未收到 TurnResult")
		}
	})

	Convey("无订阅者时 publish 不 panic", t, func() {
		svc := chat_svc.NewChatForTest(chat_svc.NoopEmitter{})
		So(func() { svc.PublishTurnResultForTest(99, chat_svc.TurnResult{Err: errors.New("x")}) }, ShouldNotPanic)
	})
}

// TestFailTurn_PublishesExactlyOneErrTerminal 驱动真实 failTurn, 验证错误路径
// 也回灌恰好一条 Err != nil 的终态给订阅者(failTurn 直线无内部 early return,
// 尾端单点 publish 即覆盖全部退出路径)。failTurn 会 UPDATE chat_messages /
// chat_sessions, 用 gomock repo 满足这两次写入, 不连真库。
func TestFailTurn_PublishesExactlyOneErrTerminal(t *testing.T) {
	Convey("failTurn 应向订阅者回灌恰好一条带错误的终态", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		msgRepo := mock_chat_repo.NewMockMessageRepo(ctrl)
		prevMsg := chat_repo.Message()
		chat_repo.RegisterMessage(msgRepo)
		t.Cleanup(func() { chat_repo.RegisterMessage(prevMsg) })

		sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
		prevSess := chat_repo.Session()
		chat_repo.RegisterSession(sessRepo)
		t.Cleanup(func() { chat_repo.RegisterSession(prevSess) })

		// failTurn 写一次 message + 一次 session, 都成功即可。
		msgRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
		sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

		svc := chat_svc.NewChatForTest(chat_svc.NoopEmitter{})
		ch, cancel := svc.ObserveTurn(7)
		defer cancel()

		boom := errors.New("turn boom")
		svc.FailTurnForTest(context.Background(), 7, 11, "stream-7", boom)

		select {
		case r := <-ch:
			So(r.SessionID, ShouldEqual, 7)
			So(r.AssistantMessageID, ShouldEqual, 11)
			So(r.Aborted, ShouldBeFalse)
			So(r.Err, ShouldEqual, boom)
		case <-time.After(time.Second):
			t.Fatal("failTurn 未回灌 TurnResult")
		}

		// 恰好一条: 不应有第二条。
		select {
		case extra := <-ch:
			t.Fatalf("failTurn 重复回灌: %+v", extra)
		case <-time.After(50 * time.Millisecond):
		}
	})
}
