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

// streamCapture 与 captureEmitter 的区别:保留 stream 名。跨轮翻转的镜像必须落在
// **会话级**流上(派遣卡不在任何 liveBlocks 里,per-turn 流那一路合并必然落空),
// 所以流名本身就是契约的一部分。
type streamCapture struct{ events []streamedEvent }

type streamedEvent struct {
	stream string
	ev     ChatStreamEvent
}

func (c *streamCapture) Emit(_ context.Context, stream string, payload any) {
	if ev, ok := payload.(ChatStreamEvent); ok {
		c.events = append(c.events, streamedEvent{stream: stream, ev: ev})
	}
}

func setupFlipperTest(t *testing.T) (*mock_chat_repo.MockMessageRepo, *streamCapture, *chatSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	msgRepo := mock_chat_repo.NewMockMessageRepo(ctrl)
	prev := chat_repo.Message()
	chat_repo.RegisterMessage(msgRepo)
	t.Cleanup(func() { chat_repo.RegisterMessage(prev) })
	rec := &streamCapture{}
	return msgRepo, rec, &chatSvc{emitter: rec}
}

// TestSubagentFlipperAdapter_PersistsAndMirrors 是 sess-2825 回归的 chat_svc 一半:
// handler 判定「这条完成帧属于更早的消息」之后,真正把它落到库里并让已打开的界面即时
// 翻转的那一步。
func TestSubagentFlipperAdapter_PersistsAndMirrors(t *testing.T) {
	Convey("Given 会话 42 有一个后台 subagent 记在 bgRunning 集合里", t, func() {
		msgRepo, rec, svc := setupFlipperTest(t)
		sess := &chat_entity.Session{}
		sess.ID = 42
		sess.AgentStatus = "running"
		svc.addBgRunning(42, "toolu-earlier-msg")

		Convey("When 它的完成帧在别人的轮里到达,经 flipper 落地", func() {
			msgRepo.EXPECT().
				FlipSubagentStatus(gomock.Any(), int64(42), "toolu-earlier-msg", "completed", "").
				Return(nil)

			a := subagentFlipperAdapter{svc: svc, sess: sess, stream: "chat:event:42:99"}
			err := a.FlipSubagentStatus(context.Background(), "toolu-earlier-msg", "completed")

			Convey("Then 落库 + 会话级流镜像 subagent_done + 退出 bgRunning 集合", func() {
				So(err, ShouldBeNil)

				var mirror *streamedEvent
				for i := range rec.events {
					if rec.events[i].ev.Kind == StreamSubagentDone {
						mirror = &rec.events[i]
					}
				}
				So(mirror, ShouldNotBeNil)
				// 会话级流:ChatPanel 常驻订阅它,能就地合并进已落库的那张派遣卡。
				So(mirror.stream, ShouldEqual, AutonomousStreamName(42))
				So(mirror.ev.ToolCallID, ShouldEqual, "toolu-earlier-msg")
				So(mirror.ev.Subagent, ShouldNotBeNil)
				So(mirror.ev.Subagent.Status, ShouldEqual, "completed")

				So(svc.bgRunningActive(42), ShouldBeFalse)
			})
		})
	})
}

// TestSubagentFlipperAdapter_PersistFailedNoMirror 失败路径:落库失败时不得镜像
// 「已完成」——界面翻成 completed 而库里仍是 running,重开会话又变回 running,比一直
// 显示 running 更难排查。错误上抛由 dispatcher 记日志。
func TestSubagentFlipperAdapter_PersistFailedNoMirror(t *testing.T) {
	Convey("落库失败 → 上抛错误且不镜像终态", t, func() {
		msgRepo, rec, svc := setupFlipperTest(t)
		sess := &chat_entity.Session{}
		sess.ID = 42
		svc.addBgRunning(42, "toolu-earlier-msg")

		msgRepo.EXPECT().
			FlipSubagentStatus(gomock.Any(), int64(42), "toolu-earlier-msg", "failed", "").
			Return(errors.New("db is locked"))

		a := subagentFlipperAdapter{svc: svc, sess: sess, stream: "chat:event:42:99"}
		err := a.FlipSubagentStatus(context.Background(), "toolu-earlier-msg", "failed")

		So(err, ShouldNotBeNil)
		for _, e := range rec.events {
			So(e.ev.Kind, ShouldNotEqual, StreamSubagentDone)
		}
		// 落库没成功,集合也不该被清 —— 否则后台任务胶囊先消失、界面却还是 running。
		So(svc.bgRunningActive(42), ShouldBeTrue)
	})
}

// TestSubagentFlipperAdapter_NoSessionNoOp 边界:没有会话上下文时(sess 为 nil /
// 未落库的 id)不得拿 sessionID=0 去扫全库。
func TestSubagentFlipperAdapter_NoSessionNoOp(t *testing.T) {
	Convey("sess 缺失 / 会话 id 为零 → 静默 no-op,不触库", t, func() {
		_, _, svc := setupFlipperTest(t) // repo mock 没有 EXPECT,被调用即 ctrl.Finish 报错

		So(subagentFlipperAdapter{svc: svc}.
			FlipSubagentStatus(context.Background(), "toolu-x", "completed"), ShouldBeNil)
		So(subagentFlipperAdapter{svc: svc, sess: &chat_entity.Session{}}.
			FlipSubagentStatus(context.Background(), "toolu-x", "completed"), ShouldBeNil)
	})
}
