package terminal_svc_test

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc/mocks"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestService_Open_Local_RegistersHandle(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBE := mocks.NewMockPTYBackend(ctrl)
	mockH := mocks.NewMockHandle(ctrl)
	mockH.EXPECT().Data().AnyTimes().Return(make(chan []byte))
	mockH.EXPECT().Exit().AnyTimes().Return(make(chan pty.ExitInfo))
	mockBE.EXPECT().Open(gomock.Any(), pty.Spec{
		TerminalID: "t1", Cwd: "/tmp", Cols: 80, Rows: 24,
	}).Return(mockH, nil)

	sel := terminal_svc.NewBackendSelector(mockBE, func(string) (terminal_svc.PTYBackend, error) {
		t.Fatal("should not call remote factory for local")
		return nil, nil
	})
	svc := terminal_svc.NewService(sel, terminal_svc.NoopEmitter{})

	require.NoError(t, svc.Open(context.Background(), "t1", "", "/tmp", 80, 24))

	mockH.EXPECT().Write([]byte("x")).Return(1, nil)
	assert.NoError(t, svc.Write(context.Background(), "t1", "x"))
}

func TestService_Write_NoOpenTerminal(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	sel := terminal_svc.NewBackendSelector(mocks.NewMockPTYBackend(ctrl), nil)
	svc := terminal_svc.NewService(sel, terminal_svc.NoopEmitter{})
	err := svc.Write(context.Background(), "t1", "x")
	require.ErrorIs(t, err, terminal_svc.ErrTerminalClosed)
}

func TestService_Write_UnknownTerminalReturnsClosed(t *testing.T) {
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(&fakeBackend{}, nil), terminal_svc.NoopEmitter{})
	if err := svc.Write(context.Background(), "ghost", "x"); !errors.Is(err, terminal_svc.ErrTerminalClosed) {
		t.Fatalf("want ErrTerminalClosed, got %v", err)
	}
}

func TestService_Close_UnknownTerminal(t *testing.T) {
	sel := terminal_svc.NewBackendSelector(&fakeBackend{}, nil)
	svc := terminal_svc.NewService(sel, terminal_svc.NoopEmitter{})
	err := svc.Close(context.Background(), "ghost")
	require.ErrorIs(t, err, terminal_svc.ErrTerminalNotOpen)
}

type nonComparableHandleState struct {
	data          chan []byte
	exit          chan pty.ExitInfo
	settleOnClose bool
	closeCalls    atomic.Int32
	writeCalls    atomic.Int32
	settle        sync.Once
}

func newNonComparableHandleState(settleOnClose bool) *nonComparableHandleState {
	return &nonComparableHandleState{
		data:          make(chan []byte),
		exit:          make(chan pty.ExitInfo, 1),
		settleOnClose: settleOnClose,
	}
}

func (s *nonComparableHandleState) finish(info pty.ExitInfo) {
	s.settle.Do(func() {
		s.exit <- info
		close(s.exit)
		close(s.data)
	})
}

type nonComparableHandle struct {
	marker []byte
	state  *nonComparableHandleState
}

func newNonComparableHandle(state *nonComparableHandleState) pty.Handle {
	return nonComparableHandle{marker: []byte("valid but non-comparable"), state: state}
}

func (h nonComparableHandle) Write(p []byte) (int, error) {
	h.state.writeCalls.Add(1)
	return len(p), nil
}

func (h nonComparableHandle) Resize(uint16, uint16) error { return nil }
func (h nonComparableHandle) Data() <-chan []byte         { return h.state.data }
func (h nonComparableHandle) Exit() <-chan pty.ExitInfo   { return h.state.exit }
func (h nonComparableHandle) Close() error {
	h.state.closeCalls.Add(1)
	if h.state.settleOnClose {
		h.state.finish(pty.ExitInfo{Reason: "killed"})
	}
	return nil
}

type handleSequenceBackend struct {
	opens   atomic.Int32
	handles []pty.Handle
}

