package migrations_test

import (
	"testing"

	daemonmigrations "github.com/agentre-hub/agentre/internal/daemon/migrations"
	desktopmigrations "github.com/agentre-hub/agentre/migrations"
)

// portForwardTables 是两个宿主必须同形的一张表:设备端口转发映射(规格
// 2026-09-06「设备端口转发」)。它由**同一份** internal/model/entity/port_forward_entity
// 与 internal/repository/port_forward_repo 代码读写,但落在两个进程的两个 SQLite
// 库上:桌面端由顶层 migrations/ 建,agentred 由 internal/daemon/migrations/ 手抄
// 一份建(migration202609080201 的注释写着「逐字取自」)。手抄没有编译器把关——
// 漂一格,桌面端全绿,只在 agentred 上运行时炸,或者反过来。这条守卫就是那个把关人。
//
// 复用 transcript_parity_test.go 里的 migrated / readTableShape / diffTableShapes:
// 两条守卫比的是同一件事(两个宿主跑完全部迁移后,点名的几张表的规范化结构一致),
// 只是点名的表不同,没有理由为同一个比对逻辑另写一份。
var portForwardTables = []string{"port_forwards"}

// TestPortForwardSchemaParityAcrossHosts 钉死两个宿主跑完**全部**迁移之后,
// port_forwards 表的最终形态一致。比的是规范化结构(列的类型/非空/默认值/主键位次,
// 索引的列序/唯一性)而不是 DDL 文本——文本上的空白、IF NOT EXISTS、列的声明顺序
// 都不是「同一行代码在两台机器上写出两种结果」的成因。
func TestPortForwardSchemaParityAcrossHosts(t *testing.T) {
	desktop := migrated(t, "agentre.db", desktopmigrations.RunMigrations)
	daemon := migrated(t, "agentred.db", daemonmigrations.RunMigrations)

	for _, table := range portForwardTables {
		want, wantOK := readTableShape(t, desktop, table)
		got, gotOK := readTableShape(t, daemon, table)
		switch {
		case !wantOK && !gotOK:
			t.Errorf("table %s exists on neither host: the guard is watching a table nobody creates", table)
			continue
		case !wantOK:
			t.Errorf("table %s exists on agentred but not on the desktop", table)
			continue
		case !gotOK:
			t.Errorf("table %s exists on the desktop but not on agentred", table)
			continue
		}
		for _, line := range diffTableShapes(want, got) {
			t.Errorf("port forward schema drift, %s", line)
		}
	}
}
