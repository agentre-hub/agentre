package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608200001 给 chat_messages 加上首 token 与输出速度，供消息脚注刷新后仍能显示。
func migration202608200001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608200001",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`ALTER TABLE chat_messages ADD COLUMN first_token_ms INTEGER NOT NULL DEFAULT 0`).Error; err != nil {
				return err
			}
			return tx.Exec(`ALTER TABLE chat_messages ADD COLUMN tokens_per_sec REAL NOT NULL DEFAULT 0`).Error
		},
	}
}
