package migrations_test

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	daemonmigrations "github.com/agentre-hub/agentre/internal/daemon/migrations"
	desktopmigrations "github.com/agentre-hub/agentre/migrations"
)

// transcriptTables 是两个宿主必须同形的三张转录表。
//
// 它们由**同一份** internal/model/entity/transcript_entity 与
// internal/repository/transcript_repo 代码读写,但落在两个进程的两个 SQLite 库上:
// 桌面端由顶层 migrations/ 建,agentred 由 internal/daemon/migrations/ 手抄一份建
// (migration202609060201 的注释写着「逐字取自桌面端」)。手抄没有编译器把关 ——
// 漂一格,桌面端全绿,只在 agentred 上运行时炸。这条守卫就是那个把关人。
var transcriptTables = []string{"chat_messages", "chat_message_blocks", "chat_frame_seqs"}

// TestTranscriptSchemaParityAcrossHosts 钉死两个宿主跑完**全部**迁移之后,这三张表的
// 最终形态一致。
//
// 比的是最终形态而不是某两条迁移的产物:后续任何一轮改表(补主键、加列、换索引)都得
// 两边一起改,只改一边就在这里判红。
//
// 比的是**规范化结构**而不是 DDL 文本:空白、IF NOT EXISTS、列的声明顺序都不是「同一行
// 代码在两台机器上写出两种结果」的成因,文本比对会被它们误报;真正要钉的是列(类型 /
// 非空 / 默认值 / 主键位次)、索引(列序 / 升降序 / 唯一性 / 部分索引谓词)与表级的
// AUTOINCREMENT / WITHOUT ROWID。
func TestTranscriptSchemaParityAcrossHosts(t *testing.T) {
	desktop := migrated(t, "agentre.db", desktopmigrations.RunMigrations)
	daemon := migrated(t, "agentred.db", daemonmigrations.RunMigrations)

	for _, table := range transcriptTables {
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
			t.Errorf("transcript schema drift, %s", line)
		}
	}
}

// migrated 开一个空的临时库并跑完一整条迁移链。
func migrated(t *testing.T, name string, run func(*gorm.DB) error) *gorm.DB {
	t.Helper()
	gormDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), name)), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite %s: %v", name, err)
	}
	if err := run(gormDB); err != nil {
		t.Fatalf("RunMigrations() on %s error = %v", name, err)
	}
	return gormDB
}

// tableShape 是一张表的规范化形态。列与索引都按名字排序 —— 声明顺序不进比对口径。
type tableShape struct {
	Table         string
	AutoIncrement bool
	WithoutRowID  bool
	Columns       []columnShape
	Indexes       []indexShape
}

type columnShape struct {
	Name    string
	Type    string
	NotNull bool
	Default string // "<none>" = 没有 DEFAULT 子句
	PKOrder int    // 0 = 不在主键里,1.. = 主键里的位次
}

func (c columnShape) String() string {
	out := fmt.Sprintf("%s %s", c.Name, c.Type)
	if c.NotNull {
		out += " NOT NULL"
	}
	if c.Default != "<none>" {
		out += " DEFAULT " + c.Default
	}
	if c.PKOrder > 0 {
		out += fmt.Sprintf(" PRIMARY KEY(#%d)", c.PKOrder)
	}
	return out
}

type indexShape struct {
	Name    string
	Unique  bool
	Origin  string // c = CREATE INDEX, u = UNIQUE 约束, pk = 主键
	Where   string // 部分索引谓词,"" = 全表索引
	Columns string // "session_id, last_message_at DESC"
}

func (i indexShape) String() string {
	out := "INDEX"
	if i.Unique {
		out = "UNIQUE INDEX"
	}
	out += fmt.Sprintf(" (%s) origin=%s", i.Columns, i.Origin)
	if i.Where != "" {
		out += " WHERE " + i.Where
	}
	return out
}

// diffTableShapes 把两份形态的差异摊成一行一条、指名道姓的说明:哪张表、哪一列 / 哪个
// 索引对不上,两边各是什么。不丢整块 diff —— 这条守卫红的时候,人要能直接照着去改 DDL。
func diffTableShapes(desktop, daemon tableShape) []string {
	var out []string
	table := desktop.Table

	if desktop.AutoIncrement != daemon.AutoIncrement {
		out = append(out, fmt.Sprintf("table %s: AUTOINCREMENT desktop=%t agentred=%t", table, desktop.AutoIncrement, daemon.AutoIncrement))
	}
	if desktop.WithoutRowID != daemon.WithoutRowID {
		out = append(out, fmt.Sprintf("table %s: WITHOUT ROWID desktop=%t agentred=%t", table, desktop.WithoutRowID, daemon.WithoutRowID))
	}

	wantCols := map[string]columnShape{}
	for _, c := range desktop.Columns {
		wantCols[c.Name] = c
	}
	gotCols := map[string]columnShape{}
	for _, c := range daemon.Columns {
		gotCols[c.Name] = c
	}
	for _, name := range sortedKeysOfBoth(wantCols, gotCols) {
		want, wantOK := wantCols[name]
		got, gotOK := gotCols[name]
		switch {
		case !gotOK:
			out = append(out, fmt.Sprintf("table %s: column %q exists only on the desktop (%s)", table, name, want))
		case !wantOK:
			out = append(out, fmt.Sprintf("table %s: column %q exists only on agentred (%s)", table, name, got))
		case want != got:
			out = append(out, fmt.Sprintf("table %s: column %q differs\n  desktop:  %s\n  agentred: %s", table, name, want, got))
		}
	}

	wantIdx := map[string]indexShape{}
	for _, i := range desktop.Indexes {
		wantIdx[i.Name] = i
	}
	gotIdx := map[string]indexShape{}
	for _, i := range daemon.Indexes {
		gotIdx[i.Name] = i
	}
	for _, name := range sortedKeysOfBoth(wantIdx, gotIdx) {
		want, wantOK := wantIdx[name]
		got, gotOK := gotIdx[name]
		switch {
		case !gotOK:
			out = append(out, fmt.Sprintf("table %s: index %q exists only on the desktop (%s)", table, name, want))
		case !wantOK:
			out = append(out, fmt.Sprintf("table %s: index %q exists only on agentred (%s)", table, name, got))
		case want != got:
			out = append(out, fmt.Sprintf("table %s: index %q differs\n  desktop:  %s\n  agentred: %s", table, name, want, got))
		}
	}
	return out
}

