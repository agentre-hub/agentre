package hook_repo_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

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

func TestHookEventRepo_FindByDedupeKey_Found(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	rows := sqlmock.NewRows([]string{"id", "hook_id", "dedupe_key", "status"}).
		AddRow(5, 7, "K1", consts.ACTIVE)
	mock.ExpectQuery(`SELECT \* FROM .hook_events. WHERE hook_id = \? AND dedupe_key = \? AND status = \?`).
		WithArgs(int64(7), "K1", consts.ACTIVE, 1).
		WillReturnRows(rows)

	got, err := hook_repo.NewHookEvent().FindByDedupeKey(ctx, 7, "K1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(5), got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHookEventRepo_FindByDedupeKey_NotFound(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery(`SELECT \* FROM .hook_events.`).
		WithArgs(int64(7), "K1", consts.ACTIVE, 1).
		WillReturnError(gorm.ErrRecordNotFound)

	got, err := hook_repo.NewHookEvent().FindByDedupeKey(ctx, 7, "K1")
	require.NoError(t, err)
	assert.Nil(t, got, "record-not-found should map to (nil, nil)")
	assert.NoError(t, mock.ExpectationsWereMet())
}
