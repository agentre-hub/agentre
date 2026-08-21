// Package migrations 汇总并执行 Agentre 桌面端 SQLite 数据库的全部迁移。
//
// 规范：
//   - 文件名前缀 = 时间戳排序键（YYYYMMDDNNNN），调用顺序按时间升序。
//   - 每个迁移返回一个 *gormigrate.Migration，包含 Migrate 与可选的 Rollback。
//   - 一次迁移只做一件事；新增表、加列、加索引各自独立成文件，方便回滚和 git bisect。
//   - DDL 优先使用原生 SQL，避免依赖 GORM AutoMigrate 的隐式行为。
package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// RunMigrations 执行全部迁移。新增迁移时把构造函数追加到 migrationList 末尾。
func RunMigrations(db *gorm.DB) error {
	m := gormigrate.New(db, gormigrate.DefaultOptions, migrationList())
	return m.Migrate()
}

// migrationList 按时间升序列出全部迁移构造函数。
func migrationList() []*gormigrate.Migration {
	return []*gormigrate.Migration{
		migration202608080001(), // llm_providers
		migration202608080002(), // departments
		migration202608080003(), // agent_backends
		migration202608080004(), // agents + default agent
		migration202608080005(), // projects + project_agents + project_locations
		migration202608080006(), // chat_sessions + chat_messages
		migration202608080007(), // hooks + hook_events
		migration202608080008(), // app_settings + proxy defaults
		migration202608080009(), // server_state + paired_agentreds
		migration202608080010(), // issues + labels + issue_labels + label defaults
		migration202608080011(), // 执行目标 + 本机路径（R15/R15e/R15b/R10/决策 26）
		migration202608080012(), // 同步基础设施（R1 同步元数据 / R5/R7/R2a 队列表）
		migration202608100001(), // chat_messages 恢复标记索引 (role, device_id)
		migration202608110001(), // llm_provider_models：Provider 1→N 稳定模型 + 默认/目标列 + 旧路由结构化
		migration202608130001(), // 本端执行目标顺序覆盖（R14，纯本地不同步）
		migration202608150001(), // 删 agents 名唯一索引（与 R12a 的同名共存冲突）
		migration202608150002(), // paired_agentreds 容纳「只有中转路径」的行（决策 1）
		migration202608200001(), // 账号级 LLM Provider + 每设备 CLI 覆盖
	}
}
