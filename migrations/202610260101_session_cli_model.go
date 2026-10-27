package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202610260101 为会话追加 CLI 三态选择；已有行空串仍跟随 Agent。
func migration202610260101() *gormigrate.Migration {
	return &gormigrate.Migration{ID: "202610260101", Migrate: func(tx *gorm.DB) error {
		if err := tx.Exec("ALTER TABLE chat_sessions ADD COLUMN cli_model_mode TEXT NOT NULL DEFAULT ''").Error; err != nil {
			return err
		}
		return tx.Exec("ALTER TABLE chat_sessions ADD COLUMN cli_model TEXT NOT NULL DEFAULT ''").Error
	}}
}
