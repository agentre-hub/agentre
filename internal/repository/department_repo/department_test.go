package department_repo_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/department_entity"
	"github.com/agentre-hub/agentre/internal/repository/department_repo"
)

func setupRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, department_repo.DepartmentRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, department_repo.NewDepartment()
}

func TestCreate(t *testing.T) {
	ctx, mock, repo := setupRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `departments`").
		WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, &department_entity.Department{Name: "工程部", AccentColor: "agent-2", Status: consts.ACTIVE})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindFound(t *testing.T) {
	ctx, mock, repo := setupRepo(t)

	mock.ExpectQuery("SELECT \\* FROM `departments` WHERE id = \\? AND status = \\? ORDER BY `departments`.`id` LIMIT \\?").
		WithArgs(int64(7), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status"}).
			AddRow(int64(7), "工程部", consts.ACTIVE))

	got, err := repo.Find(ctx, 7)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "工程部", got.Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindNotFound(t *testing.T) {
	ctx, mock, repo := setupRepo(t)

	mock.ExpectQuery("SELECT \\* FROM `departments` WHERE id = \\? AND status = \\? ORDER BY `departments`.`id` LIMIT \\?").
		WithArgs(int64(99), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	got, err := repo.Find(ctx, 99)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindByName(t *testing.T) {
	ctx, mock, repo := setupRepo(t)

	mock.ExpectQuery("SELECT \\* FROM `departments` WHERE name = \\? AND parent_id = \\? AND status = \\? ORDER BY `departments`.`id` LIMIT \\?").
		WithArgs("工程部", int64(0), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "parent_id"}).
			AddRow(int64(7), "工程部", int64(0)))

	got, err := repo.FindByName(ctx, "工程部", 0)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(7), got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestList(t *testing.T) {
	ctx, mock, repo := setupRepo(t)

	mock.ExpectQuery("SELECT \\* FROM `departments` WHERE status = \\? ORDER BY parent_id ASC, sort_order ASC, id ASC").
		WithArgs(consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
			AddRow(int64(1), "工程部").
			AddRow(int64(2), "产品部"))

	rows, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListByParent(t *testing.T) {
	ctx, mock, repo := setupRepo(t)

	mock.ExpectQuery("SELECT \\* FROM `departments` WHERE parent_id = \\? AND status = \\? ORDER BY sort_order ASC, id ASC").
		WithArgs(int64(7), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(8)))

	rows, err := repo.ListByParent(ctx, 7)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNextSortOrder(t *testing.T) {
	ctx, mock, repo := setupRepo(t)

	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(sort_order\\), 0\\) FROM `departments`").
		WithArgs(int64(0), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(3))

	n, err := repo.NextSortOrder(ctx, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDelete(t *testing.T) {
	ctx, mock, repo := setupRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `departments` SET `status`").
		WithArgs(consts.DELETE, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(ctx, 7))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReparentChildren(t *testing.T) {
	ctx, mock, repo := setupRepo(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `departments` SET `parent_id`").
		WithArgs(int64(0), int64(7), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.ReparentChildren(ctx, 7, 0))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDepartmentReorderSiblings(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE departments SET sort_order = \\?, updatetime = \\? WHERE id = \\? AND parent_id = \\? AND status = \\?").
		WithArgs(1, sqlmock.AnyArg(), int64(5), int64(0), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE departments SET sort_order = \\?, updatetime = \\? WHERE id = \\? AND parent_id = \\? AND status = \\?").
		WithArgs(2, sqlmock.AnyArg(), int64(4), int64(0), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.ReorderSiblings(ctx, 0, []int64{5, 4})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
