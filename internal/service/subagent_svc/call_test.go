package subagent_svc

import (
	"context"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/subagent_svc/mock_subagent_svc"
)

func TestCallAgent_HappyPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	agents := mock_subagent_svc.NewMockAgentGateway(ctrl)
	chat := mock_subagent_svc.NewMockChatGateway(ctrl)
	s := &subagentSvc{agents: agents, chat: chat, chains: map[int64][]int64{}}

	agents.EXPECT().FindByName(gomock.Any(), "Reviewer").Return(&agent_entity.Agent{ID: 20, Name: "Reviewer"}, nil)
	// 调用方会话(100)的项目应被继承到子 agent 一次性会话。
	chat.EXPECT().SessionProjectID(gomock.Any(), int64(100)).Return(int64(42), nil)
	chat.EXPECT().EnsureSession(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req *chat_svc.EnsureSessionRequest) (*chat_svc.EnsureSessionResponse, error) {
			if req.ProjectID != 42 {
				t.Fatalf("ephemeral session should inherit caller project 42, got %d", req.ProjectID)
			}
			if req.Purpose != chat_svc.SessionPurposeSubagentCall || req.AgentID != 20 {
				t.Fatalf("bad EnsureSession req: %+v", req)
			}
			return &chat_svc.EnsureSessionResponse{SessionID: 999, Created: true}, nil
		})
	ch := make(chan chat_svc.TurnResult, 1)
	ch <- chat_svc.TurnResult{SessionID: 999, AssistantMessageID: 555}
	chat.EXPECT().ObserveTurn(int64(999)).Return((<-chan chat_svc.TurnResult)(ch), func() {})
	chat.EXPECT().Send(gomock.Any(), gomock.Any()).Return(&chat_svc.SendResponse{}, nil)
	chat.EXPECT().FinalAssistantText(gomock.Any(), int64(555)).Return("done: looks good", nil)
	// 注意:不再期望 DeleteSession —— 一次性会话保留(可供事后查看)。

	out, err := s.callAgent(context.Background(), agenttool.Ref{AgentID: 10, SessionID: 100}, "Reviewer", "review the diff")
	if err != nil {
		t.Fatal(err)
	}
	if out != "done: looks good" {
		t.Fatalf("got %q", out)
	}
	if _, exists := s.chains[999]; exists {
		t.Fatal("chain not cleaned up")
	}
}

func TestCallAgent_UnknownAgent(t *testing.T) {
	ctrl := gomock.NewController(t)
	agents := mock_subagent_svc.NewMockAgentGateway(ctrl)
	chat := mock_subagent_svc.NewMockChatGateway(ctrl)
	s := &subagentSvc{agents: agents, chat: chat, chains: map[int64][]int64{}}

	agents.EXPECT().FindByName(gomock.Any(), "Ghost").Return(nil, nil)
	if _, err := s.callAgent(context.Background(), agenttool.Ref{AgentID: 1, SessionID: 1}, "Ghost", "x"); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestCallAgent_CycleRejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	agents := mock_subagent_svc.NewMockAgentGateway(ctrl)
	chat := mock_subagent_svc.NewMockChatGateway(ctrl)
	s := &subagentSvc{agents: agents, chat: chat, chains: map[int64][]int64{}}
	s.registerChain(100, []int64{20})
	agents.EXPECT().FindByName(gomock.Any(), "Reviewer").Return(&agent_entity.Agent{ID: 20, Name: "Reviewer"}, nil)
	// 环在建会话前拦截 —— SessionProjectID/EnsureSession 不应被调用。
	if _, err := s.callAgent(context.Background(), agenttool.Ref{AgentID: 10, SessionID: 100}, "Reviewer", "x"); err == nil {
		t.Fatal("expected cycle rejection")
	}
}

func TestCallAgent_CtxCanceled(t *testing.T) {
	ctrl := gomock.NewController(t)
	agents := mock_subagent_svc.NewMockAgentGateway(ctrl)
	chat := mock_subagent_svc.NewMockChatGateway(ctrl)
	s := &subagentSvc{agents: agents, chat: chat, chains: map[int64][]int64{}}

	agents.EXPECT().FindByName(gomock.Any(), "Slow").Return(&agent_entity.Agent{ID: 20, Name: "Slow"}, nil)
	chat.EXPECT().SessionProjectID(gomock.Any(), int64(100)).Return(int64(0), nil)
	chat.EXPECT().EnsureSession(gomock.Any(), gomock.Any()).Return(&chat_svc.EnsureSessionResponse{SessionID: 999}, nil)
	never := make(chan chat_svc.TurnResult)
	chat.EXPECT().ObserveTurn(int64(999)).Return((<-chan chat_svc.TurnResult)(never), func() {})
	chat.EXPECT().Send(gomock.Any(), gomock.Any()).Return(&chat_svc.SendResponse{}, nil)
	chat.EXPECT().Stop(gomock.Any(), gomock.Any()).Return(&chat_svc.StopResponse{}, nil) // 取消时中止子 agent

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预取消 → select 命中 ctx.Done
	if _, err := s.callAgent(ctx, agenttool.Ref{AgentID: 10, SessionID: 100}, "Slow", "x"); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestCallAgent_SubagentErr(t *testing.T) {
	ctrl := gomock.NewController(t)
	agents := mock_subagent_svc.NewMockAgentGateway(ctrl)
	chat := mock_subagent_svc.NewMockChatGateway(ctrl)
	s := &subagentSvc{agents: agents, chat: chat, chains: map[int64][]int64{}}

	agents.EXPECT().FindByName(gomock.Any(), "R").Return(&agent_entity.Agent{ID: 20, Name: "R"}, nil)
	chat.EXPECT().SessionProjectID(gomock.Any(), int64(100)).Return(int64(0), nil)
	chat.EXPECT().EnsureSession(gomock.Any(), gomock.Any()).Return(&chat_svc.EnsureSessionResponse{SessionID: 999}, nil)
	ch := make(chan chat_svc.TurnResult, 1)
	ch <- chat_svc.TurnResult{SessionID: 999, Err: errTest()}
	chat.EXPECT().ObserveTurn(int64(999)).Return((<-chan chat_svc.TurnResult)(ch), func() {})
	chat.EXPECT().Send(gomock.Any(), gomock.Any()).Return(&chat_svc.SendResponse{}, nil)

	if _, err := s.callAgent(context.Background(), agenttool.Ref{AgentID: 10, SessionID: 100}, "R", "x"); err == nil {
		t.Fatal("expected error propagated from sub-agent")
	}
}

func errTest() error { return &simpleErr{"boom"} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }
