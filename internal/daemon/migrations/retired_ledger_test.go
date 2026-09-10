package migrations_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	daemonmigrations "github.com/agentre-hub/agentre/internal/daemon/migrations"
)

// retiredMigrationLedgerIDs 是 agentred 侧曾经落进过开发机账本、如今文件已不存在的迁移号。
//
// 前三个是 2026-09-04 发布前压缩未发布迁移时退役的那一批；后两个是 2026-09-10 为 0.1.0
// 首发把补丁折进建表语句时退役的。账本只记 id，删文件收不回号。
var retiredMigrationLedgerIDs = []string{
	"202608010001", "202608080011", "202608100001",
	"202609080101", "202609090101",
}

// TestRunMigrationsSkipsNothingOnALedgerHoldingRetiredIDs 钉死迁移号不得复用退役号。
//
// 事故形态：新迁移接着「文件列表里最大的那个」往下编号，而那个号段早被上一个纪元的迁移
// 占着 —— gormigrate 只认账本里的 id 字符串，于是新迁移被**静默跳过**：全新库一切正常，
// 长活的库缺表缺列，直到运行时才炸。桌面端真炸过两次；agentred 跑在用户的机器上，同样
// 的跳过在那里只会更难被发现（没有人盯着它的启动日志）。
//
// 这里把退役号预先填进账本再跑全链：当前每一条迁移都必须照跑不误，跑完的 schema 与全新
// 库一字不差。谁再复用一个退役号，他那条迁移的效果就会在这里整条消失。
func TestRunMigrationsSkipsNothingOnALedgerHoldingRetiredIDs(t *testing.T) {
	fresh := migrateForLedgerGuard(t, "fresh.db", nil)
	legacy := migrateForLedgerGuard(t, "legacy.db", retiredMigrationLedgerIDs)

	if want, got := schemaOf(t, fresh), schemaOf(t, legacy); want != got {
		t.Errorf("a database whose ledger carries retired migration ids did not converge to the fresh schema.\nfresh:\n%s\nlegacy:\n%s", want, got)
	}
}

// migrateForLedgerGuard 开一个空库，先把 ledgerIDs 播进 gormigrate 的账本（模拟一台长活
// 的开发机），再跑全链迁移。
func migrateForLedgerGuard(t *testing.T, name string, ledgerIDs []string) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite %s: %v", name, err)
	}
	if len(ledgerIDs) > 0 {
		// 账本表由 gormigrate 建；这里先建出来再播种，建表语句与它的一致（id 主键）。
		if err := gormDB.Exec(`CREATE TABLE migrations (id VARCHAR(255) PRIMARY KEY)`).Error; err != nil {
			t.Fatalf("create migration ledger: %v", err)
		}
		for _, id := range ledgerIDs {
			if err := gormDB.Exec(`INSERT INTO migrations (id) VALUES (?)`, id).Error; err != nil {
				t.Fatalf("seed retired ledger id %s: %v", id, err)
			}
		}
	}
	if err := daemonmigrations.RunMigrations(gormDB); err != nil {
		t.Fatalf("RunMigrations() on %s error = %v", name, err)
	}
	return gormDB
}

// schemaOf 交出一个库的全部 DDL（表、索引），按名字排序 —— 两个库比对的口径。
// 排除 sqlite_ 开头的内部对象与 migrations 账本表本身（账本内容本来就不同）。
func schemaOf(t *testing.T, gormDB *gorm.DB) string {
	t.Helper()
	var rows []struct {
		Name string `gorm:"column:name"`
		SQL  string `gorm:"column:sql"`
	}
	if err := gormDB.Raw(`SELECT name, COALESCE(sql, '') AS sql FROM sqlite_master
WHERE name NOT LIKE 'sqlite_%' AND name != 'migrations' ORDER BY name`).Scan(&rows).Error; err != nil {
		t.Fatalf("read sqlite_master: %v", err)
	}
	out := ""
	for _, row := range rows {
		out += fmt.Sprintf("%s: %s\n", row.Name, row.SQL)
	}
	return out
}
