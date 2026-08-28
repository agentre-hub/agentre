package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608270006 给 issues 补 session_id / assignee_agent_id 两个索引
// (Problem 21 / 决策 27)。202608080010 建表时两列都没有索引;现仅 1 行看不出
// 影响，但按会话反查工单、按受理人反查工单都没有索引可用。202608080010 是既有
// 迁移，不可修改，这里追加。
func migration202608270006() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608270006",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_session_id ON issues(session_id)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_assignee_agent_id ON issues(assignee_agent_id)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP INDEX IF EXISTS idx_issues_assignee_agent_id`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP INDEX IF EXISTS idx_issues_session_id`).Error
		},
	}
}
