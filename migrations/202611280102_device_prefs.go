package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611280102 引入设备偏好与设备相关配置的结构（spec 2026-11-25「组织成员与执行位置
// 解耦」决策 5 / 13 / 14 与「项目」「LLM 供应商」「设备页」）。只建结构，回填在下一条迁移
// （结构与回填分两条，失败时分得清是 DDL 还是数据的错）。
//
//   - device_prefs            每台参与派发的设备一行：标签 + 在账号设备顺序里的位置。
//   - device_cli_paths        CLI 路径覆盖，键是（运行时类型, 设备）；取代按（后端 sync_id,
//     设备）存的 agent_backend_cli_overlays（旧表原样保留，由后续任务移除）。
//   - projects.default_device_fingerprint  项目默认设备，空 = 没设。
//   - llm_providers.reach_mode / reach_devices_json  供应商「谁能连到」，默认任何设备。
//
// 不可逆的部分：无。旧表旧列原样保留；新表新列可整体丢弃回到迁移前。
func migration202611280102() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202611280102",
		Migrate: func(tx *gorm.DB) error {
			// gormigrate 默认不包事务：中途失败会留下一半的 ALTER，下次启动再跑同一条
			// ALTER 就撞「列已存在」永远起不来。SQLite 的 DDL 可以进事务。
			return tx.Transaction(func(tx *gorm.DB) error {
				for _, stmt := range devicePrefDDL {
					if err := tx.Exec(stmt).Error; err != nil {
						return err
					}
				}
				return nil
			})
		},
		Rollback: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				"ALTER TABLE llm_providers DROP COLUMN reach_devices_json",
				"ALTER TABLE llm_providers DROP COLUMN reach_mode",
				"ALTER TABLE projects DROP COLUMN default_device_fingerprint",
				"DROP TABLE IF EXISTS device_cli_paths",
				"DROP TABLE IF EXISTS device_prefs",
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}

var devicePrefDDL = []string{
	`CREATE TABLE IF NOT EXISTS device_prefs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	device_fingerprint TEXT NOT NULL DEFAULT '',
	labels_json TEXT NOT NULL DEFAULT '[]',
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
)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uniq_device_prefs_fingerprint ON device_prefs(device_fingerprint) WHERE status = 1`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uniq_device_prefs_sync_id ON device_prefs(sync_id) WHERE sync_id != ''`,
	`CREATE TABLE IF NOT EXISTS device_cli_paths (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	runtime_type TEXT NOT NULL DEFAULT '',
	device_fingerprint TEXT NOT NULL DEFAULT '',
	cli_path TEXT NOT NULL DEFAULT '',
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0
)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uniq_device_cli_paths_natural ON device_cli_paths(runtime_type, device_fingerprint) WHERE status = 1`,
	`CREATE UNIQUE INDEX IF NOT EXISTS uniq_device_cli_paths_sync_id ON device_cli_paths(sync_id) WHERE sync_id != ''`,
	"ALTER TABLE projects ADD COLUMN default_device_fingerprint TEXT NOT NULL DEFAULT ''",
	"ALTER TABLE llm_providers ADD COLUMN reach_mode TEXT NOT NULL DEFAULT 'any'",
	"ALTER TABLE llm_providers ADD COLUMN reach_devices_json TEXT NOT NULL DEFAULT '[]'",
}
