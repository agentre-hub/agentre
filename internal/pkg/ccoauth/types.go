// Package ccoauth 封装 Claude Code 内部 OAuth usage endpoint
// (GET https://api.anthropic.com/api/oauth/usage) 的读取，复用给桌面端
// cc_usage_svc 和 agentred daemon 远端 RPC。
//
// 我们只读 OAuth access token，不做 refresh、不写回 .credentials.json，
// 避免和 Claude Code 主进程抢锁。token 过期时返回 ErrAuthExpired，
// 用户需自行去对应机器跑 `claude /login`。
package ccoauth

import "time"

// RateLimits 是 /api/oauth/usage 响应的归一化结果。百分比统一在 [0,100]，
// resets_at 字段缺失时为 nil。按模型的周配额（Fable / Opus / Sonnet 等）可选，
// account 没有就为空。
type RateLimits struct {
	FiveHourPercent  float64    `json:"fiveHourPercent"`
	WeeklyPercent    float64    `json:"weeklyPercent"`
	FiveHourResetsAt *time.Time `json:"fiveHourResetsAt,omitempty"`
	WeeklyResetsAt   *time.Time `json:"weeklyResetsAt,omitempty"`

	ModelWeekly []ModelWeeklyLimit `json:"modelWeekly,omitempty"`
}

// ModelWeeklyLimit 是一档只算某个模型的 7 天配额。Model 是 Anthropic 给的展示名
// （如 "Fable"），原样透传给界面，不在本地维护模型名表。
type ModelWeeklyLimit struct {
	Model    string     `json:"model"`
	Percent  float64    `json:"percent"`
	ResetsAt *time.Time `json:"resetsAt,omitempty"`
}
