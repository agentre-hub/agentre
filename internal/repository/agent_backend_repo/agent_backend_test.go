package agent_backend_repo_test

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
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/repository/agent_backend_repo"
)

// setupAgentBackendRepoTest 起一个 sqlmock 数据库，返回 ctx / mock / repo。
// 业务代码通过 db.Ctx(ctx) 命中 mock，断言落在「拼出的 SQL 与参数」上。
func setupAgentBackendRepoTest(t *testing.T) (context.Context, sqlmock.Sqlmock, agent_backend_repo.AgentBackendRepo) {
	t.Helper()
	ctx, _, mock := testutils.Database(t)
	return ctx, mock, agent_backend_repo.NewAgentBackend()
}

func TestAgentBackendRepo_Create(t *testing.T) {
	convey.Convey("Create", t, func() {
		ctx, mock, repo := setupAgentBackendRepoTest(t)

		convey.Convey("写入成功并回填自增 ID", func() {
			b := &agent_backend_entity.AgentBackend{
				Type:           string(agent_backend_entity.TypeBuiltin),
				Name:           "默认助手",
				LLMProviderKey: "test-key-uuid-1",
				Status:         consts.ACTIVE,
			}
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `agent_backends`").
				WillReturnResult(sqlmock.NewResult(42, 1))
			mock.ExpectCommit()

			assert.NoError(t, repo.Create(ctx, b))
			assert.Equal(t, int64(42), b.ID)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动报错时透传", func() {
			mock.ExpectBegin()
			mock.ExpectExec("INSERT INTO `agent_backends`").
				WillReturnError(errors.New("boom"))
			mock.ExpectRollback()

			err := repo.Create(ctx, &agent_backend_entity.AgentBackend{
				Type: string(agent_backend_entity.TypeBuiltin), Name: "x", LLMProviderKey: "test-key-uuid-1", Status: consts.ACTIVE,
			})
			assert.EqualError(t, err, "boom")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestAgentBackendRepo_Find(t *testing.T) {
	convey.Convey("Find", t, func() {
		ctx, mock, repo := setupAgentBackendRepoTest(t)

		convey.Convey("命中时返回实体", func() {
			rows := sqlmock.NewRows([]string{"id", "type", "name", "llm_provider_key", "cli_path", "status", "createtime", "updatetime"}).
				AddRow(1, string(agent_backend_entity.TypeBuiltin), "默认助手", "test-key-uuid-2", "", consts.ACTIVE, int64(0), int64(0))
			mock.ExpectQuery("SELECT \\* FROM `agent_backends` WHERE id = \\? AND status = \\? ORDER BY `agent_backends`.`id` LIMIT \\?").
				WithArgs(int64(1), consts.ACTIVE, 1).
				WillReturnRows(rows)

			got, err := repo.Find(ctx, 1)
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, "默认助手", got.Name)
			assert.Equal(t, "test-key-uuid-2", got.LLMProviderKey)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("ErrRecordNotFound 静默吞掉返回 nil", func() {
			mock.ExpectQuery("SELECT \\* FROM `agent_backends` WHERE id = \\? AND status = \\?").
				WithArgs(int64(99), consts.ACTIVE, 1).
				WillReturnError(gorm.ErrRecordNotFound)

			got, err := repo.Find(ctx, 99)
			assert.NoError(t, err)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("其它错误向上抛", func() {
			mock.ExpectQuery("SELECT \\* FROM `agent_backends`").
				WithArgs(int64(1), consts.ACTIVE, 1).
				WillReturnError(sql.ErrConnDone)

			got, err := repo.Find(ctx, 1)
			assert.ErrorIs(t, err, sql.ErrConnDone)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

// TestAgentBackendRepo_ExistsByName 回归 Problem 19 / 决策 25:扫描创建的撞名判据
// 只看 ACTIVE 行(FindByName),导致「扫描建 → 被删 → 再扫描建」每轮都留一条新墓碑。
// ExistsByName 判据改成「同名行存在即为真,无论其状态」,ScanAndCreateAgentBackends
// 用它在真正 Create 之前先行拦截同名墓碑。
func TestAgentBackendRepo_ExistsByName(t *testing.T) {
	convey.Convey("ExistsByName", t, func() {
		ctx, mock, repo := setupAgentBackendRepoTest(t)

		convey.Convey("同名墓碑存在时也算命中,不看 status", func() {
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `agent_backends` WHERE name = \\?").
				WithArgs("Claude Code CLI").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

			got, err := repo.ExistsByName(ctx, "Claude Code CLI")
			assert.NoError(t, err)
			assert.True(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("任何状态下都没有同名行时返回 false", func() {
			mock.ExpectQuery("SELECT count\\(\\*\\) FROM `agent_backends` WHERE name = \\?").
				WithArgs("Codex CLI").
				WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

			got, err := repo.ExistsByName(ctx, "Codex CLI")
			assert.NoError(t, err)
			assert.False(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestAgentBackendRepo_FindByName(t *testing.T) {
	convey.Convey("FindByName", t, func() {
		ctx, mock, repo := setupAgentBackendRepoTest(t)

		convey.Convey("命中时返回实体", func() {
			rows := sqlmock.NewRows([]string{"id", "type", "name", "llm_provider_key", "cli_path", "status", "createtime", "updatetime"}).
				AddRow(7, string(agent_backend_entity.TypeBuiltin), "alpha", "test-key-uuid-1", "", consts.ACTIVE, int64(0), int64(0))
			mock.ExpectQuery("SELECT \\* FROM `agent_backends` WHERE name = \\? AND status = \\? ORDER BY `agent_backends`.`id` LIMIT \\?").
				WithArgs("alpha", consts.ACTIVE, 1).
				WillReturnRows(rows)

			got, err := repo.FindByName(ctx, "alpha")
			assert.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, int64(7), got.ID)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("找不到时返回 nil 而非错误", func() {
			mock.ExpectQuery("SELECT \\* FROM `agent_backends` WHERE name = \\? AND status = \\?").
				WithArgs("beta", consts.ACTIVE, 1).
				WillReturnError(gorm.ErrRecordNotFound)

			got, err := repo.FindByName(ctx, "beta")
			assert.NoError(t, err)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestAgentBackendRepo_List(t *testing.T) {
	convey.Convey("List", t, func() {
		ctx, mock, repo := setupAgentBackendRepoTest(t)

		convey.Convey("按 id 升序过滤 status=ACTIVE", func() {
			rows := sqlmock.NewRows([]string{"id", "type", "name", "llm_provider_key", "cli_path", "status", "createtime", "updatetime"}).
				AddRow(1, string(agent_backend_entity.TypeBuiltin), "a", "test-key-uuid-1", "", consts.ACTIVE, int64(0), int64(0)).
				AddRow(2, string(agent_backend_entity.TypeBuiltin), "b", "test-key-uuid-1", "", consts.ACTIVE, int64(0), int64(0)).
				AddRow(3, string(agent_backend_entity.TypeBuiltin), "c", "test-key-uuid-1", "", consts.ACTIVE, int64(0), int64(0))
			mock.ExpectQuery("SELECT \\* FROM `agent_backends` WHERE status = \\? ORDER BY id ASC").
				WithArgs(consts.ACTIVE).
				WillReturnRows(rows)

			got, err := repo.List(ctx)
			assert.NoError(t, err)
			assert.Len(t, got, 3)
			assert.Equal(t, "a", got[0].Name)
			assert.Equal(t, "c", got[2].Name)
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动报错时透传", func() {
			mock.ExpectQuery("SELECT \\* FROM `agent_backends` WHERE status = \\?").
				WithArgs(consts.ACTIVE).
				WillReturnError(sql.ErrConnDone)

			got, err := repo.List(ctx)
			assert.ErrorIs(t, err, sql.ErrConnDone)
			assert.Nil(t, got)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestAgentBackendRepo_Delete(t *testing.T) {
	convey.Convey("Delete", t, func() {
		ctx, mock, repo := setupAgentBackendRepoTest(t)

		convey.Convey("软删除：UPDATE status=DELETE,updatetime=now WHERE id=?", func() {
			// updatetime 必须跟着软删一起写:决策 24 的墓碑回收要按"距被软删多久"
			// 判定保留期,Delete 不落这个时间戳,回收就无从知道一条墓碑是刚产生的
			// 还是躺了很久——软删前最后一次合法更新的旧 updatetime 不能代表这个。
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `agent_backends` SET `status`=\\?,`updatetime`=\\? WHERE id = \\?").
				WithArgs(consts.DELETE, sqlmock.AnyArg(), int64(5)).
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			assert.NoError(t, repo.Delete(ctx, 5))
			assert.NoError(t, mock.ExpectationsWereMet())
		})

		convey.Convey("驱动报错时透传并回滚", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `agent_backends` SET `status`=\\?,`updatetime`=\\? WHERE id = \\?").
				WithArgs(consts.DELETE, sqlmock.AnyArg(), int64(5)).
				WillReturnError(errors.New("write failed"))
			mock.ExpectRollback()

			err := repo.Delete(ctx, 5)
			assert.EqualError(t, err, "write failed")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

func TestAgentBackendRepo_ClaimRelative_ClonesTargetsAndTombstonesOriginals(t *testing.T) {
	ctx, mock, repo := setupAgentBackendRepoTest(t)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `agent_backends` WHERE device_fingerprint = \\? AND status = \\? ORDER BY id ASC").
		WithArgs("", consts.ACTIVE).
		WillReturnRows(sqlmock.NewRows([]string{"id", "type", "name", "device_fingerprint", "status", "sync_id", "sync_account_id"}).
			AddRow(int64(1), "claudecode", "Local Claude", "", consts.ACTIVE, "backend-old", int64(7)))
	mock.ExpectQuery("SELECT \\* FROM `agent_exec_targets` WHERE agent_backend_id = \\? ORDER BY agent_id ASC, sort_order ASC, id ASC").
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "agent_id", "agent_backend_id", "sort_order", "skills_json", "sync_id", "sync_account_id"}).
			AddRow(int64(3), int64(9), int64(1), 0, `[]`, "target-old", int64(7)))
	mock.ExpectExec("INSERT INTO `agent_backends`").WillReturnResult(sqlmock.NewResult(2, 1))
	mock.ExpectExec("DELETE FROM `agent_exec_targets` WHERE id = \\?").WithArgs(int64(3)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `agent_exec_targets`").WillReturnResult(sqlmock.NewResult(4, 1))
	mock.ExpectExec("UPDATE `agent_backends` SET `status`=\\?").WithArgs(consts.DELETE, int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	claims, err := repo.ClaimRelative(ctx, "sha256:desktop-a")
	require.NoError(t, err)
	require.Len(t, claims, 1)
	assert.Equal(t, "sha256:desktop-a", claims[0].ClaimedBackend.DeviceFingerprint)
	assert.Equal(t, int64(2), claims[0].ClaimedBackend.ID)
	require.Len(t, claims[0].ClaimedTargets, 1)
	assert.Equal(t, int64(2), claims[0].ClaimedTargets[0].AgentBackendID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestAgentBackendRepo_Update(t *testing.T) {
	convey.Convey("Update", t, func() {
		ctx, mock, repo := setupAgentBackendRepoTest(t)

		convey.Convey("Save 全字段更新", func() {
			mock.ExpectBegin()
			mock.ExpectExec("UPDATE `agent_backends` SET").
				WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()

			err := repo.Update(ctx, &agent_backend_entity.AgentBackend{
				ID:             8,
				Type:           string(agent_backend_entity.TypeBuiltin),
				Name:           "renamed",
				LLMProviderKey: "test-key-uuid-2",
				Status:         consts.ACTIVE,
			})
			assert.NoError(t, err)
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	})
}

// TestAgentBackendRepo_ListTombstonesOlderThan 回归决策 24 回收判据的前一半:
// 「墓碑 AND updatetime 早于 cutoff」。ACTIVE 行与 updatetime 落在 cutoff 之后的
// 墓碑都不该出现在结果里。
func TestAgentBackendRepo_ListTombstonesOlderThan(t *testing.T) {
	ctx, mock, repo := setupAgentBackendRepoTest(t)

	rows := sqlmock.NewRows([]string{"id", "type", "name", "status", "updatetime"}).
		AddRow(int64(1), "claudecode", "Claude Code CLI", consts.DELETE, int64(1000))
	mock.ExpectQuery("SELECT \\* FROM `agent_backends` WHERE status = \\? AND updatetime < \\? ORDER BY id ASC").
		WithArgs(consts.DELETE, int64(5000)).
		WillReturnRows(rows)

	got, err := repo.ListTombstonesOlderThan(ctx, 5000)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(1), got[0].ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAgentBackendRepo_ListExecTargetBackendRefs 回归决策 24:回收判据与巡检都要
// 知道"这个 backend id 有没有被任何执行目标提到过"。
func TestAgentBackendRepo_ListExecTargetBackendRefs(t *testing.T) {
	ctx, mock, repo := setupAgentBackendRepoTest(t)

	rows := sqlmock.NewRows([]string{"id", "agent_backend_id"}).
		AddRow(int64(3), int64(51))
	mock.ExpectQuery("SELECT id, agent_backend_id FROM `agent_exec_targets` WHERE agent_backend_id > \\?").
		WithArgs(int64(0)).
		WillReturnRows(rows)

	got, err := repo.ListExecTargetBackendRefs(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, int64(3), got[0].ExecTargetID)
	assert.Equal(t, int64(51), got[0].AgentBackendID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAgentBackendRepo_PurgeTombstones 回归决策 24:回收 = 物理删除，且防御性地
// 只删 status = DELETE 的行。
func TestAgentBackendRepo_PurgeTombstones(t *testing.T) {
	ctx, mock, repo := setupAgentBackendRepoTest(t)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM `agent_backends` WHERE id IN \\(\\?,\\?\\) AND status = \\?").
		WithArgs(int64(1), int64(2), consts.DELETE).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	n, err := repo.PurgeTombstones(ctx, []int64{1, 2})
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestAgentBackendRepo_PurgeTombstones_EmptyIsNoop 空 id 集合不发起任何 SQL。
func TestAgentBackendRepo_PurgeTombstones_EmptyIsNoop(t *testing.T) {
	ctx, mock, repo := setupAgentBackendRepoTest(t)

	n, err := repo.PurgeTombstones(ctx, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
	assert.NoError(t, mock.ExpectationsWereMet())
}