func sortedKeysOfBoth[V any](a, b map[string]V) []string {
	seen := map[string]struct{}{}
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// whereClauseRE 从一条 CREATE INDEX 语句里摘出部分索引的谓词。
// PRAGMA index_list 只说一个索引是不是部分索引(partial=1),不交出谓词本身。
var whereClauseRE = regexp.MustCompile(`(?is)\sWHERE\s+(.*)$`)

// readTableShape 从库里读出一张表的规范化形态。第二个返回值为 false 表示表不存在。
func readTableShape(t *testing.T, gormDB *gorm.DB, table string) (tableShape, bool) {
	t.Helper()

	// sqlite_master 只用来取两件 PRAGMA 交不出来的事:表级的 AUTOINCREMENT /
	// WITHOUT ROWID,以及部分索引的谓词。其余维度一律走 PRAGMA。
	var objects []struct {
		Type string `gorm:"column:type"`
		Name string `gorm:"column:name"`
		SQL  string `gorm:"column:sql"`
	}
	if err := gormDB.Raw(
		`SELECT type, name, COALESCE(sql, '') AS sql FROM sqlite_master WHERE tbl_name = ?`, table,
	).Scan(&objects).Error; err != nil {
		t.Fatalf("read sqlite_master for %s: %v", table, err)
	}
	shape := tableShape{Table: table}
	found := false
	indexSQL := map[string]string{}
	for _, obj := range objects {
		switch obj.Type {
		case "table":
			found = true
			upper := strings.ToUpper(obj.SQL)
			shape.AutoIncrement = strings.Contains(upper, "AUTOINCREMENT")
			shape.WithoutRowID = strings.Contains(normalizeSpace(upper), "WITHOUT ROWID")
		case "index":
			indexSQL[obj.Name] = obj.SQL
		}
	}
	if !found {
		return tableShape{Table: table}, false
	}

	var cols []struct {
		Name    string `gorm:"column:name"`
		Type    string `gorm:"column:type"`
		NotNull int    `gorm:"column:not_null"`
		Dflt    string `gorm:"column:dflt"`
		PK      int    `gorm:"column:pk"`
	}
	if err := gormDB.Raw(
		`SELECT name, type, "notnull" AS not_null, COALESCE(dflt_value, '<none>') AS dflt, pk
FROM pragma_table_info(?) ORDER BY name`, table,
	).Scan(&cols).Error; err != nil {
		t.Fatalf("read pragma_table_info(%s): %v", table, err)
	}
	for _, c := range cols {
		shape.Columns = append(shape.Columns, columnShape{
			Name:    c.Name,
			Type:    strings.ToUpper(strings.TrimSpace(c.Type)),
			NotNull: c.NotNull != 0,
			Default: normalizeSpace(c.Dflt),
			PKOrder: c.PK,
		})
	}

	var idxCols []struct {
		Index  string `gorm:"column:index_name"`
		Column string `gorm:"column:column_name"`
		Desc   int    `gorm:"column:is_desc"`
	}
	if err := gormDB.Raw(
		`SELECT il.name AS index_name, COALESCE(xi.name, '<expr>') AS column_name, xi."desc" AS is_desc
FROM pragma_index_list(?) AS il
JOIN pragma_index_xinfo(il.name) AS xi
WHERE xi.key = 1
ORDER BY il.name, xi.seqno`, table,
	).Scan(&idxCols).Error; err != nil {
		t.Fatalf("read pragma_index_xinfo(%s): %v", table, err)
	}
	columnsOf := map[string][]string{}
	for _, c := range idxCols {
		col := c.Column
		if c.Desc != 0 {
			col += " DESC"
		}
		columnsOf[c.Index] = append(columnsOf[c.Index], col)
	}

	var idxs []struct {
		Name   string `gorm:"column:name"`
		Unique int    `gorm:"column:is_unique"`
		Origin string `gorm:"column:origin"`
	}
	if err := gormDB.Raw(
		`SELECT name, "unique" AS is_unique, origin FROM pragma_index_list(?) ORDER BY name`, table,
	).Scan(&idxs).Error; err != nil {
		t.Fatalf("read pragma_index_list(%s): %v", table, err)
	}
	for _, i := range idxs {
		where := ""
		if m := whereClauseRE.FindStringSubmatch(indexSQL[i.Name]); m != nil {
			where = normalizeSpace(m[1])
		}
		shape.Indexes = append(shape.Indexes, indexShape{
			Name:    i.Name,
			Unique:  i.Unique != 0,
			Origin:  i.Origin,
			Where:   where,
			Columns: strings.Join(columnsOf[i.Name], ", "),
		})
	}
	return shape, true
}

var spaceRE = regexp.MustCompile(`\s+`)

func normalizeSpace(s string) string {
	return strings.TrimSpace(spaceRE.ReplaceAllString(s, " "))
}
