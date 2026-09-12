package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"
)

const (
	sensitiveDaemonLateCommand = "deploy --token=daemon-late-secret"
	sensitiveDaemonLateCwd     = "/private/daemon-late-cwd"
	sensitiveDaemonLateOutput  = "daemon-late-sensitive-output"
)

var errSensitiveDaemonLateClose = errors.New("daemon late close failed: token=sensitive")

func openTerminalAsyncWithContext(
	ctx context.Context,
	h *handlers.TerminalHandlers,
	params protocol.TerminalOpenParams,
) <-chan terminalOpenOutcome {
	out := make(chan terminalOpenOutcome, 1)
	go func() {
		result, err := h.Open(ctx, params)
		out <- terminalOpenOutcome{result: result, err: err}
	}()
	return out
}

func repeatedCloseErrors(count int) []error {
	errs := make([]error, count)
	for i := range errs {
		errs[i] = errSensitiveDaemonLateClose
	}
	return errs
}

func requireDaemonLateCleanupLogs(
	t *testing.T,
	logs *observer.ObservedLogs,
	terminalID string,
	outcome string,
) {
	t.Helper()
	entries := logs.All()
	require.Len(t, entries, 2)
	require.Equal(t, zapcore.WarnLevel, entries[0].Level)
	require.Equal(t, "handlers.TerminalHandlers.Open: detached cleanup guardian started", entries[0].Message)
	require.Equal(t, map[string]any{
		"terminalId":  terminalID,
		"cleanupKind": "lateOpen",
	}, entries[0].ContextMap())
	require.Equal(t, zapcore.InfoLevel, entries[1].Level)
	require.Equal(t, "handlers.TerminalHandlers.Open: detached cleanup guardian settled", entries[1].Message)
	require.Equal(t, map[string]any{
		"terminalId":  terminalID,
		"cleanupKind": "lateOpen",
		"outcome":     outcome,
	}, entries[1].ContextMap())

	encodedLogs, err := json.Marshal(entries)
	require.NoError(t, err)
	for _, sensitive := range []string{
		sensitiveDaemonLateCommand,
		sensitiveDaemonLateCwd,
		sensitiveDaemonLateOutput,
		errSensitiveDaemonLateClose.Error(),
	} {
		require.NotContains(t, string(encodedLogs), sensitive)
	}
}

func TestTerminalOpen_GivenCanceledLateHandleInitialCloseFails_WhenGuardianRetrySucceeds_ThenNoRegistrationPumpOrEvents(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	handle := newControlledTerminalHandle(repeatedCloseErrors(4)...)
	t.Cleanup(func() { handle.finish(pty.ExitInfo{Reason: "testCleanup"}) })
	recorder := &recordingEmitter{}
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(ctx context.Context, _ pty.Spec) (handlers.PTYHandle, error) {
		started <- ctx
		<-release
		return handle, nil
	}), recorder)
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))
	outcome := openTerminalAsyncWithContext(ctx, h, protocol.TerminalOpenParams{
		TerminalID: "daemon-late-retry",
		Cwd:        sensitiveDaemonLateCwd,
		Command:    sensitiveDaemonLateCommand,
		Cols:       80,
		Rows:       24,
	})
	openCtx := receiveTerminalTestValue(t, started)

	_, err := h.Close(context.Background(), protocol.TerminalCloseParams{
		TerminalID:        "daemon-late-retry",
		CancelPendingOpen: true,
	})
	require.NoError(t, err)
	requireTerminalContextCanceled(t, openCtx)
	close(release)

	result := receiveTerminalTestValue(t, outcome)
	require.ErrorIs(t, result.err, handlers.ErrTerminalOpenCanceled)
	require.Eventually(t, func() bool {
		return handle.dataCalls.Load() == 1 && handle.exitCalls.Load() == 1
	}, time.Second, time.Millisecond, "late-handle guardian did not retain the returned handle")
	handle.data <- []byte(sensitiveDaemonLateOutput)
	require.Eventually(t, func() bool { return len(handle.data) == 0 }, time.Second, time.Millisecond,
		"late-handle guardian did not drain discarded data")
	require.Eventually(t, func() bool {
		return handle.closeCalls.Load() == 5 && logs.Len() == 2
	}, time.Second, time.Millisecond)
	handle.finish(pty.ExitInfo{Reason: "testCleanup"})

	require.Empty(t, recorder.snapshot(), "late canceled handle must not emit terminal events")
	_, err = h.Write(context.Background(), protocol.TerminalWriteParams{TerminalID: "daemon-late-retry", Data: "x"})
	require.ErrorIs(t, err, handlers.ErrTerminalNotFound, "late handle must never enter the registry")
	requireDaemonLateCleanupLogs(t, logs, "daemon-late-retry", "closeSucceeded")
}

func TestTerminalOpen_GivenGeneratedIDReturnsLateAfterCloseAllAndCloseKeepsFailing_WhenHandleExitsNaturally_ThenDrainsWithoutEventsOrRegistration(t *testing.T) {
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	handle := newControlledTerminalHandle(repeatedCloseErrors(100)...)
	t.Cleanup(func() { handle.finish(pty.ExitInfo{Reason: "testCleanup"}) })
	recorder := &recordingEmitter{}
	h := handlers.NewTerminalHandlers(terminalBackendFunc(func(ctx context.Context, _ pty.Spec) (handlers.PTYHandle, error) {
		started <- ctx
		<-release
		return handle, nil
	}), recorder)
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))
	outcome := openTerminalAsyncWithContext(ctx, h, protocol.TerminalOpenParams{
		Cwd:     sensitiveDaemonLateCwd,
		Command: sensitiveDaemonLateCommand,
		Cols:    80,
		Rows:    24,
	})
	openCtx := receiveTerminalTestValue(t, started)

	h.CloseAll()
	requireTerminalContextCanceled(t, openCtx)
	close(release)

	result := receiveTerminalTestValue(t, outcome)
	require.ErrorIs(t, result.err, handlers.ErrTerminalHandlerClosed)
	require.Eventually(t, func() bool {
		return handle.dataCalls.Load() == 1 && handle.exitCalls.Load() == 1
	}, time.Second, time.Millisecond, "late-handle guardian did not retain the generated-ID handle")
	handle.data <- []byte(sensitiveDaemonLateOutput)
	handle.finish(pty.ExitInfo{Code: 0, Reason: "natural"})
	require.Eventually(t, func() bool { return logs.Len() == 2 }, time.Second, time.Millisecond)

	entries := logs.All()
	terminalID, ok := entries[0].ContextMap()["terminalId"].(string)
	require.True(t, ok)
	require.Len(t, terminalID, 24, "generated terminal correlation ID must be the daemon's bounded hex identity")
	require.NotEqual(t, strings.Repeat("0", 24), terminalID)
	require.Empty(t, recorder.snapshot(), "natural late cleanup must discard data without terminal events")
	_, err := h.Write(context.Background(), protocol.TerminalWriteParams{TerminalID: terminalID, Data: "x"})
	require.ErrorIs(t, err, handlers.ErrTerminalNotFound, "generated late handle must never enter the registry")
	_, err = h.Open(context.Background(), protocol.TerminalOpenParams{TerminalID: "after-close-all", Cols: 80, Rows: 24})
	require.ErrorIs(t, err, handlers.ErrTerminalHandlerClosed, "CloseAll closed/pending semantics must remain unchanged")
	requireDaemonLateCleanupLogs(t, logs, terminalID, "terminalExited")
}
