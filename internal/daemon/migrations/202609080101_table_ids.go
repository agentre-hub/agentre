package migrations

import (
	"fmt"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609080101 给 agentred 库里剩下的两张表补上自增的 id 主键，把库级约定补齐
// 成「每一张表的行身份都是一个与业务取值无关的数字」（回归见 migrations_test.go 的
// TestEveryTableHasAutoIncrementIDPrimaryKey）。
//
// chat_message_blocks / chat_frame_seqs 从来没声明过主键，一直靠 SQLite 的隐式 rowid
// 认行；补上 id 之后 rowid 只是有了名字，两张表的行身份没有变化。(message_id, idx)
// 与 (session_id, seq) 照旧是唯一索引，写路径的冲突目标因此不必改写。
//
// 这两张表的 DDL 逐字取自桌面端（migrations/202609080101_table_ids.go）—— 两个宿主
// 共用同一份实体与仓储代码，表结构错一格就是同一行代码在两台机器上写出两种结果。
// 两个进程各一个库，所以这里同样是**复制 DDL**而不是共享一张表。
//
// SQLite 加不了 AUTOINCREMENT 列，两张表都建新表搬行再改名；DROP TABLE 会连带删掉
// 索引，所以改名后重建全部索引。
func migration202609080101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609080101",
		Migrate: func(tx *gorm.DB) error {
			return rebuildTables(tx, tablesWithIDPrimaryKey())
		},
		Rollback: func(tx *gorm.DB) error {
			return rebuildTables(tx, tablesWithoutIDPrimaryKey())
		},
	}
}

// tableRebuild 描述一次「建新表 → 搬行 → 换名 → 重建索引」。
type tableRebuild struct {
	// name 是表名；重建期间的临时表叫 name + "_v2"。
	name string
	// ddl 是新表的列与表级约束，不含最外层的 CREATE TABLE 与括号。
	ddl string
	// carried 是搬行时逐字列出的列，两侧同名同序。
	carried string
	// indexes 是改名后重建的索引语句。
	indexes []string
}

// rebuildTables 逐张执行重建。每张表的四步都在同一条迁移事务里，任一步失败整条迁移回滚。
func rebuildTables(tx *gorm.DB, specs []tableRebuild) error {
	for _, spec := range specs {
		tmp := spec.name + "_v2"
		stmts := make([]string, 0, 4+len(spec.indexes))
		stmts = append(stmts,
			fmt.Sprintf("CREATE TABLE %s (\n%s\n)", tmp, spec.ddl),
			fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s", tmp, spec.carried, spec.carried, spec.name),
			fmt.Sprintf("DROP TABLE %s", spec.name),
			fmt.Sprintf("ALTER TABLE %s RENAME TO %s", tmp, spec.name),
		)
		stmts = append(stmts, spec.indexes...)
		for _, stmt := range stmts {
			if err := tx.Exec(stmt).Error; err != nil {
				return fmt.Errorf("rebuild %s: %w", spec.name, err)
			}
		}
	}
	return nil
}

// tablesWithIDPrimaryKey 是两张表补上 id 主键之后的形态。
func tablesWithIDPrimaryKey() []tableRebuild {
	return []tableRebuild{
		{
			name: "chat_message_blocks",
			ddl: `	id INTEGER PRIMARY KEY AUTOINCREMENT,
	message_id INTEGER NOT NULL,
	idx INTEGER NOT NULL,
	type TEXT NOT NULL DEFAULT '',
	tool_call_id TEXT NOT NULL DEFAULT '',
	codec INTEGER NOT NULL DEFAULT 0,
	data BLOB NOT NULL`,
			carried: messageBlockColumns,
			indexes: chatMessageBlockIndexes(),
		},
		{
			name: "chat_frame_seqs",
			ddl: `	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	block_idx INTEGER NOT NULL,
	ordinal INTEGER NOT NULL,
	seq INTEGER NOT NULL`,
			carried: frameSeqColumns,
			indexes: chatFrameSeqIndexes(),
		},
	}
}

// tablesWithoutIDPrimaryKey 是两张表的原形态，供回滚重建。
func tablesWithoutIDPrimaryKey() []tableRebuild {
	return []tableRebuild{
		{
			name: "chat_message_blocks",
			ddl: `	message_id INTEGER NOT NULL,
	idx INTEGER NOT NULL,
	type TEXT NOT NULL DEFAULT '',
	tool_call_id TEXT NOT NULL DEFAULT '',
	codec INTEGER NOT NULL DEFAULT 0,
	data BLOB NOT NULL`,
			carried: messageBlockColumns,
			indexes: chatMessageBlockIndexes(),
		},
		{
			name: "chat_frame_seqs",
			ddl: `	session_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	block_idx INTEGER NOT NULL,
	ordinal INTEGER NOT NULL,
	seq INTEGER NOT NULL`,
			carried: frameSeqColumns,
			indexes: chatFrameSeqIndexes(),
		},
	}
}

// 两侧共用的搬行列清单 —— 这一轮只动主键，列与索引两侧逐字相同。
const (
	messageBlockColumns = "message_id, idx, type, tool_call_id, codec, data"
	frameSeqColumns     = "session_id, message_id, block_idx, ordinal, seq"
)

func chatMessageBlockIndexes() []string {
	return []string{
		`CREATE UNIQUE INDEX ux_chat_message_blocks_message_idx ON chat_message_blocks(message_id, idx)`,
		`CREATE INDEX idx_chat_message_blocks_tool_call ON chat_message_blocks(tool_call_id, type, message_id) WHERE tool_call_id != ''`,
		`CREATE INDEX idx_chat_message_blocks_type_message ON chat_message_blocks(type, message_id)`,
	}
}

func chatFrameSeqIndexes() []string {
	return []string{
		`CREATE UNIQUE INDEX ux_chat_frame_seqs_session_seq ON chat_frame_seqs(session_id, seq)`,
		`CREATE INDEX idx_chat_frame_seqs_session_frame ON chat_frame_seqs(session_id, message_id, block_idx, ordinal)`,
	}
}
