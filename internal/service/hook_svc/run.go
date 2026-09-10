package hook_svc

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/hook_entity"
	"github.com/agentre-hub/agentre/internal/pkg/code"
	"github.com/agentre-hub/agentre/internal/pkg/hookexec"
	"github.com/agentre-hub/agentre/internal/repository/hook_repo"
)

const (
	runTimeout     = 30 * time.Second
	runMaxOutBytes = 256 * 1024
)

type scriptOutput struct {
	Events []scriptEvent   `json:"events"`
	State  json.RawMessage `json:"state"`
}

type scriptEvent struct {
	Title     string          `json:"title"`
	DedupeKey string          `json:"dedupeKey"`
	Payload   json.RawMessage `json:"payload"`
}

func (s *hookSvc) RunHook(ctx context.Context, req *RunHookRequest) (*RunHookResult, error) {
	if req == nil || req.ID <= 0 {
		return nil, i18n.NewError(ctx, code.InvalidParameter)
	}
	h, err := s.require(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	return s.executeHook(ctx, h, req.DryRun)
}

// executeHook 跑一次脚本；dryRun=true 时不读 / 不写库、不改 state（调度器以 dryRun=false 复用）。
func (s *hookSvc) executeHook(ctx context.Context, h *hook_entity.Hook, dryRun bool) (*RunHookResult, error) {
	spec := hookexec.RunSpec{
		Interpreter:     h.Interpreter,
		InterpreterPath: h.InterpreterPath,
		Command:         h.Command,
		Env:             buildEnv(h),
		Timeout:         runTimeout,
		MaxOutputBytes:  runMaxOutBytes,
	}
	res, runErr := s.runner.Run(ctx, spec)
	out := &RunHookResult{}
	if res != nil {
		out.ExitCode = res.ExitCode
		out.DurationMs = res.Duration.Milliseconds()
		out.TimedOut = res.TimedOut
		out.Stdout = string(res.Stdout)
		out.Stderr = string(res.Stderr)
	}

	failed := runErr != nil || res == nil || res.ExitCode != 0 || res.TimedOut
	var parsed scriptOutput
	if !failed {
		if perr := json.Unmarshal(res.Stdout, &parsed); perr != nil {
			failed = true
			out.ParseError = perr.Error()
		}
	}

	now := s.now()
	if failed {
		if !dryRun {
			s.finishRun(ctx, h, now, "failed", failureMessage(out, runErr), res, 0)
			s.writeFailureEvent(ctx, h, out, runErr, now)
		}
		logger.Ctx(ctx).Warn("hook_svc.executeHook: run failed",
			zap.Int64("hook_id", h.ID), zap.Int("exit", out.ExitCode), zap.Bool("timeout", out.TimedOut))
		return out, nil
	}

	// 成功：解析事件 → (非 dry-run) 去重落库。
	for _, ev := range parsed.Events {
		title := strings.TrimSpace(ev.Title)
		if title == "" {
			continue
		}
		item := &HookEventItem{HookID: h.ID, Kind: hook_entity.HookEventKindOutput, Title: title,
			DedupeKey: ev.DedupeKey, PayloadJSON: rawOrEmpty(ev.Payload), ReceivedAt: now}
		if ev.DedupeKey != "" && !dryRun {
			existing, err := hook_repo.HookEvent().FindByDedupeKey(ctx, h.ID, ev.DedupeKey)
			if err != nil {
				return out, err
			}
			if existing != nil {
				out.DupCount++
				continue
			}
		}
		out.Events = append(out.Events, item)
		out.NewCount++
		if !dryRun {
			row := &hook_entity.HookEvent{
				HookID: h.ID, Kind: hook_entity.HookEventKindOutput, Title: title, DedupeKey: ev.DedupeKey,
				PayloadJSON: item.PayloadJSON, ReceivedAt: now,
				Status: consts.ACTIVE, Createtime: now, Updatetime: now,
			}
			if err := row.Check(ctx); err != nil {
				return out, err
			}
			if err := hook_repo.HookEvent().Create(ctx, row); err != nil {
				return out, err
			}
		}
	}

	if !dryRun {
		newState := rawOrEmpty(parsed.State)
		if strings.TrimSpace(newState) == "" {
			newState = h.StateJSON
		}
		h.StateJSON = newState
		s.finishRun(ctx, h, now, "ok", "", res, out.NewCount)
		out.Persisted = true
	}
	return out, nil
}

// finishRun 回写 last_run_* / state / total_count / next_run_at（next_run 计算见 scheduler.go）。
func (s *hookSvc) finishRun(ctx context.Context, h *hook_entity.Hook, now int64, status, errMsg string, res *hookexec.RunResult, added int) {
	h.LastRunAt = now
	h.LastStatus = status
	h.LastError = errMsg
	if res != nil {
		h.LastDurationMs = res.Duration.Milliseconds()
	}
	h.TotalCount += int64(added)
	h.NextRunAt = s.computeNextRun(h, now)
	h.Updatetime = now
	if err := hook_repo.Hook().Update(ctx, h); err != nil {
		logger.Ctx(ctx).Error("hook_svc.finishRun: update hook failed", zap.Int64("hook_id", h.ID), zap.Error(err))
	}
}

const failureTitleMaxRunes = 200

// writeFailureEvent 把一次失败也落成 hook_event(kind=failure),与成功产出并列进「运行日志」。
// 失败留痕落库失败时仅记日志、不回传——原始运行失败已经报给调用方,不该被持久化错误盖住。
func (s *hookSvc) writeFailureEvent(ctx context.Context, h *hook_entity.Hook, out *RunHookResult, runErr error, now int64) {
	row := &hook_entity.HookEvent{
		HookID:      h.ID,
		Kind:        hook_entity.HookEventKindFailure,
		Title:       failureTitle(out, runErr),
		PayloadJSON: failurePayload(out),
		ReceivedAt:  now,
		Status:      consts.ACTIVE,
		Createtime:  now,
		Updatetime:  now,
	}
	if err := row.Check(ctx); err != nil {
		logger.Ctx(ctx).Error("hook_svc.writeFailureEvent: invalid failure event",
			zap.Int64("hook_id", h.ID), zap.Error(err))
		return
	}
	if err := hook_repo.HookEvent().Create(ctx, row); err != nil {
		logger.Ctx(ctx).Error("hook_svc.writeFailureEvent: create failed",
			zap.Int64("hook_id", h.ID), zap.Error(err))
	}
}

// failureTitle 取失败原因首行作标题(stderr 可能很长/多行,完整内容留在 payload),非零退出且
// stderr 为空时兜底成 "run failed (exit N)"。按 rune 截断避免切碎多字节 UTF-8。
func failureTitle(out *RunHookResult, runErr error) string {
	line, _, _ := strings.Cut(strings.TrimSpace(failureMessage(out, runErr)), "\n")
	msg := strings.TrimSpace(line)
	if msg == "" {
		msg = "run failed (exit " + strconv.Itoa(out.ExitCode) + ")"
	}
	if r := []rune(msg); len(r) > failureTitleMaxRunes {
		msg = string(r[:failureTitleMaxRunes]) + "…"
	}
	return msg
}

// failurePayload 把可查的失败日志(退出码/超时/stderr/stdout/解析错误)序列化进 payload_json。
func failurePayload(out *RunHookResult) string {
	raw, err := json.Marshal(struct {
		ExitCode   int    `json:"exitCode"`
		TimedOut   bool   `json:"timedOut"`
		Stderr     string `json:"stderr,omitempty"`
		Stdout     string `json:"stdout,omitempty"`
		ParseError string `json:"parseError,omitempty"`
	}{
		ExitCode: out.ExitCode, TimedOut: out.TimedOut,
		Stderr: out.Stderr, Stdout: out.Stdout, ParseError: out.ParseError,
	})
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func buildEnv(h *hook_entity.Hook) map[string]string {
	env := map[string]string{
		"HOOK_STATE": orEmptyObject(h.StateJSON),
		"HOOK_NAME":  h.Name,
		"HOOK_ID":    strconv.FormatInt(h.ID, 10),
	}
	for _, e := range parseEnv(h.EnvJSON) {
		if strings.TrimSpace(e.Key) != "" {
			env[e.Key] = e.Value
		}
	}
	return env
}

func rawOrEmpty(r json.RawMessage) string {
	if len(r) == 0 {
		return "{}"
	}
	return string(r)
}

func orEmptyObject(s string) string {
	if strings.TrimSpace(s) == "" {
		return "{}"
	}
	return s
}

func failureMessage(out *RunHookResult, runErr error) string {
	switch {
	case out.TimedOut:
		return "execution timed out"
	case out.ParseError != "":
		return "stdout not valid JSON: " + out.ParseError
	case runErr != nil:
		return runErr.Error()
	default:
		return strings.TrimSpace(out.Stderr)
	}
}
