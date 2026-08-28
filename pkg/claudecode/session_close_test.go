package claudecode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSession_GivenGracefulCloseIsWaiting_WhenKillIsRequested_ThenProcessExits(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "stdin-closed")
	p, err := startProcess(context.Background(), processSpec{
		binary: "/bin/sh",
		args: []string{
			"-c",
			`trap '' HUP TERM; while IFS= read -r _line; do :; done; : > "$1"; while :; do sleep 1; done`,
			"sh",
			marker,
		},
	})
	if err != nil {
		t.Fatalf("start stubborn subprocess: %v", err)
	}
	t.Cleanup(p.kill)

	s := newSession(p, nil, "")
	closeReturned := make(chan struct{})
	go func() {
		_ = s.Close(context.Background())
		close(closeReturned)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		_, statErr := os.Stat(marker)
		if statErr == nil {
			break
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("observe graceful-close marker: %v", statErr)
		}
		if time.Now().After(deadline) {
			t.Fatal("Close did not close subprocess stdin within one second")
		}
		time.Sleep(10 * time.Millisecond)
	}

	s.Kill()

	select {
	case <-closeReturned:
		if !p.hasExited() {
			t.Fatal("Close returned before the subprocess exited")
		}
	case <-time.After(time.Second):
		t.Fatal("Kill did not terminate the subprocess after graceful Close had started")
	}
}
