package migrations

import (
	"fmt"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// syncMetaTables 是账号级七张表（R1、决策 3）：projects、departments、agents、
// agent_backends、project_agents、project_locations、agent_exec_targets。
var syncMetaTables = []string{
	"projects",
	"departments",
	"agents",
	"project_agents",
	"project_locations",
	"agent_exec_targets",
}

// syncMetaColumns 与 internal/model/entity/syncmeta_entity.SyncMeta 的字段顺序一一
// 对应，DDL 类型跟随宿主表既有 bigint/text 惯例。
var syncMetaColumns = []struct {
	name string
	ddl  string
}{
	{"sync_id", "TEXT NOT NULL DEFAULT ''"},
	{"sync_account_id", "BIGINT NOT NULL DEFAULT 0"},
	{"sync_version", "BIGINT NOT NULL DEFAULT 0"},
	{"sync_updated_at", "BIGINT NOT NULL DEFAULT 0"},
	{"sync_origin", "TEXT NOT NULL DEFAULT ''"},
	{"sync_deleted_at", "BIGINT NOT NULL DEFAULT 0"},
}

// migration202608080012 铺同步基础设施：
//
//  1. 账号级七张表各加六列同步元数据 + sync_id 部分唯一索引（R1/决策 3/4/19/27）：
//     sync_id 是账号内全局唯一、终身不变的同步标识（ULID，决策 3），未登录期间创建
//     的行也照常生成（R12a）。本仓 DDL 一律 not null default ”，因此唯一索引必须
//     是 WHERE sync_id != ” 的部分索引——否则多行空标识互撞。sync_account_id 是
//     所属账号（server_state.ServerUserID），0 = 尚未被认领。sync_version 是 server
//     分配的账号级单调序列（决策 27），本地只存不改；sync_updated_at 只用于展示与
//     30 天窗口，不参与胜负比较；sync_origin 是决策 4 的字典序打破平局；
//     sync_deleted_at 是墓碑时间（R6），0 = 未删除，与各表既有 status 软删语义正交。
//     本迁移只加列与索引，不回填任何既有行的 sync_id——生成时机是「行创建 / 下一次
//     落库时」（R1，syncmeta_entity.SyncMeta.EnsureSyncID），历史行落在 sync_id = ”
//     上，等被仓储层 Create/Update 触达时按同一条 JIT 规则补齐。
//  2. 三张队列表（R5/R7/R2a，决策 12）：sync_lost_changes（「没能同步的改动」，
//     被覆盖 / 悬空引用超期 / 超窗口被拒三类失效事件共用，保留 30 天，并直接带上
//     R5a 的跨机自然键两列 project_sync_id / agentred_fingerprint——恢复一条路径
//     记录或远端 backend 必须能解析出归属）、sync_outbound_queue（出站，每条带基
//     版本 R4a/R6a）、sync_inbound_queue（入站，引用目标未到达时暂缓落地）。三张
//     表都按账号分命名空间（sync_account_id，R13a）。
//
// 本迁移只铺数据与骨架，不发起任何网络调用；真正的上行 / 下行 / 冲突落地是后续任务。
func migration202608080012() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080012",
		Migrate: func(tx *gorm.DB) error {
			for _, table := range syncMetaTables {
				for _, col := range syncMetaColumns {
					stmt := fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, col.name, col.ddl)
					if err := tx.Exec(stmt).Error; err != nil {
						return fmt.Errorf("table %s: add column %s: %w", table, col.name, err)
					}
				}
				idxStmt := fmt.Sprintf(
					`CREATE UNIQUE INDEX IF NOT EXISTS uniq_%s_sync_id ON %s(sync_id) WHERE sync_id != ''`,
					table, table,
				)
				if err := tx.Exec(idxStmt).Error; err != nil {
					return fmt.Errorf("table %s: create sync_id index: %w", table, err)
				}
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS sync_lost_changes (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	entity_type TEXT NOT NULL,
	entity_sync_id TEXT NOT NULL,
	base_version BIGINT NOT NULL DEFAULT 0,
	reason TEXT NOT NULL,
	payload_json TEXT NOT NULL DEFAULT '',
	origin_device TEXT NOT NULL DEFAULT '',
	project_sync_id TEXT NOT NULL DEFAULT '',
	agentred_fingerprint TEXT NOT NULL DEFAULT '',
	occurred_at BIGINT NOT NULL DEFAULT 0,
	createtime BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_sync_lost_changes_account
ON sync_lost_changes(sync_account_id, createtime)`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS sync_outbound_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	entity_type TEXT NOT NULL,
	local_id BIGINT NOT NULL DEFAULT 0,
	entity_sync_id TEXT NOT NULL DEFAULT '',
	op TEXT NOT NULL,
	base_version BIGINT NOT NULL DEFAULT 0,
	queued_at BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_sync_outbound_queue_account
ON sync_outbound_queue(sync_account_id, queued_at)`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS sync_inbound_queue (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	entity_type TEXT NOT NULL,
	entity_sync_id TEXT NOT NULL,
	payload_json TEXT NOT NULL DEFAULT '',
	missing_sync_id TEXT NOT NULL DEFAULT '',
	received_at BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_sync_inbound_queue_account
ON sync_inbound_queue(sync_account_id, received_at)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS sync_inbound_queue`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DROP TABLE IF EXISTS sync_outbound_queue`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DROP TABLE IF EXISTS sync_lost_changes`).Error; err != nil {
				return err
			}
			for _, table := range syncMetaTables {
				if err := tx.Exec(fmt.Sprintf(`DROP INDEX IF EXISTS uniq_%s_sync_id`, table)).Error; err != nil {
					return err
				}
				for _, col := range syncMetaColumns {
					stmt := fmt.Sprintf(`ALTER TABLE %s DROP COLUMN %s`, table, col.name)
					if err := tx.Exec(stmt).Error; err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
}
