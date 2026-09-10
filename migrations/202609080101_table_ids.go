package migrations

import (
	"fmt"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609080101 给库里剩下的六张表补上自增的 id 主键，把库级约定补齐成
// 「每一张表的行身份都是一个与业务取值无关的数字」（回归见 migrations_test.go 的
// TestEveryTableHasAutoIncrementIDPrimaryKey）。
//
// 六张表分三类,缺的是同一样东西:
//
//   - chat_message_blocks / chat_frame_seqs 根本没声明主键,一直靠 SQLite 的隐式
//     rowid 认行。补上 id 之后 rowid 只是有了名字 —— 这两张表的行身份没有变化。
//   - project_agents / issue_labels 的主键是复合自然键,app_settings 的主键是 key。
//     它们降级为 UNIQUE 索引:表达的唯一性照旧成立,现有的 upsert 冲突目标(app_settings
//     的 ON CONFLICT(key))、按自然键的 WHERE 查询因此都不必改写。
//   - server_state 的 id 是主键但不自增 —— 它是靠 `DEFAULT 1 CHECK (id = 1)` 把自己
//     钉成单行表的。id 改为自增后这条约束就没有着落了,单行语义改由新列 singleton
//     承担(`CHECK (singleton = 1)` + UNIQUE):第二行插不进来这件事照旧成立,而 id
//     不再兼职表达它。singleton 不进实体 —— 它是 DDL 上的一条约束,不是业务字段。
//
// SQLite 加不了 AUTOINCREMENT 列,六张表一律建新表搬行再改名。搬行只搬原有列,id 由
// 自增自己分配(server_state 例外:它那一行的 id=1 原样搬过去,repo 按 id=1 定位的
// 读写路径因此照旧成立)。DROP TABLE 会连带删掉索引,所以每张表改名后重建全部索引。
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
	// seed 是搬完行后补的兜底语句，只有 server_state 用得上。
	seed string
}

// rebuildTables 逐张执行重建。每张表的四步都在同一条迁移事务里，任一步失败整条迁移回滚。
func rebuildTables(tx *gorm.DB, specs []tableRebuild) error {
	for _, spec := range specs {
		tmp := spec.name + "_v2"
		stmts := make([]string, 0, 5+len(spec.indexes))
		stmts = append(stmts,
			fmt.Sprintf("CREATE TABLE %s (\n%s\n)", tmp, spec.ddl),
			fmt.Sprintf("INSERT INTO %s (%s) SELECT %s FROM %s", tmp, spec.carried, spec.carried, spec.name),
			fmt.Sprintf("DROP TABLE %s", spec.name),
			fmt.Sprintf("ALTER TABLE %s RENAME TO %s", tmp, spec.name),
		)
		stmts = append(stmts, spec.indexes...)
		if spec.seed != "" {
			stmts = append(stmts, spec.seed)
		}
		for _, stmt := range stmts {
			if err := tx.Exec(stmt).Error; err != nil {
				return fmt.Errorf("rebuild %s: %w", spec.name, err)
			}
		}
	}
	return nil
}

// tablesWithIDPrimaryKey 是六张表补上 id 主键之后的形态。
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
			carried: "message_id, idx, type, tool_call_id, codec, data",
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
			carried: "session_id, message_id, block_idx, ordinal, seq",
			indexes: chatFrameSeqIndexes(),
		},
		{
			name: "project_agents",
			ddl: `	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL,
	agent_id INTEGER NOT NULL,
	joined_at INTEGER NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0,
	UNIQUE (project_id, agent_id)`,
			carried: projectAgentColumns,
			indexes: projectAgentIndexes(),
		},
		{
			name: "issue_labels",
			ddl: `	id INTEGER PRIMARY KEY AUTOINCREMENT,
	issue_id INTEGER NOT NULL,
	label_id INTEGER NOT NULL,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0,
	UNIQUE (issue_id, label_id)`,
			carried: issueLabelColumns,
			indexes: issueLabelIndexes(),
		},
		{
			name: "app_settings",
			ddl: `	id INTEGER PRIMARY KEY AUTOINCREMENT,
	key TEXT NOT NULL UNIQUE,
	value TEXT NOT NULL,
	updatetime INTEGER NOT NULL DEFAULT 0`,
			carried: appSettingColumns,
		},
		{
			name: "server_state",
			ddl: `	id INTEGER PRIMARY KEY AUTOINCREMENT,
	singleton INTEGER NOT NULL DEFAULT 1 CHECK (singleton = 1) UNIQUE,
	server_url TEXT NOT NULL DEFAULT '',
	device_id INTEGER NOT NULL DEFAULT 0,
	device_fingerprint TEXT NOT NULL DEFAULT '',
	server_user_id INTEGER NOT NULL DEFAULT 0,
	keychain_account TEXT NOT NULL DEFAULT '',
	updatetime INTEGER NOT NULL DEFAULT 0`,
			carried: serverStateColumns,
			// 空库（迁移链一次跑完，基线那条 INSERT 的行已随旧表消失）也要留下那一行：
			// repo 的 Get/Save 都按 id=1 认它。
			seed: "INSERT OR IGNORE INTO server_state (id, singleton) VALUES (1, 1)",
		},
	}
}

