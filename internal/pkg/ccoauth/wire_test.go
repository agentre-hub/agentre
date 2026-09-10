package ccoauth_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/ccoauth"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Given 远端答了一次成功的配额查询;When 翻成领域结果;Then 百分比与重置时刻逐个还原。
func TestRateLimitsFromResponse_GivenOK_WhenConverted_ThenEveryBucketSurvives(t *testing.T) {
	t.Parallel()

	fiveHour := int64(1_700_000_000_000)
	sonnet := 12.5
	limits, err := ccoauth.RateLimitsFromResponse(&agentrewire.ClaudeCodeUsageResponse{
		Reason: "ok",
		Data: &agentrewire.ClaudeCodeRateLimits{
			FiveHourPercent: 42, WeeklyPercent: 7,
			FiveHourResetsAtMs: &fiveHour, SonnetWeeklyPercent: &sonnet,
		},
	})

	require.NoError(t, err)
	require.InDelta(t, 42.0, limits.FiveHourPercent, 0.001)
	require.InDelta(t, 7.0, limits.WeeklyPercent, 0.001)
	require.NotNil(t, limits.FiveHourResetsAt)
	require.Equal(t, time.UnixMilli(fiveHour), *limits.FiveHourResetsAt)
	require.NotNil(t, limits.SonnetWeeklyPercent)
	// 缺席的时刻是「这一档没有重置时刻」，不是 1970 年。
	require.Nil(t, limits.WeeklyResetsAt)
	require.Nil(t, limits.OpusWeeklyResetsAt)
}

// Given 一档配额的百分比是 0 且在 wire 上是显式给出的;When 翻译;Then 它必须仍然是
// 一个指向 0 的指针,而不是 nil。
//
// 这两者在界面上是两件事:nil 是「这一档不适用、不画」,0 是「这一档已经用满/未用,
// 照常画」。指针语义丢了,一个真实的 0% 就会从界面上整档消失。
func TestRateLimitsFromResponse_GivenAnExplicitZeroPercent_WhenConverted_ThenItStaysPresent(t *testing.T) {
	t.Parallel()

	zero := 0.0
	limits, err := ccoauth.RateLimitsFromResponse(&agentrewire.ClaudeCodeUsageResponse{
		Reason: "ok",
		Data:   &agentrewire.ClaudeCodeRateLimits{FiveHourPercent: 12, SonnetWeeklyPercent: &zero},
	})

	require.NoError(t, err)
	require.NotNil(t, limits.SonnetWeeklyPercent)
	require.InDelta(t, 0.0, *limits.SonnetWeeklyPercent, 0.001)
	// 没给的那一档仍然是 nil —— 缺席不能被补成 0。
	require.Nil(t, limits.OpusWeeklyPercent)
}

// Given 远端答的是一个失败原因;When 翻译;Then 对上本包的哨兵 —— 调用方按哨兵分支
// 决定是提示重新登录还是稍后重试,拼错一个字就会全部退化成「网络错误」。
func TestRateLimitsFromResponse_GivenAFailureReason_WhenConverted_ThenItMapsToTheSentinel(t *testing.T) {
	t.Parallel()

	for reason, want := range map[string]error{
		"no_credentials": ccoauth.ErrNoCredentials,
		"auth_expired":   ccoauth.ErrAuthExpired,
		"rate_limited":   ccoauth.ErrRateLimited,
		"":               ccoauth.ErrNetwork,
		"something_new":  ccoauth.ErrNetwork,
	} {
		t.Run(reason, func(t *testing.T) {
			_, err := ccoauth.RateLimitsFromResponse(&agentrewire.ClaudeCodeUsageResponse{Reason: reason})
			require.ErrorIs(t, err, want)
		})
	}
}
