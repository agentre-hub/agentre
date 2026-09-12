package ccoauth

import (
	"time"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// RateLimitsFromResponse 把 agentred 交回的一次配额查询翻成本包的领域结果。
//
// 它住在这里而不是 Wails 绑定层:从前这段(reason 分支 + 时间戳还原)整个写在
// internal/app/cc_usage.go 里,而 App 里的代码 go test 够不着 —— 一条把
// "auth_expired" 抄成 "auth-expired" 的改动没有任何东西会红。翻译的两头都在本包
// 与 wire 包里,放这儿谁也没多依赖一层。
//
// reason 认不出时按网络错误处理:调用方据此重试,而不是把一个未知状态当成"没配额"
// 渲染出去。
func RateLimitsFromResponse(response *agentrewire.ClaudeCodeUsageResponse) (*RateLimits, error) {
	switch response.GetReason() {
	case "ok":
		return rateLimitsFromProto(response.GetData()), nil
	case "no_credentials":
		return nil, ErrNoCredentials
	case "auth_expired":
		return nil, ErrAuthExpired
	case "rate_limited":
		return nil, ErrRateLimited
	default:
		return nil, ErrNetwork
	}
}

// rateLimitsFromProto 还原四组「百分比 + 重置时刻」。
//
// 时刻在 wire 上是可选的 unix 毫秒:字段缺失表示「这一档没有重置时刻」,而不是
// 1970 年 —— 所以逐个判 nil,不能拿零值直接 UnixMilli。
func rateLimitsFromProto(value *agentrewire.ClaudeCodeRateLimits) *RateLimits {
	if value == nil {
		return nil
	}
	result := &RateLimits{
		FiveHourPercent: value.GetFiveHourPercent(), WeeklyPercent: value.GetWeeklyPercent(),
		SonnetWeeklyPercent: value.SonnetWeeklyPercent, OpusWeeklyPercent: value.OpusWeeklyPercent,
	}
	for _, field := range []struct {
		ms  *int64
		out **time.Time
	}{
		{value.FiveHourResetsAtMs, &result.FiveHourResetsAt},
		{value.WeeklyResetsAtMs, &result.WeeklyResetsAt},
		{value.SonnetWeeklyResetsAtMs, &result.SonnetWeeklyResetsAt},
		{value.OpusWeeklyResetsAtMs, &result.OpusWeeklyResetsAt},
	} {
		if field.ms != nil {
			reset := time.UnixMilli(*field.ms)
			*field.out = &reset
		}
	}
	return result
}
