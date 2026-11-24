package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611250102 建 session_issue_links 表(spec 2026-11-24 决策 23):会话 ↔ 看板
// 任务的多对多关联。只存本机,不进同步;任务或会话被删除时由 service 清理对应行。
// (session_id, issue_id) 唯一保证重复关联幂等;issue_id 单独一个索引服务「按任务查」。
// 纯新增表,不碰已有数据,也不碰 issues.session_id。
func migration202611250102() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202611250102",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE TABLE IF NOT EXISTS session_issue_links (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	issue_id INTEGER NOT NULL,
	createtime BIGINT NOT NULL DEFAULT 0
)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_session_issue_links_pair ON session_issue_links(session_id, issue_id)`,
				`CREATE INDEX IF NOT EXISTS ix_session_issue_links_issue ON session_issue_links(issue_id)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
