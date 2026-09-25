package chat_svc

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/blocks"
	"github.com/agentre-hub/agentre/internal/pkg/transcript/turn"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
)

// newToolApprovalSvc 造一个注好宽松 session / message repo mock 的 chatSvc(Begin/Finish
// 都会 Find(sessionID) 然后 markSessionWaiting/Running 写库,并 checkpoint 本轮 assistant)。
func newToolApprovalSvc(t *testing.T) (*chatSvc, *syncRecordEmitter) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
	msgRepo := mock_transcript_repo.NewMockMessageRepo(ctrl)
	prev, prevMsg := chat_repo.Session(), transcript_repo.Message()
	chat_repo.RegisterSession(sessRepo)
	transcript_repo.RegisterMessage(msgRepo)
	t.Cleanup(func() {
		chat_repo.RegisterSession(prev)
		transcript_repo.RegisterMessage(prevMsg)
	})

	sessRepo.EXPECT().Find(gomock.Any(), int64(42)).
		DoAndReturn(func(_ context.Context, id int64) (*chat_entity.Session, error) {
			return &chat_entity.Session{ID: id, AgentStatus: "running"}, nil
		}).AnyTimes()
	sessRepo.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	msgRepo.EXPECT().CheckpointBlocks(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	em := &syncRecordEmitter{}
	return &chatSvc{emitter: em}, em
}

// registerActiveTurn 在 sessionID 上登记一轮在跑的 turn(runTurn 起手做的那一步)。
func registerActiveTurn(svc *chatSvc, sessionID int64) *turnRun {
	t := &turnRun{
		svc:          svc,
		stream:       StreamName(sessionID, 7),
		acc:          turn.New(),
		assistantMsg: &chat_entity.Message{ID: 7, SessionID: sessionID, Role: "assistant"},
	}
	svc.activeTurns.Store(sessionID, t)
	return t
}

// Given 这条会话此刻没有在跑的一轮;When 写工具登记审批卡;Then 报错、不发任何事件。
// 卡要落进一轮的转录,没有轮次就无处可落(有轮次时的全生命周期见
// tool_approval_transcript_test.go)。
func TestToolApproval_GivenNoActiveTurn_ThenBeginFails(t *testing.T) {
	Convey("无活跃 turn → Begin 返回 error + nil channel", t, func() {
		svc, em := newToolApprovalSvc(t)
		blk := &blocks.ToolApprovalBlock{
			ToolKey: "org", RequestID: "req-1", ToolName: "org_invite",
			ToolInput: map[string]any{"user_id": "u-1"}, Status: "pending",
		}
		ch, err := svc.BeginToolApproval(context.Background(), 42, blk)
		So(err, ShouldNotBeNil)
		So(ch, ShouldBeNil)
		So(em.toolApprovalEvents(), ShouldHaveLength, 0)
		So(svc.AnswerToolApproval(context.Background(), 42, "req-1", true), ShouldNotBeNil)
	})
}

func TestAnswerToolApproval(t *testing.T) {
	Convey("AnswerToolApproval 按 requestID 路由唤醒挂起的写工具调用", t, func() {
		svc, _ := newToolApprovalSvc(t)
		ctx := context.Background()
		registerActiveTurn(svc, 42)

		Convey("空 requestID → error", func() {
			So(svc.AnswerToolApproval(ctx, 42, "", true), ShouldNotBeNil)
		})

		Convey("未知 requestID → error", func() {
			So(svc.AnswerToolApproval(ctx, 42, "nope", true), ShouldNotBeNil)
		})

		Convey("Begin 后 Answer 命中 waiter → channel 收到决策;重复 Answer → error", func() {
			blk := &blocks.ToolApprovalBlock{ToolKey: "org", RequestID: "req-a", ToolName: "org_invite", Status: "pending"}
			ch, err := svc.BeginToolApproval(ctx, 42, blk)
			So(err, ShouldBeNil)

			So(svc.AnswerToolApproval(ctx, 42, "req-a", true), ShouldBeNil)
			So(<-ch, ShouldBeTrue)
			// LoadAndDelete 已摘除 waiter,重复 Answer → error。
			So(svc.AnswerToolApproval(ctx, 42, "req-a", true), ShouldNotBeNil)
		})

		Convey("点名的会话不是这张卡所在的会话 → error,卡仍挂起、原会话照常能答", func() {
			blk := &blocks.ToolApprovalBlock{ToolKey: "ctl", RequestID: "req-c", ToolName: "ctl", Status: "pending"}
			ch, err := svc.BeginToolApproval(ctx, 42, blk)
			So(err, ShouldBeNil)

			So(svc.AnswerToolApproval(ctx, 43, "req-c", true), ShouldNotBeNil)
			So(svc.AnswerToolApproval(ctx, 42, "req-c", false), ShouldBeNil)
			So(<-ch, ShouldBeFalse)
		})

		Convey("FinishToolApproval 清 waiter 后 Answer 同 requestID → error", func() {
			blk := &blocks.ToolApprovalBlock{ToolKey: "org", RequestID: "req-b", ToolName: "org_invite", Status: "pending"}
			_, err := svc.BeginToolApproval(ctx, 42, blk)
			So(err, ShouldBeNil)
			So(svc.FinishToolApproval(ctx, 42, "req-b", "denied", ""), ShouldBeNil)
			So(svc.AnswerToolApproval(ctx, 42, "req-b", true), ShouldNotBeNil)
		})
	})
}
