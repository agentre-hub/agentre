package chat_repo_test

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-ai/agentre/internal/repository/chat_repo"
)

// blocksJSONContainsMatcher 是 sqlmock 自定义参数匹配器:校验 UPDATE 时传给 blocks_json
// 列的值包含全部指定子串,捕捉 AnyArg() 查不出的「未改写(原样回传 JSON)」缺陷。
type blocksJSONContainsMatcher struct {
	substrings []string
}

func (m blocksJSONContainsMatcher) Match(v driver.Value) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, sub := range m.substrings {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

// TestFindSubagentStateInBlocksJSON 单测按 toolUseID 读出 subagent_state 的 task_id +
// status(供 StopBackgroundTask 定位 CLI task_id)。
func TestFindSubagentStateInBlocksJSON(t *testing.T) {
	const input = `[` +
		`{"type":"subagent_state","data":{"parent_tool_call_id":"tu1","task_id":"b0n82mqaj","kind":"local_bash","status":"running"}},` +
		`{"type":"text","data":{"text":"hi"}}` +
		`]`

	t.Run("命中返回 task_id + status", func(t *testing.T) {
		taskID, status, found, err := chat_repo.FindSubagentStateInBlocksJSON(input, "tu1")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "b0n82mqaj", taskID)
		assert.Equal(t, "running", status)
	})

	t.Run("旧块无 task_id:found=true 但 taskID 空", func(t *testing.T) {
		const legacy = `[{"type":"subagent_state","data":{"parent_tool_call_id":"tu1","kind":"local_bash","status":"running"}}]`
		taskID, status, found, err := chat_repo.FindSubagentStateInBlocksJSON(legacy, "tu1")
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, "", taskID)
		assert.Equal(t, "running", status)
	})

	t.Run("无命中返回 found=false", func(t *testing.T) {
		_, _, found, err := chat_repo.FindSubagentStateInBlocksJSON(input, "tu-missing")
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("空 JSON 返回 false 不报错", func(t *testing.T) {
		_, _, found, err := chat_repo.FindSubagentStateInBlocksJSON("", "tu1")
		require.NoError(t, err)
		assert.False(t, found)
	})

	t.Run("非法 JSON 返回 error", func(t *testing.T) {
		_, _, found, err := chat_repo.FindSubagentStateInBlocksJSON("{not json", "tu1")
		require.Error(t, err)
		assert.False(t, found)
	})
}

// TestFlipSubagentInBlocksJSON 直接单测 JSON 改写核心:翻转命中块的 status,其余字段
// (含 total_tokens/duration_ms/tool_uses 数字 + nested_tool_call_ids 数组)字节级保留,
// 防 float64 强转把整数写成 1e+03 之类。
func TestFlipSubagentInBlocksJSON(t *testing.T) {
	// 一条 subagent_state(running,带数字 + 数组字段)+ 一条 text。
	const input = `[` +
		`{"type":"subagent_state","data":{"parent_tool_call_id":"tu1","kind":"local_bash","description":"sleep 20","total_tokens":12345,"duration_ms":6789,"status":"running","tool_uses":42,"nested_tool_call_ids":["n1","n2"]}},` +
		`{"type":"text","data":{"text":"hi"}}` +
		`]`

	t.Run("命中块翻转 status,其余字段全保留", func(t *testing.T) {
		out, flipped, err := chat_repo.FlipSubagentInBlocksJSON(input, "tu1", "completed", "")
		require.NoError(t, err)
		assert.True(t, flipped)

		inData := subagentData(t, input)
		outData := subagentData(t, out)

		// status 翻成 completed。
		assert.Equal(t, "completed", outData["status"])
		// 其余字段逐项保留(数字仍是整数语义,数组仍是数组)—— 删掉 status 后 deep-equal。
		delete(inData, "status")
		delete(outData, "status")
		assert.Equal(t, inData, outData)
		// 显式校验数字 / 数组没被破坏(json.Number 比较,排除 1e+04 之类科学计数)。
		assert.Equal(t, json.Number("12345"), outData["total_tokens"])
		assert.Equal(t, json.Number("6789"), outData["duration_ms"])
		assert.Equal(t, json.Number("42"), outData["tool_uses"])
		assert.Equal(t, []any{"n1", "n2"}, outData["nested_tool_call_ids"])
		// 非命中块(text)原样保留。
		assert.Contains(t, out, `{"type":"text","data":{"text":"hi"}}`)
	})

	t.Run("非空 summary 同时写入", func(t *testing.T) {
		out, flipped, err := chat_repo.FlipSubagentInBlocksJSON(input, "tu1", "completed", "Background command completed")
		require.NoError(t, err)
		assert.True(t, flipped)
		outData := subagentData(t, out)
		assert.Equal(t, "completed", outData["status"])
		assert.Equal(t, "Background command completed", outData["summary"])
		// 其余字段(数字/数组)未被破坏。
		assert.Equal(t, json.Number("12345"), outData["total_tokens"])
	})

	t.Run("无命中块返回 false 且 JSON 不变", func(t *testing.T) {
		out, flipped, err := chat_repo.FlipSubagentInBlocksJSON(input, "tu-missing", "completed", "")
		require.NoError(t, err)
		assert.False(t, flipped)
		assert.Equal(t, input, out)
	})

	t.Run("空 JSON 返回 false 不报错", func(t *testing.T) {
		out, flipped, err := chat_repo.FlipSubagentInBlocksJSON("", "tu1", "completed", "")
		require.NoError(t, err)
		assert.False(t, flipped)
		assert.Equal(t, "", out)
	})

	t.Run("非法 JSON 返回 error", func(t *testing.T) {
		_, flipped, err := chat_repo.FlipSubagentInBlocksJSON("{not json", "tu1", "completed", "")
		require.Error(t, err)
		assert.False(t, flipped)
	})
}

// subagentData 解出 blocksJSON 里第一个 subagent_state 块的 data map(数字按
// json.Number 保留,以便检测整数是否被破坏)。
func subagentData(t *testing.T, blocksJSON string) map[string]any {
	t.Helper()
	var stored []struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(blocksJSON), &stored))
	for _, sb := range stored {
		if sb.Type != "subagent_state" {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(sb.Data))
		dec.UseNumber()
		var data map[string]any
		require.NoError(t, dec.Decode(&data))
		return data
	}
	t.Fatalf("no subagent_state block in %s", blocksJSON)
	return nil
}

