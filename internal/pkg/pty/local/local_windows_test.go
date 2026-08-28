//go:build windows

package local_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/pkg/pty/local"

	"github.com/stretchr/testify/require"
)

// testShell returns a deterministic shell for tests (cmd.exe), independent of
// whether PowerShell is installed on the runner.
func testShell() string {
	if comspec := os.Getenv("COMSPEC"); comspec != "" {
		return comspec
	}
	return "cmd.exe"
}

func TestLocalBackend_OpenEchoRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	be := local.NewBackend()
	h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: testShell(), Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })

	_, err = h.Write([]byte("echo hello-pty\r\n"))
	require.NoError(t, err)

	deadline := time.After(8 * time.Second)
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
	h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: testShell(), Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })

	// Open's ctx governs only spawning; canceling it must not affect a live PTY
	// (the reaper waits on its own internal context).
	cancel()

	select {
	case info := <-h.Exit():
		t.Fatalf("shell exited after open context cancel: %+v", info)
	case <-time.After(500 * time.Millisecond):
	}

	_, err = h.Write([]byte("echo context-survived\r\n"))
	require.NoError(t, err)

	deadline := time.After(8 * time.Second)
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
		Cwd:   `C:\path\that\definitely\does\not\exist\xyzzy`,
		Shell: testShell(),
		Cols:  80, Rows: 24,
	})
	require.Error(t, err)
}

func TestLocalBackend_Resize_NoError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	be := local.NewBackend()
	h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: testShell(), Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })

	// Reflecting a resize into the child is flaky on Windows (no stty); just
	// assert the ConPTY resize call itself succeeds.
	require.NoError(t, h.Resize(132, 40))
}

func TestLocalBackend_Close_EmitsKilledExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	be := local.NewBackend()
	h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: testShell(), Cols: 80, Rows: 24})
	require.NoError(t, err)

	require.NoError(t, h.Close())

	select {
	case info := <-h.Exit():
		require.Equal(t, "killed", info.Reason)
	case <-time.After(5 * time.Second):
		t.Fatal("did not receive exit info within 5s")
	}
}

func TestLocalBackend_NaturalExit_EmitsNatural(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	be := local.NewBackend()
	h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: testShell(), Cols: 80, Rows: 24})
	require.NoError(t, err)
	t.Cleanup(func() { _ = h.Close() })

	_, err = h.Write([]byte("exit 0\r\n"))
	require.NoError(t, err)

	select {
	case info := <-h.Exit():
		require.Equal(t, "natural", info.Reason)
		require.Equal(t, 0, info.Code)
	case <-time.After(8 * time.Second):
		t.Fatal("did not receive natural exit within 8s")
	}
}
