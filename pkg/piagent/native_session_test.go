package piagent

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamDiscoversNativeSessionBeforePrompt(t *testing.T) {
	// Given Pi starts a new persistent native session,
	// When Agentre opens a prompt stream,
	// Then it reads get_state before sending the prompt and exposes Pi's native ID.
	script := strings.Join([]string{
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"pi-native-123","sessionFile":"/home/me/.pi/agent/sessions/project/session.jsonl"}}`,
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
		"",
	}, "\n")
	client, proc := newCaptureClient(script)

	stream, err := client.Stream(context.Background(), "hello")
	require.NoError(t, err)
	for stream.Next() {
	}

	assert.Equal(t, "pi-native-123", stream.SessionID())
	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 4)
	assert.Equal(t, "get_state", frames[0]["type"])
	assert.Equal(t, "get_session_stats", frames[1]["type"])
	assert.Equal(t, "prompt", frames[2]["type"])
	assert.Equal(t, "get_session_stats", frames[3]["type"])
}

func TestPrepareStreamRejectsInvalidPrePromptTreeBoundary(t *testing.T) {
	tests := []struct {
		name    string
		entries string
	}{
		{
			name:    "Given entries exist without a leaf, when preparing the prompt boundary, then startup fails before prompt",
			entries: `{"entries":[{"type":"message","id":"history-user","parentId":null}],"leafId":null}`,
		},
		{
			name:    "Given the leaf is not an entry, when preparing the prompt boundary, then startup fails before prompt",
			entries: `{"entries":[{"type":"message","id":"history-user","parentId":null}],"leafId":"missing-leaf"}`,
		},
		{
			name:    "Given entry IDs are duplicated, when preparing the prompt boundary, then startup fails before prompt",
			entries: `{"entries":[{"type":"message","id":"duplicate","parentId":null},{"type":"message","id":"duplicate","parentId":null}],"leafId":"duplicate"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script := strings.Join([]string{
				`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"pi-native-boundary"}}`,
				`{"id":"session-entries-before","type":"response","command":"get_entries","success":true,"data":` + tt.entries + `}`,
				`{"type":"response","command":"prompt","success":true}`,
				"",
			}, "\n")
			client, proc := newCaptureClient(script)

			prepared, err := client.PrepareStream(context.Background(), "must not be sent", RunCaptureUserAnchor())

			assert.Nil(t, prepared)
			require.ErrorContains(t, err, "invalid pre-prompt tree boundary")
			frames := stdinFrames(t, proc.stdin.String())
			require.Len(t, frames, 2)
			assert.Equal(t, "get_state", frames[0]["type"])
			assert.Equal(t, "get_entries", frames[1]["type"])
		})
	}
}

func TestPrepareStreamRejectsWhitespacePaddedForkAnchorWithoutStartingProcess(t *testing.T) {
	client, proc, runner := newSingleProcessCaptureClient("")
	client.session = "pi-native-existing"

	prepared, err := client.PrepareStream(context.Background(), "must not be sent", RunForkAnchor(" fork-user "))

	assert.Nil(t, prepared)
	require.ErrorContains(t, err, "invalid fork anchor")
	assert.Zero(t, runner.starts)
	assert.Empty(t, proc.stdin.String())
}

func TestPreparedStreamStartRequiresAcceptedPromptResponse(t *testing.T) {
	tests := []struct {
		name      string
		response  string
		wantError string
	}{
		{
			name:      "Given Pi rejects the prompt, when the prepared stream starts, then startup fails without exposing the payload",
			response:  `{"type":"response","command":"prompt","success":false,"error":"secret prompt rejected","data":{"token":"private-token"}}`,
			wantError: "piagent rpc prompt failed",
		},
		{
			name:      "Given Pi cancels the prompt, when the prepared stream starts, then startup fails explicitly",
			response:  `{"type":"response","command":"prompt","success":true,"data":{"cancelled":true,"message":"secret prompt"}}`, //nolint:misspell // Pi RPC field uses British spelling.
			wantError: "piagent: prompt was canceled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, proc := newCaptureClient(tt.response + "\n")
			prepared, err := client.PrepareStream(context.Background(), "private prompt")
			require.NoError(t, err)
			t.Cleanup(func() { _ = prepared.Close(context.Background()) })

			stream, startErr := prepared.Start(context.Background())

			assert.Nil(t, stream)
			require.EqualError(t, startErr, tt.wantError)
			assert.NotContains(t, startErr.Error(), "secret")
			assert.NotContains(t, startErr.Error(), "private-token")
			frames := stdinFrames(t, proc.stdin.String())
			require.Len(t, frames, 3)
			assert.Equal(t, "prompt", frames[2]["type"])
		})
	}
}

