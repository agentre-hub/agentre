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

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// blockDataContains 是 sqlmock 自定义参数匹配器:校验写回块行的正文包含全部指定子串,
// 捕捉 AnyArg() 查不出的「未改写(原样回传)」缺陷。
type blockDataContains struct {
	substrings []string
}

func (m blockDataContains) Match(v driver.Value) bool {
	data, ok := v.([]byte)
	if !ok {
		return false
	}
	for _, sub := range m.substrings {
		if !strings.Contains(string(data), sub) {
			return false
		}
	}
	return true
}

// subagentBlockRows 造一行 subagent_state 块行(未压缩),供块级方法的点查用例返回。
func subagentBlockRows(messageID int64, toolCallID, data string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"message_id", "idx", "type", "tool_call_id", "codec", "data"}).
		AddRow(messageID, 0, "subagent_state", toolCallID, chat_entity.BlockCodecRaw, []byte(data))
}

// emptyBlockRows 是「定位不到目标块」的空结果集。
func emptyBlockRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"message_id", "idx", "type", "tool_call_id", "codec", "data"})
}

// blockData 把块正文解成 map(数字按 json.Number 保留,以便检测整数是否被破坏)。
func blockData(t *testing.T, data []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var decoded map[string]any
	require.NoError(t, dec.Decode(&decoded))
	return decoded
}

// TestSubagentStateFromBlockData 单测按块正文读出 task_id + status(供 StopBackgroundTask
// 定位 CLI task_id)。
func TestSubagentStateFromBlockData(t *testing.T) {
	const input = `{"parent_tool_call_id":"tu1","task_id":"b0n82mqaj","kind":"local_bash","status":"running"}`

	t.Run("读出 task_id + status", func(t *testing.T) {
		taskID, status, err := chat_repo.SubagentStateFromBlockData([]byte(input))
		require.NoError(t, err)
		assert.Equal(t, "b0n82mqaj", taskID)
		assert.Equal(t, "running", status)
	})

	t.Run("旧块无 task_id:taskID 空但 status 仍读得出", func(t *testing.T) {
		const legacy = `{"parent_tool_call_id":"tu1","kind":"local_bash","status":"running"}`
		taskID, status, err := chat_repo.SubagentStateFromBlockData([]byte(legacy))
		require.NoError(t, err)
		assert.Equal(t, "", taskID)
		assert.Equal(t, "running", status)
	})

	t.Run("非法 JSON 返回 error", func(t *testing.T) {
		_, _, err := chat_repo.SubagentStateFromBlockData([]byte("{not json"))
		assert.Error(t, err)
	})
}

func TestFlipSubagentInBlockData(t *testing.T) {
	// 一条 subagent_state(running,带数字 + 数组字段)。
	const input = `{"parent_tool_call_id":"tu1","kind":"local_bash","description":"sleep 20","total_tokens":12345,"duration_ms":6789,"status":"running","tool_uses":42,"nested_tool_call_ids":["n1","n2"]}`

	t.Run("翻转 status,其余字段全保留", func(t *testing.T) {
		out, flipped, err := chat_repo.FlipSubagentInBlockData([]byte(input), "completed", "")
		require.NoError(t, err)
		assert.True(t, flipped)

		inData := blockData(t, []byte(input))
		outData := blockData(t, out)

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
	})

	t.Run("非空 summary 同时写入", func(t *testing.T) {
		out, flipped, err := chat_repo.FlipSubagentInBlockData([]byte(input), "completed", "Background command completed")
		require.NoError(t, err)
		assert.True(t, flipped)
		outData := blockData(t, out)
		assert.Equal(t, "completed", outData["status"])
		assert.Equal(t, "Background command completed", outData["summary"])
		assert.Equal(t, json.Number("12345"), outData["total_tokens"])
	})

	t.Run("非法正文返回 error", func(t *testing.T) {
		_, flipped, err := chat_repo.FlipSubagentInBlockData([]byte("{not json"), "completed", "")
		require.Error(t, err)
		assert.False(t, flipped)
	})
}

