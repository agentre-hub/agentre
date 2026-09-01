package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

func journalPayload(t *testing.T, method string, params any) []byte {
	t.Helper()
	notification, err := protowire.WireNotificationToProto(method, params)
	require.NoError(t, err)
	payload, err := protowire.EncodeNotification(notification)
	require.NoError(t, err)
	return payload
}

func textDeltaFrame(t *testing.T, conversationID string, text string) wire.EventFrame {
	t.Helper()
	event := agentruntime.TextDelta{Text: text}
	return wire.EventFrame{ConversationID: conversationID, Event: event}
}

// setupCatchupTest 组装重连补齐这一族 handler:会话清单 / 增量拉取 / 待决策查询 /
// 显式接管。两个存储端口用 mockgen 注入,backend runtime 用测试替身。
func setupCatchupTest(t *testing.T, rt agentruntime.Runtime) (
	context.Context,
	*mock_handlers.MockSessionQueryPort,
	*mock_handlers.MockJournalReaderPort,
	*handlers.SessionCatchupHandlers,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sessions := mock_handlers.NewMockSessionQueryPort(ctrl)
	journal := mock_handlers.NewMockJournalReaderPort(ctrl)
	h := handlers.NewSessionCatchupHandlers(handlers.SessionCatchupDeps{
		Sessions: sessions,
		Journal:  journal,
		RuntimeFor: func(_ agent_backend_entity.BackendType) agentruntime.Runtime {
			return rt
		},
	})
	return context.Background(), sessions, journal, h
}

// ── 会话清单 ────────────────────────────────────────────────────────────────

// TestSessionCatchup_List_ReportsLatestSeqFromTheJournal 覆盖清单的「最新 seq」:
// 它取自通知日志的 MAX(seq),不取 daemon_sessions.latest_seq —— 那一列没有写入方,
// 读它会让每条会话都报 0,客户端每次重连都重拉整段日志。一条通知都还没发出的会话报 0。
func TestSessionCatchup_List_ReportsLatestSeqFromTheJournal(t *testing.T) {
	ctx, sessions, journal, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().List(gomock.Any(), "", "").Return([]handlers.SessionRecord{
		{PeerSessionID: convID(1), AgentID: 7, Cwd: "/work", BackendType: "claudecode", LifecycleState: wire.SessionLifecycleRunning},
		{PeerSessionID: convID(2), AgentID: 8, Cwd: "/other", BackendType: "codex", LifecycleState: wire.SessionLifecycleIdle},
	}, nil)
	journal.EXPECT().LatestSeqByPeer(gomock.Any(), "").Return(map[string]int64{convID(1): 42}, nil)

	got, err := h.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 2)

	assert.Equal(t, convID(1), got.Sessions[0].ConversationID)
	assert.Equal(t, int64(7), got.Sessions[0].AgentID)
	assert.Equal(t, "/work", got.Sessions[0].Cwd)
	assert.Equal(t, "claudecode", got.Sessions[0].BackendType)
	assert.Equal(t, wire.SessionLifecycleRunning, got.Sessions[0].LifecycleState)
	assert.Equal(t, int64(42), got.Sessions[0].LatestSeq)

	assert.Equal(t, convID(2), got.Sessions[1].ConversationID)
	assert.Zero(t, got.Sessions[1].LatestSeq, "还没发过通知的会话报 0")
}

// TestSessionCatchup_List_ReturnsTitleAgentSyncIDAndProviderSessionID 覆盖 R7 + 决策 8
// 的回传:daemon 落库的标题、Agent 同步标识与 provider_session_id 随 session.list 回到
// 客户端;老会话缺这些字段时如实留空(空串,不猜、不填占位名)。
func TestSessionCatchup_List_ReturnsTitleAgentSyncIDAndProviderSessionID(t *testing.T) {
	ctx, sessions, journal, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().List(gomock.Any(), "", "").Return([]handlers.SessionRecord{
		{PeerSessionID: convID(1), AgentID: 7, Cwd: "/work", BackendType: "claudecode",
			LifecycleState:    wire.SessionLifecycleRunning,
			Title:             "fix the bug",
			AgentSyncID:       "01HXsync000000000000000000",
			ProviderSessionID: "claude-abc123",
		},
		// 老会话:这三个字段从没落过库,如实留空。
		{PeerSessionID: convID(2), AgentID: 8, Cwd: "/other", BackendType: "codex", LifecycleState: wire.SessionLifecycleIdle},
	}, nil)
	journal.EXPECT().LatestSeqByPeer(gomock.Any(), "").Return(nil, nil)

	got, err := h.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 2)

	assert.Equal(t, "fix the bug", got.Sessions[0].Title)
	assert.Equal(t, "01HXsync000000000000000000", got.Sessions[0].AgentSyncID)
	assert.Equal(t, "claude-abc123", got.Sessions[0].ProviderSessionID)

	assert.Empty(t, got.Sessions[1].Title, "老会话缺标题时 session.list 如实留空")
	assert.Empty(t, got.Sessions[1].AgentSyncID, "老会话缺 Agent 同步标识时 session.list 如实留空")
	assert.Empty(t, got.Sessions[1].ProviderSessionID, "老会话缺 provider_session_id 时 session.list 如实留空")
}

