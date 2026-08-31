package sync_account_repo_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/repository/sync_account_repo"
)

func TestSyncAccountRepo_EnsureKey(t *testing.T) {
	convey.Convey("EnsureKey returns the existing key for a pair this desktop already knows", t, func() {
		ctx, _, mock := testutils.Database(t)
		mock.ExpectQuery("SELECT \\* FROM `sync_accounts` WHERE server_url = \\? AND remote_user_id = \\?").
			WithArgs("https://a.example", int64(1), 1).
			WillReturnRows(sqlmock.NewRows([]string{"id", "server_url", "remote_user_id", "createtime"}).
				AddRow(int64(7), "https://a.example", int64(1), int64(0)))

		got, err := sync_account_repo.NewSyncAccount().EnsureKey(ctx, "https://a.example", 1)
		assert.NoError(t, err)
		assert.Equal(t, int64(7), got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	convey.Convey("EnsureKey allocates a new key for a pair it has never seen", t, func() {
		ctx, _, mock := testutils.Database(t)
		mock.ExpectQuery("SELECT \\* FROM `sync_accounts`").
			WillReturnError(gorm.ErrRecordNotFound)
		mock.ExpectBegin()
		// 冲突不报错、什么都不做：两条路径（编辑当场上行与 30 秒轮询）可能同时
		// 第一次问同一个账号，谁先插进去都行，后到的那个回头再查一次。
		mock.ExpectExec("INSERT INTO `sync_accounts`").
			WithArgs("https://b.example", int64(1), sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(9, 1))
		mock.ExpectCommit()

		got, err := sync_account_repo.NewSyncAccount().EnsureKey(ctx, "https://b.example", 1)
		assert.NoError(t, err)
		assert.Equal(t, int64(9), got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	convey.Convey("EnsureKey re-reads when a concurrent insert won the race", t, func() {
		ctx, _, mock := testutils.Database(t)
		mock.ExpectQuery("SELECT \\* FROM `sync_accounts`").WillReturnError(gorm.ErrRecordNotFound)
		mock.ExpectBegin()
		// 冲突时 ON CONFLICT DO NOTHING 一行都没插，主键留在 0。
		mock.ExpectExec("INSERT INTO `sync_accounts`").WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()
		mock.ExpectQuery("SELECT \\* FROM `sync_accounts`").
			WillReturnRows(sqlmock.NewRows([]string{"id", "server_url", "remote_user_id", "createtime"}).
				AddRow(int64(9), "https://b.example", int64(1), int64(0)))

		got, err := sync_account_repo.NewSyncAccount().EnsureKey(ctx, "https://b.example", 1)
		assert.NoError(t, err)
		assert.Equal(t, int64(9), got, "抢输的那一方要拿到赢家分配的键，绝不能自己再造一个")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
