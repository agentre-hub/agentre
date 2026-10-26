package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202610260101 为已有会话追加 CLI 三态选择；旧行默认跟随 Agent。
func migration202610260101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610260101",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`ALTER TABLE daemon_sessions ADD COLUMN cli_model_mode TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE daemon_sessions ADD COLUMN cli_model TEXT NOT NULL DEFAULT ''`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
