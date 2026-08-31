package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608080014 给 chat_sessions 加上 conversation_id —— 一条对话在桌面端、
// agentred 与 server 三套库以及线格式上的全局身份(spec 2026-08-31 决策 1)。
//
// 本迁移**只加列**,既不回填也不建唯一索引:回填是数据、加列是结构,合在一起时
// 一次失败分不清是 DDL 还是数据的错(develop.md「When Touching Persistent Data」
// 第 3 步)。回填与唯一索引在 202608080015。
//
// 自增主键 chat_sessions.id 原样保留(决策 12):它是 chat_messages 等表引用的本地
// 主键,SQLite 换主键类型要重写每一张带外键的表,而本地主键与全局标识本来就是
// 两件事。桌面端因此永久存在一层「conversation_id ↔ 本地主键」的翻译。
//
// 默认空串而不是 NULL:那一列上马上要建唯一索引,而 SQLite 的唯一索引不约束 NULL
// (每个 NULL 各自为政),留 NULL 等于让约束对没回填上的行静默失效。空串则会在
// 下一个迁移建索引时立刻撞车,失败得见得着。
func migration202608080014() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080014",
		Migrate: func(tx *gorm.DB) error {
			if tx.Migrator().HasColumn("chat_sessions", "conversation_id") {
				return nil
			}
			return tx.Exec(`ALTER TABLE chat_sessions ADD COLUMN conversation_id TEXT NOT NULL DEFAULT ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if !tx.Migrator().HasColumn("chat_sessions", "conversation_id") {
				return nil
			}
			return tx.Exec(`ALTER TABLE chat_sessions DROP COLUMN conversation_id`).Error
		},
	}
}
