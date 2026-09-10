package sync_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

// R5 的「你的改动被谁覆盖了」现在多了一种来源：**服务端直写**。
//
// server 的组织管理面让浏览器直接建 / 改 / 删部门、Agent 与执行目标，那些行的
// origin_fingerprint 记空串（server 侧决策 21：不是任何一台机器推上来的）。冲突应答里
// 的 OverwrittenOriginFingerprint 因此会是空串，而这一侧要把它如实说成
// 「服务端（浏览器）」——原样留空则落到「不知道被谁覆盖」那一支，把一件确定的事
// 说成未知。
func TestFlush_GivenConflictFromServerDirectWrite_RecordsServerAsOrigin(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "Mine"
	h.transport.results = func(items []syncwire.PushItem) ([]syncwire.PushResult, error) {
		return []syncwire.PushResult{{
			SyncID: items[0].SyncID, Kind: items[0].Kind, Version: 12,
			Status: syncwire.PushStatusConflict, OverwrittenVersion: 11, OverwrittenOriginFingerprint: "",
		}}, nil
	}

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 8},
	})

	require.Len(t, h.lost.rows, 1)
	assert.Equal(t, syncqueue_entity.ReasonOverwritten, h.lost.rows[0].Reason)
	assert.Equal(t, OriginDeviceServer, h.lost.rows[0].OriginDevice)
	assert.NotEmpty(t, h.lost.rows[0].OriginDevice, "空白会把一件确定的事说成未知")
}

// 真的来自某台机器时照旧原样记那台机器的指纹——上面那条改的只是空串这一种取值。
func TestOriginDeviceOf_GivenRealDevice_KeepsTheFingerprint(t *testing.T) {
	assert.Equal(t, "fp-desktop-02", originDeviceOf("fp-desktop-02"))
	assert.Equal(t, OriginDeviceServer, originDeviceOf(""))
}
