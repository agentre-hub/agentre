package terminal_svc_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	ptyremote "github.com/agentre-hub/agentre/internal/pkg/pty/remote"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc"
	"github.com/agentre-hub/agentre/internal/service/terminal_svc/mocks"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"
)

type completedCommandHandle struct {
	data <-chan []byte
	exit <-chan pty.ExitInfo
}

func newCompletedCommandHandle(output []byte, exitInfo pty.ExitInfo) *completedCommandHandle {
	data := make(chan []byte, 1)
	if len(output) > 0 {
		data <- output
	}
	close(data)
	exit := make(chan pty.ExitInfo, 1)
	exit <- exitInfo
	close(exit)
	return &completedCommandHandle{data: data, exit: exit}
}

func (h *completedCommandHandle) Write(p []byte) (int, error) { return len(p), nil }
func (h *completedCommandHandle) Resize(uint16, uint16) error { return nil }
func (h *completedCommandHandle) Close() error                { return nil }
func (h *completedCommandHandle) Data() <-chan []byte         { return h.data }
func (h *completedCommandHandle) Exit() <-chan pty.ExitInfo   { return h.exit }

type scriptedCloseCommandHandle struct {
	data              chan []byte
	exit              chan pty.ExitInfo
	closeResults      []error
	blockFirstClose   bool
	firstCloseStarted chan struct{}
	releaseFirstClose chan struct{}
	closeCalls        atomic.Int32
	settle            sync.Once
}

func newScriptedCloseCommandHandle(closeResults ...error) *scriptedCloseCommandHandle {
	return &scriptedCloseCommandHandle{
		data:              make(chan []byte, 8),
		exit:              make(chan pty.ExitInfo, 1),
		closeResults:      closeResults,
		firstCloseStarted: make(chan struct{}),
		releaseFirstClose: make(chan struct{}),
	}
}

func (h *scriptedCloseCommandHandle) Write(p []byte) (int, error) { return len(p), nil }
func (h *scriptedCloseCommandHandle) Resize(uint16, uint16) error { return nil }
func (h *scriptedCloseCommandHandle) Data() <-chan []byte         { return h.data }
func (h *scriptedCloseCommandHandle) Exit() <-chan pty.ExitInfo   { return h.exit }
func (h *scriptedCloseCommandHandle) Close() error {
	call := int(h.closeCalls.Add(1))
	if h.blockFirstClose && call == 1 {
		close(h.firstCloseStarted)
		<-h.releaseFirstClose
	}
	if call <= len(h.closeResults) {
		return h.closeResults[call-1]
	}
	return nil
}

func (h *scriptedCloseCommandHandle) finish(info pty.ExitInfo) {
	h.settle.Do(func() {
		h.exit <- info
		close(h.exit)
		close(h.data)
	})
}

func (h *scriptedCloseCommandHandle) unblockFirstClose() {
	select {
	case <-h.releaseFirstClose:
	default:
		close(h.releaseFirstClose)
	}
}

func TestService_RunCommand_GivenInvalidRequest_WhenStarted_ThenRejectsBeforeResolutionOpenOrLogging(t *testing.T) {
	validRequest := terminal_svc.RunCommandRequest{
		TerminalID: "terminal-valid",
		SessionID:  71,
		Command:    "go test ./...",
		Cols:       100,
		Rows:       30,
	}
	tests := []struct {
		name   string
		mutate func(*terminal_svc.RunCommandRequest)
	}{
		{name: "empty terminal ID", mutate: func(req *terminal_svc.RunCommandRequest) { req.TerminalID = "" }},
		{name: "whitespace terminal ID", mutate: func(req *terminal_svc.RunCommandRequest) { req.TerminalID = " \t\n" }},
		{name: "zero session ID", mutate: func(req *terminal_svc.RunCommandRequest) { req.SessionID = 0 }},
		{name: "negative session ID", mutate: func(req *terminal_svc.RunCommandRequest) { req.SessionID = -1 }},
		{name: "empty command", mutate: func(req *terminal_svc.RunCommandRequest) { req.Command = "" }},
		{name: "whitespace command", mutate: func(req *terminal_svc.RunCommandRequest) { req.Command = " \t\n" }},
		{name: "zero columns", mutate: func(req *terminal_svc.RunCommandRequest) { req.Cols = 0 }},
		{name: "zero rows", mutate: func(req *terminal_svc.RunCommandRequest) { req.Rows = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			request := validRequest
			tt.mutate(&request)
			openCalls := 0
			localBackend := mocks.NewMockPTYBackend(ctrl)
			localBackend.EXPECT().Open(gomock.Any(), gomock.Any()).DoAndReturn(
				func(context.Context, pty.Spec) (pty.Handle, error) {
					openCalls++
					return nil, errors.New("invalid request reached Open")
				},
			).AnyTimes()
			svc := terminal_svc.NewService(
				terminal_svc.NewBackendSelector(localBackend, nil),
				terminal_svc.NoopEmitter{},
			)
			resolveCalls := 0
			svc.SetCommandScopeResolver(func(
				context.Context,
				terminal_svc.ResolveCommandScopeRequest,
			) (*terminal_svc.CommandScope, error) {
				resolveCalls++
				return &terminal_svc.CommandScope{}, nil
			})
			defer svc.Shutdown()
			core, logs := observer.New(zapcore.DebugLevel)
			ctx := logger.WithContextLogger(context.Background(), zap.New(core))

			response, err := svc.RunCommand(ctx, request)

			assert.Nil(t, response)
			assert.ErrorIs(t, err, terminal_svc.ErrInvalidRunCommandRequest)
			assert.Equal(t, 0, resolveCalls)
			assert.Equal(t, 0, openCalls)
			assert.Equal(t, 0, logs.Len())
		})
	}
}

