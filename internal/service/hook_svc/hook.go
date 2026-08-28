package hook_svc

import (
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/model/entity/hook_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/hookexec"
	"github.com/agentre-hub/agentre/internal/repository/hook_repo"
)

const (
	defaultEventLimit = 80
	maskedSecret      = "********"
	defaultTimezone   = "Asia/Shanghai"
)

// HookSvc 是脚本 Hook 的服务契约。
type HookSvc interface {
	Load(ctx context.Context, req *LoadHooksRequest) (*LoadHooksResponse, error)
	CreateHook(ctx context.Context, req *CreateHookRequest) (*HookItem, error)
	UpdateHook(ctx context.Context, req *UpdateHookRequest) (*HookItem, error)
	DeleteHook(ctx context.Context, id int64) error
	ToggleHook(ctx context.Context, id int64, enabled bool) (*HookItem, error)
	RunHook(ctx context.Context, req *RunHookRequest) (*RunHookResult, error)
	StartScheduler(ctx context.Context) context.CancelFunc
	ProbeInterpreters(ctx context.Context) ([]InterpreterOption, error)
}

type hookSvc struct {
	now    func() int64
	runner hookexec.ScriptRunner
	sched  schedulerState
}

var defaultHook HookSvc = newHookSvc()

func newHookSvc() *hookSvc {
	return &hookSvc{now: func() int64 { return time.Now().UnixMilli() }, runner: hookexec.NewOSRunner()}
}

func Hook() HookSvc { return defaultHook }

func (s *hookSvc) Load(ctx context.Context, req *LoadHooksRequest) (*LoadHooksResponse, error) {
	if req == nil {
		req = &LoadHooksRequest{}
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultEventLimit
	}
	hooks, err := hook_repo.Hook().List(ctx)
	if err != nil {
		return nil, err
	}
	var events []*hook_entity.HookEvent
	if req.HookID > 0 {
		events, err = hook_repo.HookEvent().ListByHook(ctx, req.HookID, limit)
	} else {
		events, err = hook_repo.HookEvent().ListRecent(ctx, limit)
	}
	if err != nil {
		return nil, err
	}
	items := make([]*HookItem, 0, len(hooks))
	for _, h := range hooks {
		items = append(items, toHookItem(h))
	}
	evItems := make([]*HookEventItem, 0, len(events))
	for _, e := range events {
		evItems = append(evItems, toEventItem(e))
	}
	return &LoadHooksResponse{Hooks: items, Events: evItems}, nil
}

