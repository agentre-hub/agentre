package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611110104 把升级前已开始的会话标成「已取过、不带记忆」（spec 2026-11-11
// 「会话带上记忆」：升级前已开始的会话没有快照）。「已开始」= 已有消息；建好但一轮都
// 没跑过的会话保持未取，留到它的首个回合再取。只改标记，不写快照正文（仍为空串）。
func migration202611110104() *gormigrate.Migration {
	return &gormigrate.Migration{ID: "202611110104", Migrate: func(tx *gorm.DB) error {
		return tx.Exec("UPDATE chat_sessions SET memory_snapshot_taken = 1 " +
			"WHERE EXISTS (SELECT 1 FROM chat_messages WHERE chat_messages.session_id = chat_sessions.id)").Error
	}}
}