// TestSessionCatchup_List_ReportsLastActivity 覆盖 R5 的「最后活动时间」:会话行的
// updated_at 随 session.list 回传,客户端据此在列表行上显示最后活动时间。没有它,
// R5 要求的四项里就少一项,而 R13 的「机器 · 时间」只能退回到别的时间来源。
func TestSessionCatchup_List_ReportsLastActivity(t *testing.T) {
	ctx, sessions, journal, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().List(gomock.Any(), "", "").Return([]handlers.SessionRecord{
		{PeerSessionID: convID(1), BackendType: "claudecode", LifecycleState: wire.SessionLifecycleRunning,
			LastMessageAt: 1754800000000},
		// 老会话:daemon 没记过活动时间,如实留 0,由客户端表达为「未知」而不是猜一个。
		{PeerSessionID: convID(2), BackendType: "codex", LifecycleState: wire.SessionLifecycleIdle},
	}, nil)
	journal.EXPECT().LatestSeqByPeer(gomock.Any(), "").Return(nil, nil)

	got, err := h.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 2)
	assert.Equal(t, int64(1754800000000), got.Sessions[0].LastMessageAt)
	assert.Zero(t, got.Sessions[1].LastMessageAt, "没有活动时间的会话如实留 0")
}

// TestSessionCatchup_List_WaitingForInputIsOverlaidLive 覆盖 R11:「是否正在等待
// 输入」由实时 waiter 状态叠加计算,永不落库 —— 落库的标志会活过 daemon 重启,变成
// 一个没人能回答的问题。同一份会话行,backend 有阻塞 waiter 时为真,没有时为假。
func TestSessionCatchup_List_WaitingForInputIsOverlaidLive(t *testing.T) {
	blocked := &fullRT{pendingWaiters: agentruntime.WaiterSnapshot{
		ToolPermissions: []agentruntime.PendingToolPermission{{RequestID: "p-1", ToolName: "Bash"}},
	}}
	rows := []handlers.SessionRecord{
		{PeerSessionID: convID(1), BackendType: "claudecode", LifecycleState: wire.SessionLifecycleRunning},
	}

	ctx, sessions, journal, h := setupCatchupTest(t, blocked)
	sessions.EXPECT().List(gomock.Any(), "", "").Return(rows, nil)
	journal.EXPECT().LatestSeqByPeer(gomock.Any(), "").Return(nil, nil)
	got, err := h.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.True(t, got.Sessions[0].WaitingForInput, "backend 有阻塞 waiter 时清单必须报正在等待输入")

	// 同一份会话行,backend 此刻没有任何 waiter → 必须报 false。
	ctx2, sessions2, journal2, h2 := setupCatchupTest(t, &fullRT{})
	sessions2.EXPECT().List(gomock.Any(), "", "").Return(rows, nil)
	journal2.EXPECT().LatestSeqByPeer(gomock.Any(), "").Return(nil, nil)
	got2, err := h2.List(ctx2, "")
	require.NoError(t, err)
	require.Len(t, got2.Sessions, 1)
	assert.False(t, got2.Sessions[0].WaitingForInput)
}

