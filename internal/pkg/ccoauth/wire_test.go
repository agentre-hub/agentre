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
	fable := int64(1_700_000_500_000)
	limits, err := ccoauth.RateLimitsFromResponse(&agentrewire.ClaudeCodeUsageResponse{
		Reason: "ok",
		Data: &agentrewire.ClaudeCodeRateLimits{
			FiveHourPercent: 42, WeeklyPercent: 7,
			FiveHourResetsAtMs: &fiveHour,
			ModelWeekly: []*agentrewire.ClaudeCodeModelWeeklyLimit{
				{Model: "Fable", Percent: 12.5, ResetsAtMs: &fable},
				{Model: "Opus", Percent: 3},
			},
		},
	})

	require.NoError(t, err)
	require.InDelta(t, 42.0, limits.FiveHourPercent, 0.001)
	require.InDelta(t, 7.0, limits.WeeklyPercent, 0.001)
	require.NotNil(t, limits.FiveHourResetsAt)
	require.Equal(t, time.UnixMilli(fiveHour), *limits.FiveHourResetsAt)
	require.Len(t, limits.ModelWeekly, 2)
	require.Equal(t, "Fable", limits.ModelWeekly[0].Model)
	require.InDelta(t, 12.5, limits.ModelWeekly[0].Percent, 0.001)
	require.NotNil(t, limits.ModelWeekly[0].ResetsAt)
	require.Equal(t, time.UnixMilli(fable), *limits.ModelWeekly[0].ResetsAt)
	// 缺席的时刻是「这一档没有重置时刻」，不是 1970 年。
	require.Nil(t, limits.WeeklyResetsAt)
	require.Nil(t, limits.ModelWeekly[1].ResetsAt)
}

// Given 一档模型周配额的百分比是 0;When 翻译;Then 这一档仍然在列表里。
//
// 列表里有没有这一档与百分比是两件事:缺席是「这一档不适用、不画」,0 是「这一档
// 未用,照常画」。按百分比过滤,一个真实的 0% 就会从界面上整档消失。
func TestRateLimitsFromResponse_GivenAZeroPercentModelLimit_WhenConverted_ThenItStaysPresent(t *testing.T) {
	t.Parallel()

	limits, err := ccoauth.RateLimitsFromResponse(&agentrewire.ClaudeCodeUsageResponse{
		Reason: "ok",
		Data: &agentrewire.ClaudeCodeRateLimits{FiveHourPercent: 12, ModelWeekly: []*agentrewire.ClaudeCodeModelWeeklyLimit{
			{Model: "Fable", Percent: 0},
		}},
	})

	require.NoError(t, err)
	require.Len(t, limits.ModelWeekly, 1)
	require.InDelta(t, 0.0, limits.ModelWeekly[0].Percent, 0.001)
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
