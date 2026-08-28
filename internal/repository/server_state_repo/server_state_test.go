package server_state_repo_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/server_state_entity"
	"github.com/agentre-hub/agentre/internal/repository/server_state_repo"
)

func TestServerStateRepo_Get(t *testing.T) {
	convey.Convey("Get returns the seeded single row", t, func() {
		ctx, _, mock := testutils.Database(t)
		rows := sqlmock.NewRows([]string{"id", "server_url", "device_id", "device_fingerprint", "server_user_id", "server_user_email", "server_user_login", "server_user_avatar_url", "keychain_account", "access_expires_at", "last_synced_at", "updatetime"}).
			AddRow(int64(1), "https://s.local", int64(0), "fp-x", int64(0), "", "", "", "", int64(0), int64(0), int64(0))
		mock.ExpectQuery("SELECT \\* FROM `server_state` WHERE `server_state`.`id` = \\?").
			WithArgs(int64(1), 1).
			WillReturnRows(rows)

		got, err := server_state_repo.NewServerState().Get(ctx)
		assert.NoError(t, err)
		assert.NotNil(t, got)
		assert.Equal(t, int64(1), got.ID)
		assert.Equal(t, "https://s.local", got.ServerURL)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	convey.Convey("Get returns (nil, nil) when row is absent", t, func() {
		ctx, _, mock := testutils.Database(t)
		mock.ExpectQuery("SELECT \\* FROM `server_state`").
			WillReturnError(gorm.ErrRecordNotFound)

		got, err := server_state_repo.NewServerState().Get(ctx)
		assert.NoError(t, err)
		assert.Nil(t, got)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServerStateRepo_Save(t *testing.T) {
	convey.Convey("Save upserts the single row and touches updatetime", t, func() {
		ctx, _, mock := testutils.Database(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `server_state` SET").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		e := &server_state_entity.ServerState{ID: 1, ServerURL: "https://s.local"}
		err := server_state_repo.NewServerState().Save(ctx, e)
		assert.NoError(t, err)
		assert.Greater(t, e.Updatetime, int64(0), "Save should Touch() so updatetime > 0")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestServerStateRepo_ClearLoginFields(t *testing.T) {
	convey.Convey("ClearLoginFields zeroes user/device/keychain but keeps server_url", t, func() {
		ctx, _, mock := testutils.Database(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `server_state` SET").
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		err := server_state_repo.NewServerState().ClearLoginFields(ctx)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
