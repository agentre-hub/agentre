package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609090101 给 agentred 库的 chat_messages 补上 turn_trigger 列。
//
// DDL 逐字取自桌面端（migrations/202609090101_chat_message_turn_trigger.go），语义与
// 落库理由见那一份。两个宿主共用同一份 transcript_entity / transcript_repo 代码，只补
// 一边就是「同一行 INSERT 在两台机器上写出两种结果」——桌面端全绿，agentred 上每一次
// 写消息都被 SQLite 以 "table chat_messages has no column named turn_trigger" 拒掉。
// 两个进程各一个库，所以这里同样是**复制 DDL** 而不是共享一张表；
// TestTranscriptSchemaParityAcrossHosts 是那个把关人。
func migration202609090101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609090101",
		Migrate: func(tx *gorm.DB) error {
			if tx.Migrator().HasColumn("chat_messages", "turn_trigger") {
				return nil
			}
			return tx.Exec(
				`ALTER TABLE chat_messages ADD COLUMN turn_trigger TEXT NOT NULL DEFAULT ''`,
			).Error
		},
		// SQLite 3.35+ 支持 DROP COLUMN；没有索引引用该列。
		Rollback: func(tx *gorm.DB) error {
			if !tx.Migrator().HasColumn("chat_messages", "turn_trigger") {
				return nil
			}
			return tx.Exec(`ALTER TABLE chat_messages DROP COLUMN turn_trigger`).Error
		},
	}
}
