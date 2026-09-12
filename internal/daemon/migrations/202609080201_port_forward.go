package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609080201 建 port_forwards 表:agentred 作为被访问设备时,记着自己
// 声明过的端口转发映射(规格 2026-09-06「设备端口转发」的「数据」一节)。
//
// DDL 逐字取自桌面端(migrations/202609080201_port_forward.go)——两个宿主共用
// 同一份实体(internal/model/entity/port_forward_entity)与仓储
// (internal/repository/port_forward_repo)代码,表结构错一格就是同一行代码在两台
// 机器上写出两种结果。两个进程各一个库,所以这里同样是**复制 DDL**而不是共享
// 一张表(同形由 transcript_parity_test.go 那一族的 port_forward_parity_test.go 钉住)。
//
// 不设「所有者标识」列:表本身整张落在这一台设备的库上,规格明写「端口在一台设备下
// 唯一」(不是「同一 owner 下唯一」),且任何一个有权连上这台设备的客户端看到的都是
// 同一份声明——见 port_forward_entity 包注释的详细说明。唯一性由
// ux_port_forwards_port 这条 UNIQUE 索引兜底。
func migration202609080201() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609080201",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS port_forwards (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	port INTEGER NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	enabled BOOLEAN NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS ux_port_forwards_port
	ON port_forwards(port)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS port_forwards`).Error
		},
	}
}
