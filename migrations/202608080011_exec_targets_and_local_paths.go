package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608080011 合并落地工作区同步前的「执行目标 + 本机路径」数据地基：
//
//  1. agent_exec_targets（R15）——Agent 的有序执行目标列表，并把每个 Agent 现有的
//     agents.agent_backend_id 回填成单元素列表（sort_order=0，语义与转换前一致；
//     agent_backend_id=0 的 Agent 转成空列表）。
//  2. agent_exec_targets.skills_json（R15e）——技能授权下沉到执行目标行，把每个
//     Agent 现有的 agents.skills_json 原样搬进它那唯一一行。
//     agents.agent_backend_id / agents.skills_json 两列保留但不再被读取，由后续
//     轮次删除——同一轮既改结构又删列会让回滚窗口内的数据无处可去。
//  3. chat_sessions.exec_agent_backend_id（R15b）——会话钉住的执行目标档，默认 0
//     （未钉住）；老会话派发时回落到按 R15 顺序挑一档并写回，无需回填。
//  4. projects.local_path_missing（R10/决策 21）——「本机未配置路径」显式状态位；
//     既有行都有本机路径，新列默认 0（已配置），不回填未配置行。
//  5. project_locations 账号内自然键 (project, device_id) → (project,
//     daemon_fingerprint)（决策 26/R2b）：device_id 降级为本地缓存，既有行按
//     device_id 反查 paired_agentreds 换算指纹，反查不到的行丢弃；换键会把多行
//     塌缩成一行，落败行先软删再建部分唯一索引。
//
// 本迁移纯结构 + 一次性回填，不发起任何网络调用。
func migration202608080011() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080011",
		Migrate: func(tx *gorm.DB) error {
			// 1. agent_exec_targets 表 + 索引
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS agent_exec_targets (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	agent_id INTEGER NOT NULL,
	agent_backend_id INTEGER NOT NULL,
	sort_order INTEGER NOT NULL DEFAULT 0,
	skills_json TEXT NOT NULL DEFAULT '[]'
)`).Error; err != nil {
				return err
			}
			// 同一个 Agent 的两档不能同序：读取侧靠 sort_order 定「第一个」，并列
			// 会让派发结果随存储顺序漂移。
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_exec_targets_agent_sort
ON agent_exec_targets(agent_id, sort_order)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_exec_targets_agent_backend_id
ON agent_exec_targets(agent_backend_id)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS agent_exec_target_overrides (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	agent_id INTEGER NOT NULL,
	order_json TEXT NOT NULL DEFAULT '[]',
	updatetime BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_exec_target_overrides_agent
ON agent_exec_target_overrides(agent_id)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS agent_exec_target_overrides`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS agent_exec_targets`).Error
		},
	}
}
