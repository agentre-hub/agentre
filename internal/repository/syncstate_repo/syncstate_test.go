package syncstate_repo

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

// TestFindLocalID 按同步标识取本机主键——跨机引用落地时靠它翻回本地 ID（R2）。
func TestFindLocalID(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT id FROM `agents` WHERE sync_id = \\?").
		WithArgs("agent-1").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(42)))

	id, err := repo.FindLocalID(ctx, syncwire.KindAgent, "agent-1")
	require.NoError(t, err)
	assert.Equal(t, int64(42), id)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestFindLocalID_GivenUnknownSyncID_ReturnsZero 查不到就是「引用目标还没到」，
// 由同步层按 R2a 暂缓落地，不是错误。
func TestFindLocalID_GivenUnknownSyncID_ReturnsZero(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT id FROM `projects` WHERE sync_id = \\?").
		WithArgs("nope").
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	id, err := repo.FindLocalID(ctx, syncwire.KindProject, "nope")
	require.NoError(t, err)
	assert.Zero(t, id)
}

// TestFindLocalID_GivenUnknownKind_Errors 表名会被拼进 SQL，白名单之外一律报错。
func TestFindLocalID_GivenUnknownKind_Errors(t *testing.T) {
	ctx, _, _ := testutils.Database(t)
	_, err := NewSyncState().FindLocalID(ctx, "chat_sessions; DROP TABLE agents", "x")
	assert.ErrorIs(t, err, ErrUnknownKind)
}

// TestFindVersion 版本守卫读的就是它：本机已有的版本与墓碑标记（R4/R6）。
func TestFindVersion(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT sync_version,sync_deleted_at FROM `departments` WHERE sync_id = \\?").
		WithArgs("dept-1").
		WillReturnRows(sqlmock.NewRows([]string{"sync_version", "sync_deleted_at"}).
			AddRow(int64(9), int64(1700)))

	version, deleted, found, err := repo.FindVersion(ctx, syncwire.KindDepartment, "dept-1")
	require.NoError(t, err)
	assert.Equal(t, int64(9), version)
	assert.True(t, deleted)
	assert.True(t, found)
}

// TestFindRow_ReadsTombstonedRowsToo 落地一条删除时也要读得到那一行：查询不带
// status 过滤。
func TestFindRow_ReadsTombstonedRowsToo(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT \\* FROM `projects` WHERE sync_id = \\?").
		WithArgs("proj-1", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "status"}).
			AddRow(int64(3), "Gone", 0))

	row := &project_entity.Project{}
	found, err := repo.FindRow(ctx, syncwire.KindProject, "proj-1", row)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, int64(3), row.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSaveMeta 只写六列同步元数据，一个业务列都不碰。
