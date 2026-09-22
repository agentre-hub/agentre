package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609210101 给 port_forwards 加 target / insecure 两列,把唯一性的判据
// 从「端口」扩成「规范化后的目标」(规格 2026-09-21「端口转发子域」的「映射与目标」
// 一节)。
//
// DDL 逐字取自桌面端一侧(migrations/202609210101_port_forward_target.go)——两个
// 宿主共用同一份实体(internal/model/entity/port_forward_entity)与仓储
// (internal/repository/port_forward_repo)代码,表结构错一格就是同一行代码在两台
// 机器上写出两种结果(同形由 port_forward_parity_test.go 钉住)。
//
//   - target 用一句 UPDATE 补上已有行的值:规格明写「给已有映射补上目标字段,值等于
//     http://127.0.0.1:<原端口>、不忽略证书」——旧行的端口已经在 port 列里,拼出
//     "http://127.0.0.1:" || port 就是那句话的字面翻译,不需要再猜一次。
//   - insecure 的默认值 0(false)恰好就是迁移要求的「不忽略证书」,新增列的
//     DEFAULT 已经把这句话说完,不需要额外一句 UPDATE。
//   - 旧的 ux_port_forwards_port 索引废弃,换成 ux_port_forwards_target:唯一性的
//     判据从裸端口扩成了整个目标(协议、主机、端口),两个目标可以共用同一个端口号
//     只要主机不同。
func migration202609210101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609210101",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`ALTER TABLE port_forwards ADD COLUMN target TEXT NOT NULL DEFAULT ''`,
				`ALTER TABLE port_forwards ADD COLUMN insecure BOOLEAN NOT NULL DEFAULT 0`,
				`UPDATE port_forwards SET target = 'http://127.0.0.1:' || port WHERE target = ''`,
				`DROP INDEX IF EXISTS ux_port_forwards_port`,
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_port_forwards_target ON port_forwards(target)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`DROP INDEX IF EXISTS ux_port_forwards_target`,
				`CREATE UNIQUE INDEX IF NOT EXISTS ux_port_forwards_port ON port_forwards(port)`,
				`ALTER TABLE port_forwards DROP COLUMN insecure`,
				`ALTER TABLE port_forwards DROP COLUMN target`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