func TestService_RunCommand_GivenResolvedTarget_WhenCommandExits_ThenLogsOneRedactedStartAndExit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	sensitiveCwd := "/Users/alice/private-worktree"
	sensitiveCommand := "deploy --token=fixture-sensitive-token"
	sensitiveOutput := []byte("output with fixture-sensitive-output")
	sensitiveExitMessage := "exit detail with fixture-sensitive-message"
	wantScope := &terminal_svc.CommandScope{DeviceID: "device-9", Cwd: sensitiveCwd}
	resolveCalls := 0
	localBackend := mocks.NewMockPTYBackend(ctrl)
	remoteBackend := mocks.NewMockPTYBackend(ctrl)
	remoteBackend.EXPECT().Open(gomock.Any(), pty.Spec{
		TerminalID: "terminal-1",
		Cwd:        sensitiveCwd, Command: sensitiveCommand, Cols: 100, Rows: 30,
	}).Return(newCompletedCommandHandle(sensitiveOutput, pty.ExitInfo{
		Code: 17, Reason: "natural", Msg: sensitiveExitMessage,
	}), nil).Times(1)
	factoryCalls := 0
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(localBackend,
		func(deviceID string) (terminal_svc.PTYBackend, error) {
			factoryCalls++
			assert.Equal(t, "device-9", deviceID)
			return remoteBackend, nil
		}), emitter)
	svc.SetCommandScopeResolver(func(
		_ context.Context,
		req terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		resolveCalls++
		assert.Equal(t, terminal_svc.ResolveCommandScopeRequest{SessionID: 71}, req)
		return wantScope, nil
	})
	defer svc.Shutdown()
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	response, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-1",
		SessionID:  71,
		Command:    sensitiveCommand,
		Cols:       100,
		Rows:       30,
	})

	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, *wantScope, response.Scope)
	assert.Empty(t, response.StartError)
	assert.Equal(t, 1, resolveCalls)
	assert.Equal(t, 1, factoryCalls)
	require.Eventually(t, func() bool {
		exitEvents := 0
		for _, event := range emitter.Snapshot() {
			if event.Name == terminal_svc.ExitEventName("terminal-1") {
				exitEvents++
			}
		}
		return logs.Len() == 2 && exitEvents == 1
	}, time.Second, time.Millisecond)

	entries := logs.All()
	require.Len(t, entries, 2)
	assert.Equal(t, zapcore.InfoLevel, entries[0].Level)
	assert.Equal(t, "terminal_svc.RunCommand: command started", entries[0].Message)
	assert.Equal(t, map[string]any{
		"sessionId":  int64(71),
		"terminalId": "terminal-1",
		"deviceId":   "device-9",
	}, entries[0].ContextMap())
	assert.Equal(t, zapcore.InfoLevel, entries[1].Level)
	assert.Equal(t, "terminal_svc.RunCommand: command exited", entries[1].Message)
	assert.Equal(t, map[string]any{
		"sessionId":  int64(71),
		"terminalId": "terminal-1",
		"deviceId":   "device-9",
		"exitCode":   int64(17),
		"exitReason": "natural",
	}, entries[1].ContextMap())
	structuredLogs, marshalErr := json.Marshal(entries)
	require.NoError(t, marshalErr)
	observedLogs := string(structuredLogs)
	for _, sensitive := range []string{
		sensitiveCommand, "fixture-sensitive-token", sensitiveCwd,
		string(sensitiveOutput), "fixture-sensitive-output", sensitiveExitMessage, "fixture-sensitive-message",
	} {
		assert.NotContains(t, observedLogs, sensitive)
	}
}

func TestService_RunCommand_GivenStartedCommand_WhenClosedBeforePumpOutcome_ThenLogsOneStoppedExitWithoutStaleEvents(t *testing.T) {
	handle := newReplacementRaceHandle(false, false)
	t.Cleanup(func() { handle.finish(pty.ExitInfo{Code: 41, Reason: "fixture-sensitive-late-exit"}) })
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(
		terminal_svc.NewBackendSelector(&replacementRaceBackend{old: handle}, nil),
		emitter,
	)
	svc.SetCommandScopeResolver(func(
		context.Context,
		terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		return &terminal_svc.CommandScope{Cwd: "/fixture-sensitive/private-cwd"}, nil
	})
	t.Cleanup(svc.Shutdown)
	core, logs := observer.New(zapcore.DebugLevel)
	requestCtx, cancel := context.WithCancel(
		logger.WithContextLogger(context.Background(), zap.New(core)),
	)

	response, err := svc.RunCommand(requestCtx, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-stopped",
		SessionID:  90,
		Command:    "deploy --token=fixture-sensitive-token",
		Cols:       80,
		Rows:       24,
	})
	require.NoError(t, err)
	require.Empty(t, response.StartError)
	require.Eventually(t, func() bool { return logs.Len() == 1 }, time.Second, time.Millisecond)
	cancel()

	require.NoError(t, svc.Close(context.Background(), "terminal-stopped"))
	require.Eventually(t, func() bool {
		return len(commandExitEntriesForSession(logs, 90)) == 1
	}, time.Second, time.Millisecond)
	handle.data <- []byte("fixture-sensitive-stale-output")
	handle.finish(pty.ExitInfo{Code: 41, Reason: "fixture-sensitive-late-exit"})
	require.Never(t, func() bool { return len(emitter.Snapshot()) != 0 }, 100*time.Millisecond, time.Millisecond)

	exitEntry := commandExitEntriesForSession(logs, 90)[0]
	require.Equal(t, map[string]any{
		"sessionId":  int64(90),
		"terminalId": "terminal-stopped",
		"deviceId":   "",
		"exitCode":   int64(-1),
		"exitReason": "stopped",
	}, exitEntry.ContextMap())
	structuredLogs, marshalErr := json.Marshal(logs.All())
	require.NoError(t, marshalErr)
	observedLogs := string(structuredLogs)
	for _, sensitive := range []string{
		"fixture-sensitive-token", "fixture-sensitive/private-cwd",
		"fixture-sensitive-stale-output", "fixture-sensitive-late-exit",
	} {
		assert.NotContains(t, observedLogs, sensitive)
	}
}

