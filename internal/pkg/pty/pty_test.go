package pty_test

import (
	"testing"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
)

func TestSpec_ZeroValueValid(t *testing.T) {
	s := pty.Spec{}
	if s.TerminalID != "" || s.Cols != 0 || s.Rows != 0 {
		t.Fatalf("zero Spec must have no runtime terminal ID and zero cols/rows")
	}
}

func TestExitInfo_Reason_KnownValues(t *testing.T) {
	for _, r := range []string{"natural", "killed", "connection_lost", "daemon_shutdown", "error"} {
		ei := pty.ExitInfo{Reason: r}
		if ei.Reason == "" {
			t.Fatalf("reason should be set")
		}
	}
}
