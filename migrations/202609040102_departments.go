package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609040102 建 departments 表。
//
// 字段语义：
//   - parent_id      0 = 顶级部门（与 CEO Agent 同层）
//   - lead_agent_id  0 = 未指定部门长
//   - accent_color   "agent-1".."agent-10" / "neutral" / ""
//   - status         cago consts: ACTIVE / DELETE
//   - sync_*         账号级同步元数据（syncmeta_entity.SyncMeta）
func migration202609040102() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609040102",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS departments (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	icon TEXT NOT NULL DEFAULT '',
	accent_color TEXT NOT NULL DEFAULT '',
	parent_id INTEGER NOT NULL DEFAULT 0,
	lead_agent_id INTEGER NOT NULL DEFAULT 0,
	sort_order INTEGER NOT NULL DEFAULT 0,
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_departments_parent_id ON departments(parent_id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_departments_lead_agent_id ON departments(lead_agent_id)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_departments_sync_id
ON departments(sync_id) WHERE sync_id != ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS departments`).Error
		},
	}
}
