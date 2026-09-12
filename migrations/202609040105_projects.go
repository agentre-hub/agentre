package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609040105 建 projects / project_agents / project_locations 三张表。
//
// projects 承担「工作上下文」语义：名字 + 本地路径 + 成员 Agent。
//   - parent_id           自引用，0 = 顶级；无外键，移动/删除级联由 service 校验
//   - local_path_missing  「本机未配置路径」显式状态位（R10/决策 21）
//
// project_agents 是 Project ↔ Agent 多对多成员关系。父项目成员**只读继承**到子项目，
// 继承在查询时按 parent_id 链上溯聚合（spec §3.3），不入库以避免父改后子副本不同步。
//
// project_locations 装载「远端 device 下，某个 project 的绝对路径」。本地 path 仍住在
// projects.path（避免双源同步），本表不存空指纹的行。自然键是 (project,
// device_fingerprint)（决策 26/R2b），partial unique index 保证同一对只能有一行
// ACTIVE，soft-delete 行可共存以回收 slot。
//
// 三张表都带账号级同步元数据（syncmeta_entity.SyncMeta）与 sync_id 部分唯一索引 ——
// 本仓 DDL 一律 not null default ”，唯一索引因此必须是 WHERE sync_id != ” 的部分
// 索引，否则多行空标识互撞。
func migration202609040105() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609040105",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS projects (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	parent_id INTEGER NOT NULL DEFAULT 0,
	name TEXT NOT NULL,
	icon TEXT NOT NULL DEFAULT '',
	color TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL,
	local_path_missing BOOLEAN NOT NULL DEFAULT 0,
	sort_order INTEGER NOT NULL DEFAULT 0,
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_projects_parent_id ON projects(parent_id, status)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_projects_parent_sort
ON projects(parent_id, status, sort_order, id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_projects_sync_id
ON projects(sync_id) WHERE sync_id != ''`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS project_agents (
	project_id INTEGER NOT NULL,
	agent_id INTEGER NOT NULL,
	joined_at INTEGER NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0,
	PRIMARY KEY (project_id, agent_id)
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_project_agents_agent_id ON project_agents(agent_id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_project_agents_sync_id
ON project_agents(sync_id) WHERE sync_id != ''`).Error; err != nil {
				return err
			}

			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS project_locations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	project_id INTEGER NOT NULL,
	device_fingerprint TEXT NOT NULL DEFAULT '',
	path TEXT NOT NULL,
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_project_locations_proj_fingerprint
	ON project_locations(project_id, device_fingerprint) WHERE status = 1`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_project_locations_sync_id
ON project_locations(sync_id) WHERE sync_id != ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS project_locations`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`DROP TABLE IF EXISTS project_agents`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS projects`).Error
		},
	}
}
