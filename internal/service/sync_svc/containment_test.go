package sync_svc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/model/entity/syncmeta_entity"
	"github.com/agentre-ai/agentre/internal/model/entity/syncqueue_entity"
	"github.com/agentre-ai/agentre/internal/pkg/syncwire"
)

// applyErr 把 applyInbound 的两个返回值收成一个 error。第一个返回值是「本机有没有
// 真的因此改变」，本文件里好几处**恰恰期待它是 false**（暂缓、无处可删的墓碑），
// 所以这里只把错误交出去 —— 谁关心落没落地谁自己断言。
func applyErr(_ bool, err error) error { return err }

// ── 下行的单条隔离 ──────────────────────────────────────────────────────────

// 一行落不了地时，同页其余的照常落地、游标照常推进，那一行留进暂缓队列。
//
// 现状是整页在它那里中断且**不推进游标**：下一轮从同一个游标再拉回同一页、再在
// 同一行中断。那台机器从此收不到任何下行，而 SyncOnce 只在 Status.LastError 里
// 留一行字。
func TestPull_GivenOneRowFailsToApply_KeepsGoingAndAdvancesCursor(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.applyFn = func(in *inbound) error {
		if in.SyncID == "p-poison" {
			return errors.New("UNIQUE constraint failed")
		}
		return nil
	}
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{
			{Kind: "project", SyncID: "p-1", Version: 1, Payload: []byte(`{"name":"A"}`)},
			{Kind: "project", SyncID: "p-poison", Version: 2, Payload: []byte(`{"name":"B"}`)},
			{Kind: "project", SyncID: "p-2", Version: 3, Payload: []byte(`{"name":"C"}`)},
		},
		NextCursor: 3,
	}}

	require.NoError(t, h.svc.SyncOnce(context.Background()))

	assert.Equal(t, "A", h.adapter.rows["p-1"])
	assert.Equal(t, "C", h.adapter.rows["p-2"], "毒丸后面的行照常落地")
	st, err := h.svc.loadCursor(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, int64(3), st.Cursor, "游标照常推进，下一轮不再重放整页")
	require.Len(t, h.inbound.rows, 1, "落不了地的那一行留进暂缓队列，30 天后进 R5 列表")
	assert.Equal(t, "p-poison", h.inbound.rows[0].EntitySyncID)
}

// 重放暂缓队列时，一行失败不该把同一轮里其余的也一并中断。
func TestReplayDeferred_GivenOneRowFails_StillReplaysTheRest(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.adapter.applyFn = func(in *inbound) error {
		if in.SyncID == "p-poison" {
			return errors.New("boom")
		}
		return nil
	}
	for _, in := range []*inbound{
		{Kind: "project", SyncID: "p-poison", Version: 1, Payload: json.RawMessage(`{"name":"B"}`)},
		{Kind: "project", SyncID: "p-ok", Version: 2, Payload: json.RawMessage(`{"name":"C"}`)},
	} {
		body, err := json.Marshal(in)
		require.NoError(t, err)
		require.NoError(t, h.inbound.Create(ctx, &syncqueue_entity.InboundQueueItem{
			SyncAccountID: 7, EntityType: in.Kind, EntitySyncID: in.SyncID,
			PayloadJSON: string(body), ReceivedAt: h.nowMs,
		}))
	}

	require.NoError(t, h.svc.replayDeferred(ctx, 7, appliedKinds{}))

	assert.Equal(t, "C", h.adapter.rows["p-ok"], "坏行不该挡住同一轮里其它行的重放")
	require.Len(t, h.inbound.rows, 1)
	assert.Equal(t, "p-poison", h.inbound.rows[0].EntitySyncID, "坏行留在队列里，不静默丢弃")
}

// ── 重放要过版本守卫 ────────────────────────────────────────────────────────

