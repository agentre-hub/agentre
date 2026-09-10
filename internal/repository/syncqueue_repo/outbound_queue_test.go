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

func setupOutboundQueueRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, syncqueue_repo.OutboundQueueRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, syncqueue_repo.NewOutboundQueue()
}

func TestOutboundQueueRepo_Create(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sync_outbound_queue`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, &syncqueue_entity.OutboundQueueItem{
		SyncAccountID: 1,
		EntityType:    "department",
		LocalID:       42,
		Op:            syncqueue_entity.OpCreate,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOutboundQueueRepo_ListByAccount(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `sync_outbound_queue` WHERE sync_account_id = \\? ORDER BY queued_at ASC, id ASC").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "sync_account_id", "op"}).
			AddRow(int64(1), int64(1), syncqueue_entity.OpCreate))

	rows, err := repo.ListByAccount(ctx, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, syncqueue_entity.OpCreate, rows[0].Op)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestOutboundQueueRepo_Delete(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_outbound_queue` WHERE id = \\?").
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(ctx, 1))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOutboundQueueRepo_DeleteMany 钉死批量删除走**一条** DELETE ... IN (...)。
//
// 此前上行刷队列是 for id := range ids { Delete(id) },每条一个 autocommit 事务
// (BEGIN IMMEDIATE + commit)。实测本机 sync_outbound_queue 积压 871 行,一次刷
// 队列就是 871 次取写锁 —— 而这把锁正好和流式落库抢同一个 SQLite 写锁。
func TestOutboundQueueRepo_DeleteMany(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_outbound_queue` WHERE id IN \\(\\?,\\?,\\?\\)").
		WithArgs(int64(1), int64(2), int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteMany(ctx, []int64{1, 2, 3}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOutboundQueueRepo_DeleteManyEmptyIsNoOp 空列表不得发语句(否则 GORM 会生成
// 一条没有 WHERE 的 DELETE,把整张表清空)。
func TestOutboundQueueRepo_DeleteManyEmptyIsNoOp(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)
	require.NoError(t, repo.DeleteMany(ctx, nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOutboundQueueRepo_DeleteManyChunksBeyondVariableLimit 超过单条语句变量上限时
// 必须分批,而不是拼出一条几千个占位符的语句(SQLITE_MAX_VARIABLE_NUMBER)。
func TestOutboundQueueRepo_DeleteManyChunksBeyondVariableLimit(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)

	ids := make([]int64, syncqueue_repo.DeleteManyChunkSize+1)
	for i := range ids {
		ids[i] = int64(i + 1)
	}

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_outbound_queue` WHERE id IN \\(").
		WillReturnResult(sqlmock.NewResult(0, int64(syncqueue_repo.DeleteManyChunkSize)))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_outbound_queue` WHERE id IN \\(").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteMany(ctx, ids))
	assert.NoError(t, mock.ExpectationsWereMet())
}
