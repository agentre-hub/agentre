// Package migrations 汇总并执行 agentred 自己的 SQLite 数据库迁移。
//
// 与桌面端 migrations/ 各自独立、互不引用——agentred 是独立进程,有自己的库文件
// (见 daemon.New),不共享桌面端的 chat_sessions 等表。
//
// 规范同桌面端:
//   - 文件名前缀 = 时间戳排序键(YYYYMMDDNNNN),调用顺序按时间升序。
//   - 每个迁移返回一个 *gormigrate.Migration,包含 Migrate 与可选的 Rollback。
//   - 一次迁移只做一件事;DDL 优先使用原生 SQL,避免依赖 GORM AutoMigrate 的隐式行为。
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
	return []*gormigrate.Migration{
		migration202608280001Baseline(),
		migration202608280002(), // daemon_sessions.project_sync_id
	}
}

// migration202608280001Baseline 是正式发布前压缩后的 agentred 数据库初始基线。
// 各领域步骤保留独立实现，但 gormigrate 只记录这一条基线。
func migration202608280001Baseline() *gormigrate.Migration {
	steps := []*gormigrate.Migration{
		migration202608080011(), // daemon_sessions + daemon_notification_logs
		migration202608100001(), // R7 标题/Agent 同步标识 + 决策 8 provider_session_id
		migration202608230001(), // 会话级 ModelTarget:provider_key + model_key
		migration202608240001(), // Protobuf notification journal
		migration202608270001(), // 命名对齐:createtime / last_message_at / notification journal
	}

	return &gormigrate.Migration{
		ID: "202608280001",
		Migrate: func(tx *gorm.DB) error {
			for _, step := range steps {
				if err := step.Migrate(tx); err != nil {
					return fmt.Errorf("daemon migrations: apply baseline step %s: %w", step.ID, err)
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
					return fmt.Errorf("daemon migrations: rollback baseline step %s: %w", steps[i].ID, err)
				}
			}
			return nil
		},
	}
}
