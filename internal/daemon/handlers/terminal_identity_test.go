package handlers_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"

	"github.com/stretchr/testify/require"
)

type terminalBackendFunc func(context.Context, pty.Spec) (handlers.PTYHandle, error)

func (f terminalBackendFunc) Open(ctx context.Context, spec pty.Spec) (handlers.PTYHandle, error) {
	return f(ctx, spec)
}

type trackedTerminalHandle struct {
	data chan []byte
	exit chan pty.ExitInfo

	closeOnce  sync.Once
	closeCalls atomic.Int32
	dataCalls  atomic.Int32
	exitCalls  atomic.Int32
}

func newTrackedTerminalHandle() *trackedTerminalHandle {
	return &trackedTerminalHandle{
		data: make(chan []byte),
		exit: make(chan pty.ExitInfo, 1),
	}
}

func (h *trackedTerminalHandle) Write(p []byte) (int, error) { return len(p), nil }
func (h *trackedTerminalHandle) Resize(_, _ uint16) error    { return nil }

func (h *trackedTerminalHandle) Close() error {
	h.closeCalls.Add(1)
	h.closeOnce.Do(func() {
		h.exit <- pty.ExitInfo{Code: 137, Reason: "killed"}
		close(h.exit)
		close(h.data)
	})
	return nil
}

func (h *trackedTerminalHandle) Data() <-chan []byte {
	h.dataCalls.Add(1)
	return h.data
}

func (h *trackedTerminalHandle) Exit() <-chan pty.ExitInfo {
	h.exitCalls.Add(1)
	return h.exit
}

type controlledTerminalHandle struct {
	data chan []byte
	exit chan pty.ExitInfo

	finishOnce   sync.Once
	closeCalls   atomic.Int32
	dataCalls    atomic.Int32
	exitCalls    atomic.Int32
	writeCalls   atomic.Int32
	closeErrors  []error
	closeStarted chan struct{}
	closeRelease <-chan struct{}
}

func newControlledTerminalHandle(closeErrors ...error) *controlledTerminalHandle {
	return &controlledTerminalHandle{
		data:        make(chan []byte, 1),
		exit:        make(chan pty.ExitInfo, 1),
		closeErrors: closeErrors,
	}
}

func (h *controlledTerminalHandle) Write(p []byte) (int, error) {
	h.writeCalls.Add(1)
	return len(p), nil
}

func (h *controlledTerminalHandle) Resize(_, _ uint16) error { return nil }

func (h *controlledTerminalHandle) Close() error {
	call := int(h.closeCalls.Add(1))
	if h.closeStarted != nil {
		select {
		case h.closeStarted <- struct{}{}:
		default:
		}
	}
	if h.closeRelease != nil {
		<-h.closeRelease
	}
	if call <= len(h.closeErrors) {
		return h.closeErrors[call-1]
	}
	return nil
}

func (h *controlledTerminalHandle) Data() <-chan []byte {
	h.dataCalls.Add(1)
	return h.data
}

func (h *controlledTerminalHandle) Exit() <-chan pty.ExitInfo {
	h.exitCalls.Add(1)
	return h.exit
}

func (h *controlledTerminalHandle) finish(info pty.ExitInfo) {
	h.finishOnce.Do(func() {
		h.exit <- info
		close(h.exit)
		close(h.data)
	})
}

type nonComparableTerminalHandle struct {
	state  *controlledTerminalHandle
	marker []byte
}

func (h nonComparableTerminalHandle) Write(p []byte) (int, error) {
	return h.state.Write(p)
}

func (h nonComparableTerminalHandle) Resize(cols, rows uint16) error {
	return h.state.Resize(cols, rows)
}

func (h nonComparableTerminalHandle) Close() error { return h.state.Close() }
func (h nonComparableTerminalHandle) Data() <-chan []byte {
	return h.state.Data()
}
func (h nonComparableTerminalHandle) Exit() <-chan pty.ExitInfo {
	return h.state.Exit()
}

type terminalOpenOutcome struct {
	result protocol.TerminalOpenResult
	err    error
}

func openTerminalAsync(h *handlers.TerminalHandlers, params protocol.TerminalOpenParams) <-chan terminalOpenOutcome {
	out := make(chan terminalOpenOutcome, 1)
	go func() {
		result, err := h.Open(context.Background(), params)
		out <- terminalOpenOutcome{result: result, err: err}
	}()
	return out
}

