package project_location_repo_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/project_location_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_location_repo"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

func setupProjectLocationRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, project_location_repo.ProjectLocationRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, project_location_repo.NewProjectLocation()
}

func TestProjectLocationRepo_Create(t *testing.T) {
	ctx, mock, repo := setupProjectLocationRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `project_locations`").
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, &project_location_entity.ProjectLocation{
		ProjectID: 1,
		DeviceID:  "7",
		Path:      "/home/me/foo",
		Status:    consts.ACTIVE,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectLocationRepo_FindByProjectAndDevice_Found(t *testing.T) {
	ctx, mock, repo := setupProjectLocationRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `project_locations` WHERE project_id = \\? AND device_id = \\? AND status = \\? LIMIT \\?").
		WithArgs(int64(1), "7", consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "device_id", "path", "status"}).
			AddRow(int64(10), int64(1), "7", "/home/me/foo", consts.ACTIVE))

	got, err := repo.FindByProjectAndDevice(ctx, 1, "7")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(10), got.ID)
	assert.Equal(t, int64(1), got.ProjectID)
	assert.Equal(t, "7", got.DeviceID)
	assert.Equal(t, "/home/me/foo", got.Path)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectLocationRepo_FindByProjectAndFingerprint_Found(t *testing.T) {
	ctx, mock, repo := setupProjectLocationRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `project_locations` WHERE project_id = \\? AND device_fingerprint = \\? AND status = \\? LIMIT \\?").
		WithArgs(int64(1), "fp-7", consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "device_id", "device_fingerprint", "path", "status"}).
			AddRow(int64(10), int64(1), "7", "fp-7", "/home/me/foo", consts.ACTIVE))

	got, err := repo.FindByProjectAndFingerprint(ctx, 1, devicefp.Carrier("fp-7"))
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(10), got.ID)
	assert.Equal(t, devicefp.Carrier("fp-7"), got.DeviceFingerprint)
	assert.Equal(t, "7", got.DeviceID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectLocationRepo_UpdateDeviceID_Backfill(t *testing.T) {
	ctx, mock, repo := setupProjectLocationRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `project_locations` SET").
		WithArgs("9", sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.UpdateDeviceID(ctx, 42, "9")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectLocationRepo_UpdateDeviceID_Clear(t *testing.T) {
	ctx, mock, repo := setupProjectLocationRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `project_locations` SET").
		WithArgs("", sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.UpdateDeviceID(ctx, 42, "")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectLocationRepo_ListByProject(t *testing.T) {
	ctx, mock, repo := setupProjectLocationRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `project_locations` WHERE project_id = \\? AND status = \\?").
		WithArgs(int64(1), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "device_id", "path", "status"}).
			AddRow(int64(10), int64(1), "7", "/home/me/foo", consts.ACTIVE).
			AddRow(int64(11), int64(1), "8", "/home/me/bar", consts.ACTIVE))

	rows, err := repo.ListByProject(ctx, 1)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectLocationRepo_UpdatePath(t *testing.T) {
	ctx, mock, repo := setupProjectLocationRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `project_locations` SET").
		WithArgs("/new/path", sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.UpdatePath(ctx, 42, "/new/path")
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectLocationRepo_Delete(t *testing.T) {
	ctx, mock, repo := setupProjectLocationRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `project_locations` WHERE id = \\?").
		WithArgs(int64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "device_fingerprint"}).AddRow(42, 7, "sha256:box"))
	mock.ExpectExec("UPDATE `project_locations` SET").
		WithArgs(consts.DELETE, sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// 去掉那台设备的路径时，把它作为项目默认设备的设置一并清掉（项目「默认设备只能是有路径的设备」）。
	mock.ExpectExec("UPDATE `projects` SET `default_device_fingerprint`=\\?,`updatetime`=\\? WHERE id = \\? AND default_device_fingerprint = \\?").
		WithArgs("", sqlmock.AnyArg(), int64(7), "sha256:box").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.Delete(ctx, 42)
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Given 已不存在的路径记录 When Delete Then 不报错也不碰项目（幂等）。
func TestProjectLocationRepo_Delete_GivenMissingRow_IsNoop(t *testing.T) {
	ctx, mock, repo := setupProjectLocationRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `project_locations` WHERE id = \\?").
		WillReturnRows(sqlmock.NewRows([]string{"id", "project_id", "device_fingerprint"}))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(ctx, 99))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestProjectLocationRepo_ReassignProject 见 SessionRepo.ReassignProject 的同名
// 用例：WHERE 里只能有 project_id，软删的路径记录也得跟着改挂（R11a）。
func TestProjectLocationRepo_ReassignProject(t *testing.T) {
	ctx, mock, repo := setupProjectLocationRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `project_locations` SET `project_id`=\\?,`updatetime`=\\? WHERE project_id = \\?$").
		WithArgs(int64(9), sqlmock.AnyArg(), int64(4)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.ReassignProject(ctx, 4, 9))
	assert.NoError(t, mock.ExpectationsWereMet())
}
