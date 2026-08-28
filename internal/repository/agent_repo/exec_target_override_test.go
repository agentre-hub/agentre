package agent_repo_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
)

func setupExecTargetOverrideRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, agent_repo.AgentExecTargetOverrideRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, agent_repo.NewAgentExecTargetOverride()
}

func overrideRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "agent_id", "order_json", "updatetime"})
}

// TestExecTargetOverrideGet_GivenNone_ThenNil R14 本地覆盖读口：该 Agent 没有覆盖
// 时返回 (nil, nil)，不是错误 —— 「没覆盖 = 用账号默认」是本端解析的正常状态之一。
func TestExecTargetOverrideGet_GivenNone_ThenNil(t *testing.T) {
	ctx, mock, repo := setupExecTargetOverrideRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_target_overrides` WHERE agent_id = \\? ORDER BY `agent_exec_target_overrides`.`id` LIMIT \\?").
		WithArgs(int64(42), 1).
		WillReturnRows(overrideRows())

	got, err := repo.Get(ctx, 42)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestExecTargetOverrideGet_GivenRow_ThenReturnsIt 该 Agent 有覆盖时原样取回。
func TestExecTargetOverrideGet_GivenRow_ThenReturnsIt(t *testing.T) {
	ctx, mock, repo := setupExecTargetOverrideRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_target_overrides` WHERE agent_id = \\? ORDER BY `agent_exec_target_overrides`.`id` LIMIT \\?").
		WithArgs(int64(42), 1).
		WillReturnRows(overrideRows().
			AddRow(int64(1), int64(42), "[52,51]", int64(1700000000)))

	got, err := repo.Get(ctx, 42)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(42), got.AgentID)
	assert.Equal(t, []int64{52, 51}, got.GetOrder())
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestExecTargetOverrideGet_WhenQueryErrors_ThenError GORM 的 ErrRecordNotFound 被
// 折叠成 nil，其它错误如实上抛。
func TestExecTargetOverrideGet_WhenQueryErrors_ThenError(t *testing.T) {
	ctx, mock, repo := setupExecTargetOverrideRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_target_overrides` WHERE agent_id = \\? ORDER BY `agent_exec_target_overrides`.`id` LIMIT \\?").
		WithArgs(int64(42), 1).
		WillReturnError(gorm.ErrRecordNotFound)

	got, err := repo.Get(ctx, 42)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestExecTargetOverrideSave_UpsertsByAgentID 写口：按 agent_id upsert —— 同一 Agent
// 只有一行，重复保存覆盖旧值（R14「任一端调整只改这一端」的落点）。
func TestExecTargetOverrideSave_UpsertsByAgentID(t *testing.T) {
	ctx, mock, repo := setupExecTargetOverrideRepo(t)
	o := &agent_entity.AgentExecTargetOverride{AgentID: 42, Updatetime: 1700000000}
	o.SetOrder([]int64{52, 51, 53})
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `agent_exec_target_overrides`").
		WithArgs(int64(42), "[52,51,53]", int64(1700000000)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Save(ctx, o))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestExecTargetOverrideDelete_RemovesRow 清覆盖（「恢复为账号默认顺序」的落点）：
// 按 agent_id 删行，行不存在时不是错误。
func TestExecTargetOverrideDelete_RemovesRow(t *testing.T) {
	ctx, mock, repo := setupExecTargetOverrideRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `agent_exec_target_overrides` WHERE agent_id = \\?").
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Delete(ctx, 42))
	assert.NoError(t, mock.ExpectationsWereMet())
}