func receiveTerminalTestValue[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminal test event")
		var zero T
		return zero
	}
}

func requireTerminalContextCanceled(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("pending terminal context was not canceled")
	}
}

func terminalHandleSequence(handles ...handlers.PTYHandle) terminalBackendFunc {
	var next atomic.Int32
	return func(context.Context, pty.Spec) (handlers.PTYHandle, error) {
		index := int(next.Add(1)) - 1
		if index >= len(handles) {
			return nil, fmt.Errorf("unexpected terminal open %d", index+1)
		}
		return handles[index], nil
	}
}

func requireTerminalPumpStarted(t *testing.T, handle *controlledTerminalHandle) {
	t.Helper()
	require.Eventually(t, func() bool {
		return handle.dataCalls.Load() > 0 && handle.exitCalls.Load() > 0
	}, time.Second, time.Millisecond)
}

func requireTerminalRemainsWritable(
	t *testing.T,
	h *handlers.TerminalHandlers,
	id string,
	duration time.Duration,
) {
	t.Helper()
	deadline := time.Now().Add(duration)
	for {
		_, err := h.Write(context.Background(), protocol.TerminalWriteParams{TerminalID: id, Data: "ping"})
		require.NoError(t, err)
		if time.Now().After(deadline) {
			return
		}
		runtime.Gosched()
	}
}

func requireNoStaleTerminalEvents(
	t *testing.T,
	recorder *recordingEmitter,
	staleData string,
	staleReason string,
) {
	t.Helper()
	for _, event := range recorder.snapshot() {
		switch payload := event.Payload.(type) {
		case handlers.TerminalDataEvent:
			require.NotEqual(t, []byte(staleData), payload.Data)
		case protocol.TerminalExitEvent:
			require.NotEqual(t, staleReason, payload.Reason)
		}
	}
}

func waitForTerminalExitReason(t *testing.T, recorder *recordingEmitter, reason string) {
	t.Helper()
	require.Eventually(t, func() bool {
		for _, event := range recorder.snapshot() {
			payload, ok := event.Payload.(protocol.TerminalExitEvent)
			if ok && payload.Reason == reason {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
}

func TestTerminalOpen_GivenSuppliedIDAndFastBackendWhenOpenedWithoutSubscribersThenResultUsesSuppliedID(t *testing.T) {
	handle := newTrackedTerminalHandle()
	var calls atomic.Int32
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(context.Context, pty.Spec) (handlers.PTYHandle, error) {
		calls.Add(1)
		return handle, nil
	}), &recordingEmitter{})
	t.Cleanup(h.CloseAll)

	result, err := h.Open(context.Background(), protocol.TerminalOpenParams{
		TerminalID: "desktop-terminal-1",
		Cols:       80,
		Rows:       24,
	})

	require.NoError(t, err)
	require.Equal(t, "desktop-terminal-1", result.TerminalID)
	require.Equal(t, int32(1), calls.Load())
}

func TestTerminalOpen_GivenUnsafeOrOversizedSuppliedIDWhenOpenedThenRejectsBeforeBackend(t *testing.T) {
	var calls atomic.Int32
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(context.Context, pty.Spec) (handlers.PTYHandle, error) {
		calls.Add(1)
		return newTrackedTerminalHandle(), nil
	}), &recordingEmitter{})

	for _, id := range []string{"has space", "bad/slash", "-missing-prefix", strings.Repeat("a", 129)} {
		_, err := h.Open(context.Background(), protocol.TerminalOpenParams{TerminalID: id, Cols: 80, Rows: 24})
		require.ErrorIsf(t, err, handlers.ErrTerminalIDInvalid, "id=%q", id)
	}
	require.Equal(t, int32(0), calls.Load())

	result, err := h.Open(context.Background(), protocol.TerminalOpenParams{
		TerminalID: strings.Repeat("a", 128),
		Cols:       80,
		Rows:       24,
	})
	require.NoError(t, err)
	require.Len(t, result.TerminalID, 128)
	h.CloseAll()
}

