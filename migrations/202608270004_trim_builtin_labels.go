package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608270004 把 202608080010 seed 的十个内置标签精简到五个
// （bug / critical / docs / feature / refactor），并按同一顺序重排 sort_order。
//
// 只删「name == tone」的内置行——这是 seed 的形状；将来目录里若出现自定义标签，
// 哪怕重名也不会被这条迁移带走。关联表里指向被删标签的行一并清掉，避免留下悬空
// issue_labels。
func migration202608270004() *gormigrate.Migration {
	const dropped = `('auth','hook','ops','perf','ui')`

	return &gormigrate.Migration{
		ID: "202608270004",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`DELETE FROM issue_labels WHERE label_id IN (
	SELECT id FROM labels WHERE name = tone AND name IN ` + dropped + `
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DELETE FROM labels WHERE name = tone AND name IN ` + dropped).Error; err != nil {
				return err
			}
			return tx.Exec(`UPDATE labels SET sort_order = CASE name
	WHEN 'bug' THEN 1
	WHEN 'critical' THEN 2
	WHEN 'docs' THEN 3
	WHEN 'feature' THEN 4
	WHEN 'refactor' THEN 5
	ELSE sort_order END
WHERE name = tone AND name IN ('bug','critical','docs','feature','refactor')`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`INSERT INTO labels (name, tone, sort_order, status, createtime, updatetime)
SELECT name, name, sort_order, 1,
	CAST(strftime('%s','now') AS INTEGER) * 1000,
	CAST(strftime('%s','now') AS INTEGER) * 1000
FROM (
	SELECT 'auth' AS name, 6 AS sort_order
	UNION ALL SELECT 'hook', 7
	UNION ALL SELECT 'ops', 8
	UNION ALL SELECT 'perf', 9
	UNION ALL SELECT 'ui', 10
) seed
WHERE NOT EXISTS (SELECT 1 FROM labels WHERE labels.name = seed.name AND labels.status = 1)`).Error
		},
	}
}
