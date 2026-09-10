package chat_entity_test

import (
	"bytes"
	"crypto/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
)

// TestEncodeBlockData 覆盖块正文编码的三条分支(决策 3/4):未超阈值原样存、超阈值压缩存、
// 压缩后反而变大时回落原样存。三条分支的解码结果都必须与入参逐字节相等。
func TestEncodeBlockData(t *testing.T) {
	t.Run("未超阈值原样存储", func(t *testing.T) {
		raw := []byte(`{"text":"hi"}`)

		codec, data := chat_entity.EncodeBlockData(raw)

		assert.Equal(t, chat_entity.BlockCodecRaw, codec)
		assert.True(t, bytes.Equal(raw, data), "raw codec 必须原样存储正文")
	})

	t.Run("超阈值且可压缩时压缩存储", func(t *testing.T) {
		raw := []byte(`{"text":"` + strings.Repeat("agentre", 2000) + `"}`)
		require.Greater(t, len(raw), chat_entity.BlockCompressThreshold)

		codec, data := chat_entity.EncodeBlockData(raw)

		assert.Equal(t, chat_entity.BlockCodecDeflate, codec)
		assert.Less(t, len(data), len(raw), "压缩后必须比原文小")

		got, err := chat_entity.DecodeBlockData(codec, data)
		require.NoError(t, err)
		assert.True(t, bytes.Equal(raw, got), "解压必须逐字节还原")
	})

	t.Run("超阈值但压缩后变大时回落原样存储", func(t *testing.T) {
		raw := make([]byte, chat_entity.BlockCompressThreshold+1)
		_, err := rand.Read(raw)
		require.NoError(t, err)

		codec, data := chat_entity.EncodeBlockData(raw)

		assert.Equal(t, chat_entity.BlockCodecRaw, codec)
		assert.True(t, bytes.Equal(raw, data))
	})

	t.Run("恰好等于阈值不压缩", func(t *testing.T) {
		raw := bytes.Repeat([]byte("a"), chat_entity.BlockCompressThreshold)

		codec, _ := chat_entity.EncodeBlockData(raw)

		assert.Equal(t, chat_entity.BlockCodecRaw, codec)
	})

	t.Run("未知 codec 报错而不是静默返回原始字节", func(t *testing.T) {
		_, err := chat_entity.DecodeBlockData(99, []byte("x"))
		assert.Error(t, err)
	})
}

// TestMessageBlockTableName 钉住块表名——迁移 DDL 与实体必须落在同一张表上。
func TestMessageBlockTableName(t *testing.T) {
	assert.Equal(t, "chat_message_blocks", (&chat_entity.MessageBlock{}).TableName())
}

// TestDiffBlocks 覆盖「按差分落块」的四种形态。
//
// 它存在的理由是一次实测事故:turn 每收到一个 ToolResult 就 checkpoint 一次,而
// checkpoint 走的是「DELETE 全部块 + INSERT 全部块」,于是第 k 次 checkpoint 重写
// 当时已有的全部 k 个块 —— 单条消息 O(N²)。用户库里消息 26382 最终只有 1723 块 /
// 2 MB,却被 checkpoint 了 840 次、重写了 723,550 行 / 910 MB(DELETE 侧),
// WAL 涨到 1.4 GB、无关的单行读被拖到几十秒。
func TestDiffBlocks(t *testing.T) {
	block := func(typ, text string) string {
		return `{"type":"` + typ + `","data":{"text":"` + text + `"}}`
	}
	doc := func(parts ...string) string { return "[" + strings.Join(parts, ",") + "]" }

	t.Run("末尾追加一个块时只产出那一个 upsert", func(t *testing.T) {
		prev := doc(block("text", "a"), block("tool_use", "b"))
		next := doc(block("text", "a"), block("tool_use", "b"), block("tool_result", "c"))

		diff, err := chat_entity.DiffBlocks(7, prev, next)

		require.NoError(t, err)
		require.Len(t, diff.Upserts, 1, "未变化的块一个都不许重写")
		assert.Equal(t, 2, diff.Upserts[0].Idx)
		assert.Equal(t, "tool_result", diff.Upserts[0].Type)
		assert.EqualValues(t, 7, diff.Upserts[0].MessageID)
		assert.Equal(t, -1, diff.TruncateFrom, "只追加不截断")
	})

	t.Run("中间某块就地改写时只产出那一个 upsert", func(t *testing.T) {
		prev := doc(block("text", "a"), block("subagent_state", "running"))
		next := doc(block("text", "a"), block("subagent_state", "done"))

		diff, err := chat_entity.DiffBlocks(7, prev, next)

		require.NoError(t, err)
		require.Len(t, diff.Upserts, 1)
		assert.Equal(t, 1, diff.Upserts[0].Idx)
		assert.Equal(t, -1, diff.TruncateFrom)
	})

	t.Run("完全没变化时不产出任何语句", func(t *testing.T) {
		same := doc(block("text", "a"), block("tool_use", "b"))

		diff, err := chat_entity.DiffBlocks(7, same, same)

		require.NoError(t, err)
		assert.Empty(t, diff.Upserts)
		assert.Equal(t, -1, diff.TruncateFrom)
	})

	t.Run("新版更短时截断掉高位残块", func(t *testing.T) {
		prev := doc(block("text", "a"), block("tool_use", "b"), block("tool_result", "c"))
		next := doc(block("text", "a"))

		diff, err := chat_entity.DiffBlocks(7, prev, next)

		require.NoError(t, err)
		assert.Empty(t, diff.Upserts, "留下的那块没变,不必重写")
		assert.Equal(t, 1, diff.TruncateFrom, "idx >= 1 的残块必须删掉")
	})

	t.Run("上一版为空(首次 checkpoint)时整份都是 upsert", func(t *testing.T) {
		next := doc(block("text", "a"), block("tool_use", "b"))

		diff, err := chat_entity.DiffBlocks(7, "[]", next)

		require.NoError(t, err)
		assert.Len(t, diff.Upserts, 2)
		assert.Equal(t, -1, diff.TruncateFrom)
	})

	t.Run("upsert 的正文按阈值编码,与整表落库口径一致", func(t *testing.T) {
		big := strings.Repeat("agentre", 2000)
		next := doc(block("text", big))

		diff, err := chat_entity.DiffBlocks(7, "[]", next)

		require.NoError(t, err)
		require.Len(t, diff.Upserts, 1)
		assert.Equal(t, chat_entity.BlockCodecDeflate, diff.Upserts[0].Codec)
	})
}