func TestPreparedStreamStartReturnsSanitizedProcessExitBeforeAcknowledgement(t *testing.T) {
	proc := &captureProc{
		stdin:  &lockedBuffer{},
		stdout: strings.NewReader(`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"pi-native-exit"}}` + "\n"),
		stderr: strings.NewReader("private-token from provider stderr"),
		done:   make(chan error, 1),
	}
	proc.done <- errors.New("exit status 17")
	client := New(WithRPCProcessRunnerForTesting(&captureRunner{proc: proc}))
	prepared, err := client.PrepareStream(context.Background(), "private prompt")
	require.NoError(t, err)
	t.Cleanup(func() { _ = prepared.Close(context.Background()) })

	stream, startErr := prepared.Start(context.Background())

	assert.Nil(t, stream)
	var exitErr *ExitError
	require.ErrorAs(t, startErr, &exitErr)
	assert.NotContains(t, startErr.Error(), "private-token")
	assert.Empty(t, exitErr.Stderr)
	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 3)
	assert.Equal(t, "prompt", frames[2]["type"])
}

func TestPreparedStreamStartHandsPreAcknowledgementEventsToDrain(t *testing.T) {
	script := strings.Join([]string{
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"pi-native-handoff"}}`,
		`{"type":"extension_ui_request","id":"notify-before-ack","method":"notify","message":"private extension payload"}`,
		`{"type":"message_start","message":{"role":"user","content":"queued before ack"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"before ack"}}`,
		`{"type":"response","command":"prompt","success":true}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":" after ack"}}`,
		`{"type":"agent_end","messages":[],"willRetry":false}`,
		`{"type":"agent_settled"}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
		"",
	}, "\n")
	client, _ := newCaptureClient(script)
	prepared, err := client.PrepareStream(context.Background(), "handoff")
	require.NoError(t, err)

	stream, err := prepared.Start(context.Background())
	require.NoError(t, err)
	var (
		kinds []EventKind
		texts []string
	)
	for stream.Next() {
		event := stream.Event()
		kinds = append(kinds, event.Kind)
		if event.Text != "" {
			texts = append(texts, event.Text)
		}
	}

	assert.Equal(t, []EventKind{EventUserMessage, EventTextDelta, EventTextDelta, EventDone}, kinds)
	assert.Equal(t, []string{"queued before ack", "before ack", " after ack"}, texts)
}

func TestPreparedStreamCloseBeforeStartSendsNoPrompt(t *testing.T) {
	// Given preparation has explicit ownership of a live Pi process,
	// When the owner closes it before Start,
	// Then repeated cleanup is safe and a later Start cannot send the prompt.
	client, proc := newCaptureClient(
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"pi-native-abandoned"}}` + "\n",
	)
	prepared, err := client.PrepareStream(context.Background(), "must not be sent")
	require.NoError(t, err)
	assert.Equal(t, "pi-native-abandoned", prepared.SessionID())

	require.NoError(t, prepared.Close(context.Background()))
	require.NoError(t, prepared.Close(context.Background()))
	stream, startErr := prepared.Start(context.Background())

	assert.Nil(t, stream)
	require.ErrorIs(t, startErr, errStreamClosed)
	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 2)
	assert.Equal(t, "get_state", frames[0]["type"])
	assert.Equal(t, "get_session_stats", frames[1]["type"])
}

