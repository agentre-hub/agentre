package ccoauth

import (
	"encoding/json"
	"errors"
	"time"
)

// ErrNoUsageFields 表示响应里 five_hour 和 seven_day 两个窗口都缺 utilization 字段，
// 无法构造任何配额信息。
var ErrNoUsageFields = errors.New("ccoauth: response has neither five_hour nor seven_day utilization")

// rawWindow 对应响应里 { "utilization": N, "resets_at": "ISO8601" } 结构。
// 使用指针 + omitempty 区分"缺字段"和"字段为 0"。
type rawWindow struct {
	Utilization *float64 `json:"utilization,omitempty"`
	ResetsAt    string   `json:"resets_at,omitempty"`
}

type rawUsage struct {
	FiveHour       *rawWindow `json:"five_hour,omitempty"`
	SevenDay       *rawWindow `json:"seven_day,omitempty"`
	SevenDaySonnet *rawWindow `json:"seven_day_sonnet,omitempty"`
	SevenDayOpus   *rawWindow `json:"seven_day_opus,omitempty"`
}

// ParseUsageResponse 把 /api/oauth/usage 的 200 响应体解析成 RateLimits。
// 当 five_hour / seven_day 至少一个的 utilization 字段存在时视为有效。
func ParseUsageResponse(body []byte) (*RateLimits, error) {
	var raw rawUsage
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}

	fiveH := windowUtil(raw.FiveHour)
	sevenD := windowUtil(raw.SevenDay)
	if fiveH == nil && sevenD == nil {
		return nil, ErrNoUsageFields
	}

	out := &RateLimits{}
	if fiveH != nil {
		out.FiveHourPercent = clamp(*fiveH)
		out.FiveHourResetsAt = parseTime(raw.FiveHour.ResetsAt)
	}
	if sevenD != nil {
		out.WeeklyPercent = clamp(*sevenD)
		out.WeeklyResetsAt = parseTime(raw.SevenDay.ResetsAt)
	}
	if u := windowUtil(raw.SevenDaySonnet); u != nil {
		v := clamp(*u)
		out.SonnetWeeklyPercent = &v
		out.SonnetWeeklyResetsAt = parseTime(raw.SevenDaySonnet.ResetsAt)
	}
	if u := windowUtil(raw.SevenDayOpus); u != nil {
		v := clamp(*u)
		out.OpusWeeklyPercent = &v
		out.OpusWeeklyResetsAt = parseTime(raw.SevenDayOpus.ResetsAt)
	}
	return out, nil
}

func windowUtil(w *rawWindow) *float64 {
	if w == nil {
		return nil
	}
	return w.Utilization
}

func clamp(v float64) float64 {
	return min(max(v, 0), 100)
}

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}
