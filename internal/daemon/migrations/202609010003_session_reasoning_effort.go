package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609010003 给 daemon_sessions 加上 reasoning_effort —— 这条会话在本机
// 记下的思考力度(规格 2026-09-01「agentred 侧的会话行」)。空串 = 跟随后端配置,也正是
// 全部老行的默认值:加列前后行为一字不差。
//
// 与同为会话级覆盖镜像的 provider_key / model_key 同形(TEXT NOT NULL DEFAULT ”),
// 也与它们同一条纪律:只供显示,执行路径不读它 —— 本轮用哪一档由 runtime.run 的
// run 参数决定。取值词表由发起端把关,不在 DDL 上加 CHECK:档位表会随后端能力演进,
// 写死在表结构里改一次要重写整张表。
func migration202609010003() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609010003",
		Migrate: func(tx *gorm.DB) error {
			// 加列是可重入的:台账行没写成而迁移体已提交时会重跑一次。
			if tx.Migrator().HasColumn("daemon_sessions", "reasoning_effort") {
				return nil
			}
			return tx.Exec(`ALTER TABLE daemon_sessions ADD COLUMN reasoning_effort TEXT NOT NULL DEFAULT ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if !tx.Migrator().HasColumn("daemon_sessions", "reasoning_effort") {
				return nil
			}
			return tx.Exec(`ALTER TABLE daemon_sessions DROP COLUMN reasoning_effort`).Error
		},
	}
}
