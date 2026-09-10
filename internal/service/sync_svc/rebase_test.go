package sync_svc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/project_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-hub/agentre/internal/pkg/syncwire"
)

// ── server 不认识本端的游标（库被重建 / 换了一套自建服务端） ─────────────────
//
// 失效方式是**静默永久失联**：账号的版本序列从头开始，而本端游标停在上一套历史的
// 某个大数上。四道闸门同时焊死——
//
//  1. 已经打过标的行不再入队（ClaimForAccount 只认领不属于当前账号的行，这些行
//     归属没问题），出站队列是空的，因此没有任何上行的触发源；
//  2. ListSince 是 `version > cursor`，游标比新序列的头还大 → 每一轮下行都是空页；
//  3. 空页被 server 当成「消费干净」并刷新 30 天窗口 → 重同步指令永不发出；
//  4. 就算发出了，重同步也只拉不推。
//
// 结果：账号下什么都没有，两台机器互相看不见，而设置里显示「待同步 0 / 无错误 /
// 最近成功」每 30 秒刷新一次。
func TestSyncOnce_GivenServerDoesNotKnowOurCursor_RebasesAndRepushes(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.adapter.rows["p-1"] = "Alpha"
	// 行上盖着上一套 server 的账号与版本号：它「以为自己同步过了」。
	h.state.meta["project:p-1"] = syncmeta_entity.SyncMeta{
		SyncID: "p-1", SyncAccountID: 7, SyncVersion: 500,
	}
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))

	// 新库空空如也：第一次下行被判「不认识这个游标」。
	h.transport.pullErrs = []error{syncwire.ErrCursorUnknown}

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Equal(t, []int64{500, 0}, h.transport.pulledAt, "收到指令后从 0 重来")
	st, err := h.svc.loadCursor(ctx, 7)
	require.NoError(t, err)
	assert.Zero(t, st.Cursor, "游标跟着重置，否则下一轮还是同一条死路")

	require.Len(t, h.transport.pushed, 1, "server 不认识的本地行必须重新上行")
	require.Len(t, h.transport.pushed[0], 1)
	assert.Equal(t, "p-1", h.transport.pushed[0][0].SyncID)
	assert.Zero(t, h.transport.pushed[0][0].BaseVersion,
		"按新建走（R4a）：新库从没见过这个标识，带旧序列的基版本没有意义")
}

// **本轮最大的陷阱。** 重推不得复活已被服务端正当删除的行（R6）。
//
// 本机之所以还留着 p-gone，正是因为游标卡死之后那份墓碑从来没到过；重建时它会随
// 全量快照一起到达，而它在新序列里的版本号（2）远小于本机行上盖着的旧版本号
// （500）——版本守卫「本机版本 >= 来的版本就不落」会把它整个挡掉，于是删除落不了
// 地，紧接着的重推又把它推回账号里：一次静默复活。
func TestRebase_GivenServerTombstone_DoesNotResurrectDeletedRow(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.adapter.rows["p-gone"] = "别人已经删掉的项目"
	h.adapter.rows["p-mine"] = "只有本机有的项目"
	h.state.meta["project:p-gone"] = syncmeta_entity.SyncMeta{
		SyncID: "p-gone", SyncAccountID: 7, SyncVersion: 500,
	}
	h.state.meta["project:p-mine"] = syncmeta_entity.SyncMeta{
		SyncID: "p-mine", SyncAccountID: 7, SyncVersion: 480,
	}
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))

	h.transport.pullErrs = []error{syncwire.ErrCursorUnknown}
	h.transport.pages = []*syncwire.PullPage{{
		Items:      []syncwire.PullItem{{Kind: "project", SyncID: "p-gone", Version: 2, DeletedAt: 1700}},
		NextCursor: 2,
	}, {NextCursor: 2}}

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.NotContains(t, h.adapter.rows, "p-gone", "墓碑照常落地：删除不被复活（R6）")
	require.Len(t, h.transport.pushed, 1)
	assert.Equal(t, []string{"p-mine"}, pushedSyncIDs(h.transport.pushed[0]),
		"只重推 server 不认识的**存活**行；刚被墓碑删掉的那一行一个字都不许推回去")
}