// TestSessionCatchup_List_BackendWithoutApprovalProtocol_NotWaiting 覆盖 R7 的
// 回落:未实现审批协议的 backend(WaiterLister 断言失败)在清单里报「不在等待」,
// 而不是让整个清单查询报错 —— 一个 backend 不支持枚举不该让客户端连清单都拿不到。
func TestSessionCatchup_List_BackendWithoutApprovalProtocol_NotWaiting(t *testing.T) {
	ctx, sessions, journal, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().List(gomock.Any(), "", "").Return([]handlers.SessionRecord{
		{PeerSessionID: convID(1), BackendType: "unknown", LifecycleState: wire.SessionLifecycleRunning},
	}, nil)
	journal.EXPECT().LatestSeqByPeer(gomock.Any(), "").Return(nil, nil)

	got, err := h.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.False(t, got.Sessions[0].WaitingForInput)
}

// TestSessionCatchup_List_SkipsRowsWithAnUnparseableSessionID 覆盖坏行:会话 id 是
// 客户端的本地自增主键,解不出数字说明那一行不是本协议写的(手改过的库 / 未来格式)。
// 跳过它而不是让整份清单失败 —— 一条坏行不该让客户端连自己其余的会话都看不到。
func TestSessionCatchup_List_SkipsRowsWithAnUnparseableSessionID(t *testing.T) {
	ctx, sessions, journal, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().List(gomock.Any(), "", "").Return([]handlers.SessionRecord{
		{PeerSessionID: "not-a-number", LifecycleState: wire.SessionLifecycleIdle},
		{PeerSessionID: convID(2), LifecycleState: wire.SessionLifecycleIdle},
	}, nil)
	journal.EXPECT().LatestSeqByPeer(gomock.Any(), "").Return(nil, nil)

	got, err := h.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.Equal(t, convID(2), got.Sessions[0].ConversationID)
}

// TestSessionCatchup_List_StoreFailurePropagates 覆盖失败路径:会话表读不出来时
// 必须报错,不能返回一份空清单 —— 空清单与「这台 daemon 上没有你的会话」无法区分,
// 客户端会据此把还活着的会话当成已消失。
func TestSessionCatchup_List_StoreFailurePropagates(t *testing.T) {
	ctx, sessions, _, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().List(gomock.Any(), "", "").Return(nil, errors.New("disk I/O error"))

	_, err := h.List(ctx, "")
	require.Error(t, err)
}

// ── 增量拉取 ────────────────────────────────────────────────────────────────

// TestSessionCatchup_Pull_ReturnsPageAndAdvancesCursor 覆盖拉取的主路径:按游标
// 取回该会话其后的通知(seq 升序)、把新游标推到本页最后一条、并原样转达「是否还有
// 更多」。落库的 payload 不含 seq(seq 是日志行自己的列),所以帧上的 seq 与行上的
// 分开返回,由客户端在补齐时盖上去。
func TestSessionCatchup_Pull_ReturnsPageAndAdvancesCursor(t *testing.T) {
	ctx, _, journal, h := setupCatchupTest(t, bareRT{})
	journal.EXPECT().ListSince(gomock.Any(), "", convID(5), int64(0), wire.DefaultSessionPullLimit).
		Return([]handlers.JournalRow{
			{Seq: 1, Payload: journalPayload(t, wire.NotifyEvent, textDeltaFrame(t, convID(5), "x"))},
			{Seq: 2, Payload: journalPayload(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{ConversationID: convID(5)})},
		}, true, nil)
	journal.EXPECT().OldestSeq(gomock.Any(), "", convID(5)).Return(int64(1), nil)

	got, err := h.Pull(ctx, wire.SessionPullParams{ConversationID: convID(5), Cursor: 0})
	require.NoError(t, err)
	require.Len(t, got.Notifications, 2)
	assert.Equal(t, int64(1), got.Notifications[0].Seq)
	assert.Equal(t, wire.NotifyEvent, got.Notifications[0].Method)
	// Params 是帧本身,不再是一段待解析的字节。
	frame, ok := got.Notifications[0].Params.(*wire.EventFrame)
	require.True(t, ok, "got %T", got.Notifications[0].Params)
	assert.Equal(t, convID(5), frame.ConversationID)
	assert.Equal(t, int64(2), got.Cursor, "新游标 = 本页最后一条的 seq")
	assert.True(t, got.HasMore)
}

