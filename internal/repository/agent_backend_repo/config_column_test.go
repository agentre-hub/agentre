package agent_backend_repo_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// configRow 是一行带独占配置的 codex 后端：sandbox / approval 那两格只有 codex 认。
const configRow = `{"sandbox":"workspace-write","approval":"on-request"}`

// TestAgentBackendRepo_Find_HydratesConfigFromTheColumn 读口契约：九个独占设置存在
// config_json 一列里，取出来时必须回到各自的字段上。
//
// 它挡住的坏实现是「加了一条读路径却忘了补齐」——那条路径返回的后端上 Sandbox 是
// 空串，而空串是**合法取值**（走 CLI 默认），于是 codex 子进程会被静默地以默认沙箱
// 起来，没有任何一处报错。
func TestAgentBackendRepo_Find_HydratesConfigFromTheColumn(t *testing.T) {
	ctx, mock, repo := setupAgentBackendRepoTest(t)
	mock.ExpectQuery("SELECT \\* FROM `agent_backends` WHERE id = \\? AND status = \\?").
		WithArgs(int64(1), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "type", "name", "config_json", "status"}).
			AddRow(1, string(agent_backend_entity.TypeCodex), "Codex", configRow, consts.ACTIVE))

	got, err := repo.Find(ctx, 1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "workspace-write", got.Sandbox)
	assert.Equal(t, "on-request", got.Approval)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAgentBackendRepo_List_HydratesEveryRow 列表口同上，且必须**每一行**都补齐：
// 只补第一行的实现在单行用例里是绿的。
func TestAgentBackendRepo_List_HydratesEveryRow(t *testing.T) {
	ctx, mock, repo := setupAgentBackendRepoTest(t)
	mock.ExpectQuery("SELECT \\* FROM `agent_backends` WHERE status = \\? ORDER BY id ASC").
		WithArgs(consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id", "type", "name", "config_json", "status"}).
			AddRow(1, string(agent_backend_entity.TypeCodex), "A", configRow, consts.ACTIVE).
			AddRow(2, string(agent_backend_entity.TypeCodex), "B", configRow, consts.ACTIVE))

	rows, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "workspace-write", rows[0].Sandbox)
	assert.Equal(t, "workspace-write", rows[1].Sandbox, "第二行也要补齐")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAgentBackendRepo_Create_SendsConfigAsOneColumn 写口契约：独占设置在落库前被
// 收进 config_json，退役的那九列一个都不再出现在 INSERT 里。
func TestAgentBackendRepo_Create_SendsConfigAsOneColumn(t *testing.T) {
	ctx, mock, repo := setupAgentBackendRepoTest(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `agent_backends` .*`config_json`").
		WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()

	b := &agent_backend_entity.AgentBackend{
		Type: string(agent_backend_entity.TypeCodex), Name: "Codex",
		Sandbox: "workspace-write", Approval: "on-request", Status: consts.ACTIVE,
	}
	require.NoError(t, repo.Create(ctx, b))
	assert.JSONEq(t, configRow, b.ConfigJSON)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAgentBackendRepo_Update_SendsConfigAsOneColumn 改口同上：轮中改过的沙箱要
// 真的落进那一列，而不是停在内存字段上。
func TestAgentBackendRepo_Update_SendsConfigAsOneColumn(t *testing.T) {
	ctx, mock, repo := setupAgentBackendRepoTest(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agent_backends` SET .*`config_json`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	b := &agent_backend_entity.AgentBackend{
		ID: 7, Type: string(agent_backend_entity.TypeCodex), Name: "Codex",
		Sandbox: "workspace-write", Approval: "on-request", Status: consts.ACTIVE,
	}
	require.NoError(t, repo.Update(ctx, b))
	assert.JSONEq(t, configRow, b.ConfigJSON)
	assert.NoError(t, mock.ExpectationsWereMet())
}
