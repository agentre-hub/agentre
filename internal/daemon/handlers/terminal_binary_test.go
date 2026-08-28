package handlers_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"

	"github.com/stretchr/testify/require"
)

// TestTerminal_Pump_EmitsRawBytesPreservingSplitUTF8 is the remote-side
// regression for garbled terminal output. A multibyte rune may be split across
// PTY reads, so the producer must preserve each raw byte chunk without decoding.
func TestTerminal_Pump_EmitsRawBytesPreservingSplitUTF8(t *testing.T) {
	full := []byte("─") // E2 94 80
	data := make(chan []byte, 2)
	data <- full[:1] // E2
	data <- full[1:] // 94 80
	close(data)
	exit := make(chan pty.ExitInfo, 1)
	exit <- pty.ExitInfo{Code: 0, Reason: "natural"}
	close(exit)

	rec := &recordingEmitter{}
	h := handlers.NewTerminalHandlers(&fakeTermBackend{h: &fakeTermHandle{data: data, exit: exit}}, rec)
	_, err := h.Open(context.Background(), protocol.TerminalOpenParams{Cols: 80, Rows: 24})
	require.NoError(t, err)

	deadline := time.Now().Add(2 * time.Second)
	for !sawDaemonExit(rec.snapshot()) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.True(t, sawDaemonExit(rec.snapshot()), "daemon never emitted exit")

	var got []byte
	for _, e := range rec.snapshot() {
		if e.Name != handlers.EventNameTerminalData {
			continue
		}
		wire, err := json.Marshal(e.Payload) // what the WebSocket serializes
		require.NoError(t, err)
		var ev handlers.TerminalDataEvent
		require.NoError(t, json.Unmarshal(wire, &ev))
		got = append(got, ev.Data...)
	}
	require.Equal(t, full, got, "split multibyte char must survive the daemon→desktop JSON hop")
}