func TestService_RunCommand_GivenReplacementCloseFails_WhenStartingSameID_ThenKeepsOldAuthorityAndSurfacesStartFailure(t *testing.T) {
	sensitiveCloseErr := errors.New("fixture-sensitive replacement close failure: token=secret")
	old := newScriptedCloseCommandHandle(sensitiveCloseErr, nil)
	replacement := newReplacementRaceHandle(false, false)
	backend := &handleSequenceBackend{handles: []pty.Handle{old, replacement}}
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend, nil), emitter)
	svc.SetCommandScopeResolver(func(
		context.Context,
		terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		return &terminal_svc.CommandScope{}, nil
	})
	t.Cleanup(func() { old.finish(pty.ExitInfo{Code: 0, Reason: "closed"}) })
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	oldResponse, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-replacement-close-failure",
		SessionID:  191,
		Command:    "old command",
		Cols:       80,
		Rows:       24,
	})
	require.NoError(t, err)
	require.Empty(t, oldResponse.StartError)

	replacementResponse, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-replacement-close-failure",
		SessionID:  192,
		Command:    "replacement command",
		Cols:       80,
		Rows:       24,
	})

	require.NoError(t, err)
	require.NotNil(t, replacementResponse)
	require.Equal(t, sensitiveCloseErr.Error(), replacementResponse.StartError)
	require.Equal(t, int32(1), backend.opens.Load(), "replacement backend.Open must wait for confirmed old-handle close")
	require.Empty(t, commandExitEntriesForSession(logs, 191), "failed close has no retirement authority")

	old.data <- []byte("old-still-authoritative")
	require.Eventually(t, func() bool {
		for _, event := range emitter.Snapshot() {
			if event.Name == terminal_svc.DataEventName("terminal-replacement-close-failure") {
				return recordedDataEquals(event, "old-still-authoritative")
			}
		}
		return false
	}, time.Second, time.Millisecond)
	require.NoError(t, svc.Close(context.Background(), "terminal-replacement-close-failure"),
		"the retained old handle must remain Close-retryable")
	require.Equal(t, int32(2), old.closeCalls.Load())
	require.Eventually(t, func() bool {
		return len(commandExitEntriesForSession(logs, 191)) == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, "stopped", commandExitEntriesForSession(logs, 191)[0].ContextMap()["exitReason"])

	failedOpenLogs := logs.FilterMessage("terminal_svc.RunCommand: open command failed").All()
	require.Len(t, failedOpenLogs, 1)
	require.Equal(t, map[string]any{
		"sessionId":     int64(192),
		"terminalId":    "terminal-replacement-close-failure",
		"deviceId":      "",
		"startStage":    "replacementClose",
		"errorCategory": "unknown",
		"errorClass":    "terminalCommandStartFailed",
	}, failedOpenLogs[0].ContextMap())
	structuredLogs, marshalErr := json.Marshal(logs.All())
	require.NoError(t, marshalErr)
	require.NotContains(t, string(structuredLogs), sensitiveCloseErr.Error())
	require.NotContains(t, string(structuredLogs), "token=secret")
}

func TestService_RunCommand_GivenReplacementCloseBlocks_WhenStartingSameID_ThenOldEmitsUntilCloseAuthority(t *testing.T) {
	old := newScriptedCloseCommandHandle(nil)
	old.blockFirstClose = true
	replacement := newReplacementRaceHandle(false, false)
	backend := &handleSequenceBackend{handles: []pty.Handle{old, replacement}}
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend, nil), emitter)
	svc.SetCommandScopeResolver(func(
		context.Context,
		terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		return &terminal_svc.CommandScope{}, nil
	})
	t.Cleanup(old.unblockFirstClose)
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	response, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-replacement-close-gate",
		SessionID:  193,
		Command:    "old command",
		Cols:       80,
		Rows:       24,
	})
	require.NoError(t, err)
	require.Empty(t, response.StartError)

	resultCh := startRunCommand(ctx, svc, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-replacement-close-gate",
		SessionID:  194,
		Command:    "replacement command",
		Cols:       80,
		Rows:       24,
	})
	<-old.firstCloseStarted
	require.Equal(t, int32(1), backend.opens.Load())
	old.data <- []byte("valid-before-close-authority")
	require.Eventually(t, func() bool {
		for _, event := range emitter.Snapshot() {
			if event.Name == terminal_svc.DataEventName("terminal-replacement-close-gate") {
				return recordedDataEquals(event, "valid-before-close-authority")
			}
		}
		return false
	}, time.Second, time.Millisecond)
	require.Empty(t, commandExitEntriesForSession(logs, 193))

	old.unblockFirstClose()
	result := <-resultCh
	require.NoError(t, result.err)
	require.Empty(t, result.response.StartError)
	require.Equal(t, int32(2), backend.opens.Load())
	require.Eventually(t, func() bool {
		return len(commandExitEntriesForSession(logs, 193)) == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, "replaced", commandExitEntriesForSession(logs, 193)[0].ContextMap()["exitReason"])

	old.data <- []byte("invalid-after-retirement")
	old.finish(pty.ExitInfo{Code: 41, Reason: "stale"})
	replacement.finish(pty.ExitInfo{Code: 0, Reason: "natural"})
	require.Eventually(t, func() bool {
		return len(commandExitEntriesForSession(logs, 194)) == 1
	}, time.Second, time.Millisecond)
	invalidData := base64.StdEncoding.EncodeToString([]byte("invalid-after-retirement"))
	require.Never(t, func() bool {
		for _, event := range emitter.Snapshot() {
			if payload, ok := event.Payload.(map[string]string); ok && payload["data"] == invalidData {
				return true
			}
		}
		return false
	}, 100*time.Millisecond, time.Millisecond)
}

func TestService_RunCommand_GivenFailedReplacementCloseIsPreempted_WhenNewerStartWaits_ThenUsesPreemptionPolicyAndKeepsOldActive(t *testing.T) {
	sensitiveCloseErr := errors.New("fixture-sensitive preempted close failure")
	old := newScriptedCloseCommandHandle(sensitiveCloseErr, nil)
	old.blockFirstClose = true
	replacement := newReplacementRaceHandle(false, false)
	backend := &handleSequenceBackend{handles: []pty.Handle{old, replacement}}
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend, nil), emitter)
	newerResolverStarted := make(chan struct{})
	releaseNewerResolver := make(chan struct{})
	svc.SetCommandScopeResolver(func(
		_ context.Context,
		req terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		if req.SessionID == 197 {
			close(newerResolverStarted)
			<-releaseNewerResolver
		}
		return &terminal_svc.CommandScope{}, nil
	})
	t.Cleanup(old.unblockFirstClose)
	t.Cleanup(func() {
		select {
		case <-releaseNewerResolver:
		default:
			close(releaseNewerResolver)
		}
	})
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	response, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-replacement-close-preempted",
		SessionID:  195,
		Command:    "old command",
		Cols:       80,
		Rows:       24,
	})
	require.NoError(t, err)
	require.Empty(t, response.StartError)

	olderResultCh := startRunCommand(ctx, svc, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-replacement-close-preempted",
		SessionID:  196,
		Command:    "older replacement",
		Cols:       80,
		Rows:       24,
	})
	<-old.firstCloseStarted
	newerResultCh := startRunCommand(ctx, svc, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-replacement-close-preempted",
		SessionID:  197,
		Command:    "newer replacement",
		Cols:       80,
		Rows:       24,
	})
	<-newerResolverStarted
	old.unblockFirstClose()
	olderResult := <-olderResultCh

	require.NoError(t, olderResult.err)
	require.Equal(t, terminal_svc.ErrCommandStartPreempted.Error(), olderResult.response.StartError)
	require.Empty(t, logs.FilterMessage("terminal_svc.RunCommand: open command failed").All())
	require.Empty(t, commandExitEntriesForSession(logs, 195),
		"a preempted failed close has no retirement authority")
	old.data <- []byte("old-valid-after-preempted-close-failure")
	require.Eventually(t, func() bool {
		for _, event := range emitter.Snapshot() {
			if event.Name == terminal_svc.DataEventName("terminal-replacement-close-preempted") {
				return recordedDataEquals(event, "old-valid-after-preempted-close-failure")
			}
		}
		return false
	}, time.Second, time.Millisecond)

	close(releaseNewerResolver)
	newerResult := <-newerResultCh
	require.NoError(t, newerResult.err)
	require.Empty(t, newerResult.response.StartError)
	require.Equal(t, int32(2), backend.opens.Load())
	require.Eventually(t, func() bool {
		return len(commandExitEntriesForSession(logs, 195)) == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, "replaced", commandExitEntriesForSession(logs, 195)[0].ContextMap()["exitReason"])
	old.finish(pty.ExitInfo{Code: 41, Reason: "stale"})
	replacement.finish(pty.ExitInfo{Code: 0, Reason: "natural"})
	require.Eventually(t, func() bool {
		return len(commandExitEntriesForSession(logs, 197)) == 1
	}, time.Second, time.Millisecond)
}