func TestTerminalClose_GivenRegisteredPendingOpenWhenCancellationRequestedThenCancelsIdempotently(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(ctx context.Context, _ pty.Spec) (handlers.PTYHandle, error) {
		started <- ctx
		<-release
		return nil, ctx.Err()
	}), &recordingEmitter{})
	outcome := openTerminalAsync(h, protocol.TerminalOpenParams{TerminalID: "pending-cancel-1", Cols: 80, Rows: 24})
	openCtx := receiveTerminalTestValue(t, started)

	params := protocol.TerminalCloseParams{TerminalID: "pending-cancel-1", CancelPendingOpen: true}
	_, err := h.Close(context.Background(), params)
	require.NoError(t, err)
	_, err = h.Close(context.Background(), params)
	require.NoError(t, err)
	requireTerminalContextCanceled(t, openCtx)
	close(release)

	result := receiveTerminalTestValue(t, outcome)
	require.ErrorIs(t, result.err, handlers.ErrTerminalOpenCanceled)
}

func TestTerminalClose_GivenCancellationDispatchedBeforeOpenWhenMatchingOpenArrivesThenBackendIsNotCalled(t *testing.T) {
	var calls atomic.Int32
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(context.Context, pty.Spec) (handlers.PTYHandle, error) {
		calls.Add(1)
		return newTrackedTerminalHandle(), nil
	}), &recordingEmitter{})
	params := protocol.TerminalCloseParams{TerminalID: "close-before-open-1", CancelPendingOpen: true}

	_, err := h.Close(context.Background(), params)
	require.NoError(t, err)
	_, err = h.Close(context.Background(), params)
	require.NoError(t, err)
	_, err = h.Open(context.Background(), protocol.TerminalOpenParams{
		TerminalID: "close-before-open-1",
		Cols:       80,
		Rows:       24,
	})

	require.ErrorIs(t, err, handlers.ErrTerminalOpenCanceled)
	require.Equal(t, int32(0), calls.Load())
}

func TestTerminalOpen_GivenCanceledBackendIgnoresContextWhenHandleReturnsLateThenClosesWithoutPumpOrEvents(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	handle := newTrackedTerminalHandle()
	recorder := &recordingEmitter{}
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(ctx context.Context, _ pty.Spec) (handlers.PTYHandle, error) {
		started <- ctx
		<-release
		return handle, nil
	}), recorder)
	outcome := openTerminalAsync(h, protocol.TerminalOpenParams{TerminalID: "late-handle-1", Cols: 80, Rows: 24})
	openCtx := receiveTerminalTestValue(t, started)

	_, err := h.Close(context.Background(), protocol.TerminalCloseParams{
		TerminalID:        "late-handle-1",
		CancelPendingOpen: true,
	})
	require.NoError(t, err)
	requireTerminalContextCanceled(t, openCtx)
	close(release)

	result := receiveTerminalTestValue(t, outcome)
	require.ErrorIs(t, result.err, handlers.ErrTerminalOpenCanceled)
	require.Equal(t, int32(1), handle.closeCalls.Load())
	require.Zero(t, handle.dataCalls.Load())
	require.Zero(t, handle.exitCalls.Load())
	require.Empty(t, recorder.snapshot())
}

func TestTerminalOpen_GivenDuplicatePendingIDWhenOpenedThenDoesNotOverwriteFirstAttempt(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	firstHandle := newTrackedTerminalHandle()
	var calls atomic.Int32
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(context.Context, pty.Spec) (handlers.PTYHandle, error) {
		if calls.Add(1) == 1 {
			started <- struct{}{}
			<-release
			return firstHandle, nil
		}
		return newTrackedTerminalHandle(), nil
	}), &recordingEmitter{})
	first := openTerminalAsync(h, protocol.TerminalOpenParams{TerminalID: "duplicate-pending-1", Cols: 80, Rows: 24})
	receiveTerminalTestValue(t, started)

	_, err := h.Open(context.Background(), protocol.TerminalOpenParams{
		TerminalID: "duplicate-pending-1",
		Cols:       80,
		Rows:       24,
	})

	require.ErrorIs(t, err, handlers.ErrTerminalIDInUse)
	require.Equal(t, int32(1), calls.Load())
	_, err = h.Close(context.Background(), protocol.TerminalCloseParams{
		TerminalID:        "duplicate-pending-1",
		CancelPendingOpen: true,
	})
	require.NoError(t, err)
	close(release)
	require.ErrorIs(t, receiveTerminalTestValue(t, first).err, handlers.ErrTerminalOpenCanceled)
}

