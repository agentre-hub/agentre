package project_repo_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/repository/project_repo"
)

func setupProjectRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, project_repo.ProjectRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, project_repo.NewProject()
}

func TestProjectCreate(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `projects`").WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, &project_entity.Project{
		Name:      "Agentre",
		Path:      "/Users/foo/Code/agentre",
		SortOrder: 1,
		Status:    consts.ACTIVE,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestProjectCreate_GivenNoLogin_ProducesNoObservableDifference R12:未登录时
// 本规格引入的一切都不存在——新增的同步元数据列照常本地写入(sync_id 就地生成,
// R1/R12a),但不改变既有的读写路径,也不产生任何额外副作用。这里没有给
// server_state_repo 注册任何实现,若 Create 曾经尝试读取登录态就会在这里 panic;
// 全部既有业务列(parent_id/name/.../updatetime)与 sync_account_id 等五项账号级
// 元数据原样是未触碰的零值,只有 sync_id 被生成。
func TestProjectCreate_GivenNoLogin_ProducesNoObservableDifference(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `projects`").
		WithArgs(
			int64(0), "Agentre", "", "", "", "/Users/foo/Code/agentre", false, 1, consts.ACTIVE,
			sqlmock.AnyArg(), sqlmock.AnyArg(), // createtime/updatetime: 本地写入时间,与登录态无关
			sqlmock.AnyArg(), int64(0), int64(0), int64(0), "", int64(0), // sync_id 生成,其余五项原样零值
		).
		WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectCommit()

	p := &project_entity.Project{
		Name:      "Agentre",
		Path:      "/Users/foo/Code/agentre",
		SortOrder: 1,
		Status:    consts.ACTIVE,
	}
	require.NoError(t, repo.Create(ctx, p))
	assert.NoError(t, mock.ExpectationsWereMet())
	assert.NotEmpty(t, p.SyncID, "R1:行创建时就地生成标识,不因未登录而跳过")
	assert.Equal(t, int64(0), p.SyncAccountID, "未登录时不认领任何账号")
}

func TestProjectFindByName(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `projects` WHERE parent_id = \\? AND name = \\? AND status = \\? ORDER BY `projects`.`id` LIMIT \\?").
		WithArgs(int64(0), "Agentre", consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "parent_id"}).
			AddRow(int64(42), "Agentre", int64(0)))

	got, err := repo.FindByName(ctx, 0, "Agentre")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(42), got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectFindByName_NotFound(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `projects` WHERE parent_id = \\? AND name = \\? AND status = \\?").
		WithArgs(int64(0), "Agentre", consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	got, err := repo.FindByName(ctx, 0, "Agentre")
	require.NoError(t, err)
	assert.Nil(t, got, "未找到时返回 nil,nil")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectList(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `projects` WHERE status = \\? ORDER BY parent_id ASC, sort_order ASC, id ASC").
		WithArgs(consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).
			AddRow(int64(1), "Agentre").
			AddRow(int64(2), "Side"))

	rows, err := repo.List(ctx)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectListByParent(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `projects` WHERE parent_id = \\? AND status = \\? ORDER BY sort_order ASC, id ASC").
		WithArgs(int64(7), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(11)).AddRow(int64(12)))

	rows, err := repo.ListByParent(ctx, 7)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectHasActiveChildren(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `projects` WHERE parent_id = \\? AND status = \\?").
		WithArgs(int64(7), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	has, err := repo.HasActiveChildren(ctx, 7)
	require.NoError(t, err)
	assert.True(t, has)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectDelete(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `projects` SET").
		WithArgs(consts.DELETE, sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(ctx, 42))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectUpdate(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `projects` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Update(ctx, &project_entity.Project{
		ID:     42,
		Name:   "Agentre v2",
		Path:   "/Users/foo/Code/agentre",
		Status: consts.ACTIVE,
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectNextSortOrder(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(sort_order\\), 0\\) FROM `projects` WHERE parent_id = \\? AND status = \\?").
		WithArgs(int64(7), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(3))

	got, err := repo.NextSortOrder(ctx, 7)
	require.NoError(t, err)
	assert.Equal(t, 4, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectReorderSiblings(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE projects SET sort_order = \\?, updatetime = \\? WHERE id = \\? AND parent_id = \\? AND status = \\?").
		WithArgs(1, sqlmock.AnyArg(), int64(3), int64(7), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE projects SET sort_order = \\?, updatetime = \\? WHERE id = \\? AND parent_id = \\? AND status = \\?").
		WithArgs(2, sqlmock.AnyArg(), int64(1), int64(7), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE projects SET sort_order = \\?, updatetime = \\? WHERE id = \\? AND parent_id = \\? AND status = \\?").
		WithArgs(3, sqlmock.AnyArg(), int64(2), int64(7), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.ReorderSiblings(ctx, 7, []int64{3, 1, 2})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestProjectReassignParent R11a：合并时子项目的 parent_id 整批改挂。WHERE 里
// **只能有 parent_id**——软删的子项目 ListByParent 看不见，它的 parent_id 却照样
// 指着已消失的那个项目，合并后不允许留下任何这样的引用。`$` 锚住结尾：谁再往
// WHERE 里加一个 status 判据，这条就会红。
func TestProjectReassignParent(t *testing.T) {
	ctx, mock, repo := setupProjectRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `projects` SET `parent_id`=\\?,`updatetime`=\\? WHERE parent_id = \\?$").
		WithArgs(int64(9), sqlmock.AnyArg(), int64(4)).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.ReassignParent(ctx, 4, 9))
	assert.NoError(t, mock.ExpectationsWereMet())
}