// TestSessionCatchup_Pull_CarriesTheJournalRowsCreatetime 钉住转录时间戳的**唯一**
// 可信来源:日志行落库时记下的那一刻(notification_repo.Append 就地盖的
// time.Now().UnixMilli()),也就是这一帧真正发生的时刻。
//
// 补齐这一跳不带它,下游就只剩「收到的时刻」可用,而补齐本身是成批的:一条离线两天的
// 对话补回来时,几百帧会被盖上同一个瞬间,浏览器控制台里整段转录因此显示成同一分钟。
// 时刻只能由原点报,中途每一跳都只是原样转交。
func TestSessionCatchup_Pull_CarriesTheJournalRowsCreatetime(t *testing.T) {
	ctx, _, journal, h := setupCatchupTest(t, bareRT{})
	journal.EXPECT().ListSince(gomock.Any(), "", convID(5), int64(0), wire.DefaultSessionPullLimit).
		Return([]handlers.JournalRow{
			{Seq: 1, Createtime: 1700000000111, Payload: journalPayload(t, wire.NotifyEvent, textDeltaFrame(t, convID(5), "x"))},
			{Seq: 2, Createtime: 1700000009222, Payload: journalPayload(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{ConversationID: convID(5)})},
		}, false, nil)
	journal.EXPECT().OldestSeq(gomock.Any(), "", convID(5)).Return(int64(1), nil)

	got, err := h.Pull(ctx, wire.SessionPullParams{ConversationID: convID(5), Cursor: 0})
	require.NoError(t, err)
	require.Len(t, got.Notifications, 2)
	assert.Equal(t, int64(1700000000111), got.Notifications[0].Createtime)
	assert.Equal(t, int64(1700000009222), got.Notifications[1].Createtime)
}

// TestSessionCatchup_Pull_ReportsTheSurvivingFloor 覆盖老前缀不在了的那一半:一页补齐
// 除了内容,还要交出这条会话此刻**现存最老**的 seq。
//
// agentred 自己已经不回收日志(规格 2026-08-18 决策 8),但库可能被从外部恢复或截断,
// 游标正落在消失了的那一段里。不报下界,它拉到的每一页第一条都比 游标+1 大,只能当成
// 跳号丢弃、再拉一次同一页 —— 游标永远推不动,此后连实时通知也全被判成跳号,会话没有
// 错误、没有跳号地冻住。报了它,客户端就知道那截尾巴是真的没有了,复位游标接着补。
func TestSessionCatchup_Pull_ReportsTheSurvivingFloor(t *testing.T) {
	ctx, _, journal, h := setupCatchupTest(t, bareRT{})
	journal.EXPECT().ListSince(gomock.Any(), "", convID(5), int64(7), wire.DefaultSessionPullLimit).
		Return([]handlers.JournalRow{
			{Seq: 10, Payload: journalPayload(t, wire.NotifyEvent, textDeltaFrame(t, convID(5), "x"))},
		}, false, nil)
	journal.EXPECT().OldestSeq(gomock.Any(), "", convID(5)).Return(int64(10), nil)

	got, err := h.Pull(ctx, wire.SessionPullParams{ConversationID: convID(5), Cursor: 7})
	require.NoError(t, err)
	assert.Equal(t, int64(10), got.OldestSeq,
		"游标之后的 8、9 已被回收,客户端只有拿到这个下界才不会一直等它们")
}

// TestSessionCatchup_Pull_FloorNeverExceedsTheRowsInTheSamePage 钉死两次读之间老前缀
// 恰好消失时的那一半:交出去的下界不得高于**同一页里**的行。
//
// 客户端拿 oldestSeq 复位游标,而复位跑在这一页重放**之前**(否则第一条当场被判成跳号)。
// 下界若是老前缀消失之后的那个高水位,而页里还留着更低的行,这些已经拿到手的行会被当成重复
// 全部丢掉 —— 一整页转录凭空消失,而它们本来是读得到的。所以下界要先读:先读的下界只会
// 偏小,偏小最多让客户端少复位一次,不会丢内容。
func TestSessionCatchup_Pull_FloorNeverExceedsTheRowsInTheSamePage(t *testing.T) {
	ctx, _, journal, h := setupCatchupTest(t, bareRT{})
	// 回收恰好在这两次读之间跑:读页之后,现存最老的一行已经涨到 20。
	swept := false
	journal.EXPECT().ListSince(gomock.Any(), "", convID(5), int64(7), wire.DefaultSessionPullLimit).
		DoAndReturn(func(context.Context, string, string, int64, int) ([]handlers.JournalRow, bool, error) {
			swept = true
			return []handlers.JournalRow{
				{Seq: 10, Payload: journalPayload(t, wire.NotifyEvent, textDeltaFrame(t, convID(5), "x"))},
				{Seq: 20, Payload: journalPayload(t, wire.NotifyEvent, textDeltaFrame(t, convID(5), "y"))},
			}, false, nil
		})
	journal.EXPECT().OldestSeq(gomock.Any(), "", convID(5)).
		DoAndReturn(func(context.Context, string, string) (int64, error) {
			if swept {
				return 20, nil
			}
			return 10, nil
		})

	got, err := h.Pull(ctx, wire.SessionPullParams{ConversationID: convID(5), Cursor: 7})
	require.NoError(t, err)
	require.Len(t, got.Notifications, 2)
	assert.LessOrEqual(t, got.OldestSeq, got.Notifications[0].Seq,
		"下界高过同一页里的行,客户端会把已经拿到手的那截当重复丢掉")
}

