package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608080011 建 agentred 侧持久化地基的两张表(见规格「持久化数据变化 /
// agentred 侧」)。
//
// daemon_sessions —— 会话表。复合主键 (peer_fingerprint, peer_session_id):会话 id
// 是各客户端本地自增的,不同客户端必然重号,单独拿 peer_session_id 当主键会把不同对端
// 的同号会话错当同一条(R16)。不含"等待输入"列——那是 running 之上的实时叠加,不落库
// (R11,后续任务处理)。agent_id 是对端(桌面端)本地的数字 agent 主键,原样透传保存,
// 供后续任务展示用。「某会话最新的 seq」以通知日志自己的 MAX(seq) 为唯一真相源,
// 不在会话表重复维护游标。
//
// daemon_notification_logs —— 通知日志表。复合主键 (peer_fingerprint,
// peer_session_id, seq):日志的一行 = 一条本该发出的通知,method/payload 是原样的
// JSON-RPC (method, params)。只追加、永久保存——agentred 不再回收任何一行(规格决策 8,
// server 与 agentred 两端都不设保留期),清理逻辑留给后续,本迁移不建任何清理路径。
func migration202608080011() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080011",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS daemon_sessions (
	peer_fingerprint TEXT NOT NULL,
	peer_session_id TEXT NOT NULL,
	agent_id INTEGER NOT NULL DEFAULT 0,
	cwd TEXT NOT NULL DEFAULT '',
	backend_type TEXT NOT NULL DEFAULT '',
	lifecycle_state TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL DEFAULT '',
	agent_sync_id TEXT NOT NULL DEFAULT '',
	provider_session_id TEXT NOT NULL DEFAULT '',
	provider_key TEXT NOT NULL DEFAULT '',
	model_key TEXT NOT NULL DEFAULT '',
	project_sync_id TEXT NOT NULL DEFAULT '',
	createtime INTEGER NOT NULL DEFAULT 0,
	last_message_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (peer_fingerprint, peer_session_id)
)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE TABLE IF NOT EXISTS daemon_notification_journal (
	peer_fingerprint TEXT NOT NULL,
	peer_session_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	payload BLOB NOT NULL,
	createtime INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (peer_fingerprint, peer_session_id, seq)
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