func TestService_RunCommand_GivenSameIDReplacementWhenOldChannelsSettleLateThenOldLogsReplacedAndOnlyNewEmits(t *testing.T) {
	old := newReplacementRaceHandle(false, false)
	replacement := newReplacementRaceHandle(false, false)
	backend := &replacementRaceBackend{old: old, replacement: replacement}
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend, nil), emitter)
	svc.SetCommandScopeResolver(func(
		_ context.Context,
		req terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		return &terminal_svc.CommandScope{Cwd: map[int64]string{91: "/old", 92: "/replacement"}[req.SessionID]}, nil
	})
	t.Cleanup(svc.Shutdown)
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	for _, req := range []terminal_svc.RunCommandRequest{
		{TerminalID: "terminal-replaced", SessionID: 91, Command: "old command", Cols: 80, Rows: 24},
		{TerminalID: "terminal-replaced", SessionID: 92, Command: "replacement command", Cols: 80, Rows: 24},
	} {
		response, err := svc.RunCommand(ctx, req)
		require.NoError(t, err)
		require.Empty(t, response.StartError)
	}

	old.data <- []byte("stale")
	old.finish(pty.ExitInfo{Code: 41, Reason: "stale"})
	replacement.data <- []byte("fresh")
	replacement.finish(pty.ExitInfo{Code: 7, Reason: "natural"})
	require.Eventually(t, func() bool {
		for _, event := range emitter.Snapshot() {
			exitEvent, ok := event.Payload.(protocol.TerminalExitEvent)
			if ok && exitEvent.Reason == "natural" {
				return len(commandExitEntriesForSession(logs, 91)) == 1 &&
					len(commandExitEntriesForSession(logs, 92)) == 1
			}
		}
		return false
	}, time.Second, time.Millisecond)

	staleData := base64.StdEncoding.EncodeToString([]byte("stale"))
	require.Never(t, func() bool {
		for _, event := range emitter.Snapshot() {
			if payload, ok := event.Payload.(map[string]string); ok && payload["data"] == staleData {
				return true
			}
			if payload, ok := event.Payload.(protocol.TerminalExitEvent); ok && payload.Reason == "stale" {
				return true
			}
		}
		return false
	}, 100*time.Millisecond, time.Millisecond,
		"retired command must not emit stale data or a stale terminal exit")

	events := emitter.Snapshot()
	require.Len(t, events, 2)
	require.Equal(t, []byte("fresh"), recordedData(t, events[0]))
	require.Equal(t, protocol.TerminalExitEvent{Code: 7, Reason: "natural"}, events[1].Payload)
	require.Equal(t, int64(-1), commandExitEntriesForSession(logs, 91)[0].ContextMap()["exitCode"])
	require.Equal(t, "replaced", commandExitEntriesForSession(logs, 91)[0].ContextMap()["exitReason"])
	require.Equal(t, int64(7), commandExitEntriesForSession(logs, 92)[0].ContextMap()["exitCode"])
	require.Equal(t, "natural", commandExitEntriesForSession(logs, 92)[0].ContextMap()["exitReason"])
}

func TestService_RunCommand_GivenStartedCommand_WhenServiceShutsDown_ThenLogsOneShutdownExitWithoutStaleEvents(t *testing.T) {
	handle := newReplacementRaceHandle(false, false)
	t.Cleanup(func() { handle.finish(pty.ExitInfo{Code: 42, Reason: "late"}) })
	emitter := &recordingEmitter{}
	backend := &replacementRaceBackend{old: handle}
	svc := terminal_svc.NewService(
		terminal_svc.NewBackendSelector(backend, func(string) (terminal_svc.PTYBackend, error) {
			return backend, nil
		}),
		emitter,
	)
	svc.SetCommandScopeResolver(func(
		context.Context,
		terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		return &terminal_svc.CommandScope{DeviceID: "device-shutdown", Cwd: "/private/shutdown"}, nil
	})
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	response, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-shutdown",
		SessionID:  93,
		Command:    "private command",
		Cols:       80,
		Rows:       24,
	})
	require.NoError(t, err)
	require.Empty(t, response.StartError)
	require.Eventually(t, func() bool { return logs.Len() == 1 }, time.Second, time.Millisecond)

	svc.Shutdown()
	require.Eventually(t, func() bool {
		return len(commandExitEntriesForSession(logs, 93)) == 1
	}, time.Second, time.Millisecond)
	handle.data <- []byte("stale")
	handle.finish(pty.ExitInfo{Code: 42, Reason: "late"})
	require.Never(t, func() bool { return len(emitter.Snapshot()) != 0 }, 100*time.Millisecond, time.Millisecond)
	require.Equal(t, map[string]any{
		"sessionId":  int64(93),
		"terminalId": "terminal-shutdown",
		"deviceId":   "device-shutdown",
		"exitCode":   int64(-1),
		"exitReason": "shutdown",
	}, commandExitEntriesForSession(logs, 93)[0].ContextMap())
}

