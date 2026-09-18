package sync_svc

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/utils/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/llm_provider_model_entity"
	"github.com/agentre-hub/agentre/internal/repository/llm_provider_repo"
	"github.com/agentre-hub/agentre/internal/repository/syncstate_repo"
	"github.com/agentre-hub/agentre/pkg/syncwire"
)

// capturingLLMProviderRepo 包一层真实 GORM 仓储：转发全部调用（保证真实
// UpsertFromSync 的落地逻辑照常跑），只在 UpsertFromSync 上多做一件事——调用结束
// 后把 provider 行的最终字段值抄一份下来。之所以不直接断言 SQL 参数，是因为这份值
// 就是最终交给 tx.Save 的那份，读它与读驱动收到的绑定参数是同一件事，但不必对着
// 16 列的 UPDATE 猜 GORM 的列序。
type capturingLLMProviderRepo struct {
	llm_provider_repo.LLMProviderRepo
	lastProvider *llm_provider_entity.LLMProvider
}

func (c *capturingLLMProviderRepo) UpsertFromSync(
	ctx context.Context, p *llm_provider_entity.LLMProvider, models []*llm_provider_model_entity.LLMProviderModel,
) error {
	err := c.LLMProviderRepo.UpsertFromSync(ctx, p, models)
	cp := *p
	c.lastProvider = &cp
	return err
}

// TestRestoreLostChange_GivenExistingProvider_UploadsWithPreRestoreVersionAsBase
// 覆盖 Problem 4 的第二半：「同步失败的改动 → 恢复」这条路径不像正常下行的 land()
// 那样在落地后调用 saveInboundMeta 补写 sync_* 元数据（见 lostchange.go 的
// applyLocally 与 downlink.go 的 land 对比）。这原本是安全的——只要 apply 落地时不
// 触碰本已存在的 sync_version，本机上一次拿到的账号级版本号就原样留在行上，恢复后
// 的上行自然拿它当基版本。但改动前的 llmProviderAdapter.apply → UpsertFromSync 用
// tx.Save 整行覆盖，把这一列连同 createtime 一起写成 0，恢复后的上行因此永远以 0
// 为基版本——server 收到一个「看起来像新建」的请求，R5 的「被覆盖」判定也就失了真。
//
// 断言的是 RestoreLostChange 真实会执行的那两步：applyLocally 落地，然后
// FindVersion 读回「这一行现在的版本」——它就是 lostchange.go:134 之后交给
// NotifyLocalChange 当 BaseVersion 上行的那个值。
func TestRestoreLostChange_GivenExistingProvider_UploadsWithPreRestoreVersionAsBase(t *testing.T) {
	ctx, _, mock := testutils.Database(t)

	prevRepo, prevState := llm_provider_repo.LLMProvider(), syncstate_repo.SyncState()
	t.Cleanup(func() {
		llm_provider_repo.RegisterLLMProvider(prevRepo)
		syncstate_repo.RegisterSyncState(prevState)
	})
	capturing := &capturingLLMProviderRepo{LLMProviderRepo: llm_provider_repo.NewLLMProvider()}
	llm_provider_repo.RegisterLLMProvider(capturing)
	syncstate_repo.RegisterSyncState(syncstate_repo.NewSyncState())

	s := &service{
		adapters: map[string]adapter{syncwire.KindLLMProvider: &llmProviderAdapter{}},
		now:      func() int64 { return 1_700_000_000_000 },
	}

	// RestoreLostChange 先查一次当前版本，供整个用例复核「恢复前」到底是哪一版——
	// 这一版号（42）正是本用例要证明会被当作恢复后上行基版本的那个值。
	mock.ExpectQuery("SELECT (.+) FROM `llm_providers` WHERE sync_id = \\?").
		WithArgs("provider-1").
		WillReturnRows(sqlmock.NewRows([]string{"sync_version", "sync_deleted_at"}).AddRow(int64(42), int64(0)))
	preVersion, deleted, found, err := syncstate_repo.SyncState().FindVersion(ctx, syncwire.KindLLMProvider, "provider-1")
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, deleted)
	require.Equal(t, int64(42), preVersion)

	// applyLocally → llmProviderAdapter.apply → 真实 UpsertFromSync：事务内读到的
	// 已存在行带着 sync_version=42、createtime=1000（本机上一次落地时拿到的那份）。
	existingRow := sqlmock.NewRows([]string{
		"id", "provider_key", "type", "name", "api_key", "base_url", "enabled",
		"default_model_key", "status", "createtime", "updatetime",
		"sync_id", "sync_account_id", "sync_version", "sync_updated_at",
		"sync_origin_fingerprint", "sync_deleted_at",
	}).AddRow(
		int64(5), "provider-1", string(llm_provider_entity.TypeAnthropic), "claude-old", "sk-old", "",
		llm_provider_entity.EnabledOn, "", consts.ACTIVE, int64(1000), int64(1000),
		"provider-1", int64(7), int64(42), int64(2000), "device-a", int64(0),
	)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT \\* FROM `llm_providers` WHERE provider_key = \\?").
		WithArgs("provider-1", 1).
		WillReturnRows(existingRow)
	mock.ExpectExec("UPDATE `llm_providers` SET").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE `llm_provider_models` SET `status`=\\?").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	payload, err := json.Marshal(syncwire.LLMProviderPayload{
		Name: "claude-restored", Type: string(llm_provider_entity.TypeAnthropic), Enabled: true,
	})
	require.NoError(t, err)
	in := &inbound{Kind: syncwire.KindLLMProvider, SyncID: "provider-1", Payload: payload}

	require.NoError(t, s.applyLocally(ctx, s.adapters[syncwire.KindLLMProvider], in))
	require.NotNil(t, capturing.lastProvider, "真实 UpsertFromSync 必须真的跑过一次")
	assert.Equal(t, int64(42), capturing.lastProvider.SyncVersion,
		"恢复落地时保留本机原有的 sync_version，不因 apply 重建行结构被清零")
	assert.Equal(t, int64(1000), capturing.lastProvider.Createtime)

	// applyLocally 走的不是正常下行的 land()，不会调用 saveInboundMeta 去另外补写
	// sync_version——这里再读一次，证明行上的版本就是 UpsertFromSync 保留下来的那个
	//42，而不是被后续任何一步重置成了 0。
	mock.ExpectQuery("SELECT (.+) FROM `llm_providers` WHERE sync_id = \\?").
		WithArgs("provider-1").
		WillReturnRows(sqlmock.NewRows([]string{"sync_version", "sync_deleted_at"}).AddRow(int64(42), int64(0)))
	baseVersion, _, found, err := syncstate_repo.SyncState().FindVersion(ctx, syncwire.KindLLMProvider, "provider-1")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, preVersion, baseVersion,
		"恢复后随之而来的上行以这一行恢复前的版本为基线（Problem 4）")

	assert.NoError(t, mock.ExpectationsWereMet())
}
