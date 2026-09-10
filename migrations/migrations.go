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
//
// 当前这一批是 2026-09-04 发布前压缩出来的基线：产品尚未发布，全部长活的开发库 /
// 联调库一律删库重建，因此每个领域只留一条「直接建最终形态」的迁移，不再保留任何
// 补丁或回填迁移。
//
// 新迁移的 id 必须**大于历史上用过的任何一个**，而不是「文件列表里最大的那个 +1」：
// 账本 id 一旦落进过谁的库就永久退役，删掉文件也收不回来。2026-08-28 压缩未发布迁移
// 时空出的 202608080013~202608080018 就是这样一段号 —— 长活的开发库账本里有它们，
// 后来的迁移复用了这个号段，gormigrate 见到 id 已在账本便静默跳过，老库因此缺列。
// 本轮压缩同理退役了 202608080001~202608080012、202609010001 与 202609040001~
// 202609040006，所以基线另起 202609040101 这一段（回归见
// internal/bootstrap/cago_test.go 的 retiredMigrationLedgerIDs 用例）。取当天日期编号即可。
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
	}
}
