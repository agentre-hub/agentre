package remote

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-ai/agentre/internal/pkg/agentruntime"
	"github.com/agentre-ai/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-ai/agentre/internal/pkg/jsonrpc"
)

// ── rig:App 刚启动、本进程内一轮都没有在跑 ─────────────────────────────────

// restartConn 造一条「App 刚重启后新连上」的连接:session.list 交出该 daemon 上的
// 会话清单,attach / pull / pendingWaiters 按补齐三步应答。
func restartConn(summaries []wire.SessionSummary, journal []wire.JournaledNotification) *fakeConn {
	c := newFakeConn()
	latest := map[int64]int64{}
	lifecycle := map[int64]string{}
	for _, s := range summaries {
		latest[s.SessionID] = s.LatestSeq
		lifecycle[s.SessionID] = s.LifecycleState
	}
	c.script(func(method string, params, result any) error {
		switch method {
		case wire.MethodSessionList:
			*(result.(*wire.SessionListResult)) = wire.SessionListResult{Sessions: summaries}
		case wire.MethodSessionAttach:
			p := params.(wire.SessionAttachParams)
			*(result.(*wire.SessionAttachResult)) = wire.SessionAttachResult{
				SessionID:      p.SessionID,
				LifecycleState: lifecycle[p.SessionID],
				LatestSeq:      latest[p.SessionID],
			}
		case wire.MethodSessionPull:
			p := params.(wire.SessionPullParams)
			out := wire.SessionPullResult{Cursor: p.Cursor}
			for _, n := range journal {
				// 现存最老的一行:真 daemon 报的是 MIN(seq),被回收掉的前缀不在里面。
				if out.OldestSeq == 0 || n.Seq < out.OldestSeq {
					out.OldestSeq = n.Seq
				}
				if n.Seq > p.Cursor {
					out.Notifications = append(out.Notifications, n)
					out.Cursor = n.Seq
				}
			}
			*(result.(*wire.SessionPullResult)) = out
		case wire.MethodSessionPendingWaiters:
			*(result.(*wire.SessionPendingWaitersResult)) = wire.SessionPendingWaitersResult{}
		}
		return nil
	})
	return c
}

// newRestartRuntime 起一个「App 刚启动」的 runtime:没有 Run 过任何一轮,只有一份
// 存活下来的游标。
func newRestartRuntime(t *testing.T, conn *fakeConn, cursorAt int64) (*Runtime, *fakeCursorPort, *connStateRecorder) {
	t.Helper()
	cursor := &fakeCursorPort{}
	cursor.setLoad(func(int64, string) (int64, bool, error) { return cursorAt, true, nil })
	obs := &connStateRecorder{}
	rt := New(conn,
		WithReconnect(ReconnectFunc(func(context.Context) (agentruntime.DaemonClientPort, string, error) {
			return nil, "", ErrReconnectAbandoned
		})),
		WithDaemonFingerprint(rigFingerprint),
		WithSessionCursor(cursor),
		WithConnStateObserver(obs),
		WithReconnectBackoff([]time.Duration{time.Millisecond}),
		WithCursorFlushInterval(0),
	)
	t.Cleanup(func() { _ = rt.Close() })
	return rt, cursor, obs
}

// catchUpRequest 抽出一次补齐族请求的 (sessionID, peerFingerprint)。
func catchUpRequest(t *testing.T, c fakeCall) (int64, string) {
	t.Helper()
	switch p := c.Params.(type) {
	case wire.SessionAttachParams:
		return p.SessionID, p.PeerFingerprint
	case wire.SessionPullParams:
		return p.SessionID, p.PeerFingerprint
	case wire.SessionPendingWaitersParams:
		return p.SessionID, p.PeerFingerprint
	default:
		t.Fatalf("unexpected catch-up params type %T for %s", c.Params, c.Method)
		return 0, ""
	}
}

