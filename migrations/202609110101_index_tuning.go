package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609110101 是一次纯索引调优迁移，SQL 形状不变，只调整索引集合（决策 9：
// 纯 DDL 索引迁移不写先红单测，证据是真库前后 EXPLAIN QUERY PLAN 留档，见
// .dev-kit/artifacts/db-perf-fixes/desktop-migration/）。
//
//   - hook_events: hook_repo.ListRecent 的 `status = ? ORDER BY received_at DESC, id
//     DESC` 与 hook_repo.ListByHook 的 `hook_id = ? AND status = ? ORDER BY received_at
//     DESC, id DESC` 在无统计信息的库上都退化为全表扫描 + 临时 B 树排序。两条索引都要
//     建：只建 status 前缀的那条会把 ListByHook 也挪到这条索引上（hook_id 退化成过滤
//     条件），反而更慢（已用无 ANALYZE 的库验证）。旧的 idx_hook_events_hook(hook_id,
//     received_at) 不再被任何查询计划选中，一并收掉。
//   - projects: idx_projects_parent_id(parent_id, status) 是 idx_projects_parent_sort
//     (parent_id, status, sort_order, id) 的严格前缀，从未被规划器单独选中过，删掉不会
//     让 ListByParent / HasActiveChildren / NextSortOrder / ReassignParent / List 退化为
//     SCAN——它们都改走 parent_sort。FindByName 因为 GORM First 隐式按 id 排序，会多一次
//     无害的 TEMP B-TREE（覆盖 status = ? AND name = ? 组合命中的行本来就很少）。
//   - chat_sessions: ListForRollup 的 `status = ? AND purpose <> ? AND last_message_at >
//     0 [AND createtime >= ?] ORDER BY createtime ASC, id ASC` 同样是全表扫描 + 临时 B
//     树；新增 (status, createtime, id) 后走索引，ListIndexPaged / ListByAgent* /
//     按 project 的查询继续用各自既有的 status_last 系索引，未受影响。
func migration202609110101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609110101",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE INDEX IF NOT EXISTS idx_hook_events_status_received ON hook_events(status, received_at, id)`,
				`CREATE INDEX IF NOT EXISTS idx_hook_events_hook_status_received ON hook_events(hook_id, status, received_at, id)`,
				`DROP INDEX IF EXISTS idx_hook_events_hook`,
				`DROP INDEX IF EXISTS idx_projects_parent_id`,
				`CREATE INDEX IF NOT EXISTS idx_chat_sessions_status_created ON chat_sessions(status, createtime, id)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`DROP INDEX IF EXISTS idx_chat_sessions_status_created`,
				`CREATE INDEX IF NOT EXISTS idx_projects_parent_id ON projects(parent_id, status)`,
				`CREATE INDEX IF NOT EXISTS idx_hook_events_hook ON hook_events(hook_id, received_at)`,
				`DROP INDEX IF EXISTS idx_hook_events_hook_status_received`,
				`DROP INDEX IF EXISTS idx_hook_events_status_received`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
