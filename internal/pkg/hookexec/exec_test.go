package hookexec

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestOSRunner_EchoJSON(t *testing.T) {
	if _, err := resolve("sh", ""); err != nil {
		t.Skip("sh unavailable")
	}
	r := NewOSRunner()
	res, err := r.Run(context.Background(), RunSpec{
		Interpreter:    "sh",
		Command:        `printf '{"events":[],"state":{"k":"%s"}}' "$HOOK_STATE"`,
		Env:            map[string]string{"HOOK_STATE": "v1"},
		Timeout:        5 * time.Second,
		MaxOutputBytes: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(string(res.Stdout), `"k":"v1"`) {
		t.Fatalf("unexpected result: code=%d out=%s", res.ExitCode, res.Stdout)
	}
}

func TestOSRunner_NonZeroExit(t *testing.T) {
	if _, err := resolve("sh", ""); err != nil {
		t.Skip("sh unavailable")
	}
	r := NewOSRunner()
	res, err := r.Run(context.Background(), RunSpec{
		Interpreter: "sh", Command: "echo oops 1>&2; exit 3", Timeout: 5 * time.Second, MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatalf("non-zero exit should be reported in result, not err: %v", err)
	}
	if res.ExitCode != 3 || !strings.Contains(string(res.Stderr), "oops") {
		t.Fatalf("unexpected: code=%d stderr=%s", res.ExitCode, res.Stderr)
	}
}

func TestOSRunner_Timeout(t *testing.T) {
	if _, err := resolve("sh", ""); err != nil {
		t.Skip("sh unavailable")
	}
	r := NewOSRunner()
	res, err := r.Run(context.Background(), RunSpec{
		Interpreter: "sh", Command: "sleep 5", Timeout: 200 * time.Millisecond, MaxOutputBytes: 1024,
	})
	if err != nil {
		t.Fatalf("Run returned err (timeout should be in result, not err): %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("expected TimedOut, got %+v", res)
	}
}
