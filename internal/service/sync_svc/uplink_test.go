package sync_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
)

// R5 的「你的改动被谁覆盖了」现在多了一种来源：**服务端直写**。
//
// server 的组织管理面让浏览器直接建 / 改 / 删部门、Agent 与执行目标，那些行的
// SourceDeviceID 记 0（server 侧决策 21：0 = 不是任何一台设备推上来的）。冲突应答里
// 的 OverwrittenDeviceID 因此会是 0，而这一侧要把它如实说成「服务端（浏览器）」：
//
//   - 记成 "0" 就是「设备 #0」——账号里没有这台机器，用户按它去找是找不到的；
//   - 记成空串则落到「不知道被谁覆盖」那一支，把一件确定的事说成未知。
func TestFlush_GivenConflictFromServerDirectWrite_RecordsServerAsOrigin(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "Mine"
	h.transport.results = func(items []syncwire.PushItem) ([]syncwire.PushResult, error) {
		return []syncwire.PushResult{{
			SyncID: items[0].SyncID, Kind: items[0].Kind, Version: 12,
			Status: syncwire.PushStatusConflict, OverwrittenVersion: 11, OverwrittenDeviceID: 0,
		}}, nil
	}

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 8},
	})

	require.Len(t, h.lost.rows, 1)
	assert.Equal(t, syncqueue_entity.ReasonOverwritten, h.lost.rows[0].Reason)
	assert.Equal(t, OriginDeviceServer, h.lost.rows[0].OriginDevice)
	assert.NotEqual(t, "0", h.lost.rows[0].OriginDevice, "「设备 #0」是一个不存在的设备")
	assert.NotEmpty(t, h.lost.rows[0].OriginDevice, "空白会把一件确定的事说成未知")
}

// 真的来自某台设备时照旧记那台设备的标识——上面那条改的只是 0 这一种取值。
func TestOriginDeviceOf_GivenRealDevice_KeepsTheDeviceID(t *testing.T) {
	assert.Equal(t, "5", originDeviceOf(5))
	assert.Equal(t, OriginDeviceServer, originDeviceOf(0))
}
