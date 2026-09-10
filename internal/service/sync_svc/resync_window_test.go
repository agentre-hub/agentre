package sync_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

// ── R6a：一次下行要拉到「空页」才算把增量消费干净 ───────────────────────────
//
// server 用「这一页一行都没有」当作「这台设备的游标已经站在账号序列的头上」，
// 并且**只在那一刻**刷新它的 last_sync_at（agentre-server sync_svc/sync.go:323）；
// 而 R6a 的超窗口判定读的正是那个值。
//
// 停在「最后一页非空」上，server 就永远看不到这台设备赶上进度。对**正常**设备
// 这没关系：下一个 30 秒周期的 pull 会从推进后的游标拉回一个空页，窗口随之刷新。
// 但对**超窗口**的设备，那个「下一个周期的 pull」根本不会发生 —— SyncOnce 里
// flush 失败就直接 return，pull 排在它后面（svc.go:249-256），而 flush 正是因为
// 超窗口才失败的。于是：
//
//	上行被拒 → 重同步 → 窗口没刷新 → 重试再被拒 → 报错 → 队列一条不出队 → 下一轮重来
//
// 真运行时抓到过这个死循环：45 秒内 24 次「resync required」，队列一条也上不去，
// 包括 R6a 明写「照常上行」的那一条（离线期间新建、基版本为空）。
// 证据：e2e/scratch/2026-08-07-workspace-sync/resources/r6a-does-not-hold.md
func TestPull_GivenLastPageHasRows_DrainsUntilEmptyPage(t *testing.T) {
	h := newHarness(t, true)
	// 一页就拉完了（HasMore=false），但这一页**有内容**。
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{
			{Kind: "project", SyncID: "p-1", Version: 5, Payload: []byte(`{"name":"A"}`)},
		},
		NextCursor: 5,
		HasMore:    false,
	}}

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	require.Len(t, h.transport.pulledAt, 2,
		"最后一页非空时要再拉一次，让 server 看到「这一页一行都没有」")
	assert.Equal(t, int64(5), h.transport.pulledAt[1],
		"补的那一次要用推进后的游标，不能从头再拉一遍")
	assert.Equal(t, []string{"p-1@A"}, h.adapter.applied, "内容仍然只落地一次")
}

// 反向守卫：本来就没有增量时不该多打一次空转。空页即已消费干净，一次就够。
func TestPull_GivenFirstPageEmpty_DoesNotPullAgain(t *testing.T) {
	h := newHarness(t, true)
	h.transport.pages = []*syncwire.PullPage{{NextCursor: 0, HasMore: false}}

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	assert.Len(t, h.transport.pulledAt, 1, "空页本身就是「已消费干净」，不必再拉")
}

// 分页场景：中间页 HasMore=true 照常连翻，末页非空时仍要补一次空拉。
func TestPull_GivenMultiplePages_DrainsAfterTheLastNonEmptyPage(t *testing.T) {
	h := newHarness(t, true)
	h.transport.pages = []*syncwire.PullPage{
		{
			Items:      []syncwire.PullItem{{Kind: "project", SyncID: "p-1", Version: 3, Payload: []byte(`{"name":"A"}`)}},
			NextCursor: 3,
			HasMore:    true,
		},
		{
			Items:      []syncwire.PullItem{{Kind: "project", SyncID: "p-2", Version: 7, Payload: []byte(`{"name":"B"}`)}},
			NextCursor: 7,
			HasMore:    false,
		},
	}

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	require.Len(t, h.transport.pulledAt, 3, "两页内容 + 一次收尾空拉")
	assert.Equal(t, int64(7), h.transport.pulledAt[2])
}