// TestSessionCatchup_Pull_CursorPastNewestSeq_EmptyPageKeepsCursor 覆盖边界:
// 起始游标大于最新 seq 时返回空页,游标**保持不变**(不能回退到 0),hasMore 为假。
// 游标回退会让客户端把整段日志重放一遍,转录流里出现重复消息。
func TestSessionCatchup_Pull_CursorPastNewestSeq_EmptyPageKeepsCursor(t *testing.T) {
	ctx, _, journal, h := setupCatchupTest(t, bareRT{})
	journal.EXPECT().ListSince(gomock.Any(), "", convID(5), int64(999), wire.DefaultSessionPullLimit).
		Return(nil, false, nil)
	journal.EXPECT().OldestSeq(gomock.Any(), "", convID(5)).Return(int64(1), nil)

	got, err := h.Pull(ctx, wire.SessionPullParams{ConversationID: convID(5), Cursor: 999})
	require.NoError(t, err)
	assert.Empty(t, got.Notifications)
	assert.Equal(t, int64(999), got.Cursor)
	assert.False(t, got.HasMore)
}

// TestSessionCatchup_Pull_LimitIsClampedToTheDaemonCap 覆盖单次返回条数上限
// (规格「增量拉取」明写 daemon 对单次返回条数设上限):客户端报的 limit 超过上限时
// 按上限截断,报 0 / 负数时用默认值。没有上限时一次几万条的日志会把一帧 WS 撑爆。
func TestSessionCatchup_Pull_LimitIsClampedToTheDaemonCap(t *testing.T) {
	t.Run("超过上限按上限", func(t *testing.T) {
		ctx, _, journal, h := setupCatchupTest(t, bareRT{})
		journal.EXPECT().ListSince(gomock.Any(), "", convID(5), int64(0), wire.MaxSessionPullLimit).
			Return(nil, false, nil)
		journal.EXPECT().OldestSeq(gomock.Any(), "", convID(5)).Return(int64(0), nil)
		_, err := h.Pull(ctx, wire.SessionPullParams{ConversationID: convID(5), Limit: wire.MaxSessionPullLimit * 10})
		require.NoError(t, err)
	})
	t.Run("未指定用默认值", func(t *testing.T) {
		ctx, _, journal, h := setupCatchupTest(t, bareRT{})
		journal.EXPECT().ListSince(gomock.Any(), "", convID(5), int64(0), wire.DefaultSessionPullLimit).
			Return(nil, false, nil)
		journal.EXPECT().OldestSeq(gomock.Any(), "", convID(5)).Return(int64(0), nil)
		_, err := h.Pull(ctx, wire.SessionPullParams{ConversationID: convID(5), Limit: 0})
		require.NoError(t, err)
	})
	t.Run("低于上限的正数原样使用", func(t *testing.T) {
		ctx, _, journal, h := setupCatchupTest(t, bareRT{})
		journal.EXPECT().ListSince(gomock.Any(), "", convID(5), int64(0), 3).Return(nil, false, nil)
		journal.EXPECT().OldestSeq(gomock.Any(), "", convID(5)).Return(int64(0), nil)
		_, err := h.Pull(ctx, wire.SessionPullParams{ConversationID: convID(5), Limit: 3})
		require.NoError(t, err)
	})
}

// ── 待决策查询(R7)────────────────────────────────────────────────────────

