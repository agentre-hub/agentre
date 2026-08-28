package project_repo_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/repository/project_repo"
)

func setupProjectAgentRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, project_repo.ProjectAgentRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, project_repo.NewProjectAgent()
}

func TestProjectAgentAdd(t *testing.T) {
	ctx, mock, repo := setupProjectAgentRepo(t)
	mock.ExpectBegin()
	// 参数顺序跟随 ProjectAgent 的字段声明顺序：project_id/agent_id/joined_at 之后
	// 是内嵌的 SyncMeta 六列——sync_id 在 Add() 里就地生成(R1/R12a),其余五项是
	// 未触碰的零值(账号认领/版本号/来源等留给后续任务)。
	mock.ExpectExec("INSERT INTO `project_agents`").
		WithArgs(
			int64(7), int64(42), sqlmock.AnyArg(),
			sqlmock.AnyArg(), int64(0), int64(0), int64(0), "", int64(0),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Add(ctx, 7, 42))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectAgentRemove(t *testing.T) {
	ctx, mock, repo := setupProjectAgentRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `project_agents` WHERE project_id = \\? AND agent_id = \\?").
		WithArgs(int64(7), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Remove(ctx, 7, 42))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectAgentListByProject(t *testing.T) {
	ctx, mock, repo := setupProjectAgentRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `project_agents` WHERE project_id = \\? ORDER BY joined_at ASC, agent_id ASC").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"project_id", "agent_id", "joined_at"}).
			AddRow(int64(7), int64(42), int64(0)).
			AddRow(int64(7), int64(43), int64(0)))

	rows, err := repo.ListByProject(ctx, 7)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectAgentListByProjects(t *testing.T) {
	ctx, mock, repo := setupProjectAgentRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `project_agents` WHERE project_id IN \\(\\?,\\?\\) ORDER BY").
		WithArgs(int64(7), int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"project_id", "agent_id", "joined_at"}).
			AddRow(int64(7), int64(42), int64(0)).
			AddRow(int64(7), int64(43), int64(0)).
			AddRow(int64(8), int64(44), int64(0)))

	out, err := repo.ListByProjects(ctx, []int64{7, 8})
	require.NoError(t, err)
	assert.Len(t, out[7], 2)
	assert.Len(t, out[8], 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestProjectAgentListByProjects_Empty(t *testing.T) {
	ctx, _, repo := setupProjectAgentRepo(t)
	out, err := repo.ListByProjects(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestProjectAgentListByAgent(t *testing.T) {
	ctx, mock, repo := setupProjectAgentRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `project_agents` WHERE agent_id = \\? ORDER BY project_id ASC").
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"project_id", "agent_id"}).
			AddRow(int64(7), int64(42)).
			AddRow(int64(8), int64(42)))

	rows, err := repo.ListByAgent(ctx, 42)
	require.NoError(t, err)
	assert.Len(t, rows, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}
