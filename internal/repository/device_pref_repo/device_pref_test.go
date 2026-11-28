package device_pref_repo_test

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

	"github.com/agentre-hub/agentre/internal/model/entity/device_pref_entity"
	"github.com/agentre-hub/agentre/internal/repository/device_pref_repo"
)

func setup(t *testing.T) (context.Context, sqlmock.Sqlmock) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock
}

var prefColumns = []string{"id", "device_fingerprint", "labels_json", "sort_order", "status", "sync_id"}
var cliColumns = []string{"id", "runtime_type", "device_fingerprint", "cli_path", "status", "sync_id"}

func TestDevicePrefRepo(t *testing.T) {
	convey.Convey("DevicePrefRepo", t, func() {
		ctx, mock := setup(t)
		repo := device_pref_repo.NewDevicePref()

		convey.Convey("Given rows When listed Then they come back in account device order", func() {
			mock.ExpectQuery("SELECT \\* FROM `device_prefs` WHERE status = \\? ORDER BY sort_order ASC, id ASC").
				WithArgs(consts.ACTIVE).
				WillReturnRows(sqlmock.NewRows(prefColumns).
					AddRow(2, "sha256:b", `["gpu"]`, 0, consts.ACTIVE, "s2").
					AddRow(1, "sha256:a", `[]`, 1, consts.ACTIVE, "s1"))
			got, err := repo.List(ctx)
			require.NoError(t, err)
			require.Len(t, got, 2)
			assert.EqualValues(t, "sha256:b", got[0].DeviceFingerprint)
			assert.Equal(t, []string{"gpu"}, got[0].Labels())
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("Given a new pref When created Then it is stamped and gets a sync identity", func() {
			p := &device_pref_entity.DevicePref{DeviceFingerprint: "sha256:a"}
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `device_prefs`").WillReturnResult(sqlmock.NewResult(4, 1))
			mock.ExpectCommit()
			require.NoError(t, repo.Create(ctx, p))
			assert.Equal(t, int64(4), p.ID)
			assert.NotEmpty(t, p.SyncID)
			assert.NotZero(t, p.Createtime)
			assert.Equal(t, consts.ACTIVE, p.Status)
		})

		convey.Convey("Given the driver fails When created Then the error is returned", func() {
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `device_prefs`").WillReturnError(errors.New("boom"))
			mock.ExpectRollback()
			assert.EqualError(t, repo.Create(ctx, &device_pref_entity.DevicePref{DeviceFingerprint: "sha256:a"}), "boom")
		})

		convey.Convey("When a position is set Then only sort_order and updatetime change", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `device_prefs` SET `sort_order`=\\?,`updatetime`=\\? WHERE device_fingerprint = \\?").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			require.NoError(t, repo.SetSortOrder(ctx, "sha256:a", 3))
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("When labels are set Then only labels_json and updatetime change", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `device_prefs` SET `labels_json`=\\?,`updatetime`=\\? WHERE device_fingerprint = \\?").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			require.NoError(t, repo.SetLabels(ctx, "sha256:a", `["x"]`))
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestCLIPathRepo(t *testing.T) {
	convey.Convey("CLIPathRepo", t, func() {
		ctx, mock := setup(t)
		repo := device_pref_repo.NewCLIPath()

		convey.Convey("Given a row When found by (runtime, device) Then it is returned", func() {
			mock.ExpectQuery("SELECT \\* FROM `device_cli_paths` WHERE runtime_type = \\? AND device_fingerprint = \\? AND status = \\?").
				WithArgs("codex", "sha256:a", consts.ACTIVE, 1).
				WillReturnRows(sqlmock.NewRows(cliColumns).AddRow(1, "codex", "sha256:a", "/opt/codex", consts.ACTIVE, "s"))
			got, err := repo.Find(ctx, "codex", "sha256:a")
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, "/opt/codex", got.CLIPath)
		})

		convey.Convey("Given no row When found Then nil", func() {
			mock.ExpectQuery("SELECT \\* FROM `device_cli_paths`").WillReturnRows(sqlmock.NewRows(cliColumns))
			got, err := repo.Find(ctx, "codex", "sha256:a")
			require.NoError(t, err)
			assert.Nil(t, got)
		})

		convey.Convey("When created Then it is stamped with a sync identity", func() {
			o := &device_pref_entity.CLIPathOverride{RuntimeType: "codex", DeviceFingerprint: "sha256:a", CLIPath: "/x"}
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `device_cli_paths`").WillReturnResult(sqlmock.NewResult(2, 1))
			mock.ExpectCommit()
			require.NoError(t, repo.Create(ctx, o))
			assert.NotEmpty(t, o.SyncID)
			assert.Equal(t, consts.ACTIVE, o.Status)
		})

		convey.Convey("When the path is updated Then only cli_path and updatetime change", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `device_cli_paths` SET `cli_path`=\\?,`updatetime`=\\? WHERE id = \\?").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			require.NoError(t, repo.UpdatePath(ctx, 2, "/y"))
		})

		convey.Convey("When removed Then the row is soft deleted", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `device_cli_paths` SET `status`=\\?,`updatetime`=\\? WHERE id = \\?").
				WithArgs(consts.DELETE, sqlmock.AnyArg(), int64(2)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			require.NoError(t, repo.Delete(ctx, 2))
		})
	})
}
