package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608230001 给 daemon_sessions 补上会话级 ModelTarget 的两列:
//
//   - provider_key —— 这条会话钉的 LLM 供应商稳定 key;
//   - model_key —— 这条会话钉的稳定 ModelKey。
//
// 两格组合成 ModelTarget 契约的三态,与桌面端 chat_sessions 的同名两列逐字同义
// (chat_entity/session.go):两者皆空 = 跟随 Agent 绑定、provider 非空 + model 空 =
// 该供应商当前的默认模型、两者都非空 = 固定模型。老会话没落过这两列,保持空串 ——
// 而空**正好**是「跟随 Agent 绑定」,所以老数据的语义与今天完全一致,不需要回填。
//
// 为什么承载执行的这一端也要存:同一条对话可以在桌面端与 agentred 上各有一份,
// 而此刻承载连接的那台未必是发起它的那台(mirror_entity 包注释)。用户在浏览器里
// 换模型时两台都写,用户在哪一台打开都看到自己刚选的那个。此前 agentred 这一侧
// 没有落脚处,是本轮唯一一处扩张「承载者只管执行」那条分工的地方,只针对这两格。
//
// **每轮起手的 Upsert 不碰这两列** —— 见 session_repo.Upsert 的赋值列。
//
// SQLite 的 ALTER TABLE ADD COLUMN 一次只加一列,故逐条执行。
func migration202608230001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608230001",
		Migrate: func(tx *gorm.DB) error {
			for _, ddl := range []string{
				`ALTER TABLE daemon_sessions ADD COLUMN provider_key TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE daemon_sessions ADD COLUMN model_key TEXT NOT NULL DEFAULT ''`,
			} {
				if err := tx.Exec(ddl).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			// SQLite 不支持 DROP COLUMN,回滚只把值清回空串;列结构保留。
			// 清回空串 = 全部回到「跟随 Agent 绑定」,与本迁移之前的行为一致。
			return tx.Exec(`UPDATE daemon_sessions SET provider_key = '', model_key = ''`).Error
		},
	}
}
