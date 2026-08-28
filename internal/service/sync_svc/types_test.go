package sync_svc

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusSerialization 锁定同步区状态经 Wails 到达前端时字段为 camelCase。
func TestStatusSerialization(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(Status{
		Enabled:         true,
		AccountID:       3,
		Cursor:          42,
		LastSuccessAt:   1234,
		PendingCount:    2,
		DeferredCount:   1,
		LostChangeCount: 5,
		LastError:       "unreachable",
	})
	require.NoError(t, err)

	s := string(data)
	assert.Contains(t, s, `"enabled":true`)
	assert.Contains(t, s, `"accountID":3`)
	assert.Contains(t, s, `"cursor":42`)
	assert.Contains(t, s, `"lastSuccessAt":1234`)
	assert.Contains(t, s, `"pendingCount":2`)
	assert.Contains(t, s, `"deferredCount":1`)
	assert.Contains(t, s, `"lostChangeCount":5`)
	assert.Contains(t, s, `"lastError":"unreachable"`)
}

// TestLostChangeViewSerialization 锁定「没能同步的改动」一行同样是 camelCase。
func TestLostChangeViewSerialization(t *testing.T) {
	t.Parallel()

	data, err := json.Marshal(LostChangeView{
		ID:           9,
		EntityType:   "project",
		EntitySyncID: "sync-1",
		Reason:       "overwritten",
		PayloadJSON:  `{"name":"x"}`,
		OriginDevice: "dev-1",
		BaseVersion:  4,
		OccurredAt:   1234,
	})
	require.NoError(t, err)

	s := string(data)
	assert.Contains(t, s, `"id":9`)
	assert.Contains(t, s, `"entityType":"project"`)
	assert.Contains(t, s, `"entitySyncID":"sync-1"`)
	assert.Contains(t, s, `"reason":"overwritten"`)
	assert.Contains(t, s, `"payloadJSON":"{\"name\":\"x\"}"`)
	assert.Contains(t, s, `"originDevice":"dev-1"`)
	assert.Contains(t, s, `"baseVersion":4`)
	assert.Contains(t, s, `"occurredAt":1234`)
}
