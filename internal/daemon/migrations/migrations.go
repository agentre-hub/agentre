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
//
// 新迁移的 id 必须**大于历史上用过的任何一个**,而不是「文件列表里最大的那个 +1」:
// 账本 id 一旦落进过谁的库就永久退役,删掉文件也收不回来。gormigrate 见到 id 已在账本
// 便静默跳过,复用退役号会让老库悄悄缺表缺列(桌面端 migrations/migrations.go 记着这么
// 一次事故)。取当天日期编号即可。
//
// 已退役、永不复用的号:202608080011、202609010001、202609010002、202609010003 ——
// 2026-09-04 发布前压缩成基线迁移 202609040101 时退役(当时所有长活开发库/联调库一律
// 删库重建,故不保留补丁迁移)。
func migrationList() []*gormigrate.Migration {
	return []*gormigrate.Migration{
		migration202609040101(),
		migration202609060201(),
	}
}