func TestService_RunCommand_GivenShutdownCloseFails_WhenOldLaterExitsNaturally_ThenWarnsSafelyAndDefersFinalAuthority(t *testing.T) {
	sensitiveCloseErr := errors.New("fixture-sensitive shutdown close failure: command=secret cwd=/private")
	handle := newScriptedCloseCommandHandle(sensitiveCloseErr)
	backend := &handleSequenceBackend{handles: []pty.Handle{handle}}
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend,
		func(string) (terminal_svc.PTYBackend, error) { return backend, nil }), emitter)
	svc.SetCommandScopeResolver(func(
		context.Context,
		terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		return &terminal_svc.CommandScope{DeviceID: "device-shutdown-failure", Cwd: "/fixture-sensitive/private-cwd"}, nil
	})
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	response, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-shutdown-close-failure",
		SessionID:  198,
		Command:    "fixture-sensitive private command",
		Cols:       80,
		Rows:       24,
	})
	require.NoError(t, err)
	require.Empty(t, response.StartError)

	svc.Shutdown()
	require.Equal(t, int32(1), handle.closeCalls.Load())
	require.Empty(t, commandExitEntriesForSession(logs, 198),
		"failed Close must not claim shutdown final authority")
	warnEntries := logs.FilterMessage("terminal_svc.Shutdown: command close failed").All()
	require.Len(t, warnEntries, 1)
	require.Equal(t, zapcore.WarnLevel, warnEntries[0].Level)
	require.Equal(t, map[string]any{
		"sessionId":  int64(198),
		"terminalId": "terminal-shutdown-close-failure",
		"deviceId":   "device-shutdown-failure",
		"errorClass": "terminalCommandShutdownCloseFailed",
	}, warnEntries[0].ContextMap())

	handle.data <- []byte("still-authoritative-after-shutdown-close-failure")
	require.Eventually(t, func() bool {
		for _, event := range emitter.Snapshot() {
			if event.Name == terminal_svc.DataEventName("terminal-shutdown-close-failure") {
				return recordedDataEquals(event, "still-authoritative-after-shutdown-close-failure")
			}
		}
		return false
	}, time.Second, time.Millisecond)
	handle.finish(pty.ExitInfo{Code: 23, Reason: "connectionLost", Msg: "fixture-sensitive exit detail"})
	require.Eventually(t, func() bool {
		return len(commandExitEntriesForSession(logs, 198)) == 1
	}, time.Second, time.Millisecond)
	require.Equal(t, map[string]any{
		"sessionId":  int64(198),
		"terminalId": "terminal-shutdown-close-failure",
		"deviceId":   "device-shutdown-failure",
		"exitCode":   int64(23),
		"exitReason": "connectionLost",
	}, commandExitEntriesForSession(logs, 198)[0].ContextMap())
	require.Eventually(t, func() bool {
		for _, event := range emitter.Snapshot() {
			if exitEvent, ok := event.Payload.(protocol.TerminalExitEvent); ok {
				return exitEvent.Code == 23 && exitEvent.Reason == "connectionLost"
			}
		}
		return false
	}, time.Second, time.Millisecond)
	structuredLogs, marshalErr := json.Marshal(logs.All())
	require.NoError(t, marshalErr)
	observedLogs := string(structuredLogs)
	for _, sensitive := range []string{
		sensitiveCloseErr.Error(), "command=secret", "/private", "fixture-sensitive",
	} {
		require.NotContains(t, observedLogs, sensitive)
	}
}

func TestService_RunCommand_GivenNaturalExitRacesClose_WhenBothSettle_ThenLogsExactlyOneExit(t *testing.T) {
	const iterations = 64
	handles := make([]pty.Handle, 0, iterations)
	concreteHandles := make([]*replacementRaceHandle, 0, iterations)
	for range iterations {
		handle := newReplacementRaceHandle(false, false)
		handles = append(handles, handle)
		concreteHandles = append(concreteHandles, handle)
	}
	backend := &handleSequenceBackend{handles: handles}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend, nil), terminal_svc.NoopEmitter{})
	svc.SetCommandScopeResolver(func(
		context.Context,
		terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		return &terminal_svc.CommandScope{}, nil
	})
	t.Cleanup(svc.Shutdown)
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	for i, handle := range concreteHandles {
		terminalID := fmt.Sprintf("terminal-natural-close-%d", i)
		sessionID := int64(1000 + i)
		response, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
			TerminalID: terminalID,
			SessionID:  sessionID,
			Command:    "command",
			Cols:       80,
			Rows:       24,
		})
		require.NoError(t, err)
		require.Empty(t, response.StartError)
		require.Eventually(t, func() bool {
			for _, entry := range logs.FilterMessage("terminal_svc.RunCommand: command started").All() {
				if entry.ContextMap()["sessionId"] == sessionID {
					return true
				}
			}
			return false
		}, time.Second, time.Millisecond)

		start := make(chan struct{})
		closeResult := make(chan error, 1)
		finishDone := make(chan struct{})
		go func() {
			<-start
			closeResult <- svc.Close(context.Background(), terminalID)
		}()
		go func() {
			defer close(finishDone)
			<-start
			handle.finish(pty.ExitInfo{Code: 0, Reason: "natural"})
		}()
		close(start)
		closeErr := <-closeResult
		if closeErr != nil {
			require.ErrorIs(t, closeErr, terminal_svc.ErrTerminalNotOpen)
		}
		<-finishDone

		require.Eventually(t, func() bool {
			return len(commandExitEntriesForSession(logs, sessionID)) == 1
		}, time.Second, time.Millisecond)
		require.Never(t, func() bool {
			return len(commandExitEntriesForSession(logs, sessionID)) > 1
		}, 5*time.Millisecond, 100*time.Microsecond)
	}
}

func commandExitEntriesForSession(logs *observer.ObservedLogs, sessionID int64) []observer.LoggedEntry {
	entries := make([]observer.LoggedEntry, 0, 1)
	for _, entry := range logs.FilterMessage("terminal_svc.RunCommand: command exited").All() {
		if entry.ContextMap()["sessionId"] == sessionID {
			entries = append(entries, entry)
		}
	}
	return entries
}

