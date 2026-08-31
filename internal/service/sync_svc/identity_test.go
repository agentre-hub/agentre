package sync_svc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
)

// serverA / serverB 是两套 server 的地址。测试里的「换 server」一律指本机从 A 换到 B。
const (
	serverA = "https://a.example"
	serverB = "https://b.example"
)

// ── 换了一套 server,但两边的用户主键恰好相同 ────────────────────────────────
//
// 这是自建部署的常态:两套 server 各自的第一个用户都是 user_id = 1。本地这一侧的
// 「我是谁」以前只有这个整数(account()),于是换 server 之后本机把 B 的账号认成了
// A 的同一个账号,把 A 的游标、A 序列的版本号原样用在 B 上。
//
// 后果不是报错而是**静默漏数据**:server 端「游标超出序列的头」那道守卫只在返回
// 空页时才判(agentre-server sync_svc.Pull),而 B 的序列只要比这个游标长,拉回来的
// 就是非空页——守卫不触发,B 上版本号 ≤ 旧游标的对象一条都拉不到,而游标只增不减,
// 它们再也不会被拉到。
func TestSyncOnce_GivenSameUserIDOnAnotherServer_RebasesInsteadOfReusingTheCursor(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.adapter.rows["p-1"] = "Alpha"
	// 本机的同步状态属于 A:游标 500、行上盖着 A 序列的版本号。
	h.state.meta["project:p-1"] = syncmeta_entity.SyncMeta{
		SyncID: "p-1", SyncAccountID: 7, SyncVersion: 500,
	}
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))
	require.NoError(t, h.svc.saveServerIdentity(ctx, serverIdentity{ServerURL: serverA, AccountID: 7}))

	// 换到 B,用户主键撞上同一个 7。B 的历史是满的,拉回来的页非空。
	h.row.ServerURL = serverB

	require.NoError(t, h.svc.SyncOnce(ctx))

	require.NotEmpty(t, h.transport.pulledAt)
	assert.Equal(t, int64(0), h.transport.pulledAt[0],
		"B 的历史要从头拉:沿用 A 的游标会把 B 上版本号 ≤ 500 的对象永久漏掉")

	st, err := h.svc.loadCursor(ctx, 7)
	require.NoError(t, err)
	assert.Zero(t, st.Cursor, "A 的游标必须作废,它是上一套版本序列里的坐标")

	// 本机那些属于 A 的行会不会被推到 B 上,是**归属**的问题,由
	// TestSyncOnce_GivenAnotherServerWithTheSameRemoteUserID_DoesNotPushTheOldAccountsRows
	// 守：答案是不会。本用例只管坐标。
}

// 认对了才有意义:同一套 server、同一个账号时游标必须原样沿用,否则每 30 秒就是一次
// 全量重拉。
func TestSyncOnce_GivenSameServerAndAccount_KeepsTheCursor(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))
	require.NoError(t, h.svc.saveServerIdentity(ctx, serverIdentity{ServerURL: serverA, AccountID: 7}))

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Equal(t, []int64{500}, h.transport.pulledAt, "接着上次的进度拉")
}

// 同一套 server 上换了账号,同样是换身份:A 的游标属于上一个账号的版本序列。
func TestSyncOnce_GivenAnotherAccountOnTheSameServer_Rebases(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))
	require.NoError(t, h.svc.saveServerIdentity(ctx, serverIdentity{ServerURL: serverA, AccountID: 9}))

	require.NoError(t, h.svc.SyncOnce(ctx))

	require.NotEmpty(t, h.transport.pulledAt)
	assert.Equal(t, int64(0), h.transport.pulledAt[0])
}