func TestSaveMeta(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `project_locations` SET `sync_account_id`=\\?,`sync_deleted_at`=\\?,`sync_origin_fingerprint`=\\?,`sync_updated_at`=\\?,`sync_version`=\\? WHERE sync_id = \\?").
		WithArgs(int64(7), int64(0), "3", int64(1700), int64(12), "loc-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.SaveMeta(ctx, syncwire.KindProjectLocation, "loc-1", syncmeta_entity.SyncMeta{
		SyncID: "loc-1", SyncAccountID: 7, SyncVersion: 12, SyncUpdatedAt: 1700, SyncOriginFingerprint: "3",
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClaimForAccount_GivenUnownedRows R12a：登录前已有的行归入当前账号；没有同步
// 标识的历史行就地补一个。只认领**存活**的行——本机已经软删的行不该被当成一次新建
// 推上账号（R6）。未归属的行版本号本来就是 0，认领不动它。
func TestClaimForAccount_GivenUnownedRows(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT rowid,sync_id,sync_version,sync_account_id FROM `projects` WHERE \\(\\(sync_account_id = 0 OR sync_account_id <> \\?\\) AND sync_deleted_at = 0\\) AND status = \\?").
		WithArgs(int64(7), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"rowid", "sync_id", "sync_version", "sync_account_id"}).
			AddRow(int64(1), "p-known", int64(0), int64(0)).
			AddRow(int64(2), "", int64(0), int64(0)))
	// 整批一个事务：认领了却没能入队的半截状态此后没有任何取数会看见（见
	// TestClaimForAccount_GivenOneUpdateFails_RollsBackTheWholeBatch）。
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `projects` SET `sync_account_id`=\\? WHERE rowid = \\?").
		WithArgs(int64(7), int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `projects` SET `sync_account_id`=\\?,`sync_id`=\\? WHERE rowid = \\?").
		WithArgs(int64(7), sqlmock.AnyArg(), int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	rows, err := repo.ClaimForAccount(ctx, syncwire.KindProject, 7)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "p-known", rows[0].SyncID)
	assert.Zero(t, rows[0].Version, "未归属的行从没拿过版本号")
	assert.NotEmpty(t, rows[1].SyncID, "历史行就地补一个标识")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClaimForAccount_GivenOneUpdateFails_RollsBackTheWholeBatch 认领的逐行 UPDATE
// 必须在**同一个事务**里：认领是「改归属」与「入队上行」两件事的第一半，而
// claimForCurrentAccount 只凭本次的返回值入队。中途失败却让前几行的归属留在库里，
// 那几行此后一次取数都碰不到——ClaimForAccount 下一轮按归属过滤已经收不到它们，
// 而 ListUnversioned 只在 rebase 那条路径上被问——于是它们静默地再也不上行。
//
// 一起回滚，下一轮重跑才真的是幂等的（接口注释正是这么承诺的）。
func TestClaimForAccount_GivenOneUpdateFails_RollsBackTheWholeBatch(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT rowid,sync_id,sync_version,sync_account_id FROM `projects` WHERE \\(\\(sync_account_id = 0 OR sync_account_id <> \\?\\) AND sync_deleted_at = 0\\) AND status = \\?").
		WithArgs(int64(7), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"rowid", "sync_id", "sync_version", "sync_account_id"}).
			AddRow(int64(1), "p-first", int64(0), int64(0)).
			AddRow(int64(2), "p-second", int64(0), int64(0)))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `projects` SET `sync_account_id`=\\? WHERE rowid = \\?").
		WithArgs(int64(7), int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `projects` SET `sync_account_id`=\\? WHERE rowid = \\?").
		WithArgs(int64(7), int64(2)).WillReturnError(errors.New("database is locked"))
	mock.ExpectRollback()

	rows, err := repo.ClaimForAccount(ctx, syncwire.KindProject, 7)
	require.Error(t, err)
	assert.Empty(t, rows, "一行都没交出去：调用方入不了队，归属也不该留在库里")
	assert.NoError(t, mock.ExpectationsWereMet(),
		"前一行的归属必须随整批回滚，否则它被认领了却永远不上行")
}

// TestClaimForAccount_GivenRowOfAnotherAccount_ClaimsItAndZeroesTheVersion
// 规格 2026-09-04 决策 1 与决策 2：属于上一个账号的存活行也要归入当前账号，且
// **版本号必须清零**——它是上一个账号那套序列里的坐标，拿它当基版本上行是撒谎。
func TestClaimForAccount_GivenRowOfAnotherAccount_ClaimsItAndZeroesTheVersion(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT rowid,sync_id,sync_version,sync_account_id FROM `projects` WHERE \\(\\(sync_account_id = 0 OR sync_account_id <> \\?\\) AND sync_deleted_at = 0\\) AND status = \\?").
		WithArgs(int64(9), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"rowid", "sync_id", "sync_version", "sync_account_id"}).
			AddRow(int64(5), "p-from-a", int64(4200), int64(7)))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `projects` SET `sync_account_id`=\\?,`sync_version`=\\? WHERE rowid = \\?").
		WithArgs(int64(9), 0, int64(5)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	rows, err := repo.ClaimForAccount(ctx, syncwire.KindProject, 9)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "p-from-a", rows[0].SyncID, "带着自己原有的同步标识过去")
	assert.Zero(t, rows[0].Version, "基版本归零：按 R4a 当新建上行")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClaimForAccount_GivenTableWithoutStatus_SkipsStatusFilter 成员关系与执行目标是
