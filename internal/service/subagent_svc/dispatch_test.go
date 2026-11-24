package subagent_svc

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/chat_svc/mock_session_dispatch"
	"github.com/agentre-hub/agentre/internal/service/subagent_svc/mock_subagent_svc"
)

// dispatch_test.go 钉住 callAgent 与派发关系登记(chat_svc.DispatchRecorder)的契约
// (spec 2026-11-24 决策 21):agent 派发在起轮之前登记 method=agent_call、隔离的一行,起轮成功
// 转运行中,子 agent 那一轮收尾(完成 / 失败 / 中止 / 调用方取消)时落终态。

type dispatchRig struct {
	s     *subagentSvc
	chat  *mock_subagent_svc.MockChatGateway
	rec   *mock_session_dispatch.MockDispatchRecorder
	turns chan chat_svc.TurnResult
}

const agentDispatchID = int64(31)

func newDispatchRig(t *testing.T) *dispatchRig {
	t.Helper()
	ctrl := gomock.NewController(t)
	agents := mock_subagent_svc.NewMockAgentGateway(ctrl)
	chat := mock_subagent_svc.NewMockChatGateway(ctrl)
	rec := mock_session_dispatch.NewMockDispatchRecorder(ctrl)
	s := newSubagentSvcForTest(agents, chat)
	s.RegisterDispatchRecorder(rec)

	agents.EXPECT().FindByName(gomock.Any(), "Reviewer").Return(&agent_entity.Agent{ID: 20, Name: "Reviewer"}, nil)
	chat.EXPECT().SessionProjectID(gomock.Any(), int64(100)).Return(int64(0), nil)
	chat.EXPECT().EnsureSession(gomock.Any(), gomock.Any()).Return(&chat_svc.EnsureSessionResponse{SessionID: 999, Created: true}, nil)
	turns := make(chan chat_svc.TurnResult, 1)
	chat.EXPECT().ObserveTurn(int64(999)).Return((<-chan chat_svc.TurnResult)(turns), func() {})
	return &dispatchRig{s: s, chat: chat, rec: rec, turns: turns}
}

func (r *dispatchRig) call(ctx context.Context) (string, error) {
	return r.s.callAgent(ctx, agenttool.Ref{AgentID: 10, SessionID: 100}, "Reviewer", "review the diff")
}

func (r *dispatchRig) expectBegin(sent *bool) {
	r.rec.EXPECT().Begin(gomock.Any(), chat_svc.DispatchBegin{
		DispatcherSessionID: 100, DispatchedSessionID: 999,
		Method: chat_svc.DispatchMethodAgentCall, Task: "review the diff", Isolated: true,
	}).DoAndReturn(func(context.Context, chat_svc.DispatchBegin) int64 {
		if *sent {
			panic("登记必须先于起轮")
		}
		return agentDispatchID
	})
}

func (r *dispatchRig) expectSend(sent *bool, err error) {
	r.chat.EXPECT().Send(gomock.Any(), gomock.Any()).DoAndReturn(
		func(context.Context, *chat_svc.SendRequest) (*chat_svc.SendResponse, error) {
			*sent = true
			if err != nil {
				return nil, err
			}
			return &chat_svc.SendResponse{}, nil
		})
}

