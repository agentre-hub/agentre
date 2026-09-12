package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609060201 把 agentred 的转录存储换成与桌面端同形的「消息行 + 块行」,
// 并退役通知日志(规格 2026-09-05 决策 1 / 8 / 9)。
//
// 三件事,都是同一件事的三半:
//
//  1. daemon_sessions 补一个本地数字主键。共用的消息实体
//     (transcript_entity.Message.SessionID)按数字主键挂靠,而这张表的主键一直是
//     conversation_id(TEXT)。补上之后两个宿主才**真是**同一形状,而不是长得像
//     ——桌面端 chat_sessions 早就是「本地主键 id + 全局标识 conversation_id」的拆分
//     (决策 9)。SQLite 加不了 AUTOINCREMENT 列,只能建新表搬行;conversation_id
//     退成 UNIQUE,原来按它做的 upsert 冲突目标因此照旧成立。
//
//  2. 建 chat_messages / chat_message_blocks / chat_frame_seqs。DDL 逐字取自桌面端
//     (migrations/202609040106_chat.go 与 202609060101_transcript_frame_seq.go)——
//     两个宿主共用同一份实体与仓储代码,表结构错一格就是同一行代码在两台机器上写出
//     两种结果。两个进程各一个库,所以这里是**复制 DDL**而不是共享一张表。
//
//  3. 删掉 daemon_notification_journal。它退役了:同一段内容不再有第二种存储形态。
//     留着一张没人写、没人读的表不是保守,是把「两套存储」这件事的残骸留在库里 ——
//     而它正是这一轮要消灭的那张最大的表(只追加、从不回收)。存量行一并消失:
//     那些字节按新的读路径本来就解释不出任何东西,桌面端与 server 各自还留着自己
//     那一份转录(规格「宿主与它的转录」:同一段内容在两处各存一份)。
//
// 它**没有 Rollback**:回滚要还原一张已经被删掉的表连同它的全部行,而那些行在这次
// 迁移之后就不存在了 —— 一个只删掉新表、把库留在「会话表是新形状、日志却回不来」这种
// 中间态的回滚,比没有回滚更危险。
func migration202609060201() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609060201",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE TABLE IF NOT EXISTS daemon_sessions_v2 (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	conversation_id TEXT NOT NULL,
	peer_fingerprint TEXT NOT NULL,
	agent_id INTEGER NOT NULL DEFAULT 0,
	cwd TEXT NOT NULL DEFAULT '',
	backend_type TEXT NOT NULL DEFAULT '',
	lifecycle_state TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	agent_sync_id TEXT NOT NULL DEFAULT '',
	provider_session_id TEXT NOT NULL DEFAULT '',
	provider_key TEXT NOT NULL DEFAULT '',
	model_key TEXT NOT NULL DEFAULT '',
	reasoning_effort TEXT NOT NULL DEFAULT '',
	project_sync_id TEXT NOT NULL DEFAULT '',
	createtime INTEGER NOT NULL DEFAULT 0,
	last_message_at INTEGER NOT NULL DEFAULT 0
)`,
				`INSERT INTO daemon_sessions_v2 (
	conversation_id, peer_fingerprint, agent_id, cwd, backend_type, lifecycle_state,
	title, agent_sync_id, provider_session_id, provider_key, model_key,
	reasoning_effort, project_sync_id, createtime, last_message_at)
SELECT conversation_id, peer_fingerprint, agent_id, cwd, backend_type, lifecycle_state,
	title, agent_sync_id, provider_session_id, provider_key, model_key,
	reasoning_effort, project_sync_id, createtime, last_message_at
FROM daemon_sessions`,
				`DROP TABLE daemon_sessions`,
				`ALTER TABLE daemon_sessions_v2 RENAME TO daemon_sessions`,
				// 冲突目标必须是库上真实存在的 UNIQUE 约束:session_repo.Upsert 的
				// ON CONFLICT (conversation_id) 认的就是它。
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_daemon_sessions_conversation_id
	ON daemon_sessions (conversation_id)`,
				`CREATE INDEX IF NOT EXISTS idx_daemon_sessions_peer_fingerprint
	ON daemon_sessions (peer_fingerprint, last_message_at)`,

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
	updatetime INTEGER NOT NULL DEFAULT 0
)`,
				`CREATE INDEX IF NOT EXISTS idx_chat_messages_session_seq
	ON chat_messages(session_id, seq)`,

				`CREATE TABLE IF NOT EXISTS chat_message_blocks (
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

				`DROP TABLE IF EXISTS daemon_notification_journal`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
