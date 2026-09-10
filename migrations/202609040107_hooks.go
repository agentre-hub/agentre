package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609040107 建 hooks / hook_events 两张表。
//
// hooks 只有定时触发一种形态：调度由 schedule_expr + timezone + next_run_at 描述，
// 表上不留「触发类型」鉴别列 —— 取值集合大小为 1 的鉴别列不承担任何分派。
func migration202609040107() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609040107",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS hooks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	interpreter TEXT NOT NULL DEFAULT 'bash',
	interpreter_path TEXT NOT NULL DEFAULT '',
	command TEXT NOT NULL DEFAULT '',
	schedule_expr TEXT NOT NULL DEFAULT '',
	timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
	env_json TEXT NOT NULL DEFAULT '[]',
	state_json TEXT NOT NULL DEFAULT '{}',
	next_run_at INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1,
	last_run_at INTEGER NOT NULL DEFAULT 0,
	last_status TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT '',
	last_duration_ms INTEGER NOT NULL DEFAULT 0,
	total_count INTEGER NOT NULL DEFAULT 0,
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_hooks_due ON hooks(enabled, next_run_at)`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS hook_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	hook_id INTEGER NOT NULL,
	kind TEXT NOT NULL DEFAULT 'output',
	title TEXT NOT NULL,
	dedupe_key TEXT NOT NULL DEFAULT '',
	payload_json TEXT NOT NULL DEFAULT '{}',
	received_at INTEGER NOT NULL DEFAULT 0,
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_hook_events_hook ON hook_events(hook_id, received_at)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ux_hook_events_dedupe ON hook_events(hook_id, dedupe_key) WHERE dedupe_key <> ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS hook_events`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS hooks`).Error
		},
	}
}
