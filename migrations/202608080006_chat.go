package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608080006 建 chat_sessions / chat_messages 两张表。
//
// chat_sessions —— 一条会话 = 一个 Agent + 一份 cwd 上下文。
//   - provider_session_id      cago cliagent Session id；builtin 写 "builtin-<id>"
//   - project_id               0 = 自由会话；> 0 时受 project_svc 管控
//   - last_read_at             unix ms；与 last_message_at 配合判断未读
//   - context_window           runner 上报的模型上下文窗口 token 数；0 走 provider/catalog 兜底
//   - permission_mode          运行时切换的 CLI 模式（claudecode/codex）
//   - permission_mode_at_launch  spawn 时下发的快照（claudecode 专用），决定前端能否切回 bypass
//   - provider_key             会话级 LLM 供应商 key；空串 = 跟随 agent 绑定（spec 2026-08-09）
//
// chat_messages —— 一条消息（user / assistant）。blocks_json 用 cago/agents 的 StoredBlock 编码。
//   - cached_tokens / cache_creation_tokens / reasoning_tokens  provider.Usage 三个 token 维度
//   - fork_anchor              fork/regenerate 时的不透明锚点（builtin 空 / claudecode 写 message uuid）
//   - total_input_tokens       runtime translator 按 family 聚合的"本次 API call 输入大小"
//     （Anthropic = prompt+cached+cacheCreation；OpenAI = prompt）
//   - device_id                空 = 本地；非空 = remote device id 字符串（仅展示用）
func migration202608080006() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080006",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS chat_sessions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	agent_id INTEGER NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	agent_status TEXT NOT NULL DEFAULT 'idle',
	last_message_at INTEGER NOT NULL DEFAULT 0,
	provider_session_id TEXT NOT NULL DEFAULT '',
	project_id INTEGER NOT NULL DEFAULT 0,
	purpose TEXT NOT NULL DEFAULT '',
	last_read_at INTEGER NOT NULL DEFAULT 0,
	context_window INTEGER NOT NULL DEFAULT 0,
	permission_mode TEXT NOT NULL DEFAULT '',
	permission_mode_at_launch TEXT NOT NULL DEFAULT '',
	provider_key TEXT NOT NULL DEFAULT '',
	model_key TEXT NOT NULL DEFAULT '',
	exec_agent_backend_id BIGINT NOT NULL DEFAULT 0,
	exec_device_id BIGINT NOT NULL DEFAULT 0,
	exec_daemon_fingerprint TEXT NOT NULL DEFAULT '',
	event_cursor BIGINT NOT NULL DEFAULT 0,
	cwd TEXT NOT NULL DEFAULT '',
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_chat_sessions_agent_status_last
ON chat_sessions(agent_id, status, last_message_at)`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS chat_messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	device_id TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL,
	blocks_json TEXT NOT NULL DEFAULT '[]',
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
)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_chat_messages_session_seq
ON chat_messages(session_id, seq)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS chat_messages`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS chat_sessions`).Error
		},
	}
}
