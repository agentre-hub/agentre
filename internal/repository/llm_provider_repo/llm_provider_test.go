package llm_provider_repo_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
)

// setupLLMProviderRepoTest 起一个 sqlmock 数据库，返回 ctx / mock / repo。
// 业务代码通过 db.Ctx(ctx) 命中 mock，断言落在「拼出的 SQL 与参数」上。
func setupLLMProviderRepoTest(t *testing.T) (context.Context, sqlmock.Sqlmock, llm_provider_repo.LLMProviderRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, llm_provider_repo.NewLLMProvider()
}

// providerRows 构造一行 sqlmock 返回值（llm_providers 新 schema：无 model/max_output/
// context_window，新增 enabled + default_model_key）。
func providerRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "type", "name", "api_key", "base_url", "enabled",
		"default_model_key", "provider_key", "status", "createtime", "updatetime",
	})
}

func modelRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "provider_id", "model_key", "model_id", "name",
		"context_window", "max_output", "enabled", "status", "createtime", "updatetime",
	})
}

func TestLLMProviderRepo_Find(t *testing.T) {
	convey.Convey("Find", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("命中时返回实体（含 enabled/default_model_key）", func() {
			rows := providerRows().AddRow(
				1, string(llm_provider_entity.TypeAnthropic), "claude", "sk-test", "",
				llm_provider_entity.EnabledOn, "mk-1", "uuid-1", consts.ACTIVE, int64(0), int64(0),
			)
			mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE id = \\? AND status = \\? ORDER BY `llm_providers`.`id` LIMIT \\?").
				WithArgs(int64(1), consts.ACTIVE, 1).
				WillReturnRows(rows)

			got, err := repo.Find(ctx, 1)
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, "claude", got.Name)
			assert.Equal(t, "sk-test", got.APIKey)
			assert.Equal(t, llm_provider_entity.EnabledOn, got.Enabled)
			assert.Equal(t, "mk-1", got.DefaultModelKey)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("ErrRecordNotFound 静默吞掉返回 nil", func() {
			mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE id = \\? AND status = \\?").
				WithArgs(int64(99), consts.ACTIVE, 1).
				WillReturnError(gorm.ErrRecordNotFound)

			got, err := repo.Find(ctx, 99)
			assert.NoError(t, err)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("其它错误向上抛", func() {
			mock.ExpectQuery("SELECT \\* FROM `llm_providers`").
				WithArgs(int64(1), consts.ACTIVE, 1).
				WillReturnError(sql.ErrConnDone)

			got, err := repo.Find(ctx, 1)
			assert.ErrorIs(t, err, sql.ErrConnDone)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_FindByName(t *testing.T) {
	convey.Convey("FindByName", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("命中时返回实体", func() {
			rows := providerRows().AddRow(
				5, string(llm_provider_entity.TypeOpenAIChat), "openai", "sk-1", "",
				llm_provider_entity.EnabledOn, "", "uuid-openai", consts.ACTIVE, int64(0), int64(0),
			)
			mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE name = \\? AND status = \\? ORDER BY `llm_providers`.`id` LIMIT \\?").
				WithArgs("openai", consts.ACTIVE, 1).
				WillReturnRows(rows)

			got, err := repo.FindByName(ctx, "openai")
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, int64(5), got.ID)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("找不到时返回 nil 而非错误", func() {
			mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE name = \\? AND status = \\?").
				WithArgs("missing", consts.ACTIVE, 1).
				WillReturnError(gorm.ErrRecordNotFound)

			got, err := repo.FindByName(ctx, "missing")
			assert.NoError(t, err)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_List(t *testing.T) {
	convey.Convey("List", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("按 id 升序过滤 status=ACTIVE", func() {
			rows := providerRows().
				AddRow(1, string(llm_provider_entity.TypeAnthropic), "a", "k1", "", 1, "mk-a", "ka", consts.ACTIVE, int64(0), int64(0)).
				AddRow(2, string(llm_provider_entity.TypeOpenAIChat), "b", "k2", "", 1, "mk-b", "kb", consts.ACTIVE, int64(0), int64(0))
			mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE status = \\? ORDER BY id ASC").
				WithArgs(consts.ACTIVE).
				WillReturnRows(rows)

			got, err := repo.List(ctx)
			assert.NoError(t, err)
			assert.Len(t, got, 2)
			assert.Equal(t, "a", got[0].Name)
			assert.Equal(t, "b", got[1].Name)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动报错时透传", func() {
			mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE status = \\?").
				WithArgs(consts.ACTIVE).
				WillReturnError(sql.ErrConnDone)

			got, err := repo.List(ctx)
			assert.ErrorIs(t, err, sql.ErrConnDone)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_DeleteWithModels(t *testing.T) {
	convey.Convey("DeleteWithModels", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("Provider 与其 Models 在同一事务内原子软删除（spec：无引用 Provider 的删除事务同时移除其 Models）", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_providers` SET `status`=\\?(,`updatetime`=\\?)? WHERE id = \\?").
				WithArgs(consts.DELETE, int64(3)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE `llm_provider_models` SET `status`=\\?(,`updatetime`=\\?)? WHERE provider_id = \\?").
				WithArgs(consts.DELETE, int64(3)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			assert.NoError(t, repo.DeleteWithModels(ctx, 3))
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("Provider 软删除失败 → 整体回滚，Models 不单独删除（不留半批）", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_providers` SET `status`=\\?(,`updatetime`=\\?)? WHERE id = \\?").
				WithArgs(consts.DELETE, int64(3)).
				WillReturnError(errors.New("write failed"))
			mock.ExpectRollback()

			err := repo.DeleteWithModels(ctx, 3)
			assert.EqualError(t, err, "write failed")
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("Models 软删除失败 → 整体回滚，Provider 已做的软删除一并撤销", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_providers` SET `status`=\\?(,`updatetime`=\\?)? WHERE id = \\?").
				WithArgs(consts.DELETE, int64(3)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("UPDATE `llm_provider_models` SET `status`=\\?(,`updatetime`=\\?)? WHERE provider_id = \\?").
				WithArgs(consts.DELETE, int64(3)).
				WillReturnError(errors.New("models write failed"))
			mock.ExpectRollback()

			err := repo.DeleteWithModels(ctx, 3)
			assert.EqualError(t, err, "models write failed")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_FindByKey(t *testing.T) {
	convey.Convey("FindByKey", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("命中", func() {
			rows := sqlmock.NewRows([]string{"id", "provider_key", "type", "name", "enabled", "status"}).
				AddRow(int64(5), "uuid-abc", "anthropic", "huu-glm", 1, 1)
			mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE provider_key = \\? ORDER BY `llm_providers`.`id` LIMIT \\?").
				WithArgs("uuid-abc", 1).WillReturnRows(rows)

			got, err := repo.FindByKey(ctx, "uuid-abc")
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, int64(5), got.ID)
			assert.Equal(t, "huu-glm", got.Name)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
		convey.Convey("未命中返 nil, nil(GORM RecordNotFound)", func() {
			mock.ExpectQuery("SELECT \\* FROM `llm_providers`").
				WithArgs("missing", 1).WillReturnError(gorm.ErrRecordNotFound)

			got, err := repo.FindByKey(ctx, "missing")
			assert.NoError(t, err)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_BatchFindByKey(t *testing.T) {
	convey.Convey("BatchFindByKey", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("空 keys 快速返回空 map", func() {
			got, err := repo.BatchFindByKey(ctx, []string{})
			assert.NoError(t, err)
			assert.Empty(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("命中多行时按 provider_key 索引", func() {
			rows := sqlmock.NewRows([]string{"id", "provider_key", "type", "name", "enabled", "status"}).
				AddRow(int64(1), "key-1", "anthropic", "prov-1", 1, 1).
				AddRow(int64(2), "key-2", "openai-chat", "prov-2", 1, 1)
			mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE provider_key IN \\(\\?,\\?\\) AND status = \\?").
				WithArgs("key-1", "key-2", consts.ACTIVE).
				WillReturnRows(rows)

			got, err := repo.BatchFindByKey(ctx, []string{"key-1", "key-2"})
			assert.NoError(t, err)
			assert.Len(t, got, 2)
			assert.Equal(t, "prov-1", got["key-1"].Name)
			assert.Equal(t, "prov-2", got["key-2"].Name)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动报错时透传", func() {
			mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE provider_key IN \\(\\?\\) AND status = \\?").
				WithArgs("key-1", consts.ACTIVE).
				WillReturnError(errors.New("db error"))

			got, err := repo.BatchFindByKey(ctx, []string{"key-1"})
			assert.EqualError(t, err, "db error")
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_Update(t *testing.T) {
	convey.Convey("Update", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("Save 全字段更新", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_providers` SET").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			err := repo.Update(ctx, &llm_provider_entity.LLMProvider{
				ID:              8,
				Type:            string(llm_provider_entity.TypeOpenAIChat),
				Name:            "renamed",
				APIKey:          "sk-new",
				Enabled:         llm_provider_entity.EnabledOn,
				DefaultModelKey: "mk-1",
				Status:          consts.ACTIVE,
			})
			assert.NoError(t, err)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

// ── Model CRUD ──────────────────────────────────────────────────────────────

func TestLLMProviderRepo_CreateModel(t *testing.T) {
	convey.Convey("CreateModel", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("写入成功并回填自增 ID", func() {
			m := &llm_provider_model_entity.LLMProviderModel{
				ProviderID: 3,
				ModelKey:   "mk-1",
				ModelID:    "claude-sonnet-4-6",
				Enabled:    llm_provider_model_entity.EnabledOn,
				Status:     consts.ACTIVE,
			}
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `llm_provider_models`").
				WillReturnResult(sqlmock.NewResult(9, 1))
			mock.ExpectCommit()

			assert.NoError(t, repo.CreateModel(ctx, m))
			assert.Equal(t, int64(9), m.ID)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动报错时透传并回滚", func() {
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `llm_provider_models`").
				WillReturnError(errors.New("boom"))
			mock.ExpectRollback()

			err := repo.CreateModel(ctx, &llm_provider_model_entity.LLMProviderModel{
				ProviderID: 3, ModelKey: "mk-1", ModelID: "x", Status: consts.ACTIVE,
			})
			assert.EqualError(t, err, "boom")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_UpdateModel(t *testing.T) {
	convey.Convey("UpdateModel", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("model_key 不可变：UPDATE 不包含 model_key 列", func() {
			// 枚举 GORM Save+Omit(model_key) 的 SET 列，逐字不含 model_key —— 一旦
			// 实现把 model_key 写进 UPDATE，这条 regex 匹配不到 → RED。
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_provider_models` SET " +
				"`provider_id`=\\?,`model_id`=\\?,`name`=\\?,`context_window`=\\?,`max_output`=\\?," +
				"`enabled`=\\?,`status`=\\?,`createtime`=\\?,`updatetime`=\\? WHERE `id` = \\?" +
				"").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			err := repo.UpdateModel(ctx, &llm_provider_model_entity.LLMProviderModel{
				ID:         4,
				ProviderID: 3,
				ModelKey:   "mk-immutable",
				ModelID:    "claude-haiku-4-5",
				Name:       "haiku",
				Status:     consts.ACTIVE,
			})
			assert.NoError(t, err)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动报错时透传并回滚", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_provider_models` SET").
				WillReturnError(errors.New("write failed"))
			mock.ExpectRollback()

			err := repo.UpdateModel(ctx, &llm_provider_model_entity.LLMProviderModel{
				ID: 4, ProviderID: 3, ModelKey: "mk", ModelID: "x", Status: consts.ACTIVE,
			})
			assert.EqualError(t, err, "write failed")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_FindModel(t *testing.T) {
	convey.Convey("FindModel", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("命中时返回实体", func() {
			rows := modelRows().AddRow(
				9, 3, "mk-1", "claude-sonnet-4-6", "sonnet", 200000, 4096,
				llm_provider_model_entity.EnabledOn, consts.ACTIVE, int64(0), int64(0),
			)
			mock.ExpectQuery("SELECT \\* FROM `llm_provider_models` WHERE id = \\? AND status = \\? ORDER BY `llm_provider_models`.`id` LIMIT \\?").
				WithArgs(int64(9), consts.ACTIVE, 1).
				WillReturnRows(rows)

			got, err := repo.FindModel(ctx, 9)
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, "mk-1", got.ModelKey)
			assert.Equal(t, "claude-sonnet-4-6", got.ModelID)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("未命中返回 nil 而非错误", func() {
			mock.ExpectQuery("SELECT \\* FROM `llm_provider_models` WHERE id = \\? AND status = \\?").
				WithArgs(int64(99), consts.ACTIVE, 1).
				WillReturnError(gorm.ErrRecordNotFound)

			got, err := repo.FindModel(ctx, 99)
			assert.NoError(t, err)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_FindModelByKey(t *testing.T) {
	convey.Convey("FindModelByKey", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("命中（含 enabled=0 的停用模型，用于 fixed-model 失效提示）", func() {
			rows := modelRows().AddRow(
				9, 3, "mk-1", "claude-sonnet-4-6", "sonnet", 200000, 4096,
				llm_provider_model_entity.EnabledOff, consts.ACTIVE, int64(0), int64(0),
			)
			mock.ExpectQuery("SELECT \\* FROM `llm_provider_models` WHERE model_key = \\? AND status = \\? ORDER BY `llm_provider_models`.`id` LIMIT \\?").
				WithArgs("mk-1", consts.ACTIVE, 1).
				WillReturnRows(rows)

			got, err := repo.FindModelByKey(ctx, "mk-1")
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, llm_provider_model_entity.EnabledOff, got.Enabled)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("未命中返回 nil", func() {
			mock.ExpectQuery("SELECT \\* FROM `llm_provider_models`").
				WithArgs("missing", consts.ACTIVE, 1).
				WillReturnError(gorm.ErrRecordNotFound)

			got, err := repo.FindModelByKey(ctx, "missing")
			assert.NoError(t, err)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_ListModels(t *testing.T) {
	convey.Convey("ListModels", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("按 id 升序过滤 provider + status=ACTIVE", func() {
			rows := modelRows().
				AddRow(1, 3, "mk-1", "a", "A", 0, 0, 1, consts.ACTIVE, int64(0), int64(0)).
				AddRow(2, 3, "mk-2", "b", "B", 0, 0, 1, consts.ACTIVE, int64(0), int64(0))
			mock.ExpectQuery("SELECT \\* FROM `llm_provider_models` WHERE provider_id = \\? AND status = \\? ORDER BY id ASC").
				WithArgs(int64(3), consts.ACTIVE).
				WillReturnRows(rows)

			got, err := repo.ListModels(ctx, 3)
			assert.NoError(t, err)
			assert.Len(t, got, 2)
			assert.Equal(t, "mk-1", got[0].ModelKey)
			assert.Equal(t, "mk-2", got[1].ModelKey)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动报错时透传", func() {
			mock.ExpectQuery("SELECT \\* FROM `llm_provider_models` WHERE provider_id = \\? AND status = \\?").
				WithArgs(int64(3), consts.ACTIVE).
				WillReturnError(sql.ErrConnDone)

			got, err := repo.ListModels(ctx, 3)
			assert.ErrorIs(t, err, sql.ErrConnDone)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_CountModelsByProvider(t *testing.T) {
	convey.Convey("CountModelsByProvider", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("按 provider 分组计数 status=ACTIVE 的模型，返回 map", func() {
			mock.ExpectQuery("SELECT provider_id, COUNT\\(\\*\\) as count FROM `llm_provider_models` WHERE status = \\? AND provider_id IN \\(\\?,\\?\\) GROUP BY `provider_id`").
				WithArgs(consts.ACTIVE, int64(1), int64(2)).
				WillReturnRows(sqlmock.NewRows([]string{"provider_id", "count"}).
					AddRow(int64(1), int64(3)).
					AddRow(int64(2), int64(0)))

			got, err := repo.CountModelsByProvider(ctx, []int64{1, 2})
			assert.NoError(t, err)
			assert.Equal(t, map[int64]int64{1: 3, 2: 0}, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("空 providerIDs 直接返回空 map，不发查询", func() {
			got, err := repo.CountModelsByProvider(ctx, []int64{})
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Empty(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_DeleteModel(t *testing.T) {
	convey.Convey("DeleteModel", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("软删除：UPDATE status=DELETE WHERE id=?", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_provider_models` SET `status`=\\?(,`updatetime`=\\?)? WHERE id = \\?").
				WithArgs(consts.DELETE, int64(6)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			assert.NoError(t, repo.DeleteModel(ctx, 6))
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

// ── 原子操作 ────────────────────────────────────────────────────────────────

func TestLLMProviderRepo_CreateWithModels(t *testing.T) {
	convey.Convey("CreateWithModels", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("Provider + Models + 默认模型在同一事务提交", func() {
			p := &llm_provider_entity.LLMProvider{
				Type: string(llm_provider_entity.TypeAnthropic), Name: "claude", Status: consts.ACTIVE,
			}
			models := []*llm_provider_model_entity.LLMProviderModel{
				{ModelKey: "mk-1", ModelID: "claude-sonnet-4-6", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
				{ModelKey: "mk-2", ModelID: "claude-haiku-4-5", Enabled: llm_provider_model_entity.EnabledOn, Status: consts.ACTIVE},
			}

			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `llm_providers`").
				WillReturnResult(sqlmock.NewResult(7, 1))
			mock.ExpectExec("INSERT INTO `llm_provider_models`").
				WillReturnResult(sqlmock.NewResult(9, 1))
			mock.ExpectExec("INSERT INTO `llm_provider_models`").
				WillReturnResult(sqlmock.NewResult(10, 1))
			mock.ExpectExec("UPDATE `llm_providers` SET `default_model_key`=\\? WHERE id = \\?").
				WithArgs("mk-1", int64(7)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			err := repo.CreateWithModels(ctx, p, models, "mk-1")
			assert.NoError(t, err)
			assert.Equal(t, int64(7), p.ID)
			assert.Equal(t, int64(7), models[0].ProviderID)
			assert.Equal(t, int64(7), models[1].ProviderID)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("任一步失败整体回滚，不留半批状态", func() {
			p := &llm_provider_entity.LLMProvider{
				Type: string(llm_provider_entity.TypeAnthropic), Name: "claude", Status: consts.ACTIVE,
			}
			models := []*llm_provider_model_entity.LLMProviderModel{
				{ModelKey: "mk-1", ModelID: "claude-sonnet-4-6", Status: consts.ACTIVE},
			}

			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `llm_providers`").
				WillReturnResult(sqlmock.NewResult(7, 1))
			mock.ExpectExec("INSERT INTO `llm_provider_models`").
				WillReturnError(errors.New("constraint failed"))
			mock.ExpectRollback()

			err := repo.CreateWithModels(ctx, p, models, "mk-1")
			assert.EqualError(t, err, "constraint failed")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_ImportModels(t *testing.T) {
	convey.Convey("ImportModels", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("已存在行补齐 + 新行插入在同一事务提交，不留半批", func() {
			updates := []*llm_provider_model_entity.LLMProviderModel{
				{ID: 4, ProviderID: 3, ModelKey: "mk-up", ModelID: "a", Name: "A", Status: consts.ACTIVE},
			}
			inserts := []*llm_provider_model_entity.LLMProviderModel{
				{ProviderID: 3, ModelKey: "mk-new", ModelID: "b", Status: consts.ACTIVE},
			}

			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_provider_models` SET ").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO `llm_provider_models`").
				WillReturnResult(sqlmock.NewResult(2, 1))
			mock.ExpectCommit()

			assert.NoError(t, repo.ImportModels(ctx, updates, inserts))
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("只新增不补齐：事务内仅 INSERT", func() {
			inserts := []*llm_provider_model_entity.LLMProviderModel{
				{ProviderID: 3, ModelKey: "mk-new", ModelID: "b", Status: consts.ACTIVE},
			}

			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `llm_provider_models`").
				WillReturnResult(sqlmock.NewResult(1, 1))
			mock.ExpectCommit()

			assert.NoError(t, repo.ImportModels(ctx, nil, inserts))
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("空列表直接返回，不产生 SQL", func() {
			assert.NoError(t, repo.ImportModels(ctx, nil, nil))
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("插入失败整体回滚，已存在行补齐也不残留", func() {
			updates := []*llm_provider_model_entity.LLMProviderModel{
				{ID: 4, ProviderID: 3, ModelKey: "mk-up", ModelID: "a", Status: consts.ACTIVE},
			}
			inserts := []*llm_provider_model_entity.LLMProviderModel{
				{ProviderID: 3, ModelKey: "mk-new", ModelID: "b", Status: consts.ACTIVE},
			}

			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `llm_provider_models` SET ").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectExec("INSERT INTO `llm_provider_models`").
				WillReturnError(errors.New("dup model_id"))
			mock.ExpectRollback()

			err := repo.ImportModels(ctx, updates, inserts)
			assert.EqualError(t, err, "dup model_id")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

// ── 引用影响计数 ────────────────────────────────────────────────────────────

func TestLLMProviderRepo_CountProviderReferences(t *testing.T) {
	convey.Convey("CountProviderReferences", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("返回 Backend / Session / Route 三路计数", func() {
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `agent_backends` WHERE llm_provider_key = \\? AND status = \\?").
				WithArgs("uuid-1", consts.ACTIVE).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(2)))
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `chat_sessions` WHERE provider_key = \\? AND status = \\?").
				WithArgs("uuid-1", consts.ACTIVE).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(5)))
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `agent_backends` WHERE status = \\? AND model_routes LIKE \\?").
				WithArgs(consts.ACTIVE, "%uuid-1%").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))

			got, err := repo.CountProviderReferences(ctx, "uuid-1")
			assert.NoError(t, err)
			assert.Equal(t, int64(2), got.Backends)
			assert.Equal(t, int64(5), got.Sessions)
			assert.Equal(t, int64(1), got.Routes)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动报错时透传", func() {
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `agent_backends` WHERE llm_provider_key = \\? AND status = \\?").
				WithArgs("uuid-1", consts.ACTIVE).
				WillReturnError(sql.ErrConnDone)

			_, err := repo.CountProviderReferences(ctx, "uuid-1")
			assert.ErrorIs(t, err, sql.ErrConnDone)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestLLMProviderRepo_CountModelReferences(t *testing.T) {
	convey.Convey("CountModelReferences", t, func() {
		ctx, mock, repo := setupLLMProviderRepoTest(t)

		convey.Convey("返回 Backend / Session / Route 三路计数", func() {
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `agent_backends` WHERE model_key = \\? AND status = \\?").
				WithArgs("mk-1", consts.ACTIVE).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(1)))
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `chat_sessions` WHERE model_key = \\? AND status = \\?").
				WithArgs("mk-1", consts.ACTIVE).
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(3)))
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `agent_backends` WHERE status = \\? AND model_routes LIKE \\?").
				WithArgs(consts.ACTIVE, "%mk-1%").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(0)))

			got, err := repo.CountModelReferences(ctx, "mk-1")
			assert.NoError(t, err)
			assert.Equal(t, int64(1), got.Backends)
			assert.Equal(t, int64(3), got.Sessions)
			assert.Equal(t, int64(0), got.Routes)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动报错时透传", func() {
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `agent_backends` WHERE model_key = \\? AND status = \\?").
				WithArgs("mk-1", consts.ACTIVE).
				WillReturnError(sql.ErrConnDone)

			_, err := repo.CountModelReferences(ctx, "mk-1")
			assert.ErrorIs(t, err, sql.ErrConnDone)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}
