package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608200002 promotes providers and backend identities to account
// sync objects and creates the per-device CLI overlay projection. Existing
// backend rows keep their IDs and sync IDs (no type/name merge); a legacy CLI
// path only seeds an overlay where its original fingerprint is still present.
func migration202608200002() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608200002",
		Migrate: func(tx *gorm.DB) error {
			for _, column := range []string{
				"sync_id TEXT NOT NULL DEFAULT ''",
				"sync_account_id BIGINT NOT NULL DEFAULT 0",
				"sync_version BIGINT NOT NULL DEFAULT 0",
				"sync_updated_at BIGINT NOT NULL DEFAULT 0",
				"sync_origin TEXT NOT NULL DEFAULT ''",
				"sync_deleted_at BIGINT NOT NULL DEFAULT 0",
			} {
				if err := tx.Exec("ALTER TABLE llm_providers ADD COLUMN " + column).Error; err != nil {
					return err
				}
			}
			if err := tx.Exec(`UPDATE llm_providers SET sync_id = provider_key WHERE sync_id = ''`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_llm_providers_sync_id
ON llm_providers(sync_id) WHERE sync_id != ''`).Error; err != nil {
				return err
			}
			// Historical backends predate EnsureSyncID. Give each row an opaque stable
			// identity before creating an overlay; never collapse same-name/type rows.
			if err := tx.Exec(`UPDATE agent_backends SET sync_id = lower(hex(randomblob(16))) WHERE sync_id = ''`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS agent_backend_cli_overlays (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	backend_sync_id TEXT NOT NULL DEFAULT '',
	agentred_fingerprint TEXT NOT NULL DEFAULT '',
	cli_path TEXT NOT NULL DEFAULT '',
	status INTEGER NOT NULL DEFAULT 1,
	createtime BIGINT NOT NULL DEFAULT 0,
	updatetime BIGINT NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_backend_cli_overlays_natural
ON agent_backend_cli_overlays(backend_sync_id, agentred_fingerprint) WHERE status = 1`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_backend_cli_overlays_sync_id
ON agent_backend_cli_overlays(sync_id) WHERE sync_id != ''`).Error; err != nil {
				return err
			}
			// A known canonical fingerprint can preserve its own explicit path. Rows
			// with a missing/relative historical device deliberately get no overlay:
			// assigning their credential-shaped absolute path to another machine would
			// violate the per-device boundary.
			if err := tx.Exec(`INSERT INTO agent_backend_cli_overlays
	(backend_sync_id, agentred_fingerprint, cli_path, status, sync_id, sync_account_id, createtime, updatetime)
SELECT b.sync_id, b.device_id, b.cli_path, 1, lower(hex(randomblob(16))), b.sync_account_id, b.createtime, b.updatetime
FROM agent_backends b
WHERE b.status = 1 AND b.sync_id != '' AND b.cli_path != '' AND b.device_id LIKE 'sha256:%'`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`INSERT INTO agent_backend_cli_overlays
	(backend_sync_id, agentred_fingerprint, cli_path, status, sync_id, sync_account_id, createtime, updatetime)
SELECT b.sync_id, p.daemon_fingerprint, b.cli_path, 1, lower(hex(randomblob(16))), b.sync_account_id, b.createtime, b.updatetime
FROM agent_backends b
JOIN paired_agentreds p ON CAST(p.id AS TEXT) = b.device_id
WHERE b.status = 1 AND p.status = 1 AND b.sync_id != '' AND b.cli_path != '' AND p.daemon_fingerprint != ''`).Error; err != nil {
				return err
			}
			return tx.Exec(`UPDATE agent_backends SET device_id = '', cli_path = '' WHERE status = 1`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS agent_backend_cli_overlays`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DROP INDEX IF EXISTS uniq_llm_providers_sync_id`).Error; err != nil {
				return err
			}
			for _, column := range []string{"sync_deleted_at", "sync_origin", "sync_updated_at", "sync_version", "sync_account_id", "sync_id"} {
				if err := tx.Exec("ALTER TABLE llm_providers DROP COLUMN " + column).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
