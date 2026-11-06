//go:build !windows

package terminal_svc_test

import (
	"sync"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"

	"github.com/stretchr/testify/require"
)

// fakeFlushTimers records every flush timer the pump starts and lets a test fire
// the most recent one on demand, so the flush schedule is driven by the test
// rather than by wall-clock time.
type fakeFlushTimers struct {
	mu     sync.Mutex
	starts int
	fires  []chan time.Time
}

func (f *fakeFlushTimers) start(_ time.Duration) (<-chan time.Time, func() bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	ch := make(chan time.Time, 1)
	f.fires = append(f.fires, ch)
	return ch, func() bool { return true }
}

func (f *fakeFlushTimers) startCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts
}

func (f *fakeFlushTimers) fireLatest(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.fires, "no flush timer has been started to fire")
	f.fires[len(f.fires)-1] <- time.Now()
}

// openPumpWithTimers opens a terminal whose flush timers are fake. Every test
// that uses it must end by delivering an exit and calling waitForExit, so the
// pump goroutine has finished reading the seam before the restore runs.
func openPumpWithTimers(t *testing.T, data chan []byte, exit chan pty.ExitInfo) (*recordingEmitter, *fakeFlushTimers) {
	t.Helper()
	timers := &fakeFlushTimers{}
	restore := terminal_svc.SetStartFlushTimerForTest(timers.start)
	t.Cleanup(restore)
	return openPump(t, data, exit), timers
}

// Given an open terminal with no pending output, when ten flush intervals pass,
// then the pump has started no flush timer, so it cannot wake up while idle.
func TestService_Pump_IdleTerminalStartsNoFlushTimer(t *testing.T) {
	data := make(chan []byte)
	exit := make(chan pty.ExitInfo, 1)
	rec, timers := openPumpWithTimers(t, data, exit)

	time.Sleep(100 * time.Millisecond)
	require.Zero(t, timers.startCount(), "an idle terminal must not arm a flush timer")

	exit <- pty.ExitInfo{Code: 0, Reason: "natural"}
	waitForExit(t, rec)
}

// Given an empty buffer, when one chunk arrives, then exactly one flush timer is
// armed and the chunk waits for it; when it fires, the chunk is emitted and the
// terminal goes idle again without arming another timer.
func TestService_Pump_FirstByteArmsOneTimerAndFiringFlushesIt(t *testing.T) {
	data := make(chan []byte, 1)
	exit := make(chan pty.ExitInfo, 1)
	rec, timers := openPumpWithTimers(t, data, exit)

	data <- []byte("x")
	deadline := time.Now().Add(2 * time.Second)
	for timers.startCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.Equal(t, 1, timers.startCount(), "first byte into an empty buffer must arm one flush timer")
	got, count := decodeDataEvents(t, rec.Snapshot(), "1")
	require.Zero(t, count, "output must wait for the flush timer, not be emitted on arrival")

	timers.fireLatest(t)
	for time.Now().Before(deadline) {
		if got, count = decodeDataEvents(t, rec.Snapshot(), "1"); count > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	require.Equal(t, []byte("x"), got, "firing the flush timer must emit the buffered byte")

	time.Sleep(100 * time.Millisecond)
	require.Equal(t, 1, timers.startCount(), "after the flush, an idle terminal must not arm another timer")

	exit <- pty.ExitInfo{Code: 0, Reason: "natural"}
	waitForExit(t, rec)
}

// Given buffered output whose flush timer has not fired, when the terminal exits,
// then the pending bytes are emitted before the exit event.
func TestService_Pump_ExitFlushesPendingOutputBeforeTimerFires(t *testing.T) {
	data := make(chan []byte, 1)
	exit := make(chan pty.ExitInfo, 1)
	rec, timers := openPumpWithTimers(t, data, exit)

	data <- []byte("abc")
	deadline := time.Now().Add(2 * time.Second)
	for timers.startCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	require.Equal(t, 1, timers.startCount(), "first byte must arm a flush timer")

	exit <- pty.ExitInfo{Code: 0, Reason: "natural"}
	waitForExit(t, rec)

	got, _ := decodeDataEvents(t, rec.Snapshot(), "1")
	require.Equal(t, []byte("abc"), got, "bytes pending at exit must be flushed without waiting for the timer")
	events := rec.Snapshot()
	exitAt, dataAt := -1, -1
	for i, e := range events {
		if e.Name == terminal_svc.ExitEventName("1") && exitAt < 0 {
			exitAt = i
		}
		if e.Name == terminal_svc.DataEventName("1") {
			dataAt = i
		}
	}
	require.GreaterOrEqual(t, dataAt, 0)
	require.Less(t, dataAt, exitAt, "pending output must be emitted before the exit event")
}

// Given a chunk that reaches the size threshold, when it arrives, then it is
// emitted at once without waiting for the flush timer.
func TestService_Pump_ThresholdSizedChunkFlushesWithoutTimer(t *testing.T) {
	data := make(chan []byte, 1)
	exit := make(chan pty.ExitInfo, 1)
	rec, _ := openPumpWithTimers(t, data, exit)

	big := make([]byte, 32*1024)
	for i := range big {
		big[i] = byte('a' + i%26)
	}
	data <- big
	deadline := time.Now().Add(time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		if got, _ = decodeDataEvents(t, rec.Snapshot(), "1"); len(got) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	require.Equal(t, big, got, "a threshold-sized chunk must be flushed without the timer firing")

	exit <- pty.ExitInfo{Code: 0, Reason: "natural"}
	waitForExit(t, rec)
}
