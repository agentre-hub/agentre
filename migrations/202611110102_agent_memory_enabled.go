package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611110102 为 agents 追加记忆开关。默认开，已有的 Agent 也一并为开（决策 17）。
func migration202611110102() *gormigrate.Migration {
	return &gormigrate.Migration{ID: "202611110102", Migrate: func(tx *gorm.DB) error {
		return tx.Exec("ALTER TABLE agents ADD COLUMN memory_enabled BOOLEAN NOT NULL DEFAULT 1").Error
	}}
}
