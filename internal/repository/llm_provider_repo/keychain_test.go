package llm_provider_repo_test

import (
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/pkg/keychain"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
)

// noAPIKeyColumn 匹配不含 api_key 列的 SQL：该列必须永远不进写语句。
const noAPIKeyColumn = "(?:[^a]|a[^p])*$"

// brokenKeychain 模拟钥匙串不可用：所有读写都失败。
type brokenKeychain struct{}

func (brokenKeychain) Get(string) (string, error) { return "", errors.New("locked") }
func (brokenKeychain) Set(string, string) error   { return errors.New("locked") }
func (brokenKeychain) Delete(string) error        { return errors.New("locked") }

func TestLLMProviderRepo_APIKeyInKeychain(t *testing.T) {
	convey.Convey("API Key 住在钥匙串，SQLite 列恒为空", t, func() {
		ctx, _, mock := testutils.Database(t)
		kc := keychain.NewMemory()
		repo := llm_provider_repo.NewLLMProviderWithKeychain(kc)
		slot := keychain.LLMProviderAPIKeyAccount("pk-1")

		convey.Convey("CreateWithModels 把 key 写进钥匙串，INSERT 不带 api_key 列，实体仍带 key", func() {
			p := &llm_provider_entity.LLMProvider{ProviderKey: "pk-1", Name: "c", APIKey: "sk-secret", Status: consts.ACTIVE}
			mock.ExpectBegin()
			mock.ExpectExec("^INSERT INTO `llm_providers` " + noAPIKeyColumn).WillReturnResult(sqlmock.NewResult(7, 1))
			mock.ExpectExec("UPDATE `llm_providers` SET `default_model_key`").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			require.NoError(t, repo.CreateWithModels(ctx, p, nil, "mk"))
			got, err := kc.Get(slot)
			require.NoError(t, err)
			assert.Equal(t, "sk-secret", got)
			assert.Equal(t, "sk-secret", p.APIKey)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("Update 写钥匙串且 UPDATE 语句不含 api_key", func() {
			mock.ExpectBegin()
			mock.ExpectExec("^UPDATE `llm_providers` SET " + noAPIKeyColumn).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			require.NoError(t, repo.Update(ctx, &llm_provider_entity.LLMProvider{ID: 8, ProviderKey: "pk-1", APIKey: "sk-new", Status: consts.ACTIVE}))
			got, _ := kc.Get(slot)
			assert.Equal(t, "sk-new", got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("读路径从钥匙串回填 key（Find / FindByKey / List / BatchFindByKey / ListByKeysAnyStatus / FindByName）", func() {
			require.NoError(t, kc.Set(slot, "sk-kc"))
			row := func() *sqlmock.Rows {
				return providerRows().AddRow(1, "anthropic", "c", "", "", llm_provider_entity.EnabledOn, "mk", "pk-1", consts.ACTIVE, int64(0), int64(0))
			}
			mock.ExpectQuery("SELECT \\* FROM `llm_providers`").WillReturnRows(row())
			p, err := repo.Find(ctx, 1)
			require.NoError(t, err)
			assert.Equal(t, "sk-kc", p.APIKey)

			mock.ExpectQuery("SELECT \\* FROM `llm_providers`").WillReturnRows(row())
			p, err = repo.FindByKey(ctx, "pk-1")
			require.NoError(t, err)
			assert.Equal(t, "sk-kc", p.APIKey)

			mock.ExpectQuery("SELECT \\* FROM `llm_providers`").WillReturnRows(row())
			p, err = repo.FindByName(ctx, "c")
			require.NoError(t, err)
			assert.Equal(t, "sk-kc", p.APIKey)

			mock.ExpectQuery("SELECT \\* FROM `llm_providers`").WillReturnRows(row())
			list, err := repo.List(ctx)
			require.NoError(t, err)
			assert.Equal(t, "sk-kc", list[0].APIKey)

			mock.ExpectQuery("SELECT \\* FROM `llm_providers`").WillReturnRows(row())
			m, err := repo.BatchFindByKey(ctx, []string{"pk-1"})
			require.NoError(t, err)
			assert.Equal(t, "sk-kc", m["pk-1"].APIKey)

			mock.ExpectQuery("SELECT \\* FROM `llm_providers`").WillReturnRows(row())
			m, err = repo.ListByKeysAnyStatus(ctx, []string{"pk-1"})
			require.NoError(t, err)
			assert.Equal(t, "sk-kc", m["pk-1"].APIKey)
		})

		convey.Convey("UpsertFromSync（下行）把收到的 key 写进钥匙串而不是列", func() {
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE provider_key").WillReturnRows(providerRowsWithSyncMeta())
			mock.ExpectExec("^INSERT INTO `llm_providers` " + noAPIKeyColumn).WillReturnResult(sqlmock.NewResult(3, 1))
			mock.ExpectExec("UPDATE `llm_provider_models` SET `status`").WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectCommit()
			p := &llm_provider_entity.LLMProvider{ProviderKey: "pk-1", Name: "c", APIKey: "sk-synced", Status: consts.ACTIVE}
			require.NoError(t, repo.UpsertFromSync(ctx, p, []*llm_provider_model_entity.LLMProviderModel{}))
			got, err := kc.Get(slot)
			require.NoError(t, err)
			assert.Equal(t, "sk-synced", got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("空 key 清掉钥匙串槽位", func() {
			require.NoError(t, kc.Set(slot, "old"))
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_providers` SET").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			require.NoError(t, repo.Update(ctx, &llm_provider_entity.LLMProvider{ID: 8, ProviderKey: "pk-1", Status: consts.ACTIVE}))
			_, err := kc.Get(slot)
			assert.ErrorIs(t, err, keychain.ErrNotFound)
		})

		convey.Convey("DeleteWithModels 事务成功后清掉钥匙串槽位", func() {
			require.NoError(t, kc.Set(slot, "sk"))
			mock.ExpectQuery("SELECT `provider_key` FROM `llm_providers` WHERE id = \\?").
				WillReturnRows(sqlmock.NewRows([]string{"provider_key"}).AddRow("pk-1"))
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_providers`").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE `llm_provider_models`").WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			require.NoError(t, repo.DeleteWithModels(ctx, 3))
			_, err := kc.Get(slot)
			assert.ErrorIs(t, err, keychain.ErrNotFound)
		})
	})
}

func TestLLMProviderRepo_KeychainUnavailable(t *testing.T) {
	convey.Convey("钥匙串不可用：写失败报 ErrKeychainUnavailable，且不落明文、不发写 SQL", t, func() {
		ctx, _, mock := testutils.Database(t)
		repo := llm_provider_repo.NewLLMProviderWithKeychain(brokenKeychain{})

		convey.Convey("CreateWithModels", func() {
			err := repo.CreateWithModels(ctx, &llm_provider_entity.LLMProvider{ProviderKey: "pk-1", APIKey: "sk"}, nil, "")
			assert.ErrorIs(t, err, llm_provider_repo.ErrKeychainUnavailable)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
		convey.Convey("Update", func() {
			err := repo.Update(ctx, &llm_provider_entity.LLMProvider{ID: 1, ProviderKey: "pk-1", APIKey: "sk"})
			assert.ErrorIs(t, err, llm_provider_repo.ErrKeychainUnavailable)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
		convey.Convey("UpsertFromSync", func() {
			err := repo.UpsertFromSync(ctx, &llm_provider_entity.LLMProvider{ProviderKey: "pk-1", APIKey: "sk"}, nil)
			assert.ErrorIs(t, err, llm_provider_repo.ErrKeychainUnavailable)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
		convey.Convey("没有注册任何钥匙串（nil）同样拒绝", func() {
			keychain.SetDefault(nil)
			err := llm_provider_repo.NewLLMProvider().Update(ctx, &llm_provider_entity.LLMProvider{ID: 1, ProviderKey: "pk-1", APIKey: "sk"})
			assert.ErrorIs(t, err, llm_provider_repo.ErrKeychainUnavailable)
		})
	})
}
