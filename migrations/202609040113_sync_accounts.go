package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609040113 建 sync_accounts 表 —— 本机为 (server 地址, 远端用户主键)
// 分配的代理键，也就是各表 sync_account_id 那一列的含义。
//
// 为什么不直接用 server 的 user_id：那是它自己库里的自增主键，两套自建部署的第一个
// 用户都是 1。归属判定（行属于谁、队列属于谁、游标属于谁）全落在这一个整数上，于是
// 换一套 server 之后本机会把 B 的 1 号用户认成 A 的 1 号用户 —— 上一个账号的行照常
// 上行到新 server 里去，而 R13a 说的正是这些行不该参与同步。
//
// (server, 用户) 对由 sync_account_repo.EnsureKey 按需分配。server 侧对这张表一无所知，
// 这是纯本地的一层身份。
func migration202609040113() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609040113",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS sync_accounts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	server_url TEXT NOT NULL DEFAULT '',
	remote_user_id BIGINT NOT NULL DEFAULT 0,
	createtime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_sync_accounts_pair
	ON sync_accounts (server_url, remote_user_id)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS sync_accounts`).Error
		},
	}
}