// takeTurn 取下一轮合成/自主轮;没有就让用例失败(而不是永久阻塞)。
func takeTurn(t *testing.T, turns <-chan agentruntime.AutonomousTurn) agentruntime.AutonomousTurn {
	t.Helper()
	select {
	case at, ok := <-turns:
		require.True(t, ok, "会话的轮次流被关掉了")
		return at
	case <-time.After(2 * time.Second):
		t.Fatal("等不到轮次:补齐到的内容没有落点")
		return agentruntime.AutonomousTurn{}
	}
}

// Given 一台已认领的 daemon 交回账号级会话清单(每条都带它的发起对端 PeerFingerprint,
// 本客户端自己的会话为空),When 对它们跑补齐三步,Then attach / pull / pendingWaiters
// 请求把清单里的 PeerFingerprint 原样带过去 —— 同账号客户端因此能按 origin 操作别的对端
// 发起的会话(R12 桌面侧);origin 为空的会话则省略该字段(未认领 daemon / 自己对端,
// 行为与今天完全一致)。
func TestCatchUpSessions_PeerOrigin_CarriedIntoRequests(t *testing.T) {
	const (
		peerSession int64 = 77 // 对端 A 发起
		ownSession  int64 = 78 // 本客户端自己的会话,清单里 origin 为空
	)
	conn := restartConn([]wire.SessionSummary{
		{SessionID: peerSession, PeerFingerprint: "peer-A",
			LifecycleState: wire.SessionLifecycleRunning},
		{SessionID: ownSession, LifecycleState: wire.SessionLifecycleRunning},
	}, nil)
	rt, _, _ := newRestartRuntime(t, conn, 0)

	live, err := rt.CatchUpSessions(context.Background(), []int64{peerSession, ownSession})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int64{peerSession, ownSession}, live)

	for _, method := range []string{
		wire.MethodSessionAttach, wire.MethodSessionPull, wire.MethodSessionPendingWaiters,
	} {
		calls := conn.methodCalls(method)
		require.Len(t, calls, 2, "每条运行中的会话都要发一次 %s", method)
		peerSid, peerFP := catchUpRequest(t, calls[0])
		ownSid, ownFP := catchUpRequest(t, calls[1])
		// 调用顺序不保证,按 sessionID 归位。
		if peerSid != peerSession {
			peerSid, ownSid, peerFP, ownFP = ownSid, peerSid, ownFP, peerFP
		}
		assert.Equal(t, peerSession, peerSid)
		assert.Equal(t, ownSession, ownSid)
		assert.Equal(t, "peer-A", peerFP, "%s 必须把对端 origin 原样带进请求", method)
		assert.Empty(t, ownFP, "%s 对空 origin 的会话必须省略该字段(向后兼容)", method)
	}
}

// Given 账号级清单里两个对端各有一条**同号**会话(会话 id 是各客户端本地自增的,重号是
// 常态而非例外),When 本客户端问一眼清单,Then 这一格留的是**自己**那条 —— 别的对端那条
// 不得覆盖它。R12 放宽的是可见性的过滤条件,「会话主键结构不变」:会话主键是
// (对端指纹, 会话 id) 两段,按裸 id 索引会让别的对端那条把自己的顶掉。
func TestSessionSummaries_CollidingSessionID_OwnPeerWins(t *testing.T) {
	const collided int64 = 42
	conn := restartConn([]wire.SessionSummary{
		{SessionID: collided, LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 3},
		{SessionID: collided, PeerFingerprint: "peer-B",
			LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 900},
	}, nil)
	rt, _, _ := newRestartRuntime(t, conn, 0)

	summaries, err := rt.sessionSummaries(context.Background())
	require.NoError(t, err)
	// 高水位取错的后果:turnStartFloor 把别的对端的 900 当成自己会话的下限,自己此后
	// 每一条通知(seq 4、5……)都低于下限被判成重复丢弃 —— 会话没有报错地冻住。
	assert.Equal(t, int64(3), summaries[collided].LatestSeq,
		"同号会话必须留自己那条的高水位")
	assert.Empty(t, rt.originFor(collided),
		"自己那条会话的 origin 必须是空,记成别的对端会让 attach/pull 点名别人的会话")
}

