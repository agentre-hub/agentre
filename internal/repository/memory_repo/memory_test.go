package memory_repo_test

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/memory_entity"
	"github.com/agentre-hub/agentre/internal/repository/memory_repo"
)

func setupMemoryRepo(t *testing.T) (context.Context, sqlmock.Sqlmock, memory_repo.MemoryRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, memory_repo.NewMemory()
}

var memoryColumns = []string{"id", "owner_kind", "owner_id", "summary", "detail", "pinned", "state", "changed_at"}

func TestMemoryCreate_StampsTimesAndSyncID(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `memories`").WillReturnResult(sqlmock.NewResult(7, 1))
	mock.ExpectCommit()

	m := memory_entity.NewMemory(memory_entity.GlobalOwner(), "用 pnpm", "", memory_entity.UserActor(), 100)
	require.NoError(t, repo.Create(ctx, m))
	assert.Equal(t, int64(7), m.ID)
	assert.NotZero(t, m.Createtime)
	assert.NotZero(t, m.Updatetime)
	assert.NotEmpty(t, m.SyncID, "同步标识在行创建时就地生成")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryUpdate_WritesEveryMutableColumnByID(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `memories` SET `change_action`=\\?,`change_actor_kind`=\\?,`change_agent_id`=\\?,`change_session_id`=\\?,`changed_at`=\\?,`detail`=\\?,`owner_id`=\\?,`owner_kind`=\\?,`pinned`=\\?,`prev_detail`=\\?,`prev_summary`=\\?,`state`=\\?,`summary`=\\?,`sync_id`=\\?,`updatetime`=\\? WHERE id = \\?").
		WithArgs("edited", "user", int64(0), int64(0), int64(200), "d", int64(5), "agent", true, "", "old",
			"normal", "new", "S1", sqlmock.AnyArg(), int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	m := &memory_entity.Memory{
		ID: 9, OwnerKind: memory_entity.OwnerAgent, OwnerID: 5, Summary: "new", Detail: "d",
		PrevSummary: "old", Pinned: true, State: memory_entity.StateNormal,
		ChangeAction: memory_entity.ActionEdited, ChangeActorKind: memory_entity.ActorUser, ChangedAt: 200,
	}
	m.SyncID = "S1"
	require.NoError(t, repo.Update(ctx, m))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryFind(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `memories` WHERE id = \\?").
		WithArgs(int64(9), 1).
		WillReturnRows(sqlmock.NewRows(memoryColumns).AddRow(9, "global", 0, "s", "", false, "normal", 1))
	got, err := repo.Find(ctx, 9)
	require.NoError(t, err)
	assert.Equal(t, int64(9), got.ID)

	mock.ExpectQuery("SELECT \\* FROM `memories` WHERE id = \\?").
		WithArgs(int64(10), 1).
		WillReturnRows(sqlmock.NewRows(memoryColumns))
	got, err = repo.Find(ctx, 10)
	require.NoError(t, err)
	assert.Nil(t, got, "不存在时返回 nil, nil")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryFindNormalBySummary_MatchesLayerAndState(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `memories` WHERE owner_kind = \\? AND owner_id = \\? AND summary = \\? AND state = \\?").
		WithArgs("project", int64(3), "用 pnpm", "normal", 1).
		WillReturnRows(sqlmock.NewRows(memoryColumns).AddRow(4, "project", 3, "用 pnpm", "", false, "normal", 1))
	got, err := repo.FindNormalBySummary(ctx, memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 3}, "用 pnpm")
	require.NoError(t, err)
	assert.Equal(t, int64(4), got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryListByOwner_AllStatesInBudgetOrder(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `memories` WHERE owner_kind = \\? AND owner_id = \\? ORDER BY pinned DESC, changed_at DESC, id DESC").
		WithArgs("department", int64(2)).
		WillReturnRows(sqlmock.NewRows(memoryColumns).
			AddRow(1, "department", 2, "a", "", true, "normal", 1).
			AddRow(2, "department", 2, "b", "", false, "archived", 2))
	got, err := repo.ListByOwner(ctx, memory_entity.Owner{Kind: memory_entity.OwnerDepartment, ID: 2})
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryListNormalByOwners(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `memories` WHERE state = \\? AND \\(\\(owner_kind = \\? AND owner_id = \\?\\) OR \\(owner_kind = \\? AND owner_id = \\?\\)\\) ORDER BY pinned DESC, changed_at DESC, id DESC").
		WithArgs("normal", "global", int64(0), "agent", int64(5)).
		WillReturnRows(sqlmock.NewRows(memoryColumns).AddRow(1, "global", 0, "a", "", false, "normal", 1))
	got, err := repo.ListNormalByOwners(ctx, []memory_entity.Owner{
		memory_entity.GlobalOwner(), {Kind: memory_entity.OwnerAgent, ID: 5},
	})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.NoError(t, mock.ExpectationsWereMet())

	got, err = repo.ListNormalByOwners(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, got, "没有归属时不查库")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryCountNormalByOwner(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectQuery("SELECT owner_kind, owner_id, COUNT\\(\\*\\) AS count FROM `memories` WHERE state = \\? GROUP BY owner_kind, owner_id").
		WithArgs("normal").
		WillReturnRows(sqlmock.NewRows([]string{"owner_kind", "owner_id", "count"}).
			AddRow("global", 0, 3).
			AddRow("agent", 5, 2))
	got, err := repo.CountNormalByOwner(ctx)
	require.NoError(t, err)
	assert.Equal(t, []*memory_repo.OwnerCount{
		{Owner: memory_entity.GlobalOwner(), Count: 3},
		{Owner: memory_entity.Owner{Kind: memory_entity.OwnerAgent, ID: 5}, Count: 2},
	}, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryListChangedSince(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `memories` WHERE changed_at >= \\? ORDER BY changed_at DESC, id DESC").
		WithArgs(int64(1000)).
		WillReturnRows(sqlmock.NewRows(memoryColumns).AddRow(1, "global", 0, "a", "", false, "archived", 1200))
	got, err := repo.ListChangedSince(ctx, 1000)
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemorySearch_EscapesLikeAndMatchesSummaryOrDetail(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `memories` WHERE \\(summary LIKE \\? ESCAPE '\\\\' OR detail LIKE \\? ESCAPE '\\\\'\\) ORDER BY owner_kind, owner_id, pinned DESC, changed_at DESC, id DESC").
		WithArgs(`%100\%\_x%`, `%100\%\_x%`).
		WillReturnRows(sqlmock.NewRows(memoryColumns).AddRow(1, "global", 0, "100%_x", "", false, "normal", 1))
	got, err := repo.Search(ctx, "100%_x")
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryDelete_IsHardDelete(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `memories` WHERE `memories`.`id` = \\?").
		WithArgs(int64(9)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Delete(ctx, 9))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryListByOwners_IncludesArchived(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectQuery("SELECT \\* FROM `memories` WHERE \\(owner_kind = \\? AND owner_id = \\?\\) OR \\(owner_kind = \\? AND owner_id = \\?\\) ORDER BY pinned DESC, changed_at DESC, id DESC").
		WithArgs("agent", int64(5), "project", int64(2)).
		WillReturnRows(sqlmock.NewRows(memoryColumns).AddRow(1, "agent", 5, "a", "", false, "archived", 1))
	got, err := repo.ListByOwners(ctx, []memory_entity.Owner{
		{Kind: memory_entity.OwnerAgent, ID: 5}, {Kind: memory_entity.OwnerProject, ID: 2},
	})
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.NoError(t, mock.ExpectationsWereMet())

	got, err = repo.ListByOwners(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, got, "没有归属时不查库")
}

func TestMemoryDeleteByOwners_HardDeletesEveryState(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `memories` WHERE \\(owner_kind = \\? AND owner_id = \\?\\) OR \\(owner_kind = \\? AND owner_id = \\?\\)").
		WithArgs("agent", int64(5), "department", int64(3)).
		WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectCommit()
	require.NoError(t, repo.DeleteByOwners(ctx, []memory_entity.Owner{
		{Kind: memory_entity.OwnerAgent, ID: 5}, {Kind: memory_entity.OwnerDepartment, ID: 3},
	}))
	assert.NoError(t, mock.ExpectationsWereMet())

	require.NoError(t, repo.DeleteByOwners(ctx, nil), "没有归属时不删，更不能删光整张表")
}

func TestMemoryReassignOwner_MovesEveryStateAndStampsUpdatetime(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `memories` SET `owner_id`=\\?,`updatetime`=\\? WHERE owner_kind = \\? AND owner_id = \\?").
		WithArgs(int64(8), sqlmock.AnyArg(), "project", int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()
	require.NoError(t, repo.ReassignOwner(ctx,
		memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 2},
		memory_entity.Owner{Kind: memory_entity.OwnerProject, ID: 8}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMemoryCountByOwners_CountsEveryState(t *testing.T) {
	ctx, mock, repo := setupMemoryRepo(t)
	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `memories` WHERE \\(owner_kind = \\? AND owner_id = \\?\\) OR \\(owner_kind = \\? AND owner_id = \\?\\)").
		WithArgs("agent", int64(5), "department", int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(4))
	got, err := repo.CountByOwners(ctx, []memory_entity.Owner{
		{Kind: memory_entity.OwnerAgent, ID: 5}, {Kind: memory_entity.OwnerDepartment, ID: 3},
	})
	require.NoError(t, err)
	assert.Equal(t, int64(4), got)
	assert.NoError(t, mock.ExpectationsWereMet())

	got, err = repo.CountByOwners(ctx, nil)
	require.NoError(t, err)
	assert.Zero(t, got, "没有归属时不查库，更不能数整张表")
}
