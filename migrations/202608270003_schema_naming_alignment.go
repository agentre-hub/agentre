package migrations

import (
	"fmt"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// syncOriginFingerprintTables 是内嵌 syncmeta_entity.SyncMeta 的九张账号级表
// （六张由 202608080012 加列，llm_providers / agent_backends /
// agent_backend_cli_overlays 三张在各自建表时就带着这六列）。
var syncOriginFingerprintTables = []string{
	"llm_providers",
	"agent_backends",
	"agent_backend_cli_overlays",
	"agents",
	"agent_exec_targets",
	"departments",
	"projects",
	"project_agents",
	"project_locations",
}

// migration202608270003 把桌面端这一侧的机器指纹列与同步来源列改名，对齐
// docs/specs/2026-08-27-schema-overhaul.md 的决策 14/16：
//
//   - agent_backends.device_id 存的从来就是**目标机器的规范设备指纹**，不是任何数字
//     主键（agent_backend_repo 认领本机时写进去的就是指纹）。错的只有 `_id` 那半截，
//     它暗示了一个并不存在的数字外键，还顺着 chat_messages.device_id 把同一个值用同
//     一个错名扩散到了第二张表与线格式上。两张表一起改成 device_fingerprint。
//   - project_locations.daemon_fingerprint / chat_sessions.exec_daemon_fingerprint
//     里的那台机器**可以是 agentred，也可以是另一台桌面端**（device_entity 有
//     KindDesktop 与 KindAgentred 两种，中转两种都走），`daemon_` 是过度限定，统一到
//     device_fingerprint / exec_device_fingerprint。真的只装 agentred 的
//     paired_agentreds.daemon_fingerprint 名副其实，不动。
//   - 九张账号级表的 sync_origin 改名 sync_origin_fingerprint：这一格记的是「最后一次
//     修改来自哪台设备」，工作区其余跨机引用一律用指纹表达。
//
// 桌面端未发布（决策 22），因此直接 RENAME COLUMN：不回填、不保留旧列、不留兼容读
// 路径。SQLite 的 RENAME COLUMN 会一并改写引用该列的索引定义，因此只有名字里带着
// 旧列名的那个索引需要重建。
func migration202608270003() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608270003",
		Migrate: func(tx *gorm.DB) error {
			stmts := make([]string, 0, 6+len(syncOriginFingerprintTables))
			stmts = append(stmts,
				`ALTER TABLE agent_backends RENAME COLUMN device_id TO device_fingerprint`,
				`ALTER TABLE chat_messages RENAME COLUMN device_id TO device_fingerprint`,
				`ALTER TABLE project_locations RENAME COLUMN daemon_fingerprint TO device_fingerprint`,
				`ALTER TABLE chat_sessions RENAME COLUMN exec_daemon_fingerprint TO exec_device_fingerprint`,
				`DROP INDEX IF EXISTS idx_agent_backends_device_id`,
				`CREATE INDEX IF NOT EXISTS idx_agent_backends_device_fingerprint
ON agent_backends(device_fingerprint) WHERE status = 1`,
			)
			for _, table := range syncOriginFingerprintTables {
				stmts = append(stmts, fmt.Sprintf(
					`ALTER TABLE %s RENAME COLUMN sync_origin TO sync_origin_fingerprint`, table))
			}
			for _, stmt := range stmts {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			stmts := make([]string, 0, 6+len(syncOriginFingerprintTables))
			stmts = append(stmts,
				`ALTER TABLE agent_backends RENAME COLUMN device_fingerprint TO device_id`,
				`ALTER TABLE chat_messages RENAME COLUMN device_fingerprint TO device_id`,
				`ALTER TABLE project_locations RENAME COLUMN device_fingerprint TO daemon_fingerprint`,
				`ALTER TABLE chat_sessions RENAME COLUMN exec_device_fingerprint TO exec_daemon_fingerprint`,
				`DROP INDEX IF EXISTS idx_agent_backends_device_fingerprint`,
				`CREATE INDEX IF NOT EXISTS idx_agent_backends_device_id
ON agent_backends(device_id) WHERE status = 1`,
			)
			for _, table := range syncOriginFingerprintTables {
				stmts = append(stmts, fmt.Sprintf(
					`ALTER TABLE %s RENAME COLUMN sync_origin_fingerprint TO sync_origin`, table))
			}
			for _, stmt := range stmts {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
