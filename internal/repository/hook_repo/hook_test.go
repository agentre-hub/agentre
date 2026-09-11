package hook_repo_test

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/hook_entity"
	"github.com/agentre-hub/agentre/internal/repository/hook_repo"
)

func TestHookRepo_ListDue(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	rows := sqlmock.NewRows([]string{"id", "name", "enabled", "next_run_at", "status"}).
		AddRow(1, "due", 1, 100, consts.ACTIVE)
	mock.ExpectQuery(`SELECT \* FROM .hooks. WHERE enabled = 1 AND next_run_at <= \? AND status = \? ORDER BY next_run_at ASC`).
		WithArgs(int64(150), consts.ACTIVE).
		WillReturnRows(rows)

	got, err := hook_repo.NewHook().ListDue(ctx, 150)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "due", got[0].Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func newHookEventForDedupe() *hook_entity.HookEvent {
	return &hook_entity.HookEvent{
		HookID: 7, Kind: hook_entity.HookEventKindOutput, Title: "t", DedupeKey: "K1",
		PayloadJSON: "{}", ReceivedAt: 1000, Status: consts.ACTIVE, Createtime: 1000, Updatetime: 1000,
	}
}

func TestHookEventRepo_CreateIfAbsent_Created(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `hook_events`").
		WillReturnResult(sqlmock.NewResult(5, 1))
	mock.ExpectCommit()

	created, err := hook_repo.NewHookEvent().CreateIfAbsent(ctx, newHookEventForDedupe())
	require.NoError(t, err)
	assert.True(t, created, "no existing row for this (hook_id, dedupe_key) → insert must land")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// 撞上部分唯一索引 ux_hook_events_dedupe：ON CONFLICT DO NOTHING 影响行数为 0，
// 不报错——本次运行内重复或与另一次并发运行撞车都走这条路径。
func TestHookEventRepo_CreateIfAbsent_DuplicateKey(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `hook_events`").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	created, err := hook_repo.NewHookEvent().CreateIfAbsent(ctx, newHookEventForDedupe())
	require.NoError(t, err)
	assert.False(t, created, "existing (hook_id, dedupe_key) row must not error, just report not-created")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHookEventRepo_CreateIfAbsent_Error(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `hook_events`").
		WillReturnError(errors.New("disk I/O error"))
	mock.ExpectRollback()

	created, err := hook_repo.NewHookEvent().CreateIfAbsent(ctx, newHookEventForDedupe())
	assert.Error(t, err)
	assert.False(t, created)
	assert.NoError(t, mock.ExpectationsWereMet())
}