func TestCompactDiscoversNativeSessionBeforeCommand(t *testing.T) {
	// Given an existing native Pi session is opened for compaction,
	// When Agentre starts the compact stream,
	// Then it confirms the native ID before issuing compact.
	script := strings.Join([]string{
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"pi-native-compact"}}`,
		`{"type":"response","command":"compact","success":true,"data":{"summary":"done"}}`,
		`{"type":"response","command":"get_session_stats","success":true,"data":{}}`,
		"",
	}, "\n")
	client, proc := newCaptureClient(script)
	client.session = "pi-native-compact"

	stream, err := client.Compact(context.Background(), "pi-native-compact")
	require.NoError(t, err)
	for stream.Next() {
	}

	assert.Equal(t, "pi-native-compact", stream.SessionID())
	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 3)
	assert.Equal(t, "get_state", frames[0]["type"])
	assert.Equal(t, "compact", frames[1]["type"])
	assert.Equal(t, "get_session_stats", frames[2]["type"])
}

func TestStreamRejectsUnexpectedResumedSessionID(t *testing.T) {
	// Given Agentre asked Pi to resume one native session,
	// When get_state reports a different identity,
	// Then startup fails closed before the prompt is sent.
	client, proc := newCaptureClient(
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"pi-native-other"}}` + "\n",
	)
	client.session = "pi-native-expected"

	stream, err := client.Stream(context.Background(), "must not be sent")

	require.Error(t, err)
	assert.Nil(t, stream)
	assert.Contains(t, err.Error(), "unexpected session id")
	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 1)
	assert.Equal(t, "get_state", frames[0]["type"])
}

