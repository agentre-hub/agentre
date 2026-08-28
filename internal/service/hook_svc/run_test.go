package hook_svc

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/hook_entity"
	"github.com/agentre-hub/agentre/internal/pkg/hookexec"
	"github.com/agentre-hub/agentre/internal/repository/hook_repo"
	"github.com/agentre-hub/agentre/internal/repository/hook_repo/mock_hook_repo"
)

// buildEnv 必须按脚本契约(spec §4.1 + MCP hook_create schema)注入 HOOK_STATE / HOOK_NAME /
// HOOK_ID 三个上下文变量,外加用户声明的 env/密钥条目。HOOK_ID 缺失时脚本里 $HOOK_ID 取空值
// 而 schema 却向 agent 宣称会注入 → 契约违背,此处守住。
func TestBuildEnv_InjectsContextVarsIncludingHookID(t *testing.T) {
	h := &hook_entity.Hook{
		ID:        42,
		Name:      "nightly",
		StateJSON: `{"cursor":7}`,
		EnvJSON:   `[{"key":"TOKEN","value":"sk-1"}]`,
	}
	env := buildEnv(h)
	if got := env["HOOK_ID"]; got != "42" {
		t.Errorf("HOOK_ID = %q, want %q", got, "42")
	}
	if got := env["HOOK_NAME"]; got != "nightly" {
		t.Errorf("HOOK_NAME = %q, want %q", got, "nightly")
	}
	if got := env["HOOK_STATE"]; got != `{"cursor":7}` {
		t.Errorf("HOOK_STATE = %q, want state JSON", got)
	}
	if got := env["TOKEN"]; got != "sk-1" {
		t.Errorf("TOKEN env entry = %q, want %q", got, "sk-1")
	}
}

type fakeRunner struct {
	res *hookexec.RunResult
	err error
}

func (f fakeRunner) Run(_ context.Context, _ hookexec.RunSpec) (*hookexec.RunResult, error) {
	return f.res, f.err
}

func TestRunHook_DryRunParsesButDoesNotPersist(t *testing.T) {
	ctrl := gomock.NewController(t)
	mh := mock_hook_repo.NewMockHookRepo(ctrl)
	hook_repo.RegisterHook(mh)
	// dry-run 不应触碰 event repo（没有 EXPECT 即代表「不可被调用」）。
	hook_repo.RegisterHookEvent(mock_hook_repo.NewMockHookEventRepo(ctrl))

	mh.EXPECT().Find(gomock.Any(), int64(1)).Return(&hook_entity.Hook{
		ID: 1, Name: "j", Interpreter: "bash", Command: "x", EnvJSON: "[]", StateJSON: "{}",
	}, nil)

	svc := &hookSvc{
		now: func() int64 { return 1000 },
		runner: fakeRunner{res: &hookexec.RunResult{
			ExitCode: 0, Duration: 10 * time.Millisecond,
			Stdout: []byte(`{"events":[{"title":"t","dedupeKey":"K1","payload":{"a":1}}],"state":{"c":2}}`),
		}},
	}
	out, err := svc.RunHook(context.Background(), &RunHookRequest{ID: 1, DryRun: true})
	if err != nil {
		t.Fatalf("RunHook: %v", err)
	}
	if out.Persisted || len(out.Events) != 1 || out.NewCount != 1 {
		t.Fatalf("dry-run unexpected: %+v", out)
	}
}

func TestRunHook_RealPersistsDedupAndState(t *testing.T) {
	ctrl := gomock.NewController(t)
	mh := mock_hook_repo.NewMockHookRepo(ctrl)
	me := mock_hook_repo.NewMockHookEventRepo(ctrl)
	hook_repo.RegisterHook(mh)
	hook_repo.RegisterHookEvent(me)

	mh.EXPECT().Find(gomock.Any(), int64(1)).Return(&hook_entity.Hook{
		ID: 1, Name: "j", Interpreter: "bash", Command: "x", EnvJSON: "[]", StateJSON: "{}",
		ScheduleExpr: "*/5 * * * *",
	}, nil)
	// 第一条新事件 → 查重未命中 → 落库；hook 状态回写。
	me.EXPECT().FindByDedupeKey(gomock.Any(), int64(1), "K1").Return(nil, nil)
	me.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil)
	mh.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, h *hook_entity.Hook) error {
			if h.LastStatus != "ok" || h.TotalCount != 1 {
				t.Errorf("hook not updated correctly: %+v", h)
			}
			return nil
		})

	svc := &hookSvc{
		now: func() int64 { return 1000 },
		runner: fakeRunner{res: &hookexec.RunResult{ExitCode: 0,
			Stdout: []byte(`{"events":[{"title":"t","dedupeKey":"K1"}],"state":{"c":2}}`)}},
	}
	out, err := svc.RunHook(context.Background(), &RunHookRequest{ID: 1, DryRun: false})
	if err != nil || !out.Persisted || out.NewCount != 1 {
		t.Fatalf("real run unexpected: out=%+v err=%v", out, err)
	}
}