// TestSessionCatchup_PendingWaiters_ResolvesBackendFromThePersistedRow 覆盖 R7 的
// 重连场景:待决策查询按**库里的会话行**解 backend,而不是按某条连接的内存会话表 ——
// 重连后的新连接内存里一条会话都没有,按内存解会永远返回空快照,而 R9 不给 waiter
// 设过期,断连期间产生的审批会永久挂死。
func TestSessionCatchup_PendingWaiters_ResolvesBackendFromThePersistedRow(t *testing.T) {
	want := agentruntime.WaiterSnapshot{
		ToolPermissions: []agentruntime.PendingToolPermission{
			{RequestID: "p-1", ToolName: "Bash", Input: json.RawMessage(`{"command":"ls"}`)},
		},
		AskUserQuestions: []agentruntime.PendingAskUserQuestion{
			{RequestID: "a-1", Questions: []agentruntime.AskQuestion{{Question: "continue?"}}},
		},
	}
	ctx, sessions, _, h := setupCatchupTest(t, &fullRT{pendingWaiters: want})
	sessions.EXPECT().Find(gomock.Any(), "", convID(5)).Return(&handlers.SessionRecord{
		PeerSessionID: convID(5), BackendType: "claudecode", LifecycleState: wire.SessionLifecycleRunning,
	}, nil)

	got, err := h.PendingWaiters(ctx, wire.SessionPendingWaitersParams{ConversationID: convID(5)})
	require.NoError(t, err)
	assert.Equal(t, want.ToolPermissions, got.ToolPermissions)
	assert.Equal(t, want.AskUserQuestions, got.AskUserQuestions)
}

// TestSessionCatchup_PendingWaiters_UnknownSession_EmptyNoError 覆盖 R16 的读侧
// 边界:会话不属于调用方(或根本不存在)时返回空快照而非报错,也绝不去问 backend ——
// 报错会让客户端在正常的「这条会话不在这台 daemon 上」时误判为故障。
func TestSessionCatchup_PendingWaiters_UnknownSession_EmptyNoError(t *testing.T) {
	blocked := &fullRT{pendingWaiters: agentruntime.WaiterSnapshot{
		ToolPermissions: []agentruntime.PendingToolPermission{{RequestID: "p-1"}},
	}}
	ctx, sessions, _, h := setupCatchupTest(t, blocked)
	sessions.EXPECT().Find(gomock.Any(), "", convID(999)).Return(nil, nil)

	got, err := h.PendingWaiters(ctx, wire.SessionPendingWaitersParams{ConversationID: convID(999)})
	require.NoError(t, err)
	assert.Empty(t, got.ToolPermissions, "别人的 / 不存在的会话不得泄漏 waiter")
	assert.Empty(t, got.AskUserQuestions)
}

// TestSessionCatchup_PendingWaiters_InterruptedSession_NeverConsultsTheBackend
// 覆盖 R10 与 R16 交叉的那条边:中断态会话的那一轮子进程随上一个 daemon 进程消亡了,
// 所以它**不可能**有活的 waiter —— 此刻 backend 内存里挂在同一个原始会话 id 下的
// waiter 必然属于**别的对端**那条正在跑的同号会话(会话 id 是各客户端本地自增的,
// 必然重号,而 backend 的 waiter 只按会话 id 定位、不带对端)。把它交出去等于把别人
// 的审批载荷(工具名 + 原样 input)泄漏给一个只要配过对就能问的设备,而且对方还能照着
// requestID 替人回答。
//
// 清单侧的 waitingForInput 已经这么做了(中断态直接报 false);本方法当时漏了同一条
// 守卫,于是同一个 handler 家族对「这条中断态会话在等输入吗」给出两个答案。
func TestSessionCatchup_PendingWaiters_InterruptedSession_NeverConsultsTheBackend(t *testing.T) {
	blocked := &fullRT{pendingWaiters: agentruntime.WaiterSnapshot{
		ToolPermissions: []agentruntime.PendingToolPermission{
			{RequestID: "p-1", ToolName: "Bash", Input: json.RawMessage(`{"command":"rm -rf /"}`)},
		},
		AskUserQuestions: []agentruntime.PendingAskUserQuestion{{RequestID: "a-1"}},
	}}
	ctx, sessions, _, h := setupCatchupTest(t, blocked)
	sessions.EXPECT().Find(gomock.Any(), "", convID(5)).Return(&handlers.SessionRecord{
		PeerSessionID: convID(5), BackendType: "claudecode", LifecycleState: wire.SessionLifecycleInterrupted,
	}, nil)

	got, err := h.PendingWaiters(ctx, wire.SessionPendingWaitersParams{ConversationID: convID(5)})
	require.NoError(t, err)
	assert.Equal(t, wire.SessionPendingWaitersResult{}, got,
		"中断态会话没有自己的活 waiter,查到的必然是别人的")
}

