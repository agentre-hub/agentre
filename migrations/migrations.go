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

// migrationList 按时间升序列出全部迁移构造函数。新增迁移取当天日期编号,追加在末尾。
func migrationList() []*gormigrate.Migration {
	return []*gormigrate.Migration{
		migration202609040101(),
		migration202609040102(),
		migration202609040103(),
		migration202609040104(),
		migration202609040105(),
		migration202609040106(),
		migration202609040107(),
		migration202609040108(),
		migration202609040109(),
		migration202609040110(),
		migration202609040111(),
		migration202609040112(),
		migration202609040113(),
		migration202609060101(),
		migration202609080201(),
	}
}
