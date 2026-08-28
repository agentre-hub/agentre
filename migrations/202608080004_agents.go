package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608080004 建 agents 表并 seed 一条 CEO 助手。
//
// 字段语义：
//   - department_id    0 = 未直接挂部门；非 CEO 必须有 department_id 或 parent_agent_id
//   - parent_agent_id  0 = 无上级 Agent
//   - agent_backend_id 0 = 未配置；非 CEO 必填
//   - system_badge     "DEFAULT" = CEO 助手；其它 Agent 留空
//   - avatar_data_url  用户上传头像（data URL）；空时回落 avatar_icon / name[0]
//   - avatar_icon      lucide 图标 key
//   - prompt_json      []string 序列化
//   - skills_json      []AgentSkillItem 序列化
//
// 注：运行态由 chat_sessions.agent_status 承载；Agent 实体不再持有 agent_status。
//
// Seed：用 INSERT ... WHERE NOT EXISTS 保证幂等。
func migration202608080004() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080004",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS agents (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	avatar_color TEXT NOT NULL DEFAULT '',
	avatar_icon TEXT NOT NULL DEFAULT '',
	avatar_data_url TEXT NOT NULL DEFAULT '',
	system_badge TEXT NOT NULL DEFAULT '',
	department_id INTEGER NOT NULL DEFAULT 0,
	parent_agent_id INTEGER NOT NULL DEFAULT 0,
	agent_backend_id INTEGER NOT NULL DEFAULT 0,
	sort_order INTEGER NOT NULL DEFAULT 0,
	prompt_json TEXT NOT NULL DEFAULT '[]',
	skills_json TEXT NOT NULL DEFAULT '[]',
	tools_json TEXT NOT NULL DEFAULT '[]',
	pinned BOOLEAN NOT NULL DEFAULT 0,
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agents_department_id ON agents(department_id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agents_parent_agent_id ON agents(parent_agent_id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agents_agent_backend_id ON agents(agent_backend_id)`).Error; err != nil {
				return err
			}
			return tx.Exec(`INSERT INTO agents (
	name, description, avatar_color, system_badge,
	department_id, agent_backend_id, prompt_json, skills_json, tools_json,
	status, createtime, updatetime
)
SELECT
	'CEO 助手', '默认入口 · 不可删除', 'agent-1', 'DEFAULT',
	0, 0, '[]', '[]', '[{"key":"org","enabled":true}]',
	1, strftime('%s','now'), strftime('%s','now')
WHERE NOT EXISTS (SELECT 1 FROM agents WHERE system_badge = 'DEFAULT')`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			return tx.Exec(`DROP TABLE IF EXISTS agents`).Error
		},
	}
}
