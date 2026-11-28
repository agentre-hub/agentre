package session_dispatch_repo_test

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/session_dispatch_entity"
	"github.com/agentre-hub/agentre/internal/repository/session_dispatch_repo"
)

var dispatchCols = []string{"id", "dispatcher_session_id", "dispatched_session_id", "method", "task",
	"isolated", "status", "reply_message_id", "createtime", "finishtime"}

// Given 一次新派发, When 记下它, Then 写出两端会话 / 方式 / 任务 / 隔离 / 状态并回填主键与建行时间。
func TestSessionDispatch_CreateWritesRowAndFillsID(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `session_dispatches` \\(`dispatcher_session_id`,`dispatched_session_id`,`method`,`task`,`isolated`,`status`,`reply_message_id`,`createtime`,`finishtime`\\) VALUES \\(\\?,\\?,\\?,\\?,\\?,\\?,\\?,\\?,\\?\\)").
		WithArgs(int64(1), int64(2), "agrctl_send", "写测试", true, "queued", int64(0), sqlmock.AnyArg(), int64(0)).
		WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectCommit()

	row := &session_dispatch_entity.SessionDispatch{
		DispatcherSessionID: 1, DispatchedSessionID: 2, Method: session_dispatch_entity.MethodAgrctlSend,
		Task: "写测试", Isolated: true, Status: session_dispatch_entity.StatusQueued,
	}
	require.NoError(t, session_dispatch_repo.NewSessionDispatch().Create(ctx, row))
	assert.EqualValues(t, 9, row.ID)
	assert.NotZero(t, row.Createtime)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionDispatch_CreateReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `session_dispatches`").WillReturnError(errors.New("disk full"))
	mock.ExpectRollback()

	assert.Error(t, session_dispatch_repo.NewSessionDispatch().Create(ctx, &session_dispatch_entity.SessionDispatch{}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// UpdateState 只改还没进入终态的行(finishtime = 0):终态之后的迟到写入不能把它改回去。
func TestSessionDispatch_UpdateStateOnlyTouchesUnfinishedRow(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `session_dispatches` SET `finishtime`=\\?,`reply_message_id`=\\?,`status`=\\? WHERE id = \\? AND finishtime = 0").
		WithArgs(int64(5000), int64(77), "completed", int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, session_dispatch_repo.NewSessionDispatch().UpdateState(ctx, 9,
		session_dispatch_repo.StateChange{Status: "completed", Finishtime: 5000, ReplyMessageID: 77}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 转入「运行中」只改状态,不写完成时间与回复。
func TestSessionDispatch_UpdateStateRunningWritesOnlyStatus(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `session_dispatches` SET `status`=\\? WHERE id = \\? AND finishtime = 0").
		WithArgs("running", int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, session_dispatch_repo.NewSessionDispatch().UpdateState(ctx, 9,
		session_dispatch_repo.StateChange{Status: "running"}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionDispatch_UpdateStateReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `session_dispatches`").WillReturnError(errors.New("locked"))
	mock.ExpectRollback()

	assert.Error(t, session_dispatch_repo.NewSessionDispatch().UpdateState(ctx, 9,
		session_dispatch_repo.StateChange{Status: "running"}))
}

// ListByDispatcher 取本会话派发出去的,最早的在前。
func TestSessionDispatch_ListByDispatcherOldestFirst(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `session_dispatches` WHERE dispatcher_session_id = \\? ORDER BY id ASC").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows(dispatchCols).
			AddRow(1, 1, 2, "agrctl_send", "a", 0, "completed", 5, 100, 200).
			AddRow(2, 1, 3, "agent_call", "b", 1, "running", 0, 300, 0))

	rows, err := session_dispatch_repo.NewSessionDispatch().ListByDispatcher(ctx, 1)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.EqualValues(t, 2, rows[0].DispatchedSessionID)
	assert.True(t, rows[1].Isolated)
	assert.EqualValues(t, 200, rows[0].Finishtime)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionDispatch_ListByDispatcherReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery("SELECT \\* FROM `session_dispatches`").WillReturnError(errors.New("locked"))

	_, err := session_dispatch_repo.NewSessionDispatch().ListByDispatcher(ctx, 1)
	assert.Error(t, err)
}

// ListByDispatched 取派发了本会话的记录。
func TestSessionDispatch_ListByDispatchedOldestFirst(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `session_dispatches` WHERE dispatched_session_id = \\? ORDER BY id ASC").
		WithArgs(int64(2)).
		WillReturnRows(sqlmock.NewRows(dispatchCols).AddRow(1, 1, 2, "agrctl_send", "a", 0, "running", 0, 100, 0))

	rows, err := session_dispatch_repo.NewSessionDispatch().ListByDispatched(ctx, 2)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 1, rows[0].DispatcherSessionID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// DeleteBySession 两个方向都删:本会话派发出去的与派发了本会话的。
func TestSessionDispatch_DeleteBySessionRemovesBothDirections(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `session_dispatches` WHERE dispatcher_session_id = \\? OR dispatched_session_id = \\?").
		WithArgs(int64(7), int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, session_dispatch_repo.NewSessionDispatch().DeleteBySession(ctx, 7))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionDispatch_DeleteBySessionReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `session_dispatches`").WillReturnError(errors.New("locked"))
	mock.ExpectRollback()

	assert.Error(t, session_dispatch_repo.NewSessionDispatch().DeleteBySession(ctx, 7))
}

// FinishUnfinished 把还没进入终态的行一次收成给定终态(进程重启后没人再跟踪它们)。
func TestSessionDispatch_FinishUnfinishedClosesEveryOpenRow(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `session_dispatches` SET `finishtime`=\\?,`status`=\\? WHERE finishtime = 0").
		WithArgs(int64(5000), "canceled").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	n, err := session_dispatch_repo.NewSessionDispatch().FinishUnfinished(ctx, "canceled", 5000)
	require.NoError(t, err)
	assert.EqualValues(t, 2, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestSessionDispatch_FinishUnfinishedReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `session_dispatches`").WillReturnError(errors.New("locked"))
	mock.ExpectRollback()

	_, err := session_dispatch_repo.NewSessionDispatch().FinishUnfinished(ctx, "canceled", 5000)
	assert.Error(t, err)
}
