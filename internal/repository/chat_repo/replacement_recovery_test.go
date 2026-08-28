package chat_repo_test

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/repository/app_setting_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// valueContains 校验写进 app_settings 的 value 含指定子串。
type valueContains struct{ sub string }

func (m valueContains) Match(v driver.Value) bool {
	s, ok := v.(string)
	return ok && strings.Contains(s, m.sub)
}

// useRealAppSettings 让恢复标记走真实的 app_settings 仓储实现(仍是 sqlmock,不碰真库),
// 以便断言标记确实落在 app_settings 而不是 chat_messages。
func useRealAppSettings(t *testing.T) {
	t.Helper()
	prev := app_setting_repo.AppSetting()
	app_setting_repo.RegisterAppSetting(app_setting_repo.NewAppSetting())
	t.Cleanup(func() { app_setting_repo.RegisterAppSetting(prev) })
}

func sampleRecovery() *chat_repo.ReplacementRecovery {
	return &chat_repo.ReplacementRecovery{
		RecoverySessionID:    -7, // = -(3*2+1),隐藏命名空间由会话 id 推出
		SessionID:            3,
		FromSeq:              5,
		RequestMessageID:     41,
		UserMessageID:        51,
		AssistantMessageID:   52,
		OldProviderSessionID: "pi-old",
		NewProviderSessionID: "pi-new",
		OldAgentStatus:       "idle",
		OldLastMessageAt:     1700,
		State:                chat_repo.ReplacementRecoveryPending,
	}
}

