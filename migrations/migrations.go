// Package migrations 汇总并执行 Agentre 桌面端 SQLite 数据库的全部迁移。
//
// 规范：
//   - 文件名前缀 = 时间戳排序键（YYYYMMDDNNNN），调用顺序按时间升序。
//   - 每个迁移返回一个 *gormigrate.Migration，包含 Migrate 与可选的 Rollback。
//   - 一次迁移只做一件事；新增表、加列、加索引各自独立成文件，方便回滚和 git bisect。
//   - DDL 优先使用原生 SQL，避免依赖 GORM AutoMigrate 的隐式行为。
package migrations

import (
	"fmt"

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
	return []*gormigrate.Migration{migration202608280001Baseline()}
}

// migration202608280001Baseline 是正式发布前压缩后的桌面数据库初始基线。
// 各领域步骤仍分文件保留以便审阅，但 gormigrate 只记录这一条基线；正式发布后的
// schema 变化必须重新追加迁移，不能再修改基线。
func migration202608280001Baseline() *gormigrate.Migration {
	steps := []*gormigrate.Migration{
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
		migration202608130001(), // 本端执行目标顺序覆盖（R14，纯本地不同步）
		migration202608260002(), // chat_sessions 索引页三根轴的索引(最近 / 项目 / 执行设备)
		migration202608270001(), // pi 恢复标记迁出 chat_messages(改存 app_settings)
		migration202608270002(), // 消息正文拆成一块一行的 chat_message_blocks + 删 blocks_json
		migration202608270003(), // 命名对齐:device_fingerprint / sync_origin_fingerprint
		migration202608270004(), // 内置标签精简到 bug/critical/docs/feature/refactor 五档
		migration202608270005(), // 补丁:agents 种子秒级时间戳换算成毫秒
		migration202608270006(), // issues 补 session_id / assignee_agent_id 索引
		migration202608280001(), // 补丁:块表两个索引补到已迁过的库上(收尾评审改了 202608270002 但它跑过了)
		migration202608280002(), // 看板并入账号级同步组:执行归属三列 + 三张表的同步元数据 + 8 档色调
	}

	return &gormigrate.Migration{
		ID: "202608280001",
		Migrate: func(tx *gorm.DB) error {
			for _, step := range steps {
				if err := step.Migrate(tx); err != nil {
					return fmt.Errorf("migrations: apply baseline step %s: %w", step.ID, err)
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for i := len(steps) - 1; i >= 0; i-- {
				if steps[i].Rollback == nil {
					continue
				}
				if err := steps[i].Rollback(tx); err != nil {
					return fmt.Errorf("migrations: rollback baseline step %s: %w", steps[i].ID, err)
				}
			}
			return nil
		},
	}
}
