package chat_repo_test

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

const pendingConversationID = "0190f5d2-6b3a-7c41-9a55-2f1d3e4b5c6d"

// Given 一笔还没补发的删除, When 记下它, Then 按 (conversation_id, target) upsert:
// 同一条对话对同一个对象重复记只留一行，并刷新执行端信息。
func TestPendingDelete_CreateUpsertsOnConversationAndTarget(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `chat_pending_deletes` \\(`conversation_id`,`target`,`machine_fingerprint`,`device_id`,`createtime`\\) VALUES \\(\\?,\\?,\\?,\\?,\\?\\) "+
		"ON DUPLICATE KEY UPDATE `machine_fingerprint`=VALUES\\(`machine_fingerprint`\\),`device_id`=VALUES\\(`device_id`\\)").
		WithArgs(pendingConversationID, "agentred", "sha256:carrier", int64(7), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := chat_repo.NewPendingDelete().Create(ctx, &chat_entity.PendingDelete{
		ConversationID:     pendingConversationID,
		Target:             chat_entity.PendingDeleteTargetAgentred,
		MachineFingerprint: "sha256:carrier",
		DeviceID:           7,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPendingDelete_CreateReturnsDatabaseError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `chat_pending_deletes`").WillReturnError(errors.New("disk full"))
	mock.ExpectRollback()

	err := chat_repo.NewPendingDelete().Create(ctx, &chat_entity.PendingDelete{
		ConversationID: pendingConversationID, Target: chat_entity.PendingDeleteTargetServer,
	})
	assert.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// List 交回全部待删除，最早记下的在前（补发按记下的先后来）。
func TestPendingDelete_ListReturnsOldestFirst(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_pending_deletes` ORDER BY id ASC").
		WillReturnRows(sqlmock.NewRows([]string{"id", "conversation_id", "target", "machine_fingerprint", "device_id", "createtime"}).
			AddRow(1, pendingConversationID, "server", "sha256:carrier", 0, 1700).
			AddRow(2, pendingConversationID, "agentred", "sha256:carrier", 7, 1800))

	rows, err := chat_repo.NewPendingDelete().List(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, chat_entity.PendingDeleteTargetServer, rows[0].Target)
	assert.Equal(t, chat_entity.PendingDeleteTargetAgentred, rows[1].Target)
	assert.Equal(t, int64(7), rows[1].DeviceID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Remove 只删这条对话对这个对象的那一笔，不碰同一条对话挂给别的对象的待删除。
func TestPendingDelete_RemoveDeletesOnlyThatTarget(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `chat_pending_deletes` WHERE conversation_id = \\? AND target = \\?").
		WithArgs(pendingConversationID, "server").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := chat_repo.NewPendingDelete().Remove(ctx, pendingConversationID, chat_entity.PendingDeleteTargetServer)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