// 全量快照必须真的落地。新序列的版本号从头开始，普遍小于本机行上盖着的旧版本号；
// 不先忘掉旧版本号，这份快照会被版本守卫整个挡在门外——「重同步」于是变成一次
// 什么都没发生的空转。
func TestRebase_GivenSnapshotVersionsBelowLocalOnes_StillLands(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.adapter.rows["p-1"] = "本机这一份"
	h.state.meta["project:p-1"] = syncmeta_entity.SyncMeta{
		SyncID: "p-1", SyncAccountID: 7, SyncVersion: 500,
	}
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))

	h.transport.pullErrs = []error{syncwire.ErrCursorUnknown}
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{
			{Kind: "project", SyncID: "p-1", Version: 3, Payload: []byte(`{"name":"新库这一份"}`)},
		},
		NextCursor: 3,
	}, {NextCursor: 3}}

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Equal(t, "新库这一份", h.adapter.rows["p-1"], "快照为准")
	assert.Equal(t, int64(3), h.state.meta["project:p-1"].SyncVersion, "改用新序列的版本号")
	assert.Empty(t, h.transport.pushed, "server 认识的行不重推——那只会凭空造出一次冲突")
}

// 本机路径的上报（R16）判「发不发」看的是内容指纹：内容没变就不发。那份指纹同样是
// 一份「相对上一套 server」的缓存，而新库那边一条路径都没有——不作废它，web 端会把
// 每个项目永远显示成「本机未配置路径」，且因为上报是纯投影，谁都不会报错。
func TestRebase_InvalidatesTheLocalPathReportFingerprint(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	registerProjects(t, []*project_entity.Project{projectRowForAccount("p-1", "/Users/me/a", 7)})
	h.adapter.rows["p-1"] = "Alpha"
	h.state.meta["project:p-1"] = syncmeta_entity.SyncMeta{
		SyncID: "p-1", SyncAccountID: 7, SyncVersion: 500,
	}
	// 上一套 server 上报成功过一次，此后本机路径一个字都没改。
	require.NoError(t, h.svc.reportLocalPathsOnce(ctx))
	require.Len(t, h.transport.localPathReports, 1)
	require.NoError(t, h.svc.reportLocalPathsOnce(ctx))
	require.Len(t, h.transport.localPathReports, 1, "内容没变就不发")

	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))
	h.transport.pullErrs = []error{syncwire.ErrCursorUnknown}
	require.NoError(t, h.svc.SyncOnce(ctx))

	require.NoError(t, h.svc.reportLocalPathsOnce(ctx))
	assert.Len(t, h.transport.localPathReports, 2, "重建之后整份快照要重新送一次")
}

// 规格 2026-09-04 决策 1 推翻了 R13a：换账号登录之后，本地那些属于**上一个**账号的
// 存活行归入当前账号并正常上行。重建这条路径上同样如此——它与登录后的第一轮走的是
// 同一个认领入口。
//
// 基版本必须是 0：上一个账号那套序列里的版本号在这个账号里既不可比、也不能当基版本
// 用（决策 2），按 R4a 当新建。
func TestRebase_GivenRowsOfAnotherAccount_ClaimsThemIntoTheCurrentAccount(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.adapter.rows["p-theirs"] = "上一个账号的项目"
	h.state.meta["project:p-theirs"] = syncmeta_entity.SyncMeta{
		SyncID: "p-theirs", SyncAccountID: 999, SyncVersion: 500,
	}
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))
	h.transport.pullErrs = []error{syncwire.ErrCursorUnknown}

	require.NoError(t, h.svc.SyncOnce(ctx))

	// 这一轮推两次是这条路径本来就有的形状：认领先推一次，随后下行收到「游标不认识」
	// → 重建 → requeueUnknownRows 把新库不认识的行再推一次。这里守的是内容，不是批次数。
	var sent int
	for _, batch := range h.transport.pushed {
		for _, item := range batch {
			if item.SyncID != "p-theirs" {
				continue
			}
			sent++
			assert.Zero(t, item.BaseVersion, "上一套序列的版本号不是这个账号的基版本")
		}
	}
	assert.Positive(t, sent, "上一个账号的行要归入当前账号并上行")
	assert.Equal(t, int64(7), h.state.meta["project:p-theirs"].SyncAccountID, "归属改成当前账号")
}

