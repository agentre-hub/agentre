package chat_repo_test

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	cagoblocks "github.com/cago-frame/agents/agent/blocks"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
)

// deflatedMatcher 校验落库的块正文解压后与预期原文逐字节相等 —— AnyArg() 查不出
// 「压缩写坏了」这一类缺陷。
type deflatedMatcher struct{ want []byte }

func (m deflatedMatcher) Match(v driver.Value) bool {
	data, ok := v.([]byte)
	if !ok {
		return false
	}
	got, err := chat_entity.DecodeBlockData(chat_entity.BlockCodecDeflate, data)
	if err != nil {
		return false
	}
	return bytes.Equal(m.want, got) && len(data) < len(m.want)
}

// mixedBlocksJSON 造一条「混合块 + 其中一块超过压缩阈值」的消息正文,返回 blocks_json
// 与逐块的 StoredBlock(供断言块行的 type / tool_call_id / data)。
func mixedBlocksJSON(t *testing.T) (string, []cagoblocks.StoredBlock) {
	t.Helper()
	big := strings.Repeat("agentre-block-payload ", 500) // > 4 KiB
	require.Greater(t, len(big), chat_entity.BlockCompressThreshold)

	m := &chat_entity.Message{}
	require.NoError(t, m.SetBlocks([]cagoblocks.ContentBlock{
		&cagoblocks.TextBlock{Text: "hello"},
		&cagoblocks.ToolUseBlock{ID: "tu-1", Name: "bash"},
		&cagoblocks.ToolResultBlock{ToolUseID: "tu-1"},
		&cagoblocks.TextBlock{Text: big},
	}))

	var stored []cagoblocks.StoredBlock
	require.NoError(t, json.Unmarshal([]byte(m.BlocksJSON), &stored))
	require.Len(t, stored, 4)
	return m.BlocksJSON, stored
}

