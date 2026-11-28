package migrations

import (
	"errors"
	"fmt"

	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/pkg/keychain"
)

// migration202611280104 把供应商 API Key 从 SQLite 明文列搬进系统钥匙串（spec 2026-11-25「迁移」
// 规则 9 桌面端部分）。槽位按 provider_key 命名（keychain.LLMProviderAPIKeyAccount），
// 搬完把 llm_providers.api_key 清成空串；此后列恒为空，key 的读写都走 llm_provider_repo。
//
// 先写钥匙串、后清列：任何一行写钥匙串失败就整条迁移失败、列原样不动，下次启动重跑
// （幂等：已清的行 api_key 为空，不会再被选中）。绝不在钥匙串不可用时让明文留在列里
// 悄悄「迁移完成」。软删除的行同样搬（同步墓碑复活时仍要有 key）。
//
// 影响的数据：llm_providers.api_key 被清空，值进入钥匙串。可逆：Rollback 把钥匙串里的 key
// 写回列（尽力而为，取不到的行保持空）。
func migration202611280104() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202611280104",
		Migrate: func(tx *gorm.DB) error {
			return moveProviderKeysToKeychain(tx, keychain.Default())
		},
		Rollback: func(tx *gorm.DB) error {
			return moveProviderKeysBackToColumn(tx, keychain.Default())
		},
	}
}

type providerKeyRow struct {
	ID          int64
	ProviderKey string
	APIKey      string
}

func moveProviderKeysToKeychain(tx *gorm.DB, kc keychain.Keychain) error {
	var rows []providerKeyRow
	if err := tx.Raw("SELECT id, provider_key, api_key FROM llm_providers WHERE api_key <> ''").Scan(&rows).Error; err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	if kc == nil {
		return errors.New("migrate llm provider keys: system keychain unavailable")
	}
	for _, row := range rows {
		account := keychain.LLMProviderAPIKeyAccount(row.ProviderKey)
		if account == "" {
			return fmt.Errorf("migrate llm provider keys: provider id=%d has no provider_key", row.ID)
		}
		if err := kc.Set(account, row.APIKey); err != nil {
			return fmt.Errorf("migrate llm provider keys: write keychain for provider id=%d: %w", row.ID, err)
		}
	}
	return tx.Exec("UPDATE llm_providers SET api_key = '' WHERE api_key <> ''").Error
}

func moveProviderKeysBackToColumn(tx *gorm.DB, kc keychain.Keychain) error {
	if kc == nil {
		return nil
	}
	var rows []providerKeyRow
	if err := tx.Raw("SELECT id, provider_key, api_key FROM llm_providers WHERE api_key = ''").Scan(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		account := keychain.LLMProviderAPIKeyAccount(row.ProviderKey)
		if account == "" {
			continue
		}
		key, err := kc.Get(account)
		if err != nil {
			continue
		}
		if err := tx.Exec("UPDATE llm_providers SET api_key = ? WHERE id = ?", key, row.ID).Error; err != nil {
			return err
		}
	}
	return nil
}
