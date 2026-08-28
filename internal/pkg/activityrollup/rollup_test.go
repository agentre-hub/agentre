package activityrollup_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
)

func ms(t *testing.T, layout, value, zone string) int64 {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	require.NoError(t, err)
	parsed, err := time.ParseInLocation(layout, value, loc)
	require.NoError(t, err)
	return parsed.UnixMilli()
}

func mustLoad(t *testing.T, zone string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	require.NoError(t, err)
	return loc
}

// TestAggregate_BucketsByTheRequestedZonesDayBoundary 覆盖「日界按调用方给的时区
// 切」。同一个 UTC 时刻在不同时区落在不同的一天,格子图画的是「我哪天干了活」,不是
// 「UTC 的哪一天」—— 按 UTC 切会把 UTC+8 用户每天的活动劈到两格上。
func TestAggregate_BucketsByTheRequestedZonesDayBoundary(t *testing.T) {
	// 上海时间 2026-08-28 07:30 = UTC 2026-08-27 23:30。
	at := ms(t, "2006-01-02 15:04", "2026-08-28 07:30", "Asia/Shanghai")
	item := activityrollup.Activity{CreatedAt: at, LastMessageAt: at, AgentSyncID: "a1"}

	shanghai := activityrollup.Aggregate([]activityrollup.Activity{item}, mustLoad(t, "Asia/Shanghai"), "")
	require.Len(t, shanghai, 1)
	assert.Equal(t, "2026-08-28", shanghai[0].Day)

	utc := activityrollup.Aggregate([]activityrollup.Activity{item}, time.UTC, "")
	require.Len(t, utc, 1)
	assert.Equal(t, "2026-08-27", utc[0].Day, "同一时刻在 UTC 属于前一天")
}

// TestAggregate_CountsPerDayAndDimensionCombo 覆盖分组口径:一条会话只落在
// (天 × Agent × 后端 × provider/model × 项目) 的**一个**组合里,所以按任意维度子集
// 求和都是对的 —— 这正是服务端能用同一张表同时画热力图和三张分布卡的前提。
func TestAggregate_CountsPerDayAndDimensionCombo(t *testing.T) {
	day := func(v string) int64 { return ms(t, "2006-01-02 15:04", v, "UTC") }
	at := func(v string) activityrollup.Activity {
		return activityrollup.Activity{CreatedAt: day(v), LastMessageAt: day(v)}
	}
	with := func(a activityrollup.Activity, agent, backend, project string) activityrollup.Activity {
		a.AgentSyncID, a.BackendType, a.ProjectSyncID = agent, backend, project
		return a
	}
	got := activityrollup.Aggregate([]activityrollup.Activity{
		with(at("2026-08-28 10:00"), "a1", "claudecode", "p1"),
		with(at("2026-08-28 11:00"), "a1", "claudecode", "p1"),
		with(at("2026-08-28 12:00"), "a1", "codex", "p1"),
		with(at("2026-08-27 09:00"), "a1", "claudecode", "p1"),
	}, time.UTC, "")

	assert.Equal(t, []activityrollup.Bucket{
		{Day: "2026-08-27", AgentSyncID: "a1", BackendType: "claudecode", ProjectSyncID: "p1", SessionCount: 1},
		{Day: "2026-08-28", AgentSyncID: "a1", BackendType: "claudecode", ProjectSyncID: "p1", SessionCount: 2},
		{Day: "2026-08-28", AgentSyncID: "a1", BackendType: "codex", ProjectSyncID: "p1", SessionCount: 1},
	}, got)
}

// TestAggregate_SinceDayIsInclusive 覆盖增量拉取:调用方给的下界是**闭区间** ——
// 当天的计数在一天之内还会变,把它排除在外会永远少最后一天。
func TestAggregate_SinceDayIsInclusive(t *testing.T) {
	day := func(v string) int64 { return ms(t, "2006-01-02 15:04", v, "UTC") }
	born := func(v string) activityrollup.Activity {
		return activityrollup.Activity{CreatedAt: day(v), LastMessageAt: day(v), AgentSyncID: "a1"}
	}
	got := activityrollup.Aggregate([]activityrollup.Activity{
		born("2026-08-26 10:00"),
		born("2026-08-27 10:00"),
		born("2026-08-28 10:00"),
	}, time.UTC, "2026-08-27")

	require.Len(t, got, 2)
	assert.Equal(t, "2026-08-27", got[0].Day, "下界那一天必须包含在内")
	assert.Equal(t, "2026-08-28", got[1].Day)
}

