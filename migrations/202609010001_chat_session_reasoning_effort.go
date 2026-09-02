package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609010001 给 chat_sessions 加上 reasoning_effort —— 会话级思考力度
// (spec 2026-09-01 决策 1)。空串 = 跟随该会话那一档 backend 的配置,也正是全部老行
// 的默认值:加列前后行为一字不差。
//
// 与同为会话级覆盖的 provider_key / model_key 同形(TEXT NOT NULL DEFAULT ”),
// 取值由 agent_backend_entity.IsValidReasoningEffort 在服务层把关,不在 DDL 上加
// CHECK —— 档位表会随后端能力演进,写死在表结构里改一次要重写整张表。
func migration202609010001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609010001",
		Migrate: func(tx *gorm.DB) error {
			if tx.Migrator().HasColumn("chat_sessions", "reasoning_effort") {
				return nil
			}
			return tx.Exec(`ALTER TABLE chat_sessions ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if !tx.Migrator().HasColumn("chat_sessions", "reasoning_effort") {
				return nil
			}
			return tx.Exec(`ALTER TABLE chat_sessions DROP COLUMN reasoning_effort`).Error
		},
	}
}
