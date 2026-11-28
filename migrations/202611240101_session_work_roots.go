package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202611240101 建 session_work_roots 表(spec 2026-11-24「识别信号 · 认领」):
// 会话认领到的、会话 cwd 之外的工作根(同一主仓库的另一个工作树)。认领结果持久化,
// 重新打开会话与重启后仍在;会话 cwd 本身(主根)不入表。
//   - path      认领时的工作树根路径(远端会话是执行设备上的路径)
//   - worktree  该工作树在会话主仓库 git 公共目录下的短名,主 checkout 为空串;它与
//     会话的公共目录一起唯一确定一个工作树,用来判「是否已认领」
//
// 同一会话的同一路径只记一行,所以 (session_id, path) 唯一;该索引同时是按会话取
// 集合的走法。纯新增表,不碰已有数据。
func migration202611240101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202611240101",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE TABLE IF NOT EXISTS session_work_roots (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	session_id INTEGER NOT NULL,
	path TEXT NOT NULL,
	worktree TEXT NOT NULL DEFAULT '',
	createtime BIGINT NOT NULL DEFAULT 0
)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_session_work_roots_session_path ON session_work_roots(session_id, path)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