// TestAggregate_SkipsSessionsThatNeverRan 覆盖「一轮都没跑过的会话不计数」。
//
// 建了一条会话却一个字都没发,它不是「一条对话」。LastMessageAt 在这里只作**闸门**,
// 不再决定落在哪一天 —— 哪一天由 CreatedAt 说了算。
func TestAggregate_SkipsSessionsThatNeverRan(t *testing.T) {
	at := ms(t, "2006-01-02 15:04", "2026-08-28 10:00", "UTC")
	got := activityrollup.Aggregate([]activityrollup.Activity{
		{CreatedAt: at, LastMessageAt: 0, AgentSyncID: "a1"},
	}, time.UTC, "")
	assert.Empty(t, got)
}

// TestAggregate_SkipsSessionsWithoutACreationMoment 覆盖「没有建立时刻的会话不计数」。
// CreatedAt = 0 是「对端没记这条会话是什么时候建的」,不是 1970-01-01 —— 算进去会在
// 格子图最左端凭空长出一块假数据。
func TestAggregate_SkipsSessionsWithoutACreationMoment(t *testing.T) {
	at := ms(t, "2006-01-02 15:04", "2026-08-28 10:00", "UTC")
	got := activityrollup.Aggregate([]activityrollup.Activity{
		{CreatedAt: 0, LastMessageAt: at, AgentSyncID: "a1"},
	}, time.UTC, "")
	assert.Empty(t, got)
}

// TestAggregate_ACountedDayNeverMoves 是这个包最重要的一条守卫:一条会话落在**它建立
// 的那天**,此后不管被续多少轮都不会挪窝。
//
// 按「最后活动日」分桶的话,同一条会话每被续一次就换一天,而增量拉取的下界
// (since_day) 会越过它原来那天再也不回去 —— 于是一条用了三十天的对话在库里留下三十
// 行、每行 1,「累计 N 条」显示成 30;反过来,一次性回填只看得见它最后那天,同一台机器
// 同一份数据,回填与增量给出两份不同的历史。
//
// 建立时刻不会变,所以这两件事都不成立:两次聚合、隔了五天,给出的是同一个桶。
func TestAggregate_ACountedDayNeverMoves(t *testing.T) {
	moment := func(v string) int64 { return ms(t, "2006-01-02 15:04", v, "UTC") }
	born := moment("2026-08-01 09:00")

	fresh := activityrollup.Aggregate([]activityrollup.Activity{
		{CreatedAt: born, LastMessageAt: moment("2026-08-01 09:30"), AgentSyncID: "a1"},
	}, time.UTC, "")
	// 同一条会话,五天后又被续了一轮。
	continued := activityrollup.Aggregate([]activityrollup.Activity{
		{CreatedAt: born, LastMessageAt: moment("2026-08-06 21:00"), AgentSyncID: "a1"},
	}, time.UTC, "")

	assert.Equal(t, fresh, continued, "续了一轮不该让这条会话换一天")
	require.Len(t, continued, 1)
	assert.Equal(t, "2026-08-01", continued[0].Day)
	assert.Equal(t, int32(1), continued[0].SessionCount)
}

// TestAggregate_EmptyDimensionsSurviveAsEmpty 覆盖「空维度是有含义的值」:
// provider/model 皆空 = 跟随 Agent 绑定,project 空 = 未归属项目。它们必须原样成桶,
// 不能被丢掉、也不能被补成占位值。
func TestAggregate_EmptyDimensionsSurviveAsEmpty(t *testing.T) {
	at := ms(t, "2006-01-02 15:04", "2026-08-28 10:00", "UTC")
	got := activityrollup.Aggregate([]activityrollup.Activity{
		{CreatedAt: at, LastMessageAt: at, AgentSyncID: "a1", BackendType: "claudecode"},
	}, time.UTC, "")

	require.Len(t, got, 1)
	assert.Empty(t, got[0].ProviderKey)
	assert.Empty(t, got[0].ModelKey)
	assert.Empty(t, got[0].ProjectSyncID)
	assert.Equal(t, int32(1), got[0].SessionCount)
}