// 硬删，没有 status 列——认领时不能把一个不存在的列拼进 SQL。
func TestClaimForAccount_GivenTableWithoutStatus_SkipsStatusFilter(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT rowid,sync_id,sync_version,sync_account_id FROM `project_agents` WHERE \\(sync_account_id = 0 OR sync_account_id <> \\?\\) AND sync_deleted_at = 0").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rowid", "sync_id", "sync_version", "sync_account_id"}))

	rows, err := repo.ClaimForAccount(ctx, syncwire.KindProjectAgent, 7)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResetVersions 换了一套 server 或换了账号之后，行上盖的还是上一套序列的版本号：
// 既比不了大小，也会把新序列的全量快照整个挡在门外（版本守卫是「本机版本 >= 来的版本
// 就不落」）。清零只碰版本号一列，墓碑标记不动。
//
// **不按账号过滤**（规格 2026-09-04）：换账号时本机的行还盖着**上一个**账号的版本号，
// 按当前账号过滤等于一行都清不到，新账号那份快照于是被守卫挡掉——固定同步标识那一行
// （系统 Agent）因此会被上一个账号那份覆盖。
func TestResetVersions(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agents` SET `sync_version`=\\? WHERE \\(sync_version <> \\? AND sync_deleted_at = 0\\) AND status = \\?").
		WithArgs(0, 0, consts.ACTIVE).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, repo.ResetVersions(ctx, syncwire.KindAgent))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResetVersions_GivenTableWithoutStatus_SkipsStatusFilter 关联表一族是硬删，
// 没有 status 列——把它拼进 SQL 会让清零在这张表上直接报错。
func TestResetVersions_GivenTableWithoutStatus_SkipsStatusFilter(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `issue_labels` SET `sync_version`=\\? WHERE sync_version <> \\? AND sync_deleted_at = 0").
		WithArgs(0, 0).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	require.NoError(t, NewSyncState().ResetVersions(ctx, syncwire.KindIssueLabel))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListUnversioned 版本号只由 server 分配，为 0 就是「server 从没见过这一行」。
// 判据与 ClaimForAccount 同一套：只要存活的行——本机已经软删或落了墓碑的不该被重新
// 推上账号（R6）。
func TestListUnversioned(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT sync_id FROM `projects` WHERE \\(sync_account_id = \\? AND sync_version = 0 AND sync_deleted_at = 0 AND sync_id != ''\\) AND status = \\?").
		WithArgs(int64(7), consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"sync_id"}).AddRow("p-1").AddRow("p-2"))

	rows, err := repo.ListUnversioned(ctx, syncwire.KindProject, 7)
	require.NoError(t, err)
	assert.Equal(t, []string{"p-1", "p-2"}, rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListUnversioned_GivenTableWithoutStatus_SkipsStatusFilter 成员关系与执行目标是
// 硬删，没有 status 列——不能把一个不存在的列拼进 SQL。
func TestListUnversioned_GivenTableWithoutStatus_SkipsStatusFilter(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT sync_id FROM `agent_exec_targets` WHERE sync_account_id = \\? AND sync_version = 0 AND sync_deleted_at = 0 AND sync_id != ''").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"sync_id"}))

	rows, err := repo.ListUnversioned(ctx, syncwire.KindAgentExecTarget, 7)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListUnsyncedTombstones 补删除用的取数：本机已经软删、server 却从没收到过这条
// 删除的行。三个条件缺一不可——归属当前账号、有版本号（version = 0 = server 从没见过
// 这一行，没有什么要删的）、sync_deleted_at 仍为 0（非 0 = 那条墓碑已经送达过）。
func TestListUnsyncedTombstones(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT sync_id,sync_version FROM `agent_backends` WHERE \\(sync_account_id = \\? AND sync_version != 0 AND sync_deleted_at = 0 AND sync_id != ''\\) AND status = \\?").
		WithArgs(int64(7), consts.DELETE).
		WillReturnRows(sqlmock.NewRows([]string{"sync_id", "sync_version"}).
			AddRow("be-1", int64(42)).AddRow("be-2", int64(7)))

	rows, err := repo.ListUnsyncedTombstones(ctx, syncwire.KindAgentBackend, 7)
	require.NoError(t, err)
	assert.Equal(t, []ClaimedRow{{SyncID: "be-1", Version: 42}, {SyncID: "be-2", Version: 7}}, rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListUnsyncedTombstones_GivenTableWithoutStatus_ReturnsNothing 成员关系与执行目标
// 是硬删：行已经不在了，没有「软删但没上行」这个状态可查，也没有 status 列可拼。
// 它们的删除靠父行的级联入队送达（notify.enqueue 的 children 分支）。
func TestListUnsyncedTombstones_GivenTableWithoutStatus_ReturnsNothing(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	rows, err := NewSyncState().ListUnsyncedTombstones(ctx, syncwire.KindAgentExecTarget, 7)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListUnsyncedTombstones_GivenLoggedOut_DoesNothing 与 ClaimForAccount 同一条口径。
func TestListUnsyncedTombstones_GivenLoggedOut_DoesNothing(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	rows, err := NewSyncState().ListUnsyncedTombstones(ctx, syncwire.KindProject, 0)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClaimForAccount_GivenLoggedOut_DoesNothing R12：未登录时一行都不碰。
func TestClaimForAccount_GivenLoggedOut_DoesNothing(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	rows, err := NewSyncState().ClaimForAccount(ctx, syncwire.KindProject, 0)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClaimForAccount_GivenTheBoardKinds_ResolvesEveryTable 看板并入同步组带来的三个
// 对象类型必须都在表名白名单里：认领（R12a）逐个跑一遍同步组的全部类型，任何一个
// 不在白名单里就在那一次调用上报 ErrUnknownKind，把整轮同步——连同已经在用的九个
// 类型——一起打断。
func TestClaimForAccount_GivenTheBoardKinds_ResolvesEveryTable(t *testing.T) {
	for _, tc := range []struct{ kind, table string }{
		{syncwire.KindLabel, "labels"},
		{syncwire.KindIssue, "issues"},
	} {
		ctx, _, mock := testutils.Database(t)
		mock.ExpectQuery("SELECT rowid,sync_id,sync_version,sync_account_id FROM `"+tc.table+
			"` WHERE \\(\\(sync_account_id = 0 OR sync_account_id <> \\?\\) AND sync_deleted_at = 0\\) AND status = \\?").
			WithArgs(int64(7), consts.ACTIVE).
			WillReturnRows(sqlmock.NewRows([]string{"rowid", "sync_id", "sync_version", "sync_account_id"}))

		rows, err := NewSyncState().ClaimForAccount(ctx, tc.kind, 7)
		require.NoError(t, err, tc.kind)
		assert.Empty(t, rows)
		assert.NoError(t, mock.ExpectationsWereMet(), tc.kind)
	}
}

