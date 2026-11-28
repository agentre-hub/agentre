package app

import (
	"context"
	"testing"

	"github.com/cago-frame/agents/agent/blocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/session_dispatch_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo/mock_agent_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/session_dispatch_repo"
	"github.com/agentre-hub/agentre/internal/repository/session_dispatch_repo/mock_session_dispatch_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo"
	"github.com/agentre-hub/agentre/internal/repository/transcript_repo/mock_transcript_repo"
)

// Given 本会话既被别的会话派发、又派发出去一个隔离子会话, When 前端经 ListSessionDispatches 查询,
// Then 两组都返回,隔离子会话带它最后一条回复(spec 2026-11-24「关联工具窗」派发我的 / 我派发的)。
func TestListSessionDispatches_ReturnsBothGroupsWithIsolatedLastReply(t *testing.T) {
	ctrl := gomock.NewController(t)
	dispatches := mock_session_dispatch_repo.NewMockSessionDispatchRepo(ctrl)
	sessions := mock_chat_repo.NewMockSessionRepo(ctrl)
	agents := mock_agent_repo.NewMockAgentRepo(ctrl)
	messages := mock_transcript_repo.NewMockMessageRepo(ctrl)
	prevD, prevS, prevA, prevM := session_dispatch_repo.SessionDispatch(), chat_repo.Session(), agent_repo.Agent(), transcript_repo.Message()
	session_dispatch_repo.RegisterSessionDispatch(dispatches)
	chat_repo.RegisterSession(sessions)
	agent_repo.RegisterAgent(agents)
	transcript_repo.RegisterMessage(messages)
	t.Cleanup(func() {
		session_dispatch_repo.RegisterSessionDispatch(prevD)
		chat_repo.RegisterSession(prevS)
		agent_repo.RegisterAgent(prevA)
		transcript_repo.RegisterMessage(prevM)
	})

	reply := &chat_entity.Message{ID: 6, SessionID: 21, Role: "assistant"}
	require.NoError(t, reply.SetBlocks([]blocks.ContentBlock{blocks.TextBlock{Text: "子会话的最后回复"}}))

	dispatches.EXPECT().ListByDispatched(gomock.Any(), int64(10)).Return([]*session_dispatch_entity.SessionDispatch{
		{ID: 1, DispatcherSessionID: 4, DispatchedSessionID: 10, Method: "agent_call", Createtime: 500},
	}, nil)
	dispatches.EXPECT().ListByDispatcher(gomock.Any(), int64(10)).Return([]*session_dispatch_entity.SessionDispatch{
		{ID: 2, DispatcherSessionID: 10, DispatchedSessionID: 21, Method: "agrctl_send", Task: "隔离任务",
			Isolated: true, Status: "completed", Createtime: 1_000, Finishtime: 3_000, ReplyMessageID: 6},
	}, nil)
	sessions.EXPECT().Find(gomock.Any(), int64(4)).Return(&chat_entity.Session{ID: 4, Title: "上游会话", AgentID: 40}, nil)
	sessions.EXPECT().Find(gomock.Any(), int64(21)).Return(&chat_entity.Session{ID: 21, AgentID: 41}, nil)
	agents.EXPECT().Find(gomock.Any(), int64(40)).Return(&agent_entity.Agent{ID: 40, Name: "Boss"}, nil)
	agents.EXPECT().Find(gomock.Any(), int64(41)).Return(&agent_entity.Agent{ID: 41, Name: "Coder"}, nil)
	messages.EXPECT().Find(gomock.Any(), int64(6)).Return(reply, nil)

	a := &App{ctx: context.Background()}
	view, err := a.ListSessionDispatches(10)

	require.NoError(t, err)
	require.Len(t, view.DispatchedBy, 1)
	assert.Equal(t, "上游会话", view.DispatchedBy[0].Title)
	assert.Equal(t, "Boss", view.DispatchedBy[0].AgentName)
	assert.Equal(t, "agent_call", view.DispatchedBy[0].Method)
	require.Len(t, view.Dispatched, 1)
	got := view.Dispatched[0]
	assert.Equal(t, "隔离任务", got.Task)
	assert.Equal(t, "Coder", got.AgentName)
	assert.True(t, got.Isolated)
	assert.Equal(t, "completed", got.Status)
	assert.EqualValues(t, 2_000, got.DurationMs)
	assert.Equal(t, "子会话的最后回复", got.LastReply)
}
