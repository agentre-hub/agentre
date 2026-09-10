package syncmeta_entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
)

// TestEnsureSyncID_GivenEmpty_GeneratesIdentity R1：行创建/下一次落库时就地生成
// 标识；未登录（accountID 未知/0）也不影响生成——EnsureSyncID 本身不看账号。
func TestEnsureSyncID_GivenEmpty_GeneratesIdentity(t *testing.T) {
	m := &syncmeta_entity.SyncMeta{}
	m.EnsureSyncID()
	assert.NotEmpty(t, m.SyncID, "空标识必须就地生成")
}

// TestEnsureSyncID_GivenExisting_NeverOverwrites 标识终身不变：已有值时不得被
// 覆盖，否则同一个对象在别的端就对不上号了（R1）。
func TestEnsureSyncID_GivenExisting_NeverOverwrites(t *testing.T) {
	m := &syncmeta_entity.SyncMeta{SyncID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}
	m.EnsureSyncID()
	assert.Equal(t, "01ARZ3NDEKTSV4RRFFQ69G5FAV", m.SyncID)
}

// TestNewSyncID_DoesNotCollideAcrossManyCalls 多行空 sync_id 迁移前互不相干，
// 但一旦生成就必须两两不同——否则新生成的标识自己就会撞部分唯一索引。
func TestNewSyncID_DoesNotCollideAcrossManyCalls(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := syncmeta_entity.NewSyncID()
		assert.NotEmpty(t, id)
		_, dup := seen[id]
		assert.False(t, dup, "生成的同步标识不能重复: %s", id)
		seen[id] = struct{}{}
	}
}

// TestEligibleForSync_GivenLoggedOut_AlwaysFalse R12：未登录时（currentAccountID
// == 0）本规格引入的一切都不存在——任何行都不参与同步。
func TestEligibleForSync_GivenLoggedOut_AlwaysFalse(t *testing.T) {
	m := syncmeta_entity.SyncMeta{SyncAccountID: 0}
	assert.False(t, m.EligibleForSync(0))

	claimed := syncmeta_entity.SyncMeta{SyncAccountID: 7}
	assert.False(t, claimed.EligibleForSync(0))
}

// TestEligibleForSync_GivenUnclaimedRow_FirstLoginClaimsIt R12a：登录前已有的
// 行（SyncAccountID == 0）首次登录时照常带自己的标识归入当前账号。
func TestEligibleForSync_GivenUnclaimedRow_FirstLoginClaimsIt(t *testing.T) {
	m := syncmeta_entity.SyncMeta{SyncID: "id-1", SyncAccountID: 0}
	assert.True(t, m.EligibleForSync(9))
}

// TestEligibleForSync_GivenSameAccount_True 本账号自己的行照常参与同步。
func TestEligibleForSync_GivenSameAccount_True(t *testing.T) {
	m := syncmeta_entity.SyncMeta{SyncID: "id-1", SyncAccountID: 9}
	assert.True(t, m.EligibleForSync(9))
}

// TestEligibleForSync_GivenDifferentAccount_False R13a：换账号登录后，本地行
// 记录的账号（非零）与当前登录账号不同时，这些行不上行、也不携带原同步标识。
func TestEligibleForSync_GivenDifferentAccount_False(t *testing.T) {
	m := syncmeta_entity.SyncMeta{SyncID: "id-from-account-A", SyncAccountID: 1}
	assert.False(t, m.EligibleForSync(2), "换账号不带原标识")
}

// TestIsClaimed 报告一行是否已经归属某个账号。
func TestIsClaimed(t *testing.T) {
	assert.False(t, syncmeta_entity.SyncMeta{}.IsClaimed())
	assert.True(t, syncmeta_entity.SyncMeta{SyncAccountID: 3}.IsClaimed())
}
