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