func TestStreamRejectsResumedSessionIDWithExpectedPrefix(t *testing.T) {
	// Given Pi resolves a different native session whose ID merely starts with
	// the persisted ID, when startup validates the identity, then it fails
	// closed instead of treating the prefix match as the same session.
	client, proc := newCaptureClient(
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"pi-native-expected-other"}}` + "\n",
	)
	client.session = "pi-native-expected"

	stream, err := client.Stream(context.Background(), "must not be sent")

	require.Error(t, err)
	assert.Nil(t, stream)
	assert.Contains(t, err.Error(), "unexpected session id")
	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 1)
	assert.Equal(t, "get_state", frames[0]["type"])
}

func TestProcessExitClassifiesMissingNativeSession(t *testing.T) {
	// Given Pi rejects --session because the native ID no longer exists,
	// When the subprocess error is classified,
	// Then callers can recover through a stable session-not-found sentinel.
	err := wrapExitError(errors.New("exit status 1"), "No session found matching 'pi-native-gone'")

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound)
	assert.Contains(t, err.Error(), "pi-native-gone")
}

func TestStreamWaitsForMissingNativeSessionExitClassification(t *testing.T) {
	// Given Pi closes stdout before its delayed Wait result and stderr are
	// available, when startup observes EOF, then it waits for the process result
	// and preserves the session-not-found classification.
	proc := newFakeProcess(t)
	proc.stderr = strings.NewReader("No session found matching 'pi-native-gone'")
	client := New(
		WithRPCProcessRunnerForTesting(&fakeRunner{process: proc}),
		WithSession("pi-native-gone"),
		WithKillGrace(time.Second),
	)
	go func() {
		time.Sleep(150 * time.Millisecond)
		proc.complete(errors.New("exit status 1"))
	}()

	stream, err := client.Stream(context.Background(), "must not be sent")

	assert.Nil(t, stream)
	require.ErrorIs(t, err, ErrSessionNotFound)
}

type silentStartupRunner struct {
	process processHandle
}

func (r *silentStartupRunner) Start(context.Context, procOptions) (processHandle, error) {
	return r.process, nil
}

type promptNotifyingBuffer struct {
	lockedBuffer
	promptWritten chan struct{}
	once          sync.Once
}

func newPromptNotifyingBuffer() *promptNotifyingBuffer {
	return &promptNotifyingBuffer{promptWritten: make(chan struct{})}
}

func (b *promptNotifyingBuffer) Write(p []byte) (int, error) {
	n, err := b.lockedBuffer.Write(p)
	if strings.Contains(string(p), `"type":"prompt"`) {
		b.once.Do(func() { close(b.promptWritten) })
	}
	return n, err
}

type silentStartupProcess struct {
	stdin   *promptNotifyingBuffer
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	exited  chan struct{}

	once    sync.Once
	waitErr error
}

func newSilentStartupProcess(prefix string) *silentStartupProcess {
	stdoutR, stdoutW := io.Pipe()
	proc := &silentStartupProcess{
		stdin:   newPromptNotifyingBuffer(),
		stdoutR: stdoutR,
		stdoutW: stdoutW,
		exited:  make(chan struct{}),
	}
	if prefix != "" {
		go func() {
			_, _ = io.WriteString(stdoutW, prefix)
		}()
	}
	return proc
}

func (p *silentStartupProcess) Stdin() io.Writer  { return p.stdin }
func (p *silentStartupProcess) Stdout() io.Reader { return p.stdoutR }
func (p *silentStartupProcess) Stderr() io.Reader { return strings.NewReader("") }
func (p *silentStartupProcess) Wait() error {
	<-p.exited
	return p.waitErr
}
func (p *silentStartupProcess) Kill() error {
	p.finish(errors.New("signal: killed"))
	return nil
}
func (p *silentStartupProcess) Signal(os.Signal) error {
	p.finish(errors.New("signal: interrupt"))
	return nil
}
func (p *silentStartupProcess) finish(err error) {
	p.once.Do(func() {
		p.waitErr = err
		_ = p.stdoutW.Close()
		close(p.exited)
	})
}

func TestPrepareStreamStartupHonorsCallerDeadlineWhilePiIsSilent(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		runOption RunOption
		wantTypes []string
	}{
		{
			name:      "Given get_state stays silent, when the startup deadline expires, then startup stops before prompt",
			wantTypes: []string{"get_state"},
		},
		{
			name:      "Given fork stays silent, when the startup deadline expires, then startup stops before prompt",
			prefix:    `{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-old"}}` + "\n",
			runOption: RunForkAnchor("fork-user"),
			wantTypes: []string{"get_state", "fork"},
		},
		{
			name:      "Given get_entries stays silent, when the startup deadline expires, then startup stops before prompt",
			prefix:    `{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-old"}}` + "\n",
			runOption: RunCaptureUserAnchor(),
			wantTypes: []string{"get_state", "get_entries"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := newSilentStartupProcess(tt.prefix)
			client := New(
				WithRPCProcessRunnerForTesting(&silentStartupRunner{process: proc}),
				WithSession("session-old"),
				WithKillGrace(50*time.Millisecond),
			)
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()

			type startupResult struct {
				prepared *PreparedStream
				err      error
			}
			resultC := make(chan startupResult, 1)
			go func() {
				var opts []RunOption
				if tt.runOption != nil {
					opts = append(opts, tt.runOption)
				}
				prepared, err := client.PrepareStream(ctx, "must not be sent", opts...)
				resultC <- startupResult{prepared: prepared, err: err}
			}()

			var result startupResult
			select {
			case result = <-resultC:
			case <-time.After(250 * time.Millisecond):
				t.Error("Pi startup remained blocked after its caller deadline")
				_ = proc.Signal(interruptSignal())
				result = <-resultC
			}

			assert.Nil(t, result.prepared)
			require.ErrorIs(t, result.err, context.DeadlineExceeded)
			frames := stdinFrames(t, proc.stdin.String())
			require.Len(t, frames, len(tt.wantTypes))
			for i, wantType := range tt.wantTypes {
				assert.Equal(t, wantType, frames[i]["type"])
			}
			for _, frame := range frames {
				assert.NotEqual(t, "prompt", frame["type"])
			}
			select {
			case <-proc.exited:
			case <-time.After(time.Second):
				t.Fatal("Pi startup process was not released after cancellation")
			}
		})
	}
}