// 暂缓的那一份可能早已被更新的版本盖过：重放时必须跟 applyInbound 一样看版本，
// 否则一次迟到的重放会把本机回退到旧版本，而且**游标已经越过它**——那一版从此
// 再也不会被重新投递，这个回退是永久的。
func TestReplayDeferred_GivenNewerVersionAlreadyApplied_DoesNotRegress(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.adapter.needRef = ref{Kind: "project", SyncID: "parent"}

	// v5 到达时引用目标还没到 → 暂缓。
	require.NoError(t, applyErr(h.svc.applyInbound(ctx, 7, &inbound{
		Kind: "project", SyncID: "p-1", Version: 5, Payload: json.RawMessage(`{"name":"旧"}`),
	})))
	require.Len(t, h.inbound.rows, 1)

	// 随后 v9 落地（引用目标此时已到）。
	h.state.ids["project:parent"] = 1
	require.NoError(t, applyErr(h.svc.applyInbound(ctx, 7, &inbound{
		Kind: "project", SyncID: "p-1", Version: 9, Payload: json.RawMessage(`{"name":"新"}`),
	})))
	require.Equal(t, "新", h.adapter.rows["p-1"])

	require.NoError(t, h.svc.replayDeferred(ctx, 7, appliedKinds{}))

	assert.Equal(t, "新", h.adapter.rows["p-1"], "重放不得把本机退回旧版本")
	assert.Equal(t, int64(9), h.state.meta["project:p-1"].SyncVersion)
	assert.Empty(t, h.inbound.rows, "已被更新版本取代的暂缓行直接出队")
}

// 硬删的两类（成员关系、执行目标）没有 status 列，行删掉之后同步元数据也跟着没了
// ——真仓储的 SaveMeta 是 `UPDATE … WHERE sync_id = ?`，命中 0 行。于是版本守卫对
// 它们失忆：一份还压在暂缓队列里的旧版本会在墓碑之后被重放，把删掉的行原样建回来，
// 而且游标早已越过那两版，谁也不会再纠正它。
//
// 墓碑落地时必须把同一个同步标识的暂缓行一并清掉——那是旧副本唯一的藏身处。
func TestApplyInbound_GivenTombstoneAfterDeferredOlderVersion_DoesNotResurrect(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.state.hardDeleted["project"] = true // 本用例把替身这一类当作硬删表
	h.adapter.needRef = ref{Kind: "project", SyncID: "parent"}

	// v5 到达时引用目标还没到 → 暂缓。
	require.NoError(t, applyErr(h.svc.applyInbound(ctx, 7, &inbound{
		Kind: "project", SyncID: "t-1", Version: 5, Payload: json.RawMessage(`{"name":"旧"}`),
	})))
	require.Len(t, h.inbound.rows, 1)

	// 随后 v9 的墓碑到达：行删掉，元数据落不下去。
	h.adapter.rows["t-1"] = "旧"
	h.state.meta["project:t-1"] = syncmeta_entity.SyncMeta{SyncID: "t-1", SyncVersion: 4}
	require.NoError(t, applyErr(h.svc.applyInbound(ctx, 7, &inbound{
		Kind: "project", SyncID: "t-1", Version: 9, Deleted: true,
	})))

	// 引用目标终于到了，重放。
	h.state.ids["project:parent"] = 1
	require.NoError(t, h.svc.replayDeferred(ctx, 7, appliedKinds{}))

	assert.NotContains(t, h.adapter.rows, "t-1", "已经删掉的行不得被一份旧副本建回来")
}

// ── 删除不该被引用解析挡住 ──────────────────────────────────────────────────

// 墓碑不写任何引用（adapter.remove 根本不看 resolved），却和普通落地一样要先解析
// 引用。本机没配对那台 agentred 时，一条 backend 的墓碑会在暂缓队列里白等 30 天，
// 然后被当成「引用丢失」丢掉——那一行在本机永远删不掉。
func TestApplyInbound_GivenTombstoneWithUnresolvableRef_RemovesInsteadOfDeferring(t *testing.T) {
	h := newHarness(t, true)
	ctx := context.Background()
	h.adapter.rows["p-1"] = "在本机"
	h.state.meta["project:p-1"] = syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 3}
	h.adapter.needRef = ref{Fingerprint: "fp-never-paired"}

	require.NoError(t, applyErr(h.svc.applyInbound(ctx, 7, &inbound{
		Kind: "project", SyncID: "p-1", Version: 4, Deleted: true,
	})))

	assert.Equal(t, []string{"p-1"}, h.adapter.removed, "删除当场生效")
	assert.Empty(t, h.inbound.rows, "不该为一条墓碑等引用目标")
}

