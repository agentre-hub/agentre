package agent_repo_test

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_repo"
)

func setupExecTargetRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, agent_repo.AgentExecTargetRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, agent_repo.NewAgentExecTarget()
}

// execTargetRows 造一批执行目标行(sqlmock 的返回行)。
func execTargetRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "agent_id", "agent_backend_id", "sort_order"})
}

// execTargetSyncRows 带上 sync_id 那一列:按差异更新的写口要证明活下来的档保持
// 同一个同步标识(R1)。
func execTargetSyncRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"id", "agent_id", "agent_backend_id", "sort_order", "sync_id"})
}

// expectExecTargetHydration 预期一次「补齐 AgentBackendID」的查询(返回空列表)。
// 每个返回 Agent 的仓储方法都会跟一发。
func expectExecTargetHydration(mock sqlmock.Sqlmock, agentIDs ...int64) {
	args := make([]driver.Value, 0, len(agentIDs))
	for _, id := range agentIDs {
		args = append(args, id)
	}
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id IN").
		WithArgs(args...).
		WillReturnRows(execTargetRows())
}

// TestExecTargetListByAgent 读口:按 sort_order 升序给出一个 Agent 的有序执行目标列表。
func TestExecTargetListByAgent(t *testing.T) {
	ctx, mock, repo := setupExecTargetRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id IN \\(\\?\\) ORDER BY agent_id ASC, sort_order ASC, id ASC").
		WithArgs(int64(42)).
		WillReturnRows(execTargetRows().
			AddRow(int64(1), int64(42), int64(7), 0).
			AddRow(int64(2), int64(42), int64(9), 1))

	rows, err := repo.ListByAgent(ctx, 42)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, int64(7), rows[0].AgentBackendID)
	assert.Equal(t, int64(9), rows[1].AgentBackendID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestExecTargetListByAgents 批量读口:一次查询给出多个 Agent 的列表,按 Agent 分组。
func TestExecTargetListByAgents(t *testing.T) {
	ctx, mock, repo := setupExecTargetRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id IN \\(\\?,\\?\\) ORDER BY agent_id ASC, sort_order ASC, id ASC").
		WithArgs(int64(41), int64(42)).
		WillReturnRows(execTargetRows().
			AddRow(int64(1), int64(41), int64(7), 0).
			AddRow(int64(2), int64(42), int64(9), 0))

	byAgent, err := repo.ListByAgents(ctx, []int64{41, 42})
	require.NoError(t, err)
	require.Len(t, byAgent[41], 1)
	require.Len(t, byAgent[42], 1)
	assert.Equal(t, int64(7), byAgent[41][0].AgentBackendID)
	assert.Equal(t, int64(9), byAgent[42][0].AgentBackendID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestExecTargetListByAgents_Empty(t *testing.T) {
	ctx, _, repo := setupExecTargetRepo(t)
	byAgent, err := repo.ListByAgents(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, byAgent)
}

// TestExecTargetReplace 写口:列表按差异更新,sort_order 按下标落,每一档自己的
// skills_json 跟着它的行一起写(R15e:技能授权挂在执行目标行上)。这里 Agent 还没有
// 任何目标行,两档都是新建。
func TestExecTargetReplace(t *testing.T) {
	ctx, mock, repo := setupExecTargetRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id = \\? ORDER BY sort_order ASC, id ASC").
		WithArgs(int64(42)).
		WillReturnRows(execTargetRows())
	// 参数顺序跟随 AgentExecTarget 的字段声明顺序:agent_id/agent_backend_id/
	// sort_order/skills_json 之后是内嵌的 SyncMeta 六列——sync_id 在
	// insertExecTargets 里就地生成(R1/R12a),其余五项是未触碰的零值。
	mock.ExpectExec("INSERT INTO `agent_exec_targets`").
		WithArgs(
			int64(42), int64(7), 0, `[{"id":"superpowers@x","enabled":true}]`,
			sqlmock.AnyArg(), int64(0), int64(0), int64(0), "", int64(0),
			int64(42), int64(9), 1, `[]`,
			sqlmock.AnyArg(), int64(0), int64(0), int64(0), "", int64(0),
		).
		WillReturnResult(sqlmock.NewResult(2, 2))
	mock.ExpectCommit()

	require.NoError(t, repo.Replace(ctx, 42, []*agent_entity.AgentExecTarget{
		{AgentBackendID: 7, SkillsJSON: `[{"id":"superpowers@x","enabled":true}]`},
		{AgentBackendID: 9, SkillsJSON: `[]`},
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestExecTargetReplace_Empty 空列表 = 清空该 Agent 的执行目标,且不发 INSERT。
func TestExecTargetReplace_Empty(t *testing.T) {
	ctx, mock, repo := setupExecTargetRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id = \\? ORDER BY sort_order ASC, id ASC").
		WithArgs(int64(42)).
		WillReturnRows(execTargetSyncRows().AddRow(int64(1), int64(42), int64(7), 0, "tgt-1"))
	mock.ExpectExec("DELETE FROM `agent_exec_targets` WHERE id = \\?").
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Replace(ctx, 42, nil))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestExecTargetReplace_KeepsSyncIdentityOfSurvivingTarget R1:活下来的档保持同一行
// ——同一个自增主键、同一个同步标识。删后重插会让每次改 Agent 都给幸存的档重新铸
// 一个标识,在对端表现成「删了又建」。这里把两档换个顺序,断言全程没有 DELETE、
// 没有 INSERT,只有按 id 的原地更新。
func TestExecTargetReplace_KeepsSyncIdentityOfSurvivingTarget(t *testing.T) {
	ctx, mock, repo := setupExecTargetRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id = \\? ORDER BY sort_order ASC, id ASC").
		WithArgs(int64(42)).
		WillReturnRows(execTargetSyncRows().
			AddRow(int64(1), int64(42), int64(7), 0, "tgt-a").
			AddRow(int64(2), int64(42), int64(9), 1, "tgt-b"))
	// (agent_id, sort_order) 上有唯一索引:先把留下的行挪到不可能相撞的临时序号。
	mock.ExpectExec("UPDATE `agent_exec_targets` SET `sort_order`=\\? WHERE id = \\?").
		WithArgs(-1, int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `agent_exec_targets` SET `sort_order`=\\? WHERE id = \\?").
		WithArgs(-2, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `agent_exec_targets` SET").
		WithArgs(int64(9), `[]`, 0, int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `agent_exec_targets` SET").
		WithArgs(int64(7), `[]`, 1, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Replace(ctx, 42, []*agent_entity.AgentExecTarget{
		{AgentBackendID: 9, SkillsJSON: `[]`},
		{AgentBackendID: 7, SkillsJSON: `[]`},
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestExecTargetReplace_DroppingATargetDropsItsSkills R15e:删档连带删授权。没有被
// 带进新列表的档整行消失(连它的 skills_json),不需要一个单独的"清理授权"步骤;
// 留下来的那一档原地更新,标识不变。
func TestExecTargetReplace_DroppingATargetDropsItsSkills(t *testing.T) {
	ctx, mock, repo := setupExecTargetRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id = \\? ORDER BY sort_order ASC, id ASC").
		WithArgs(int64(42)).
		WillReturnRows(execTargetSyncRows().
			AddRow(int64(1), int64(42), int64(7), 0, "tgt-dropped").
			AddRow(int64(2), int64(42), int64(9), 1, "tgt-kept"))
	mock.ExpectExec("DELETE FROM `agent_exec_targets` WHERE id = \\?").
		WithArgs(int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `agent_exec_targets` SET `sort_order`=\\? WHERE id = \\?").
		WithArgs(-1, int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `agent_exec_targets` SET").
		WithArgs(int64(9), `[{"id":"kept@x","enabled":true}]`, 0, int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	// 档 7(原来带着 dropped@x 的授权)从列表里被拿掉,只剩档 9。
	require.NoError(t, repo.Replace(ctx, 42, []*agent_entity.AgentExecTarget{
		{AgentBackendID: 9, SkillsJSON: `[{"id":"kept@x","enabled":true}]`},
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestFindDerivesBackendFromExecTargets 派生读:Agent 的 AgentBackendID 一律取自
// 执行目标行(sort_order 最小的那一行),不再读 agents.agent_backend_id 历史列。
func TestFindDerivesBackendFromExecTargets(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agents` WHERE id = \\? AND status = \\?").
		WithArgs(int64(42), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "agent_backend_id"}).
			AddRow(int64(42), "Eva", int64(999)))
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id IN \\(\\?\\)").
		WithArgs(int64(42)).
		WillReturnRows(execTargetRows().
			AddRow(int64(1), int64(42), int64(7), 0).
			AddRow(int64(2), int64(42), int64(9), 1))

	got, err := repo.Find(ctx, 42)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(7), got.AgentBackendID, "历史列的 999 不许泄漏出来")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestFindWithoutExecTargets 没有执行目标行的 Agent 派生出 0(= 未配置后端)。
func TestFindWithoutExecTargets(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agents` WHERE id = \\? AND status = \\?").
		WithArgs(int64(42), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "agent_backend_id"}).
			AddRow(int64(42), "Eva", int64(999)))
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id IN \\(\\?\\)").
		WithArgs(int64(42)).
		WillReturnRows(execTargetRows())

	got, err := repo.Find(ctx, 42)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(0), got.AgentBackendID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestFindFailsWhenExecTargetsUnreadable 错误路径:派生查询失败时必须报错并且不交出
// Agent —— 交出去的话调用方会拿着 AgentBackendID = 0 的 Agent 当「未配置后端」处理。
func TestFindFailsWhenExecTargetsUnreadable(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agents` WHERE id = \\? AND status = \\?").
		WithArgs(int64(42), consts.ACTIVE, 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "agent_backend_id"}).AddRow(int64(42), int64(999)))
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id IN \\(\\?\\)").
		WithArgs(int64(42)).
		WillReturnError(assert.AnError)

	got, err := repo.Find(ctx, 42)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListDerivesBackendFromExecTargets 列表读:一次补齐整批 Agent 的派生值。
func TestListDerivesBackendFromExecTargets(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agents` WHERE status = \\?").
		WithArgs(consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id", "agent_backend_id"}).
			AddRow(int64(41), int64(999)).
			AddRow(int64(42), int64(999)))
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id IN \\(\\?,\\?\\)").
		WithArgs(int64(41), int64(42)).
		WillReturnRows(execTargetRows().AddRow(int64(1), int64(41), int64(7), 0))

	rows, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, int64(7), rows[0].AgentBackendID)
	assert.Equal(t, int64(0), rows[1].AgentBackendID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateWritesSingleExecTarget 写口:Agent 的 AgentBackendID 落成单元素目标行,
// Agent 行上携带的 SkillsJSON(仓储写入载荷,见 agent_entity.Agent.SkillsJSON 字段
// 注释)跟着这一行一起落 —— 存放位置下沉到执行目标行(R15e)。
func TestCreateWritesSingleExecTarget(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `agents`").WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectExec("INSERT INTO `agent_exec_targets`").
		WithArgs(
			int64(42), int64(7), 0, `[{"id":"superpowers@x","enabled":true}]`,
			sqlmock.AnyArg(), int64(0), int64(0), int64(0), "", int64(0),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, &agent_entity.Agent{
		Name: "Eva", DepartmentID: 1, AgentBackendID: 7, Status: consts.ACTIVE,
		SkillsJSON: `[{"id":"superpowers@x","enabled":true}]`,
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestCreateWithoutBackendWritesNoExecTarget 未配置后端的 Agent 落成空列表。
func TestCreateWithoutBackendWritesNoExecTarget(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `agents`").WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectCommit()

	err := repo.Create(ctx, &agent_entity.Agent{Name: "Eva", DepartmentID: 1, Status: consts.ACTIVE})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpdateWithTargetsReplacesGivenOrderedList 写口:UpdateWithTargets 让调用方
// （agent_svc,R15 多档编辑)直接给出完整的有序执行目标列表——两档都要落地,
// 顺序即 sort_order。
func TestUpdateWithTargetsReplacesGivenOrderedList(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agents` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id = \\? ORDER BY sort_order ASC, id ASC").
		WithArgs(int64(42)).
		WillReturnRows(execTargetRows())
	mock.ExpectExec("INSERT INTO `agent_exec_targets`").
		WithArgs(
			int64(42), int64(9), 0, `[{"id":"opsctl@x","enabled":true}]`,
			sqlmock.AnyArg(), int64(0), int64(0), int64(0), "", int64(0),
			int64(42), int64(10), 1, `[]`,
			sqlmock.AnyArg(), int64(0), int64(0), int64(0), "", int64(0),
		).
		WillReturnResult(sqlmock.NewResult(1, 2))
	mock.ExpectCommit()

	err := repo.UpdateWithTargets(ctx, &agent_entity.Agent{
		ID: 42, Name: "Eva", DepartmentID: 1, AgentBackendID: 9, Status: consts.ACTIVE,
		SkillsJSON: `[{"id":"opsctl@x","enabled":true}]`,
	}, []*agent_entity.AgentExecTarget{
		{AgentBackendID: 9, SkillsJSON: `[{"id":"opsctl@x","enabled":true}]`},
		{AgentBackendID: 10, SkillsJSON: `[]`},
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpsertFromSync_InsertsNewRowWithIncomingSyncID 下行来的执行目标沿用它自己的
// 同步标识落地(R1);Agent 下还没有别的行时直接插入。
func TestUpsertFromSync_InsertsNewRowWithIncomingSyncID(t *testing.T) {
	ctx, mock, repo := setupExecTargetRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id = \\?").
		WithArgs(int64(42)).
		WillReturnRows(execTargetSyncRows())
	mock.ExpectExec("INSERT INTO `agent_exec_targets`").
		WithArgs(int64(42), int64(9), 0, `[]`, "tgt-remote", int64(0), int64(0), int64(0), "", int64(0)).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	row := &agent_entity.AgentExecTarget{AgentID: 42, AgentBackendID: 9, SortOrder: 0, SkillsJSON: `[]`}
	row.SyncID = "tgt-remote"
	require.NoError(t, repo.UpsertFromSync(ctx, row))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestUpsertFromSync_MovesTheOccupantOfThatSortOrderAside (agent_id, sort_order) 上
// 有唯一索引:别的行占着这个序号时先挪到末尾,等它自己的下行项到达再归位。
func TestUpsertFromSync_MovesTheOccupantOfThatSortOrderAside(t *testing.T) {
	ctx, mock, repo := setupExecTargetRepo(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_id = \\?").
		WithArgs(int64(42)).
		WillReturnRows(execTargetSyncRows().
			AddRow(int64(1), int64(42), int64(7), 0, "tgt-local").
			AddRow(int64(2), int64(42), int64(9), 1, "tgt-remote"))
	mock.ExpectExec("UPDATE `agent_exec_targets` SET `sort_order`=\\? WHERE id = \\?").
		WithArgs(2, int64(1)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `agent_exec_targets` SET").
		WithArgs(int64(9), int64(42), `[]`, 0, int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	row := &agent_entity.AgentExecTarget{AgentID: 42, AgentBackendID: 9, SortOrder: 0, SkillsJSON: `[]`}
	row.SyncID = "tgt-remote"
	require.NoError(t, repo.UpsertFromSync(ctx, row))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDeleteBySyncID 墓碑到达时按同步标识删掉那一行(R6)。
func TestDeleteBySyncID(t *testing.T) {
	ctx, mock, repo := setupExecTargetRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `agent_exec_targets` WHERE sync_id = \\?").
		WithArgs("tgt-remote").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.DeleteBySyncID(ctx, "tgt-remote"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestExecTargetListByBackend 删 backend 时要找出引用它的全部执行目标行(R6)。
func TestExecTargetListByBackend(t *testing.T) {
	ctx, mock, repo := setupExecTargetRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_backend_id = \\?").
		WithArgs(int64(9)).
		WillReturnRows(execTargetSyncRows().
			AddRow(int64(2), int64(42), int64(9), 1, "tgt-a").
			AddRow(int64(5), int64(43), int64(9), 0, "tgt-b"))

	rows, err := repo.ListByBackend(ctx, 9)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "tgt-a", rows[0].SyncID)
	assert.NoError(t, mock.ExpectationsWereMet())
}