func TestMessageRepo_List(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? ORDER BY seq ASC").
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(1, 3, "user", `[]`, 1).
			AddRow(2, 3, "assistant", `[]`, 2))

	got, err := chat_repo.NewMessage().List(ctx, 3)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "user", got[0].Role)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_NextSeq(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(seq\\), 0\\) \\+ 1 FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(5))

	got, err := chat_repo.NewMessage().NextSeq(ctx, 3)
	assert.NoError(t, err)
	assert.Equal(t, 5, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_Create(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `chat_messages`").
		WithArgs(
			int64(3), "", "user", "[]", "",
			0, 0, 0, 0, 0, 0, 0,
			0, 0.0, // first_token_ms, tokens_per_sec
			"", "", 1,
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectCommit()

	m := &chat_entity.Message{SessionID: 3, Role: "user", BlocksJSON: "[]", Seq: 1}
	err := chat_repo.NewMessage().Create(ctx, m)
	assert.NoError(t, err)
	assert.Equal(t, int64(42), m.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_CreateReplacementStage(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `chat_messages`").
		WithArgs(
			chat_repo.ReplacementStageSessionID(3), "", "user", "[]", "",
			0, 0, 0, 0, 0, 0, 0,
			0, 0.0, // first_token_ms, tokens_per_sec
			"", "", 5,
			sqlmock.AnyArg(), sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(52, 1))
	mock.ExpectCommit()

	message := &chat_entity.Message{
		SessionID:  chat_repo.ReplacementStageSessionID(3),
		Role:       "user",
		BlocksJSON: "[]",
		Seq:        5,
	}
	err := chat_repo.NewMessage().Create(ctx, message)
	require.NoError(t, err)
	assert.Equal(t, int64(52), message.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_Find(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE id = \\? ORDER BY `chat_messages`.`id` LIMIT \\?").
		WithArgs(int64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(42, 3, "assistant", `[]`, 4))

	got, err := chat_repo.NewMessage().Find(ctx, 42)
	assert.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, int64(42), got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_Find_NotFound(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE id = \\? ORDER BY `chat_messages`.`id` LIMIT \\?").
		WithArgs(int64(99), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	got, err := chat_repo.NewMessage().Find(ctx, 99)
	assert.NoError(t, err)
	assert.Nil(t, got, "missing row 应返回 nil 而不是 ErrRecordNotFound")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_DeleteFromSeq(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(3), 5).
		WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectCommit()

	deleted, err := chat_repo.NewMessage().DeleteFromSeq(ctx, 3, 5)
	assert.NoError(t, err)
	assert.Equal(t, int64(4), deleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestReplacementRecoveryMarkerRoundTripPreservesExactOwnership(t *testing.T) {
	recovery := &chat_repo.ReplacementRecovery{
		SessionID:            3,
		FromSeq:              5,
		RequestMessageID:     41,
		UserMessageID:        51,
		AssistantMessageID:   52,
		OldProviderSessionID: "pi-old",
		NewProviderSessionID: "pi-new",
		OldAgentStatus:       "error",
		OldLastMessageAt:     1234,
		State:                chat_repo.ReplacementRecoveryPending,
	}
	marker, err := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, err)
	marker.ID = 77
	marker.SessionID, err = chat_repo.ReplacementRecoverySessionID(marker.ID)
	require.NoError(t, err)

	decoded, err := chat_repo.ParseReplacementRecoveryMarker(marker)
	require.NoError(t, err)
	assert.Equal(t, marker.ID, decoded.MarkerID)
	assert.Equal(t, marker.SessionID, decoded.RecoverySessionID)
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

func TestReplacementRecoverySessionIDIsolatesGenerationsAndVisibleSessions(t *testing.T) {
	first, err := chat_repo.ReplacementRecoverySessionID(100)
	require.NoError(t, err)
	second, err := chat_repo.ReplacementRecoverySessionID(101)
	require.NoError(t, err)

	assert.Negative(t, first)
	assert.Negative(t, second)
	assert.NotEqual(t, first, second, "each prepared generation must own a distinct hidden namespace")
	assert.NotEqual(t, int64(100), first, "hidden recovery rows must not share a visible session namespace")
	assert.NotEqual(t, chat_repo.ReplacementStageSessionID(100), first,
		"the recovery namespace must not collide with the legacy per-session stage")
}

func TestMessageRepo_RecoveryNamespaceCollisionFailsClosed(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM `chat_messages` WHERE session_id = \\?").
		WithArgs(int64(-201)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	err := chat_repo.EnsureReplacementRecoveryNamespaceAvailable(ctx, -201)
	require.ErrorIs(t, err, chat_repo.ErrReplacementNamespaceCollision)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_MoveMessagesFromSeqPreservesOriginalRows(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectExec("UPDATE `chat_messages` SET `session_id`=\\? WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(-201), int64(3), 5).
		WillReturnResult(sqlmock.NewResult(0, 4))

	moved, err := chat_repo.MoveMessagesFromSeq(ctx, 3, -201, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(4), moved)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_DeleteOwnedReplacementMessagesUsesExactGenerationIDs(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND id IN \\(\\?,\\?\\)").
		WithArgs(int64(3), int64(51), int64(52)).
		WillReturnResult(sqlmock.NewResult(0, 2))

	deleted, err := chat_repo.DeleteOwnedReplacementMessages(ctx, 3, 51, 52)
	require.NoError(t, err)
	assert.Equal(t, int64(2), deleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindReplacementRecoveryUsesHiddenOwnership(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	recovery := &chat_repo.ReplacementRecovery{
		SessionID:            3,
		FromSeq:              5,
		RequestMessageID:     41,
		UserMessageID:        51,
		AssistantMessageID:   52,
		OldProviderSessionID: "pi-old",
		NewProviderSessionID: "pi-new",
		OldAgentStatus:       "idle",
		OldLastMessageAt:     1234,
		State:                chat_repo.ReplacementRecoveryPending,
	}
	marker, err := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, err)
	marker.ID = 77
	marker.SessionID, err = chat_repo.ReplacementRecoverySessionID(marker.ID)
	require.NoError(t, err)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? ORDER BY id ASC").
		WithArgs(marker.SessionID, marker.Role).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "session_id", "device_id", "role", "blocks_json", "model", "seq",
		}).AddRow(marker.ID, marker.SessionID, marker.DeviceID, marker.Role, marker.BlocksJSON, marker.Model, marker.Seq))

	got, err := chat_repo.FindReplacementRecovery(ctx, marker.SessionID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, recovery.UserMessageID, got.UserMessageID)
	assert.Equal(t, recovery.AssistantMessageID, got.AssistantMessageID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindReplacementRecoveryForSessionUsesExactHiddenOwnership(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	recovery := &chat_repo.ReplacementRecovery{
		SessionID:            3,
		FromSeq:              5,
		RequestMessageID:     41,
		UserMessageID:        51,
		AssistantMessageID:   52,
		OldProviderSessionID: "pi-old",
		NewProviderSessionID: "pi-new",
		OldAgentStatus:       "idle",
		State:                chat_repo.ReplacementRecoveryPending,
	}
	marker, err := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, err)
	marker.ID = 77
	marker.SessionID, err = chat_repo.ReplacementRecoverySessionID(marker.ID)
	require.NoError(t, err)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE role = \\? AND device_id = \\? ORDER BY id ASC").
		WithArgs(marker.Role, "3").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "session_id", "device_id", "role", "blocks_json", "model", "seq",
		}).AddRow(marker.ID, marker.SessionID, marker.DeviceID, marker.Role, marker.BlocksJSON, marker.Model, marker.Seq))

	got, err := chat_repo.FindReplacementRecoveryForSession(ctx, 3)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, recovery.UserMessageID, got.UserMessageID)
	assert.Equal(t, recovery.NewProviderSessionID, got.NewProviderSessionID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindReplacementRecoveryForSessionRejectsOverlappingMarkers(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	first := &chat_repo.ReplacementRecovery{
		SessionID: 3, FromSeq: 5, RequestMessageID: 41, UserMessageID: 51, AssistantMessageID: 52,
		NewProviderSessionID: "pi-new-1", State: chat_repo.ReplacementRecoveryPending,
	}
	second := &chat_repo.ReplacementRecovery{
		SessionID: 3, FromSeq: 7, RequestMessageID: 61, UserMessageID: 71, AssistantMessageID: 72,
		NewProviderSessionID: "pi-new-2", State: chat_repo.ReplacementRecoveryPending,
	}
	firstMarker, err := chat_repo.NewReplacementRecoveryMarker(first)
	require.NoError(t, err)
	firstMarker.ID = 77
	firstMarker.SessionID, err = chat_repo.ReplacementRecoverySessionID(firstMarker.ID)
	require.NoError(t, err)
	secondMarker, err := chat_repo.NewReplacementRecoveryMarker(second)
	require.NoError(t, err)
	secondMarker.ID = 78
	secondMarker.SessionID, err = chat_repo.ReplacementRecoverySessionID(secondMarker.ID)
	require.NoError(t, err)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE role = \\? AND device_id = \\? ORDER BY id ASC").
		WithArgs(firstMarker.Role, "3").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "session_id", "device_id", "role", "blocks_json", "model", "seq",
		}).
			AddRow(firstMarker.ID, firstMarker.SessionID, firstMarker.DeviceID, firstMarker.Role, firstMarker.BlocksJSON, firstMarker.Model, firstMarker.Seq).
			AddRow(secondMarker.ID, secondMarker.SessionID, secondMarker.DeviceID, secondMarker.Role, secondMarker.BlocksJSON, secondMarker.Model, secondMarker.Seq))

	got, err := chat_repo.FindReplacementRecoveryForSession(ctx, 3)
	require.Error(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindReplacementRecoveryForActiveMessageUsesExactOwnership(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	recovery := &chat_repo.ReplacementRecovery{
		SessionID:            3,
		FromSeq:              5,
		RequestMessageID:     41,
		UserMessageID:        51,
		AssistantMessageID:   52,
		OldProviderSessionID: "pi-old",
		NewProviderSessionID: "pi-new",
		OldAgentStatus:       "idle",
		State:                chat_repo.ReplacementRecoveryPending,
	}
	marker, err := chat_repo.NewReplacementRecoveryMarker(recovery)
	require.NoError(t, err)
	marker.ID = 77
	marker.SessionID, err = chat_repo.ReplacementRecoverySessionID(marker.ID)
	require.NoError(t, err)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE role = \\? AND device_id = \\? ORDER BY id ASC").
		WithArgs(marker.Role, "3").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "session_id", "device_id", "role", "blocks_json", "model", "seq",
		}).AddRow(marker.ID, marker.SessionID, marker.DeviceID, marker.Role, marker.BlocksJSON, marker.Model, marker.Seq))

	got, err := chat_repo.FindReplacementRecoveryForMessage(ctx, 3, 52)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, recovery.RequestMessageID, got.RequestMessageID)
	assert.Equal(t, recovery.AssistantMessageID, got.AssistantMessageID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_EnsureReplacementActiveTailOwnedUsesPersistedIDsAndSequence(t *testing.T) {
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

func TestMessageRepo_AcknowledgeAndCleanupReplacementAreIdempotent(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	recovery := &chat_repo.ReplacementRecovery{
		MarkerID:          77,
		RecoverySessionID: -155,
		State:             chat_repo.ReplacementRecoveryPending,
	}

	mock.ExpectExec("UPDATE `chat_messages` SET `model`=\\?,`updatetime`=\\? WHERE id = \\? AND session_id = \\? AND role = \\?").
		WithArgs(string(chat_repo.ReplacementRecoveryAcknowledged), sqlmock.AnyArg(), recovery.MarkerID, recovery.RecoverySessionID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, chat_repo.AcknowledgeReplacementRecovery(ctx, recovery))
	assert.Equal(t, chat_repo.ReplacementRecoveryAcknowledged, recovery.State)

	for _, affected := range []int64{3, 0} {
		mock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\?").
			WithArgs(recovery.RecoverySessionID).
			WillReturnResult(sqlmock.NewResult(0, affected))
		deleted, err := chat_repo.DeleteReplacementRecovery(ctx, recovery.RecoverySessionID)
		require.NoError(t, err)
		assert.Equal(t, affected, deleted)
	}
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_AcknowledgeReplacementRejectsMissingOwnedMarker(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	recovery := &chat_repo.ReplacementRecovery{
		MarkerID: 77, RecoverySessionID: -155, State: chat_repo.ReplacementRecoveryPending,
	}
	mock.ExpectExec("UPDATE `chat_messages` SET `model`=\\?,`updatetime`=\\? WHERE id = \\? AND session_id = \\? AND role = \\?").
		WithArgs(string(chat_repo.ReplacementRecoveryAcknowledged), sqlmock.AnyArg(), recovery.MarkerID, recovery.RecoverySessionID, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := chat_repo.AcknowledgeReplacementRecovery(ctx, recovery)
	require.ErrorIs(t, err, chat_repo.ErrReplacementOwnershipLost)
	assert.Equal(t, chat_repo.ReplacementRecoveryPending, recovery.State)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_RestoreReplacementSessionRejectsStaleGeneration(t *testing.T) {
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

func TestMessageRepo_FlipSubagentStatus_FlipsMatchingBlock(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	blocksJSON := `[{"type":"subagent_state","data":{"parent_tool_call_id":"tu1","kind":"local_bash","description":"sleep 20","status":"running"}}]`

	// 按 blocks_json LIKE toolUseID 定位发起消息(不受近因窗口限制)。
	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? AND blocks_json LIKE \\? ORDER BY seq DESC").
		WithArgs(int64(3), "assistant", "%tu1%").
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(42, 3, "assistant", blocksJSON, 4))

	// 命中后重写该条:status 翻成 completed。
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_messages` SET ").
		WithArgs(
			sqlmock.AnyArg(),                                                                         // session_id
			sqlmock.AnyArg(),                                                                         // device_id
			sqlmock.AnyArg(),                                                                         // role
			sqlmock.AnyArg(),                                                                         // blocks_json (翻转后)
			sqlmock.AnyArg(),                                                                         // model
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), // token 列
			sqlmock.AnyArg(),                   // total_input_tokens
			sqlmock.AnyArg(),                   // duration_ms
			sqlmock.AnyArg(), sqlmock.AnyArg(), // first_token_ms / tokens_per_sec
			sqlmock.AnyArg(),                   // fork_anchor
			sqlmock.AnyArg(),                   // error_text
			sqlmock.AnyArg(),                   // seq
			sqlmock.AnyArg(), sqlmock.AnyArg(), // createtime / updatetime
			int64(42), // WHERE id
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := chat_repo.NewMessage().FlipSubagentStatus(ctx, 3, "tu1", "completed", "Background command completed")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FlipSubagentStatus_NoMatchSilentNil(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	// 没有任何 subagent_state 命中 → 不写库,静默返回 nil。
	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? AND blocks_json LIKE \\? ORDER BY seq DESC").
		WithArgs(int64(3), "assistant", "%tu-missing%").
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(42, 3, "assistant", `[{"type":"text","data":{"text":"hi"}}]`, 4))

	err := chat_repo.NewMessage().FlipSubagentStatus(ctx, 3, "tu-missing", "completed", "")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_FlipSubagentStatus_FiltersByToolUseID 锁定 bug #2 修复:后台任务的
// 发起消息可能落在近 N 条 assistant 消息之外(长会话),旧实现 seq DESC LIMIT 50 盲扫会
// 漏掉它 → DB 永远卡 running。改为按 blocks_json LIKE toolUseID 定位那条消息,不受近因
// 窗口限制。此处断言查询带 blocks_json LIKE 过滤、且不再传 LIMIT 实参。
func TestMessageRepo_FlipSubagentStatus_FiltersByToolUseID(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? AND blocks_json LIKE \\? ORDER BY seq DESC").
		WithArgs(int64(3), "assistant", "%toolu_old%").
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(7, 3, "assistant", `[{"type":"text","data":{"text":"hi"}}]`, 1))

	// 命中的行不含 subagent_state → 不写库,静默返回 nil。核心断言是上面的查询形状。
	err := chat_repo.NewMessage().FlipSubagentStatus(ctx, 3, "toolu_old", "completed", "")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAppendSubagentChildrenInBlocksJSON 直接单测 JSON 改写核心:把子块追加进 subagent_state。
func TestAppendSubagentChildrenInBlocksJSON(t *testing.T) {
	const baseBlocks = `[` +
		`{"type":"tool_use","data":{"id":"toolu_agent","name":"Task","input":{"description":"run something"}}},` +
		`{"type":"subagent_state","data":{"parent_tool_call_id":"toolu_agent","kind":"local_bash","description":"run something","status":"running","nested_tool_call_ids":[]}}` +
		`]`

	childBlocks := `[` +
		`{"type":"tool_use","data":{"id":"sub_bash","name":"Bash","input":{"command":"ls"}}},` +
		`{"type":"tool_result","data":{"id":"sub_bash","content":"file1.txt"}}` +
		`]`

	t.Run("追加子块并更新 nested_tool_call_ids", func(t *testing.T) {
		out, ok, err := chat_repo.AppendSubagentChildrenInBlocksJSON(baseBlocks, "toolu_agent", childBlocks, []string{"sub_bash"})
		require.NoError(t, err)
		assert.True(t, ok)
		// nested_tool_call_ids 应包含 sub_bash。
		data := subagentData(t, out)
		ids, _ := data["nested_tool_call_ids"].([]any)
		assert.Equal(t, []any{"sub_bash"}, ids)
		// 子块被追加到末尾。
		assert.Contains(t, out, `"sub_bash"`)
		assert.Contains(t, out, `"Bash"`)
		assert.Contains(t, out, `"tool_result"`)
		// 原有块仍在。
		assert.Contains(t, out, `"tool_use"`)
		assert.Contains(t, out, `"toolu_agent"`)
	})

	t.Run("childIDs 去重", func(t *testing.T) {
		// nested_tool_call_ids 已有 existing_id。
		withExisting := `[{"type":"subagent_state","data":{"parent_tool_call_id":"toolu_agent","status":"running","nested_tool_call_ids":["existing_id"]}}]`
		out, ok, err := chat_repo.AppendSubagentChildrenInBlocksJSON(withExisting, "toolu_agent", `[]`, []string{"existing_id", "new_id"})
		require.NoError(t, err)
		assert.True(t, ok)
		data := subagentData(t, out)
		ids, _ := data["nested_tool_call_ids"].([]any)
		// existing_id 不重复,new_id 补进去。
		assert.Equal(t, []any{"existing_id", "new_id"}, ids)
	})

	t.Run("无命中返回 false", func(t *testing.T) {
		out, ok, err := chat_repo.AppendSubagentChildrenInBlocksJSON(baseBlocks, "toolu_missing", childBlocks, []string{"sub_bash"})
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Equal(t, baseBlocks, out)
	})

	t.Run("空 blocksJSON 返回 false", func(t *testing.T) {
		out, ok, err := chat_repo.AppendSubagentChildrenInBlocksJSON("", "toolu_agent", childBlocks, []string{"sub_bash"})
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Equal(t, "", out)
	})

	t.Run("非法 blocksJSON 返回 error", func(t *testing.T) {
		_, ok, err := chat_repo.AppendSubagentChildrenInBlocksJSON("{not json", "toolu_agent", childBlocks, []string{"sub_bash"})
		require.Error(t, err)
		assert.False(t, ok)
	})
}

// TestFlipAndAppendCompose 证明两个纯 JSON 改写 helper 在任意执行顺序下可以安全组合:
// Flip(Append(...)) 和 Append(Flip(...)) 产出的结果都同时包含嵌套子块和 completed 状态。
// 这是 per-session mutex 序列化并发写的正确性依据 —— 先后无关,两个路径不互相覆写对方的字段。
func TestFlipAndAppendCompose(t *testing.T) {
	// 基础 blocks_json:一个空 subagent_state(running,nested_tool_call_ids 为空)。
	const base = `[` +
		`{"type":"subagent_state","data":{"parent_tool_call_id":"toolu_agent","kind":"local_bash","description":"run something","status":"running","nested_tool_call_ids":[]}}` +
		`]`

	nestedBlock := `[{"type":"tool_use","data":{"id":"sub_bash","name":"Bash","input":{"command":"ls"}}}]`

	assertBothPresent := func(t *testing.T, result string) {
		t.Helper()
		data := subagentData(t, result)
		// Flip 的效果:status == "completed"。
		assert.Equal(t, "completed", data["status"])
		// Append 的效果:nested_tool_call_ids 包含 "sub_bash"。
		ids, _ := data["nested_tool_call_ids"].([]any)
		assert.Contains(t, ids, "sub_bash")
		// 子块被追加到顶层数组末尾。
		assert.Contains(t, result, `"sub_bash"`)
	}

	t.Run("Flip-then-Append", func(t *testing.T) {
		// 先 Flip(status running→completed),再 Append(追加子块)。
		flipped, _, err := chat_repo.FlipSubagentInBlocksJSON(base, "toolu_agent", "completed", "done")
		require.NoError(t, err)
		result, _, err := chat_repo.AppendSubagentChildrenInBlocksJSON(flipped, "toolu_agent", nestedBlock, []string{"sub_bash"})
		require.NoError(t, err)
		assertBothPresent(t, result)
	})

	t.Run("Append-then-Flip", func(t *testing.T) {
		// 先 Append(追加子块),再 Flip(status running→completed)。
		appended, _, err := chat_repo.AppendSubagentChildrenInBlocksJSON(base, "toolu_agent", nestedBlock, []string{"sub_bash"})
		require.NoError(t, err)
		result, _, err := chat_repo.FlipSubagentInBlocksJSON(appended, "toolu_agent", "completed", "done")
		require.NoError(t, err)
		assertBothPresent(t, result)
	})
}

func TestMessageRepo_AppendSubagentChildren_AppendsBlocks(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	blocksJSON := `[` +
		`{"type":"tool_use","data":{"id":"toolu_agent","name":"Task","input":{"description":"run something"}}},` +
		`{"type":"subagent_state","data":{"parent_tool_call_id":"toolu_agent","kind":"local_bash","description":"run something","status":"running","nested_tool_call_ids":[]}}` +
		`]`
	childBlocksJSON := `[{"type":"tool_use","data":{"id":"sub_bash","name":"Bash","input":{"command":"ls"}}}]`

	// 倒序拉近 N 条 assistant 消息。
	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? ORDER BY seq DESC LIMIT \\?").
		WithArgs(int64(3), "assistant", 50).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(42, 3, "assistant", blocksJSON, 4))

	// 命中后重写该条。blocks_json 参数必须包含追加的子块 id("sub_bash")和子块
	// 的 name("Bash"),以确保方法不会把原始(未重写)的 JSON 传给 Update。
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_messages` SET ").
		WithArgs(
			sqlmock.AnyArg(), // session_id
			sqlmock.AnyArg(), // device_id
			sqlmock.AnyArg(), // role
			blocksJSONContainsMatcher{substrings: []string{"sub_bash", "\"Bash\""}}, // blocks_json (追加后,含子块 id 及子块 name)
			sqlmock.AnyArg(),                                                                         // model
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), // token 列
			sqlmock.AnyArg(),                   // total_input_tokens
			sqlmock.AnyArg(),                   // duration_ms
			sqlmock.AnyArg(), sqlmock.AnyArg(), // first_token_ms / tokens_per_sec
			sqlmock.AnyArg(),                   // fork_anchor
			sqlmock.AnyArg(),                   // error_text
			sqlmock.AnyArg(),                   // seq
			sqlmock.AnyArg(), sqlmock.AnyArg(), // createtime / updatetime
			int64(42), // WHERE id
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := chat_repo.NewMessage().AppendSubagentChildren(ctx, 3, "toolu_agent", childBlocksJSON, []string{"sub_bash"})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_AppendSubagentChildren_NoMatchSilentNil(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	// 没有 subagent_state 命中 → 不写库,静默返回 nil。
	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? ORDER BY seq DESC LIMIT \\?").
		WithArgs(int64(3), "assistant", 50).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(42, 3, "assistant", `[{"type":"text","data":{"text":"hi"}}]`, 4))

	err := chat_repo.NewMessage().AppendSubagentChildren(ctx, 3, "toolu_missing", `[{"type":"tool_use","data":{}}]`, []string{"x"})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindAssistantBySubagentToolUseID_ReturnsMatch(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	matchBlocks := `[{"type":"subagent_state","data":{"parent_tool_call_id":"toolu_agent","kind":"local_agent","description":"do work","status":"running"}}]`
	otherBlocks := `[{"type":"text","data":{"text":"hi"}}]`

	// 倒序拉近 N 条 assistant 消息;返回第一条 blocks 含命中 subagent_state 的。
	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? ORDER BY seq DESC LIMIT \\?").
		WithArgs(int64(3), "assistant", 50).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(43, 3, "assistant", otherBlocks, 5).
			AddRow(42, 3, "assistant", matchBlocks, 4))

	got, err := chat_repo.NewMessage().FindAssistantBySubagentToolUseID(ctx, 3, "toolu_agent")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(42), got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindAssistantBySubagentToolUseID_NoMatchNil(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? ORDER BY seq DESC LIMIT \\?").
		WithArgs(int64(3), "assistant", 50).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(42, 3, "assistant", `[{"type":"text","data":{"text":"hi"}}]`, 4))

	got, err := chat_repo.NewMessage().FindAssistantBySubagentToolUseID(ctx, 3, "toolu_missing")
	require.NoError(t, err)
	assert.Nil(t, got, "无命中 subagent_state 应返回 (nil, nil)")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindAssistantBySubagentToolUseID_EmptyToolUseID(t *testing.T) {
	ctx, _, _ := testutils.Database(t)

	got, err := chat_repo.NewMessage().FindAssistantBySubagentToolUseID(ctx, 3, "")
	require.NoError(t, err)
	assert.Nil(t, got, "空 toolUseID 短路返回 (nil, nil),不查库")
}

func TestMessageRepo_LatestAssistant(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	// gorm appends `,`chat_messages`.`id`` to ORDER BY when using First(); regex adjusted to match.
	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? ORDER BY seq DESC,`chat_messages`.`id` LIMIT \\?").
		WithArgs(int64(7), "assistant", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "seq"}).
			AddRow(42, 7, "assistant", 9))
	got, err := chat_repo.NewMessage().LatestAssistant(ctx, 7)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(42), got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_LatestAssistant_None(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	// gorm appends `,`chat_messages`.`id`` to ORDER BY when using First(); regex adjusted to match.
	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? ORDER BY seq DESC,`chat_messages`.`id` LIMIT \\?").
		WithArgs(int64(7), "assistant", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	got, err := chat_repo.NewMessage().LatestAssistant(ctx, 7)
	require.NoError(t, err)
	assert.Nil(t, got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_Update(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_messages` SET ").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	m := &chat_entity.Message{ID: 42, SessionID: 3, Role: "assistant", BlocksJSON: `[{"type":"text"}]`, Seq: 2}
	err := chat_repo.NewMessage().Update(ctx, m)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestPatchSubagentProgressInBlocksJSON 单测进度快照的就地改写:后台 subagent 在会话
// **空闲态**跑的那段时间,CLI 持续吐 task_progress,但这些进度只能靠定向 patch 落回发起
// 消息(per-turn accumulator 已经收尾)。零值字段不覆盖已有值 —— 缺 usage 的帧不该把
// 已经攒起来的工具数 / token 抹回 0。
func TestPatchSubagentProgressInBlocksJSON(t *testing.T) {
	const baseBlocks = `[` +
		`{"type":"tool_use","data":{"id":"toolu_agent","name":"Agent","input":{"description":"T7"}}},` +
		`{"type":"subagent_state","data":{"parent_tool_call_id":"toolu_agent","kind":"local_agent","description":"T7","status":"running","total_tokens":84739,"tool_uses":9,"last_tool_name":"Read","nested_tool_call_ids":["n1"]}}` +
		`]`

	t.Run("命中块的进度字段被更新", func(t *testing.T) {
		out, ok, err := chat_repo.PatchSubagentProgressInBlocksJSON(baseBlocks, "toolu_agent", chat_repo.SubagentProgress{
			TotalTokens: 132480, ToolUses: 21, DurationMs: 754000, LastToolName: "Edit",
		})
		require.NoError(t, err)
		assert.True(t, ok)
		data := subagentData(t, out)
		assert.Equal(t, json.Number("132480"), data["total_tokens"])
		assert.Equal(t, json.Number("21"), data["tool_uses"])
		assert.Equal(t, json.Number("754000"), data["duration_ms"])
		assert.Equal(t, "Edit", data["last_tool_name"])
		// 非进度字段原样保留。
		assert.Equal(t, "running", data["status"])
		assert.Equal(t, "local_agent", data["kind"])
		assert.Equal(t, []any{"n1"}, data["nested_tool_call_ids"])
	})

	t.Run("零值字段不覆盖已有值", func(t *testing.T) {
		out, ok, err := chat_repo.PatchSubagentProgressInBlocksJSON(baseBlocks, "toolu_agent", chat_repo.SubagentProgress{
			ToolUses: 12, // 只有工具数是新的,其余零值
		})
		require.NoError(t, err)
		assert.True(t, ok)
		data := subagentData(t, out)
		assert.Equal(t, json.Number("12"), data["tool_uses"])
		assert.Equal(t, json.Number("84739"), data["total_tokens"], "零值 TotalTokens 不该抹掉已有值")
		assert.Equal(t, "Read", data["last_tool_name"], "零值 LastToolName 不该抹掉已有值")
	})

	t.Run("全零进度不改写", func(t *testing.T) {
		out, ok, err := chat_repo.PatchSubagentProgressInBlocksJSON(baseBlocks, "toolu_agent", chat_repo.SubagentProgress{})
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Equal(t, baseBlocks, out)
	})

	t.Run("无命中返回 false", func(t *testing.T) {
		out, ok, err := chat_repo.PatchSubagentProgressInBlocksJSON(baseBlocks, "toolu_missing", chat_repo.SubagentProgress{ToolUses: 3})
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Equal(t, baseBlocks, out)
	})

	t.Run("非法 blocksJSON 返回 error", func(t *testing.T) {
		_, ok, err := chat_repo.PatchSubagentProgressInBlocksJSON("{not json", "toolu_agent", chat_repo.SubagentProgress{ToolUses: 3})
		require.Error(t, err)
		assert.False(t, ok)
	})

	// 后台子代理跨轮写回模型(R6/A9):子代理在会话空闲活动轮解出实际模型,靠这条
	// 定向 patch 落回发起消息;其它字段(status/kind/进度数字)必须原样保留。
	t.Run("模型字段被写入且不影响其它字段", func(t *testing.T) {
		out, ok, err := chat_repo.PatchSubagentProgressInBlocksJSON(baseBlocks, "toolu_agent", chat_repo.SubagentProgress{
			Model: "claude-haiku-4-5-20251001",
		})
		require.NoError(t, err)
		assert.True(t, ok)
		data := subagentData(t, out)
		assert.Equal(t, "claude-haiku-4-5-20251001", data["model"])
		// 非模型字段原样保留。
		assert.Equal(t, json.Number("84739"), data["total_tokens"])
		assert.Equal(t, json.Number("9"), data["tool_uses"])
		assert.Equal(t, "Read", data["last_tool_name"])
		assert.Equal(t, "running", data["status"])
		assert.Equal(t, "local_agent", data["kind"])
	})

	t.Run("空模型不覆盖已记录模型", func(t *testing.T) {
		const blocksWithModel = `[` +
			`{"type":"tool_use","data":{"id":"toolu_agent","name":"Agent","input":{"description":"T7"}}},` +
			`{"type":"subagent_state","data":{"parent_tool_call_id":"toolu_agent","kind":"local_agent","status":"running","total_tokens":84739,"tool_uses":9,"model":"claude-opus-5"}}` +
			`]`
		out, ok, err := chat_repo.PatchSubagentProgressInBlocksJSON(blocksWithModel, "toolu_agent", chat_repo.SubagentProgress{
			ToolUses: 12, // 只有工具数更新,Model 留空(first-wins,已记录的模型不该被空值抹掉)
		})
		require.NoError(t, err)
		assert.True(t, ok)
		data := subagentData(t, out)
		assert.Equal(t, json.Number("12"), data["tool_uses"])
		assert.Equal(t, "claude-opus-5", data["model"], "空 Model 不该抹掉已记录模型")
	})
}

// TestMessageRepo_PatchSubagentProgress_UpdatesMatchingBlock 走 repo:定位方式与
// FlipSubagentStatus 一致(blocks_json LIKE toolUseID,不受近因窗口限制),命中即重写该行。
func TestMessageRepo_PatchSubagentProgress_UpdatesMatchingBlock(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	blocksJSON := `[{"type":"subagent_state","data":{"parent_tool_call_id":"tu1","kind":"local_agent","status":"running","tool_uses":9}}]`

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? AND blocks_json LIKE \\? ORDER BY seq DESC").
		WithArgs(int64(3), "assistant", "%tu1%").
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(42, 3, "assistant", blocksJSON, 4))

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_messages` SET ").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := chat_repo.NewMessage().PatchSubagentProgress(ctx, 3, "tu1", chat_repo.SubagentProgress{ToolUses: 21, TotalTokens: 132480})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_PatchSubagentProgress_NoMatchSilentNil 无命中静默返回:任务可能已 evict。
func TestMessageRepo_PatchSubagentProgress_NoMatchSilentNil(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? AND blocks_json LIKE \\? ORDER BY seq DESC").
		WithArgs(int64(3), "assistant", "%tu-missing%").
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "blocks_json", "seq"}).
			AddRow(42, 3, "assistant", `[{"type":"text","data":{"text":"hi"}}]`, 4))

	err := chat_repo.NewMessage().PatchSubagentProgress(ctx, 3, "tu-missing", chat_repo.SubagentProgress{ToolUses: 3})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
