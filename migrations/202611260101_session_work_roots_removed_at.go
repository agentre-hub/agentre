package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611260101 给 session_work_roots 加 removed_at(spec 2026-11-24「worktree 被
// 移除」):根被撤销、或重验时已不是认领时那个工作树时,认领行不再删除,而是记下移除时刻
// (毫秒),变更窗的「已移除」分组在重启后、会话从没打开过时也能知道它曾是一个根;同一
// 路径被认领回来时清零。已有的行全部取默认 0,即仍是已认领 —— 不改写任何已有数据;
// 此前已被删除的认领行无从恢复,不做回填。
func migration202611260101() *gormigrate.Migration {
	return &gormigrate.Migration{ID: "202611260101", Migrate: func(tx *gorm.DB) error {
		return tx.Exec("ALTER TABLE session_work_roots ADD COLUMN removed_at BIGINT NOT NULL DEFAULT 0").Error
	}}
}