// Given 同上的同号会话清单,When 为自己那条会话跑补齐三步,Then attach / pull /
// pendingWaiters 一律省略 origin(补的是自己那条),而不是点名别的对端 —— 否则拉回来的
// 是别人的通知日志,会被原样重放进自己的转录。
func TestCatchUpSessions_CollidingSessionID_CatchesUpOwnSession(t *testing.T) {
	const collided int64 = 42
	conn := restartConn([]wire.SessionSummary{
		{SessionID: collided, LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 3},
		{SessionID: collided, PeerFingerprint: "peer-B",
			LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 900},
	}, nil)
	rt, _, _ := newRestartRuntime(t, conn, 0)

	_, err := rt.CatchUpSessions(context.Background(), []int64{collided})
	require.NoError(t, err)

	for _, method := range []string{
		wire.MethodSessionAttach, wire.MethodSessionPull, wire.MethodSessionPendingWaiters,
	} {
		calls := conn.methodCalls(method)
		require.NotEmpty(t, calls, "%s 必须发出", method)
		for _, c := range calls {
			sid, fp := catchUpRequest(t, c)
			assert.Equal(t, collided, sid)
			assert.Empty(t, fp, "%s 补的是自己那条同号会话,不得点名别的对端", method)
		}
	}
}

// ── F6:App 重启后的补齐 ────────────────────────────────────────────────────

// Given 桌面 App 退出后重开,本进程内一轮都没有在跑,而 daemon 上这条会话在这段时间
// 里又跑完了一整轮,When 按 exec_device_id 重新连上同一台 daemon 并对它跑补齐三步,
// Then 断连期间产生的通知按序重放,并落成一条**不由用户发起**的轮次交给上层持久化。
//
// 这一条就是本轮的头号用户故事:「退出桌面 App 后下次打开看到这段时间发生的全部
// 内容」。在此之前 catchUpAll 只遍历本进程内在飞的轮次,而 App 刚启动时那个集合必然
// 是空的 —— 补齐一条都不会发起,重放上来的内容也没有任何落点。
func TestCatchUpSessions_NoLiveRun_ReplaysIntoSynthesizedTurn(t *testing.T) {
	journal := []wire.JournaledNotification{
		journaledEvent(4, "away-a"),
		journaledEvent(5, "away-b"),
		journaledDone(6, "sonnet"),
	}
	conn := restartConn([]wire.SessionSummary{{
		SessionID:      rigSessionID,
		LifecycleState: wire.SessionLifecycleIdle,
		LatestSeq:      6,
	}}, journal)
	rt, cursor, _ := newRestartRuntime(t, conn, 3)

	turns := rt.AutonomousTurns(rigSessionID)
	_, err := rt.CatchUpSessions(context.Background(), []int64{rigSessionID})
	require.NoError(t, err)

	at := takeTurn(t, turns)
	assert.Equal(t, TriggerCatchUp, at.Trigger,
		"重放上来的这一轮不是用户发起的,上层要据此把它落成纯 assistant 轮")
	assert.Equal(t, []string{"away-a", "away-b"}, drainTexts(t, at.Events, 2*time.Second))
	assert.Equal(t, "sonnet", at.Result.Model, "终态帧的结果要落在这一轮上")

	assert.Equal(t, []string{
		wire.MethodSessionAttach, wire.MethodSessionPull, wire.MethodSessionPendingWaiters,
	}, conn.catchUpOrder(), "补齐三步的顺序是硬的")
	require.Len(t, conn.methodCalls(wire.MethodSessionList), 1,
		"清单是第一步,每次补齐只问一次(不是每条会话问一次)")

	saved, ok := cursor.lastSaved()
	require.True(t, ok)
	assert.Equal(t, int64(6), saved.Seq, "补齐完游标要推到日志末尾,否则下次开 App 再重放一遍")
}

