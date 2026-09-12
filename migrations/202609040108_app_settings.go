package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609040108 建 app_settings 表 —— App 全局 key-value 配置项。
//
// 该迁移只 seed 本地 HTTP 代理监听地址 / 端口；其它设置由后续代码按需写入。
//
// seed 默认值：
//   - proxy.listen_host = 127.0.0.1（loopback，只允许本机访问）
//   - proxy.listen_port = 52401（IANA 动态端口段，避开常见开发服务端口）
func migration202609040108() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609040108",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS app_settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL,
	updatetime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			return tx.Exec(`INSERT OR IGNORE INTO app_settings (key, value, updatetime) VALUES
	('proxy.listen_host', '127.0.0.1', 0),
	('proxy.listen_port', '52401', 0)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS app_settings`).Error
		},
	}
}