// TestAppendNestedToolCallIDsInBlockData 单测子块 id 的去重追加。
func TestAppendNestedToolCallIDsInBlockData(t *testing.T) {
	t.Run("追加进空数组", func(t *testing.T) {
		const base = `{"parent_tool_call_id":"toolu_agent","status":"running","nested_tool_call_ids":[]}`
		out, ok, err := chat_repo.AppendNestedToolCallIDsInBlockData([]byte(base), []string{"sub_bash"})
		require.NoError(t, err)
		assert.True(t, ok)
		ids, _ := blockData(t, out)["nested_tool_call_ids"].([]any)
		assert.Equal(t, []any{"sub_bash"}, ids)
	})

	t.Run("childIDs 去重", func(t *testing.T) {
		const withExisting = `{"parent_tool_call_id":"toolu_agent","status":"running","nested_tool_call_ids":["existing_id"]}`
		out, ok, err := chat_repo.AppendNestedToolCallIDsInBlockData([]byte(withExisting), []string{"existing_id", "new_id"})
		require.NoError(t, err)
		assert.True(t, ok)
		ids, _ := blockData(t, out)["nested_tool_call_ids"].([]any)
		assert.Equal(t, []any{"existing_id", "new_id"}, ids)
	})

	t.Run("非法正文返回 error", func(t *testing.T) {
		_, ok, err := chat_repo.AppendNestedToolCallIDsInBlockData([]byte("{not json"), []string{"x"})
		require.Error(t, err)
		assert.False(t, ok)
	})
}

// TestFlipAndAppendCompose 证明两个纯改写 helper 在任意执行顺序下可以安全组合:
// Flip(Append(...)) 和 Append(Flip(...)) 产出的结果都同时带 completed 状态和嵌套子块 id。
// 这是 per-session mutex 序列化并发写的正确性依据 —— 先后无关,两个路径不互相覆写对方的字段。
func TestFlipAndAppendCompose(t *testing.T) {
	const base = `{"parent_tool_call_id":"toolu_agent","kind":"local_bash","description":"run something","status":"running","nested_tool_call_ids":[]}`

	assertBothPresent := func(t *testing.T, out []byte) {
		t.Helper()
		data := blockData(t, out)
		assert.Equal(t, "completed", data["status"])
		ids, _ := data["nested_tool_call_ids"].([]any)
		assert.Contains(t, ids, "sub_bash")
	}

	t.Run("Flip-then-Append", func(t *testing.T) {
		flipped, _, err := chat_repo.FlipSubagentInBlockData([]byte(base), "completed", "done")
		require.NoError(t, err)
		result, _, err := chat_repo.AppendNestedToolCallIDsInBlockData(flipped, []string{"sub_bash"})
		require.NoError(t, err)
		assertBothPresent(t, result)
	})

	t.Run("Append-then-Flip", func(t *testing.T) {
		appended, _, err := chat_repo.AppendNestedToolCallIDsInBlockData([]byte(base), []string{"sub_bash"})
		require.NoError(t, err)
		result, _, err := chat_repo.FlipSubagentInBlockData(appended, "completed", "done")
		require.NoError(t, err)
		assertBothPresent(t, result)
	})
}

func TestMessageRepo_List(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? ORDER BY seq ASC").
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "seq"}).
			AddRow(1, 3, "user", 1).
			AddRow(2, 3, "assistant", 2))
	mock.ExpectQuery("SELECT \\* FROM `chat_message_blocks` WHERE message_id IN \\(\\?,\\?\\)").
		WithArgs(int64(1), int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"message_id", "idx", "type", "tool_call_id", "codec", "data"}).
			AddRow(1, 0, "text", "", chat_entity.BlockCodecRaw, []byte(`{"text":"hi"}`)))

	got, err := chat_repo.NewMessage().List(ctx, 3)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "user", got[0].Role)
	assert.Equal(t, `[{"type":"text","data":{"text":"hi"}}]`, got[0].BlocksJSON)
	assert.Equal(t, "[]", got[1].BlocksJSON, "没有块行的消息拿到空正文而不是空串")
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
			int64(3), "", "user", "",
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
			chat_repo.ReplacementStageSessionID(3), "", "user", "",
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
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "seq"}).
			AddRow(42, 3, "assistant", 4))
	mock.ExpectQuery("SELECT \\* FROM `chat_message_blocks` WHERE message_id IN \\(\\?\\)").
		WithArgs(int64(42)).
		WillReturnRows(emptyBlockRows())

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