// Given 补齐把一条会话补完,而 daemon 说它已经空闲(此后不会再有推送),When 补齐返回,
// Then 补齐登记与自主轮镜像一并收摊:轮次流关掉(上层 watcher goroutine 随之退出)、
// liveSessionIDs 不再含它、hasLiveRun 归假。
//
// 不收摊的代价按会话数累加,且到进程退出都不还:
//   - tracked 只在 failSession 删,于是 hasLiveRun() 永远为真 —— 此后每次断连都要为
//     早就结束的会话跑完整退避 + 重接管;
//   - autoSessions 条目只由 closeAllAutoSessions 关,于是每条补齐过的会话留下一个
//     常驻 goroutine,还撑大 liveSessionIDs(),让之后每次重连把它们全部重新接管。
func TestCatchUpSessions_FinishedSession_ReleasesTrackingAndWatcher(t *testing.T) {
	journal := []wire.JournaledNotification{
		journaledEvent(4, "away-a"),
		journaledDone(5, "sonnet"),
	}
	conn := restartConn([]wire.SessionSummary{{
		SessionID:      rigSessionID,
		LifecycleState: wire.SessionLifecycleIdle,
		LatestSeq:      5,
	}}, journal)
	rt, _, _ := newRestartRuntime(t, conn, 3)

	turns := rt.AutonomousTurns(rigSessionID)
	live, err := rt.CatchUpSessions(context.Background(), []int64{rigSessionID})
	require.NoError(t, err)
	require.Empty(t, live)

	at := takeTurn(t, turns)
	assert.Equal(t, []string{"away-a"}, drainTexts(t, at.Events, 2*time.Second),
		"收摊不能吃掉补齐到的内容")

	select {
	case _, ok := <-turns:
		assert.False(t, ok, "轮次流必须关掉,上层的 watcher goroutine 才收得了工")
	case <-time.After(2 * time.Second):
		t.Fatal("轮次流没关:每条补齐过的会话都留下一个常驻 goroutine")
	}
	assert.Empty(t, rt.liveSessionIDs(),
		"补完就结束的会话不该再进重连接管的名单")
	assert.False(t, rt.hasLiveRun(),
		"手上一轮都没有了:再断连不值得重连,否则会借回一条谁也不会归还的连接")
}

// Given daemon 说这条会话还在跑,When 补齐跑完,Then 登记与镜像都留着 —— 它接下来的
// 推送要落在这里,断一次线也必须重连接回去。收摊只针对已经结束的那些。
func TestCatchUpSessions_StillRunningSession_KeepsTrackingForReconnect(t *testing.T) {
	conn := restartConn([]wire.SessionSummary{{
		SessionID:      rigSessionID,
		LifecycleState: wire.SessionLifecycleRunning,
		LatestSeq:      3,
	}}, nil)
	rt, _, _ := newRestartRuntime(t, conn, 3)

	turns := rt.AutonomousTurns(rigSessionID)
	live, err := rt.CatchUpSessions(context.Background(), []int64{rigSessionID})
	require.NoError(t, err)
	assert.Equal(t, []int64{rigSessionID}, live)

	select {
	case _, ok := <-turns:
		t.Fatalf("还在跑的会话不能收摊(closed=%v):它接下来的推送就没有落点了", !ok)
	case <-time.After(50 * time.Millisecond):
	}
	assert.Equal(t, []int64{rigSessionID}, rt.liveSessionIDs())
	assert.True(t, rt.hasLiveRun())
}

