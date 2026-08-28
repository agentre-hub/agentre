package app_setting_repo_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/app_setting_entity"
	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
)

func setupAppSettingRepoTest(t *testing.T) (context.Context, sqlmock.Sqlmock, app_setting_repo.AppSettingRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, app_setting_repo.NewAppSetting()
}

func TestAppSettingRepo_Get(t *testing.T) {
	convey.Convey("Get", t, func() {
		ctx, mock, repo := setupAppSettingRepoTest(t)

		convey.Convey("命中返回实体", func() {
			rows := sqlmock.NewRows([]string{"key", "value", "updatetime"}).
				AddRow("proxy.listen_port", "60080", int64(1700000000))
			mock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\? ORDER BY `app_settings`.`key` LIMIT \\?").
				WithArgs("proxy.listen_port", 1).
				WillReturnRows(rows)

			got, err := repo.Get(ctx, "proxy.listen_port")
			assert.NoError(t, err)
			if assert.NotNil(t, got) {
				assert.Equal(t, "60080", got.Value)
				assert.Equal(t, int64(1700000000), got.Updatetime)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("未命中返回 nil 不报错", func() {
			mock.ExpectQuery("SELECT \\* FROM `app_settings`").
				WithArgs("missing", 1).
				WillReturnError(gorm.ErrRecordNotFound)

			got, err := repo.Get(ctx, "missing")
			assert.NoError(t, err)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("其它错误透传", func() {
			mock.ExpectQuery("SELECT \\* FROM `app_settings`").
				WithArgs("x", 1).
				WillReturnError(sql.ErrConnDone)

			got, err := repo.Get(ctx, "x")
			assert.ErrorIs(t, err, sql.ErrConnDone)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestAppSettingRepo_Set(t *testing.T) {
	convey.Convey("Set", t, func() {
		ctx, mock, repo := setupAppSettingRepoTest(t)

		convey.Convey("Upsert 写入", func() {
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `app_settings`").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			err := repo.Set(ctx, &app_setting_entity.AppSetting{
				Key: "proxy.listen_port", Value: "60080", Updatetime: 1700000000,
			})
			assert.NoError(t, err)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动错误透传", func() {
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `app_settings`").
				WillReturnError(errors.New("boom"))
			mock.ExpectRollback()

			err := repo.Set(ctx, &app_setting_entity.AppSetting{
				Key: "x", Value: "1",
			})
			assert.EqualError(t, err, "boom")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestAppSettingRepo_List(t *testing.T) {
	convey.Convey("List", t, func() {
		ctx, mock, repo := setupAppSettingRepoTest(t)

		convey.Convey("按 key 升序返回", func() {
			rows := sqlmock.NewRows([]string{"key", "value", "updatetime"}).
				AddRow("proxy.listen_host", "127.0.0.1", int64(0)).
				AddRow("proxy.listen_port", "0", int64(0))
			mock.ExpectQuery("SELECT \\* FROM `app_settings` ORDER BY `key` ASC").
				WillReturnRows(rows)

			got, err := repo.List(ctx)
			assert.NoError(t, err)
			assert.Len(t, got, 2)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("空列表返回空 slice", func() {
			rows := sqlmock.NewRows([]string{"key", "value", "updatetime"})
			mock.ExpectQuery("SELECT \\* FROM `app_settings`").WillReturnRows(rows)

			got, err := repo.List(ctx)
			assert.NoError(t, err)
			assert.Empty(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}
