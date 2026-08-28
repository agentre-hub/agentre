package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/issue_entity"
)

// migration202608080010 建 issues / labels / issue_labels 三张表，并 seed 10 个内置标签。
func migration202608080010() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080010",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS issues (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL DEFAULT 0,
	title TEXT NOT NULL,
	body TEXT NOT NULL DEFAULT '',
	state TEXT NOT NULL DEFAULT 'open',
	agent_status TEXT NOT NULL DEFAULT 'idle',
	stage TEXT NOT NULL DEFAULT 'todo',
	position REAL NOT NULL DEFAULT 0,
	assignee_agent_id INTEGER NOT NULL DEFAULT 0,
	agent_backend_id BIGINT NOT NULL DEFAULT 0,
	llm_provider_key TEXT NOT NULL DEFAULT '',
	llm_model_key TEXT NOT NULL DEFAULT '',
	session_id INTEGER NOT NULL DEFAULT 0,
	source TEXT NOT NULL DEFAULT 'manual',
	closed_at INTEGER NOT NULL DEFAULT 0,
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
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_state ON issues(status, state, updatetime)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_project ON issues(project_id, status)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_board ON issues(status, stage, position)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_session_id ON issues(session_id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_assignee_agent_id ON issues(assignee_agent_id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_issues_sync_id ON issues(sync_id) WHERE sync_id != ''`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS labels (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	tone TEXT NOT NULL DEFAULT '',
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
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_labels_name_active ON labels(name) WHERE status = 1`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_labels_sync_id ON labels(sync_id) WHERE sync_id != ''`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS issue_labels (
	issue_id INTEGER NOT NULL,
	label_id INTEGER NOT NULL,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0,
	PRIMARY KEY (issue_id, label_id)
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_issue_labels_label ON issue_labels(label_id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_issue_labels_sync_id ON issue_labels(sync_id) WHERE sync_id != ''`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`INSERT INTO labels (name, tone, sort_order, status, createtime, updatetime)
SELECT name, tone, sort_order, 1,
	CAST(strftime('%s','now') AS INTEGER) * 1000,
	CAST(strftime('%s','now') AS INTEGER) * 1000
FROM (
	SELECT 'bug' AS name, 'red' AS tone, 1 AS sort_order
	UNION ALL SELECT 'critical', 'red_solid', 2
	UNION ALL SELECT 'docs', 'gray', 3
	UNION ALL SELECT 'feature', 'green', 4
	UNION ALL SELECT 'refactor', 'steel', 5
) seed
WHERE NOT EXISTS (SELECT 1 FROM labels WHERE labels.name = seed.name AND labels.status = 1)`).Error; err != nil {
				return err
			}
			for _, name := range issue_entity.BuiltinLabelNames() {
				if err := tx.Exec(`UPDATE labels SET sync_id = ? WHERE name = ? AND status = 1`,
					issue_entity.SeedLabelSyncID(name), name).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS issue_labels`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DROP TABLE IF EXISTS labels`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS issues`).Error
		},
	}
}