func (b *handleSequenceBackend) Open(context.Context, pty.Spec) (pty.Handle, error) {
	return b.handles[int(b.opens.Add(1))-1], nil
}

func TestService_GivenNonComparableHandleWhenCloseSucceedsThenItClosesWithoutPanic(t *testing.T) {
	state := newNonComparableHandleState(false)
	t.Cleanup(func() { state.finish(pty.ExitInfo{Reason: "killed"}) })
	backend := &handleSequenceBackend{handles: []pty.Handle{newNonComparableHandle(state)}}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend, nil), terminal_svc.NoopEmitter{})

	require.NoError(t, svc.Open(context.Background(), "terminal-non-comparable-close", "", "/tmp", 80, 24))
	var closeErr error
	require.NotPanics(t, func() {
		closeErr = svc.Close(context.Background(), "terminal-non-comparable-close")
	})
	require.NoError(t, closeErr)
	require.ErrorIs(t, svc.Write(context.Background(), "terminal-non-comparable-close", "x"), terminal_svc.ErrTerminalClosed)
	require.Equal(t, int32(1), state.closeCalls.Load())
}

func TestService_GivenNonComparableHandleWhenPumpExitsNaturallyThenItCleansUpWithoutPanic(t *testing.T) {
	state := newNonComparableHandleState(false)
	backend := &handleSequenceBackend{handles: []pty.Handle{newNonComparableHandle(state)}}
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend, nil), emitter)

	require.NoError(t, svc.Open(context.Background(), "terminal-non-comparable-pump", "", "/tmp", 80, 24))
	state.finish(pty.ExitInfo{Code: 0, Reason: "natural"})

	require.Eventually(t, func() bool {
		for _, event := range emitter.Snapshot() {
			if event.Name == terminal_svc.ExitEventName("terminal-non-comparable-pump") {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
	require.ErrorIs(t, svc.Write(context.Background(), "terminal-non-comparable-pump", "x"), terminal_svc.ErrTerminalClosed)
	require.Zero(t, state.closeCalls.Load())
}

type replacementRaceHandle struct {
	data              chan []byte
	exit              chan pty.ExitInfo
	blockFirstClose   bool
	settleOnClose     bool
	firstCloseStarted chan struct{}
	releaseFirstClose chan struct{}
	closeCalls        atomic.Int32
	writeCalls        atomic.Int32
	settle            sync.Once
}

func newReplacementRaceHandle(blockFirstClose bool, settleOnClose bool) *replacementRaceHandle {
	return &replacementRaceHandle{
		data:              make(chan []byte, 4),
		exit:              make(chan pty.ExitInfo, 1),
		blockFirstClose:   blockFirstClose,
		settleOnClose:     settleOnClose,
		firstCloseStarted: make(chan struct{}),
		releaseFirstClose: make(chan struct{}),
	}
}

func (h *replacementRaceHandle) Write(p []byte) (int, error) {
	h.writeCalls.Add(1)
	return len(p), nil
}

func (h *replacementRaceHandle) Resize(uint16, uint16) error { return nil }
func (h *replacementRaceHandle) Data() <-chan []byte         { return h.data }
func (h *replacementRaceHandle) Exit() <-chan pty.ExitInfo   { return h.exit }

func (h *replacementRaceHandle) Close() error {
	call := h.closeCalls.Add(1)
	if h.blockFirstClose && call == 1 {
		close(h.firstCloseStarted)
		<-h.releaseFirstClose
	}
	if h.settleOnClose {
		h.finish(pty.ExitInfo{Reason: "killed"})
	}
	return nil
}

func (h *replacementRaceHandle) finish(info pty.ExitInfo) {
	h.settle.Do(func() {
		h.exit <- info
		close(h.exit)
		close(h.data)
	})
}

type replacementRaceBackend struct {
	opens       atomic.Int32
	old         pty.Handle
	replacement pty.Handle
}

func (b *replacementRaceBackend) Open(context.Context, pty.Spec) (pty.Handle, error) {
	if b.opens.Add(1) == 1 {
		return b.old, nil
	}
	return b.replacement, nil
}

type routedRaceBackend struct {
	mu      sync.Mutex
	handles map[string][]pty.Handle
	opens   map[string]int
}

func (b *routedRaceBackend) Open(_ context.Context, spec pty.Spec) (pty.Handle, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	queue := b.handles[spec.TerminalID]
	if len(queue) == 0 {
		return nil, errors.New("no routed test handle")
	}
	b.handles[spec.TerminalID] = queue[1:]
	b.opens[spec.TerminalID]++
	return queue[0], nil
}

func (b *routedRaceBackend) openCount(terminalID string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.opens[terminalID]
}

func TestService_GivenCloseOfOldHandleBlocksReplacementThenOldRemainsValidUntilCloseAndCannotDeleteReplacement(t *testing.T) {
	old := newReplacementRaceHandle(true, false)
	replacement := newReplacementRaceHandle(false, false)
	backend := &replacementRaceBackend{old: old, replacement: replacement}
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend, nil), emitter)
	t.Cleanup(svc.Shutdown)
	t.Cleanup(func() {
		select {
		case <-old.releaseFirstClose:
		default:
			close(old.releaseFirstClose)
		}
	})

	require.NoError(t, svc.Open(context.Background(), "terminal-race", "", "/old", 80, 24))
	closeResult := make(chan error, 1)
	go func() { closeResult <- svc.Close(context.Background(), "terminal-race") }()
	<-old.firstCloseStarted

	replacementResult := make(chan error, 1)
	go func() {
		replacementResult <- svc.Open(context.Background(), "terminal-race", "", "/replacement", 80, 24)
	}()
	require.Never(t, func() bool { return backend.opens.Load() == 2 }, 100*time.Millisecond, time.Millisecond,
		"replacement backend.Open must wait for confirmed old-handle close")
	old.data <- []byte("valid-before-close-authority")
	require.Eventually(t, func() bool { return len(emitter.Snapshot()) == 1 }, time.Second, time.Millisecond)
	require.Equal(t, []byte("valid-before-close-authority"), recordedData(t, emitter.Snapshot()[0]))

	close(old.releaseFirstClose)
	require.NoError(t, <-closeResult)
	require.NoError(t, <-replacementResult)
	require.NoError(t, svc.Write(context.Background(), "terminal-race", "replacement-current"))

	old.data <- []byte("stale")
	old.finish(pty.ExitInfo{Code: 41, Reason: "stale"})
	require.Never(t, func() bool { return len(emitter.Snapshot()) != 1 }, 100*time.Millisecond, time.Millisecond,
		"the retired old pump must not emit data or exit after replacement registration")
	require.NoError(t, svc.Write(context.Background(), "terminal-race", "old-pump-finished"),
		"completion of the old pump must not delete the replacement handle")
	require.Equal(t, int32(2), replacement.writeCalls.Load())

	replacement.data <- []byte("fresh")
	replacement.finish(pty.ExitInfo{Code: 7, Reason: "replacement"})
	require.Eventually(t, func() bool { return len(emitter.Snapshot()) == 3 }, time.Second, time.Millisecond)
	events := emitter.Snapshot()
	require.Equal(t, terminal_svc.DataEventName("terminal-race"), events[1].Name)
	require.Equal(t, []byte("fresh"), recordedData(t, events[1]))
	require.Equal(t, terminal_svc.ExitEventName("terminal-race"), events[2].Name)
	require.Equal(t, protocol.TerminalExitEvent{Code: 7, Reason: "replacement"}, events[2].Payload)
}

func TestService_GivenRetirementRacesBlockedEmissionThenReplacementWaitsAndNoStaleEmissionFollows(t *testing.T) {
	old := newReplacementRaceHandle(false, false)
	replacement := newReplacementRaceHandle(false, false)
	distinct := newReplacementRaceHandle(false, false)
	backend := &routedRaceBackend{
		handles: map[string][]pty.Handle{
			"terminal-gate-race": {old, replacement},
			"terminal-distinct":  {distinct},
		},
		opens: map[string]int{},
	}
	emitter := newBlockingRecordingEmitter()
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend, nil), emitter)
	t.Cleanup(svc.Shutdown)
	t.Cleanup(emitter.unblock)

	require.NoError(t, svc.Open(context.Background(), "terminal-gate-race", "", "/old", 80, 24))
	old.data <- []byte("in-progress")
	<-emitter.blocked

	distinctResult := make(chan error, 1)
	go func() {
		distinctResult <- svc.Open(
			context.Background(), "terminal-distinct", "", "/distinct", 80, 24,
		)
	}()
	select {
	case err := <-distinctResult:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("a blocked emission for one terminal ID held the service lock across an unrelated Open")
	}

	replacementResult := make(chan error, 1)
	go func() {
		replacementResult <- svc.Open(
			context.Background(), "terminal-gate-race", "", "/replacement", 80, 24,
		)
	}()
	require.Never(t, func() bool { return backend.openCount("terminal-gate-race") == 2 }, 100*time.Millisecond, time.Millisecond,
		"replacement backend.Open must wait for the old in-progress emission boundary")

	emitter.unblock()
	require.NoError(t, <-replacementResult)
	require.Equal(t, 2, backend.openCount("terminal-gate-race"))
	require.NoError(t, svc.Close(context.Background(), "terminal-distinct"))
	distinct.finish(pty.ExitInfo{Reason: "closed"})

	old.data <- []byte("post-replacement-stale")
	old.finish(pty.ExitInfo{Code: 42, Reason: "stale"})
	replacement.data <- []byte("replacement")
	replacement.finish(pty.ExitInfo{Code: 0, Reason: "natural"})
	require.Eventually(t, func() bool { return len(emitter.Snapshot()) == 3 }, time.Second, time.Millisecond)
	require.Never(t, func() bool { return len(emitter.Snapshot()) > 3 }, 100*time.Millisecond, time.Millisecond)

	events := emitter.Snapshot()
	require.Equal(t, []byte("in-progress"), recordedData(t, events[0]))
	require.Equal(t, []byte("replacement"), recordedData(t, events[1]))
	require.Equal(t, protocol.TerminalExitEvent{Code: 0, Reason: "natural"}, events[2].Payload)
}

