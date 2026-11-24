package workspace_fs_repo_test

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/workspace_fs_entity"
	"github.com/agentre-hub/agentre/internal/repository/workspace_fs_repo"
)

// Given 一个新认领的工作根, When 记下它, Then 写出会话 / 路径 / worktree 短名与建行
// 时间;同一 (session_id, path) 已有时什么也不改(认领是幂等的,不因重复认领报错)。
func TestWorkRoot_CreateIsIdempotentOnSessionAndPath(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `session_work_roots` \\(`session_id`,`path`,`worktree`,`createtime`\\) VALUES \\(\\?,\\?,\\?,\\?\\) ON DUPLICATE KEY UPDATE `id`=`id`").
		WithArgs(int64(42), "/repo/.claude/worktrees/x", "x", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	row := &workspace_fs_entity.WorkRoot{SessionID: 42, Path: "/repo/.claude/worktrees/x", Worktree: "x"}
	require.NoError(t, workspace_fs_repo.NewWorkRoot().Create(ctx, row))
	assert.NotZero(t, row.Createtime, "建行时间必须被填上")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkRoot_CreateReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `session_work_roots`").WillReturnError(errors.New("disk full"))
	mock.ExpectRollback()

	err := workspace_fs_repo.NewWorkRoot().Create(ctx, &workspace_fs_entity.WorkRoot{SessionID: 42, Path: "/wt"})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ListBySession 只取这个会话的认领,先认领的在前(工作根集合按认领先后排列)。
func TestWorkRoot_ListBySessionReturnsOldestFirst(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `session_work_roots` WHERE session_id = \\? ORDER BY id ASC").
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "path", "worktree", "createtime"}).
			AddRow(1, 42, "/wt-a", "wt-a", 1700).
			AddRow(2, 42, "/main", "", 1800))

	rows, err := workspace_fs_repo.NewWorkRoot().ListBySession(ctx, 42)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "/wt-a", rows[0].Path)
	assert.Equal(t, "wt-a", rows[0].Worktree)
	assert.Equal(t, "/main", rows[1].Path)
	assert.Equal(t, "", rows[1].Worktree)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkRoot_ListBySessionReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `session_work_roots`").WillReturnError(errors.New("locked"))

	_, err := workspace_fs_repo.NewWorkRoot().ListBySession(ctx, 42)
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Delete 只撤销这个会话的这一个根,不碰别的会话认领的同一路径。
func TestWorkRoot_DeleteRemovesOnlyThatSessionsRoot(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `session_work_roots` WHERE session_id = \\? AND path = \\?").
		WithArgs(int64(42), "/wt-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, workspace_fs_repo.NewWorkRoot().Delete(ctx, 42, "/wt-a"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// DeleteBySession 是会话删除时的清理:这个会话的认领一行不留。
func TestWorkRoot_DeleteBySessionRemovesAllOfThatSession(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `session_work_roots` WHERE session_id = \\?").
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, workspace_fs_repo.NewWorkRoot().DeleteBySession(ctx, 42))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkRoot_DeleteReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `session_work_roots`").WillReturnError(errors.New("locked"))
	mock.ExpectRollback()

	assert.Error(t, workspace_fs_repo.NewWorkRoot().Delete(ctx, 42, "/wt-a"))
	assert.NoError(t, mock.ExpectationsWereMet())
}
