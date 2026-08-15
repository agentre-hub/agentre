package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608150002 让 paired_agentreds 容得下「只有中转路径」的行。
//
// 决策 1 说「账号即信任边界，配对降为『未认领』时的本地路径」，但在此之前只有 LAN
// 配对握手能产生这张表里的行，而远端执行链路（ConnPool.Borrow / localPairedDeviceID）
// 一律以本表主键寻址。后果是：账号里已经信任、中转上明明在线的一台 agentred，只要本机
// 没亲手配对过它，就选不进「运行设备」，同步下来的后端在这台机器上跑起来报「远端设备
// 不存在」——而配对握手必须直连可达，跨网段的机器连补配对的机会都没有。
//
// 收编这类机器需要一行没有 LAN 地址的记录（URL 为空 = IsRelayOnly，中转按指纹寻址）。
// 两处索引因此要改：
//
//   - uniq_paired_agentreds_url 覆盖 status=1 的全部行，空串也算值：**第二台**被收编的
//     机器会与第一台在空 URL 上撞唯一键。加上 url 非空 收窄，它继续守它真正该守的
//     东西（同一个 LAN 地址不重复配对）。
//   - 新增指纹唯一索引：收编要按指纹幂等，且同一台机器不能既有配对行又有收编行——
//     那会变成两台设备、两个连接池 entry、两份在线状态。
//
// 纯结构迁移，不动任何一行。
func migration202608150002() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608150002",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP INDEX IF EXISTS uniq_paired_agentreds_url`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_paired_agentreds_url
ON paired_agentreds(url) WHERE status = 1 AND url != ''`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_paired_agentreds_fingerprint
ON paired_agentreds(daemon_fingerprint) WHERE status = 1 AND daemon_fingerprint != ''`).Error
		},
	}
}
