package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611110103 为会话追加记忆快照两列（spec 2026-11-11 决策 11）：快照正文与
// 「首个回合已取过」标记。新列对已有行一律是「还没取过」，哪些老会话算升级前已开始、
// 不再带记忆，由紧随其后的 202611110104 回填。
func migration202611110103() *gormigrate.Migration {
	return &gormigrate.Migration{ID: "202611110103", Migrate: func(tx *gorm.DB) error {
		if err := tx.Exec("ALTER TABLE chat_sessions ADD COLUMN memory_snapshot TEXT NOT NULL DEFAULT ''").Error; err != nil {
			return err
		}
		return tx.Exec("ALTER TABLE chat_sessions ADD COLUMN memory_snapshot_taken BOOLEAN NOT NULL DEFAULT 0").Error
	}}
}
