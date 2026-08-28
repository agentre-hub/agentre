package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// secondsSeedThreshold 是「像秒级 epoch」的判据:2001-09-09 之后的任意秒值都
// 小于这个数(1e11 ≈ 公元 5138 年的毫秒值下限的十分之一，留了足够余量)，而任意
// 毫秒值（本仓 2026 年前后落库的行）都远大于它。种子按 INSERT ... WHERE NOT
// EXISTS 幂等写入，id 不稳定，因此判据只能是值域阈值，不能是行 id。
const secondsSeedThreshold = 100_000_000_000

// migration202608270005 把种子数据里残留的秒级时间戳换算成毫秒(Problem 20 /
// 决策 26)。
//
// 全仓统一 UnixMilli，唯独 202608080004 的 CEO agent 种子用
// strftime('%s','now')（秒），同批其余种子（如 202608080010 的标签）都是
// *1000 的毫秒值。那一行的 createtime/updatetime 因此被解释成 1970 年附近。
// 202608080004 是既有迁移，不可修改（仓库硬规矩）；这里追加一条把
// < secondsSeedThreshold 的 createtime/updatetime 值 *1000。判据按值域而非
// 行 id，已经是毫秒的值不会被二次放大。
func migration202608270005() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608270005",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(
				`UPDATE agents SET createtime = createtime * 1000 WHERE createtime > 0 AND createtime < ?`,
				secondsSeedThreshold,
			).Error; err != nil {
				return err
			}
			return tx.Exec(
				`UPDATE agents SET updatetime = updatetime * 1000 WHERE updatetime > 0 AND updatetime < ?`,
				secondsSeedThreshold,
			).Error
		},
	}
}
