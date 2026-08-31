package notification_repo_test

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/repository/notification_repo"
)

// appendSQLPattern 是 Append 必须发出的那条 SQL:分配 seq 与写入在同一条语句里
// 完成。分成「先 SELECT MAX(seq)+1 再 INSERT」两条语句的实现会漏掉这个模式而失败
// ——那种实现下两个并发写者会读到同一个 MAX(seq)、其中一条通知被静默丢弃
// (见 daemon_test.go 的并发用例)。
//
// 取最大 seq 的范围只按 conversation_id:主键是 (conversation_id, seq),再按写者收窄
// 会让这条对话的第二个对端从 1 重新分配,第一条通知就撞主键(见 daemon_test.go 的
// 跨对端用例)。
const appendSQLPattern = "INSERT INTO daemon_notification_journal " +
	"\\(conversation_id, seq, peer_fingerprint, payload, createtime\\) " +
	"SELECT \\?, COALESCE\\(MAX\\(seq\\), 0\\) \\+ 1, \\?, \\?, \\? " +
	"FROM daemon_notification_journal WHERE conversation_id = \\? " +
	"RETURNING seq"

// TestNotificationRepo_Append_AllocatesNextSeqInOneStatement 覆盖任务目标的
// 「一条通知能以下一个 seq 落库」:Append 只发一条语句,库分配的 seq 经 RETURNING
// 回填到入参上供调用方构造推送帧。
func TestNotificationRepo_Append_AllocatesNextSeqInOneStatement(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := notification_repo.NewNotification()

	mock.ExpectQuery(appendSQLPattern).
		WithArgs("s1", "peerA", []byte{1, 2}, sqlmock.AnyArg(), "s1").
		WillReturnRows(sqlmock.NewRows([]string{"seq"}).AddRow(7))

	n := &notification_repo.NotificationLog{
		PeerFingerprint: "peerA", ConversationID: "s1", Payload: string([]byte{1, 2}),
	}
	require.NoError(t, repo.Append(ctx, n))
	assert.Equal(t, int64(7), n.Seq, "库分配的 seq 必须回填到入参")
	assert.NotZero(t, n.Createtime, "落库时间必须被填上")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNotificationRepo_Append_PropagatesError 覆盖错误路径:落库失败必须冒泡给
// 调用方(R3 靠它判断「不推进 seq、不推送」),且失败时不得回填 Seq。
func TestNotificationRepo_Append_PropagatesError(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := notification_repo.NewNotification()

	mock.ExpectQuery(appendSQLPattern).WillReturnError(errors.New("disk I/O error"))

	n := &notification_repo.NotificationLog{
		PeerFingerprint: "peerA", ConversationID: "s1", Payload: string([]byte{1, 2}),
	}
	err := repo.Append(ctx, n)
	require.Error(t, err)
	assert.Zero(t, n.Seq, "落库失败不得回填 seq")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNotificationRepo_Append_IgnoresCallerSuppliedSeq 钉死「seq 只由库分配」这条唯一
// 写路径:调用方在入参上填了 Seq 也不会被写进 SQL —— 语句里绑的仍然只有 (对端, 会话,
// method, payload, 时间),seq 那一格由 COALESCE(MAX(seq),0)+1 现算。
//
// 会漏掉这个模式的实现:再开一条「按调用方给的 seq 写入」的路径。两条写路径并存时,
// 同一会话上就有两个 seq 分配者,谁也不知道对方给出去过什么 —— 主键冲突要么把通知
// 静默吞掉、要么把裸的唯一约束错误抛回热路径,而硬不变量要求的是单调、无洞、无重复。
func TestNotificationRepo_Append_IgnoresCallerSuppliedSeq(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := notification_repo.NewNotification()

	mock.ExpectQuery(appendSQLPattern).
		WithArgs("s1", "peerA", []byte{1, 2}, sqlmock.AnyArg(), "s1").
		WillReturnRows(sqlmock.NewRows([]string{"seq"}).AddRow(3))

	n := &notification_repo.NotificationLog{
		PeerFingerprint: "peerA", ConversationID: "s1", Seq: 99, Payload: string([]byte{1, 2}),
	}
	require.NoError(t, repo.Append(ctx, n))
	assert.Equal(t, int64(3), n.Seq, "入参里的 seq 必须被库分配到的那个覆盖掉")

	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNotificationRepo_LatestSeq_ReadsMaxSeqFromTheLog 覆盖「某会话最新的 seq」:
// 它的唯一真相源是通知日志自己的 MAX(seq),不是会话表上的某个冗余列。会话一条通知都
// 没有时报 0。
func TestNotificationRepo_LatestSeq_ReadsMaxSeqFromTheLog(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := notification_repo.NewNotification()

	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(seq\\), 0\\) FROM daemon_notification_journal WHERE peer_fingerprint = \\? AND conversation_id = \\?").
		WithArgs("peerA", "s1").
		WillReturnRows(sqlmock.NewRows([]string{"seq"}).AddRow(42))

	got, err := repo.LatestSeq(ctx, "peerA", "s1")
	require.NoError(t, err)
	assert.Equal(t, int64(42), got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNotificationRepo_LatestSeqByPeer_GroupsPerSession 覆盖会话清单要的那份「每条
// 会话的最新 seq」:一条 GROUP BY 查询把该对端全部会话的 MAX(seq) 一次取回,而不是按
// 会话数发 N 条查询。没有通知的会话不出现在结果里,调用方按 0 处理。
//
// 「该对端的会话」由 daemon_sessions 圈定(那份清单本来就从那里来),不是按日志自己的
// 来源列筛 —— 日志是一张没有上界也不回收的表,为这条查询给它再加一条以 peer_fingerprint
// 打头的索引,等于把全库最大那张表的索引占用翻一倍。
func TestNotificationRepo_LatestSeqByPeer_GroupsPerSession(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := notification_repo.NewNotification()

	mock.ExpectQuery("SELECT conversation_id, MAX\\(seq\\) AS seq FROM daemon_notification_journal" +
		" WHERE conversation_id IN \\(SELECT conversation_id FROM daemon_sessions WHERE peer_fingerprint = \\?\\)" +
		" GROUP BY conversation_id").
		WithArgs("peerA").
		WillReturnRows(sqlmock.NewRows([]string{"conversation_id", "seq"}).
			AddRow("s1", 42).
			AddRow("s2", 7))

	got, err := repo.LatestSeqByPeer(ctx, "peerA")
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{"s1": 42, "s2": 7}, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNotificationRepo_ListSince_CursorBoundaries 覆盖测试接缝表要求的「增量拉取边界」:
// 起始游标为 0、起始游标大于最新 seq、以及翻页 hasMore 标志。
func TestNotificationRepo_ListSince_CursorBoundaries(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := notification_repo.NewNotification()

	t.Run("cursor=0 返回从 seq=1 开始的全部通知", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"peer_fingerprint", "conversation_id", "seq", "method", "payload", "createtime"}).
			AddRow("peerA", "s1", 1, "runtime.event", "{}", 100).
			AddRow("peerA", "s1", 2, "runtime.event", "{}", 200)
		mock.ExpectQuery("SELECT \\* FROM `daemon_notification_journal` WHERE peer_fingerprint = \\? AND conversation_id = \\? AND seq > \\? ORDER BY seq ASC LIMIT \\?").
			WithArgs("peerA", "s1", int64(0), 11).
			WillReturnRows(rows)

		got, hasMore, err := repo.ListSince(ctx, "peerA", "s1", 0, 10)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, int64(1), got[0].Seq)
		assert.Equal(t, int64(2), got[1].Seq)
		assert.False(t, hasMore)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cursor 大于最新 seq 返回空且 hasMore=false", func(t *testing.T) {
		mock.ExpectQuery("SELECT \\* FROM `daemon_notification_journal` WHERE peer_fingerprint = \\? AND conversation_id = \\? AND seq > \\? ORDER BY seq ASC LIMIT \\?").
			WithArgs("peerA", "s1", int64(999), 11).
			WillReturnRows(sqlmock.NewRows([]string{"peer_fingerprint", "conversation_id", "seq", "method", "payload", "createtime"}))

		got, hasMore, err := repo.ListSince(ctx, "peerA", "s1", 999, 10)
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.False(t, hasMore)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("超过 limit 的剩余行触发 hasMore=true 且只返回 limit 条", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"peer_fingerprint", "conversation_id", "seq", "method", "payload", "createtime"}).
			AddRow("peerA", "s1", 1, "runtime.event", "{}", 100).
			AddRow("peerA", "s1", 2, "runtime.event", "{}", 200).
			AddRow("peerA", "s1", 3, "runtime.event", "{}", 300)
		mock.ExpectQuery("SELECT \\* FROM `daemon_notification_journal` WHERE peer_fingerprint = \\? AND conversation_id = \\? AND seq > \\? ORDER BY seq ASC LIMIT \\?").
			WithArgs("peerA", "s1", int64(0), 3).
			WillReturnRows(rows)

		got, hasMore, err := repo.ListSince(ctx, "peerA", "s1", 0, 2)
		require.NoError(t, err)
		require.Len(t, got, 2, "page must be capped at limit even though the mock returned limit+1 rows")
		assert.Equal(t, int64(1), got[0].Seq)
		assert.Equal(t, int64(2), got[1].Seq)
		assert.True(t, hasMore)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestNotificationRepo_OldestSeq_ReportsTheSurvivingFloor 覆盖「这条会话的日志从哪一格
// 开始还在」:补齐的客户端只有拿到这个下界,才分得清「游标之后那一条还没写」与「它
// 已经不在这台机器上了」。分不清就只能一直等,会话静默冻住。
func TestNotificationRepo_OldestSeq_ReportsTheSurvivingFloor(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := notification_repo.NewNotification()

	mock.ExpectQuery("SELECT COALESCE\\(MIN\\(seq\\), 0\\) FROM daemon_notification_journal "+
		"WHERE peer_fingerprint = \\? AND conversation_id = \\?").
		WithArgs("peerA", "s1").
		WillReturnRows(sqlmock.NewRows([]string{"seq"}).AddRow(900))

	got, err := repo.OldestSeq(ctx, "peerA", "s1")
	require.NoError(t, err)
	assert.Equal(t, int64(900), got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNotificationRepo_DeleteAll_RemovesTheWholeSessionJournal 覆盖整条会话的日志
// 清空(会话删除的另一半):删掉这一条 (对端, 对话) 的**全部**行,包括高水位那一条。
//
// 高水位那一行也必须删掉:留下它,那条会话的转录就永远清不干净。抹掉高水位带来的
// seq 复位由镜像客户端按 dropCursorAboveHighWater 那条规则收口。
func TestNotificationRepo_DeleteAll_RemovesTheWholeSessionJournal(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := notification_repo.NewNotification()

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `daemon_notification_journal` WHERE peer_fingerprint = \\? AND conversation_id = \\?").
		WithArgs("peerA", "s1").
		WillReturnResult(sqlmock.NewResult(0, 900))
	mock.ExpectCommit()

	deleted, err := repo.DeleteAll(ctx, "peerA", "s1")
	require.NoError(t, err)
	assert.Equal(t, int64(900), deleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNotificationRepo_DeleteAll_LeavesAnotherPeersConversationAlone 钉住删除的权限
// 边界:对端指纹退出主键之后**仍然**收窄这条删除。
//
// 它守的不再是「两个对端的同号会话」—— conversation_id 全局唯一,那种构造已经不存在
// 了 —— 而是授权本身:补齐与删除这两条路径不另做归属校验,一个对端点名别人的对话号时,
// 拦住它的就是这里的 peer_fingerprint 条件(integration_test.go 的
// TestIntegration_SessionDelete_ScopedToTheCallersPeer)。
func TestNotificationRepo_DeleteAll_LeavesAnotherPeersConversationAlone(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := notification_repo.NewNotification()

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `daemon_notification_journal` WHERE peer_fingerprint = \\? AND conversation_id = \\?").
		WithArgs("peerB", "s1").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	deleted, err := repo.DeleteAll(ctx, "peerB", "s1")
	require.NoError(t, err)
	assert.Zero(t, deleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}
