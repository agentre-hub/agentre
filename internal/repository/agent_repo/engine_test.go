package agent_repo_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
)

// Given 成员的执行设置改了 When 落库 Then 只写引擎引用、约束、技能授权四列（+ updatetime），
// 不碰成员的其它列——它们有各自的写入口（自动保存、同步落地）。
func TestUpdateEngineSettings_WritesOnlyTheExecutionColumns(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agents` SET `engine_profile_id`=\\?,`exec_constraint_json`=\\?,`skill_grants_json`=\\?,`skill_grants_runtime`=\\?,`updatetime`=\\? WHERE id = \\? AND status = \\?").
		WithArgs(int64(5), `{"mode":"any"}`, `[]`, "codex", int64(1700000000), int64(42), consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateEngineSettings(ctx, &agent_entity.Agent{
		ID: 42, EngineProfileID: 5, ExecConstraintJSON: `{"mode":"any"}`,
		SkillGrantsJSON: `[]`, SkillGrantsRuntime: "codex", Updatetime: 1700000000,
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Given 一份配置被删 When 清它的成员引用 Then 引用它的成员（含软删的）一律改回跟随部门。
func TestClearEngineProfile_ResetsEveryReferenceToFollow(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agents` SET `engine_profile_id`=\\?,`updatetime`=\\? WHERE engine_profile_id = \\?").
		WithArgs(int64(0), sqlmock.AnyArg(), int64(5)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, repo.ClearEngineProfile(ctx, 5))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestListByEngineProfile_ReturnsActiveDirectReferences(t *testing.T) {
	ctx, mock, repo := setupRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `agents` WHERE engine_profile_id = \\? AND status = \\? ORDER BY id ASC").
		WithArgs(int64(5), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "Eva").AddRow(2, "Max"))

	rows, err := repo.ListByEngineProfile(ctx, 5)
	require.NoError(t, err)
	if assert.Len(t, rows, 2) {
		assert.Equal(t, "Eva", rows[0].Name)
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCountByEngineProfiles(t *testing.T) {
	t.Run("Given two profiles When counted Then each gets its active direct-reference count", func(t *testing.T) {
		ctx, mock, repo := setupRepo(t)
		mock.ExpectQuery("SELECT engine_profile_id, COUNT\\(\\*\\) AS cnt FROM `agents` WHERE engine_profile_id IN \\(\\?,\\?\\) AND status = \\? GROUP BY `engine_profile_id`").
			WithArgs(int64(3), int64(7), consts.ACTIVE).
			WillReturnRows(sqlmock.NewRows([]string{"engine_profile_id", "cnt"}).AddRow(int64(3), int64(2)))

		counts, err := repo.CountByEngineProfiles(ctx, []int64{3, 7})
		require.NoError(t, err)
		assert.Equal(t, map[int64]int64{3: 2}, counts)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
	t.Run("Given no profiles When counted Then no query and an empty map", func(t *testing.T) {
		ctx, _, repo := setupRepo(t)
		counts, err := repo.CountByEngineProfiles(ctx, nil)
		require.NoError(t, err)
		assert.Empty(t, counts)
	})
}

// Given 成员页的自动保存或同步落地整行回写一份读得早的成员 When 落库 Then 执行设置四列
// 不在 SET 里——它们只由 UpdateEngineSettings 写，整行回写会把刚换好的引擎配置拍回旧值。
func TestWholeRowWrites_LeaveEngineSettingsAlone(t *testing.T) {
	stale := &agent_entity.Agent{ID: 42, Name: "Eva", DepartmentID: 1, Status: consts.ACTIVE}

	t.Run("UpdateRow", func(t *testing.T) {
		ctx, mock, repo := setupRepo(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `agents` SET .*`memory_enabled`=\\? WHERE `id` = \\?").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()
		require.NoError(t, repo.UpdateRow(ctx, stale))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("UpdateWithTargets", func(t *testing.T) {
		ctx, mock, repo := setupRepo(t)
		mock.ExpectBegin()
		mock.ExpectExec("UPDATE `agents` SET .*`memory_enabled`=\\? WHERE `id` = \\?").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets`").WillReturnRows(sqlmock.NewRows([]string{"id"}))
		mock.ExpectCommit()
		require.NoError(t, repo.UpdateWithTargets(ctx, stale, nil))
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
