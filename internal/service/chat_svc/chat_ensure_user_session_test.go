package chat_svc_test

import (
	"context"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// SessionPurposeUserChat 给 ! 命令在新会话态先坐实一个「普通用户会话」用 —— 与 subagent
// 子会话不同:Purpose 必须为空,这样它出现在侧栏、可继续对话(不被 repo 隐藏)。
func TestEnsureSession_UserChat(t *testing.T) {
	Convey("Given SessionPurposeUserChat, When EnsureSession is called, Then it creates a normal idle session with empty Purpose", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
		prev := chat_repo.Session()
		chat_repo.RegisterSession(sessRepo)
		t.Cleanup(func() { chat_repo.RegisterSession(prev) })
		registerAgentBackendForSubagentSession(t, ctrl, int64(9), "acceptEdits")

		ctx := context.Background()

		sessRepo.EXPECT().
			Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
				So(s.AgentID, ShouldEqual, 9)
				So(s.AgentStatus, ShouldEqual, "idle")
				So(s.PermissionMode, ShouldEqual, "acceptEdits")
				So(s.PermissionModeAtLaunch, ShouldEqual, "acceptEdits")
				// 普通用户会话:Purpose 留空(侧栏可见、可续聊),不是隔离子会话。
				So(string(s.Purpose), ShouldEqual, "")
				s.ID = 303
				return nil
			})

		svc := chat_svc.NewChat(chat_svc.NoopEmitter{})
		resp, err := svc.EnsureSession(ctx, &chat_svc.EnsureSessionRequest{
			Purpose:   chat_svc.SessionPurposeUserChat,
			AgentID:   9,
			ProjectID: 0,
			Title:     "",
		})
		So(err, ShouldBeNil)
		So(resp.SessionID, ShouldEqual, 303)
		So(resp.Created, ShouldBeTrue)
	})

	Convey("Given SessionPurposeUserChat with agentID=0, When EnsureSession is called, Then it returns an error", t, func() {
		svc := chat_svc.NewChat(chat_svc.NoopEmitter{})
		_, err := svc.EnsureSession(context.Background(), &chat_svc.EnsureSessionRequest{
			Purpose: chat_svc.SessionPurposeUserChat,
			AgentID: 0,
		})
		So(err, ShouldNotBeNil)
	})
}
