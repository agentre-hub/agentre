package syncqueue_repo_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/repository/syncqueue_repo"
)

func setupLostChangeRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, syncqueue_repo.LostChangeRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, syncqueue_repo.NewLostChange()
}

func TestLostChangeRepo_Create(t *testing.T) {
	ctx, mock, repo := setupLostChangeRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sync_lost_changes`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, &syncqueue_entity.LostChange{
		SyncAccountID: 1,
		EntityType:    "project",
		EntitySyncID:  "proj-sync-1",
		Reason:        syncqueue_entity.ReasonOverwritten,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLostChangeRepo_ListByAccount(t *testing.T) {
	ctx, mock, repo := setupLostChangeRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `sync_lost_changes` WHERE sync_account_id = \\? ORDER BY occurred_at DESC, id DESC").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "sync_account_id", "entity_type"}).
			AddRow(int64(1), int64(1), "project"))

	rows, err := repo.ListByAccount(ctx, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "project", rows[0].EntityType)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLostChangeRepo_Delete(t *testing.T) {
	ctx, mock, repo := setupLostChangeRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_lost_changes` WHERE id = \\?").
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(ctx, 1))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestLostChangeRepo_ListExpired 30 天回收只把到期的行读回来：条件落在 SQL 里
// （命中 idx_sync_lost_changes_account），而不是全表读回来再在 Go 里比。
func TestLostChangeRepo_ListExpired(t *testing.T) {
	ctx, mock, repo := setupLostChangeRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `sync_lost_changes` WHERE sync_account_id = \\? AND createtime <= \\? ORDER BY createtime ASC, id ASC").
		WithArgs(int64(1), int64(1000)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "sync_account_id", "entity_type", "createtime"}).
			AddRow(int64(2), int64(1), "project", int64(1000)))

	rows, err := repo.ListExpired(ctx, 1, 1000)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1000), rows[0].Createtime)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLostChangeRepo_ListExpired_GivenQueryFails_ReturnsTheError(t *testing.T) {
	ctx, mock, repo := setupLostChangeRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `sync_lost_changes`").WillReturnError(assert.AnError)

	_, err := repo.ListExpired(ctx, 1, 1000)
	require.ErrorIs(t, err, assert.AnError)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLostChangeRepo_DeleteMany(t *testing.T) {
	ctx, mock, repo := setupLostChangeRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_lost_changes` WHERE id IN \\(\\?,\\?\\)").
		WithArgs(int64(1), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteMany(ctx, []int64{1, 2}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLostChangeRepo_DeleteMany_GivenNoIDs_SendsNothing(t *testing.T) {
	ctx, mock, repo := setupLostChangeRepo(t)
	require.NoError(t, repo.DeleteMany(ctx, nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestLostChangeRepo_DeleteMany_GivenExecFails_ReturnsTheError(t *testing.T) {
	ctx, mock, repo := setupLostChangeRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_lost_changes`").WillReturnError(assert.AnError)
	mock.ExpectRollback()

	require.ErrorIs(t, repo.DeleteMany(ctx, []int64{1, 2}), assert.AnError)
	assert.NoError(t, mock.ExpectationsWereMet())
}
