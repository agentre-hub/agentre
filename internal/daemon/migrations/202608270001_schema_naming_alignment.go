package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608270001 把 agentred 这一侧的命名对齐到整个工作区
// (docs/specs/2026-08-27-schema-overhaul.md 决策 9/11/18):
//
//   - created_at → createtime:桌面端与 server 的 26 张表都用 createtime,只有
//     agentred 这两张表用 created_at,而三套 schema 在同一个工作区里演进。
//   - daemon_sessions.updated_at → last_message_at:这一列的**唯一**消费方是线格式
//     SessionSummary 的「会话最后活动时刻」(protobuf_registry 把它原样喂过去,server
//     当最后活动持久化),叫 updated_at 既与真实用途不符,又会被 GORM 认作行更新时刻
//     在每一次写入上自动改写。agentred 库里没有第二个读取方需要行更新时刻。
//   - daemon_notification_logs → daemon_notification_journal:同一行东西过去有三个词
//     (线格式 JournaledNotification / agentred notification_logs / server
//     journal_frames),以线格式为准统一词根;daemon_ 前缀保留(决策 18)。
//
// 三侧均未发布/未部署(决策 22),因此直接改名:不回填、不保留旧列、不留兼容读路径。
func migration202608270001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608270001",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`ALTER TABLE daemon_sessions RENAME COLUMN created_at TO createtime`,
				`ALTER TABLE daemon_sessions RENAME COLUMN updated_at TO last_message_at`,
				`ALTER TABLE daemon_notification_logs RENAME TO daemon_notification_journal`,
				`ALTER TABLE daemon_notification_journal RENAME COLUMN created_at TO createtime`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`ALTER TABLE daemon_notification_journal RENAME COLUMN createtime TO created_at`,
				`ALTER TABLE daemon_notification_journal RENAME TO daemon_notification_logs`,
				`ALTER TABLE daemon_sessions RENAME COLUMN last_message_at TO updated_at`,
				`ALTER TABLE daemon_sessions RENAME COLUMN createtime TO created_at`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
