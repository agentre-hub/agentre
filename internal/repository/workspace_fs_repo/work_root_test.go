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
// 时间;同一 (session_id, path) 已有时不报错(认领是幂等的),而是把它重新记成已认领:
// 撤销 / 移除过的同一路径被认领回来时清掉 removed_at,worktree 短名取这次的判定。
func TestWorkRoot_CreateIsIdempotentOnSessionAndPathAndReclaimsARemovedRow(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `session_work_roots` \\(`session_id`,`path`,`worktree`,`createtime`,`removed_at`\\) VALUES \\(\\?,\\?,\\?,\\?,\\?\\) ON DUPLICATE KEY UPDATE `removed_at`=\\?,`worktree`=VALUES\\(`worktree`\\)").
		WithArgs(int64(42), "/repo/.claude/worktrees/x", "x", sqlmock.AnyArg(), int64(0), int64(0)).
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

// ListBySession 取这个会话的全部行(已认领的与已移除的),先认领的在前(工作根集合按
// 认领先后排列);已移除的行带 removed_at。
func TestWorkRoot_ListBySessionReturnsOldestFirst(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `session_work_roots` WHERE session_id = \\? ORDER BY id ASC").
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "path", "worktree", "createtime", "removed_at"}).
			AddRow(1, 42, "/wt-a", "wt-a", 1700, 0).
			AddRow(2, 42, "/main", "", 1800, 0).
			AddRow(3, 42, "/wt-gone", "wt-gone", 1900, 2000))

	rows, err := workspace_fs_repo.NewWorkRoot().ListBySession(ctx, 42)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	assert.Equal(t, "/wt-a", rows[0].Path)
	assert.Zero(t, rows[0].RemovedAt)
	assert.Equal(t, int64(2000), rows[2].RemovedAt)
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

// MarkRemoved 把这个会话的这一个根记成已移除(行留着,removed_at = 移除时刻):根消失后
// 「它曾是这个会话的一个根」仍可查,变更窗据此画「已移除」分组。已移除的行不重复改写。
func TestWorkRoot_MarkRemovedStampsOnlyThatSessionsClaimedRow(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `session_work_roots` SET `removed_at`=\\? WHERE session_id = \\? AND path = \\? AND removed_at = 0").
		WithArgs(int64(1700000000000), int64(42), "/wt-a").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, workspace_fs_repo.NewWorkRoot().MarkRemoved(ctx, 42, "/wt-a", 1700000000000))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWorkRoot_MarkRemovedReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `session_work_roots`").WillReturnError(errors.New("locked"))
	mock.ExpectRollback()

	assert.Error(t, workspace_fs_repo.NewWorkRoot().MarkRemoved(ctx, 42, "/wt-a", 1))
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