// ── 「没能同步的改动」记的必须是没能同步的那一份 ─────────────────────────────

// 重同步之后，被拦下的那一条要留住**用户自己那一版**。快照刚刚把本地行覆盖成了
// server 的内容，此刻再去读本地行，记下来的是覆盖它的那一份——列表里那条「追回」
// 点下去等于把别人的内容再写一遍。
func TestFlush_GivenResyncRequired_LostChangeKeepsTheContentTheUserPushed(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-old"] = "我改的那一版"
	ctx := context.Background()

	attempted := 0
	h.transport.results = func([]syncwire.PushItem) ([]syncwire.PushResult, error) {
		attempted++
		if attempted == 1 {
			return nil, syncwire.ErrResyncRequired
		}
		return nil, nil
	}
	h.transport.pages = []*syncwire.PullPage{{
		Items: []syncwire.PullItem{{
			Kind: "project", SyncID: "p-old", Version: 9, Payload: []byte(`{"name":"别人那一版"}`),
		}},
		NextCursor: 9,
	}, {NextCursor: 9}}

	h.svc.NotifyLocalChange(ctx, LocalChange{
		Kind: "project", Op: OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-old", SyncVersion: 4},
	})

	require.Len(t, h.lost.rows, 1)
	assert.JSONEq(t, `{"name":"我改的那一版"}`, h.lost.rows[0].PayloadJSON)
	assert.Equal(t, "别人那一版", h.adapter.rows["p-old"], "本地以快照为准")
}

// 冲突时被覆盖掉的是 server 上原来那一版；本端手上那一份是**覆盖别人的**那一份。
// 记下自己推上去的内容等于把「追回被覆盖的那一版」写成「再推一遍刚生效的内容」。
func TestFlush_GivenConflict_LostChangeKeepsTheOverwrittenPayload(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "我的"
	h.transport.results = func(items []syncwire.PushItem) ([]syncwire.PushResult, error) {
		return []syncwire.PushResult{{
			SyncID: items[0].SyncID, Kind: items[0].Kind, Version: 12,
			Status: syncwire.PushStatusConflict, OverwrittenVersion: 11, OverwrittenDeviceID: 5,
			OverwrittenPayload: json.RawMessage(`{"name":"被我覆盖掉的那一版"}`),
		}}, nil
	}

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 8},
	})

	require.Len(t, h.lost.rows, 1)
	assert.JSONEq(t, `{"name":"被我覆盖掉的那一版"}`, h.lost.rows[0].PayloadJSON)
}

// server 单条拒掉一条（对象类型不认、载荷过不了守卫）时：这一条出队——留着它只会
// 每轮再被拒一次，把队列堵死——并进 R5 列表，用户才知道这次改动没同步上去。
func TestFlush_GivenItemRejectedAsInvalid_DequeuesAndRecordsLostChange(t *testing.T) {
	h := newHarness(t, true)
	h.adapter.rows["p-1"] = "我的"
	h.transport.results = func(items []syncwire.PushItem) ([]syncwire.PushResult, error) {
		return []syncwire.PushResult{{
			SyncID: items[0].SyncID, Kind: items[0].Kind, Version: 3,
			Status: syncwire.PushStatusRejected, Reason: "payload_rejected",
		}}, nil
	}

	h.svc.NotifyLocalChange(context.Background(), LocalChange{
		Kind: "project", Op: OpUpdate, Meta: syncmeta_entity.SyncMeta{SyncID: "p-1", SyncVersion: 2},
	})

	assert.Empty(t, h.outbound.rows, "被拒的那一条出队，不再每轮重发")
	require.Len(t, h.lost.rows, 1)
	assert.Equal(t, "p-1", h.lost.rows[0].EntitySyncID)
	assert.Equal(t, syncqueue_entity.ReasonRejected, h.lost.rows[0].Reason)
	assert.JSONEq(t, `{"name":"我的"}`, h.lost.rows[0].PayloadJSON)
}