// TestSessionCatchup_PendingWaiters_BackendWithoutApprovalProtocol_EmptyNoError
// 覆盖 R7 的「未实现者返回空列表而非报错」。
func TestSessionCatchup_PendingWaiters_BackendWithoutApprovalProtocol_EmptyNoError(t *testing.T) {
	ctx, sessions, _, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().Find(gomock.Any(), "", convID(5)).Return(&handlers.SessionRecord{
		PeerSessionID: convID(5), BackendType: "unknown", LifecycleState: wire.SessionLifecycleRunning,
	}, nil)

	got, err := h.PendingWaiters(ctx, wire.SessionPendingWaitersParams{ConversationID: convID(5)})
	require.NoError(t, err)
	assert.Equal(t, wire.SessionPendingWaitersResult{}, got)
}

// ── 显式接管 ────────────────────────────────────────────────────────────────

// TestSessionCatchup_Attach_ReturnsBackendAndHighWaterMark 覆盖显式接管的返回:
// 重连的客户端说「这条会话此后由我消费」,daemon 交回它接着补齐需要的两样东西 ——
// 当前生命周期状态与此刻的最新 seq(高水位)。
func TestSessionCatchup_Attach_ReturnsBackendAndHighWaterMark(t *testing.T) {
	ctx, sessions, journal, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().Find(gomock.Any(), "", convID(5)).Return(&handlers.SessionRecord{
		PeerSessionID: convID(5), BackendType: "claudecode", LifecycleState: wire.SessionLifecycleRunning,
	}, nil)
	journal.EXPECT().LatestSeq(gomock.Any(), "", convID(5)).Return(int64(42), nil)

	got, err := h.Attach(ctx, wire.SessionAttachParams{ConversationID: convID(5)})
	require.NoError(t, err)
	assert.Equal(t, convID(5), got.ConversationID)
	assert.Equal(t, "claudecode", got.BackendType)
	assert.Equal(t, wire.SessionLifecycleRunning, got.LifecycleState)
	assert.Equal(t, int64(42), got.LatestSeq)
}

// TestSessionCatchup_Attach_UnknownSession_ErrSessionNotFound 覆盖 R16 的写侧边界:
// 接管一条不属于调用方的会话必须被拒 —— 接管会改变通知的推送目标,允许跨对端接管等于
// 把别人的会话事件流引到自己的连接上。
func TestSessionCatchup_Attach_UnknownSession_ErrSessionNotFound(t *testing.T) {
	ctx, sessions, _, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().Find(gomock.Any(), "", convID(999)).Return(nil, nil)

	_, err := h.Attach(ctx, wire.SessionAttachParams{ConversationID: convID(999)})
	require.ErrorIs(t, err, agentruntime.ErrSessionNotFound)
}