// 失败的真运行(非 dry-run)必须把失败也落成一条 hook_event(kind=failure),让「运行日志」
// tab 能与成功产出并列展示失败历史。否则失败只活在 hooks.last_error 单槽里——被下一次成功
// 覆盖清空、且前端从不渲染——调度失败事后完全不可见。payload 须保留 stderr 作为可查的失败日志。
func TestRunHook_FailureWritesFailureEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	mh := mock_hook_repo.NewMockHookRepo(ctrl)
	me := mock_hook_repo.NewMockHookEventRepo(ctrl)
	hook_repo.RegisterHook(mh)
	hook_repo.RegisterHookEvent(me)

	mh.EXPECT().Find(gomock.Any(), int64(1)).Return(&hook_entity.Hook{
		ID: 1, Name: "j", Interpreter: "bash", Command: "x", EnvJSON: "[]", StateJSON: "{}",
		ScheduleExpr: "*/5 * * * *"}, nil)
	mh.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)
	me.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, e *hook_entity.HookEvent) error {
			if e.Kind != hook_entity.HookEventKindFailure {
				t.Errorf("event kind = %q, want %q", e.Kind, hook_entity.HookEventKindFailure)
			}
			if strings.TrimSpace(e.Title) == "" {
				t.Error("failure event must carry a non-empty title")
			}
			if !strings.Contains(e.PayloadJSON, "boom") {
				t.Errorf("failure payload should retain stderr, got %q", e.PayloadJSON)
			}
			return nil
		})

	svc := &hookSvc{now: func() int64 { return 1000 },
		runner: fakeRunner{res: &hookexec.RunResult{ExitCode: 2, Stderr: []byte("boom")}}}
	out, err := svc.RunHook(context.Background(), &RunHookRequest{ID: 1, DryRun: false})
	if err != nil {
		t.Fatalf("RunHook returned error: %v", err)
	}
	if out.ExitCode != 2 {
		t.Fatalf("expected exit 2, got %+v", out)
	}
}

// dry-run(试运行)即便失败也绝不落库:event repo 无 EXPECT 即「不可被调用」,hook repo 不回写。
func TestRunHook_DryRunFailureWritesNoEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	mh := mock_hook_repo.NewMockHookRepo(ctrl)
	hook_repo.RegisterHook(mh)
	hook_repo.RegisterHookEvent(mock_hook_repo.NewMockHookEventRepo(ctrl))
	mh.EXPECT().Find(gomock.Any(), int64(1)).Return(&hook_entity.Hook{
		ID: 1, Name: "j", Interpreter: "bash", Command: "x", EnvJSON: "[]", StateJSON: "{}"}, nil)
	svc := &hookSvc{now: func() int64 { return 1000 },
		runner: fakeRunner{res: &hookexec.RunResult{ExitCode: 1, Stderr: []byte("boom")}}}
	out, err := svc.RunHook(context.Background(), &RunHookRequest{ID: 1, DryRun: true})
	if err != nil || out.ExitCode != 1 {
		t.Fatalf("dry-run failure unexpected: out=%+v err=%v", out, err)
	}
}

type captureRunner struct {
	res  *hookexec.RunResult
	spec hookexec.RunSpec
}

func (c *captureRunner) Run(_ context.Context, spec hookexec.RunSpec) (*hookexec.RunResult, error) {
	c.spec = spec
	return c.res, nil
}

func TestRunHook_ThreadsInterpreterPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	mh := mock_hook_repo.NewMockHookRepo(ctrl)
	hook_repo.RegisterHook(mh)
	hook_repo.RegisterHookEvent(mock_hook_repo.NewMockHookEventRepo(ctrl))

	mh.EXPECT().Find(gomock.Any(), int64(1)).Return(&hook_entity.Hook{
		ID: 1, Name: "j", Interpreter: "python", InterpreterPath: "/opt/py/bin/python3",
		Command: "x", EnvJSON: "[]", StateJSON: "{}",
	}, nil)

	cr := &captureRunner{res: &hookexec.RunResult{ExitCode: 0}}
	svc := &hookSvc{now: func() int64 { return 1000 }, runner: cr}

	if _, err := svc.RunHook(context.Background(), &RunHookRequest{ID: 1, DryRun: true}); err != nil {
		t.Fatalf("RunHook: %v", err)
	}
	if cr.spec.InterpreterPath != "/opt/py/bin/python3" {
		t.Errorf("spec.InterpreterPath = %q, want threaded path", cr.spec.InterpreterPath)
	}
}

func TestRunHook_NonZeroExitMarksFailed(t *testing.T) {
	ctrl := gomock.NewController(t)
	mh := mock_hook_repo.NewMockHookRepo(ctrl)
	me := mock_hook_repo.NewMockHookEventRepo(ctrl)
	hook_repo.RegisterHook(mh)
	hook_repo.RegisterHookEvent(me)
	mh.EXPECT().Find(gomock.Any(), int64(1)).Return(&hook_entity.Hook{
		ID: 1, Name: "j", Interpreter: "bash", Command: "x", EnvJSON: "[]", StateJSON: "{}",
		ScheduleExpr: "*/5 * * * *"}, nil)
	me.EXPECT().Create(gomock.Any(), gomock.Any()).Return(nil) // 失败也落一条 failure 事件
	mh.EXPECT().Update(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, h *hook_entity.Hook) error {
			if h.LastStatus != "failed" {
				t.Errorf("expected failed, got %q", h.LastStatus)
			}
			return nil
		})
	svc := &hookSvc{now: func() int64 { return 1000 },
		runner: fakeRunner{res: &hookexec.RunResult{ExitCode: 1, Stderr: []byte("boom")}}}
	out, _ := svc.RunHook(context.Background(), &RunHookRequest{ID: 1, DryRun: false})
	if out.ExitCode != 1 {
		t.Fatalf("expected exit 1, got %+v", out)
	}
}