func recordedData(t *testing.T, event recordedEvent) []byte {
	t.Helper()
	payload, ok := event.Payload.(map[string]string)
	require.Truef(t, ok, "data payload should be map[string]string, got %T", event.Payload)
	data, err := base64.StdEncoding.DecodeString(payload["data"])
	require.NoError(t, err)
	return data
}

func recordedDataEquals(event recordedEvent, want string) bool {
	payload, ok := event.Payload.(map[string]string)
	if !ok {
		return false
	}
	data, err := base64.StdEncoding.DecodeString(payload["data"])
	return err == nil && string(data) == want
}

type blockingRecordingEmitter struct {
	recordingEmitter
	blocked     chan struct{}
	release     chan struct{}
	blockOnce   sync.Once
	releaseOnce sync.Once
}

func newBlockingRecordingEmitter() *blockingRecordingEmitter {
	return &blockingRecordingEmitter{blocked: make(chan struct{}), release: make(chan struct{})}
}

func (e *blockingRecordingEmitter) Emit(ctx context.Context, name string, payload any) {
	e.blockOnce.Do(func() {
		close(e.blocked)
		<-e.release
	})
	e.recordingEmitter.Emit(ctx, name, payload)
}

func (e *blockingRecordingEmitter) unblock() {
	e.releaseOnce.Do(func() { close(e.release) })
}

