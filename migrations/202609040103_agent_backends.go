package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202609040103 建 agent_backends 表 —— 一条 backend = 一个可被多个 Agent
// 共享引用的「后端实例」—— 以及 agent_backend_cli_overlays（同一条 backend 在某台
// agentred 上的 CLI 路径覆盖）。
//
// 字段语义：
//   - type               backend 实现类型：builtin / claudecode / codex / openclaw / …
//   - name               用户可见名称
//   - llm_provider_key   绑定的 llm_providers.provider_key（builtin 必填；其它 kind 由 entity.Check 分派）
//   - device_fingerprint 空 = 本地；非空 = 执行这条 backend 的机器指纹
//   - cli_path           claudecode / codex 用，builtin 必须为空
//   - env_json           claudecode / codex / piagent 共用：透传环境变量 JSON，保留键拒入
//   - reasoning_effort   思考力度六档（"" / low / medium / high / xhigh / max）
//   - config_json        单类型独占的配置格（modelRoutes / sandbox / approval /
//     defaultPermissionMode / defaultModel / openclaw*）。这些格每一格只服务一种
//     BackendKind，摊成列的话每加一种后端就要往这张共用表上追加一批恒空的列，而
//     没有一格被当过查询条件 —— 真相源是 BackendKind，不是列的形态。
//   - status             cago consts: ACTIVE / DELETE
//   - sync_*             账号级同步元数据（syncmeta_entity.SyncMeta）
func migration202609040103() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202609040103",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS agent_backends (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	type TEXT NOT NULL,
	name TEXT NOT NULL,
	llm_provider_key TEXT NOT NULL DEFAULT '',
	model_key TEXT NOT NULL DEFAULT '',
	device_fingerprint TEXT NOT NULL DEFAULT '',
	cli_path TEXT NOT NULL DEFAULT '',
	env_json TEXT NOT NULL DEFAULT '{}',
	reasoning_effort TEXT NOT NULL DEFAULT '',
	config_json TEXT NOT NULL DEFAULT '{}',
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
			if err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_agent_backends_device_fingerprint ON agent_backends(device_fingerprint) WHERE status = 1`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_backends_sync_id ON agent_backends(sync_id) WHERE sync_id != ''`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS agent_backend_cli_overlays (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	backend_sync_id TEXT NOT NULL DEFAULT '',
	agentred_fingerprint TEXT NOT NULL DEFAULT '',
	cli_path TEXT NOT NULL DEFAULT '',
	status INTEGER NOT NULL DEFAULT 1,
	createtime BIGINT NOT NULL DEFAULT 0,
	updatetime BIGINT NOT NULL DEFAULT 0,
	sync_id TEXT NOT NULL DEFAULT '',
	sync_account_id BIGINT NOT NULL DEFAULT 0,
	sync_version BIGINT NOT NULL DEFAULT 0,
	sync_updated_at BIGINT NOT NULL DEFAULT 0,
	sync_origin_fingerprint TEXT NOT NULL DEFAULT '',
	sync_deleted_at BIGINT NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_backend_cli_overlays_natural
ON agent_backend_cli_overlays(backend_sync_id, agentred_fingerprint) WHERE status = 1`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_agent_backend_cli_overlays_sync_id
ON agent_backend_cli_overlays(sync_id) WHERE sync_id != ''`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS agent_backend_cli_overlays`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS agent_backends`).Error
		},
	}
}
