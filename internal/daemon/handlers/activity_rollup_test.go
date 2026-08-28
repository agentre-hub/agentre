package handlers_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
)

func setupActivityTest(t *testing.T) (
	context.Context, *mock_handlers.MockSessionQueryPort, *handlers.ActivityHandlers,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	sessions := mock_handlers.NewMockSessionQueryPort(ctrl)
	return context.Background(), sessions, handlers.NewActivityHandlers(handlers.ActivityDeps{Sessions: sessions})
}

func atUTC(t *testing.T, value string) int64 {
	t.Helper()
	parsed, err := time.ParseInLocation("2006-01-02 15:04", value, time.UTC)
	require.NoError(t, err)
	return parsed.UnixMilli()
}

// TestActivityRollup_CountsSessionsByDayAndDimensions 覆盖这条通道交出去的**全部**
// 内容:天 × 维度 × 一个计数。标题与 cwd 就在会话行上,但一个都不出现在回包里 ——
// 这不是实现细节,这就是那个开关向用户承诺的边界。
func TestActivityRollup_CountsSessionsByDayAndDimensions(t *testing.T) {
	ctx, sessions, h := setupActivityTest(t)
	sessions.EXPECT().List(gomock.Any(), "").Return([]handlers.SessionRecord{
		{PeerSessionID: "1", Title: "改一个 bug", Cwd: "/Users/me/secret",
			AgentSyncID: "a1", BackendType: "claudecode", ProjectSyncID: "p1",
			Createtime: atUTC(t, "2026-08-28 10:00"), LastMessageAt: atUTC(t, "2026-08-28 10:00")},
		{PeerSessionID: "2", Title: "另一件事", Cwd: "/Users/me/other",
			AgentSyncID: "a1", BackendType: "claudecode", ProjectSyncID: "p1",
			Createtime: atUTC(t, "2026-08-28 18:00"), LastMessageAt: atUTC(t, "2026-08-28 18:00")},
		{PeerSessionID: "3", AgentSyncID: "a2", BackendType: "codex",
			Createtime: atUTC(t, "2026-08-27 09:00"), LastMessageAt: atUTC(t, "2026-08-27 09:00")},
	}, nil)

	got, err := h.ActivityRollup(ctx, "", "UTC")
	require.NoError(t, err)
	assert.Equal(t, []activityrollup.Bucket{
		{Day: "2026-08-27", AgentSyncID: "a2", BackendType: "codex", SessionCount: 1},
		{Day: "2026-08-28", AgentSyncID: "a1", BackendType: "claudecode", ProjectSyncID: "p1", SessionCount: 2},
	}, got)
}

// TestActivityRollup_BucketsInTheRequestedZone 覆盖时区透传:日界按调用方(服务端)
// 的时区切,好让一个账号下分散在各地的机器落在同一套日界上。
func TestActivityRollup_BucketsInTheRequestedZone(t *testing.T) {
	ctx, sessions, h := setupActivityTest(t)
	// UTC 2026-08-27 23:30 = 上海 2026-08-28 07:30。
	sessions.EXPECT().List(gomock.Any(), "").Return([]handlers.SessionRecord{
		{PeerSessionID: "1", AgentSyncID: "a1", Createtime: atUTC(t, "2026-08-27 23:30"), LastMessageAt: atUTC(t, "2026-08-27 23:30")},
	}, nil)

	got, err := h.ActivityRollup(ctx, "", "Asia/Shanghai")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "2026-08-28", got[0].Day)
}

// TestActivityRollup_UnknownZoneFallsBackToUTC 覆盖降级:对端的 tzdata 未必认识调用方
// 报的时区名。此时按 UTC 切并如常作答 —— 整份统计因为一个时区名解不开就失败,是拿
// 用户的数据去赌一件无关的事。
func TestActivityRollup_UnknownZoneFallsBackToUTC(t *testing.T) {
	ctx, sessions, h := setupActivityTest(t)
	sessions.EXPECT().List(gomock.Any(), "").Return([]handlers.SessionRecord{
		{PeerSessionID: "1", AgentSyncID: "a1", Createtime: atUTC(t, "2026-08-27 23:30"), LastMessageAt: atUTC(t, "2026-08-27 23:30")},
	}, nil)

	got, err := h.ActivityRollup(ctx, "", "Mars/Olympus_Mons")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "2026-08-27", got[0].Day, "解不开的时区名落回 UTC,不报错")
}

// TestActivityRollup_SinceDayNarrowsTheAnswer 覆盖增量拉取:服务端已经有的那些天不必
// 再传一遍。下界是闭区间(当天的计数还会变)。
func TestActivityRollup_SinceDayNarrowsTheAnswer(t *testing.T) {
	ctx, sessions, h := setupActivityTest(t)
	sessions.EXPECT().List(gomock.Any(), "").Return([]handlers.SessionRecord{
		{PeerSessionID: "1", AgentSyncID: "a1", Createtime: atUTC(t, "2026-08-20 10:00"), LastMessageAt: atUTC(t, "2026-08-20 10:00")},
		{PeerSessionID: "2", AgentSyncID: "a1", Createtime: atUTC(t, "2026-08-28 10:00"), LastMessageAt: atUTC(t, "2026-08-28 10:00")},
	}, nil)

	got, err := h.ActivityRollup(ctx, "2026-08-28", "UTC")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "2026-08-28", got[0].Day)
}

// TestActivityRollup_TheDayComesFromWhenTheSessionWasCreated 把「哪一天」这件事钉死在
// 建立时刻上。
//
// 上面几条用例里 Createtime 与 LastMessageAt 相等,证不出这一点。这里让它们差五天:
// 会话建于 08-01、最后一轮在 08-06,答案必须是 08-01。
//
// 按最后活动日分桶的话,同一条会话每被续一轮就换一天,而增量拉取的下界会越过它原来
// 那天再也不回去 —— 一条用了三十天的对话会在服务端留下三十行、每行 1。
func TestActivityRollup_TheDayComesFromWhenTheSessionWasCreated(t *testing.T) {
	ctx, sessions, h := setupActivityTest(t)
	sessions.EXPECT().List(gomock.Any(), "").Return([]handlers.SessionRecord{
		{PeerSessionID: "1", AgentSyncID: "a1",
			Createtime: atUTC(t, "2026-08-01 09:00"), LastMessageAt: atUTC(t, "2026-08-06 21:00")},
	}, nil)

	got, err := h.ActivityRollup(ctx, "", "UTC")

	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "2026-08-01", got[0].Day)
}

// TestActivityRollup_SkipsSessionsWithoutACreationMoment 守「没有建立时刻就不计数」:
// 算成 1970-01-01 会在格子图最左端凭空长出一块假数据。
func TestActivityRollup_SkipsSessionsWithoutACreationMoment(t *testing.T) {
	ctx, sessions, h := setupActivityTest(t)
	sessions.EXPECT().List(gomock.Any(), "").Return([]handlers.SessionRecord{
		{PeerSessionID: "1", AgentSyncID: "a1",
			Createtime: 0, LastMessageAt: atUTC(t, "2026-08-06 21:00")},
	}, nil)

	got, err := h.ActivityRollup(ctx, "", "UTC")

	require.NoError(t, err)
	assert.Empty(t, got)
}
