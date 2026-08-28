//go:build !windows

package local_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/pkg/pty/local"

	"github.com/stretchr/testify/require"
)

// TestLocalBackend_OutputNotTruncatedOnExit verifies that all stdout produced
// before a natural exit is delivered. The buggy reaper closes the PTY file
// immediately after cmd.Wait, discarding output still buffered in the PTY that
// the reader had not yet drained — so the tail of a burst is lost.
func TestLocalBackend_OutputNotTruncatedOnExit(t *testing.T) {
	const lines = 5000
	const marker = "END-OF-OUTPUT-MARKER-7e3f"
	for iter := 0; iter < 10; iter++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		be := local.NewBackend()
		h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
		require.NoError(t, err)

		_, err = fmt.Fprintf(h,
			"i=0; while [ $i -lt %d ]; do echo line-$i; i=$((i+1)); done; echo %s; exit 0\n",
			lines, marker)
		require.NoError(t, err)

		dataCh := h.Data()
		exitCh := h.Exit()
		var buf bytes.Buffer
		deadline := time.After(8 * time.Second)
	consume:
		for {
			select {
			case chunk, ok := <-dataCh:
				if !ok {
					dataCh = nil
				} else {
					buf.Write(chunk)
				}
			case _, ok := <-exitCh:
				if !ok {
					exitCh = nil
				} else {
					exitCh = nil
				}
			case <-deadline:
				cancel()
				t.Fatal("timed out draining terminal")
			}
			if dataCh == nil && exitCh == nil {
				break consume
			}
		}
		cancel()
		require.Containsf(t, buf.String(), marker,
			"iter %d: tail of output truncated — end marker missing", iter)
	}
}

// TestLocalBackend_BurstThenExit_NoSendOnClosedPanic drives a large burst of
// stdout immediately followed by shell exit. In the buggy implementation the
// reader goroutine sends to h.data while the reaper goroutine closes h.data,
// which panics with "send on closed channel" and crashes the process. A
// correct implementation has the reader as the sole closer of the channel.
func TestLocalBackend_BurstThenExit_NoSendOnClosedPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("stress test")
	}
	for iter := 0; iter < 40; iter++ {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		be := local.NewBackend()
		h, err := be.Open(ctx, pty.Spec{Cwd: os.TempDir(), Shell: "/bin/sh", Cols: 80, Rows: 24})
		require.NoError(t, err)

		// Burst a few thousand lines then exit so the reader is mid-stream
		// exactly as the process dies and the reaper tears down.
		_, err = h.Write([]byte("i=0; while [ $i -lt 3000 ]; do echo line-$i; i=$((i+1)); done; exit 0\n"))
		require.NoError(t, err)

		// Drain data and wait for exit; the channels close after exit fires.
		dataCh := h.Data()
		exitCh := h.Exit()
		var gotExit bool
		deadline := time.After(8 * time.Second)
	consume:
		for {
			select {
			case _, ok := <-dataCh:
				if !ok {
					dataCh = nil
				}
			case _, ok := <-exitCh:
				if ok {
					gotExit = true
				}
				exitCh = nil
			case <-deadline:
				cancel()
				t.Fatal("timed out draining terminal")
			}
			if dataCh == nil && exitCh == nil {
				break consume
			}
		}
		cancel()
		require.True(t, gotExit, "expected an exit event")
	}
}