func TestTerminalOpen_GivenDuplicateActiveIDWhenOpenedThenDoesNotOverwriteLiveHandle(t *testing.T) {
	var calls atomic.Int32
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(context.Context, pty.Spec) (handlers.PTYHandle, error) {
		calls.Add(1)
		return newTrackedTerminalHandle(), nil
	}), &recordingEmitter{})
	t.Cleanup(h.CloseAll)
	params := protocol.TerminalOpenParams{TerminalID: "duplicate-active-1", Cols: 80, Rows: 24}
	_, err := h.Open(context.Background(), params)
	require.NoError(t, err)

	_, err = h.Open(context.Background(), params)

	require.ErrorIs(t, err, handlers.ErrTerminalIDInUse)
	require.Equal(t, int32(1), calls.Load())
}

func TestTerminalClose_GivenSuccessfulCloseAndNonComparableHandleWhenSameIDReopensThenOldPumpCannotAffectReplacement(t *testing.T) {
	const id = "stable-reopen-1"
	oldHandle := newControlledTerminalHandle()
	replacement := newControlledTerminalHandle()
	recorder := &recordingEmitter{}
	h := handlers.NewTerminalHandlers(terminalHandleSequence(
		nonComparableTerminalHandle{state: oldHandle, marker: []byte("identity")},
		replacement,
	), recorder)
	t.Cleanup(func() {
		oldHandle.finish(pty.ExitInfo{Reason: "cleanup-old"})
		replacement.finish(pty.ExitInfo{Reason: "cleanup-replacement"})
		h.CloseAll()
	})

	_, err := h.Open(context.Background(), protocol.TerminalOpenParams{TerminalID: id, Cols: 80, Rows: 24})
	require.NoError(t, err)
	requireTerminalPumpStarted(t, oldHandle)

	_, err = h.Close(context.Background(), protocol.TerminalCloseParams{TerminalID: id})
	require.NoError(t, err)
	result, err := h.Open(context.Background(), protocol.TerminalOpenParams{TerminalID: id, Cols: 80, Rows: 24})
	require.NoError(t, err)
	require.Equal(t, id, result.TerminalID)
	requireTerminalPumpStarted(t, replacement)

	oldHandle.data <- []byte("stale-old-data")
	oldHandle.finish(pty.ExitInfo{Code: 137, Reason: "stale-old-exit"})
	require.Eventually(t, func() bool { return len(oldHandle.exit) == 0 }, time.Second, time.Millisecond)
	requireTerminalRemainsWritable(t, h, id, 50*time.Millisecond)
	requireNoStaleTerminalEvents(t, recorder, "stale-old-data", "stale-old-exit")

	replacement.finish(pty.ExitInfo{Code: 0, Reason: "replacement-exit"})
	waitForTerminalExitReason(t, recorder, "replacement-exit")
	requireNoStaleTerminalEvents(t, recorder, "stale-old-data", "stale-old-exit")
}

func TestTerminalClose_GivenHandleCloseFailsWhenRetriedThenSameEntryStaysOccupiedUntilSuccess(t *testing.T) {
	const id = "stable-close-retry-1"
	closeErr := errors.New("close failed")
	oldHandle := newControlledTerminalHandle(closeErr, nil)
	replacement := newControlledTerminalHandle()
	var backendCalls atomic.Int32
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(context.Context, pty.Spec) (handlers.PTYHandle, error) {
		if backendCalls.Add(1) == 1 {
			return oldHandle, nil
		}
		return replacement, nil
	}), &recordingEmitter{})
	t.Cleanup(func() {
		oldHandle.finish(pty.ExitInfo{Reason: "cleanup-old"})
		replacement.finish(pty.ExitInfo{Reason: "cleanup-replacement"})
		h.CloseAll()
	})

	_, err := h.Open(context.Background(), protocol.TerminalOpenParams{TerminalID: id, Cols: 80, Rows: 24})
	require.NoError(t, err)
	requireTerminalPumpStarted(t, oldHandle)

	_, err = h.Close(context.Background(), protocol.TerminalCloseParams{TerminalID: id})
	require.ErrorIs(t, err, closeErr)
	_, err = h.Open(context.Background(), protocol.TerminalOpenParams{TerminalID: id, Cols: 80, Rows: 24})
	require.ErrorIs(t, err, handlers.ErrTerminalIDInUse)
	require.Equal(t, int32(1), backendCalls.Load())
	_, err = h.Write(context.Background(), protocol.TerminalWriteParams{TerminalID: id, Data: "retryable"})
	require.NoError(t, err)
	require.Equal(t, int32(1), oldHandle.writeCalls.Load())
	require.Zero(t, replacement.writeCalls.Load())

	_, err = h.Close(context.Background(), protocol.TerminalCloseParams{TerminalID: id})
	require.NoError(t, err)
	_, err = h.Open(context.Background(), protocol.TerminalOpenParams{TerminalID: id, Cols: 80, Rows: 24})
	require.NoError(t, err)
	requireTerminalPumpStarted(t, replacement)
	oldHandle.finish(pty.ExitInfo{Code: 137, Reason: "closed-old"})
	requireTerminalRemainsWritable(t, h, id, 25*time.Millisecond)

	replacement.finish(pty.ExitInfo{Code: 0, Reason: "replacement-after-retry"})
}

