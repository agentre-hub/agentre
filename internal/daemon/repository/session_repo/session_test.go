package session_repo_test

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/database/db"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/daemon/repository/session_repo"
)

// TestSessionRepo_Upsert_WritesRowAndIsRepeatable 覆盖「会话开始时建行」:一轮执行
// 起手时把 (对端, 会话) 这一行写进 daemon_sessions,同一会话再起一轮时更新同一行而不是
// 撞主键报错 —— 会话 id 在整个会话生命周期里复用,第二轮报错会让清单从此缺这条会话。
func TestSessionRepo_Upsert_WritesRowAndIsRepeatable(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	row := &session_repo.DaemonSession{
		PeerFingerprint: "peerA", PeerSessionID: "s1", AgentID: 7,
		Cwd: "/work", BackendType: "claudecode", LifecycleState: "running",
	}

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `daemon_sessions`.*ON DUPLICATE KEY UPDATE").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Upsert(ctx, row))
	assert.NotZero(t, row.Createtime, "建行时间必须被填上")
	assert.NotZero(t, row.LastMessageAt)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `daemon_sessions`.*ON DUPLICATE KEY UPDATE").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Upsert(ctx, row), "同一会话的第二轮必须更新同一行,不能撞主键")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_UpdateLifecycle_TouchesOnlyThatPeersRow 覆盖生命周期迁移:轮末
// running→idle 只改这一条 (对端, 会话),不按会话 id 单独定位 —— 两个对端各持同一个
// 本地会话 id 时按 id 更新会改到别人那条(R16)。
func TestSessionRepo_UpdateLifecycle_TouchesOnlyThatPeersRow(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `daemon_sessions` SET `last_message_at`=\\?,`lifecycle_state`=\\? WHERE peer_fingerprint = \\? AND peer_session_id = \\?").
		WithArgs(sqlmock.AnyArg(), "idle", "peerA", "s1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateLifecycle(ctx, "peerA", "s1", "idle"))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_ListByPeer_ScopedToCaller 覆盖清单查询的对端限定(R16):SQL 必须
// 带上 peer_fingerprint 条件,否则一个对端能看见另一个对端的会话。
func TestSessionRepo_ListByPeer_ScopedToCaller(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	rows := sqlmock.NewRows([]string{"peer_fingerprint", "peer_session_id", "agent_id", "cwd", "backend_type", "lifecycle_state", "title", "agent_sync_id", "provider_session_id", "createtime", "last_message_at"}).
		AddRow("peerA", "s1", 7, "/work", "claudecode", "running", "fix the bug", "01HXsync000000000000000000", "claude-abc123", 100, 200).
		AddRow("peerA", "s2", 8, "/other", "codex", "idle", "", "", "", 100, 150)
	mock.ExpectQuery("SELECT \\* FROM `daemon_sessions` WHERE peer_fingerprint = \\? ORDER BY last_message_at DESC").
		WithArgs("peerA").
		WillReturnRows(rows)

	got, err := repo.ListByPeer(ctx, "peerA")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "s1", got[0].PeerSessionID)
	assert.Equal(t, "claudecode", got[0].BackendType)
	assert.Equal(t, "running", got[0].LifecycleState)
	assert.Equal(t, int64(7), got[0].AgentID)
	assert.Equal(t, "fix the bug", got[0].Title)
	assert.Equal(t, "01HXsync000000000000000000", got[0].AgentSyncID)
	assert.Equal(t, "claude-abc123", got[0].ProviderSessionID)
	assert.Empty(t, got[1].Title, "老会话缺这些字段时如实留空")
	assert.Empty(t, got[1].AgentSyncID)
	assert.Empty(t, got[1].ProviderSessionID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_ListAll_ReturnsRowsAcrossPeers covers claimed-daemon visibility:
// account authorization is decided above the repository, so this deliberately has
// no peer filter while retaining the unchanged composite session primary key.
func TestSessionRepo_ListAll_ReturnsRowsAcrossPeers(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	rows := sqlmock.NewRows([]string{"peer_fingerprint", "peer_session_id", "agent_id", "cwd", "backend_type", "lifecycle_state", "title", "agent_sync_id", "provider_session_id", "createtime", "last_message_at"}).
		AddRow("peerA", "s1", 7, "/work", "claudecode", "running", "", "", "", 100, 200).
		AddRow("peerB", "s1", 8, "/other", "codex", "idle", "", "", "", 100, 150)
	mock.ExpectQuery("SELECT \\* FROM `daemon_sessions` ORDER BY last_message_at DESC").WillReturnRows(rows)

	got, err := repo.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "peerA", got[0].PeerFingerprint)
	assert.Equal(t, "peerB", got[1].PeerFingerprint)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_Find_ScopedToPeer 覆盖单条查询同样带对端限定:调用方拿它判断
// 「这条会话是不是我的」,只按会话 id 查会让跨对端接管在 SQL 层就成立。
func TestSessionRepo_Find_ScopedToPeer(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	rows := sqlmock.NewRows([]string{"peer_fingerprint", "peer_session_id", "backend_type", "lifecycle_state", "title", "agent_sync_id", "provider_session_id"}).
		AddRow("peerA", "s1", "claudecode", "running", "fix the bug", "01HXsync000000000000000000", "claude-abc123")
	mock.ExpectQuery("SELECT \\* FROM `daemon_sessions` WHERE peer_fingerprint = \\? AND peer_session_id = \\?").
		WithArgs("peerA", "s1", 1).
		WillReturnRows(rows)

	got, err := repo.Find(ctx, "peerA", "s1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "claudecode", got.BackendType)
	assert.Equal(t, "fix the bug", got.Title)
	assert.Equal(t, "01HXsync000000000000000000", got.AgentSyncID)
	assert.Equal(t, "claude-abc123", got.ProviderSessionID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_Find_NotFound 覆盖未命中:按 (nil, nil) 返回,让调用方按「不是我的
// 会话」处理,而不是把 gorm.ErrRecordNotFound 当成 I/O 故障往客户端冒泡。
func TestSessionRepo_Find_NotFound(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	mock.ExpectQuery("SELECT \\* FROM `daemon_sessions`").
		WithArgs("peerA", "nope", 1).
		WillReturnError(gorm.ErrRecordNotFound)

	got, err := repo.Find(ctx, "peerA", "nope")
	require.NoError(t, err)
	assert.Nil(t, got, "record-not-found should map to (nil, nil)")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_InterruptAll_SweepsEveryNonTerminalRow 覆盖 R10 的启动清扫:
// daemon 启动时把**全部对端**的非终态会话一次改成已中断。条件必须是
// 「lifecycle_state <> interrupted」而不是按对端或按会话枚举 —— 重启时 daemon 内存里
// 一条会话都没有,没有可枚举的来源。
func TestSessionRepo_InterruptAll_SweepsEveryNonTerminalRow(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `daemon_sessions` SET `last_message_at`=\\?,`lifecycle_state`=\\? WHERE lifecycle_state <> \\?").
		WithArgs(sqlmock.AnyArg(), "interrupted", "interrupted").
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectCommit()

	n, err := repo.InterruptAll(ctx, "interrupted")
	require.NoError(t, err)
	assert.Equal(t, int64(3), n, "受影响行数要回给调用方,启动日志据此说明清扫了几条")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_CountByLifecycle_CountsOnlyThatState 覆盖 `agentred status` 的
// 「活跃会话数」:它数的是库里此刻停在某个生命周期上的行(daemon 自己记的那份真相),
// 一次 COUNT 拿到,而不是把行全查出来在内存里数。
//
// 这一列曾经答的是一张没有任何写入方的内存表,于是有轮次在跑时也恒印 0 —— 读的人
// 据此以为自己的会话没了。
func TestSessionRepo_CountByLifecycle_CountsOnlyThatState(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	mock.ExpectQuery("SELECT count\\(\\*\\) FROM `daemon_sessions` WHERE lifecycle_state = \\?").
		WithArgs("running").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))

	n, err := repo.CountByLifecycle(ctx, "running")
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_Delete_ScopedToThatPeersRow 覆盖整条会话的删除:只删这一条
// (对端, 会话),并交回真的删了几行。不带对端指纹的 DELETE 会把别的机器上同号的
// 那条会话一起抹掉 —— 会话 id 是各客户端本地自增的,重号是常态。
func TestSessionRepo_Delete_ScopedToThatPeersRow(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `daemon_sessions` WHERE peer_fingerprint = \\? AND peer_session_id = \\?").
		WithArgs("peerA", "s1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	deleted, err := repo.Delete(ctx, "peerA", "s1")
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_Delete_MissingRowIsNotAnError 覆盖重复删除:会话行早就不在时删掉
// 零行、不报错。报错会让调用方(server 那条删除待办)永远重放同一条指令。
func TestSessionRepo_Delete_MissingRowIsNotAnError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `daemon_sessions` WHERE peer_fingerprint = \\? AND peer_session_id = \\?").
		WithArgs("peerA", "gone").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	deleted, err := repo.Delete(ctx, "peerA", "gone")
	require.NoError(t, err)
	assert.Zero(t, deleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_Upsert_LeavesTheSessionModelTargetAlone 是一条**纪律守卫**:
// provider_key / model_key 绝不能出现在 Upsert 的赋值列里。
//
// Upsert 在**每一轮起手**都会跑,携带的是当轮 RunParams 里的元数据。会话级
// ModelTarget 不在 RunParams 里,一旦它进了赋值列,每轮起手都会拿零值把用户轮中
// 刚选好的模型冲回「跟随 Agent 绑定」——而且是静默的。桌面端 chat_entity 的
// ModelKey 注释写的是同一条纪律的另一半(「普通 Session Save 必须 Omit 这一列」)。
//
// 断言方式是把整段赋值列钉死并锚到行尾:多出任何一列都匹配不上。
func TestSessionRepo_Upsert_LeavesTheSessionModelTargetAlone(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("ON DUPLICATE KEY UPDATE `agent_id`=VALUES\\(`agent_id`\\),`cwd`=VALUES\\(`cwd`\\)," +
		"`backend_type`=VALUES\\(`backend_type`\\),`lifecycle_state`=VALUES\\(`lifecycle_state`\\)," +
		"`title`=VALUES\\(`title`\\),`agent_sync_id`=VALUES\\(`agent_sync_id`\\)," +
		"`project_sync_id`=VALUES\\(`project_sync_id`\\)," +
		"`provider_session_id`=VALUES\\(`provider_session_id`\\),`last_message_at`=VALUES\\(`last_message_at`\\)$").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Upsert(ctx, &session_repo.DaemonSession{
		PeerFingerprint: "peerA", PeerSessionID: "s1", BackendType: "claudecode",
		LifecycleState: "running",
		// 就算调用方糊里糊涂带上了它们,也不该被写进去。
		ProviderKey: "should-not-be-assigned", ModelKey: "should-not-be-assigned",
	}))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestDaemonSessionEntity_PlainUpdatesNeverRewriteLastMessageAt 守 decision 10 的
// 那句主张:「会话最后活动时刻」改名成 last_message_at 之后,GORM 把 UpdatedAt 认作
// 行更新时刻、在每一次写入上自动改写它的陷阱随之消失——防御不再需要
// autoUpdateTime:false 之类的标注,因为招来它的那个名字没有了。
//
// 这一格是会话清单的排序键与「最后活动」的唯一真相源(线格式
// SessionSummary.last_message_at)。任何一次不指名它的普通更新都顶掉它,清单顺序就
// 会被一次改模型、一次改标题这样的非活动写入打乱。断言用锚到行尾的赋值列:多出
// 任何一列(尤其是自动补上的时刻列)都匹配不上。
func TestDaemonSessionEntity_PlainUpdatesNeverRewriteLastMessageAt(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `daemon_sessions` SET `cwd`=\\? WHERE peer_fingerprint = \\? AND peer_session_id = \\?").
		WithArgs("/work", "peerA", "s1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := db.Ctx(ctx).Model(&session_repo.DaemonSession{}).
		Where("peer_fingerprint = ? AND peer_session_id = ?", "peerA", "s1").
		Updates(map[string]any{"cwd": "/work"}).Error
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestSessionRepo_Upsert_CarriesProjectSyncID 覆盖「会话发起那一刻就记下项目」。
//
// 此前 agentred 只存 cwd,项目归属由服务端按 (指纹, cwd) 反推(agent_sessions 决策
// 12)。日活跃统计要按项目分组,而它走的是一条**不上行任何路径**的纯计数通道 ——
// 反推那条路在那里用不了。项目因此必须在发起时随会话落库。
//
// 它和 title / agent_sync_id 同批:每轮起手携带当轮的值、幂等覆盖,所以既要出现在
// 插入列里,也要出现在冲突更新列里 —— 只插不更新的话,会话换了项目就再也改不回来。
func TestSessionRepo_Upsert_CarriesProjectSyncID(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := session_repo.NewSession()

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `daemon_sessions`.*`project_sync_id`.*ON DUPLICATE KEY UPDATE.*`project_sync_id`").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.Upsert(ctx, &session_repo.DaemonSession{
		PeerFingerprint: "peerA", PeerSessionID: "s1",
		Cwd: "/work", BackendType: "claudecode", LifecycleState: "running",
		ProjectSyncID: "prj-9",
	}))

	assert.NoError(t, mock.ExpectationsWereMet())
}