// Given daemon 交回的会话清单里,一条会话的最新 seq 与本地游标相等(断连期间它什么
// 都没产生),另一条落下了一大段,而本地还认得第三条根本不在这台 daemon 上,
// When 跑补齐,Then 只有真正落下内容的那条才发起 attach + pull。
//
// 清单交回的 SessionSummary 在此之前被整个丢掉。它的用处正是这个:重启后本地可能有
// 几十条老会话记着同一台 daemon,逐条 attach + pull 是几十次白跑的往返,而清单一次
// 就把「谁落下了内容」问清楚了;不在清单里的那条更是连 attach 都不该发 —— 它要么
// 已被 daemon 的重启清扫掉,要么压根属于别的对端。
func TestCatchUpSessions_SessionListDecidesWhoNeedsPulling(t *testing.T) {
	const (
		idleQuiet int64 = 42 // 空闲且已追平:一次往返都不必花
		liveQuiet int64 = 43 // 已追平,但还在跑:必须接管,否则它接下来的通知没有推送目标
		behind    int64 = 44 // 落下了一整段
		unknown   int64 = 45 // 本地还认得,daemon 上已经没有了
	)
	conn := restartConn([]wire.SessionSummary{
		{SessionID: idleQuiet, LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 3},
		{SessionID: liveQuiet, LifecycleState: wire.SessionLifecycleRunning, LatestSeq: 3},
		{SessionID: behind, LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 9},
	}, nil)
	rt, _, _ := newRestartRuntime(t, conn, 3)

	live, err := rt.CatchUpSessions(context.Background(),
		[]int64{idleQuiet, liveQuiet, behind, unknown})
	require.NoError(t, err)
	assert.Equal(t, []int64{liveQuiet}, live,
		"交回的 live 只含 daemon 说还在跑的那条 —— 调用方据它判断本地哪些 running 行是重启遗孤")

	calls := conn.methodCalls(wire.MethodSessionAttach)
	attached := make([]int64, 0, len(calls))
	for _, c := range calls {
		attached = append(attached, c.Params.(wire.SessionAttachParams).SessionID)
	}
	assert.Equal(t, []int64{liveQuiet, behind}, attached,
		"空闲且追平的会话与不在清单里的会话都不该发 attach;还在跑的必须接管推送流")
}

// Given 对面是不认识补齐族 RPC 的老 daemon,When App 启动后发起补齐,
// Then 立刻判定不支持并交出哨兵,不再逐条 attach(R18)。
func TestCatchUpSessions_OldDaemon_ReportsUnsupported(t *testing.T) {
	conn := newFakeConn()
	conn.script(func(method string, _, _ any) error {
		if method == wire.MethodSessionList {
			return &jsonrpc.Error{Code: jsonrpc.ErrMethodNotFound.Code, Message: "Method not found"}
		}
		return nil
	})
	rt, _, _ := newRestartRuntime(t, conn, 3)

	_, err := rt.CatchUpSessions(context.Background(), []int64{rigSessionID})
	require.ErrorIs(t, err, ErrCatchUpUnsupported)
	assert.Empty(t, conn.methodCalls(wire.MethodSessionAttach),
		"老 daemon 上不该继续发补齐族的其余方法")
}

// Given 一段补齐正在写进合成轮,When 重放到一条 autonomousTurn.started(daemon 在
// 那之后自主起了新的一轮),Then 先把手上这一轮收尾再开新的。
//
// 这是 F2 留下的第二个缺口:handleAutonomousTurnStarted 无条件覆盖 a.cur。旧的那轮
// events 因此永远关不掉 —— 上层 driveAutonomousTurn 卡在 `for ev := range at.Events`
// 上不返回,它那条 assistant 消息永久停在 running,界面上就是一张永远转圈的卡片,
// 旁边又多出一张新卡片。App 重启后「回放一整段历史」变成常态,这一幕随之从边角变成
// 常见路径。
func TestAutonomousTurnStarted_ClosesTheOpenTurnFirst(t *testing.T) {
	conn := newFakeConn()
	rt, _, _ := newRestartRuntime(t, conn, 0)
	turns := rt.AutonomousTurns(rigSessionID)

	// 一条带 seq、却不属于本进程任何在飞轮次的事件 —— 它开出一轮合成轮。
	ev, err := json.Marshal(agentruntime.TextDelta{Text: "replayed"})
	require.NoError(t, err)
	conn.deliver(t, wire.NotifyEvent, wire.EventFrame{SessionID: rigSessionID, Event: ev, Seq: 1})
	first := takeTurn(t, turns)

	conn.deliver(t, wire.NotifyAutonomousTurnStarted, wire.AutonomousTurnStartedFrame{
		SessionID: rigSessionID, Trigger: "background_task", Seq: 2,
	})
	second := takeTurn(t, turns)

	assert.Equal(t, []string{"replayed"}, drainTexts(t, first.Events, 2*time.Second),
		"手上那一轮必须被收尾,否则它的消费方永远不返回")
	assert.Equal(t, "background_task", second.Trigger)
	assert.NotEqual(t, TriggerCatchUp, second.Trigger)
}