// expectSubagentBlockLookup 是块级方法共用的定位期望:按 (tool_call_id, type) 索引点查,
// 不再对整列正文做 LIKE 全扫。
//
// WHERE 里必须带 `tool_call_id` <> ”。定位索引是**部分索引**
// (idx_chat_message_blocks_tool_call ... WHERE tool_call_id != ”),而 SQLite 只在能
// 证明查询蕴含索引的 WHERE 子句时才肯用它 —— `tool_call_id = ?` 里的绑定变量证不出
// `? != ”`,少了这一句,EXPLAIN QUERY PLAN 走的是全表串扫(只有 UNIQUE 索引时)或
// 按 type 扫遍全库的 subagent_state 块(补了 (type, message_id) 索引之后),正是本轮
// 要消灭的那个形态。它对结果集没有影响 —— 调用方在 toolCallID 为空时就已经返回了。
func expectSubagentBlockLookup(mock sqlmock.Sqlmock, sessionID int64, toolCallID string, rows *sqlmock.Rows) {
	mock.ExpectQuery("SELECT .* FROM `chat_message_blocks` JOIN `chat_messages`.*`chat_message_blocks`.`tool_call_id` <> ''").
		WithArgs(sessionID, "subagent_state", toolCallID, 1).
		WillReturnRows(rows)
}

