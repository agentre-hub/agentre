package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609040112 建同步的三张队列表（R5/R7/R2a，决策 12）：
//
//   - sync_lost_changes —— 「没能同步的改动」，被覆盖 / 悬空引用超期 / 超窗口被拒
//     三类失效事件共用，保留 30 天，并直接带上 R5a 的跨机自然键两列
//     scope_sync_id / agentred_fingerprint —— 恢复一条路径记录或远端 backend
//     必须能解析出归属。
//   - sync_outbound_queue —— 出站，每条带基版本（R4a/R6a）。
//   - sync_inbound_queue —— 入站，引用目标未到达时暂缓落地。排空路径
//     （sync_svc.replayDeferred）是整份重列、逐条重试，从不按「在等谁」收窄，
//     因此等待原因只进调试日志、不落库。
//
// 三张表都按账号分命名空间（sync_account_id，R13a）。账号级实体表上的六列同步元数据
// 与 sync_id 部分唯一索引住在各自的建表迁移里，不在这里重复。
func migration202609040112() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609040112",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS sync_lost_changes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	entity_type TEXT NOT NULL,
	entity_sync_id TEXT NOT NULL,
	base_version BIGINT NOT NULL DEFAULT 0,
	reason TEXT NOT NULL,
	payload_json TEXT NOT NULL DEFAULT '',
	origin_device TEXT NOT NULL DEFAULT '',
	scope_sync_id TEXT NOT NULL DEFAULT '',
	agentred_fingerprint TEXT NOT NULL DEFAULT '',
	occurred_at BIGINT NOT NULL DEFAULT 0,
	createtime BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_sync_lost_changes_account
ON sync_lost_changes(sync_account_id, createtime)`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS sync_outbound_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	entity_type TEXT NOT NULL,
	local_id BIGINT NOT NULL DEFAULT 0,
	entity_sync_id TEXT NOT NULL DEFAULT '',
	op TEXT NOT NULL,
	base_version BIGINT NOT NULL DEFAULT 0,
	queued_at BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_sync_outbound_queue_account
ON sync_outbound_queue(sync_account_id, queued_at)`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS sync_inbound_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	entity_type TEXT NOT NULL,
	entity_sync_id TEXT NOT NULL,
	payload_json TEXT NOT NULL DEFAULT '',
	received_at BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_sync_inbound_queue_account
ON sync_inbound_queue(sync_account_id, received_at)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS sync_inbound_queue`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DROP TABLE IF EXISTS sync_outbound_queue`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS sync_lost_changes`).Error
		},
	}
}
