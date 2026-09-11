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

// TestOutboundQueueRepo_CreateMany 钉死批量入队走**一条** INSERT 语句(要求 15)：
// 此前 enqueue 是 for row := range rows { Create(row) },一条改动带 k 个从属行就是
// k+1 次单独的 BEGIN IMMEDIATE。
func TestOutboundQueueRepo_CreateMany(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sync_outbound_queue`").
		WillReturnResult(sqlmock.NewResult(1, 2))
	mock.ExpectCommit()

	err := repo.CreateMany(ctx, []*syncqueue_entity.OutboundQueueItem{
		{SyncAccountID: 1, EntityType: "project", LocalID: 1, Op: syncqueue_entity.OpCreate},
		{SyncAccountID: 1, EntityType: "agent", LocalID: 2, Op: syncqueue_entity.OpCreate},
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOutboundQueueRepo_CreateManyEmptyIsNoOp 空切片不得发语句(否则 GORM 的
// CreateInBatches 在长度为 0 时仍会打开一段没有语句的事务)。
func TestOutboundQueueRepo_CreateManyEmptyIsNoOp(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)
	require.NoError(t, repo.CreateMany(ctx, nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOutboundQueueRepo_CreateManyChunksBeyondBatchSize 超过单批上限(100,8 列留足
// SQLITE_MAX_VARIABLE_NUMBER 余量)时分批插入,但整体仍在**一个**事务里
// (gorm.CreateInBatches 对多批调用 tx.Transaction),不是 DeleteMany 那种每批一个
// autocommit 事务。
func TestOutboundQueueRepo_CreateManyChunksBeyondBatchSize(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)

	rows := make([]*syncqueue_entity.OutboundQueueItem, syncqueue_repo.CreateManyBatchSize+1)
	for i := range rows {
		rows[i] = &syncqueue_entity.OutboundQueueItem{
			SyncAccountID: 1, EntityType: "project", LocalID: int64(i + 1), Op: syncqueue_entity.OpCreate,
		}
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sync_outbound_queue`").
		WillReturnResult(sqlmock.NewResult(1, int64(syncqueue_repo.CreateManyBatchSize)))
	mock.ExpectExec("INSERT INTO `sync_outbound_queue`").
		WillReturnResult(sqlmock.NewResult(int64(syncqueue_repo.CreateManyBatchSize)+1, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.CreateMany(ctx, rows))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOutboundQueueRepo_CreateManyPropagatesError 写入失败时事务回滚、错误照实返回。
func TestOutboundQueueRepo_CreateManyPropagatesError(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sync_outbound_queue`").
		WillReturnError(assert.AnError)
	mock.ExpectRollback()

	err := repo.CreateMany(ctx, []*syncqueue_entity.OutboundQueueItem{
		{SyncAccountID: 1, EntityType: "project", LocalID: 1, Op: syncqueue_entity.OpCreate},
	})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOutboundQueueRepo_ReassignAccount 钉死匿名认领走**一条** UPDATE(要求 15)：
// 此前 claimAnonymousQueue 是先读整批行,再逐行 Create + Delete,M 行就是
// 1 次读 + 2M 次写事务,且这一对不是原子的。改成一条集合 UPDATE 后原行的 id、
// EntitySyncID、Op、QueuedAt 都不变——只有 sync_account_id 换了主人。
func TestOutboundQueueRepo_ReassignAccount(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `sync_outbound_queue` SET .*sync_account_id.*=\\? WHERE sync_account_id = \\?").
		WithArgs(int64(7), int64(0)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, repo.ReassignAccount(ctx, 0, 7))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestOutboundQueueRepo_ReassignAccountPropagatesError 写入失败时错误照实返回。
func TestOutboundQueueRepo_ReassignAccountPropagatesError(t *testing.T) {
	ctx, mock, repo := setupOutboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `sync_outbound_queue`").
		WillReturnError(assert.AnError)
	mock.ExpectRollback()

	err := repo.ReassignAccount(ctx, 0, 7)
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
