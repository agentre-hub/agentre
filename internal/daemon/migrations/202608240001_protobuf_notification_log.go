package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608240001 resets the unreleased notification journal and changes
// its payload from JSON-RPC method/params text to a typed Protobuf blob.
func migration202608240001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608240001",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE daemon_notification_logs`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE TABLE daemon_notification_logs (
	peer_fingerprint TEXT NOT NULL,
	peer_session_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	payload BLOB NOT NULL,
	created_at INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (peer_fingerprint, peer_session_id, seq)
)`).Error
		},
	}
}
