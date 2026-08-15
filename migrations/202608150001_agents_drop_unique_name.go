package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608150001 删掉 agents(name) WHERE status=1 的唯一索引
// （202608080004 建的 uniq_agents_name_active）。
//
// 它与同步规格 R12a 正面冲突，冲突的结果是一个用户无法靠界面脱身的死锁：
//
//	R12a —— 双机办公的用户两边各建过一个「开发」，登录同一账号后账号下会各出现两份，
//	「用户自行删掉多余的那个（Agent）」。
//
// 两份要先都落地，用户才谈得上删。有这个唯一索引，第二份**插不进来**：下行每 30 秒
// 撞一次 `UNIQUE constraint failed: agents.name (2067)`，那一行连同引用它的下游
// （子 Agent、执行目标）全部堆在暂缓队列里，界面上表现为「N 项待同步」永不清零，
// 直到 30 天窗口把它们推进「没能同步的改动」。用户看不到那个 Agent，自然也删不掉它。
//
// 「Agent 名全局唯一」这条产品规则**没有放松**：它由 agent_svc.Create / Update 里的
// FindByName + code.AgentNameDuplicated 把守，管的是用户手输的名字。索引管不了这件事
// 却顺带把同步下来的另一段合法历史也挡在门外——这正是该由服务层而非存储层表达的规则。
//
// 纯结构迁移：只删索引，不动任何一行。
func migration202608150001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608150001",
		Migrate: func(tx *gorm.DB) error {
			return tx.Exec(`DROP INDEX IF EXISTS uniq_agents_name_active`).Error
		},
	}
}
