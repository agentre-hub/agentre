// Package hook_entity 是脚本驱动 Hook 与其产出事件的富模型。
package hook_entity

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

const TriggerSchedule = "schedule" // 预留 "webhook"

// ValidInterpreters 是允许声明的解释器 allowlist（见 hookexec 注册表）。
var ValidInterpreters = map[string]struct{}{
	"bash": {}, "sh": {}, "node": {}, "python": {}, "pwsh": {}, "powershell": {}, "cmd": {},
}

// Hook 是一段可调度的脚本：拉数据→stdout 产出 {events,state}。
type Hook struct {
	ID              int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name            string `gorm:"column:name;type:text;not null"`
	Interpreter     string `gorm:"column:interpreter;type:text;not null;default:'bash'"`
	InterpreterPath string `gorm:"column:interpreter_path;type:text;not null;default:''"`
	Command         string `gorm:"column:command;type:text;not null;default:''"`
	TriggerType     string `gorm:"column:trigger_type;type:text;not null;default:'schedule'"`
	ScheduleExpr    string `gorm:"column:schedule_expr;type:text;not null;default:''"` // cron 表达式
	Timezone        string `gorm:"column:timezone;type:text;not null;default:'Asia/Shanghai'"`
	EnvJSON         string `gorm:"column:env_json;type:text;not null;default:'[]'"`
	StateJSON       string `gorm:"column:state_json;type:text;not null;default:'{}'"`
	NextRunAt       int64  `gorm:"column:next_run_at;type:bigint;not null;default:0"`
	Enabled         int    `gorm:"column:enabled;type:int;not null;default:1"`
	LastRunAt       int64  `gorm:"column:last_run_at;type:bigint;not null;default:0"`
	LastStatus      string `gorm:"column:last_status;type:text;not null;default:''"`
	LastError       string `gorm:"column:last_error;type:text;not null;default:''"`
	LastDurationMs  int64  `gorm:"column:last_duration_ms;type:bigint;not null;default:0"`
	TotalCount      int64  `gorm:"column:total_count;type:bigint;not null;default:0"`
	Status          int    `gorm:"column:status;type:int;not null;default:1"`
	Createtime      int64
	Updatetime      int64
}

func (*Hook) TableName() string { return "hooks" }

func (h *Hook) IsEnabled() bool { return h != nil && h.Enabled == 1 }

func (h *Hook) Check(ctx context.Context) error {
	if h == nil {
		return i18n.NewError(ctx, code.HookNotFound)
	}
	if strings.TrimSpace(h.Name) == "" || strings.TrimSpace(h.Command) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if _, ok := ValidInterpreters[strings.TrimSpace(h.Interpreter)]; !ok {
		return i18n.NewError(ctx, code.HookInvalidInterpreter)
	}
	if h.TriggerType == "" {
		h.TriggerType = TriggerSchedule
	}
	if strings.TrimSpace(h.ScheduleExpr) == "" {
		return i18n.NewError(ctx, code.HookInvalidSchedule)
	}
	if err := validateJSONArray(h.EnvJSON); err != nil {
		return i18n.NewError(ctx, code.HookInvalidConfig)
	}
	if err := validateJSONObject(h.StateJSON); err != nil {
		return i18n.NewError(ctx, code.HookInvalidConfig)
	}
	return nil
}

// HookEvent 的 Kind:脚本成功产出的结构化记录 vs 运行失败留痕。
const (
	HookEventKindOutput  = "output"  // 脚本 stdout 解析出的一条结构化事件
	HookEventKindFailure = "failure" // 一次运行失败的留痕（exit≠0 / 超时 / stdout 非 JSON）
)

// HookEvent 是一条运行留痕:成功时为脚本产出的结构化记录(kind=output),失败时为失败日志(kind=failure)。
type HookEvent struct {
	ID          int64  `gorm:"column:id;primaryKey;autoIncrement"`
	HookID      int64  `gorm:"column:hook_id;type:bigint;not null"`
	Kind        string `gorm:"column:kind;type:text;not null;default:'output'"`
	Title       string `gorm:"column:title;type:text;not null"`
	DedupeKey   string `gorm:"column:dedupe_key;type:text;not null;default:''"`
	PayloadJSON string `gorm:"column:payload_json;type:text;not null;default:'{}'"`
	ReceivedAt  int64  `gorm:"column:received_at;type:bigint;not null;default:0"`
	Status      int    `gorm:"column:status;type:int;not null;default:1"`
	Createtime  int64
	Updatetime  int64
}

func (*HookEvent) TableName() string { return "hook_events" }

func (e *HookEvent) Check(ctx context.Context) error {
	if e == nil {
		return i18n.NewError(ctx, code.HookEventNotFound)
	}
	if e.HookID <= 0 || strings.TrimSpace(e.Title) == "" {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if err := validateJSONObject(e.PayloadJSON); err != nil {
		return i18n.NewError(ctx, code.HookInvalidConfig)
	}
	return nil
}

func validateJSONObject(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var out map[string]any
	return json.Unmarshal([]byte(trimmed), &out)
}

func validateJSONArray(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var out []any
	return json.Unmarshal([]byte(trimmed), &out)
}
