package chat_svc_test

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	. "github.com/smartystreets/goconvey/convey"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo/mock_agent_backend_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
)

// registerAgentBackendForSubagentSession 让 createSubagentSession 的创建分支能解析出会话的
// 默认权限模式。用 AnyTimes 容忍创建路径调用一次、早退路径零次。
func registerAgentBackendForSubagentSession(t *testing.T, ctrl *gomock.Controller, agentID int64, defaultMode string) {
	t.Helper()
	agentRepo := mock_agent_repo.NewMockAgentRepo(ctrl)
	prevA := agent_repo.Agent()
	agent_repo.RegisterAgent(agentRepo)
	t.Cleanup(func() { agent_repo.RegisterAgent(prevA) })
	agentRepo.EXPECT().Find(gomock.Any(), agentID).
		Return(&agent_entity.Agent{ID: agentID, AgentBackendID: 12}, nil).AnyTimes()

	beRepo := mock_agent_backend_repo.NewMockAgentBackendRepo(ctrl)
	prevB := agent_backend_repo.AgentBackend()
	agent_backend_repo.RegisterAgentBackend(beRepo)
	t.Cleanup(func() { agent_backend_repo.RegisterAgentBackend(prevB) })
	beRepo.EXPECT().Find(gomock.Any(), int64(12)).
		Return(&agent_backend_entity.AgentBackend{
			ID:                    12,
			Type:                  string(agent_backend_entity.TypeClaudeCode),
			DefaultPermissionMode: defaultMode,
			Status:                consts.ACTIVE,
		}, nil).AnyTimes()
}

func TestEnsureSession_SubagentCall(t *testing.T) {
	Convey("Given SessionPurposeSubagentCall, When EnsureSession is called, Then it creates a fresh session every time (non-idempotent)", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
		prev := chat_repo.Session()
		chat_repo.RegisterSession(sessRepo)
		t.Cleanup(func() { chat_repo.RegisterSession(prev) })
		registerAgentBackendForSubagentSession(t, ctrl, int64(7), "acceptEdits")

		ctx := context.Background()

		// First call: expects Create, returns id=101
		sessRepo.EXPECT().
			Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
				So(s.AgentID, ShouldEqual, 7)
				So(s.AgentStatus, ShouldEqual, "idle")
				So(s.PermissionMode, ShouldEqual, "acceptEdits")
				So(s.PermissionModeAtLaunch, ShouldEqual, "acceptEdits")
				// 子 agent 会话必须落 purpose 标记, repo 层据此从所有会话列表/计数隐藏它。
				So(s.Purpose, ShouldEqual, chat_entity.SessionPurposeSubagent)
				s.ID = 101
				return nil
			})

		// Second call: expects another Create, returns id=202
		sessRepo.EXPECT().
			Create(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, s *chat_entity.Session) error {
				s.ID = 202
				return nil
			})

		svc := chat_svc.NewChat(chat_svc.NoopEmitter{})

		resp1, err := svc.EnsureSession(ctx, &chat_svc.EnsureSessionRequest{
			Purpose:   chat_svc.SessionPurposeSubagentCall,
			AgentID:   7,
			ProjectID: 0,
			Title:     "subagent task",
		})
		So(err, ShouldBeNil)
		So(resp1.SessionID, ShouldEqual, 101)
		So(resp1.Created, ShouldBeTrue)

		resp2, err := svc.EnsureSession(ctx, &chat_svc.EnsureSessionRequest{
			Purpose:   chat_svc.SessionPurposeSubagentCall,
			AgentID:   7,
			ProjectID: 0,
			Title:     "subagent task",
		})
		So(err, ShouldBeNil)
		So(resp2.SessionID, ShouldEqual, 202)
		So(resp2.Created, ShouldBeTrue)

		// Two successive calls must produce distinct session IDs (non-idempotent)
		So(resp1.SessionID, ShouldNotEqual, resp2.SessionID)
	})

	Convey("Given SessionPurposeSubagentCall with agentID=0, When EnsureSession is called, Then it returns InvalidParameter error", t, func() {
		svc := chat_svc.NewChat(chat_svc.NoopEmitter{})
		ctx := context.Background()

		_, err := svc.EnsureSession(ctx, &chat_svc.EnsureSessionRequest{
			Purpose: chat_svc.SessionPurposeSubagentCall,
			AgentID: 0,
		})
		So(err, ShouldNotBeNil)
	})
}

func TestSessionProjectID(t *testing.T) {
	Convey("Given a session with a project, When SessionProjectID is called, Then it returns that project id", t, func() {
		ctrl := gomock.NewController(t)
		t.Cleanup(ctrl.Finish)

		sessRepo := mock_chat_repo.NewMockSessionRepo(ctrl)
		prev := chat_repo.Session()
		chat_repo.RegisterSession(sessRepo)
		t.Cleanup(func() { chat_repo.RegisterSession(prev) })

		sessRepo.EXPECT().Find(gomock.Any(), int64(55)).
			Return(&chat_entity.Session{ID: 55, ProjectID: 42}, nil)

		svc := chat_svc.NewChat(chat_svc.NoopEmitter{})
		pid, err := svc.SessionProjectID(context.Background(), 55)
		So(err, ShouldBeNil)
		So(pid, ShouldEqual, 42)
	})

	Convey("Given sessionID<=0, When SessionProjectID is called, Then it returns 0 without hitting the repo", t, func() {
		svc := chat_svc.NewChat(chat_svc.NoopEmitter{})
		pid, err := svc.SessionProjectID(context.Background(), 0)
		So(err, ShouldBeNil)
		So(pid, ShouldEqual, 0)
	})
}
