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

func TestInboundQueueRepo_ListByAccount(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `sync_inbound_queue` WHERE sync_account_id = \\? ORDER BY received_at ASC, id ASC").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "sync_account_id", "entity_sync_id"}).
			AddRow(int64(1), int64(1), "agent-sync-1"))

	rows, err := repo.ListByAccount(ctx, 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "agent-sync-1", rows[0].EntitySyncID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func discardedLostChange(syncID string) *syncqueue_entity.LostChange {
	return &syncqueue_entity.LostChange{
		SyncAccountID: 1, EntityType: "agent", EntitySyncID: syncID,
		Reason: syncqueue_entity.ReasonDiscarded, OccurredAt: 1000, Createtime: 1000,
	}
}

// TestInboundQueueRepo_DiscardToLostChanges 30 天回收的记录与出队是一个事务：先把每一行
// 记进「没能同步的改动」，再一条 DELETE ... IN 出队，最后一起提交。
func TestInboundQueueRepo_DiscardToLostChanges(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sync_lost_changes`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `sync_lost_changes`").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec("DELETE FROM `sync_inbound_queue` WHERE id IN \\(\\?,\\?\\)$").
		WithArgs(int64(3), int64(4)).WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.DiscardToLostChanges(ctx, []int64{3, 4},
		[]*syncqueue_entity.LostChange{discardedLostChange("agent-sync-1"), discardedLostChange("agent-sync-2")}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInboundQueueRepo_DiscardToLostChanges_GivenARecordFails_RollsBackWithoutDequeuing
// 记录落不下时一行都不出队、已经写下的记录随之回滚：出了队却没记下，这一行就悄悄丢了。
func TestInboundQueueRepo_DiscardToLostChanges_GivenARecordFails_RollsBackWithoutDequeuing(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sync_lost_changes`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("INSERT INTO `sync_lost_changes`").WillReturnError(assert.AnError)
	mock.ExpectRollback()

	err := repo.DiscardToLostChanges(ctx, []int64{3, 4},
		[]*syncqueue_entity.LostChange{discardedLostChange("agent-sync-1"), discardedLostChange("agent-sync-2")})
	require.ErrorIs(t, err, assert.AnError)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInboundQueueRepo_DiscardToLostChanges_GivenDequeueFails_RollsBackTheRecords 出队
// 失败时记录也不留：留下的话下一轮会把同一行再记一遍。
func TestInboundQueueRepo_DiscardToLostChanges_GivenDequeueFails_RollsBackTheRecords(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `sync_lost_changes`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("DELETE FROM `sync_inbound_queue`").WillReturnError(assert.AnError)
	mock.ExpectRollback()

	err := repo.DiscardToLostChanges(ctx, []int64{3}, []*syncqueue_entity.LostChange{discardedLostChange("agent-sync-1")})
	require.ErrorIs(t, err, assert.AnError)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInboundQueueRepo_DiscardToLostChanges_GivenNothing_SendsNothing 空批次不开事务。
func TestInboundQueueRepo_DiscardToLostChanges_GivenNothing_SendsNothing(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	require.NoError(t, repo.DiscardToLostChanges(ctx, nil, nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInboundQueueRepo_ListExpired 30 天回收只把到期的行读回来：条件落在 SQL 里
// （命中 idx_sync_inbound_queue_account），而不是全表读回来再在 Go 里比。
func TestInboundQueueRepo_ListExpired(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `sync_inbound_queue` WHERE sync_account_id = \\? AND received_at <= \\? ORDER BY received_at ASC, id ASC").
		WithArgs(int64(1), int64(1000)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "sync_account_id", "entity_sync_id", "received_at"}).
			AddRow(int64(3), int64(1), "agent-sync-1", int64(1000)))

	rows, err := repo.ListExpired(ctx, 1, 1000)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1000), rows[0].ReceivedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboundQueueRepo_ListExpired_GivenQueryFails_ReturnsTheError(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `sync_inbound_queue`").WillReturnError(assert.AnError)

	_, err := repo.ListExpired(ctx, 1, 1000)
	require.ErrorIs(t, err, assert.AnError)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// expectEarliestReceivedAt 期望 ReplaceForEntity 事务里的第一条语句：同一个同步标识
// 已有行里最早的收到时间。
func expectEarliestReceivedAt(mock sqlmock.Sqlmock) *sqlmock.ExpectedQuery {
	return mock.ExpectQuery("SELECT MIN\\(received_at\\) FROM `sync_inbound_queue` WHERE sync_account_id = \\? AND entity_type = \\? AND entity_sync_id = \\? AND received_at > 0").
		WithArgs(int64(1), "agent", "agent-sync-1")
}

// TestInboundQueueRepo_ReplaceForEntity_GivenAnEarlierDeferral_KeepsItsReceivedAt 一个
// 事务：读最早收到时间 → 删掉旧行 → 插入新行，新行带着最早那个时间落库。
func TestInboundQueueRepo_ReplaceForEntity_GivenAnEarlierDeferral_KeepsItsReceivedAt(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	expectEarliestReceivedAt(mock).WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(int64(500)))
	// 锚定整句：前一条 MIN 查询的条件漏进这条 DELETE 时（received_at > 0），旧行里没有
	// 收到时间的那些会留在队列里。
	mock.ExpectExec("DELETE FROM `sync_inbound_queue` WHERE sync_account_id = \\? AND entity_type = \\? AND entity_sync_id = \\?$").
		WithArgs(int64(1), "agent", "agent-sync-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `sync_inbound_queue`").
		WithArgs(int64(1), "agent", "agent-sync-1", "{}", int64(500)).
		WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectCommit()

	row := &syncqueue_entity.InboundQueueItem{
		SyncAccountID: 1, EntityType: "agent", EntitySyncID: "agent-sync-1",
		PayloadJSON: "{}", ReceivedAt: 2000,
	}
	require.NoError(t, repo.ReplaceForEntity(ctx, row))
	assert.Equal(t, int64(500), row.ReceivedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInboundQueueRepo_ReplaceForEntity_GivenNoEarlierDeferral_KeepsItsOwnReceivedAt
// 第一次暂缓：MIN 是 NULL，新行用自己的收到时间。
func TestInboundQueueRepo_ReplaceForEntity_GivenNoEarlierDeferral_KeepsItsOwnReceivedAt(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	expectEarliestReceivedAt(mock).WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(nil))
	mock.ExpectExec("DELETE FROM `sync_inbound_queue`").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT INTO `sync_inbound_queue`").
		WithArgs(int64(1), "agent", "agent-sync-1", "{}", int64(2000)).
		WillReturnResult(sqlmock.NewResult(9, 1))
	mock.ExpectCommit()

	row := &syncqueue_entity.InboundQueueItem{
		SyncAccountID: 1, EntityType: "agent", EntitySyncID: "agent-sync-1",
		PayloadJSON: "{}", ReceivedAt: 2000,
	}
	require.NoError(t, repo.ReplaceForEntity(ctx, row))
	assert.Equal(t, int64(2000), row.ReceivedAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInboundQueueRepo_ReplaceForEntity_GivenInsertFails_RollsBack 插入失败时旧行不能
// 已经被删：删除与插入同进同退，否则那一行就从队列里悄悄消失了。
func TestInboundQueueRepo_ReplaceForEntity_GivenInsertFails_RollsBack(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	expectEarliestReceivedAt(mock).WillReturnRows(sqlmock.NewRows([]string{"min"}).AddRow(int64(500)))
	mock.ExpectExec("DELETE FROM `sync_inbound_queue`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `sync_inbound_queue`").WillReturnError(assert.AnError)
	mock.ExpectRollback()

	err := repo.ReplaceForEntity(ctx, &syncqueue_entity.InboundQueueItem{
		SyncAccountID: 1, EntityType: "agent", EntitySyncID: "agent-sync-1", PayloadJSON: "{}", ReceivedAt: 2000,
	})
	require.ErrorIs(t, err, assert.AnError)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboundQueueRepo_ReplaceForEntity_GivenLookupFails_RollsBackWithoutWriting(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	expectEarliestReceivedAt(mock).WillReturnError(assert.AnError)
	mock.ExpectRollback()

	err := repo.ReplaceForEntity(ctx, &syncqueue_entity.InboundQueueItem{
		SyncAccountID: 1, EntityType: "agent", EntitySyncID: "agent-sync-1", PayloadJSON: "{}", ReceivedAt: 2000,
	})
	require.ErrorIs(t, err, assert.AnError)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboundQueueRepo_DeleteByEntity(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_inbound_queue` WHERE sync_account_id = \\? AND entity_type = \\? AND entity_sync_id = \\?").
		WithArgs(int64(1), "agent", "agent-sync-1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteByEntity(ctx, 1, "agent", "agent-sync-1"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestInboundQueueRepo_DeleteByEntity_GivenExecFails_ReturnsTheError(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_inbound_queue`").WillReturnError(assert.AnError)
	mock.ExpectRollback()

	require.ErrorIs(t, repo.DeleteByEntity(ctx, 1, "agent", "agent-sync-1"), assert.AnError)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInboundQueueRepo_DeleteMany 一条 DELETE ... IN (...)，超过变量上限分批。
func TestInboundQueueRepo_DeleteMany(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	ids := make([]int64, syncqueue_repo.DeleteManyChunkSize+1)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_inbound_queue` WHERE id IN \\(").
		WillReturnResult(sqlmock.NewResult(0, int64(syncqueue_repo.DeleteManyChunkSize)))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_inbound_queue` WHERE id IN \\(\\?\\)").
		WithArgs(int64(syncqueue_repo.DeleteManyChunkSize + 1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteMany(ctx, ids))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInboundQueueRepo_DeleteMany_GivenNoIDs_SendsNothing 空列表不得发语句（否则是
// 一条没有 WHERE 的 DELETE）。
func TestInboundQueueRepo_DeleteMany_GivenNoIDs_SendsNothing(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	require.NoError(t, repo.DeleteMany(ctx, nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestInboundQueueRepo_DeleteMany_GivenAChunkFails_StopsAndReturnsTheError 一批失败就
// 停：调用方据此知道这一批没删掉，下一轮再来。
func TestInboundQueueRepo_DeleteMany_GivenAChunkFails_StopsAndReturnsTheError(t *testing.T) {
	ctx, mock, repo := setupInboundQueueRepo(t)
	ids := make([]int64, syncqueue_repo.DeleteManyChunkSize+1)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `sync_inbound_queue` WHERE id IN \\(").WillReturnError(assert.AnError)
	mock.ExpectRollback()

	require.ErrorIs(t, repo.DeleteMany(ctx, ids), assert.AnError)
	assert.NoError(t, mock.ExpectationsWereMet())
}
