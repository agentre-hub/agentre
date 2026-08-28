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

func setupInboundQueueRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, syncqueue_repo.InboundQueueRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, syncqueue_repo.NewInboundQueue()
}

func TestInboundQueueRepo_Create(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sync_inbound_queue`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, &syncqueue_entity.InboundQueueItem{
		SyncAccountID: 1,
		EntityType:    "agent",
		EntitySyncID:  "agent-sync-1",
		MissingSyncID: "department-sync-missing",
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboundQueueRepo_ListByAccount(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `sync_inbound_queue` WHERE sync_account_id = \\? ORDER BY received_at ASC, id ASC").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "sync_account_id", "missing_sync_id"}).
			AddRow(int64(1), int64(1), "department-sync-missing"))

	rows, err := repo.ListByAccount(ctx, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "department-sync-missing", rows[0].MissingSyncID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboundQueueRepo_Delete(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_inbound_queue` WHERE id = \\?").
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(ctx, 1))
	assert.NoError(t, mock.ExpectationsWereMet())
}