func TestService_Open_GivenInteractiveTerminal_WhenItExits_ThenLogsNoCommandLifecycle(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	localBackend := mocks.NewMockPTYBackend(ctrl)
	localBackend.EXPECT().Open(gomock.Any(), pty.Spec{
		TerminalID: "interactive-1", Cwd: "/private/interactive-cwd", Cols: 80, Rows: 24,
	}).Return(newCompletedCommandHandle([]byte("private interactive output"), pty.ExitInfo{
		Code: 0, Reason: "natural", Msg: "private interactive exit detail",
	}), nil).Times(1)
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(
		terminal_svc.NewBackendSelector(localBackend, nil),
		emitter,
	)
	defer svc.Shutdown()
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	require.NoError(t, svc.Open(ctx, "interactive-1", "", "/private/interactive-cwd", 80, 24))
	require.Eventually(t, func() bool {
		for _, event := range emitter.Snapshot() {
			if event.Name == terminal_svc.ExitEventName("interactive-1") {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
	assert.Zero(t, logs.Len())
}

func TestService_RunCommand_GivenStartFailure_WhenStarted_ThenLogsSafeStageAndCategoryAndReturnsExactError(t *testing.T) {
	tests := []struct {
		name          string
		startStage    string
		startErr      error
		errorCategory string
	}{
		{
			name:       "backend selector path is not found",
			startStage: "backendSelect",
			startErr: &os.PathError{
				Op: "select fixture-sensitive-selector", Path: "/fixture-sensitive/missing.sock", Err: os.ErrNotExist,
			},
			errorCategory: "notFound",
		},
		{
			name:       "PTY executable path is permission denied",
			startStage: "ptyOpen",
			startErr: &os.PathError{
				Op: "fork fixture-sensitive-shell", Path: "/fixture-sensitive/private/zsh", Err: os.ErrPermission,
			},
			errorCategory: "permissionDenied",
		},
		{
			name:          "PTY network operation fails",
			startStage:    "ptyOpen",
			startErr:      &net.OpError{Op: "dial fixture-sensitive-host", Net: "tcp-sensitive", Err: errors.New("fixture-sensitive-network-cause")},
			errorCategory: "network",
		},
		{
			name:          "PTY network operation times out",
			startStage:    "ptyOpen",
			startErr:      &net.OpError{Op: "dial fixture-sensitive-timeout", Net: "tcp-sensitive", Err: os.ErrDeadlineExceeded},
			errorCategory: "timeout",
		},
		{
			name:          "remote daemon open times out",
			startStage:    "ptyOpen",
			startErr:      ptyremote.ErrDaemonTimeout,
			errorCategory: "timeout",
		},
		{
			name:          "backend selector becomes unavailable",
			startStage:    "backendSelect",
			startErr:      errors.Join(context.Canceled, errors.New("fixture-sensitive-canceled-selection")),
			errorCategory: "unavailable",
		},
		{
			name:          "backend selector returns an unknown error",
			startStage:    "backendSelect",
			startErr:      errors.New("fixture-sensitive-generic-selection-failure"),
			errorCategory: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			sensitiveCwd := "/fixture-sensitive/private-worktree"
			sensitiveCommand := "deploy --token=fixture-sensitive-token"
			wantScope := &terminal_svc.CommandScope{DeviceID: "device-9", Cwd: sensitiveCwd}
			localBackend := mocks.NewMockPTYBackend(ctrl)
			remoteBackend := mocks.NewMockPTYBackend(ctrl)
			if tt.startStage == "ptyOpen" {
				remoteBackend.EXPECT().Open(gomock.Any(), pty.Spec{
					TerminalID: "terminal-2",
					Cwd:        sensitiveCwd, Command: sensitiveCommand, Cols: 80, Rows: 24,
				}).Return(nil, tt.startErr).Times(1)
			}
			factoryCalls := 0
			svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(localBackend,
				func(string) (terminal_svc.PTYBackend, error) {
					factoryCalls++
					if tt.startStage == "backendSelect" {
						return nil, tt.startErr
					}
					return remoteBackend, nil
				}), terminal_svc.NoopEmitter{})
			svc.SetCommandScopeResolver(func(
				context.Context,
				terminal_svc.ResolveCommandScopeRequest,
			) (*terminal_svc.CommandScope, error) {
				return wantScope, nil
			})
			defer svc.Shutdown()
			core, logs := observer.New(zapcore.DebugLevel)
			ctx := logger.WithContextLogger(context.Background(), zap.New(core))

			response, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
				TerminalID: "terminal-2",
				SessionID:  72,
				Command:    sensitiveCommand,
				Cols:       80,
				Rows:       24,
			})

			require.NoError(t, err)
			require.NotNil(t, response)
			assert.Equal(t, *wantScope, response.Scope)
			assert.Equal(t, tt.startErr.Error(), response.StartError)
			assert.Equal(t, 1, factoryCalls)
			require.Equal(t, 1, logs.Len())
			assert.Zero(t, logs.FilterMessage("terminal_svc.RunCommand: command started").Len())
			assert.Zero(t, logs.FilterMessage("terminal_svc.RunCommand: command exited").Len())
			entry := logs.All()[0]
			assert.Equal(t, zapcore.WarnLevel, entry.Level)
			assert.Equal(t, "terminal_svc.RunCommand: open command failed", entry.Message)
			assert.Equal(t, map[string]any{
				"sessionId":     int64(72),
				"terminalId":    "terminal-2",
				"deviceId":      "device-9",
				"startStage":    tt.startStage,
				"errorCategory": tt.errorCategory,
				"errorClass":    "terminalCommandStartFailed",
			}, entry.ContextMap())
			structuredEntry, marshalErr := json.Marshal(entry)
			require.NoError(t, marshalErr)
			observedLog := string(structuredEntry)
			assert.NotContains(t, observedLog, "fixture-sensitive")
			assert.NotContains(t, observedLog, tt.startErr.Error())
		})
	}
}

func TestService_RunCommand_GivenClosePreemptsCancellationIgnoringOpen_WhenBackendReturnsHandle_ThenReturnsScopedStartErrorWithoutLifecycleEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	wantScope := &terminal_svc.CommandScope{
		DeviceID: "device-9",
		Cwd:      "/Users/alice/private-worktree",
	}
	sensitiveCommand := "deploy --token=fixture-sensitive-token"
	localBackend := mocks.NewMockPTYBackend(ctrl)
	remoteBackend := mocks.NewMockPTYBackend(ctrl)
	handle := mocks.NewMockHandle(ctrl)
	openCtxCh := make(chan context.Context, 1)
	proceed := make(chan struct{})
	remoteBackend.EXPECT().Open(gomock.Any(), pty.Spec{
		TerminalID: "terminal-preempted",
		Cwd:        wantScope.Cwd, Command: sensitiveCommand, Cols: 80, Rows: 24,
	}).DoAndReturn(func(openCtx context.Context, _ pty.Spec) (pty.Handle, error) {
		openCtxCh <- openCtx
		<-proceed
		return handle, nil
	}).Times(1)
	handle.EXPECT().Close().Return(nil).Times(1)

	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(localBackend,
		func(deviceID string) (terminal_svc.PTYBackend, error) {
			assert.Equal(t, wantScope.DeviceID, deviceID)
			return remoteBackend, nil
		}), emitter)
	svc.SetCommandScopeResolver(func(
		context.Context,
		terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		return wantScope, nil
	})
	defer svc.Shutdown()
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	type runResult struct {
		response *terminal_svc.RunCommandResponse
		err      error
	}
	resultCh := make(chan runResult, 1)
	go func() {
		response, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
			TerminalID: "terminal-preempted",
			SessionID:  72,
			Command:    sensitiveCommand,
			Cols:       80,
			Rows:       24,
		})
		resultCh <- runResult{response: response, err: err}
	}()

	openCtx := <-openCtxCh
	require.NoError(t, svc.Close(context.Background(), "terminal-preempted"))
	require.ErrorIs(t, openCtx.Err(), context.Canceled)
	close(proceed)
	result := <-resultCh

	require.NoError(t, result.err)
	require.NotNil(t, result.response)
	assert.Equal(t, *wantScope, result.response.Scope)
	assert.Equal(t, terminal_svc.ErrCommandStartPreempted.Error(), result.response.StartError)
	var preempted terminal_svc.CommandStartPreemptedError
	assert.ErrorAs(t, terminal_svc.ErrCommandStartPreempted, &preempted)
	assert.Empty(t, emitter.Snapshot())
	assert.Zero(t, logs.Len())
	assert.ErrorIs(t, svc.Write(context.Background(), "terminal-preempted", "x"), terminal_svc.ErrTerminalClosed)
}

