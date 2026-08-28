package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608270001 把 pi 转录替换的「恢复标记」清出 chat_messages。
//
// 标记从前借 chat_messages 的四个列表达自己:payload 借 blocks_json、查找键借
// device_id、状态借 model、角色用一个隐藏 role。本轮删掉 blocks_json 列(它的
// payload 就在那一列),标记改存 app_settings 的 chat.pi_recovery:<sessionID>
// (按 key 主键点查),(role, device_id) 索引随之失去唯一用途。
//
// 标记是**在飞状态**:它只在一次替换生成从开始到收尾之间存在,收尾时被删掉。因此
// 正常情况下这里是 0 行;真有残留时删掉它即可 —— 旧格式的 payload 在下一条迁移里
// 连同 blocks_json 列一起消失,留着也没有任何代码读得懂。
func migration202608270001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608270001",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(
				`DELETE FROM chat_messages WHERE role = ?`, "__agentre_pi_recovery__",
			).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP INDEX IF EXISTS idx_chat_messages_recovery`).Error
		},
	}
}
