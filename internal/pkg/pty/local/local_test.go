//go:build !windows

package local_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/pkg/pty/local"

	"github.com/stretchr/testify/require"
)

func TestLocalBackend_OpenEchoRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	be := local.NewBackend()
	h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })

	_, err = h.Write([]byte("echo hello-pty\n"))
	require.NoError(t, err)

	deadline := time.After(3 * time.Second)
	var buf bytes.Buffer
	for {
		select {
		case chunk, ok := <-h.Data():
			if !ok {
				t.Fatalf("data channel closed before seeing echo output; got: %q", buf.String())
			}
			buf.Write(chunk)
			if bytes.Contains(buf.Bytes(), []byte("hello-pty")) {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for echo output; got: %q", buf.String())
		}
	}
}

func TestLocalBackend_OpenContextCancelAfterSpawn_DoesNotKillShell(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	be := local.NewBackend()
	h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })

	cancel()

	select {
	case info := <-h.Exit():
		t.Fatalf("shell exited after open context cancel: %+v", info)
	case <-time.After(300 * time.Millisecond):
	}

	_, err = h.Write([]byte("echo context-survived\n"))
	require.NoError(t, err)

	deadline := time.After(3 * time.Second)
	var buf bytes.Buffer
	for {
		select {
		case chunk, ok := <-h.Data():
			if !ok {
				t.Fatalf("data channel closed after open context cancel; got: %q", buf.String())
			}
			buf.Write(chunk)
			if bytes.Contains(buf.Bytes(), []byte("context-survived")) {
				return
			}
		case info := <-h.Exit():
			t.Fatalf("shell exited after open context cancel: %+v", info)
		case <-deadline:
			t.Fatalf("timeout waiting for shell after open context cancel; got: %q", buf.String())
		}
	}
}

func TestLocalBackend_OpenBadCwd_Errors(t *testing.T) {
	be := local.NewBackend()
	_, err := be.Open(context.Background(), pty.Spec{
		Cwd:   "/path/that/definitely/does/not/exist/xyzzy",
		Shell: "/bin/sh",
		Cols:  80, Rows: 24,
	})
	require.Error(t, err)
}

func TestLocalBackend_Resize_Reflected(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	be := local.NewBackend()
	h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })

	require.NoError(t, h.Resize(132, 40))
	_, err = h.Write([]byte("stty size\n"))
	require.NoError(t, err)

	deadline := time.After(3 * time.Second)
	var buf bytes.Buffer
	for {
		select {
		case chunk, ok := <-h.Data():
			if !ok {
				t.Fatalf("data closed before stty output; got: %q", buf.String())
			}
			buf.Write(chunk)
			if bytes.Contains(buf.Bytes(), []byte("40 132")) {
				return
			}
		case <-deadline:
			t.Fatalf("timeout waiting for 40 132 in output; got: %q", buf.String())
		}
	}
}

func TestLocalBackend_Close_EmitsKilledExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	be := local.NewBackend()
	h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
	require.NoError(t, err)

	require.NoError(t, h.Close())

	select {
	case info := <-h.Exit():
		require.Equal(t, "killed", info.Reason)
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive exit info within 2s")
	}
}

func TestLocalBackend_GivenShellWaitsForStubbornChild_WhenHandleCloses_ThenProcessTreeExitsWithinDeadline(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	command := fmt.Sprintf(
		`/bin/sh -c 'trap "" HUP TERM; printf "%%s %%s" "$$" "$PPID" > %q; while :; do sleep 1; done'`+"\n",
		pidFile,
	)
	h, err := local.NewBackend().Open(context.Background(), pty.Spec{
		Cwd:   os.TempDir(),
		Shell: "/bin/sh",
		Cols:  80,
		Rows:  24,
	})
	require.NoError(t, err)
	_, err = h.Write([]byte(command))
	require.NoError(t, err)

	var childPID, shellPID int
	require.Eventually(t, func() bool {
		rawPID, readErr := os.ReadFile(pidFile) //nolint:gosec // G304: pidFile is test-owned under t.TempDir.
		if readErr != nil {
			return false
		}
		fields := strings.Fields(string(rawPID))
		if len(fields) != 2 {
			return false
		}
		childPID, readErr = strconv.Atoi(fields[0])
		if readErr != nil {
			return false
		}
		shellPID, readErr = strconv.Atoi(fields[1])
		return readErr == nil && childPID > 0 && shellPID > 0
	}, time.Second, 10*time.Millisecond, "shell did not publish its child PID")
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		_ = syscall.Kill(shellPID, syscall.SIGKILL)
	})

	closeStarted := time.Now()
	closeReturned := make(chan error, 1)
	go func() { closeReturned <- h.Close() }()
	select {
	case closeErr := <-closeReturned:
		require.NoError(t, closeErr)
	case <-time.After(time.Second):
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		_ = syscall.Kill(shellPID, syscall.SIGKILL)
		select {
		case <-closeReturned:
		case <-time.After(time.Second):
		}
		t.Fatal("Close did not return within one second while the shell waited for its child")
	}
	require.Less(t, time.Since(closeStarted), time.Second, "Close must have a final deadline")
	require.Eventually(t, func() bool {
		return !localProcessAlive(childPID)
	}, time.Second, 10*time.Millisecond, "Close left the shell's child process alive")
}

func localProcessAlive(pid int) bool {
	output, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output() //nolint:gosec // G204: fixed executable with a test-owned PID.
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.ContainsAny(state, "ZE")
}

func TestLocalBackend_NaturalExit_EmitsNatural(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	be := local.NewBackend()
	h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })

	_, err = h.Write([]byte("exit 0\n"))
	require.NoError(t, err)

	select {
	case info := <-h.Exit():
		require.Equal(t, "natural", info.Reason)
		require.Equal(t, 0, info.Code)
	case <-time.After(3 * time.Second):
		t.Fatal("did not receive natural exit within 3s")
	}
}

func TestLocalBackend_OpenCommand_RunsAndExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	be := local.NewBackend()
	h, err := be.Open(ctx, pty.Spec{
		Cwd: os.TempDir(), Shell: "/bin/sh",
		Command: "echo cmd-mode-ok", Cols: 80, Rows: 24,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })

	deadline := time.After(3 * time.Second)
	var buf bytes.Buffer
	for {
		select {
		case chunk, ok := <-h.Data():
			buf.Write(chunk)
			if bytes.Contains(buf.Bytes(), []byte("cmd-mode-ok")) {
				goto awaitExit
			}
			if !ok {
				t.Fatalf("data closed before output; got %q", buf.String())
			}
		case <-deadline:
			t.Fatalf("timeout; got %q", buf.String())
		}
	}
awaitExit:
	select {
	case info := <-h.Exit():
		require.Equal(t, 0, info.Code)
	case <-time.After(3 * time.Second):
		t.Fatal("command did not exit")
	}
}
