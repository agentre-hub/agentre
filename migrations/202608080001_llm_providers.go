package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// migration202608080001 建 llm_providers 表 —— 用户配置的 LLM 供应商，以及它下挂
// 的 llm_provider_models 表 —— 该供应商启用的具体模型。
//
// llm_providers 字段语义：
//   - type           cago provider 实现：anthropic / openai-chat / openai-response
//   - name           用户可见名称
//   - api_key        明文 API Key（后续单独迭代加密）
//   - base_url       自定义 endpoint，留空走 provider 默认值
//   - provider_key   稳定 UUID，跨机器引用用，agent_backends.llm_provider_key 指向它
//   - status         cago consts: ACTIVE / DELETE
//
// llm_provider_models 字段语义（model / max_output / context_window 落在这张表，
// 不在 llm_providers 上——一个供应商可以启用多个模型）：
//   - model_key      稳定 ModelKey，跨机器引用用
//   - model_id       调用 provider API 时用的模型 id
//   - context_window 上下文窗口 token 数（0 = 走 cago catalog 默认）
//   - max_output     单次响应最大输出 token 数（0 = 走 cago catalog 默认）
func migration202608080001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202608080001",
		Migrate: func(tx *gorm.DB) error {
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS llm_providers (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	type TEXT NOT NULL,
	name TEXT NOT NULL,
	api_key TEXT NOT NULL DEFAULT '',
	base_url TEXT NOT NULL DEFAULT '',
	provider_key TEXT NOT NULL DEFAULT '',
	enabled INTEGER NOT NULL DEFAULT 1,
	default_model_key TEXT NOT NULL DEFAULT '',
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
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_llm_providers_provider_key ON llm_providers(provider_key)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_llm_providers_sync_id
ON llm_providers(sync_id) WHERE sync_id != ''`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE TABLE IF NOT EXISTS llm_provider_models (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	provider_id INTEGER NOT NULL,
	model_key TEXT NOT NULL DEFAULT '',
	model_id TEXT NOT NULL DEFAULT '',
	name TEXT NOT NULL DEFAULT '',
	context_window INTEGER NOT NULL DEFAULT 0,
	max_output INTEGER NOT NULL DEFAULT 0,
	enabled INTEGER NOT NULL DEFAULT 1,
	status INTEGER NOT NULL DEFAULT 1,
	createtime INTEGER NOT NULL DEFAULT 0,
	updatetime INTEGER NOT NULL DEFAULT 0
)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_llm_provider_models_model_key
ON llm_provider_models(model_key)`).Error; err != nil {
				return err
			}
			if err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uniq_llm_provider_models_provider_model_id
ON llm_provider_models(provider_id, model_id)`).Error; err != nil {
				return err
			}
			return tx.Exec(`CREATE INDEX IF NOT EXISTS idx_llm_provider_models_provider
ON llm_provider_models(provider_id, status)`).Error
		},
		Rollback: func(tx *gorm.DB) error {
			if err := tx.Exec(`DROP TABLE IF EXISTS llm_provider_models`).Error; err != nil {
				return err
			}
			return tx.Exec(`DROP TABLE IF EXISTS llm_providers`).Error
		},
	}
}
