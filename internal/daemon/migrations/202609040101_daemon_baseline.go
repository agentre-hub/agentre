package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609040101 是 agentred 侧持久化的基线迁移:一次建出会话表与通知日志表的
// 最终形态。它压缩了发布前的四条未发布迁移(建表 / 加 conversation_id 列 / 身份键收缩
// 重建 / 加 reasoning_effort),那些迁移曾经服务的存量库在 2026-09-04 已一律删库重建,
// 因此这里只建终态、不带任何回填与重建路径。
//
// daemon_sessions —— 会话表。主键是 conversation_id 一列:对话身份全局唯一,两张表都
// 只按它认人(规格「会话身份 / 身份键收缩为一列」)。peer_fingerprint 是普通列,做来源
// 标注与授权收窄用(session_repo 的读路径按它收窄),并单独建索引把「列出某个对端的
// 会话、按最近活动倒序」那条查询接住。agent_id 是对端(桌面端)本地的数字 agent 主键,
// 原样透传保存。不含「等待输入」列 —— 那是 running 之上的实时叠加,不落库(R11)。
// 「某会话最新的 seq」以通知日志自己的 MAX(seq) 为唯一真相源,不在会话表重复维护游标。
// provider_key / model_key / reasoning_effort 是会话级覆盖的镜像,只供显示,执行路径
// 不读它们;取值词表由发起端把关,不在 DDL 上加 CHECK —— 档位表会随后端能力演进,写死
// 在表结构里改一次要重写整张表。
//
// daemon_notification_journal —— 通知日志表。主键 (conversation_id, seq),一行 = 一条
// 本该发出的通知,payload 是原样的 JSON-RPC 载荷。只追加、永久保存 —— agentred 不再
// 回收任何一行(规格决策 8,server 与 agentred 两端都不设保留期)。
func migration202609040101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609040101",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS daemon_sessions (
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
	last_message_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (conversation_id)
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_daemon_sessions_peer_fingerprint
	ON daemon_sessions (peer_fingerprint, last_message_at)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE TABLE IF NOT EXISTS daemon_notification_journal (
	conversation_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	peer_fingerprint TEXT NOT NULL,
	payload BLOB NOT NULL,
	createtime INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (conversation_id, seq)
)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS daemon_notification_journal`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS daemon_sessions`).Error
		},
	}
}