// TestReplacementRecovery_LivesInAppSettings 走完「建立 → 查找 → 状态翻转」全程,
// 全程只触碰 app_settings 的按 key 点查/点写:sqlmock 是有序严格模式,任何一条落到
// chat_messages 的语句都会让本用例失败。
func TestReplacementRecovery_LivesInAppSettings(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	useRealAppSettings(t)

	const key = "chat.pi_recovery:3"
	recovery := sampleRecovery()

	// 建立。
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `app_settings`").
		WithArgs(key, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()
	require.NoError(t, chat_repo.SaveReplacementRecovery(ctx, recovery))

	marker, err := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, err)
	assert.Equal(t, key, marker.Key)

	// 查找。
	mock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))
	found, err := chat_repo.FindReplacementRecoveryForSession(ctx, 3)
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, *recovery, *found, "标记必须逐字段往返")

	// 状态翻转。
	mock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs(key, 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `app_settings`").
		WithArgs(key, valueContains{sub: `"state":"acknowledged"`}, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	require.NoError(t, chat_repo.AcknowledgeReplacementRecovery(ctx, found))
	assert.Equal(t, chat_repo.ReplacementRecoveryAcknowledged, found.State)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReplacementRecovery_AcknowledgeRejectsMissingMarker 保持借道 chat_messages 时期的
// 失败语义:标记已被别的收尾删掉时不重新造一条,而是报「所有权丢失」。
func TestReplacementRecovery_AcknowledgeRejectsMissingMarker(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	useRealAppSettings(t)

	mock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:3", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}))

	err := chat_repo.AcknowledgeReplacementRecovery(ctx, sampleRecovery())
	assert.ErrorIs(t, err, chat_repo.ErrReplacementOwnershipLost)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReplacementRecovery_DeleteRemovesMarkerAndHiddenRows 证明清理同时收走 app_settings
// 里的标记与隐藏命名空间里的原始消息(含它们的块行),返回值仍是「标记被删掉几条」。
func TestReplacementRecovery_DeleteRemovesMarkerAndHiddenRows(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	useRealAppSettings(t)

	ns, err := chat_repo.ReplacementRecoverySessionID(3)
	require.NoError(t, err)

	mock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\?\\)").
		WithArgs(ns).
		WillReturnResult(sqlmock.NewResult(0, 7))
	mock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(ns).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:3").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	deleted, err := chat_repo.DeleteReplacementRecovery(ctx, 3)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted, "返回值是标记行数,不含隐藏消息")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestReplacementRecovery_NamespaceRejectsSecondGeneration 证明命名空间改由会话 id 推出后,
// 同一会话上已有在飞的替换生成时第二次申领失败关闭。
func TestReplacementRecovery_NamespaceRejectsSecondGeneration(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	useRealAppSettings(t)

	mock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:3", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow("chat.pi_recovery:3", "{}", 0))

	err := chat_repo.EnsureReplacementRecoveryNamespaceAvailable(ctx, 3)
	assert.ErrorIs(t, err, chat_repo.ErrReplacementNamespaceCollision)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplacementRecovery_NamespaceChecksHiddenRowsWhenUnclaimed(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	useRealAppSettings(t)

	ns, err := chat_repo.ReplacementRecoverySessionID(3)
	require.NoError(t, err)

	mock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:3", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}))
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(ns).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))

	assert.NoError(t, chat_repo.EnsureReplacementRecoveryNamespaceAvailable(ctx, 3))
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplacementRecoveryMarkerRoundTripPreservesExactOwnership(t *testing.T) {
	recovery := sampleRecovery()
	recovery.OldAgentStatus = "error"
	recovery.OldLastMessageAt = 1234

	marker, err := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, err)

	decoded, err := chat_repo.ParseReplacementRecoveryMarker(marker)
	require.NoError(t, err)
	ns, err := chat_repo.ReplacementRecoverySessionID(recovery.SessionID)
	require.NoError(t, err)
	assert.Equal(t, ns, decoded.RecoverySessionID)
	assert.Equal(t, recovery.SessionID, decoded.SessionID)
	assert.Equal(t, recovery.FromSeq, decoded.FromSeq)
	assert.Equal(t, recovery.RequestMessageID, decoded.RequestMessageID)
	assert.Equal(t, recovery.UserMessageID, decoded.UserMessageID)
	assert.Equal(t, recovery.AssistantMessageID, decoded.AssistantMessageID)
	assert.Equal(t, recovery.OldProviderSessionID, decoded.OldProviderSessionID)
	assert.Equal(t, recovery.NewProviderSessionID, decoded.NewProviderSessionID)
	assert.Equal(t, recovery.OldAgentStatus, decoded.OldAgentStatus)
	assert.Equal(t, recovery.OldLastMessageAt, decoded.OldLastMessageAt)
	assert.Equal(t, recovery.State, decoded.State)
}

// TestReplacementRecoveryMarkerRejectsForeignKey 证明 key 与载荷不一致的标记不被接受
// ——命名空间由会话 id 推出后,这是「这条记录归谁」的唯一判据。
func TestReplacementRecoveryMarkerRejectsForeignKey(t *testing.T) {
	marker, err := chat_repo.NewReplacementRecoveryMarker(sampleRecovery())
	require.NoError(t, err)
	marker.Key = "chat.pi_recovery:9"

	got, err := chat_repo.ParseReplacementRecoveryMarker(marker)
	require.Error(t, err)
	assert.Nil(t, got)
}

func TestReplacementRecoverySessionIDIsolatesGenerationsAndVisibleSessions(t *testing.T) {
	first, err := chat_repo.ReplacementRecoverySessionID(100)
	require.NoError(t, err)
	second, err := chat_repo.ReplacementRecoverySessionID(101)
	require.NoError(t, err)

	assert.Negative(t, first)
	assert.Negative(t, second)
	assert.NotEqual(t, first, second, "each session must own a distinct hidden namespace")
	assert.NotEqual(t, int64(100), first, "hidden recovery rows must not share a visible session namespace")
	assert.NotEqual(t, chat_repo.ReplacementStageSessionID(100), first,
		"the recovery namespace must not collide with the legacy per-session stage")
}

func TestReplacementRecovery_MoveMessagesFromSeqPreservesOriginalRows(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(-201), int64(3), 5).
		WillReturnResult(sqlmock.NewResult(0, 4))

	moved, err := chat_repo.MoveMessagesFromSeq(ctx, 3, -201, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(4), moved)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplacementRecovery_DeleteOwnedMessagesUsesExactGenerationIDs(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)\\)").
		WithArgs(int64(3), int64(51), int64(52)).
		WillReturnResult(sqlmock.NewResult(0, 6))
	mock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)").
		WithArgs(int64(3), int64(51), int64(52)).
		WillReturnResult(sqlmock.NewResult(0, 2))

	deleted, err := chat_repo.DeleteOwnedReplacementMessages(ctx, 3, 51, 52)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplacementRecovery_FindForMessageUsesExactOwnership(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	useRealAppSettings(t)

	marker, err := chat_repo.NewReplacementRecoveryMarker(sampleRecovery())
	require.NoError(t, err)

	for _, tc := range []struct {
		name      string
		messageID int64
		wantFound bool
	}{
		{name: "生成拥有的 assistant 行", messageID: 52, wantFound: true},
		{name: "不属于该生成的行", messageID: 99, wantFound: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
				WithArgs("chat.pi_recovery:3", 1).
				WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
					AddRow(marker.Key, marker.Value, marker.Updatetime))

			got, err := chat_repo.FindReplacementRecoveryForMessage(ctx, 3, tc.messageID)
			require.NoError(t, err)
			if tc.wantFound {
				require.NotNil(t, got)
				assert.Equal(t, int64(41), got.RequestMessageID)
			} else {
				assert.Nil(t, got)
			}
		})
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplacementRecovery_EnsureActiveTailOwnedUsesPersistedIDsAndSequence(t *testing.T) {
	for _, tc := range []struct {
		name       string
		unexpected int64
		wantErr    bool
	}{
		{name: "exact owned replacement pair", unexpected: 0},
		{name: "unowned follow-up row overlaps recovery tail", unexpected: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _, mock := testutils.Database(t)
			recovery := &chat_repo.ReplacementRecovery{
				SessionID: 3, FromSeq: 5, UserMessageID: 51, AssistantMessageID: 52,
			}
			mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\? AND seq >= \\? AND id NOT IN \\(\\?,\\?\\)").
				WithArgs(int64(3), 5, int64(51), int64(52)).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(tc.unexpected))

			err := chat_repo.EnsureReplacementActiveTailOwned(ctx, recovery)
			if tc.wantErr {
				require.ErrorIs(t, err, chat_repo.ErrReplacementOwnershipLost)
			} else {
				require.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestReplacementRecovery_AcknowledgeRejectsStaleGeneration 证明状态翻转认的是「哪一次
// 生成」:库里那条标记换了一次生成后,旧生成的收尾不再翻它。
func TestReplacementRecovery_AcknowledgeRejectsStaleGeneration(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	useRealAppSettings(t)

	newer := sampleRecovery()
	newer.NewProviderSessionID = "pi-new-2"
	marker, err := chat_repo.NewReplacementRecoveryMarker(newer)
	require.NoError(t, err)

	mock.ExpectQuery("SELECT \\* FROM `app_settings` WHERE `key` = \\?").
		WithArgs("chat.pi_recovery:3", 1).
		WillReturnRows(sqlmock.NewRows([]string{"key", "value", "updatetime"}).
			AddRow(marker.Key, marker.Value, marker.Updatetime))

	stale := sampleRecovery()
	err = chat_repo.AcknowledgeReplacementRecovery(ctx, stale)
	require.ErrorIs(t, err, chat_repo.ErrReplacementOwnershipLost)
	assert.Equal(t, chat_repo.ReplacementRecoveryPending, stale.State)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplacementRecovery_RestoreSessionRejectsStaleGeneration(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	recovery := &chat_repo.ReplacementRecovery{
		SessionID:            3,
		OldProviderSessionID: "pi-old",
		NewProviderSessionID: "pi-new",
		OldAgentStatus:       "idle",
		OldLastMessageAt:     1234,
	}

	mock.ExpectExec("UPDATE `chat_sessions` SET `provider_session_id`=\\?,`agent_status`=\\?,`last_message_at`=\\?,`updatetime`=\\? WHERE id = \\? AND provider_session_id = \\?").
		WithArgs("pi-old", "idle", int64(1234), sqlmock.AnyArg(), int64(3), "pi-new").
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := chat_repo.RestoreReplacementSession(ctx, recovery)
	require.ErrorIs(t, err, chat_repo.ErrReplacementOwnershipLost)
	assert.NoError(t, mock.ExpectationsWereMet())
}