func TestTerminalClose_GivenCloseReopenAndPumpCleanupRaceThenReplacementKeepsStableIDOwnership(t *testing.T) {
	const iterations = 64
	for i := 0; i < iterations; i++ {
		id := fmt.Sprintf("stable-race-%03d", i)
		closeStarted := make(chan struct{}, 1)
		closeRelease := make(chan struct{})
		oldHandle := newControlledTerminalHandle()
		oldHandle.closeStarted = closeStarted
		oldHandle.closeRelease = closeRelease
		replacement := newControlledTerminalHandle()
		recorder := &recordingEmitter{}
		h := handlers.NewTerminalHandlers(terminalHandleSequence(oldHandle, replacement), recorder)

		_, err := h.Open(context.Background(), protocol.TerminalOpenParams{TerminalID: id, Cols: 80, Rows: 24})
		require.NoError(t, err)
		requireTerminalPumpStarted(t, oldHandle)
		closeOutcome := make(chan error, 1)
		go func() {
			_, closeErr := h.Close(context.Background(), protocol.TerminalCloseParams{TerminalID: id})
			closeOutcome <- closeErr
		}()
		receiveTerminalTestValue(t, closeStarted)

		gate := make(chan struct{})
		oldFinished := make(chan struct{})
		go func() {
			<-gate
			oldHandle.finish(pty.ExitInfo{Code: 137, Reason: fmt.Sprintf("old-%03d", i)})
			close(oldFinished)
		}()
		go func() {
			<-gate
			close(closeRelease)
		}()
		close(gate)

		require.NoError(t, receiveTerminalTestValue(t, closeOutcome))
		receiveTerminalTestValue(t, oldFinished)
		_, err = h.Open(context.Background(), protocol.TerminalOpenParams{TerminalID: id, Cols: 80, Rows: 24})
		require.NoErrorf(t, err, "iteration=%d", i)
		requireTerminalPumpStarted(t, replacement)
		requireTerminalRemainsWritable(t, h, id, 2*time.Millisecond)

		replacementReason := fmt.Sprintf("replacement-%03d", i)
		replacement.finish(pty.ExitInfo{Reason: replacementReason})
		waitForTerminalExitReason(t, recorder, replacementReason)
		h.CloseAll()
	}
}

