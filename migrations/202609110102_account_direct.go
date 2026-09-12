package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609110102 给 paired_agentreds 加两列，支撑「来自账号的直连」设备行
// （spec 2026-09-11 opaque-credentials-auto-direct 的 D6/D10/D14/D15）：
//
//   - origin：这一行的来源标记，'manual'（默认，覆盖今天已有的手动 LAN 配对行与
//     纯中转收编行两种历史形状——两者都不是账号下发的直连内容）或 'account'（账号
//     握手下发过地址/证书/凭据）。只有它决定 IsAccountDirect()；相比之下
//     IsRelayOnly() 仍然只看 url 是否为空，两者是正交的判据。
//   - direct_urls_json：账号一次下发的全部地址（D6「保存下发的全部地址」），JSON
//     数组。既有的 url 列不够用——它是单条「地址位」，展示最近一次直连成功的那个
//     地址（没成功过时是这个列表的第一个），而下发的地址可能不止一个。
//
// 两列都是新增、非破坏性：旧行落到各自的默认值（origin='manual' 是安全的默认——
// 既有的手动配对行与收编行都不是账号直连行，direct_urls_json='[]' 对它们也没有
// 意义），不改写任何既有数据、可回滚。
func migration202609110102() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609110102",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`ALTER TABLE paired_agentreds ADD COLUMN origin TEXT NOT NULL DEFAULT 'manual'`,
				`ALTER TABLE paired_agentreds ADD COLUMN direct_urls_json TEXT NOT NULL DEFAULT '[]'`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`ALTER TABLE paired_agentreds DROP COLUMN direct_urls_json`,
				`ALTER TABLE paired_agentreds DROP COLUMN origin`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
