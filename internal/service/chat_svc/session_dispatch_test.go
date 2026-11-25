package chat_svc

import (
	"context"
	"errors"
	"testing"

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
)

// 本文件钉住 spec 2026-11-24-chat-right-toolwindows 决策 21:派发关系的登记、状态推进与
// 双向查询(派发我的 / 我派发的)。

type dispatchRig struct {
	svc      *dispatchSvc
	repo     *mock_session_dispatch_repo.MockSessionDispatchRepo
	sessions *mock_chat_repo.MockSessionRepo
	agents   *mock_agent_repo.MockAgentRepo
	now      int64
}

func newDispatchRig(t *testing.T) *dispatchRig {
	t.Helper()
	ctrl := gomock.NewController(t)
	rig := &dispatchRig{
		repo:     mock_session_dispatch_repo.NewMockSessionDispatchRepo(ctrl),
		sessions: mock_chat_repo.NewMockSessionRepo(ctrl),
		agents:   mock_agent_repo.NewMockAgentRepo(ctrl),
		now:      10_000,
	}
	prevRepo, prevSessions, prevAgents := session_dispatch_repo.SessionDispatch(), chat_repo.Session(), agent_repo.Agent()
	session_dispatch_repo.RegisterSessionDispatch(rig.repo)
	chat_repo.RegisterSession(rig.sessions)
	agent_repo.RegisterAgent(rig.agents)
	t.Cleanup(func() {
		session_dispatch_repo.RegisterSessionDispatch(prevRepo)
		chat_repo.RegisterSession(prevSessions)
		agent_repo.RegisterAgent(prevAgents)
	})
	rig.svc = &dispatchSvc{
		now: func() int64 { return rig.now },
		replyText: func(_ context.Context, id int64) (string, error) {
			if id == 99 {
				return "", errors.New("read boom")
			}
			return "reply-of-" + string(rune('0'+id)), nil
		},
	}
	return rig
}

// Given 一次 agrctl send, When 登记, Then 写入以「等待」起步的一行并交回它的 ID。
func TestDispatchBegin_WritesQueuedRow(t *testing.T) {
	rig := newDispatchRig(t)
	rig.repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, row *session_dispatch_entity.SessionDispatch) error {
			assert.Equal(t, int64(1), row.DispatcherSessionID)
			assert.Equal(t, int64(2), row.DispatchedSessionID)
			assert.Equal(t, session_dispatch_entity.MethodAgrctlSend, row.Method)
			assert.Equal(t, "写测试", row.Task)
			assert.True(t, row.Isolated)
			assert.Equal(t, session_dispatch_entity.StatusQueued, row.Status)
			assert.EqualValues(t, 10_000, row.Createtime)
			row.ID = 55
			return nil
		})

	id := rig.svc.Begin(context.Background(), DispatchBegin{
		DispatcherSessionID: 1, DispatchedSessionID: 2, Method: DispatchMethodAgrctlSend, Task: "写测试", Isolated: true,
	})
	assert.EqualValues(t, 55, id)
}

// 派发方式 agent_call 原样落库(agent 派发)。
func TestDispatchBegin_AgentCallMethod(t *testing.T) {
	rig := newDispatchRig(t)
	rig.repo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, row *session_dispatch_entity.SessionDispatch) error {
			assert.Equal(t, session_dispatch_entity.MethodAgentCall, row.Method)
			row.ID = 3
			return nil
		})
	assert.EqualValues(t, 3, rig.svc.Begin(context.Background(), DispatchBegin{
		DispatcherSessionID: 1, DispatchedSessionID: 2, Method: DispatchMethodAgentCall, Isolated: true,
	}))
}

// 登记是尽力而为:库写失败不能让派发本身失败,只交回 0,后续推进都成空操作。
func TestDispatchBegin_RepoFailureYieldsZeroAndLaterCallsAreNoOps(t *testing.T) {
	rig := newDispatchRig(t)
	rig.repo.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New("disk full"))

	id := rig.svc.Begin(context.Background(), DispatchBegin{DispatcherSessionID: 1, DispatchedSessionID: 2, Method: DispatchMethodAgrctlSend})
	assert.Zero(t, id)
	rig.svc.Started(context.Background(), id)
	rig.svc.Finish(context.Background(), id, DispatchOutcome{Status: CtlSendCompleted})
}

