package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609010001 为两张表加上 conversation_id 列(规格「迁移与回填 / agentred」
// 的第一步:**只加列,不动数据、不动约束**)。
//
// 结构与回填/重建刻意分成两个迁移(develop.md "When Touching Persistent Data" 第 3 步):
// 合在一起时,一次失败分不清是 DDL 的错还是数据的错。
//
// 列先落成 NOT NULL DEFAULT ”:空串 = 「这一行还没回填」,是下一个迁移唯一的待办判据,
// 也让它可以分批、可以重跑(见 migration202609010002)。真正的唯一性由那一步重建主键时
// 才建立 —— 此刻库里全是空串,任何约束都建不起来。
//
// 迁移体自己包一层 tx.Transaction:gormigrate.DefaultOptions.UseTransaction 为 false
// (见 RunMigrations),它交给 Migrate 的 tx 就是裸库句柄,begin/commit 都是 no-op。
// 两条 ALTER 中途失败会留下半迁移状态且不写 migrations 台账行。
func migration202609010001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609010001",
		Migrate: func(tx *gorm.DB) error {
			return tx.Transaction(func(tx *gorm.DB) error {
				for _, table := range []string{"daemon_sessions", "daemon_notification_journal"} {
					// 加列是可重入的:台账行没写成而迁移体已提交时会重跑一次。
					if tx.Migrator().HasColumn(table, "conversation_id") {
						continue
					}
					if err := tx.Exec(
						`ALTER TABLE ` + table + ` ADD COLUMN conversation_id TEXT NOT NULL DEFAULT ''`,
					).Error; err != nil {
						return err
					}
				}
				return nil
			})
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Transaction(func(tx *gorm.DB) error {
				for _, table := range []string{"daemon_sessions", "daemon_notification_journal"} {
					if !tx.Migrator().HasColumn(table, "conversation_id") {
						continue
					}
					if err := tx.Exec(`ALTER TABLE ` + table + ` DROP COLUMN conversation_id`).Error; err != nil {
						return err
					}
				}
				return nil
			})
		},
	}
}
