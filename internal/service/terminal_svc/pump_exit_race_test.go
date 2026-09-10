//go:build !windows

package terminal_svc_test

import (
	"context"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"

	"github.com/stretchr/testify/require"
)

// fakeExitHandle is a pty.Handle whose Data/Exit channels we control, to
// reproduce the exact teardown order of local/remote reapers: the exit value
// is buffered+closed first, then the data channel is closed. After that both
// channels are simultaneously "ready" inside Service.pump's select.
type fakeExitHandle struct {
	data chan []byte
	exit chan pty.ExitInfo
}

func (f *fakeExitHandle) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakeExitHandle) Resize(_, _ uint16) error    { return nil }
func (f *fakeExitHandle) Close() error                { return nil }
func (f *fakeExitHandle) Data() <-chan []byte         { return f.data }
func (f *fakeExitHandle) Exit() <-chan pty.ExitInfo   { return f.exit }

type fixedHandleBackend struct{ h pty.Handle }

func (b *fixedHandleBackend) Open(_ context.Context, _ pty.Spec) (pty.Handle, error) {
	return b.h, nil
}

func sawExitEvent(evs []recordedEvent) bool {
	for _, e := range evs {
		if e.Name == terminal_svc.ExitEventName("1") {
			return true
		}
	}
	return false
}

// TestService_Pump_ExitNotDroppedWhenDataChannelClosed reproduces the bug
// where a PTY that has exited drops its exit event ~50% of the time. The
// reaper buffers the exit value, then closes the data channel; Service.pump's
// select then sees BOTH the closed Data() channel and the buffered Exit()
// value as ready and picks randomly. When it picks the closed-data case it
// returns silently — the frontend never receives terminal:<id>:exit, so the
// terminal panel and toggle button stay stuck in the "open" state forever.
func TestService_Pump_ExitNotDroppedWhenDataChannelClosed(t *testing.T) {
	const iters = 80
	dropped := 0
	for i := 0; i < iters; i++ {
		data := make(chan []byte)
		close(data) // drained + closed, like reaper's final close(h.data)
		exit := make(chan pty.ExitInfo, 1)
		exit <- pty.ExitInfo{Code: 0, Reason: "natural"}
		close(exit)

		rec := &recordingEmitter{}
		sel := terminal_svc.NewBackendSelector(
			&fixedHandleBackend{h: &fakeExitHandle{data: data, exit: exit}}, nil,
		)
		svc := terminal_svc.NewService(sel, rec)

		require.NoError(t, svc.Open(context.Background(), "1", "", "/tmp", 80, 24))

		// Poll for the exit event rather than sleeping a fixed amount.
		deadline := time.Now().Add(500 * time.Millisecond)
		for !sawExitEvent(rec.Snapshot()) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if !sawExitEvent(rec.Snapshot()) {
			dropped++
		}
	}
	require.Zero(t, dropped,
		"exit event dropped in %d/%d runs — select race in Service.pump", dropped, iters)
}