func TestService_GivenReplacementCloseFailsWhenOpeningSameIDThenReturnsErrorAndRetainsOldForRetry(t *testing.T) {
	closeErr := errors.New("replacement close failed")
	old := newScriptedCloseCommandHandle(closeErr, nil)
	replacement := newReplacementRaceHandle(false, false)
	backend := &handleSequenceBackend{handles: []pty.Handle{old, replacement}}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend, nil), terminal_svc.NoopEmitter{})
	t.Cleanup(func() {
		old.finish(pty.ExitInfo{Reason: "closed"})
		replacement.finish(pty.ExitInfo{Reason: "closed"})
	})

	require.NoError(t, svc.Open(context.Background(), "terminal-open-close-failure", "", "/old", 80, 24))
	require.ErrorIs(t,
		svc.Open(context.Background(), "terminal-open-close-failure", "", "/replacement", 80, 24),
		closeErr,
	)
	require.Equal(t, int32(1), backend.opens.Load())
	require.NoError(t, svc.Write(context.Background(), "terminal-open-close-failure", "old-current"))

	require.NoError(t, svc.Open(context.Background(), "terminal-open-close-failure", "", "/replacement", 80, 24))
	require.Equal(t, int32(2), backend.opens.Load())
	require.NoError(t, svc.Write(context.Background(), "terminal-open-close-failure", "replacement-current"))
	require.Equal(t, int32(2), old.closeCalls.Load())
}