// 起轮成功 → 运行中。
func TestDispatchStarted_MarksRunning(t *testing.T) {
	rig := newDispatchRig(t)
	rig.repo.EXPECT().UpdateState(gomock.Any(), int64(5), session_dispatch_repo.StateChange{Status: "running"}).Return(nil)
	rig.svc.Started(context.Background(), 5)
}

// 终态:状态、完成时间(现在)与回复消息一起写;三种终态词表同子代理卡。
func TestDispatchFinish_WritesTerminalStateForEachOutcome(t *testing.T) {
	for _, st := range []CtlSendStatus{CtlSendCompleted, CtlSendFailed, CtlSendCanceled} {
		t.Run(string(st), func(t *testing.T) {
			rig := newDispatchRig(t)
			rig.repo.EXPECT().UpdateState(gomock.Any(), int64(5), session_dispatch_repo.StateChange{
				Status: string(st), Finishtime: 10_000, ReplyMessageID: 8,
			}).Return(nil)
			rig.svc.Finish(context.Background(), 5, DispatchOutcome{Status: st, ReplyMessageID: 8})
		})
	}
}

// 起轮失败也要收成终态,不能永远停在「等待」。
func TestDispatchFinish_FailedWithoutReply(t *testing.T) {
	rig := newDispatchRig(t)
	rig.repo.EXPECT().UpdateState(gomock.Any(), int64(5), session_dispatch_repo.StateChange{
		Status: "failed", Finishtime: 10_000,
	}).Return(nil)
	rig.svc.Finish(context.Background(), 5, DispatchOutcome{Status: CtlSendFailed})
}

// Given 两个方向都有记录, When 查询, Then 「派发我的」带派发方标题 / Agent / 方式 / 时间,
// 「我派发的」带任务 / Agent / 方式 / 状态 / 耗时 / 隔离标记,隔离子会话读出最后回复。
func TestListSessionDispatches_ReturnsBothDirections(t *testing.T) {
	rig := newDispatchRig(t)
	ctx := context.Background()
	const me = int64(10)

	rig.repo.EXPECT().ListByDispatched(ctx, me).Return([]*session_dispatch_entity.SessionDispatch{
		{ID: 1, DispatcherSessionID: 4, DispatchedSessionID: me, Method: "agrctl_send", Createtime: 7_000},
	}, nil)
	rig.repo.EXPECT().ListByDispatcher(ctx, me).Return([]*session_dispatch_entity.SessionDispatch{
		{ID: 2, DispatcherSessionID: me, DispatchedSessionID: 20, Method: "agrctl_send", Task: "可见任务",
			Status: "completed", Createtime: 1_000, Finishtime: 4_500, ReplyMessageID: 3},
		{ID: 3, DispatcherSessionID: me, DispatchedSessionID: 21, Method: "agent_call", Task: "隔离任务", Isolated: true,
			Status: "completed", Createtime: 2_000, Finishtime: 3_000, ReplyMessageID: 6},
		{ID: 4, DispatcherSessionID: me, DispatchedSessionID: 22, Method: "agent_call", Task: "还在跑", Isolated: true,
			Status: "running", Createtime: 9_000},
	}, nil)
	rig.sessions.EXPECT().Find(ctx, int64(4)).Return(&chat_entity.Session{ID: 4, Title: "派发者标题", AgentID: 40}, nil)
	rig.sessions.EXPECT().Find(ctx, int64(20)).Return(&chat_entity.Session{ID: 20, AgentID: 41}, nil)
	rig.sessions.EXPECT().Find(ctx, int64(21)).Return(&chat_entity.Session{ID: 21, AgentID: 42}, nil)
	rig.sessions.EXPECT().Find(ctx, int64(22)).Return(&chat_entity.Session{ID: 22, AgentID: 42}, nil)
	rig.agents.EXPECT().Find(ctx, int64(40)).Return(&agent_entity.Agent{ID: 40, Name: "Boss"}, nil)
	rig.agents.EXPECT().Find(ctx, int64(41)).Return(&agent_entity.Agent{ID: 41, Name: "Coder"}, nil)
	rig.agents.EXPECT().Find(ctx, int64(42)).Return(&agent_entity.Agent{ID: 42, Name: "Helper"}, nil).AnyTimes()

	view, err := rig.svc.ListSessionDispatches(ctx, me)
	require.NoError(t, err)

	require.Len(t, view.DispatchedBy, 1)
	by := view.DispatchedBy[0]
	assert.Equal(t, int64(4), by.SessionID)
	assert.Equal(t, "派发者标题", by.Title)
	assert.Equal(t, "Boss", by.AgentName)
	assert.Equal(t, "agrctl_send", by.Method)
	assert.EqualValues(t, 7_000, by.CreatedAt)

	require.Len(t, view.Dispatched, 3)
	visible, isolated, running := view.Dispatched[0], view.Dispatched[1], view.Dispatched[2]
	assert.Equal(t, int64(20), visible.SessionID)
	assert.Equal(t, "可见任务", visible.Task)
	assert.Equal(t, "Coder", visible.AgentName)
	assert.Equal(t, "completed", visible.Status)
	assert.EqualValues(t, 3_500, visible.DurationMs)
	assert.False(t, visible.Isolated)
	assert.Empty(t, visible.LastReply, "可见会话不读最后回复(可直接打开)")

	assert.True(t, isolated.Isolated)
	assert.Equal(t, "agent_call", isolated.Method)
	assert.Equal(t, "reply-of-6", isolated.LastReply)
	assert.EqualValues(t, 1_000, isolated.DurationMs)

	assert.Equal(t, "running", running.Status)
	assert.EqualValues(t, 1_000, running.DurationMs, "未终态的耗时按现在算")
	assert.Empty(t, running.LastReply, "没有回复消息就没有最后回复")
}

