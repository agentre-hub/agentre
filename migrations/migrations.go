// Package migrations 汇总并执行 Agentre 桌面端 SQLite 数据库的全部迁移。
//
// 规范：
//   - 文件名前缀 = 时间戳排序键（YYYYMMDDNNNN），调用顺序按时间升序。
//   - 每个迁移返回一个 *gormigrate.Migration，包含 Migrate 与可选的 Rollback。
//   - 一次迁移只做一件事；新增表、加列、加索引各自独立成文件，方便回滚和 git bisect。
//   - DDL 优先使用原生 SQL，避免依赖 GORM AutoMigrate 的隐式行为。
package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// RunMigrations 执行全部迁移。新增迁移时把构造函数追加到 migrationList 末尾。
func RunMigrations(db *gorm.DB) error {
	m := gormigrate.New(db, gormigrate.DefaultOptions, migrationList())
	return m.Migrate()
}

// migrationList 按时间升序列出全部迁移构造函数。
func migrationList() []*gormigrate.Migration {
	return []*gormigrate.Migration{
		migration202608080001(),
		migration202608080002(),
		migration202608080003(),
		migration202608080004(),
		migration202608080005(),
		migration202608080006(),
		migration202608080007(),
		migration202608080008(),
		migration202608080009(),
		migration202608080010(),
		migration202608080011(),
		migration202608080012(),
	}
}
