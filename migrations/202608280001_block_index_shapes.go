package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608280001 把 chat_message_blocks 的两个索引补到「已经跑过
// 202608270002 的库」上。
//
// 202608270002 建块表时，定位索引只有 (tool_call_id, type) 两列、也没有
// (type, message_id)。收尾评审把这两处改进了那条迁移的源文件——但 gormigrate 只
// 跑 ID 不在 migrations 表里的迁移，所以任何**已经迁过**的库一行都不会执行到那些
// 改动：新装的库拿到的是修好的形状，老库停在旧形状，同一份迁移文件描述出两种库。
// 既有迁移不可修改（AGENTS.md 第 7 条），这里追加一条把两边收敛到同一形状。
//
// 少了这两个索引不会算错，只是慢：定位那条查询靠 (tool_call_id, type) 仍能等值命中，
// 但要额外排一次序；按类型取块则只能沿 (message_id, idx) 把整条会话的块读出来再逐行
// 丢掉。实测在 88 万块的真实库上，按类型取一次数 16.6ms → 7.9ms。
//
// DROP + CREATE 而不是只 CREATE：老库里那个索引名已经被两列形状占着，
// `CREATE INDEX IF NOT EXISTS` 会因为同名而静默跳过，形状永远换不过来。对新装的库
// 这一对操作把同一个定义删了再建一遍，结果不变。
func migration202608280001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608280001",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP INDEX IF EXISTS idx_chat_message_blocks_tool_call`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX idx_chat_message_blocks_tool_call
ON chat_message_blocks(tool_call_id, type, message_id) WHERE tool_call_id != ''`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_chat_message_blocks_type_message
ON chat_message_blocks(type, message_id)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return nil
		},
	}
}