// 下行的其它失败照旧只是一次失败：不重建、不重推，下一轮按退避重试（R7）。
// 把每一次网络抖动都当成「server 的历史没了」，代价是每 30 秒推一遍整个工作区。
func TestSyncOnce_GivenOrdinaryPullFailure_DoesNotRebase(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.adapter.rows["p-1"] = "Alpha"
	h.state.meta["project:p-1"] = syncmeta_entity.SyncMeta{
		SyncID: "p-1", SyncAccountID: 7, SyncVersion: 500,
	}
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 500}))
	h.transport.pullErr = errors.New("dial tcp: connection refused")

	require.Error(t, h.svc.SyncOnce(ctx))

	assert.Equal(t, []int64{500}, h.transport.pulledAt, "没有第二次下行")
	assert.Empty(t, h.transport.pushed)
	st, err := h.svc.loadCursor(ctx, 7)
	require.NoError(t, err)
	assert.Equal(t, int64(500), st.Cursor, "游标不动")
}

// 反向的那一半，同样是「墓碑不被复活」，但守的是**另一条**路径：R6a 的重同步
// （上行被拒，「你离线太久」）绝不能顺手把 server 不认识的行也推上去。
//
// 那里的「server 不认识」有完全不同的成因：本端离线超过了墓碑窗口，删除的证据已经
// 被 server 正当回收，本机这一份是个僵尸。R6a 就是为它写的——拦下，以「超时未上传」
// 进 R5 列表。把本轮的重推逻辑接到那条路上，等于把 R6a 亲手拆了。
func TestFlush_GivenResyncRequired_DoesNotRepushRowsTheSnapshotDoesNotKnow(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	// 离线期间新建的行：基版本为空，R6a 明写它照常上行。
	h.adapter.rows["p-new"] = "离线期间新建"
	// 僵尸：server 上已经删掉并回收了墓碑，本机还留着一份。
	h.adapter.rows["p-zombie"] = "server 早就删掉的项目"
	h.state.meta["project:p-zombie"] = syncmeta_entity.SyncMeta{
		SyncID: "p-zombie", SyncAccountID: 7, SyncVersion: 400,
	}

	h.transport.results = func([]syncwire.PushItem) ([]syncwire.PushResult, error) {
		return nil, errors.New("offline")
	}
	h.svc.NotifyLocalChange(ctx, LocalChange{
		Kind: "project", Op: OpCreate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-new"},
	})

	attempted := 0
	h.transport.results = func(items []syncwire.PushItem) ([]syncwire.PushResult, error) {
		attempted++
		if attempted == 1 {
			return nil, syncwire.ErrResyncRequired
		}
		out := make([]syncwire.PushResult, 0, len(items))
		for _, it := range items {
			out = append(out, syncwire.PushResult{
				SyncID: it.SyncID, Kind: it.Kind, Version: 20, Status: syncwire.PushStatusAccepted,
			})
		}
		return out, nil
	}
	// 全量快照里没有 p-zombie：墓碑早就超期被回收了。
	h.transport.pages = []*syncwire.PullPage{{NextCursor: 0}}

	require.NoError(t, h.svc.SyncOnce(ctx))

	last := h.transport.pushed[len(h.transport.pushed)-1]
	assert.Equal(t, []string{"p-new"}, pushedSyncIDs(last),
		"R6a：只有基版本为空的新建照常上行，僵尸一行都不许推回去")
	for _, row := range h.outbound.rows {
		assert.NotEqual(t, "p-zombie", row.EntitySyncID,
			"也不许留在队列里等下一轮——那只是把复活推迟 30 秒")
	}
}

func pushedSyncIDs(items []syncwire.PushItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.SyncID)
	}
	return out
}

// ── 系统 Agent 那一行：换账号时的「缺则补」（规格 2026-09-04 决策 4）────────────
//
// agent_entity.DefaultAgentSyncID 是全仓唯一一个**非随机**同步标识，所有桌面端共用
// 它，因此它也是跨账号认领唯一会撞上的那一个。决策 4 的判据是「一次全量拉取之后
// **仍然**属于别的账号才认领」，而它没有独立的分支：SyncOnce 里认领排在
// ensureServerIdentity 之后，换账号在那一步先拉一份新账号的全量快照——快照里有这个
// 标识，本机那行就已经归当前账号，认领自然跳过；快照里没有，它才被收走并上行。
// 次序反过来，下面两条都会红。