// countingCommandBackend records every backend.Open boundary while returning a
// completed handle so a regression cannot strand the test in a pump goroutine.
type countingCommandBackend struct {
	opens atomic.Int32
}

func (b *countingCommandBackend) Open(context.Context, pty.Spec) (pty.Handle, error) {
	b.opens.Add(1)
	return newCompletedCommandHandle(nil, pty.ExitInfo{Code: 0, Reason: "natural"}), nil
}

type runCommandResult struct {
	response *terminal_svc.RunCommandResponse
	err      error
}

func startRunCommand(
	ctx context.Context,
	svc *terminal_svc.Service,
	req terminal_svc.RunCommandRequest,
) <-chan runCommandResult {
	resultCh := make(chan runCommandResult, 1)
	go func() {
		response, err := svc.RunCommand(ctx, req)
		resultCh <- runCommandResult{response: response, err: err}
	}()
	return resultCh
}

func TestService_RunCommand_GivenResolverBlocks_WhenClosedBeforeScopeExists_ThenCancelsAttemptAndNeverOpensBackend(t *testing.T) {
	backend := &countingCommandBackend{}
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(
		terminal_svc.NewBackendSelector(backend, nil),
		emitter,
	)
	resolverStarted := make(chan context.Context, 1)
	releaseResolver := make(chan struct{})
	svc.SetCommandScopeResolver(func(
		resolveCtx context.Context,
		_ terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		resolverStarted <- resolveCtx
		<-releaseResolver // deliberately ignore cancellation
		return &terminal_svc.CommandScope{Cwd: "/private/resolved-too-late"}, nil
	})
	defer svc.Shutdown()
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	resultCh := startRunCommand(ctx, svc, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-resolver-stop",
		SessionID:  81,
		Command:    "private command",
		Cols:       80,
		Rows:       24,
	})
	resolveCtx := <-resolverStarted
	closeErr := svc.Close(context.Background(), "terminal-resolver-stop")
	ctxErr := resolveCtx.Err()
	close(releaseResolver)
	result := <-resultCh

	require.NoError(t, closeErr)
	assert.ErrorIs(t, ctxErr, context.Canceled)
	assert.Nil(t, result.response)
	assert.ErrorIs(t, result.err, terminal_svc.ErrCommandStartPreempted)
	var preempted terminal_svc.CommandStartPreemptedError
	assert.ErrorAs(t, result.err, &preempted)
	assert.Zero(t, backend.opens.Load())
	assert.Empty(t, emitter.Snapshot())
	assert.Zero(t, logs.Len())
}

func TestService_RunCommand_GivenSelectorBlocks_WhenClosedAfterScopeExists_ThenReturnsScopedPreemptionWithoutBackendOpen(t *testing.T) {
	backend := &countingCommandBackend{}
	factoryCalls := atomic.Int32{}
	selectorStarted := make(chan struct{})
	releaseSelector := make(chan struct{})
	wantScope := &terminal_svc.CommandScope{
		DeviceID: "device-blocked-selector",
		Cwd:      "/private/exact-scope",
	}
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(backend,
		func(string) (terminal_svc.PTYBackend, error) {
			factoryCalls.Add(1)
			close(selectorStarted)
			<-releaseSelector // selector has no context boundary
			return backend, nil
		}), emitter)
	svc.SetCommandScopeResolver(func(
		context.Context,
		terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		return wantScope, nil
	})
	defer svc.Shutdown()
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	resultCh := startRunCommand(ctx, svc, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-selector-stop",
		SessionID:  82,
		Command:    "private command",
		Cols:       80,
		Rows:       24,
	})
	<-selectorStarted
	closeErr := svc.Close(context.Background(), "terminal-selector-stop")
	close(releaseSelector)
	result := <-resultCh

	require.NoError(t, closeErr)
	require.NoError(t, result.err)
	require.NotNil(t, result.response)
	assert.Equal(t, *wantScope, result.response.Scope)
	assert.Equal(t, terminal_svc.ErrCommandStartPreempted.Error(), result.response.StartError)
	assert.Equal(t, int32(1), factoryCalls.Load())
	assert.Zero(t, backend.opens.Load())
	assert.Empty(t, emitter.Snapshot())
	assert.Zero(t, logs.Len())
}

type blockingCloseHandle struct {
	data         chan []byte
	exit         chan pty.ExitInfo
	closeStarted chan struct{}
	releaseClose chan struct{}
	closeOnce    sync.Once
}

func newBlockingCloseHandle() *blockingCloseHandle {
	return &blockingCloseHandle{
		data:         make(chan []byte),
		exit:         make(chan pty.ExitInfo, 1),
		closeStarted: make(chan struct{}),
		releaseClose: make(chan struct{}),
	}
}

func (h *blockingCloseHandle) Write(p []byte) (int, error) { return len(p), nil }
func (h *blockingCloseHandle) Resize(uint16, uint16) error { return nil }
func (h *blockingCloseHandle) Data() <-chan []byte         { return h.data }
func (h *blockingCloseHandle) Exit() <-chan pty.ExitInfo   { return h.exit }
func (h *blockingCloseHandle) Close() error {
	h.closeOnce.Do(func() {
		close(h.closeStarted)
		<-h.releaseClose
		h.exit <- pty.ExitInfo{Code: 0, Reason: "closed"}
		close(h.exit)
		close(h.data)
	})
	return nil
}

type evictionBlockingBackend struct {
	old          pty.Handle
	commandOpens atomic.Int32
}

func (b *evictionBlockingBackend) Open(_ context.Context, spec pty.Spec) (pty.Handle, error) {
	if spec.Command == "" {
		return b.old, nil
	}
	b.commandOpens.Add(1)
	return newCompletedCommandHandle(nil, pty.ExitInfo{Code: 0, Reason: "natural"}), nil
}

