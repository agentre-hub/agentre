package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611130101 建 chat_pending_deletes 表（spec 2026-11-12「删除传播」）：桌面端
// 删掉自己那份对话之后，server 或配对 agentred 那边没删成的那一笔记在这里，联系上再补发。
// 一条对话对同一个对象（server / agentred）只记一笔，所以 (conversation_id, target) 唯一。
// 纯新增表，不碰已有数据。
func migration202611130101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202611130101",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE TABLE IF NOT EXISTS chat_pending_deletes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	conversation_id TEXT NOT NULL,
	target TEXT NOT NULL,
	machine_fingerprint TEXT NOT NULL DEFAULT '',
	device_id BIGINT NOT NULL DEFAULT 0,
	createtime BIGINT NOT NULL DEFAULT 0
)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uniq_chat_pending_deletes_conversation_target ON chat_pending_deletes(conversation_id, target)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