func TestPreparedStreamStartHonorsPromptAcknowledgementCancellationAndTimeout(t *testing.T) {
	tests := []struct {
		name       string
		cancel     bool
		wantError  error
		startLimit time.Duration
	}{
		{
			name:       "Given Pi accepts the prompt frame but stays silent, when the caller cancels, then prepared startup stops",
			cancel:     true,
			wantError:  context.Canceled,
			startLimit: time.Second,
		},
		{
			name:       "Given Pi accepts the prompt frame but stays silent, when the startup timeout expires, then prepared startup stops",
			wantError:  context.DeadlineExceeded,
			startLimit: 40 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := newSilentStartupProcess(
				`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-old"}}` + "\n",
			)
			client := New(
				WithRPCProcessRunnerForTesting(&silentStartupRunner{process: proc}),
				WithSession("session-old"),
				WithKillGrace(50*time.Millisecond),
			)
			client.startupTimeout = tt.startLimit
			prepared, err := client.PrepareStream(context.Background(), "accepted by stdin")
			require.NoError(t, err)
			t.Cleanup(func() { _ = prepared.Close(context.Background()) })

			startCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type startResult struct {
				stream *Stream
				err    error
			}
			resultC := make(chan startResult, 1)
			go func() {
				stream, startErr := prepared.Start(startCtx)
				resultC <- startResult{stream: stream, err: startErr}
			}()

			select {
			case <-proc.stdin.promptWritten:
			case <-time.After(time.Second):
				t.Fatal("prepared stream did not send the prompt frame")
			}
			if tt.cancel {
				cancel()
			}

			var result startResult
			select {
			case result = <-resultC:
			case <-time.After(250 * time.Millisecond):
				t.Fatal("prepared startup remained blocked after cancellation or timeout")
			}
			assert.Nil(t, result.stream)
			require.ErrorIs(t, result.err, tt.wantError)
			frames := stdinFrames(t, proc.stdin.String())
			require.Len(t, frames, 3)
			assert.Equal(t, "get_state", frames[0]["type"])
			assert.Equal(t, "get_session_stats", frames[1]["type"])
			assert.Equal(t, "prompt", frames[2]["type"])
			select {
			case <-proc.exited:
			case <-time.After(time.Second):
				t.Fatal("failed prepared startup did not release the Pi process")
			}
		})
	}
}

func TestPrepareStreamUsesBoundedStartupTimeoutWithoutCallerDeadline(t *testing.T) {
	proc := newSilentStartupProcess("")
	client := New(
		WithRPCProcessRunnerForTesting(&silentStartupRunner{process: proc}),
		WithKillGrace(50*time.Millisecond),
	)
	client.startupTimeout = 40 * time.Millisecond

	type startupResult struct {
		prepared *PreparedStream
		err      error
	}
	resultC := make(chan startupResult, 1)
	go func() {
		prepared, err := client.PrepareStream(context.Background(), "must not be sent")
		resultC <- startupResult{prepared: prepared, err: err}
	}()

	var result startupResult
	select {
	case result = <-resultC:
	case <-time.After(250 * time.Millisecond):
		t.Error("Pi startup ignored its bounded default timeout")
		_ = proc.Signal(interruptSignal())
		result = <-resultC
	}
	assert.Nil(t, result.prepared)
	require.ErrorIs(t, result.err, context.DeadlineExceeded)
	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 1)
	assert.Equal(t, "get_state", frames[0]["type"])
	select {
	case <-proc.exited:
	case <-time.After(time.Second):
		t.Fatal("timed-out Pi startup process was not released")
	}
}

func TestStreamRejectsEmptyNativeSessionBeforePrompt(t *testing.T) {
	// Given Pi cannot report a durable native session identity,
	// When Agentre opens a prompt stream,
	// Then startup fails closed and no prompt is sent.
	client, proc := newCaptureClient(
		`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":""}}` + "\n",
	)

	stream, err := client.Stream(context.Background(), "must not be sent")

	require.Error(t, err)
	assert.Nil(t, stream)
	assert.Contains(t, err.Error(), "empty session id")
	frames := stdinFrames(t, proc.stdin.String())
	require.Len(t, frames, 1)
	assert.Equal(t, "get_state", frames[0]["type"])
}
