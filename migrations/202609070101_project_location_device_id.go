package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609070101 给 project_locations 补上 device_id 列。
//
// 202609040105 建这张表时漏了它，而 project_location_entity.ProjectLocation 一直声明着
// `column:device_id`。读不受影响（SELECT * 不按名引用该列，扫进结构体时留空），但写会带
// 上它 —— 于是在任何一个走这条迁移链建出来的库上，「给别的机器配项目路径」的每一次
// 写入都被 SQLite 以 "table project_locations has no column named device_id" 拒掉，功能
// 整个不可用。
//
// 语义按实体包注释：device_id 是**由指纹解析出的本地缓存**（空串，或 paired_agentreds.id
// 的字符串形式），不是身份、也不受唯一约束管辖 —— 自然键仍是 (project, fingerprint)。
// 所以补列不需要回填：已有行留空即「本机配对表里当前解析不出这个指纹」，正是它该表达的
// 状态，配对后由 Upsert 自愈回填。
//
// 单独一条补丁迁移而不是改 202609040105：已发出去的迁移不再改动（AGENTS.md）。
func migration202609070101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609070101",
		Migrate: func(tx *gorm.DB) error {
			if tx.Migrator().HasColumn("project_locations", "device_id") {
				return nil
			}
			return tx.Exec(
				`ALTER TABLE project_locations ADD COLUMN device_id TEXT NOT NULL DEFAULT ''`,
			).Error
		},
		// SQLite 3.35+ 支持 DROP COLUMN；这张表上没有引用该列的索引。
		Rollback: func(tx *gorm.DB) error {
			if !tx.Migrator().HasColumn("project_locations", "device_id") {
				return nil
			}
			return tx.Exec(`ALTER TABLE project_locations DROP COLUMN device_id`).Error
		},
	}
}
