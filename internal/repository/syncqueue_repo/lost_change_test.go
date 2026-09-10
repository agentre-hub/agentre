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