// Given agent 派发, When 子 agent 正常答完, Then 登记 agent_call + 隔离,运行中,收成已完成并带回复消息。
func TestCallAgent_RecordsDispatchAndCompletes(t *testing.T) {
	r := newDispatchRig(t)
	sent := false
	r.expectBegin(&sent)
	r.expectSend(&sent, nil)
	r.rec.EXPECT().Started(gomock.Any(), agentDispatchID)
	r.rec.EXPECT().Finish(gomock.Any(), agentDispatchID, chat_svc.DispatchOutcome{
		Status: chat_svc.CtlSendCompleted, ReplyMessageID: 555,
	})
	r.chat.EXPECT().FinalAssistantText(gomock.Any(), int64(555)).Return("ok", nil)
	r.turns <- chat_svc.TurnResult{SessionID: 999, AssistantMessageID: 555}

	if _, err := r.call(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCallAgent_DispatchFailedOnSubagentError(t *testing.T) {
	r := newDispatchRig(t)
	sent := false
	r.expectBegin(&sent)
	r.expectSend(&sent, nil)
	r.rec.EXPECT().Started(gomock.Any(), agentDispatchID)
	r.rec.EXPECT().Finish(gomock.Any(), agentDispatchID, chat_svc.DispatchOutcome{
		Status: chat_svc.CtlSendFailed, ReplyMessageID: 555,
	})
	r.turns <- chat_svc.TurnResult{SessionID: 999, AssistantMessageID: 555, Err: errors.New("boom")}

	if _, err := r.call(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestCallAgent_DispatchCanceledWhenSubagentAborted(t *testing.T) {
	r := newDispatchRig(t)
	sent := false
	r.expectBegin(&sent)
	r.expectSend(&sent, nil)
	r.rec.EXPECT().Started(gomock.Any(), agentDispatchID)
	r.rec.EXPECT().Finish(gomock.Any(), agentDispatchID, chat_svc.DispatchOutcome{
		Status: chat_svc.CtlSendCanceled, ReplyMessageID: 555,
	})
	r.turns <- chat_svc.TurnResult{SessionID: 999, AssistantMessageID: 555, Aborted: true}

	if _, err := r.call(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestCallAgent_DispatchCanceledWhenCallerCancels(t *testing.T) {
	r := newDispatchRig(t)
	sent := false
	r.expectBegin(&sent)
	r.expectSend(&sent, nil)
	r.rec.EXPECT().Started(gomock.Any(), agentDispatchID)
	r.rec.EXPECT().Finish(gomock.Any(), agentDispatchID, chat_svc.DispatchOutcome{Status: chat_svc.CtlSendCanceled})
	r.chat.EXPECT().Stop(gomock.Any(), gomock.Any()).Return(&chat_svc.StopResponse{}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.call(ctx); err == nil {
		t.Fatal("expected error")
	}
}

// 起轮失败:行收成失败,不停在「等待」,也不转运行中。
func TestCallAgent_DispatchFailedWhenSendFails(t *testing.T) {
	r := newDispatchRig(t)
	sent := false
	r.expectBegin(&sent)
	r.expectSend(&sent, errors.New("no backend"))
	r.rec.EXPECT().Finish(gomock.Any(), agentDispatchID, chat_svc.DispatchOutcome{Status: chat_svc.CtlSendFailed})

	if _, err := r.call(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

// 没装登记端口时 agent 派发与今天相同。
func TestCallAgent_WithoutRecorderBehavesAsBefore(t *testing.T) {
	ctrl := gomock.NewController(t)
	agents := mock_subagent_svc.NewMockAgentGateway(ctrl)
	chat := mock_subagent_svc.NewMockChatGateway(ctrl)
	s := newSubagentSvcForTest(agents, chat)
	agents.EXPECT().FindByName(gomock.Any(), "Reviewer").Return(&agent_entity.Agent{ID: 20, Name: "Reviewer"}, nil)
	chat.EXPECT().SessionProjectID(gomock.Any(), int64(100)).Return(int64(0), nil)
	chat.EXPECT().EnsureSession(gomock.Any(), gomock.Any()).Return(&chat_svc.EnsureSessionResponse{SessionID: 999}, nil)
	turns := make(chan chat_svc.TurnResult, 1)
	turns <- chat_svc.TurnResult{SessionID: 999, AssistantMessageID: 555}
	chat.EXPECT().ObserveTurn(int64(999)).Return((<-chan chat_svc.TurnResult)(turns), func() {})
	chat.EXPECT().Send(gomock.Any(), gomock.Any()).Return(&chat_svc.SendResponse{}, nil)
	chat.EXPECT().FinalAssistantText(gomock.Any(), int64(555)).Return("ok", nil)

	if _, err := s.callAgent(context.Background(), agenttool.Ref{AgentID: 10, SessionID: 100}, "Reviewer", "x"); err != nil {
		t.Fatal(err)
	}
}