// TestMessageRepo_CreateSplitsBlocksIntoRows 证明写入时消息正文被拆成「一块一行」:
// idx 按数组下标递增、type 与定位键 tool_call_id 由仓储填充、超过阈值的块压缩存储。
func TestMessageRepo_CreateSplitsBlocksIntoRows(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	blocksJSON, stored := mixedBlocksJSON(t)

	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO `chat_messages`").
		WillReturnResult(sqlmock.NewResult(42, 1))
	mock.ExpectExec("INSERT INTO `chat_message_blocks`").
		WithArgs(
			int64(42), 0, "text", "", chat_entity.BlockCodecRaw, []byte(stored[0].Data),
			int64(42), 1, "tool_use", "tu-1", chat_entity.BlockCodecRaw, []byte(stored[1].Data),
			int64(42), 2, "tool_result", "tu-1", chat_entity.BlockCodecRaw, []byte(stored[2].Data),
			int64(42), 3, "text", "", chat_entity.BlockCodecDeflate, deflatedMatcher{want: []byte(stored[3].Data)},
		).
		WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectCommit()

	m := &chat_entity.Message{SessionID: 3, Role: "assistant", BlocksJSON: blocksJSON, Seq: 1}
	require.NoError(t, chat_repo.NewMessage().Create(ctx, m))
	assert.Equal(t, int64(42), m.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_FindReassemblesBlocksByteEqual 证明读出时块按 idx 升序重组,且重组结果
// 与写入前逐字节相等(含压缩块与未压缩块混合)。
func TestMessageRepo_FindReassemblesBlocksByteEqual(t *testing.T) {
	ctx, _, mock := testutils.Database(t)
	blocksJSON, stored := mixedBlocksJSON(t)

	rows := sqlmock.NewRows([]string{"message_id", "idx", "type", "tool_call_id", "codec", "data"})
	// 乱序返回,证明重组顺序来自 idx 而不是行顺序。
	for _, idx := range []int{3, 0, 2, 1} {
		codec, data := chat_entity.EncodeBlockData(stored[idx].Data)
		toolCallID := ""
		switch idx {
		case 1, 2:
			toolCallID = "tu-1"
		}
		rows.AddRow(42, idx, stored[idx].Type, toolCallID, codec, data)
	}

	mock.ExpectQuery("SELECT \\* FROM `chat_messages` WHERE id = \\? ORDER BY `chat_messages`.`id` LIMIT \\?").
		WithArgs(int64(42), 1).
		WillReturnRows(sqlmock.NewRows([]string{"id", "session_id", "role", "seq"}).
			AddRow(42, 3, "assistant", 4))
	mock.ExpectQuery("SELECT \\* FROM `chat_message_blocks` WHERE message_id IN \\(\\?\\)").
		WithArgs(int64(42)).
		WillReturnRows(rows)

	got, err := chat_repo.NewMessage().Find(ctx, 42)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, blocksJSON, got.BlocksJSON, "重组出的正文必须与写入前逐字节相等")

	decoded, err := got.GetBlocks()
	require.NoError(t, err)
	require.Len(t, decoded, 4)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_DeleteFromSeqRemovesBlockRows 证明截断会话尾部时块行随宿主消息一起消失
// ——任何时刻都不允许存在没有宿主消息的块行。
func TestMessageRepo_DeleteFromSeqRemovesBlockRows(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id IN \\(SELECT id FROM `chat_messages` WHERE session_id = \\? AND seq >= \\?\\)").
		WithArgs(int64(3), 5).
		WillReturnResult(sqlmock.NewResult(0, 9))
	mock.ExpectExec("DELETE FROM `chat_messages` WHERE session_id = \\? AND seq >= \\?").
		WithArgs(int64(3), 5).
		WillReturnResult(sqlmock.NewResult(0, 4))
	mock.ExpectCommit()

	deleted, err := chat_repo.NewMessage().DeleteFromSeq(ctx, 3, 5)
	require.NoError(t, err)
	assert.Equal(t, int64(4), deleted)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_UpdateReplacesBlockRows 证明整行回写时旧块行先被清掉再写新块,
// 不会留下上一版残留的高位 idx 块。
func TestMessageRepo_UpdateReplacesBlockRows(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	m := &chat_entity.Message{ID: 42, SessionID: 3, Role: "assistant", Seq: 4}
	require.NoError(t, m.SetBlocks([]cagoblocks.ContentBlock{&cagoblocks.TextBlock{Text: "hi"}}))

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_messages` SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id = \\?").
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 3))
	mock.ExpectExec("INSERT INTO `chat_message_blocks`").
		WithArgs(int64(42), 0, "text", "", chat_entity.BlockCodecRaw, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	require.NoError(t, chat_repo.NewMessage().Update(ctx, m))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_CheckpointBlocksWritesOnlyTheDelta 钉住轮内 checkpoint 的写放大修复。
//
// 事故形态:turn 每收到一个 ToolResult 就 checkpoint 一次,而 checkpoint 走 Update →
// replaceBlocks(DELETE 全部块 + INSERT 全部块),于是第 k 次 checkpoint 重写当时已有的
// 全部 k 个块。用户库里消息 26382 最终 1723 块 / 2 MB,却被 checkpoint 840 次、
// DELETE 侧重写 723,550 行 / 910 MB,WAL 涨到 1.4 GB。
//
// 这里断言的正是「只写增量」:追加一个块 → 一条 upsert、零条 DELETE。
func TestMessageRepo_CheckpointBlocksWritesOnlyTheDelta(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	prev := `[{"type":"text","data":{"text":"a"}},{"type":"tool_use","data":{"id":"tu-1"}}]`
	next := prev[:len(prev)-1] + `,{"type":"tool_result","data":{"tool_use_id":"tu-1"}}]`

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_messages`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `chat_message_blocks`").
		WithArgs(
			int64(42), 2, "tool_result", "tu-1", chat_entity.BlockCodecRaw,
			[]byte(`{"tool_use_id":"tu-1"}`),
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	m := &chat_entity.Message{ID: 42, SessionID: 3, Role: "assistant", BlocksJSON: next, Seq: 1}
	require.NoError(t, chat_repo.NewMessage().CheckpointBlocks(ctx, m, prev))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_CheckpointBlocksTruncatesShrunkTail 证明新版更短时高位残块被删掉 ——
// 整表替换天然没有残块问题,差分写必须自己补上这一刀。
func TestMessageRepo_CheckpointBlocksTruncatesShrunkTail(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	prev := `[{"type":"text","data":{"text":"a"}},{"type":"text","data":{"text":"b"}}]`
	next := `[{"type":"text","data":{"text":"a"}}]`

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_messages`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("DELETE FROM `chat_message_blocks` WHERE message_id = \\? AND idx >= \\?").
		WithArgs(int64(42), 1).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	m := &chat_entity.Message{ID: 42, SessionID: 3, Role: "assistant", BlocksJSON: next, Seq: 1}
	require.NoError(t, chat_repo.NewMessage().CheckpointBlocks(ctx, m, prev))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestMessageRepo_CheckpointBlocksSkipsUnchangedBody 证明正文一个字节都没变时不发任何块语句。
func TestMessageRepo_CheckpointBlocksSkipsUnchangedBody(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	same := `[{"type":"text","data":{"text":"a"}}]`

	mock.ExpectBegin()
	mock.ExpectExec("UPDATE `chat_messages`").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	m := &chat_entity.Message{ID: 42, SessionID: 3, Role: "assistant", BlocksJSON: same, Seq: 1}
	require.NoError(t, chat_repo.NewMessage().CheckpointBlocks(ctx, m, same))
	assert.NoError(t, mock.ExpectationsWereMet())
}
