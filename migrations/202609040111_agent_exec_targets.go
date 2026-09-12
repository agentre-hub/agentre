package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609040111 建 agent_exec_targets（R15）—— Agent 的有序执行目标列表 ——
// 以及 agent_exec_target_overrides（本机对那份顺序的覆盖）。
//
// 一行 = 「这个 Agent 的第 sort_order 档执行目标」，agent_backend_id 指向那一档
// backend，skills_json 是这一档上的技能授权（R15e：技能授权的真相源下沉到执行目标行，
// 不留在 agents 上）。表带账号级同步元数据（syncmeta_entity.SyncMeta）。
func migration202609040111() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609040111",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS agent_exec_targets (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	agent_id INTEGER NOT NULL,
	agent_backend_id INTEGER NOT NULL,
	sort_order INTEGER NOT NULL DEFAULT 0,
	skills_json TEXT NOT NULL DEFAULT '[]',
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			// 同一个 Agent 的两档不能同序：读取侧靠 sort_order 定「第一个」，并列
			// 会让派发结果随存储顺序漂移。
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_exec_targets_agent_sort
ON agent_exec_targets(agent_id, sort_order)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_exec_targets_agent_backend_id
ON agent_exec_targets(agent_backend_id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_exec_targets_sync_id
ON agent_exec_targets(sync_id) WHERE sync_id != ''`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS agent_exec_target_overrides (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	agent_id INTEGER NOT NULL,
	order_json TEXT NOT NULL DEFAULT '[]',
	updatetime BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_exec_target_overrides_agent
ON agent_exec_target_overrides(agent_id)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS agent_exec_target_overrides`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS agent_exec_targets`).Error
		},
	}
}
