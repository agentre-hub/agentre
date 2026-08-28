package agent_repo_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
)

func setupRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, agent_repo.AgentRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, agent_repo.NewAgent()
}

func TestCreate(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `agents`").WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectExec("INSERT INTO `agent_exec_targets`").WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, &agent_entity.Agent{Name: "Eva", DepartmentID: 1, AgentBackendID: 1, Status: consts.ACTIVE})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindByName(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agents` WHERE name = \\? AND status = \\? ORDER BY `agents`.`id` LIMIT \\?").
		WithArgs("Eva", consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(int64(42), "Eva"))
	expectExecTargetHydration(mock, int64(42))

	got, err := repo.FindByName(ctx, "Eva")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(42), got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestFindSystem(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agents` WHERE system_badge = \\? AND status = \\? ORDER BY `agents`.`id` LIMIT \\?").
		WithArgs("DEFAULT", consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "system_badge"}).
			AddRow(int64(1), "CEO 助手", "DEFAULT"))
	expectExecTargetHydration(mock, int64(1))

	got, err := repo.FindSystem(ctx)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "CEO 助手", got.Name)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListByDepartment(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agents` WHERE department_id = \\? AND parent_agent_id = \\? AND status = \\? ORDER BY sort_order ASC, id ASC").
		WithArgs(int64(7), int64(0), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)).AddRow(int64(102)))
	expectExecTargetHydration(mock, int64(101), int64(102))

	rows, err := repo.ListByDepartment(ctx, 7)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListByParent(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agents` WHERE parent_agent_id = \\? AND status = \\? ORDER BY sort_order ASC, id ASC").
		WithArgs(int64(7), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)).AddRow(int64(102)))
	expectExecTargetHydration(mock, int64(101), int64(102))

	rows, err := repo.ListByParent(ctx, 7)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCountByBackends(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT agent_exec_targets.agent_backend_id AS agent_backend_id, COUNT\\(DISTINCT agent_exec_targets.agent_id\\) AS cnt FROM `agent_exec_targets` JOIN agents ON agents.id = agent_exec_targets.agent_id WHERE agent_exec_targets.agent_backend_id IN \\(\\?,\\?\\) AND agents.status = \\? GROUP BY `agent_exec_targets`.`agent_backend_id`").
		WithArgs(int64(3), int64(7), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"agent_backend_id", "cnt"}).
			AddRow(int64(3), int64(2)).
			AddRow(int64(7), int64(5)))

	counts, err := repo.CountByBackends(ctx, []int64{3, 7})
	require.NoError(t, err)
	assert.Equal(t, map[int64]int64{3: 2, 7: 5}, counts)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCountByBackends_Empty(t *testing.T) {
	ctx, _, repo := setupRepo(t)
	counts, err := repo.CountByBackends(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, counts)
}

func TestUpdatePlacement(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agents` SET `department_id`=\\?,`parent_agent_id`=\\?,`sort_order`=\\?").
		WithArgs(int64(0), int64(8), 3, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdatePlacement(ctx, 42, 0, 8, 3))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAvatar(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agents` SET `avatar_data_url`=\\?,`updatetime`=\\?").
		WithArgs("data:image/png;base64,AAA", int64(1700000000), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateAvatar(ctx, 42, "data:image/png;base64,AAA", 1700000000))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReparentChildren(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agents` SET `department_id`=\\?,`parent_agent_id`=\\?").
		WithArgs(int64(3), int64(0), int64(42), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.ReparentChildren(ctx, 42, 3, 0))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestClearLeadOfDepartment(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `departments` SET `lead_agent_id`").
		WithArgs(int64(0), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.ClearLeadOfDepartment(ctx, 42))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNextSortOrder(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(sort_order\\), 0\\) FROM `agents`").
		WithArgs(int64(7), int64(0), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(2))

	n, err := repo.NextSortOrder(ctx, 7)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestNextSortOrderByParent(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(sort_order\\), 0\\) FROM `agents`").
		WithArgs(int64(7), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(2))

	n, err := repo.NextSortOrderByParent(ctx, 7)
	require.NoError(t, err)
	assert.Equal(t, 3, n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAgentSetPinned(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agents` SET `pinned`=\\?").
		WithArgs(true, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.SetPinned(ctx, 42, true))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAgentReorderSiblings(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE agents SET sort_order = \\?, updatetime = \\? WHERE id = \\? AND department_id = \\? AND parent_agent_id = \\? AND status = \\?").
		WithArgs(1, sqlmock.AnyArg(), int64(3), int64(2), int64(0), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE agents SET sort_order = \\?, updatetime = \\? WHERE id = \\? AND department_id = \\? AND parent_agent_id = \\? AND status = \\?").
		WithArgs(2, sqlmock.AnyArg(), int64(1), int64(2), int64(0), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.ReorderSiblings(ctx, 2, 0, []int64{3, 1})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