func TestMessageRepo_FlipSubagentStatus_FlipsMatchingBlock(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	expectSubagentBlockLookup(mock, 3, "tu1", subagentBlockRows(42, "tu1",
		`{"parent_tool_call_id":"tu1","kind":"local_bash","description":"sleep 20","status":"running"}`))
	// 命中后只重写那一个块行,不再读出并重写宿主消息的全部块。
	mock.ExpectExec("UPDATE `chat_message_blocks` SET `codec`=\\?,`data`=\\? WHERE message_id = \\? AND idx = \\?").
		WithArgs(chat_entity.BlockCodecRaw, blockDataContains{substrings: []string{
			`"status":"completed"`, `"summary":"Background command completed"`, `"description":"sleep 20"`,
		}}, int64(42), 0).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := chat_repo.NewMessage().FlipSubagentStatus(ctx, 3, "tu1", "completed", "Background command completed")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FlipSubagentStatus_NoMatchSilentNil(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	// 定位不到目标块 → 不写库,静默返回 nil(任务可能已 evict / 非本会话)。
	expectSubagentBlockLookup(mock, 3, "tu-missing", emptyBlockRows())

	err := chat_repo.NewMessage().FlipSubagentStatus(ctx, 3, "tu-missing", "completed", "")
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FlipSubagentStatus_EmptyArgsShortCircuit(t *testing.T) {
	ctx, _, _ := testutils.Database(t)

	require.NoError(t, chat_repo.NewMessage().FlipSubagentStatus(ctx, 3, "", "completed", ""))
	require.NoError(t, chat_repo.NewMessage().FlipSubagentStatus(ctx, 3, "tu1", "", ""))
}

func TestMessageRepo_AppendSubagentChildren_AppendsBlocks(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	childBlocksJSON := `[{"type":"tool_use","data":{"id":"sub_bash","name":"Bash","input":{"command":"ls"}}}]`

	expectSubagentBlockLookup(mock, 3, "toolu_agent", subagentBlockRows(42, "toolu_agent",
		`{"parent_tool_call_id":"toolu_agent","kind":"local_bash","status":"running","nested_tool_call_ids":[]}`))
	mock.ExpectExec("UPDATE `chat_message_blocks` SET `codec`=\\?,`data`=\\? WHERE message_id = \\? AND idx = \\?").
		WithArgs(chat_entity.BlockCodecRaw, blockDataContains{substrings: []string{`"sub_bash"`}}, int64(42), 0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// 子块作为新块行追加到该消息正文末尾,idx 从当前最大值往后排。
	mock.ExpectQuery("SELECT COALESCE\\(MAX\\(idx\\), -1\\) \\+ 1 FROM `chat_message_blocks` WHERE message_id = \\?").
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"next"}).AddRow(2))
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `chat_message_blocks`").
		WithArgs(int64(42), 2, "tool_use", "sub_bash", chat_entity.BlockCodecRaw, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := chat_repo.NewMessage().AppendSubagentChildren(ctx, 3, "toolu_agent", childBlocksJSON, []string{"sub_bash"})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_AppendSubagentChildren_NoMatchSilentNil(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	expectSubagentBlockLookup(mock, 3, "toolu_missing", emptyBlockRows())

	err := chat_repo.NewMessage().AppendSubagentChildren(ctx, 3, "toolu_missing", `[{"type":"tool_use","data":{}}]`, []string{"x"})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindAssistantBySubagentToolCallID_ReturnsMatch(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	expectSubagentBlockLookup(mock, 3, "toolu_agent", subagentBlockRows(42, "toolu_agent",
		`{"parent_tool_call_id":"toolu_agent","kind":"local_agent","status":"running"}`))
	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE id = \\? ORDER BY `chat_messages`.`id` LIMIT \\?").
		WithArgs(int64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "seq"}).
			AddRow(42, 3, "assistant", 4))
	mock.ExpectQuery("SELECT \\* FROM `chat_message_blocks` WHERE message_id IN \\(\\?\\)").
		WithArgs(int64(42)).
		WillReturnRows(emptyBlockRows())

	got, err := chat_repo.NewMessage().FindAssistantBySubagentToolCallID(ctx, 3, "toolu_agent")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(42), got.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindAssistantBySubagentToolCallID_NoMatchNil(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	expectSubagentBlockLookup(mock, 3, "toolu_missing", emptyBlockRows())

	got, err := chat_repo.NewMessage().FindAssistantBySubagentToolCallID(ctx, 3, "toolu_missing")
	require.NoError(t, err)
	assert.Nil(t, got, "无命中 subagent_state 应返回 (nil, nil)")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindAssistantBySubagentToolCallID_EmptyToolCallID(t *testing.T) {
	ctx, _, _ := testutils.Database(t)

	got, err := chat_repo.NewMessage().FindAssistantBySubagentToolCallID(ctx, 3, "")
	require.NoError(t, err)
	assert.Nil(t, got, "空 toolCallID 短路返回 (nil, nil),不查库")
}

func TestMessageRepo_FindSubagentState(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	expectSubagentBlockLookup(mock, 3, "tu1", subagentBlockRows(42, "tu1",
		`{"parent_tool_call_id":"tu1","task_id":"b0n82mqaj","status":"running"}`))

	taskID, status, found, err := chat_repo.NewMessage().FindSubagentState(ctx, 3, "tu1")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "b0n82mqaj", taskID)
	assert.Equal(t, "running", status)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_FindSubagentState_NoMatch(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	expectSubagentBlockLookup(mock, 3, "tu-missing", emptyBlockRows())

	_, _, found, err := chat_repo.NewMessage().FindSubagentState(ctx, 3, "tu-missing")
	require.NoError(t, err)
	assert.False(t, found)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_LatestAssistant(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	// gorm appends `,`chat_messages`.`id`` to ORDER BY when using First(); regex adjusted to match.
	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? AND role = \\? ORDER BY seq DESC,`chat_messages`.`id` LIMIT \\?").
		WithArgs(int64(7), "assistant", 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "seq"}).
			AddRow(42, 7, "assistant", 9))
	mock.ExpectQuery("SELECT \\* FROM `chat_message_blocks` WHERE message_id IN \\(\\?\\)").
		WithArgs(int64(42)).
		WillReturnRows(emptyBlockRows())
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

// TestPatchSubagentProgressInBlockData 单测进度快照的就地改写:后台 subagent 在会话
// **空闲态**跑的那段时间,CLI 持续吐 task_progress,但这些进度只能靠定向 patch 落回发起
// 消息(per-turn accumulator 已经收尾)。零值字段不覆盖已有值 —— 缺 usage 的帧不该把
// 已经攒起来的工具数 / token 抹回 0。
func TestPatchSubagentProgressInBlockData(t *testing.T) {
	const base = `{"parent_tool_call_id":"toolu_agent","kind":"local_agent","description":"T7","status":"running","total_tokens":84739,"tool_uses":9,"last_tool_name":"Read","nested_tool_call_ids":["n1"]}`

	t.Run("进度字段被更新", func(t *testing.T) {
		out, ok, err := chat_repo.PatchSubagentProgressInBlockData([]byte(base), chat_repo.SubagentProgress{
			TotalTokens: 132480, ToolUses: 21, DurationMs: 754000, LastToolName: "Edit",
		})
		require.NoError(t, err)
		assert.True(t, ok)
		data := blockData(t, out)
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
		out, ok, err := chat_repo.PatchSubagentProgressInBlockData([]byte(base), chat_repo.SubagentProgress{
			ToolUses: 12, // 只有工具数是新的,其余零值
		})
		require.NoError(t, err)
		assert.True(t, ok)
		data := blockData(t, out)
		assert.Equal(t, json.Number("12"), data["tool_uses"])
		assert.Equal(t, json.Number("84739"), data["total_tokens"], "零值 TotalTokens 不该抹掉已有值")
		assert.Equal(t, "Read", data["last_tool_name"], "零值 LastToolName 不该抹掉已有值")
	})

	t.Run("全零进度不改写", func(t *testing.T) {
		out, ok, err := chat_repo.PatchSubagentProgressInBlockData([]byte(base), chat_repo.SubagentProgress{})
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Equal(t, base, string(out))
	})

	t.Run("非法正文返回 error", func(t *testing.T) {
		_, ok, err := chat_repo.PatchSubagentProgressInBlockData([]byte("{not json"), chat_repo.SubagentProgress{ToolUses: 3})
		require.Error(t, err)
		assert.False(t, ok)
	})

	// 后台子代理跨轮写回模型(R6/A9):子代理在会话空闲活动轮解出实际模型,靠这条
	// 定向 patch 落回发起消息;其它字段(status/kind/进度数字)必须原样保留。
	t.Run("模型字段被写入且不影响其它字段", func(t *testing.T) {
		out, ok, err := chat_repo.PatchSubagentProgressInBlockData([]byte(base), chat_repo.SubagentProgress{
			Model: "claude-haiku-4-5-20251001",
		})
		require.NoError(t, err)
		assert.True(t, ok)
		data := blockData(t, out)
		assert.Equal(t, "claude-haiku-4-5-20251001", data["model"])
		assert.Equal(t, json.Number("84739"), data["total_tokens"])
		assert.Equal(t, json.Number("9"), data["tool_uses"])
		assert.Equal(t, "Read", data["last_tool_name"])
		assert.Equal(t, "running", data["status"])
		assert.Equal(t, "local_agent", data["kind"])
	})

	t.Run("空模型不覆盖已记录模型", func(t *testing.T) {
		const withModel = `{"parent_tool_call_id":"toolu_agent","kind":"local_agent","status":"running","total_tokens":84739,"tool_uses":9,"model":"claude-opus-5"}`
		out, ok, err := chat_repo.PatchSubagentProgressInBlockData([]byte(withModel), chat_repo.SubagentProgress{
			ToolUses: 12, // 只有工具数更新,Model 留空(first-wins,已记录的模型不该被空值抹掉)
		})
		require.NoError(t, err)
		assert.True(t, ok)
		data := blockData(t, out)
		assert.Equal(t, json.Number("12"), data["tool_uses"])
		assert.Equal(t, "claude-opus-5", data["model"], "空 Model 不该抹掉已记录模型")
	})
}

// TestMessageRepo_PatchSubagentProgress_UpdatesMatchingBlock 走 repo:定位方式与
// FlipSubagentStatus 一致(块表索引点查),命中即改写那一个块行。
func TestMessageRepo_PatchSubagentProgress_UpdatesMatchingBlock(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	expectSubagentBlockLookup(mock, 3, "tu1", subagentBlockRows(42, "tu1",
		`{"parent_tool_call_id":"tu1","kind":"local_agent","status":"running","tool_uses":9}`))
	mock.ExpectExec("UPDATE `chat_message_blocks` SET `codec`=\\?,`data`=\\? WHERE message_id = \\? AND idx = \\?").
		WithArgs(chat_entity.BlockCodecRaw, blockDataContains{substrings: []string{
			`"tool_uses":21`, `"total_tokens":132480`,
		}}, int64(42), 0).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := chat_repo.NewMessage().PatchSubagentProgress(ctx, 3, "tu1", chat_repo.SubagentProgress{ToolUses: 21, TotalTokens: 132480})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_PatchSubagentProgress_NoMatchSilentNil 无命中静默返回:任务可能已 evict。
func TestMessageRepo_PatchSubagentProgress_NoMatchSilentNil(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	expectSubagentBlockLookup(mock, 3, "tu-missing", emptyBlockRows())

	err := chat_repo.NewMessage().PatchSubagentProgress(ctx, 3, "tu-missing", chat_repo.SubagentProgress{ToolUses: 3})
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_UpdateUsage 钉死 per-call usage 的**单列**写入。
//
// 此前这条路径走 Update(整行 Save):usage 帧每个 API call 来一次,而整行回写会把当时
// 已累积的正文一起写回去 —— 一条消息的正文在长轮次里是 MB 级的,实测单条最大 12.9 MB。
// 于是「存 6 个整数」变成了「重写整条正文」,一个 30 次工具调用的轮次要为此重写几十遍。
// 改成单列 UPDATE 后,这条路径在结构上就碰不到块表。
func TestMessageRepo_UpdateUsage(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := chat_repo.NewMessage()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_messages` SET `cache_creation_tokens`=\\?,`cached_tokens`=\\?,`completion_tokens`=\\?,`prompt_tokens`=\\?,`reasoning_tokens`=\\?,`total_input_tokens`=\\?,`updatetime`=\\? WHERE id = \\?").
		WithArgs(4, 3, 2, 1, 5, 6, sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateUsage(ctx, 42, chat_repo.MessageUsage{
		PromptTokens:        1,
		CompletionTokens:    2,
		CachedTokens:        3,
		CacheCreationTokens: 4,
		ReasoningTokens:     5,
		TotalInputTokens:    6,
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_UpdateErrorText 钉死 error_text 的单列写入,理由同 UpdateUsage:
// 存一个字符串不该把整条正文一起重写。
func TestMessageRepo_UpdateErrorText(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	repo := chat_repo.NewMessage()

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_messages` SET `error_text`=\\?,`updatetime`=\\? WHERE id = \\?").
		WithArgs("boom", sqlmock.AnyArg(), int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, repo.UpdateErrorText(ctx, 42, "boom"))
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_ListMeta 钉住读路径的第一步:元数据全量取回,**不碰块表**。
// LoadSession 打开一条 8k 块的会话时,这一步只能是一条查询。
func TestMessageRepo_ListMeta(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE session_id = \\? ORDER BY seq ASC").
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "seq"}).
			AddRow(1, 3, "user", 1).
			AddRow(2, 3, "assistant", 2))

	got, err := chat_repo.NewMessage().ListMeta(ctx, 3)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "", got[0].BlocksJSON, "元数据查询不取正文,BlocksJSON 留空串表示「没补过」")
	assert.Equal(t, "", got[1].BlocksJSON)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_FillBlocks 钉住读路径的第二步:只给点名的那几条消息补正文,
// 一次 IN 查询。窗口外的消息不进 IN 列表,也拿不到正文。
func TestMessageRepo_FillBlocks(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_message_blocks` WHERE message_id IN \\(\\?\\)").
		WithArgs(int64(2)).
		WillReturnRows(sqlmock.NewRows([]string{"message_id", "idx", "type", "tool_call_id", "codec", "data"}).
			AddRow(2, 1, "text", "", chat_entity.BlockCodecRaw, []byte(`{"text":"b"}`)).
			AddRow(2, 0, "text", "", chat_entity.BlockCodecRaw, []byte(`{"text":"a"}`)))

	window := []*chat_entity.Message{{ID: 2, SessionID: 3, Role: "assistant", Seq: 2}}
	require.NoError(t, chat_repo.NewMessage().FillBlocks(ctx, window))
	assert.Equal(t,
		`[{"type":"text","data":{"text":"a"}},{"type":"text","data":{"text":"b"}}]`,
		window[0].BlocksJSON, "块按 idx 升序重组")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_FillBlocksByType 钉住派生视图的取数口径:按类型点查,
// 过滤在 SQL 里而不是把整条转录读回来再筛。
func TestMessageRepo_FillBlocksByType(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectQuery("SELECT \\* FROM `chat_message_blocks` WHERE message_id IN \\(\\?,\\?\\) AND `type` IN \\(\\?,\\?\\)").
		WithArgs(int64(1), int64(2), "tool_use", "subagent_state").
		WillReturnRows(sqlmock.NewRows([]string{"message_id", "idx", "type", "tool_call_id", "codec", "data"}).
			AddRow(2, 3, "tool_use", "tu1", chat_entity.BlockCodecRaw, []byte(`{"id":"tu1"}`)))

	msgs := []*chat_entity.Message{
		{ID: 1, SessionID: 3, Role: "user", Seq: 1},
		{ID: 2, SessionID: 3, Role: "assistant", Seq: 2},
	}
	require.NoError(t, chat_repo.NewMessage().FillBlocksByType(ctx, msgs, []string{"tool_use", "subagent_state"}))
	assert.Equal(t, "[]", msgs[0].BlocksJSON, "没有命中类型的消息拿到空正文而不是空串")
	assert.Equal(t, `[{"type":"tool_use","data":{"id":"tu1"}}]`, msgs[1].BlocksJSON)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_FillBlocksByType_NoTypes 空类型集合不发查询 —— 让「一个类型都不要」
// 退化成整条转录全取回来,正是本轮要消灭的那个形态。
func TestMessageRepo_FillBlocksByType_NoTypes(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	msgs := []*chat_entity.Message{{ID: 1, SessionID: 3, Role: "user", Seq: 1}}
	require.NoError(t, chat_repo.NewMessage().FillBlocksByType(ctx, msgs, nil))
	assert.Equal(t, "[]", msgs[0].BlocksJSON)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// expectSubagentBlocksByType 是「按 task_id 找」用的定位期望:task_id 不是列、它在
// 块正文的 JSON 里,只能按 (type, message_id) 索引把本会话的 subagent_state 块取出来
// 逐条解。会话里这种块数量有限(一次派遣一条),不是全表扫。
func expectSubagentBlocksByType(mock sqlmock.Sqlmock, sessionID int64, rows *sqlmock.Rows) {
	mock.ExpectQuery("SELECT .* FROM `chat_message_blocks` JOIN `chat_messages`.*`chat_message_blocks`.`type` = \\?").
		WithArgs(sessionID, "subagent_state").
		WillReturnRows(rows)
}

// CLI 恢复一个被中断的子代理时沿用同一个 task_id、换一个 tool_use_id。恢复若发生在
// **后一轮**,原来那张派遣卡早已落库、过不了当轮 accumulator —— 这个方法就是那条路:
// 按 task_id 把它找回来,推回运行态,并把被覆盖的终态收进 resumes 留证。
func TestMessageRepo_ResumeSubagentByTaskID_ReopensTheEarlierCard(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	rows := sqlmock.NewRows([]string{"message_id", "idx", "type", "tool_call_id", "codec", "data"}).
		AddRow(int64(41), 2, "subagent_state", "tu-other", chat_entity.BlockCodecRaw,
			[]byte(`{"parent_tool_call_id":"tu-other","task_id":"OTHER","status":"completed"}`)).
		AddRow(int64(42), 5, "subagent_state", "tu-A", chat_entity.BlockCodecRaw,
			[]byte(`{"parent_tool_call_id":"tu-A","task_id":"T","status":"failed","summary":"API error","tool_uses":61}`))
	expectSubagentBlocksByType(mock, 3, rows)
	mock.ExpectExec("UPDATE `chat_message_blocks` SET `codec`=\\?,`data`=\\? WHERE message_id = \\? AND idx = \\?").
		WithArgs(chat_entity.BlockCodecRaw, blockDataContains{substrings: []string{
			`"status":"running"`,
			`"resumes":[{"status":"failed","summary":"API error"}]`,
			`"tool_uses":61`,
		}}, int64(42), 5).
		WillReturnResult(sqlmock.NewResult(0, 1))

	got, err := chat_repo.NewMessage().ResumeSubagentByTaskID(ctx, 3, "T", "running")
	assert.NoError(t, err)
	assert.Equal(t, "tu-A", got, "交回原卡的 tool_call_id,调用方据此把恢复段的新 tool call 路由到它")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_ResumeSubagentByTaskID_NoMatchSilentEmpty(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	expectSubagentBlocksByType(mock, 3, emptyBlockRows())

	got, err := chat_repo.NewMessage().ResumeSubagentByTaskID(ctx, 3, "MISSING", "running")
	assert.NoError(t, err)
	assert.Equal(t, "", got)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMessageRepo_ResumeSubagentByTaskID_EmptyArgsShortCircuit(t *testing.T) {
	ctx, _, _ := testutils.Database(t)
	got, err := chat_repo.NewMessage().ResumeSubagentByTaskID(ctx, 3, "", "running")
	require.NoError(t, err)
	require.Equal(t, "", got, "task_id 为空不查库 —— 空 task_id 匹配不出唯一的卡")
}
