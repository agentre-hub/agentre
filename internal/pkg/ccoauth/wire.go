package ccoauth

import (
	"time"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// RateLimitsFromResponse 把 agentred 交回的一次配额查询翻成本包的领域结果。
//
// 它住在这里而不是 Wails 绑定层:这段(reason 分支 + 时间戳还原)写在 internal/app
// 里的话 go test 够不着 —— 一条把 "auth_expired" 抄成 "auth-expired" 的改动没有任何
// 东西会红。翻译的两头都在本包与 wire 包里,放这儿谁也没多依赖一层。
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

// rateLimitsFromProto 还原两个主窗口与各档模型周配额的「百分比 + 重置时刻」;
// 远端报告 5 小时窗口缺席时,这一档留 nil。
//
// 时刻在 wire 上是可选的 unix 毫秒:字段缺失表示「这一档没有重置时刻」,而不是
// 1970 年 —— 所以逐个判 nil,不能拿零值直接 UnixMilli。
func rateLimitsFromProto(value *agentrewire.ClaudeCodeRateLimits) *RateLimits {
	if value == nil {
		return nil
	}
	result := &RateLimits{
		WeeklyPercent:  value.GetWeeklyPercent(),
		WeeklyResetsAt: timeFromMillis(value.WeeklyResetsAtMs),
	}
	// 缺席标记是后加的:老 daemon 从不设它,它们的 five_hour_percent 照旧当数值读。
	if !value.GetFiveHourAbsent() {
		result.FiveHourPercent = new(value.GetFiveHourPercent())
		result.FiveHourResetsAt = timeFromMillis(value.FiveHourResetsAtMs)
	}
	for _, limit := range value.GetModelWeekly() {
		result.ModelWeekly = append(result.ModelWeekly, ModelWeeklyLimit{
			Model: limit.GetModel(), Percent: limit.GetPercent(), ResetsAt: timeFromMillis(limit.ResetsAtMs),
		})
	}
	return result
}

// RateLimitsToProto 是 rateLimitsFromProto 的反方向:agentred 把自己探到的配额编码上
// wire。没有 5 小时窗口时设 five_hour_absent —— five_hour_percent 是隐式存在的
// double,单靠它分不出「缺席」和「用了 0%」。
func RateLimitsToProto(value *RateLimits) *agentrewire.ClaudeCodeRateLimits {
	if value == nil {
		return nil
	}
	result := &agentrewire.ClaudeCodeRateLimits{
		WeeklyPercent:    value.WeeklyPercent,
		WeeklyResetsAtMs: millisFromTime(value.WeeklyResetsAt),
	}
	if value.FiveHourPercent == nil {
		result.FiveHourAbsent = true
	} else {
		result.FiveHourPercent = *value.FiveHourPercent
		result.FiveHourResetsAtMs = millisFromTime(value.FiveHourResetsAt)
	}
	for _, limit := range value.ModelWeekly {
		result.ModelWeekly = append(result.ModelWeekly, &agentrewire.ClaudeCodeModelWeeklyLimit{
			Model: limit.Model, Percent: limit.Percent, ResetsAtMs: millisFromTime(limit.ResetsAt),
		})
	}
	return result
}

func millisFromTime(value *time.Time) *int64 {
	if value == nil {
		return nil
	}
	millis := value.UnixMilli()
	return &millis
}

func timeFromMillis(ms *int64) *time.Time {
	if ms == nil {
		return nil
	}
	t := time.UnixMilli(*ms)
	return &t
}
