package hook_entity

import (
	"context"
	"testing"
)

func TestHook_Check(t *testing.T) {
	ctx := context.Background()
	base := func() *Hook {
		return &Hook{Name: "jira", Interpreter: "bash", Command: "echo '{}'",
			ScheduleExpr: "*/5 * * * *", EnvJSON: "[]"}
	}
	if err := base().Check(ctx); err != nil {
		t.Fatalf("valid hook should pass: %v", err)
	}
	cases := map[string]func(*Hook){
		"empty name":      func(h *Hook) { h.Name = "  " },
		"bad interpreter": func(h *Hook) { h.Interpreter = "ruby" },
		"empty schedule":  func(h *Hook) { h.ScheduleExpr = "" },
		"empty command":   func(h *Hook) { h.Command = "" },
		"bad env json":    func(h *Hook) { h.EnvJSON = "{not array}" },
	}
	for name, mutate := range cases {
		h := base()
		mutate(h)
		if err := h.Check(ctx); err == nil {
			t.Errorf("%s: expected Check error, got nil", name)
		}
	}
}

func TestHookEvent_Check(t *testing.T) {
	ctx := context.Background()
	ok := &HookEvent{HookID: 1, Title: "t", PayloadJSON: "{}"}
	if err := ok.Check(ctx); err != nil {
		t.Fatalf("valid event should pass: %v", err)
	}
	for name, e := range map[string]*HookEvent{
		"no hook":  {HookID: 0, Title: "t", PayloadJSON: "{}"},
		"no title": {HookID: 1, Title: "", PayloadJSON: "{}"},
		"bad json": {HookID: 1, Title: "t", PayloadJSON: "{bad"},
	} {
		if err := e.Check(ctx); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
