package syncstate_repo

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/model/entity/project_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
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
	mock.ExpectExec("UPDATE `project_locations` SET `sync_account_id`=\\?,`sync_deleted_at`=\\?,`sync_origin`=\\?,`sync_updated_at`=\\?,`sync_version`=\\? WHERE sync_id = \\?").
		WithArgs(int64(7), int64(0), "3", int64(1700), int64(12), "loc-1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.SaveMeta(ctx, syncwire.KindProjectLocation, "loc-1", syncmeta_entity.SyncMeta{
		SyncID: "loc-1", SyncAccountID: 7, SyncVersion: 12, SyncUpdatedAt: 1700, SyncOrigin: "3",
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClaimUnowned R12a：登录前已有的行归入当前账号；没有同步标识的历史行就地补
// 一个。只认领**存活**的行——本机已经软删的行不该被当成一次新建推上账号（R6）。
func TestClaimUnowned(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT rowid,sync_id,sync_version FROM `projects` WHERE \\(sync_account_id = 0 AND sync_deleted_at = 0\\) AND status = \\?").
		WithArgs(consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"rowid", "sync_id", "sync_version"}).
			AddRow(int64(1), "p-known", int64(4)).
			AddRow(int64(2), "", int64(0)))
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `projects` SET `sync_account_id`=\\? WHERE rowid = \\?").
		WithArgs(int64(7), int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `projects` SET `sync_account_id`=\\?,`sync_id`=\\? WHERE rowid = \\?").
		WithArgs(int64(7), sqlmock.AnyArg(), int64(2)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	rows, err := repo.ClaimUnowned(ctx, syncwire.KindProject, 7)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "p-known", rows[0].SyncID)
	assert.Equal(t, int64(4), rows[0].Version, "基版本沿用行上那一个")
	assert.NotEmpty(t, rows[1].SyncID, "历史行就地补一个标识")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestClaimUnowned_GivenTableWithoutStatus_SkipsStatusFilter 成员关系与执行目标是
// 硬删，没有 status 列——认领时不能把一个不存在的列拼进 SQL。
func TestClaimUnowned_GivenTableWithoutStatus_SkipsStatusFilter(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectQuery("SELECT rowid,sync_id,sync_version FROM `project_agents` WHERE sync_account_id = 0 AND sync_deleted_at = 0").
		WillReturnRows(sqlmock.NewRows([]string{"rowid", "sync_id", "sync_version"}))

	rows, err := repo.ClaimUnowned(ctx, syncwire.KindProjectAgent, 7)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResetVersions 换了一套 server 之后，行上盖的还是上一套序列的版本号：既比不了
// 大小，也会把新序列的全量快照整个挡在门外（版本守卫是「本机版本 >= 来的版本就不
// 落」）。清零只碰版本号一列。
func TestResetVersions(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := NewSyncState()
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `agents` SET `sync_version`=\\? WHERE sync_account_id = \\?").
		WithArgs(0, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	require.NoError(t, repo.ResetVersions(ctx, syncwire.KindAgent, 7))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestResetVersions_GivenLoggedOut_DoesNothing R12：未登录时一行都不碰。
func TestResetVersions_GivenLoggedOut_DoesNothing(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	require.NoError(t, NewSyncState().ResetVersions(ctx, syncwire.KindProject, 0))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestListUnversioned 版本号只由 server 分配，为 0 就是「server 从没见过这一行」。
// 判据与 ClaimUnowned 同一套：只要存活的行——本机已经软删或落了墓碑的不该被重新
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

// TestClaimUnowned_GivenLoggedOut_DoesNothing R12：未登录时一行都不碰。
func TestClaimUnowned_GivenLoggedOut_DoesNothing(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	rows, err := NewSyncState().ClaimUnowned(ctx, syncwire.KindProject, 0)
	require.NoError(t, err)
	assert.Empty(t, rows)
	assert.NoError(t, mock.ExpectationsWereMet())
}
