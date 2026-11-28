package department_repo_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
)

func TestUpdateDefaultEngineProfile_WritesOnlyThatColumn(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `departments` SET `default_engine_profile_id`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\?").
		WithArgs(int64(5), sqlmock.AnyArg(), int64(7), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateDefaultEngineProfile(ctx, 7, 5))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Given 一份配置被删 When 清部门引用 Then 以它为成员默认引擎的部门改回跟随上级。
func TestClearDefaultEngineProfile_ResetsDepartmentsToFollow(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `departments` SET `default_engine_profile_id`=\\?,`updatetime`=\\? WHERE default_engine_profile_id = \\?").
		WithArgs(int64(0), sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.ClearDefaultEngineProfile(ctx, 5))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListByDefaultEngineProfile(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `departments` WHERE default_engine_profile_id = \\? AND status = \\? ORDER BY id ASC").
		WithArgs(int64(5), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(7, "工程部"))

	rows, err := repo.ListByDefaultEngineProfile(ctx, 5)
	require.NoError(t, err)
	if assert.Len(t, rows, 1) {
		assert.Equal(t, "工程部", rows[0].Name)
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Given 部门详情或同步落地整行回写一份读得早的部门 When 落库 Then 成员默认引擎不在 SET 里
// ——它只由 UpdateDefaultEngineProfile 写。
func TestUpdate_LeavesDefaultEngineProfileAlone(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `departments` SET .*`updatetime`=\\?,`sync_id`=\\?").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Update(ctx, &department_entity.Department{ID: 7, Name: "工程部", Status: consts.ACTIVE}))
	assert.NoError(t, mock.ExpectationsWereMet())
}