func TestService_Open_ReOpenClosesPrevious(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockBE := mocks.NewMockPTYBackend(ctrl)
	first := mocks.NewMockHandle(ctrl)
	second := mocks.NewMockHandle(ctrl)
	first.EXPECT().Data().AnyTimes().Return(make(chan []byte))
	first.EXPECT().Exit().AnyTimes().Return(make(chan pty.ExitInfo))
	second.EXPECT().Data().AnyTimes().Return(make(chan []byte))
	second.EXPECT().Exit().AnyTimes().Return(make(chan pty.ExitInfo))

	gomock.InOrder(
		mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).Return(first, nil),
		first.EXPECT().Close().Return(nil),
		mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).Return(second, nil),
	)

	sel := terminal_svc.NewBackendSelector(mockBE, nil)
	svc := terminal_svc.NewService(sel, terminal_svc.NoopEmitter{})

	require.NoError(t, svc.Open(context.Background(), "t1", "", "/tmp", 80, 24))
	require.NoError(t, svc.Open(context.Background(), "t1", "", "/tmp", 80, 24))
}

func TestService_Shutdown_ClosesAll(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockBE := mocks.NewMockPTYBackend(ctrl)
	mh := mocks.NewMockHandle(ctrl)
	mh.EXPECT().Data().AnyTimes().Return(make(chan []byte))
	mh.EXPECT().Exit().AnyTimes().Return(make(chan pty.ExitInfo))
	mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).Return(mh, nil)
	mh.EXPECT().Close().Return(nil)

	sel := terminal_svc.NewBackendSelector(mockBE, nil)
	svc := terminal_svc.NewService(sel, terminal_svc.NoopEmitter{})

	require.NoError(t, svc.Open(context.Background(), "t1", "", "/tmp", 80, 24))
	svc.Shutdown()
}

// TestService_Shutdown_PreemptsInFlightOpen_ClosesHandleNotRegistered covers the
// race where Shutdown runs while a backend.Open is still in flight. Shutdown must
// preempt the pending attempt so that a handle returned after Shutdown is torn
// down rather than registered into the just-cleared session map — otherwise the
// PTY (and any remote daemon-side shell) leaks past app shutdown.
func TestService_Shutdown_PreemptsInFlightOpen_ClosesHandleNotRegistered(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBE := mocks.NewMockPTYBackend(ctrl)
	mockH := mocks.NewMockHandle(ctrl)

	started := make(chan struct{})
	proceed := make(chan struct{})
	mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ pty.Spec) (pty.Handle, error) {
			close(started)
			<-proceed
			return mockH, nil // spawn succeeded despite the concurrent Shutdown
		})
	// The preempted handle must be closed and never registered; no pump should
	// start, so Data()/Exit() must NOT be consumed.
	mockH.EXPECT().Close().Return(nil)

	sel := terminal_svc.NewBackendSelector(mockBE, nil)
	svc := terminal_svc.NewService(sel, terminal_svc.NoopEmitter{})

	openErr := make(chan error, 1)
	go func() { openErr <- svc.Open(context.Background(), "t1", "", "/tmp", 80, 24) }()

	<-started
	svc.Shutdown() // preempt the in-flight Open
	close(proceed) // backend.Open now returns success
	require.NoError(t, <-openErr)

	// The handle must not be registered.
	require.ErrorIs(t, svc.Write(context.Background(), "t1", "x"), terminal_svc.ErrTerminalClosed)
}

