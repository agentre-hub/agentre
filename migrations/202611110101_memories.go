package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611110101 建 memories 表（Agent 记忆）：一条记忆归属全局 / 部门 / agent /
// 项目之一，带上一版、置顶、正常 / 已归档两态、创建者与最近一次变更，以及账号级同步
// 元数据与 sync_id 部分唯一索引。没有 status 软删列：彻底删除就是删行。
func migration202611110101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202611110101",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE TABLE IF NOT EXISTS memories (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	owner_kind TEXT NOT NULL,
	owner_id BIGINT NOT NULL DEFAULT 0,
	summary TEXT NOT NULL,
	detail TEXT NOT NULL DEFAULT '',
	prev_summary TEXT NOT NULL DEFAULT '',
	prev_detail TEXT NOT NULL DEFAULT '',
	pinned BOOLEAN NOT NULL DEFAULT 0,
	state TEXT NOT NULL DEFAULT 'normal',
	creator_kind TEXT NOT NULL,
	creator_agent_id BIGINT NOT NULL DEFAULT 0,
	creator_session_id BIGINT NOT NULL DEFAULT 0,
	change_action TEXT NOT NULL,
	change_actor_kind TEXT NOT NULL,
	change_agent_id BIGINT NOT NULL DEFAULT 0,
	change_session_id BIGINT NOT NULL DEFAULT 0,
	changed_at BIGINT NOT NULL DEFAULT 0,
	createtime BIGINT NOT NULL DEFAULT 0,
	updatetime BIGINT NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0
)`,
				`CREATE INDEX IF NOT EXISTS idx_memories_owner ON memories(owner_kind, owner_id, state)`,
				`CREATE INDEX IF NOT EXISTS idx_memories_changed_at ON memories(changed_at)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uniq_memories_sync_id ON memories(sync_id) WHERE sync_id != ''`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