func TestTerminalClose_GivenCancellationTombstonesReachCapacityThenRejectsWithoutEvictionAndRecoversAfterConsumption(t *testing.T) {
	var backendCalls atomic.Int32
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(context.Context, pty.Spec) (handlers.PTYHandle, error) {
		backendCalls.Add(1)
		return newTrackedTerminalHandle(), nil
	}), &recordingEmitter{})

	accepted := make([]string, 0, 512)
	var rejectedID string
	for i := 0; i < 2048; i++ {
		id := fmt.Sprintf("cancel-%04d", i)
		_, err := h.Close(context.Background(), protocol.TerminalCloseParams{
			TerminalID:        id,
			CancelPendingOpen: true,
		})
		if err != nil {
			require.ErrorIs(t, err, handlers.ErrTerminalCancelCapacity)
			rejectedID = id
			break
		}
		accepted = append(accepted, id)
	}
	require.NotEmpty(t, accepted)
	require.NotEmpty(t, rejectedID, "pending cancellation ownership must be bounded")

	_, err := h.Close(context.Background(), protocol.TerminalCloseParams{
		TerminalID:        accepted[0],
		CancelPendingOpen: true,
	})
	require.NoError(t, err, "repeating an owned cancellation must stay idempotent at capacity")
	_, err = h.Close(context.Background(), protocol.TerminalCloseParams{
		TerminalID:        rejectedID,
		CancelPendingOpen: true,
	})
	require.ErrorIs(t, err, handlers.ErrTerminalCancelCapacity)

	_, err = h.Open(context.Background(), protocol.TerminalOpenParams{
		TerminalID: accepted[0],
		Cols:       80,
		Rows:       24,
	})
	require.ErrorIs(t, err, handlers.ErrTerminalOpenCanceled)
	require.Equal(t, int32(0), backendCalls.Load(), "a live tombstone must not have been evicted")

	_, err = h.Close(context.Background(), protocol.TerminalCloseParams{
		TerminalID:        rejectedID,
		CancelPendingOpen: true,
	})
	require.NoError(t, err, "caller retry must succeed after a tombstone is consumed")
}

func TestTerminalCloseAll_GivenPendingOpenReturnsAfterDisconnectThenClosesLateHandleAndRejectsNewClaims(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	handle := newTrackedTerminalHandle()
	var calls atomic.Int32
	recorder := &recordingEmitter{}
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(ctx context.Context, _ pty.Spec) (handlers.PTYHandle, error) {
		calls.Add(1)
		started <- ctx
		<-release
		return handle, nil
	}), recorder)
	outcome := openTerminalAsync(h, protocol.TerminalOpenParams{TerminalID: "disconnect-pending-1", Cols: 80, Rows: 24})
	openCtx := receiveTerminalTestValue(t, started)

	h.CloseAll()
	requireTerminalContextCanceled(t, openCtx)
	close(release)

	result := receiveTerminalTestValue(t, outcome)
	require.ErrorIs(t, result.err, handlers.ErrTerminalHandlerClosed)
	require.Equal(t, int32(1), handle.closeCalls.Load())
	require.Zero(t, handle.dataCalls.Load())
	require.Zero(t, handle.exitCalls.Load())
	require.Empty(t, recorder.snapshot())

	_, err := h.Open(context.Background(), protocol.TerminalOpenParams{
		TerminalID: "after-disconnect-1",
		Cols:       80,
		Rows:       24,
	})
	require.ErrorIs(t, err, handlers.ErrTerminalHandlerClosed)
	require.Equal(t, int32(1), calls.Load())
}

func TestTerminalCloseAll_GivenOpenCompletionRacesDisconnectThenNoHandleSurvives(t *testing.T) {
	const iterations = 100
	for i := 0; i < iterations; i++ {
		handle := newTrackedTerminalHandle()
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		h := handlers.NewTerminalHandlers(terminalBackendFunc(func(context.Context, pty.Spec) (handlers.PTYHandle, error) {
			started <- struct{}{}
			<-release
			return handle, nil
		}), &recordingEmitter{})
		id := fmt.Sprintf("disconnect-race-%03d", i)
		outcome := openTerminalAsync(h, protocol.TerminalOpenParams{TerminalID: id, Cols: 80, Rows: 24})
		receiveTerminalTestValue(t, started)

		gate := make(chan struct{})
		closeAllDone := make(chan struct{})
		go func() {
			<-gate
			h.CloseAll()
			close(closeAllDone)
		}()
		go func() {
			<-gate
			close(release)
		}()
		close(gate)

		result := receiveTerminalTestValue(t, outcome)
		receiveTerminalTestValue(t, closeAllDone)
		if result.err != nil {
			require.ErrorIsf(t, result.err, handlers.ErrTerminalHandlerClosed, "iteration=%d", i)
		}
		require.Equalf(t, int32(1), handle.closeCalls.Load(), "iteration=%d", i)
		_, err := h.Write(context.Background(), protocol.TerminalWriteParams{TerminalID: id})
		require.ErrorIsf(t, err, handlers.ErrTerminalNotFound, "iteration=%d", i)
	}
}
