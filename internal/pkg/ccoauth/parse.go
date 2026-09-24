package ccoauth

import (
	"encoding/json"
	"errors"
	"strings"
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

// rawLimit 是 limits[] 里的一条。只有 kind=weekly_scoped 且 scope 指向某个模型的
// 条目会被取用：session / weekly_all 与 five_hour / seven_day 重复，按 surface
// 划分的条目不是模型配额。
type rawLimit struct {
	Kind     string   `json:"kind"`
	Percent  *float64 `json:"percent,omitempty"`
	ResetsAt string   `json:"resets_at,omitempty"`
	Scope    *struct {
		Model *struct {
			DisplayName string `json:"display_name"`
		} `json:"model,omitempty"`
	} `json:"scope,omitempty"`
}

type rawUsage struct {
	FiveHour       *rawWindow `json:"five_hour,omitempty"`
	SevenDay       *rawWindow `json:"seven_day,omitempty"`
	SevenDaySonnet *rawWindow `json:"seven_day_sonnet,omitempty"`
	SevenDayOpus   *rawWindow `json:"seven_day_opus,omitempty"`
	Limits         []rawLimit `json:"limits,omitempty"`
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
	out.ModelWeekly = modelWeekly(&raw)
	return out, nil
}

// modelWeekly 以 limits[] 为准（Fable 只出现在这里），再把旧的具名字段
// seven_day_sonnet / seven_day_opus 作为兜底补进来；同名只保留 limits[] 那一条。
func modelWeekly(raw *rawUsage) []ModelWeeklyLimit {
	var out []ModelWeeklyLimit
	seen := map[string]bool{}
	add := func(model string, percent float64, resetsAt string) {
		key := strings.ToLower(model)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, ModelWeeklyLimit{Model: model, Percent: clamp(percent), ResetsAt: parseTime(resetsAt)})
	}
	for _, l := range raw.Limits {
		if l.Kind != "weekly_scoped" || l.Percent == nil || l.Scope == nil || l.Scope.Model == nil || l.Scope.Model.DisplayName == "" {
			continue
		}
		add(l.Scope.Model.DisplayName, *l.Percent, l.ResetsAt)
	}
	for _, legacy := range []struct {
		model  string
		window *rawWindow
	}{{"Opus", raw.SevenDayOpus}, {"Sonnet", raw.SevenDaySonnet}} {
		if u := windowUtil(legacy.window); u != nil {
			add(legacy.model, *u, legacy.window.ResetsAt)
		}
	}
	return out
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
