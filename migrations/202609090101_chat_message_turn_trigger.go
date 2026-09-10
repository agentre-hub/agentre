package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609090101 给 chat_messages 补上 turn_trigger 列:这一条 assistant 消息
// 所属的那一轮**是被什么起的**。
//
// 为什么需要落库:前端判「这一轮不是用户发起的」靠的是结构 —— 一条 assistant 消息前面
// 没有 user 消息(agentre-ui 的 autonomousTurnMessageIds)。结构判得出**有没有**,判不出
// **为什么**。在 sess-3797 之前只有一种非用户发起的轮(CLI 的后台任务完成续轮),分隔卡
// 因此可以写死「后台任务完成 · 自动继续」;现在多了一种(子进程被 agentre 之外的东西
// 叫醒、自己起的一轮),两者在转录结构上一模一样,不落库就只能给外部唤醒的轮挂上一句
// 与事实不符的文案,而且刷新页面后连区分的依据都没有。
//
// 取值即 agentruntime.AutonomousTurn.Trigger:"background_task" / "catch_up" /
// "external"。**空串是有意义的默认**:用户发起的一轮不写它,本迁移之前的历史行也留空
// —— 前端见空即退回今天的渲染,历史转录逐字不变,不需要回填。
//
// 单独一条补丁迁移而不是改建表那条:已发出去的迁移不再改动(AGENTS.md)。
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
		// SQLite 3.35+ 支持 DROP COLUMN;没有索引引用该列。
		Rollback: func(tx *gorm.DB) error {
			if !tx.Migrator().HasColumn("chat_messages", "turn_trigger") {
				return nil
			}
			return tx.Exec(`ALTER TABLE chat_messages DROP COLUMN turn_trigger`).Error
		},
	}
}