// TestSessionCatchup_Attach_InterruptedSession_CannotBeResumed 覆盖 R10 的
// 「不可续跑」:daemon 重启把会话标成中断后,它的历史仍可拉取,但接管必须被拒 ——
// 那条会话的子进程随上一个 daemon 进程消亡了,接管它等于把实时流指向一个永远不会
// 再产出任何东西的会话,客户端会无限期等下去。
func TestSessionCatchup_Attach_InterruptedSession_CannotBeResumed(t *testing.T) {
	ctx, sessions, _, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().Find(gomock.Any(), "", convID(5)).Return(&handlers.SessionRecord{
		PeerSessionID: convID(5), BackendType: "claudecode", LifecycleState: wire.SessionLifecycleInterrupted,
	}, nil)

	_, err := h.Attach(ctx, wire.SessionAttachParams{ConversationID: convID(5)})
	require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

// TestSessionCatchup_List_ReportsSessionModelTarget 覆盖「会话级模型目标随清单回传」:
// providerKey/modelKey 两格组合成 ModelTarget 契约的三态(两者皆空 = 跟随 Agent 绑定,
// provider 非空 + model 空 = 供应商默认,两者非空 = 固定模型),与桌面端 chat_sessions
// 的那两列逐字同义。
//
// 老会话这两格本来就是空的 —— 空在契约里**有含义**(跟随绑定)。
func TestSessionCatchup_List_ReportsSessionModelTarget(t *testing.T) {
	ctx, sessions, journal, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().List(gomock.Any(), "", "").Return([]handlers.SessionRecord{
		// 固定模型。
		{PeerSessionID: convID(1), BackendType: "claudecode", LifecycleState: wire.SessionLifecycleRunning,
			ProviderKey: "prov-anthropic", ModelKey: "sonnet-4-6"},
		// 供应商默认:钉了供应商,模型跟着它当前的默认走。
		{PeerSessionID: convID(2), BackendType: "claudecode", LifecycleState: wire.SessionLifecycleIdle,
			ProviderKey: "prov-anthropic"},
		// 跟随 Agent 绑定:两格都空,这是个**有含义**的值,不是「没答」。
		{PeerSessionID: convID(3), BackendType: "codex", LifecycleState: wire.SessionLifecycleIdle},
	}, nil)
	journal.EXPECT().LatestSeqByPeer(gomock.Any(), "").Return(nil, nil)

	got, err := h.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 3)

	assert.Equal(t, "prov-anthropic", got.Sessions[0].ProviderKey)
	assert.Equal(t, "sonnet-4-6", got.Sessions[0].ModelKey)

	assert.Equal(t, "prov-anthropic", got.Sessions[1].ProviderKey)
	assert.Empty(t, got.Sessions[1].ModelKey, "供应商默认:模型这一格就该是空的")

	assert.Empty(t, got.Sessions[2].ProviderKey, "跟随 Agent 绑定:两格都空")
	assert.Empty(t, got.Sessions[2].ModelKey)
}

// TestSessionCatchup_List_ReturnsProjectSyncID 覆盖「项目归属随清单回传」。
//
// 服务端此前只能按 (指纹, cwd) 反推 agentred 会话的项目;日活跃统计走的是一条不上
// 行任何路径的纯计数通道,反推那条路在那里用不了。项目因此在发起时就落了库,这里
// 守它确实回得去 —— 落了库却不回传,等于没落。
//
// 老会话没落过这一列,如实留空:空 = 发起方没报,不是「未知待推导」。
func TestSessionCatchup_List_ReturnsProjectSyncID(t *testing.T) {
	ctx, sessions, journal, h := setupCatchupTest(t, bareRT{})
	sessions.EXPECT().List(gomock.Any(), "", "").Return([]handlers.SessionRecord{
		{PeerSessionID: convID(1), AgentID: 7, Cwd: "/work", BackendType: "claudecode",
			LifecycleState: wire.SessionLifecycleRunning,
			ProjectSyncID:  "01HXproj00000000000000000",
		},
		{PeerSessionID: convID(2), AgentID: 8, Cwd: "/other", BackendType: "codex", LifecycleState: wire.SessionLifecycleIdle},
	}, nil)
	journal.EXPECT().LatestSeqByPeer(gomock.Any(), "").Return(nil, nil)

	got, err := h.List(ctx, "")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 2)

	assert.Equal(t, "01HXproj00000000000000000", got.Sessions[0].ProjectSyncID)
	assert.Empty(t, got.Sessions[1].ProjectSyncID, "老会话缺项目标识时如实留空")
}

// ── 清单的关键词收窄 ────────────────────────────────────────────────────────
//
// session.list 此前整份回传这台机器上的会话。对端(浏览器的机器轴、桌面端的机器组)
// 要的往往只是其中几条,整份拉回去再筛既费带宽,也把无关会话的标题送了出去。关键词
// 因此下推到存储:handler 只负责把它原样带过去,不在内存里过一遍。

func TestSessionCatchup_List_PushesKeywordDownToTheStore(t *testing.T) {
	ctx, sessions, journal, h := setupCatchupTest(t, bareRT{})
	// 对端限定仍在:关键词是额外收窄,不是换一条查询。
	sessions.EXPECT().List(gomock.Any(), "", "happy").Return([]handlers.SessionRecord{
		{PeerSessionID: convID(1), AgentID: 7, Title: "看看happy是怎么实现中继的", BackendType: "claudecode", LifecycleState: wire.SessionLifecycleIdle},
	}, nil)
	journal.EXPECT().LatestSeqByPeer(gomock.Any(), "").Return(map[string]int64{}, nil)

	got, err := h.List(ctx, "happy")
	require.NoError(t, err)
	require.Len(t, got.Sessions, 1)
	assert.Equal(t, convID(1), got.Sessions[0].ConversationID)
}