// 老装机升级上来时本地没有这条身份记录。它**不该**被当成一次换 server:那会让所有
// 存量用户在升级后各做一次全量重同步。记下当前身份、照常按游标增量拉即可——真的
// 在升级前换过 server 的那些,server 的「游标超出序列的头」守卫仍然接着(rebase.go)。
func TestSyncOnce_GivenNoRecordedIdentity_AdoptsCurrentWithoutRebasing(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Equal(t, []int64{500}, h.transport.pulledAt, "升级不触发全量重同步")

	got, err := h.svc.loadServerIdentity(ctx)
	require.NoError(t, err)
	assert.Equal(t, serverIdentity{ServerURL: serverA, AccountID: 7}, got,
		"当前身份要记下来,下一次换 server 才认得出")
}

// 身份记录只在恢复真的做完之后才更新。中途失败却把新身份记下来的话,下一轮就认为
// 「已经是 B 了」,而本地那套 A 的版本号还留在原处——一次静默的半吊子状态。
func TestSyncOnce_GivenRebaseFails_KeepsOldIdentityForTheNextAttempt(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	require.NoError(t, h.svc.saveServerIdentity(ctx, serverIdentity{ServerURL: serverA, AccountID: 7}))
	h.row.ServerURL = serverB
	h.transport.pullErr = assert.AnError

	require.Error(t, h.svc.SyncOnce(ctx))

	got, err := h.svc.loadServerIdentity(ctx)
	require.NoError(t, err)
	assert.Equal(t, serverA, got.ServerURL, "没恢复成功就还是旧身份,下一轮接着试")
}

// 末尾斜杠、首尾空白不是换 server。它们只是同一个地址的不同写法,当成换 server 会
// 凭空触发一次全量重同步。
func TestServerIdentity_NormalizesTheServerURL(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))
	require.NoError(t, h.svc.saveServerIdentity(ctx, serverIdentity{ServerURL: serverA, AccountID: 7}))
	h.row.ServerURL = "  " + serverA + "/  "

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Equal(t, []int64{500}, h.transport.pulledAt)
}

// ── 归属：撞号的两套 server 不是同一个账号 ──────────────────────────────────
//
// 自建部署里两套 server 的第一个用户都是 user_id = 1。归属判定（行属于谁、队列
// 属于谁）落在 sync_account_id 一个整数上，光存 server 的 user_id，换一套 server
// 之后本机就把 B 的 1 号用户认成 A 的 1 号用户——上一个账号的行会照常上行到新
// server 里去，而 R13a 说的正是这些行不该参与同步。
//
// 修法是让这个整数变成**本机**为 (server 地址, 远端用户主键) 分配的代理键
// （sync_account_repo）：同一对永远同一个键，不同对永远不同。
func TestSyncOnce_GivenAnotherServerWithTheSameRemoteUserID_DoesNotPushTheOldAccountsRows(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.adapter.rows["p-1"] = "Alpha"
	// 一行属于 A 上那个账号的项目。
	h.state.meta["project:p-1"] = syncmeta_entity.SyncMeta{
		SyncID: "p-1", SyncAccountID: 7, SyncVersion: 500,
	}
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))
	require.NoError(t, h.svc.saveServerIdentity(ctx, serverIdentity{ServerURL: serverA, AccountID: 7}))

	// 换到 B。远端 user_id 还是 7 —— 但那是 B 库里的 7 号用户，与 A 的 7 号毫无关系。
	h.row.ServerURL = serverB

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Empty(t, h.transport.pushed,
		"上一个账号的行不许上行到新 server：它们属于 A，B 从来没有过它们")
	assert.Equal(t, int64(7), h.state.meta["project:p-1"].SyncAccountID,
		"也不许就地改归属——那等于替用户把 A 的工作区搬进 B")
}

// 同一套 server 上的同一个远端用户，键必须稳定；否则每次登录都换一个键，
// 本机自己的行会被自己判成「别人的」。
func TestAccount_GivenTheSameServerAndUser_ReturnsAStableKey(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()

	first, _, _, _, ok := h.svc.account(ctx)
	require.True(t, ok)
	second, _, _, _, ok := h.svc.account(ctx)
	require.True(t, ok)

	assert.Equal(t, first, second)
	assert.Equal(t, int64(7), first, "存量行盖着的就是这个数（迁移把它播成了账号键）")
}