func (s *hookSvc) CreateHook(ctx context.Context, req *CreateHookRequest) (*HookItem, error) {
	if req == nil {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	dup, err := hook_repo.Hook().FindByName(ctx, strings.TrimSpace(req.Name))
	if err != nil {
		return nil, err
	}
	if dup != nil {
		return nil, i18n.NewError(ctx, code.HookNameDuplicated)
	}
	now := s.now()
	h := &hook_entity.Hook{
		Name:            strings.TrimSpace(req.Name),
		Interpreter:     strings.TrimSpace(req.Interpreter),
		InterpreterPath: strings.TrimSpace(req.InterpreterPath),
		Command:         req.Command,
		TriggerType:     hook_entity.TriggerSchedule,
		ScheduleExpr:    strings.TrimSpace(req.ScheduleExpr),
		Timezone:        orDefault(req.Timezone, defaultTimezone),
		EnvJSON:         marshalEnv(req.Env),
		StateJSON:       "{}",
		Enabled:         boolInt(req.Enabled),
		NextRunAt:       now, // 首个 tick 即到期
		Status:          consts.ACTIVE,
		Createtime:      now,
		Updatetime:      now,
	}
	if err := h.Check(ctx); err != nil {
		return nil, err
	}
	if err := hook_repo.Hook().Create(ctx, h); err != nil {
		return nil, err
	}
	return toHookItem(h), nil
}

func (s *hookSvc) UpdateHook(ctx context.Context, req *UpdateHookRequest) (*HookItem, error) {
	if req == nil || req.ID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	h, err := s.require(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	newName := strings.TrimSpace(req.Name)
	if newName != h.Name {
		dup, err := hook_repo.Hook().FindByName(ctx, newName)
		if err != nil {
			return nil, err
		}
		if dup != nil && dup.ID != h.ID {
			return nil, i18n.NewError(ctx, code.HookNameDuplicated)
		}
	}
	h.Name = newName
	h.Interpreter = strings.TrimSpace(req.Interpreter)
	h.InterpreterPath = strings.TrimSpace(req.InterpreterPath)
	h.Command = req.Command
	h.ScheduleExpr = strings.TrimSpace(req.ScheduleExpr)
	h.Timezone = orDefault(req.Timezone, defaultTimezone)
	h.EnvJSON = marshalEnv(preserveSecrets(req.Env, parseEnv(h.EnvJSON)))
	h.Enabled = boolInt(req.Enabled)
	h.Updatetime = s.now()
	if err := h.Check(ctx); err != nil {
		return nil, err
	}
	if err := hook_repo.Hook().Update(ctx, h); err != nil {
		return nil, err
	}
	return toHookItem(h), nil
}

func (s *hookSvc) DeleteHook(ctx context.Context, id int64) error {
	if id <= 0 {
		return i18n.NewError(ctx, code.InvalidParameter)
	}
	if _, err := s.require(ctx, id); err != nil {
		return err
	}
	return hook_repo.Hook().Delete(ctx, id)
}

func (s *hookSvc) ToggleHook(ctx context.Context, id int64, enabled bool) (*HookItem, error) {
	h, err := s.require(ctx, id)
	if err != nil {
		return nil, err
	}
	h.Enabled = boolInt(enabled)
	h.Updatetime = s.now()
	if err := hook_repo.Hook().Update(ctx, h); err != nil {
		return nil, err
	}
	return toHookItem(h), nil
}

func (s *hookSvc) require(ctx context.Context, id int64) (*hook_entity.Hook, error) {
	h, err := hook_repo.Hook().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if h == nil {
		return nil, i18n.NewError(ctx, code.HookNotFound)
	}
	return h, nil
}

// ---- 投影 / env 编解码 / 密钥保留 ----

func toHookItem(h *hook_entity.Hook) *HookItem {
	if h == nil {
		return nil
	}
	env := parseEnv(h.EnvJSON)
	for i := range env {
		if env[i].Secret && strings.TrimSpace(env[i].Value) != "" {
			env[i].Value = maskedSecret
		}
	}
	return &HookItem{
		ID: h.ID, Name: h.Name, Interpreter: h.Interpreter, InterpreterPath: h.InterpreterPath, Command: h.Command,
		ScheduleExpr: h.ScheduleExpr, Timezone: h.Timezone,
		Env: env, Enabled: h.IsEnabled(), NextRunAt: h.NextRunAt, LastRunAt: h.LastRunAt,
		LastStatus: h.LastStatus, LastError: h.LastError, LastDurationMs: h.LastDurationMs,
		TotalCount: h.TotalCount, Createtime: h.Createtime, Updatetime: h.Updatetime,
	}
}

func toEventItem(e *hook_entity.HookEvent) *HookEventItem {
	if e == nil {
		return nil
	}
	return &HookEventItem{
		ID: e.ID, HookID: e.HookID, Kind: orOutputKind(e.Kind), Title: e.Title, DedupeKey: e.DedupeKey,
		PayloadJSON: e.PayloadJSON, ReceivedAt: e.ReceivedAt, Createtime: e.Createtime,
	}
}

// orOutputKind 把空 kind 兜底成 output(历史行 / 默认值缺失),前端无需处理空串。
func orOutputKind(kind string) string {
	if strings.TrimSpace(kind) == "" {
		return hook_entity.HookEventKindOutput
	}
	return kind
}

func parseEnv(raw string) []EnvVar {
	var out []EnvVar
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &out); err != nil {
		return []EnvVar{}
	}
	return out
}

func marshalEnv(env []EnvVar) string {
	if env == nil {
		env = []EnvVar{}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// preserveSecrets：更新时若 secret 值是掩码或空，则保留旧值。
func preserveSecrets(next, current []EnvVar) []EnvVar {
	old := map[string]string{}
	for _, e := range current {
		if e.Secret {
			old[e.Key] = e.Value
		}
	}
	for i := range next {
		if next[i].Secret {
			v := strings.TrimSpace(next[i].Value)
			if v == "" || v == maskedSecret {
				next[i].Value = old[next[i].Key]
			}
		}
	}
	return next
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func (s *hookSvc) ProbeInterpreters(_ context.Context) ([]InterpreterOption, error) {
	avail := hookexec.Probe(runtime.GOOS)
	out := make([]InterpreterOption, 0, len(avail))
	for _, a := range avail {
		out = append(out, InterpreterOption{Key: a.Key, Path: a.Path, Installed: a.Installed})
	}
	return out, nil
}
