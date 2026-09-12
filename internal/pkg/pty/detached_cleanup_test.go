package pty_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/pty"

	"github.com/stretchr/testify/require"
)

var errDetachedCleanupClose = errors.New("detached cleanup close failed")

type detachedCleanupHandle struct {
	data chan []byte
	exit chan pty.ExitInfo

	closeCalls  atomic.Int32
	dataCalls   atomic.Int32
	exitCalls   atomic.Int32
	allowClose  atomic.Bool
	dataStarted chan struct{}
	dataOnce    sync.Once
	finishOnce  sync.Once
}

func newDetachedCleanupHandle() *detachedCleanupHandle {
	return &detachedCleanupHandle{
		data:        make(chan []byte),
		exit:        make(chan pty.ExitInfo, 1),
		dataStarted: make(chan struct{}),
	}
}

func (h *detachedCleanupHandle) Close() error {
	h.closeCalls.Add(1)
	if h.allowClose.Load() {
		return nil
	}
	return errDetachedCleanupClose
}

func (h *detachedCleanupHandle) Data() <-chan []byte {
	h.dataCalls.Add(1)
	h.dataOnce.Do(func() { close(h.dataStarted) })
	return h.data
}

func (h *detachedCleanupHandle) Exit() <-chan pty.ExitInfo {
	h.exitCalls.Add(1)
	return h.exit
}

func (h *detachedCleanupHandle) finish(info pty.ExitInfo) {
	h.finishWithTrailingData(info)
}

func (h *detachedCleanupHandle) finishWithTrailingData(info pty.ExitInfo, trailing ...[]byte) {
	h.finishOnce.Do(func() {
		h.exit <- info
		close(h.exit)
		for _, data := range trailing {
			h.data <- data
		}
		close(h.data)
	})
}

func receiveDetachedCleanupOutcome(t *testing.T, outcomes <-chan pty.DetachedCleanupOutcome) pty.DetachedCleanupOutcome {
	t.Helper()
	select {
	case outcome := <-outcomes:
		return outcome
	case <-time.After(time.Second):
		t.Fatal("detached cleanup did not settle")
		return ""
	}
}

func TestStartDetachedCleanup_GivenInitialAndRepeatedCloseFailuresWithBlockedData_WhenCloseLaterSucceeds_ThenDrainsAndSettlesOnce(t *testing.T) {
	handle := newDetachedCleanupHandle()
	t.Cleanup(func() { handle.finish(pty.ExitInfo{Reason: "testCleanup"}) })
	require.ErrorIs(t, handle.Close(), errDetachedCleanupClose, "caller performs the initial close attempt")

	outcomes := make(chan pty.DetachedCleanupOutcome, 1)
	pty.StartDetachedCleanup(handle, func(outcome pty.DetachedCleanupOutcome) {
		outcomes <- outcome
	})
	select {
	case <-handle.dataStarted:
	case <-time.After(time.Second):
		t.Fatal("detached cleanup did not claim the data drain")
	}

	dataSent := make(chan struct{})
	go func() {
		handle.data <- []byte("discarded output")
		close(dataSent)
	}()
	select {
	case <-dataSent:
	case <-time.After(time.Second):
		t.Fatal("terminal producer remained blocked because detached cleanup did not drain data")
	}

	require.Eventually(t, func() bool {
		return handle.closeCalls.Load() >= 3
	}, time.Second, time.Millisecond, "detached cleanup did not repeat failed Close at a paced interval")
	require.LessOrEqual(t, handle.closeCalls.Load(), int32(4), "detached cleanup retried Close in a busy loop")
	handle.allowClose.Store(true)

	require.Equal(t, pty.DetachedCleanupCloseSucceeded, receiveDetachedCleanupOutcome(t, outcomes))
	require.Equal(t, int32(1), handle.dataCalls.Load())
	require.Equal(t, int32(1), handle.exitCalls.Load())
	closeCalls := handle.closeCalls.Load()
	time.Sleep(125 * time.Millisecond)
	require.Equal(t, closeCalls, handle.closeCalls.Load(), "settled cleanup must stop retrying")
}

func TestStartDetachedCleanup_GivenCloseKeepsFailing_WhenTerminalExitsWithTrailingData_ThenDrainsAndSettlesNaturally(t *testing.T) {
	handle := newDetachedCleanupHandle()
	t.Cleanup(func() { handle.finish(pty.ExitInfo{Reason: "testCleanup"}) })
	require.ErrorIs(t, handle.Close(), errDetachedCleanupClose, "caller performs the initial close attempt")

	outcomes := make(chan pty.DetachedCleanupOutcome, 1)
	pty.StartDetachedCleanup(handle, func(outcome pty.DetachedCleanupOutcome) {
		outcomes <- outcome
	})
	select {
	case <-handle.dataStarted:
	case <-time.After(time.Second):
		t.Fatal("detached cleanup did not claim the data drain")
	}
	require.Eventually(t, func() bool {
		return handle.closeCalls.Load() >= 2
	}, time.Second, time.Millisecond, "detached cleanup did not retry failed Close")

	trailingDrained := make(chan struct{})
	go func() {
		handle.finishWithTrailingData(
			pty.ExitInfo{Code: 0, Reason: "natural"},
			[]byte("trailing-1"),
			[]byte("trailing-2"),
		)
		close(trailingDrained)
	}()

	require.Equal(t, pty.DetachedCleanupTerminalExited, receiveDetachedCleanupOutcome(t, outcomes))
	select {
	case <-trailingDrained:
	case <-time.After(time.Second):
		t.Fatal("detached cleanup settled before draining trailing terminal data")
	}
	closeCalls := handle.closeCalls.Load()
	time.Sleep(125 * time.Millisecond)
	require.Equal(t, closeCalls, handle.closeCalls.Load(), "natural settlement must stop retries")
}

func TestStartDetachedCleanup_GivenCloseSuccessRacesNaturalExit_ThenStartsOneGuardianAndReportsOneBoundedOutcome(t *testing.T) {
	const iterations = 64
	type cleanupRace struct {
		handle   *detachedCleanupHandle
		outcomes atomic.Int32
		settled  chan pty.DetachedCleanupOutcome
	}

	traces := make([]cleanupRace, iterations)
	start := make(chan struct{})
	for i := range traces {
		handle := newDetachedCleanupHandle()
		require.ErrorIs(t, handle.Close(), errDetachedCleanupClose)
		traces[i] = cleanupRace{handle: handle, settled: make(chan pty.DetachedCleanupOutcome, 1)}
		race := &traces[i]
		pty.StartDetachedCleanup(handle, func(outcome pty.DetachedCleanupOutcome) {
			race.outcomes.Add(1)
			race.settled <- outcome
		})
		go func() {
			<-start
			handle.allowClose.Store(true)
		}()
		go func() {
			<-start
			handle.finish(pty.ExitInfo{Code: 0, Reason: "natural"})
		}()
	}
	close(start)

	for i := range traces {
		outcome := receiveDetachedCleanupOutcome(t, traces[i].settled)
		require.Contains(t, []pty.DetachedCleanupOutcome{
			pty.DetachedCleanupCloseSucceeded,
			pty.DetachedCleanupTerminalExited,
		}, outcome, "iteration=%d", i)
	}
	time.Sleep(125 * time.Millisecond)
	for i := range traces {
		require.Equalf(t, int32(1), traces[i].outcomes.Load(), "iteration=%d", i)
		require.Equalf(t, int32(1), traces[i].handle.dataCalls.Load(), "iteration=%d", i)
		require.Equalf(t, int32(1), traces[i].handle.exitCalls.Load(), "iteration=%d", i)
	}
}