// Given daemon 那边游标之后的那截尾巴已经不在了(agentred 自己不再回收日志 —— 规格
// 2026-08-18 决策 8 —— 但库可能被从外部恢复或截断),When 客户端回来补齐,
// Then 按 daemon 交回的「现存最老 seq」把游标复位,现存的那段照常重放进转录。
//
// 不复位的后果不是「少几条」而是这条会话就此静默冻住:每一页的第一条都比 游标+1 大,
// 被 dispatchNotification 判成跳号丢弃并触发补洞拉取,补洞又原样拉回同一页 —— 游标
// 永远停在那个已经不存在的位置,此后连实时通知也全被当成跳号,没有错误、没有跳号,
// 会话就是再也不出字(与 8496c291 修的越界冻结同类)。
func TestCatchUpSessions_ReclaimedPrefix_ResetsCursorInsteadOfFreezing(t *testing.T) {
	// 日志曾经是 seq 1..11,留存回收掉了 10 以下的前缀:现存最老的一行是 10。
	journal := []wire.JournaledNotification{
		journaledEvent(10, "survivor"),
		journaledDone(11, "sonnet"),
	}
	conn := restartConn([]wire.SessionSummary{{
		SessionID:      rigSessionID,
		LifecycleState: wire.SessionLifecycleIdle,
		LatestSeq:      11,
	}}, journal)
	// 游标停在 7:那之后的 8、9 已经随留存窗口一起没了。
	rt, cursor, _ := newRestartRuntime(t, conn, 7)

	turns := rt.AutonomousTurns(rigSessionID)
	_, err := rt.CatchUpSessions(context.Background(), []int64{rigSessionID})
	require.NoError(t, err)

	at := takeTurn(t, turns)
	assert.Equal(t, []string{"survivor"}, drainTexts(t, at.Events, 2*time.Second),
		"回收掉的那截拿不回来了,但**现存**的那段必须照常进转录")

	saved, ok := cursor.lastSaved()
	require.True(t, ok)
	assert.Equal(t, int64(11), saved.Seq,
		"游标要推到日志末尾,否则下一条实时通知仍会被判成跳号,会话继续冻着")
}

