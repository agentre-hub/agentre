package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608080009 建 server_state（联机状态单行表）+ paired_agentreds（LAN 直连
// 已配对设备表）。
//
// server_state 用 CHECK (id=1) 锁成单行 —— 桌面端只关心唯一的「当前 Server 连接」，
// 不存历史快照。CREATE 后立即 INSERT OR IGNORE id=1，业务代码总是用 Save() 更新而非
// Insert，避免 First() / Save() 路径里 "record not found" 的特例分支。
//
// paired_agentreds 用 partial unique index (url) WHERE status=1，让 soft-delete 的旧行
// 不阻塞同 URL 重新配对。
func migration202608080009() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080009",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS server_state (
	id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
	server_url TEXT NOT NULL DEFAULT '',
	device_id INTEGER NOT NULL DEFAULT 0,
	device_fingerprint TEXT NOT NULL DEFAULT '',
	server_user_id INTEGER NOT NULL DEFAULT 0,
	keychain_account TEXT NOT NULL DEFAULT '',
	updatetime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`INSERT OR IGNORE INTO server_state (id) VALUES (1)`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS paired_agentreds (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	url TEXT NOT NULL,
	daemon_fingerprint TEXT NOT NULL,
	instance_uuid TEXT NOT NULL,
	tls_mode TEXT NOT NULL DEFAULT 'default',
	tls_cert_pem TEXT NOT NULL DEFAULT '',
	paired_at INTEGER NOT NULL DEFAULT 0,
	last_seen_at INTEGER NOT NULL DEFAULT 0,
	last_error TEXT NOT NULL DEFAULT '',
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_paired_agentreds_url
ON paired_agentreds(url) WHERE status = 1 AND url != ''`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_paired_agentreds_fingerprint
ON paired_agentreds(daemon_fingerprint) WHERE status = 1 AND daemon_fingerprint != ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS paired_agentreds`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS server_state`).Error
		},
	}
}
