// Package migrations 汇总并执行 agentred 自己的 SQLite 数据库迁移。
//
// 与桌面端 migrations/ 各自独立、互不引用——agentred 是独立进程,有自己的库文件
// (见 daemon.New),不共享桌面端的 chat_sessions 等表。
//
// 规范同桌面端:
//   - 文件名前缀 = 时间戳排序键(YYYYMMDDNNNN),调用顺序按时间升序。
//   - 每个迁移返回一个 *gormigrate.Migration,包含 Migrate 与可选的 Rollback。
//   - 一次迁移只做一件事;DDL 优先使用原生 SQL,避免依赖 GORM AutoMigrate 的隐式行为。
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
		migration202608080011(),
		migration202609010001(),
		migration202609010002(),
	}
}