// Given 留存回收恰好跑在 daemon 为这一页读下界与读行之间(下界还是回收前的那个,页里
// 却只剩回收之后存活的那截),When 客户端按下界复位后发现这一页一条也交付不出去,
// Then 它从复位后的游标再拉一页,把存活的那段取回来 —— 而不是带着 oldest-1 的游标收工、
// 等下一条实时帧再触发一次补洞。
//
// 终态会话上没有「下一条实时帧」:那一页不补回来,这段转录就要等到用户下次开新一轮才
// 露面(而开新一轮时它又会以「已结束轮次」的身份被分走)。
func TestCatchUpSessions_PageEntirelyBelowTheNewFloor_PullsAgain(t *testing.T) {
	// 日志此刻只剩 30、31:下界读到的却还是回收前的 10。
	journal := []wire.JournaledNotification{
		journaledEvent(30, "survivor"),
		journaledDone(31, "sonnet"),
	}
	// 重放里那条被判成跳号的行会另起一次补洞拉取,计数器因此有并发写者。
	var pulls atomic.Int64
	conn := newFakeConn()
	conn.script(func(method string, params, result any) error {
		switch method {
		case wire.MethodSessionList:
			*(result.(*wire.SessionListResult)) = wire.SessionListResult{Sessions: []wire.SessionSummary{{
				SessionID: rigSessionID, LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 31,
			}}}
		case wire.MethodSessionAttach:
			*(result.(*wire.SessionAttachResult)) = wire.SessionAttachResult{
				SessionID: rigSessionID, LifecycleState: wire.SessionLifecycleIdle, LatestSeq: 31,
			}
		case wire.MethodSessionPull:
			p := params.(wire.SessionPullParams)
			out := wire.SessionPullResult{Cursor: p.Cursor}
			// 第一页交出回收**之前**的下界(daemon 先读它,回收随后才跑)。
			out.OldestSeq = 30
			if pulls.Add(1) == 1 {
				out.OldestSeq = 10
			}
			for _, n := range journal {
				if n.Seq > p.Cursor {
					out.Notifications = append(out.Notifications, n)
					out.Cursor = n.Seq
				}
			}
			*(result.(*wire.SessionPullResult)) = out
		case wire.MethodSessionPendingWaiters:
			*(result.(*wire.SessionPendingWaitersResult)) = wire.SessionPendingWaitersResult{}
		}
		return nil
	})
	rt, cursor, _ := newRestartRuntime(t, conn, 7)

	turns := rt.AutonomousTurns(rigSessionID)
	_, err := rt.CatchUpSessions(context.Background(), []int64{rigSessionID})
	require.NoError(t, err)

	at := takeTurn(t, turns)
	assert.Equal(t, []string{"survivor"}, drainTexts(t, at.Events, 2*time.Second),
		"复位之后这一页一条也交付不出去,就得再拉一页把存活的那截取回来")
	assert.GreaterOrEqual(t, pulls.Load(), int64(2), "复位没能消费掉这一页时必须接着拉")

	saved, ok := cursor.lastSaved()
	require.True(t, ok)
	assert.Equal(t, int64(31), saved.Seq, "游标要推到日志末尾,否则下次还是从洞里开始")
}

// Given daemon 进程在桌面端离线期间重启过,把这条会话按 R10 标成了中断态,
// When App 重开后补齐,Then 中断前的历史仍然补回来,但不发 attach(已经没有推送流可
// 接管),补完把会话按**被打断**收尾。
//
// 规格对中断态的要求是「历史可读、不可续跑」:只标终态而不补历史,用户丢的正是
// daemon 挂掉之前那一段——恰恰是最需要看到的那段。终止理由用 ErrRunInterrupted 而不
// 是 ErrDaemonDisconnected:连接明明是通的,说「连不上了」是错的(R15 要求这两种情形
// 由文案区分,而文案的唯一依据就是这个哨兵)。
func TestCatchUpSessions_InterruptedSession_ReadsHistoryThenEndsAsInterrupted(t *testing.T) {
	conn := restartConn([]wire.SessionSummary{{
		SessionID:      rigSessionID,
		LifecycleState: wire.SessionLifecycleInterrupted,
		LatestSeq:      5,
	}}, []wire.JournaledNotification{
		journaledEvent(4, "before-the-crash"),
		journaledEvent(5, "and-then-nothing"),
	})
	rt, _, obs := newRestartRuntime(t, conn, 3)

	turns := rt.AutonomousTurns(rigSessionID)
	_, err := rt.CatchUpSessions(context.Background(), []int64{rigSessionID})
	require.NoError(t, err)

	assert.Empty(t, conn.methodCalls(wire.MethodSessionAttach),
		"中断态会话没有推送流可接管")
	assert.NotEmpty(t, conn.methodCalls(wire.MethodSessionPull), "中断前的历史仍要补回来")

	at := takeTurn(t, turns)
	assert.Equal(t, []string{"before-the-crash", "and-then-nothing"},
		drainTexts(t, at.Events, 2*time.Second))
	require.ErrorIs(t, at.Result.StopErr, ErrRunInterrupted,
		"「被打断」与「连不上了」必须分得开 —— 文案的唯一依据就是这个哨兵")

	states := obs.states(rigSessionID)
	require.NotEmpty(t, states)
	assert.Equal(t, ConnStateLost, states[len(states)-1].State)
}