func TestService_RunCommand_GivenExistingHandleEvictionBlocks_WhenClosedInGap_ThenNeverLaunchesCommand(t *testing.T) {
	oldHandle := newBlockingCloseHandle()
	backend := &evictionBlockingBackend{old: oldHandle}
	svc := terminal_svc.NewService(
		terminal_svc.NewBackendSelector(backend, nil),
		terminal_svc.NoopEmitter{},
	)
	wantScope := &terminal_svc.CommandScope{Cwd: "/private/exact-scope"}
	attemptCtxCh := make(chan context.Context, 1)
	svc.SetCommandScopeResolver(func(
		resolveCtx context.Context,
		_ terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		attemptCtxCh <- resolveCtx
		return wantScope, nil
	})
	defer svc.Shutdown()
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	require.NoError(t, svc.Open(ctx, "terminal-eviction-stop", "", "/private/old", 80, 24))
	resultCh := startRunCommand(ctx, svc, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-eviction-stop",
		SessionID:  83,
		Command:    "private command",
		Cols:       80,
		Rows:       24,
	})
	<-oldHandle.closeStarted
	attemptCtx := <-attemptCtxCh
	closeResultCh := make(chan error, 1)
	go func() {
		closeResultCh <- svc.Close(context.Background(), "terminal-eviction-stop")
	}()
	<-attemptCtx.Done()
	close(oldHandle.releaseClose)
	closeErr := <-closeResultCh
	result := <-resultCh

	require.NoError(t, closeErr)
	require.NoError(t, result.err)
	require.NotNil(t, result.response)
	assert.Equal(t, *wantScope, result.response.Scope)
	assert.Equal(t, terminal_svc.ErrCommandStartPreempted.Error(), result.response.StartError)
	assert.Zero(t, backend.commandOpens.Load())
	assert.Zero(t, logs.Len())
}

func TestService_RunCommand_GivenOlderResolverBlocks_WhenNewerRunClaimsSameID_ThenOnlyNewerLaunches(t *testing.T) {
	backend := &countingCommandBackend{}
	emitter := &recordingEmitter{}
	svc := terminal_svc.NewService(
		terminal_svc.NewBackendSelector(backend, nil),
		emitter,
	)
	resolverCalls := atomic.Int32{}
	olderStarted := make(chan context.Context, 1)
	releaseOlder := make(chan struct{})
	wantScope := &terminal_svc.CommandScope{Cwd: "/private/exact-scope"}
	svc.SetCommandScopeResolver(func(
		resolveCtx context.Context,
		_ terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		if resolverCalls.Add(1) == 1 {
			olderStarted <- resolveCtx
			<-releaseOlder
		}
		return wantScope, nil
	})
	defer svc.Shutdown()
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))
	req := terminal_svc.RunCommandRequest{
		TerminalID: "terminal-newer-wins",
		SessionID:  84,
		Command:    "private command",
		Cols:       80,
		Rows:       24,
	}

	olderResultCh := startRunCommand(ctx, svc, req)
	olderCtx := <-olderStarted
	newerResponse, newerErr := svc.RunCommand(ctx, req)
	olderCtxErr := olderCtx.Err()
	close(releaseOlder)
	olderResult := <-olderResultCh

	require.NoError(t, newerErr)
	require.NotNil(t, newerResponse)
	assert.Empty(t, newerResponse.StartError)
	assert.ErrorIs(t, olderCtxErr, context.Canceled)
	assert.Nil(t, olderResult.response)
	assert.ErrorIs(t, olderResult.err, terminal_svc.ErrCommandStartPreempted)
	assert.Equal(t, int32(1), backend.opens.Load())
	require.Eventually(t, func() bool {
		return logs.Len() == 2 && len(emitter.Snapshot()) == 1
	}, time.Second, time.Millisecond)
}

func TestService_RunCommand_GivenResolverUnavailable_WhenStarted_ThenReturnsErrorWithoutPanicOrLaunch(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*terminal_svc.Service)
		wantErr   error
	}{
		{
			name:      "resolver is not initialized",
			configure: func(*terminal_svc.Service) {},
			wantErr:   terminal_svc.ErrCommandScopeResolverNotInitialized,
		},
		{
			name: "resolver returns no scope",
			configure: func(svc *terminal_svc.Service) {
				svc.SetCommandScopeResolver(func(
					context.Context,
					terminal_svc.ResolveCommandScopeRequest,
				) (*terminal_svc.CommandScope, error) {
					return nil, nil
				})
			},
			wantErr: terminal_svc.ErrCommandScopeUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			localBackend := mocks.NewMockPTYBackend(ctrl)
			svc := terminal_svc.NewService(
				terminal_svc.NewBackendSelector(localBackend, nil),
				terminal_svc.NoopEmitter{},
			)
			tt.configure(svc)

			var response *terminal_svc.RunCommandResponse
			var err error
			require.NotPanics(t, func() {
				response, err = svc.RunCommand(context.Background(), terminal_svc.RunCommandRequest{
					TerminalID: "terminal-unavailable",
					SessionID:  70,
					Command:    "private-token-command",
					Cols:       80,
					Rows:       24,
				})
			})
			assert.Nil(t, response)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestService_RunCommand_GivenResolutionFailure_WhenStarted_ThenReturnsRPCErrorWithoutScopeOrLaunch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	resolveErr := errors.New("target resolution failed")
	resolveCalls := 0
	localBackend := mocks.NewMockPTYBackend(ctrl)
	factoryCalls := 0
	svc := terminal_svc.NewService(terminal_svc.NewBackendSelector(localBackend,
		func(string) (terminal_svc.PTYBackend, error) {
			factoryCalls++
			return mocks.NewMockPTYBackend(ctrl), nil
		}), terminal_svc.NoopEmitter{})
	svc.SetCommandScopeResolver(func(
		context.Context,
		terminal_svc.ResolveCommandScopeRequest,
	) (*terminal_svc.CommandScope, error) {
		resolveCalls++
		return nil, resolveErr
	})
	defer svc.Shutdown()
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	response, err := svc.RunCommand(ctx, terminal_svc.RunCommandRequest{
		TerminalID: "terminal-3",
		SessionID:  73,
		Command:    "pwd",
		Cols:       80,
		Rows:       24,
	})

	assert.Nil(t, response)
	require.ErrorIs(t, err, resolveErr)
	assert.Equal(t, 1, resolveCalls)
	assert.Equal(t, 0, factoryCalls)
	assert.Equal(t, 0, logs.Len())
}