func TestService_Open_CancelledByClose_NoLeakedHandle(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockBE := mocks.NewMockPTYBackend(ctrl)
	started := make(chan struct{})
	canceled := make(chan struct{})
	mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).DoAndReturn(
		func(openCtx context.Context, _ pty.Spec) (pty.Handle, error) {
			close(started)
			<-openCtx.Done()
			close(canceled)
			return nil, openCtx.Err()
		})

	sel := terminal_svc.NewBackendSelector(mockBE, func(string) (terminal_svc.PTYBackend, error) {
		t.Fatal("should not call remote factory for local")
		return nil, nil
	})
	svc := terminal_svc.NewService(sel, terminal_svc.NoopEmitter{})

	openErrCh := make(chan error, 1)
	go func() {
		openErrCh <- svc.Open(context.Background(), "t1", "", "/tmp", 80, 24)
	}()
	<-started
	// Now preempt via Close
	require.NoError(t, svc.Close(context.Background(), "t1"))
	<-canceled // confirm cancel actually fired
	err := <-openErrCh
	require.ErrorIs(t, err, context.Canceled)
	// Verify no handle leaked
	require.ErrorIs(t, svc.Write(context.Background(), "t1", "x"), terminal_svc.ErrTerminalClosed)
}

func TestService_OpenCommand_PassesCommandToBackend(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockBE := mocks.NewMockPTYBackend(ctrl)
	mockH := mocks.NewMockHandle(ctrl)
	mockH.EXPECT().Data().AnyTimes().Return(make(chan []byte))
	mockH.EXPECT().Exit().AnyTimes().Return(make(chan pty.ExitInfo))
	mockBE.EXPECT().
		Open(gomock.Any(), pty.Spec{
			TerminalID: "t1", Cwd: "/tmp", Command: "go test ./...", Cols: 80, Rows: 24,
		}).
		Return(mockH, nil)

	sel := terminal_svc.NewBackendSelector(mockBE, func(string) (terminal_svc.PTYBackend, error) {
		t.Fatal("should not call remote factory for local")
		return nil, nil
	})
	svc := terminal_svc.NewService(sel, terminal_svc.NoopEmitter{})

	require.NoError(t, svc.OpenCommand(context.Background(), "t1", "", "/tmp", "go test ./...", 80, 24))
}

type recordingEmitter struct {
	mu     sync.Mutex
	events []recordedEvent
}

type recordedEvent struct {
	Name    string
	Payload any
}

func (r *recordingEmitter) Emit(_ context.Context, name string, payload any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, recordedEvent{name, payload})
}

func (r *recordingEmitter) Snapshot() []recordedEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedEvent, len(r.events))
	copy(out, r.events)
	return out
}

func TestService_Pump_EmitsDataEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockBE := mocks.NewMockPTYBackend(ctrl)
	mh := mocks.NewMockHandle(ctrl)
	dataCh := make(chan []byte, 1)
	exitCh := make(chan pty.ExitInfo)
	mh.EXPECT().Data().AnyTimes().Return(dataCh)
	mh.EXPECT().Exit().AnyTimes().Return(exitCh)
	mockBE.EXPECT().Open(gomock.Any(), gomock.Any()).Return(mh, nil)

	rec := &recordingEmitter{}
	sel := terminal_svc.NewBackendSelector(mockBE, nil)
	svc := terminal_svc.NewService(sel, rec)

	require.NoError(t, svc.Open(context.Background(), "t7", "", "/tmp", 80, 24))
	dataCh <- []byte("abc")

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(rec.Snapshot()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	evs := rec.Snapshot()
	require.Len(t, evs, 1)
	assert.Equal(t, "terminal:t7:data", evs[0].Name)
}
