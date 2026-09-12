package handlers_test

import (
	"context"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"

	"github.com/stretchr/testify/require"
)

// fakeTermHandle is a handlers.PTYHandle whose Data/Exit channels the test
// controls, mirroring the reaper teardown order (exit buffered+closed, then
// data closed).
type fakeTermHandle struct {
	data chan []byte
	exit chan pty.ExitInfo
}

func (f *fakeTermHandle) Write(p []byte) (int, error) { return len(p), nil }
func (f *fakeTermHandle) Resize(_, _ uint16) error    { return nil }
func (f *fakeTermHandle) Close() error                { return nil }
func (f *fakeTermHandle) Data() <-chan []byte         { return f.data }
func (f *fakeTermHandle) Exit() <-chan pty.ExitInfo   { return f.exit }

type fakeTermBackend struct{ h handlers.PTYHandle }

func (b *fakeTermBackend) Open(_ context.Context, _ pty.Spec) (handlers.PTYHandle, error) {
	return b.h, nil
}

func sawDaemonExit(evs []recordedEvent) bool {
	for _, e := range evs {
		if e.Name == handlers.EventNameTerminalExit {
			return true
		}
	}
	return false
}

// TestTerminal_Pump_ExitNotDroppedWhenDataClosed reproduces the select race
// where the daemon's pump drops terminal.exit ~50% of the time because it
// returns on the closed Data() channel before reading the buffered Exit()
// value — leaving the desktop's remote terminal stuck "open".
func TestTerminal_Pump_ExitNotDroppedWhenDataClosed(t *testing.T) {
	const iters = 80
	dropped := 0
	for i := 0; i < iters; i++ {
		data := make(chan []byte)
		close(data)
		exit := make(chan pty.ExitInfo, 1)
		exit <- pty.ExitInfo{Code: 0, Reason: "natural"}
		close(exit)

		rec := &recordingEmitter{}
		h := handlers.NewTerminalHandlers(&fakeTermBackend{h: &fakeTermHandle{data: data, exit: exit}}, rec)
		_, err := h.Open(context.Background(), protocol.TerminalOpenParams{Cols: 80, Rows: 24})
		require.NoError(t, err)

		deadline := time.Now().Add(500 * time.Millisecond)
		for !sawDaemonExit(rec.snapshot()) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if !sawDaemonExit(rec.snapshot()) {
			dropped++
		}
	}
	require.Zero(t, dropped,
		"terminal.exit dropped in %d/%d runs — select race in daemon pump", dropped, iters)
}

// TestTerminal_Pump_DataFlushedBeforeExit asserts trailing stdout chunks are
// emitted before the exit event (exit must not overtake queued data).
func TestTerminal_Pump_DataFlushedBeforeExit(t *testing.T) {
	data := make(chan []byte, 4)
	data <- []byte("line-1")
	data <- []byte("line-2")
	close(data)
	exit := make(chan pty.ExitInfo, 1)
	exit <- pty.ExitInfo{Code: 0, Reason: "natural"}
	close(exit)

	rec := &recordingEmitter{}
	h := handlers.NewTerminalHandlers(&fakeTermBackend{h: &fakeTermHandle{data: data, exit: exit}}, rec)
	_, err := h.Open(context.Background(), protocol.TerminalOpenParams{Cols: 80, Rows: 24})
	require.NoError(t, err)

	deadline := time.Now().Add(time.Second)
	for !sawDaemonExit(rec.snapshot()) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	evs := rec.snapshot()
	require.True(t, sawDaemonExit(evs), "exit event must be emitted")
	// The exit event must be the LAST event — all data flushed before it.
	last := evs[len(evs)-1]
	require.Equal(t, handlers.EventNameTerminalExit, last.Name,
		"exit must be emitted after all data chunks")

	var dataCount int
	for _, e := range evs {
		if e.Name == handlers.EventNameTerminalData {
			dataCount++
		}
	}
	require.Equal(t, 2, dataCount, "both data chunks must be emitted")
}
