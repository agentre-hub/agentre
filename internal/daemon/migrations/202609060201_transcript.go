package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609060201 建出与桌面端同形的三张转录表（消息行 + 块行 + 帧台账）——规格
// 2026-09-05 决策 1 / 8 / 9。
//
// DDL 逐字取自桌面端（migrations/202609040106_chat.go 与
// 202609060101_transcript_frame_seq.go）—— 两个宿主共用同一份实体与仓储代码，表结构错
// 一格就是同一行代码在两台机器上写出两种结果。两个进程各一个库，所以这里是**复制 DDL**
// 而不是共享一张表；两边形状是否真的一致由 transcript_parity_test.go 盯着。
//
// turn_trigger 排在消息表最后不是随手放的：桌面端那张表的列序如此，两边必须逐列相同。
func migration202609060201() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609060201",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE TABLE IF NOT EXISTS chat_messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	device_fingerprint TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL,
	model TEXT NOT NULL DEFAULT '',
	prompt_tokens INTEGER NOT NULL DEFAULT 0,
	completion_tokens INTEGER NOT NULL DEFAULT 0,
	cached_tokens INTEGER NOT NULL DEFAULT 0,
	cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
	reasoning_tokens INTEGER NOT NULL DEFAULT 0,
	total_input_tokens INTEGER NOT NULL DEFAULT 0,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	first_token_ms INTEGER NOT NULL DEFAULT 0,
	tokens_per_sec REAL NOT NULL DEFAULT 0,
	fork_anchor TEXT NOT NULL DEFAULT '',
	error_text TEXT NOT NULL DEFAULT '',
	seq INTEGER NOT NULL DEFAULT 0,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0,
	-- 与桌面端同一个理由：它原本是 ALTER TABLE ADD COLUMN，折叠时保持列序不变。
	turn_trigger TEXT NOT NULL DEFAULT ''
)`,
				`CREATE INDEX IF NOT EXISTS idx_chat_messages_session_seq
	ON chat_messages(session_id, seq)`,

				`CREATE TABLE IF NOT EXISTS chat_message_blocks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	message_id INTEGER NOT NULL,
	idx INTEGER NOT NULL,
	type TEXT NOT NULL DEFAULT '',
	tool_call_id TEXT NOT NULL DEFAULT '',
	codec INTEGER NOT NULL DEFAULT 0,
	data BLOB NOT NULL
)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_chat_message_blocks_message_idx
	ON chat_message_blocks(message_id, idx)`,
				`CREATE INDEX IF NOT EXISTS idx_chat_message_blocks_tool_call
	ON chat_message_blocks(tool_call_id, type, message_id) WHERE tool_call_id != ''`,
				`CREATE INDEX IF NOT EXISTS idx_chat_message_blocks_type_message
	ON chat_message_blocks(type, message_id)`,

				`CREATE TABLE IF NOT EXISTS chat_frame_seqs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	block_idx INTEGER NOT NULL,
	ordinal INTEGER NOT NULL,
	seq INTEGER NOT NULL
)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_chat_frame_seqs_session_seq
	ON chat_frame_seqs(session_id, seq)`,
				`CREATE INDEX IF NOT EXISTS idx_chat_frame_seqs_session_frame
	ON chat_frame_seqs(session_id, message_id, block_idx, ordinal)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