// 「派发我的」是拉起本会话的那个会话(spec「关联工具窗」):同一个派发方之后又往本会话
// send 了几次,也只列一行,取最早那次(拉起它的那次)的方式与时间;不同派发方各一行。
func TestListSessionDispatches_DispatchedByOneRowPerDispatcher(t *testing.T) {
	rig := newDispatchRig(t)
	ctx := context.Background()
	const me = int64(10)
	rig.repo.EXPECT().ListByDispatched(ctx, me).Return([]*session_dispatch_entity.SessionDispatch{
		{ID: 1, DispatcherSessionID: 4, DispatchedSessionID: me, Method: "agrctl_send", Createtime: 7_000},
		{ID: 2, DispatcherSessionID: 4, DispatchedSessionID: me, Method: "agent_call", Createtime: 8_000},
		{ID: 3, DispatcherSessionID: 5, DispatchedSessionID: me, Method: "agrctl_send", Createtime: 8_500},
		{ID: 4, DispatcherSessionID: 4, DispatchedSessionID: me, Method: "agrctl_send", Createtime: 9_000},
	}, nil)
	rig.repo.EXPECT().ListByDispatcher(ctx, me).Return(nil, nil)
	rig.sessions.EXPECT().Find(ctx, int64(4)).Return(&chat_entity.Session{ID: 4, Title: "A"}, nil)
	rig.sessions.EXPECT().Find(ctx, int64(5)).Return(&chat_entity.Session{ID: 5, Title: "B"}, nil)

	view, err := rig.svc.ListSessionDispatches(ctx, me)
	require.NoError(t, err)
	require.Len(t, view.DispatchedBy, 2)
	assert.Equal(t, int64(4), view.DispatchedBy[0].SessionID)
	assert.Equal(t, int64(1), view.DispatchedBy[0].DispatchID)
	assert.Equal(t, "agrctl_send", view.DispatchedBy[0].Method)
	assert.EqualValues(t, 7_000, view.DispatchedBy[0].CreatedAt)
	assert.Equal(t, int64(5), view.DispatchedBy[1].SessionID)
}

