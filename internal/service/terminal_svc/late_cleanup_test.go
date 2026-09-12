package terminal_svc_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
)

const (
	sensitiveLateCleanupCommand = "deploy --token=late-cleanup-secret"
	sensitiveLateCleanupCwd     = "/private/late-cleanup-cwd"
	sensitiveLateCleanupOutput  = "late-cleanup-sensitive-output"
)

var errSensitiveLateCleanupClose = errors.New("late cleanup close failed: token=sensitive")

type lateCleanupCommandHandle struct {
	data chan []byte
	exit chan pty.ExitInfo

	closeCalls  atomic.Int32
	dataCalls   atomic.Int32
	exitCalls   atomic.Int32
	dataStarted chan struct{}
	dataOnce    sync.Once
	finishOnce  sync.Once
	mu          sync.Mutex
	closeOwners []*lateCleanupCommandHandle
}

func newLateCleanupCommandHandle() *lateCleanupCommandHandle {
	return &lateCleanupCommandHandle{
		data:        make(chan []byte),
		exit:        make(chan pty.ExitInfo, 1),
		dataStarted: make(chan struct{}),
	}
}

func (h *lateCleanupCommandHandle) Write(p []byte) (int, error) { return len(p), nil }
func (h *lateCleanupCommandHandle) Resize(uint16, uint16) error { return nil }
func (h *lateCleanupCommandHandle) Data() <-chan []byte {
	h.dataCalls.Add(1)
	h.dataOnce.Do(func() { close(h.dataStarted) })
	return h.data
}
func (h *lateCleanupCommandHandle) Exit() <-chan pty.ExitInfo {
	h.exitCalls.Add(1)
	return h.exit
}
func (h *lateCleanupCommandHandle) Close() error {
	h.mu.Lock()
	h.closeOwners = append(h.closeOwners, h)
	h.mu.Unlock()
	if h.closeCalls.Add(1) <= 2 {
		return errSensitiveLateCleanupClose
	}
	return nil
}

func (h *lateCleanupCommandHandle) finish() {
	h.finishOnce.Do(func() {
		h.exit <- pty.ExitInfo{Reason: "testCleanup"}
		close(h.exit)
		close(h.data)
	})
}

func (h *lateCleanupCommandHandle) retainedSamePointer() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.closeOwners) == 0 {
		return false
	}
	for _, owner := range h.closeOwners {
		if owner != h {
			return false
		}
	}
	return true
}

type cancellationIgnoringCommandBackend struct {
	handle  pty.Handle
	started chan context.Context
	release chan struct{}
}

func (b *cancellationIgnoringCommandBackend) Open(ctx context.Context, _ pty.Spec) (pty.Handle, error) {
	b.started <- ctx
	<-b.release
	return b.handle, nil
}

func TestService_RunCommand_GivenPreemptedLateHandleInitialCloseFails_WhenGuardianRetrySucceeds_ThenRetainsPointerWithoutEventsAndLogsRedactedLifecycle(t *testing.T) {
	handle := newLateCleanupCommandHandle()
	t.Cleanup(handle.finish)
	backend := &cancellationIgnoringCommandBackend{
		handle:  handle,
		started: make(chan context.Context, 1),
		release: make(chan struct{}),
	}
	emitter := &recordingEmitter{}
	pickedDevice := make(chan string, 1)
	svc := terminal_svc.NewService(
		terminal_svc.NewBackendSelector(nil, func(deviceID string) (terminal_svc.PTYBackend, error) {
			pickedDevice <- deviceID
			return backend, nil
		}),
		emitter,
	)
	svc.SetCommandScopeResolver(func(
		context.Context,
		terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		return &terminal_svc.CommandScope{
			DeviceID: "device-late-cleanup",
			Cwd:      sensitiveLateCleanupCwd,
		}, nil
	})
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	type result struct {
		response *terminal_svc.RunCommandResponse
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		response, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
			TerminalID: "terminal-late-cleanup",
			SessionID:  501,
			Command:    sensitiveLateCleanupCommand,
			Cols:       80,
			Rows:       24,
		})
		resultCh <- result{response: response, err: err}
	}()

	openCtx := <-backend.started
	require.NoError(t, svc.Close(context.Background(), "terminal-late-cleanup"))
	require.ErrorIs(t, openCtx.Err(), context.Canceled)
	close(backend.release)
	openResult := <-resultCh

	require.NoError(t, openResult.err)
	require.NotNil(t, openResult.response)
	require.Equal(t, terminal_svc.ErrCommandStartPreempted.Error(), openResult.response.StartError)
	require.Equal(t, "device-late-cleanup", <-pickedDevice)
	select {
	case <-handle.dataStarted:
	case <-time.After(time.Second):
		t.Fatal("late-handle guardian did not retain the handle and claim its data drain")
	}
	dataDrained := make(chan struct{})
	go func() {
		handle.data <- []byte(sensitiveLateCleanupOutput)
		close(dataDrained)
	}()
	select {
	case <-dataDrained:
	case <-time.After(time.Second):
		t.Fatal("late-handle guardian did not drain discarded output")
	}

	require.Eventually(t, func() bool {
		return handle.closeCalls.Load() == 3 && logs.Len() == 2
	}, time.Second, time.Millisecond)
	handle.finish()

	require.True(t, handle.retainedSamePointer(), "initial close and guardian retries must retain the returned handle pointer")
	require.Equal(t, int32(1), handle.dataCalls.Load())
	require.Equal(t, int32(1), handle.exitCalls.Load())
	require.Empty(t, emitter.Snapshot(), "preempted late handle must not emit Wails terminal events")
	require.Zero(t, logs.FilterMessage("terminal_svc.RunCommand: command started").Len())
	require.Zero(t, logs.FilterMessage("terminal_svc.RunCommand: command exited").Len())
	require.ErrorIs(t, svc.Write(context.Background(), "terminal-late-cleanup", "x"), terminal_svc.ErrTerminalClosed)

	entries := logs.All()
	require.Equal(t, zapcore.WarnLevel, entries[0].Level)
	require.Equal(t, "terminal_svc.open: detached cleanup guardian started", entries[0].Message)
	require.Equal(t, map[string]any{
		"terminalId":  "terminal-late-cleanup",
		"deviceId":    "device-late-cleanup",
		"cleanupKind": "preemptedOpen",
	}, entries[0].ContextMap())
	require.Equal(t, zapcore.InfoLevel, entries[1].Level)
	require.Equal(t, "terminal_svc.open: detached cleanup guardian settled", entries[1].Message)
	require.Equal(t, map[string]any{
		"terminalId":  "terminal-late-cleanup",
		"deviceId":    "device-late-cleanup",
		"cleanupKind": "preemptedOpen",
		"outcome":     "closeSucceeded",
	}, entries[1].ContextMap())
	encodedLogs, err := json.Marshal(entries)
	require.NoError(t, err)
	for _, sensitive := range []string{
		sensitiveLateCleanupCommand,
		sensitiveLateCleanupCwd,
		sensitiveLateCleanupOutput,
		errSensitiveLateCleanupClose.Error(),
	} {
		require.NotContains(t, string(encodedLogs), sensitive)
	}
}
