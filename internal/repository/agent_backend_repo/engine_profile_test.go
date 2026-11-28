package agent_backend_repo_test

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
)

func setupEngineProfileRepoTest(t *testing.T) (context.Context, sqlmock.Sqlmock, agent_backend_repo.EngineProfileRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, agent_backend_repo.NewEngineProfile()
}

var engineProfileColumns = []string{"id", "type", "name", "config_json", "is_account_default", "status", "sync_id"}

func TestEngineProfileRepo_Create(t *testing.T) {
	convey.Convey("Create", t, func() {
		ctx, mock, repo := setupEngineProfileRepoTest(t)

		convey.Convey("Given a codex profile When created Then its exclusive settings land in config_json and it gets a sync identity", func() {
			p := &agent_backend_entity.EngineProfile{Type: "codex", Name: "Codex", Sandbox: "workspace-write", Status: consts.ACTIVE}
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `engine_profiles`").WillReturnResult(sqlmock.NewResult(9, 1))
			mock.ExpectCommit()

			require.NoError(t, repo.Create(ctx, p))
			assert.Equal(t, int64(9), p.ID)
			assert.JSONEq(t, `{"sandbox":"workspace-write"}`, p.ConfigJSON)
			assert.NotEmpty(t, p.SyncID)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("Given the driver fails When created Then the error is returned", func() {
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `engine_profiles`").WillReturnError(errors.New("boom"))
			mock.ExpectRollback()

			err := repo.Create(ctx, &agent_backend_entity.EngineProfile{Type: "codex", Name: "Codex"})
			assert.EqualError(t, err, "boom")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestEngineProfileRepo_Reads(t *testing.T) {
	convey.Convey("reads", t, func() {
		ctx, mock, repo := setupEngineProfileRepoTest(t)

		convey.Convey("Given an active row When found by id Then config_json is spread back onto the fields", func() {
			mock.ExpectQuery("SELECT \\* FROM `engine_profiles` WHERE id = \\? AND status = \\? ORDER BY `engine_profiles`.`id` LIMIT \\?").
				WithArgs(int64(3), consts.ACTIVE, 1).
				WillReturnRows(sqlmock.NewRows(engineProfileColumns).AddRow(3, "codex", "Codex", `{"sandbox":"read-only"}`, false, consts.ACTIVE, "01A"))

			got, err := repo.Find(ctx, 3)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, "read-only", got.Sandbox)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("Given no row When found by id Then nil without error", func() {
			mock.ExpectQuery("SELECT \\* FROM `engine_profiles` WHERE id = \\? AND status = \\?").
				WithArgs(int64(4), consts.ACTIVE, 1).
				WillReturnRows(sqlmock.NewRows(engineProfileColumns))

			got, err := repo.Find(ctx, 4)
			require.NoError(t, err)
			assert.Nil(t, got)
		})

		convey.Convey("Given a deleted row When found regardless of status Then the last settings are still readable", func() {
			mock.ExpectQuery("SELECT \\* FROM `engine_profiles` WHERE id = \\? ORDER BY `engine_profiles`.`id` LIMIT \\?").
				WithArgs(int64(3), 1).
				WillReturnRows(sqlmock.NewRows(engineProfileColumns).AddRow(3, "codex", "Codex", `{"approval":"never"}`, false, consts.DELETE, "01A"))

			got, err := repo.FindAnyStatus(ctx, 3)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.False(t, got.IsActive())
			assert.Equal(t, "never", got.Approval)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("When listed Then only active rows come back in id order", func() {
			mock.ExpectQuery("SELECT \\* FROM `engine_profiles` WHERE status = \\? ORDER BY id ASC").
				WithArgs(consts.ACTIVE).
				WillReturnRows(sqlmock.NewRows(engineProfileColumns).
					AddRow(1, "claudecode", "Claude Code", "{}", true, consts.ACTIVE, "01A").
					AddRow(2, "codex", "Codex", "{}", false, consts.ACTIVE, "01B"))

			rows, err := repo.List(ctx)
			require.NoError(t, err)
			assert.Len(t, rows, 2)
			assert.True(t, rows[0].IsAccountDefault)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("When the account default is asked Then it is the active row carrying the mark", func() {
			mock.ExpectQuery("SELECT \\* FROM `engine_profiles` WHERE is_account_default = \\? AND status = \\? ORDER BY `engine_profiles`.`id` LIMIT \\?").
				WithArgs(true, consts.ACTIVE, 1).
				WillReturnRows(sqlmock.NewRows(engineProfileColumns).AddRow(1, "claudecode", "Claude Code", "{}", true, consts.ACTIVE, "01A"))

			got, err := repo.FindAccountDefault(ctx)
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, int64(1), got.ID)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestEngineProfileRepo_Writes(t *testing.T) {
	convey.Convey("writes", t, func() {
		ctx, mock, repo := setupEngineProfileRepoTest(t)

		convey.Convey("When an account default is set Then the old mark is cleared before the new one is written, in one transaction", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `engine_profiles` SET `is_account_default`=\\? WHERE is_account_default = \\? AND id <> \\?").
				WithArgs(false, true, int64(5)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE `engine_profiles` SET `is_account_default`=\\? WHERE id = \\? AND status = \\?").
				WithArgs(true, int64(5), consts.ACTIVE).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			require.NoError(t, repo.SetAccountDefault(ctx, 5))
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("Given the target is gone When an account default is set Then the clear is rolled back", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `engine_profiles` SET `is_account_default`=\\? WHERE is_account_default = \\? AND id <> \\?").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE `engine_profiles` SET `is_account_default`=\\? WHERE id = \\? AND status = \\?").
				WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectRollback()

			assert.Error(t, repo.SetAccountDefault(ctx, 5))
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("When deleted Then it is a soft delete stamped with the time", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `engine_profiles` SET `status`=\\?,`updatetime`=\\? WHERE id = \\?").
				WithArgs(consts.DELETE, sqlmock.AnyArg(), int64(5)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			require.NoError(t, repo.Delete(ctx, 5))
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("When updated Then the row is saved with config_json refreshed but the account default mark untouched", func() {
			// 账号默认只由 SetAccountDefault 改：整行回写一份读得早的旧实体，会把刚移走的标记
			// 写回来（撞部分唯一索引），或把刚设上的标记抹掉。
			p := &agent_backend_entity.EngineProfile{ID: 5, Type: "codex", Name: "Codex", Approval: "on-request", Status: consts.ACTIVE}
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `engine_profiles` SET `type`=\\?,`name`=\\?,`llm_provider_key`=\\?,`model_key`=\\?,`env_json`=\\?,`reasoning_effort`=\\?,`config_json`=\\?,`status`=\\?,`createtime`=\\?,`updatetime`=\\?,`sync_id`=\\?,`sync_account_id`=\\?,`sync_version`=\\?,`sync_updated_at`=\\?,`sync_origin_fingerprint`=\\?,`sync_deleted_at`=\\? WHERE `id` = \\?").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			require.NoError(t, repo.Update(ctx, p))
			assert.JSONEq(t, `{"approval":"on-request"}`, p.ConfigJSON)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}