// 读最后回复失败只让那一项没有回复,不让整个查询失败。
func TestListSessionDispatches_ReplyReadFailureLeavesReplyEmpty(t *testing.T) {
	rig := newDispatchRig(t)
	ctx := context.Background()
	rig.repo.EXPECT().ListByDispatched(ctx, int64(10)).Return(nil, nil)
	rig.repo.EXPECT().ListByDispatcher(ctx, int64(10)).Return([]*session_dispatch_entity.SessionDispatch{
		{ID: 3, DispatcherSessionID: 10, DispatchedSessionID: 21, Isolated: true, Status: "completed", ReplyMessageID: 99},
	}, nil)
	rig.sessions.EXPECT().Find(ctx, int64(21)).Return(&chat_entity.Session{ID: 21, AgentID: 42}, nil)
	rig.agents.EXPECT().Find(ctx, int64(42)).Return(&agent_entity.Agent{ID: 42, Name: "Helper"}, nil)

	view, err := rig.svc.ListSessionDispatches(ctx, 10)
	require.NoError(t, err)
	require.Len(t, view.Dispatched, 1)
	assert.Empty(t, view.Dispatched[0].LastReply)
}

// 没有任何记录(含此前的会话)时两组都是空数组而不是 nil。
func TestListSessionDispatches_EmptyGroupsAreNonNil(t *testing.T) {
	rig := newDispatchRig(t)
	ctx := context.Background()
	rig.repo.EXPECT().ListByDispatched(ctx, int64(10)).Return(nil, nil)
	rig.repo.EXPECT().ListByDispatcher(ctx, int64(10)).Return(nil, nil)

	view, err := rig.svc.ListSessionDispatches(ctx, 10)
	require.NoError(t, err)
	assert.NotNil(t, view.DispatchedBy)
	assert.NotNil(t, view.Dispatched)
	assert.Empty(t, view.DispatchedBy)
	assert.Empty(t, view.Dispatched)
}

func TestListSessionDispatches_RepoFailureIsReturned(t *testing.T) {
	rig := newDispatchRig(t)
	rig.repo.EXPECT().ListByDispatched(gomock.Any(), int64(10)).Return(nil, errors.New("locked"))

	_, err := rig.svc.ListSessionDispatches(context.Background(), 10)
	assert.Error(t, err)
}

// 一轮的结果按「错误 > 中止 > 完成」翻成终态,回复消息始终带上。
func TestDispatchOutcomeOfTurn(t *testing.T) {
	assert.Equal(t, DispatchOutcome{Status: CtlSendCompleted, ReplyMessageID: 4}, DispatchOutcomeOfTurn(TurnResult{AssistantMessageID: 4}))
	assert.Equal(t, DispatchOutcome{Status: CtlSendCanceled, ReplyMessageID: 4}, DispatchOutcomeOfTurn(TurnResult{AssistantMessageID: 4, Aborted: true}))
	assert.Equal(t, DispatchOutcome{Status: CtlSendFailed, ReplyMessageID: 4}, DispatchOutcomeOfTurn(TurnResult{AssistantMessageID: 4, Err: errors.New("x"), Aborted: true}))
}

// Given 上次运行留下还在「等待 / 运行中」的派发(跟踪它的进程已经不在了), When 本进程启动时
// 收尾, Then 它们一次收成「已停止」并记下完成时间,不再永远显示运行中、时长一直涨。
func TestDispatch_ReconcileAfterRestartStopsUnfinishedRows(t *testing.T) {
	rig := newDispatchRig(t)
	rig.repo.EXPECT().FinishUnfinished(gomock.Any(), session_dispatch_entity.StatusCanceled, int64(10_000)).Return(int64(2), nil)

	rig.svc.ReconcileAfterRestart(context.Background())
}

// 收尾失败只记日志,不阻断启动。
func TestDispatch_ReconcileAfterRestartFailureIsLoggedOnly(t *testing.T) {
	rig := newDispatchRig(t)
	rig.repo.EXPECT().FinishUnfinished(gomock.Any(), gomock.Any(), gomock.Any()).Return(int64(0), errors.New("locked"))

	rig.svc.ReconcileAfterRestart(context.Background())
}
