package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608080003 建 agent_backends 表 —— 一条 backend = 一个可被多个 Agent
// 共享引用的「后端实例」。
//
// 字段语义：
//   - type                    backend 实现类型：builtin / claudecode / codex
//   - name                    用户可见名称
//   - llm_provider_key        绑定的 llm_providers.provider_key（builtin 必填；其它 kind 由 entity.Check 分派）
//   - device_id               空 = 本地；非空 = paired_agentreds.id 字符串化（软引用）
//   - cli_path                claudecode / codex 用，builtin 必须为空
//   - model_routes            仅 claudecode：JSON `{"OPUS":"<provider-key>",...}`，alias 缺省回落主 llm_provider_key
//   - sandbox                 仅 codex：read-only / workspace-write / danger-full-access
//   - approval                仅 codex：untrusted / on-failure / on-request / never
//   - env_json                claudecode / codex 共用：透传环境变量 JSON，保留键拒入
//   - reasoning_effort        思考力度六档（"" / low / medium / high / xhigh / max）
//   - default_permission_mode 仅 claudecode：spawn 时 --permission-mode（"" / default / acceptEdits / plan / bypassPermissions）
//   - status                  cago consts: ACTIVE / DELETE
func migration202608080003() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080003",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS agent_backends (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	type TEXT NOT NULL,
	name TEXT NOT NULL,
	llm_provider_key TEXT NOT NULL DEFAULT '',
	model_key TEXT NOT NULL DEFAULT '',
	device_fingerprint TEXT NOT NULL DEFAULT '',
	cli_path TEXT NOT NULL DEFAULT '',
	model_routes TEXT NOT NULL DEFAULT '{}',
	sandbox TEXT NOT NULL DEFAULT '',
	approval TEXT NOT NULL DEFAULT '',
	env_json TEXT NOT NULL DEFAULT '{}',
	reasoning_effort TEXT NOT NULL DEFAULT '',
	default_permission_mode TEXT NOT NULL DEFAULT '',
	default_model TEXT NOT NULL DEFAULT '',
	openclaw_gateway_url TEXT NOT NULL DEFAULT '',
	openclaw_agent_id TEXT NOT NULL DEFAULT '',
	openclaw_default_model TEXT NOT NULL DEFAULT '',
	openclaw_session_mode TEXT NOT NULL DEFAULT '',
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