// TestClaimForAccount_GivenIssueLabels_SkipsStatusFilter 关联表是硬删，没有 status 列
// ——把它拼进 SQL 会让认领在这张表上直接报错。
func TestClaimForAccount_GivenIssueLabels_SkipsStatusFilter(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	mock.ExpectQuery("SELECT rowid,sync_id,sync_version,sync_account_id FROM `issue_labels` WHERE \\(sync_account_id = 0 OR sync_account_id <> \\?\\) AND sync_deleted_at = 0").
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"rowid", "sync_id", "sync_version", "sync_account_id"}))

	rows, err := NewSyncState().ClaimForAccount(ctx, syncwire.KindIssueLabel, 7)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestFindLocalID_GivenIssueLabel_Errors 关联表的主键是 (issue_id, label_id) 两列，
// 没有 id 列：按同步标识取本地主键在它身上没有意义（与 project_agents 同理），
// 拼出来的 `SELECT id FROM issue_labels` 会在运行时炸。
func TestFindLocalID_GivenIssueLabel_Errors(t *testing.T) {
	ctx, _, _ := testutils.Database(t)
	_, err := NewSyncState().FindLocalID(ctx, syncwire.KindIssueLabel, "link-1")
	assert.ErrorIs(t, err, ErrUnknownKind)
}