// TestSyncOnce_GivenTheCurrentAccountHasItsOwnSystemAgent_DoesNotClaimTheOldOne
// 决策 4 的「已有则不动」那一半。
//
// 本机那份盖着**上一个**账号的版本号，而它通常远大于新账号那份。版本守卫是「本机
// 版本 >= 来的版本就不落」，读的 FindVersion 只按同步标识过滤、不带账号条件。所以
// 清版本号这一步若只清当前账号名下的行（换账号时那是空集），新账号那份就被挡在门
// 外：本机那行仍归上一个账号 → 被认领进新账号 → 上行时撞上同一个标识 → server 判
// conflict、按 R4 后到者胜——**上一个账号的 CEO 助手把新账号的覆盖了**，每切一次
// 账号发生一次（决策 4 拒掉的「无条件认领」正是这条）。
func TestSyncOnce_GivenTheCurrentAccountHasItsOwnSystemAgent_DoesNotClaimTheOldOne(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	agents := h.registerSystemAgent("上一个账号的 CEO 助手", 5000)
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 5000}))
	// 记下当前身份，下面那次换账号才会被认出来（没有记录 = 老装机升级，不算换）。
	require.NoError(t, h.svc.saveServerIdentity(ctx, serverIdentity{ServerURL: serverA, AccountID: 7}))
	// 新账号自己那份在它自己的序列里版本号很小。
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{{
			Kind: syncwire.KindAgent, SyncID: agent_entity.DefaultAgentSyncID,
			Payload: []byte(`{"name":"新账号的 CEO 助手"}`), Version: 3, UpdatedAt: 1,
		}},
		NextCursor: 3,
	}}

	h.row.ServerUserID = 8

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Equal(t, "新账号的 CEO 助手", agents.rows[agent_entity.DefaultAgentSyncID],
		"新账号那份必须落得下去")
	assert.Equal(t, int64(8),
		h.state.meta[syncwire.KindAgent+":"+agent_entity.DefaultAgentSyncID].SyncAccountID)
	assert.Empty(t, h.transport.pushed,
		"落地之后这一行已属于当前账号，认领不再收它，也就不会把上一个账号的内容推上去")
}

// TestSyncOnce_GivenTheCurrentAccountHasNoSystemAgent_ClaimsItAfterTheFullPull
// 决策 4 的「缺则补」那一半：当前账号如果是 web 上新建、从没有过桌面端，它根本没有
// CEO 助手。此时**不**补就又是一处「登录了却不一致」（决策 4 拒掉的「永不认领」）。
//
// 「拉完仍归别人」正好等价于「当前账号没有自己那一份」，所以这里不需要第二套判据：
// 全量快照一行都没送来，本机那行仍归上一个账号，认领照常收走它。
func TestSyncOnce_GivenTheCurrentAccountHasNoSystemAgent_ClaimsItAfterTheFullPull(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.registerSystemAgent("这台机器 seed 的 CEO 助手", 5000)
	require.NoError(t, h.svc.saveCursor(ctx, cursorState{AccountID: 7, Cursor: 5000}))
	require.NoError(t, h.svc.saveServerIdentity(ctx, serverIdentity{ServerURL: serverA, AccountID: 7}))
	// h.transport.pages 留空：新账号是空的，全量快照一行都没有。

	h.row.ServerUserID = 8

	require.NoError(t, h.svc.SyncOnce(ctx))

	assert.Contains(t, h.transport.pulledAt, int64(0), "换账号先拉一份全量快照")
	require.Len(t, h.transport.pushed, 1)
	require.Len(t, h.transport.pushed[0], 1)
	assert.Equal(t, agent_entity.DefaultAgentSyncID, h.transport.pushed[0][0].SyncID,
		"拉完仍归别人 = 当前账号没有自己那一份，补上去")
	assert.Zero(t, h.transport.pushed[0][0].BaseVersion,
		"上一个账号那套序列里的版本号不是这个账号的基版本（决策 2）")
	assert.Equal(t, int64(8),
		h.state.meta[syncwire.KindAgent+":"+agent_entity.DefaultAgentSyncID].SyncAccountID)
}

// registerSystemAgent 装上 Agent 那一类的替身适配器，并预置本机那条系统 Agent 行：
// 它盖着账号 7 的归属与版本号，也就是「上一个账号那份」。
func (h *harness) registerSystemAgent(name string, version int64) *fakeAdapter {
	agents := newFakeAdapter(syncwire.KindAgent)
	agents.rows[agent_entity.DefaultAgentSyncID] = name
	h.svc.adapters[syncwire.KindAgent] = agents
	h.state.meta[syncwire.KindAgent+":"+agent_entity.DefaultAgentSyncID] = syncmeta_entity.SyncMeta{
		SyncID: agent_entity.DefaultAgentSyncID, SyncAccountID: 7, SyncVersion: version,
	}
	return agents
}
