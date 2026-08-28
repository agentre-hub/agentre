//go:build !windows

package terminal_svc_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"

	"github.com/stretchr/testify/require"
)

// decodeDataEvents reassembles the bytes the frontend would receive: every
// terminal:<id>:data payload's "data" field, base64-decoded and concatenated.
// A payload that is not valid base64 fails the test — raw PTY bytes shipped as a
// JSON string get their invalid-UTF-8 runs rewritten to U+FFFD by json.Marshal,
// so the only way the frontend can rebuild the exact bytes is base64.
func decodeDataEvents(t *testing.T, evs []recordedEvent, id string) (data []byte, count int) {
	t.Helper()
	for _, e := range evs {
		if e.Name != terminal_svc.DataEventName(id) {
			continue
		}
		count++
		m, ok := e.Payload.(map[string]string)
		require.Truef(t, ok, "data payload should be map[string]string, got %T", e.Payload)
		b, err := base64.StdEncoding.DecodeString(m["data"])
		require.NoErrorf(t, err, "data payload %q must be base64-encoded raw bytes", m["data"])
		data = append(data, b...)
	}
	return data, count
}

func waitForExit(t *testing.T, rec *recordingEmitter) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !sawExitEvent(rec.Snapshot()) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.True(t, sawExitEvent(rec.Snapshot()), "pump never emitted the exit event")
}

func openPump(t *testing.T, data chan []byte, exit chan pty.ExitInfo) *recordingEmitter {
	t.Helper()
	rec := &recordingEmitter{}
	sel := terminal_svc.NewBackendSelector(
		&fixedHandleBackend{h: &fakeExitHandle{data: data, exit: exit}}, nil,
	)
	svc := terminal_svc.NewService(sel, rec)
	require.NoError(t, svc.Open(context.Background(), "1", "", "/tmp", 80, 24))
	return rec
}

// TestService_Pump_PreservesMultibyteSplitAcrossChunks is the core regression for
// the garbled-terminal bug: a 3-byte box-drawing char '─' (U+2500 = E2 94 80)
// split across two PTY reads must reach the frontend intact. The old pump emitted
// map{"data": string(chunk)}; Wails' json.Marshal then replaced each half-rune
// byte with U+FFFD, destroying box-drawing / powerline / emoji glyphs.
func TestService_Pump_PreservesMultibyteSplitAcrossChunks(t *testing.T) {
	full := []byte("─") // E2 94 80
	data := make(chan []byte, 2)
	data <- full[:1] // E2
	data <- full[1:] // 94 80
	close(data)
	exit := make(chan pty.ExitInfo, 1)
	exit <- pty.ExitInfo{Code: 0, Reason: "natural"}
	close(exit)

	rec := openPump(t, data, exit)
	waitForExit(t, rec)

	got, _ := decodeDataEvents(t, rec.Snapshot(), "1")
	require.Equal(t, full, got, "split multibyte char must survive the Wails JSON hop")
}

// TestService_Pump_CoalescesBurstIntoFewerEvents is the regression for the TUI
// interleave: a burst of small PTY chunks must be coalesced into fewer Wails
// events. The old pump emitted one event per chunk, flooding the event bridge so
// that dropped/reordered events desynced xterm's parser into garbled output.
// Bytes must still arrive intact and in order.
func TestService_Pump_CoalescesBurstIntoFewerEvents(t *testing.T) {
	chunks := [][]byte{[]byte("a"), []byte("b"), []byte("c"), []byte("d"), []byte("e")}
	data := make(chan []byte, len(chunks))
	for _, c := range chunks {
		data <- c // whole burst already buffered when pump starts draining
	}
	close(data)
	exit := make(chan pty.ExitInfo, 1)
	exit <- pty.ExitInfo{Code: 0, Reason: "natural"}
	close(exit)

	rec := openPump(t, data, exit)
	waitForExit(t, rec)

	got, count := decodeDataEvents(t, rec.Snapshot(), "1")
	require.Equal(t, []byte("abcde"), got, "coalescing must preserve byte order")
	require.Less(t, count, len(chunks),
		"a burst of %d buffered chunks should coalesce into fewer data events, got %d", len(chunks), count)
}

// TestService_Pump_FlushesLoneChunkPromptly guards the time-based flush: a single
// chunk that never reaches the size threshold and is not followed by close must
// still be emitted on its own (otherwise interactive output — a shell prompt, a
// single keystroke echo — would be withheld until more bytes or EOF arrive).
func TestService_Pump_FlushesLoneChunkPromptly(t *testing.T) {
	data := make(chan []byte, 1)
	data <- []byte("x")
	// data stays open; exit never fires — only the timer can flush.
	exit := make(chan pty.ExitInfo)

	rec := openPump(t, data, exit)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got, count := decodeDataEvents(t, rec.Snapshot(), "1"); count > 0 {
			require.Equal(t, []byte("x"), got)
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("lone chunk was never flushed — time-based flush missing")
}
