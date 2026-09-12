package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609110101 补三条 daemon_sessions 上的索引,接住会话列表三条主要读路径
// (规格 2026-09-11「数据库性能」要求 21 的 daemon_sessions 部分)。
//
// 迁移前(真库 EXPLAIN QUERY PLAN 留档于
// .dev-kit/artifacts/db-perf-fixes/daemon-sessions/):
//   - ListAllByLifecycle / CountByLifecycle(`lifecycle_state = ?`)—— SCAN,
//     ListAllByLifecycle 额外带一次 TEMP B-TREE 排序;
//   - ListCreatedSince(`createtime >= ?`)—— SCAN + TEMP B-TREE(本迁移之外,
//     session.go 已把这条查询的 ORDER BY 整段去掉,不再需要为它排序建索引);
//   - ListAll(全表最近排序,无 WHERE)—— SCAN + TEMP B-TREE。
//
// 三条索引:
//   - idx_daemon_sessions_lifecycle_last (lifecycle_state, last_message_at DESC,
//     id DESC) 接住 ListAllByLifecycle / CountByLifecycle 与
//     ListByPeerLifecycle/CountByPeerLifecycle 的 `peer_fingerprint = ? AND
//     lifecycle_state = ?`(无统计信息的规划器会把等值条件挪到这条索引上 ——
//     可接受:同时在跑的会话本来就是个小集合)。
//   - idx_daemon_sessions_createtime (createtime) 接住 ListCreatedSince 的下界
//     扫描;它不带排序列,因为该查询已经不再排序。
//   - idx_daemon_sessions_last (last_message_at DESC, id DESC) 接住 ListAll /
//     CountAll 的全表最近排序(ORDER BY last_message_at DESC, id DESC)。
//
// 决策 9(用户决定,豁免 AGENTS.md TDD 第 1 条):纯索引迁移不写先红单测 ——
// SQL 文本不变,docs/testing.md 禁止在单测里断言索引;证据是迁移后真库前后的
// EXPLAIN QUERY PLAN 留档,加上既有的迁移链/parity 测试跑通。
func migration202609110101() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609110101",
		Migrate: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`CREATE INDEX IF NOT EXISTS idx_daemon_sessions_lifecycle_last
	ON daemon_sessions (lifecycle_state, last_message_at DESC, id DESC)`,
				`CREATE INDEX IF NOT EXISTS idx_daemon_sessions_createtime
	ON daemon_sessions (createtime)`,
				`CREATE INDEX IF NOT EXISTS idx_daemon_sessions_last
	ON daemon_sessions (last_message_at DESC, id DESC)`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			for _, stmt := range []string{
				`DROP INDEX IF EXISTS idx_daemon_sessions_lifecycle_last`,
				`DROP INDEX IF EXISTS idx_daemon_sessions_createtime`,
				`DROP INDEX IF EXISTS idx_daemon_sessions_last`,
			} {
				if err := tx.Exec(stmt).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