// tablesWithoutIDPrimaryKey 是六张表的原形态，供回滚重建。
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
			carried: "message_id, idx, type, tool_call_id, codec, data",
			indexes: chatMessageBlockIndexes(),
		},
		{
			name: "chat_frame_seqs",
			ddl: `	session_id INTEGER NOT NULL,
	message_id INTEGER NOT NULL,
	block_idx INTEGER NOT NULL,
	ordinal INTEGER NOT NULL,
	seq INTEGER NOT NULL`,
			carried: "session_id, message_id, block_idx, ordinal, seq",
			indexes: chatFrameSeqIndexes(),
		},
		{
			name: "project_agents",
			ddl: `	project_id INTEGER NOT NULL,
	agent_id INTEGER NOT NULL,
	joined_at INTEGER NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0,
	PRIMARY KEY (project_id, agent_id)`,
			carried: projectAgentColumns,
			indexes: projectAgentIndexes(),
		},
		{
			name: "issue_labels",
			ddl: `	issue_id INTEGER NOT NULL,
	label_id INTEGER NOT NULL,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0,
	PRIMARY KEY (issue_id, label_id)`,
			carried: issueLabelColumns,
			indexes: issueLabelIndexes(),
		},
		{
			name: "app_settings",
			ddl: `	key TEXT PRIMARY KEY,
	value TEXT NOT NULL,
	updatetime INTEGER NOT NULL DEFAULT 0`,
			carried: appSettingColumns,
		},
		{
			name: "server_state",
			ddl: `	id INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
	server_url TEXT NOT NULL DEFAULT '',
	device_id INTEGER NOT NULL DEFAULT 0,
	device_fingerprint TEXT NOT NULL DEFAULT '',
	server_user_id INTEGER NOT NULL DEFAULT 0,
	keychain_account TEXT NOT NULL DEFAULT '',
	updatetime INTEGER NOT NULL DEFAULT 0`,
			carried: serverStateColumns,
			seed:    "INSERT OR IGNORE INTO server_state (id) VALUES (1)",
		},
	}
}

// 两侧共用的搬行列清单与索引 —— 这一轮只动主键，列与索引两侧逐字相同。
const (
	projectAgentColumns = "project_id, agent_id, joined_at, sync_id, sync_account_id, " +
		"sync_version, sync_updated_at, sync_origin_fingerprint, sync_deleted_at"
	issueLabelColumns = "issue_id, label_id, sync_id, sync_account_id, " +
		"sync_version, sync_updated_at, sync_origin_fingerprint, sync_deleted_at"
	appSettingColumns  = "key, value, updatetime"
	serverStateColumns = "id, server_url, device_id, device_fingerprint, " +
		"server_user_id, keychain_account, updatetime"
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

func projectAgentIndexes() []string {
	return []string{
		`CREATE INDEX idx_project_agents_agent_id ON project_agents(agent_id)`,
		`CREATE UNIQUE INDEX uniq_project_agents_sync_id ON project_agents(sync_id) WHERE sync_id != ''`,
	}
}

func issueLabelIndexes() []string {
	return []string{
		`CREATE INDEX idx_issue_labels_label ON issue_labels(label_id)`,
		`CREATE UNIQUE INDEX uniq_issue_labels_sync_id ON issue_labels(sync_id) WHERE sync_id != ''`,
	}
}
