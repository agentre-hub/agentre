package piagent

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Given EOF is observed after the child process has already exited, when the
// stream reports process death and is then closed, Close must reuse the known
// exit result instead of waiting forever for a second process completion.
func TestStreamCloseAfterProcessExitWasObserved(t *testing.T) {
	tests := []struct {
		name    string
		waitErr error
		wantErr error
	}{
		{name: "clean exit", wantErr: ErrProcessDead},
		{name: "failed exit", waitErr: errors.New("exit status 2")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			proc := newFakeProcess(t)
			rpcProc := proc.rpcProcess()
			rpcProc.lines = bufio.NewScanner(strings.NewReader(""))
			proc.complete(tt.waitErr)
			<-rpcProc.done
			stream := newStream(rpcProc, 10*time.Millisecond)

			go stream.drain(context.Background())
			for stream.Next() {
			}
			if tt.wantErr != nil {
				require.ErrorIs(t, stream.Err(), tt.wantErr)
			} else {
				require.ErrorIs(t, stream.Err(), tt.waitErr)
			}

			closed := make(chan error, 1)
			go func() {
				closed <- stream.Close(context.Background())
			}()

			select {
			case err := <-closed:
				if tt.wantErr != nil {
					require.ErrorIs(t, err, tt.wantErr)
				} else {
					require.ErrorIs(t, err, tt.waitErr)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatal("Close blocked after process completion was already observed")
			}
		})
	}
}

func TestPiWritesAreInterruptedByCallerCancellation(t *testing.T) {
	tests := []struct {
		name    string
		blockAt int
		run     func(context.Context, *Client) error
	}{
		{
			name:    "command write",
			blockAt: 1,
			run: func(ctx context.Context, client *Client) error {
				_, err := client.PrepareStream(ctx, "must not be sent")
				return err
			},
		},
		{
			name: "prompt write",
			// get_state, the optional pre-prompt get_session_stats, then the prompt.
			blockAt: 3,
			run: func(ctx context.Context, client *Client) error {
				prepared, err := client.PrepareStream(context.Background(), strings.Repeat("x", 8*1024*1024))
				if err != nil {
					return err
				}
				_, err = prepared.Start(ctx)
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := newBlockingWriteCloser(tt.blockAt)
			proc := newBlockedWriteProcess(writer)
			client := New(WithRPCProcessRunnerForTesting(&blockedWriteRunner{proc: proc}))
			ctx, cancel := context.WithCancel(context.Background())
			resultC := make(chan error, 1)
			go func() { resultC <- tt.run(ctx, client) }()

			<-writer.blocked
			cancel()
			select {
			case err := <-resultC:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(300 * time.Millisecond):
				_ = writer.Close()
				<-resultC
				t.Fatal("canceled Pi write remained blocked")
			}
			select {
			case <-writer.closed:
			case <-time.After(100 * time.Millisecond):
				t.Fatal("canceling a blocked Pi write did not close stdin")
			}
		})
	}
}

func TestPreparedStreamCloseInterruptsBlockedPromptWrite(t *testing.T) {
	// get_state, the optional pre-prompt get_session_stats, then the prompt.
	writer := newBlockingWriteCloser(3)
	proc := newBlockedWriteProcess(writer)
	client := New(WithRPCProcessRunnerForTesting(&blockedWriteRunner{proc: proc}))
	prepared, err := client.PrepareStream(context.Background(), strings.Repeat("x", 8*1024*1024))
	require.NoError(t, err)

	startErrC := make(chan error, 1)
	go func() {
		_, startErr := prepared.Start(context.Background())
		startErrC <- startErr
	}()
	<-writer.blocked

	closeErrC := make(chan error, 1)
	go func() { closeErrC <- prepared.Close(context.Background()) }()
	select {
	case closeErr := <-closeErrC:
		require.NoError(t, closeErr)
	case <-time.After(300 * time.Millisecond):
		_ = writer.Close()
		t.Fatal("Close remained blocked behind a prompt write")
	}
	select {
	case startErr := <-startErrC:
		require.Error(t, startErr)
	case <-time.After(300 * time.Millisecond):
		_ = writer.Close()
		<-startErrC
		t.Fatal("process Close did not interrupt the blocked prompt writer")
	}
	select {
	case <-writer.closed:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("process Close did not close stdin")
	}

	repeatedClose := make(chan error, 1)
	go func() { repeatedClose <- prepared.Close(context.Background()) }()
	select {
	case err := <-repeatedClose:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("repeated Close was not idempotent and bounded")
	}
}

// Given closing either process pipe can let the process-group leader exit
// before its tool child, when termination has no grace period, then the tree
// kill must happen while the leader can still address the whole group.
func TestZeroGraceTerminationKillsProcessTreeBeforeClosingPipes(t *testing.T) {
	process := newExitOnPipeCloseProcess()
	proc := &rpcProcess{
		handle: process,
		stdin:  process.stdin,
		lines:  &exitOnStopScanner{process: process},
		stderr: &lockedBuffer{},
		done:   process.done,
	}

	require.NoError(t, proc.terminate(context.Background(), 0))
	assert.Equal(t, []string{"signal", "kill", "stop", "close"}, process.events)
	assert.True(t, process.childKilled, "closing a process pipe first loses the group leader and leaves its child alive")
}

type exitOnPipeCloseProcess struct {
	stdin        *exitOnCloseWriter
	done         chan struct{}
	events       []string
	leaderExited bool
	childKilled  bool
}

func newExitOnPipeCloseProcess() *exitOnPipeCloseProcess {
	process := &exitOnPipeCloseProcess{done: make(chan struct{})}
	process.stdin = &exitOnCloseWriter{process: process}
	return process
}

func (p *exitOnPipeCloseProcess) Stdin() io.Writer { return p.stdin }
func (*exitOnPipeCloseProcess) Stdout() io.Reader  { return strings.NewReader("") }
func (*exitOnPipeCloseProcess) Stderr() io.Reader  { return strings.NewReader("") }
func (p *exitOnPipeCloseProcess) Wait() error {
	<-p.done
	return nil
}
func (p *exitOnPipeCloseProcess) Kill() error {
	p.events = append(p.events, "kill")
	p.childKilled = !p.leaderExited
	return nil
}
func (p *exitOnPipeCloseProcess) Signal(os.Signal) error {
	p.events = append(p.events, "signal")
	return nil
}

func (p *exitOnPipeCloseProcess) exitLeader(event string) {
	p.events = append(p.events, event)
	if !p.leaderExited {
		p.leaderExited = true
		close(p.done)
	}
}

type exitOnStopScanner struct {
	process *exitOnPipeCloseProcess
}

func (*exitOnStopScanner) Scan() bool    { return false }
func (*exitOnStopScanner) Bytes() []byte { return nil }
func (*exitOnStopScanner) Text() string  { return "" }
func (*exitOnStopScanner) Err() error    { return nil }
func (s *exitOnStopScanner) Stop()       { s.process.exitLeader("stop") }

type exitOnCloseWriter struct {
	process *exitOnPipeCloseProcess
}

func (*exitOnCloseWriter) Write(data []byte) (int, error) { return len(data), nil }
func (w *exitOnCloseWriter) Close() error {
	w.process.exitLeader("close")
	return nil
}

type blockingWriteCloser struct {
	mu        sync.Mutex
	blockAt   int
	writes    int
	blocked   chan struct{}
	release   chan struct{}
	closed    chan struct{}
	blockOnce sync.Once
	closeOnce sync.Once
}

func newBlockingWriteCloser(blockAt int) *blockingWriteCloser {
	return &blockingWriteCloser{
		blockAt: blockAt,
		blocked: make(chan struct{}),
		release: make(chan struct{}),
		closed:  make(chan struct{}),
	}
}

func (w *blockingWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	w.writes++
	shouldBlock := w.writes == w.blockAt
	w.mu.Unlock()
	if !shouldBlock {
		return len(p), nil
	}
	w.blockOnce.Do(func() { close(w.blocked) })
	<-w.release
	return 0, io.ErrClosedPipe
}

func (w *blockingWriteCloser) Close() error {
	w.closeOnce.Do(func() {
		close(w.release)
		close(w.closed)
	})
	return nil
}

type blockedWriteProcess struct {
	stdin      *blockingWriteCloser
	stdout     io.Reader
	done       chan struct{}
	finishOnce sync.Once
}

func newBlockedWriteProcess(stdin *blockingWriteCloser) *blockedWriteProcess {
	return &blockedWriteProcess{
		stdin: stdin,
		stdout: strings.NewReader(strings.Join([]string{
			`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"blocked-write-session"}}`,
			`{"type":"response","command":"prompt","success":true}`,
		}, "\n") + "\n"),
		done: make(chan struct{}),
	}
}

func (p *blockedWriteProcess) Stdin() io.Writer  { return p.stdin }
func (p *blockedWriteProcess) Stdout() io.Reader { return p.stdout }
func (*blockedWriteProcess) Stderr() io.Reader   { return strings.NewReader("") }
func (p *blockedWriteProcess) Wait() error {
	<-p.done
	return nil
}
func (p *blockedWriteProcess) finish() error {
	p.finishOnce.Do(func() { close(p.done) })
	return nil
}
func (p *blockedWriteProcess) Kill() error            { return p.finish() }
func (p *blockedWriteProcess) Signal(os.Signal) error { return p.finish() }

type blockedWriteRunner struct{ proc *blockedWriteProcess }

func (r *blockedWriteRunner) Start(context.Context, procOptions) (processHandle, error) {
	return r.proc, nil
}

func TestStreamClose(t *testing.T) {
	convey.Convey("Given a pi-agent text probe that already reached agent_settled", t, func() {
		runner := &fakeRunner{process: newFakeProcess(t)}
		runner.process.stdout = strings.NewReader(strings.Join([]string{
			`{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"test-native-session"}}`,
			`{"type":"response","command":"prompt","success":true}`,
			`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"pong"}}`,
			`{"type":"agent_end","messages":[],"willRetry":false}`,
			`{"type":"agent_settled"}`,
			"",
		}, "\n"))
		runner.process.finishOnSignal(interruptExitError(t))
		client := New(
			WithRPCProcessRunnerForTesting(runner),
			WithKillGrace(time.Second),
		)

		convey.Convey("When Text closes the completed RPC stream, then SIGINT cleanup is not surfaced as failure", func() {
			text, err := client.Text(context.Background(), "ping")

			convey.So(err, convey.ShouldBeNil)
			convey.So(text, convey.ShouldEqual, "pong")
			assert.True(t, runner.process.signaled, "completed text probe should interrupt the lingering RPC process during cleanup")
		})
	})

	convey.Convey("Given a running pi-agent RPC stream", t, func() {
		proc := newFakeProcess(t)
		stream := newStream(proc.rpcProcess(), time.Second)

		convey.Convey("When Close interrupts the RPC process and it exits from SIGINT, then Close succeeds", func() {
			proc.finishOnSignal(interruptExitError(t))

			err := stream.Close(context.Background())

			convey.So(err, convey.ShouldBeNil)
			assert.True(t, proc.signaled, "running process should be interrupted during Close")
		})
	})

	convey.Convey("Given a running pi-agent RPC stream", t, func() {
		proc := newFakeProcess(t)
		stream := newStream(proc.rpcProcess(), time.Second)

		convey.Convey("When Close interrupts the RPC process and it exits with another error, then Close returns that error", func() {
			proc.finishOnSignal(errors.New("exit status 2"))

			err := stream.Close(context.Background())

			convey.So(err, convey.ShouldNotBeNil)
			assert.Contains(t, err.Error(), "exit status 2")
			assert.True(t, proc.signaled, "running process should be interrupted during Close")
		})
	})
}

func TestCanceledAcceptedStreamSettlesAnchorBeforeTerminatingProcessTree(t *testing.T) {
	stream, cancel, parentPID, toolPID := startAcceptedRealStream(t, true)

	// Exercise the real cancellation race, not a scripted aborted frame on a live
	// background context: cancellation and the explicit interrupt overlap without
	// a timing sleep, while the accepted prompt keeps one scanner through settlement.
	cancel()
	require.NoError(t, stream.Interrupt(context.Background()))

	finished := make(chan struct{})
	go func() {
		for stream.Next() {
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("accepted canceled stream did not finish within the bounded settlement window")
	}

	assert.Equal(t, "turn-user-exact", stream.UserAnchor())
	assert.ErrorContains(t, stream.Err(), "abort")
	assertProcessGoneEventually(t, parentPID)
	assertProcessGoneEventually(t, toolPID)

	started := time.Now()
	assert.Error(t, stream.Close(context.Background()))
	assert.Less(t, time.Since(started), time.Second, "Close must remain responsive after settlement")
	assertProcessGoneEventually(t, parentPID)
	assertProcessGoneEventually(t, toolPID)

	started = time.Now()
	assert.ErrorContains(t, stream.Close(context.Background()), "abort")
	assert.Less(t, time.Since(started), 100*time.Millisecond, "repeated Close must be idempotent")
}

func TestCanceledAcceptedStreamWithoutSettlementTerminatesTreeWithinBound(t *testing.T) {
	stream, cancel, parentPID, toolPID := startAcceptedRealStream(t, false)

	cancel()
	require.NoError(t, stream.Interrupt(context.Background()))

	finished := make(chan struct{})
	go func() {
		for stream.Next() {
		}
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("canceled stream waited forever when Pi omitted abort settlement")
	}
	assert.ErrorIs(t, stream.Err(), context.Canceled)
	assertProcessGoneEventually(t, parentPID)
	assertProcessGoneEventually(t, toolPID)

	started := time.Now()
	_ = stream.Close(context.Background())
	assert.Less(t, time.Since(started), time.Second, "forced termination must stay bounded")
	assertProcessGoneEventually(t, parentPID)
	assertProcessGoneEventually(t, toolPID)
}

func startAcceptedRealStream(t *testing.T, settleOnAbort bool) (*Stream, context.CancelFunc, int, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the real shell process-tree regression uses Unix signals")
	}

	dir := t.TempDir()
	parentPIDFile := filepath.Join(dir, "parent.pid")
	toolPIDFile := filepath.Join(dir, "tool.pid")
	toolScript := filepath.Join(dir, "tool.sh")
	serverScript := filepath.Join(dir, "pi-rpc.sh")
	require.NoError(t, os.WriteFile(toolScript, []byte("#!/bin/sh\nexec sleep 600\n"), 0o755))

	settlement := ""
	if settleOnAbort {
		settlement = `
			printf '%s\n' '{"type":"agent_end","messages":[{"role":"assistant","content":[],"stopReason":"aborted","errorMessage":"turn stopped"}],"willRetry":false}'
			printf '%s\n' '{"type":"agent_settled"}'`
	}
	script := `#!/bin/sh
trap '' INT TERM HUP
printf '%s\n' "$$" > "$PI_PARENT_PID_FILE"
prompted=0
while IFS= read -r line; do
	case "$line" in
		*'"type":"get_state"'*)
			printf '%s\n' '{"id":"session-state","type":"response","command":"get_state","success":true,"data":{"sessionId":"session-real"}}'
			;;
		*'"type":"get_entries"'*)
			if [ "$prompted" -eq 0 ]; then
				printf '%s\n' '{"id":"session-entries-before","type":"response","command":"get_entries","success":true,"data":{"entries":[],"leafId":null}}'
			else
				printf '%s\n' '{"id":"session-entries-after","type":"response","command":"get_entries","success":true,"data":{"entries":[{"type":"message","id":"turn-user-exact","parentId":null,"message":{"role":"user","content":"hello"}}],"leafId":"turn-user-exact"}}'
			fi
			;;
		*'"type":"prompt"'*)
			prompted=1
			"$PI_TOOL_SCRIPT" >/dev/null 2>&1 &
			printf '%s\n' "$!" > "$PI_TOOL_PID_FILE"
			printf '%s\n' '{"type":"response","command":"prompt","success":true}'
			;;
		*'"type":"abort"'*)` + settlement + `
			;;
		*'"type":"get_session_stats"'*)
			printf '%s\n' '{"type":"response","command":"get_session_stats","success":true,"data":{}}'
			;;
	esac
done
`
	require.NoError(t, os.WriteFile(serverScript, []byte(script), 0o755))

	client := New(
		WithBinary(serverScript),
		WithEnv(map[string]string{
			"PI_PARENT_PID_FILE": parentPIDFile,
			"PI_TOOL_PID_FILE":   toolPIDFile,
			"PI_TOOL_SCRIPT":     toolScript,
		}),
		WithKillGrace(100*time.Millisecond),
	)
	client.startupTimeout = 5 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	prepared, err := client.PrepareStream(ctx, "hello", RunCaptureUserAnchor())
	require.NoError(t, err)
	stream, err := prepared.Start(ctx)
	require.NoError(t, err)

	// The script writes each PID before the RPC response that PrepareStream or
	// Start awaits, so both files are ordered protocol output rather than an
	// eventually-consistent side channel.
	parentPID := readPID(t, parentPIDFile)
	toolPID := readPID(t, toolPIDFile)
	t.Cleanup(func() {
		cancel()
		terminatePID(parentPID)
		terminatePID(toolPID)
	})
	return stream, cancel, parentPID, toolPID
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // G304: path is a test-owned file under t.TempDir.
	require.NoError(t, err)
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	require.NoError(t, err)
	require.Positive(t, pid)
	return pid
}

func assertProcessGoneEventually(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d remained alive after the termination bound", pid)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// kill -0 also succeeds for a dead process that remains as a zombie until
	// its new parent reaps it. ps lets this integration test distinguish work
	// that is still running from an already-terminated process-table entry.
	output, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output() //nolint:gosec // G204: fixed executable with an OS-assigned test PID.
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}

func terminatePID(pid int) {
	if pid <= 0 {
		return
	}
	_ = exec.Command("kill", "-KILL", strconv.Itoa(pid)).Run() //nolint:gosec // G204: fixed cleanup command with an OS-assigned test PID.
}

type fakeProcess struct {
	t       *testing.T
	stdout  *strings.Reader
	stderr  *strings.Reader
	done    chan struct{}
	waitErr error
	signalC chan os.Signal

	signaled bool
}

func newFakeProcess(t *testing.T) *fakeProcess {
	t.Helper()
	return &fakeProcess{
		t:       t,
		stdout:  strings.NewReader(""),
		stderr:  strings.NewReader(""),
		done:    make(chan struct{}),
		signalC: make(chan os.Signal, 1),
	}
}

func (f *fakeProcess) rpcProcess() *rpcProcess {
	stderrDone := make(chan struct{})
	close(stderrDone)
	p := &rpcProcess{
		handle:     f,
		stdin:      io.Discard,
		lines:      nil,
		stderr:     &lockedBuffer{},
		stderrDone: stderrDone,
		done:       make(chan struct{}),
	}
	go p.awaitExit()
	return p
}

func (f *fakeProcess) complete(err error) {
	f.waitErr = err
	close(f.done)
}

func (f *fakeProcess) finishOnSignal(err error) {
	f.t.Helper()
	go func() {
		<-f.signalC
		f.complete(err)
	}()
}

func (f *fakeProcess) Stdin() io.Writer  { return io.Discard }
func (f *fakeProcess) Stdout() io.Reader { return f.stdout }
func (f *fakeProcess) Stderr() io.Reader { return f.stderr }

func (f *fakeProcess) Wait() error {
	<-f.done
	return f.waitErr
}

func (f *fakeProcess) Kill() error { return nil }

func (f *fakeProcess) Signal(sig os.Signal) error {
	f.signaled = true
	select {
	case f.signalC <- sig:
	default:
	}
	return nil
}

func interruptExitError(t *testing.T) error {
	t.Helper()
	cmd := exec.Command("sh", "-c", "kill -INT $$")
	err := cmd.Run()
	require.Error(t, err)
	return err
}

type fakeRunner struct {
	process *fakeProcess
}

func (r *fakeRunner) Start(context.Context, procOptions) (processHandle, error) {
	return r.process, nil
}

func TestFakeProcessImplementsProcessHandle(t *testing.T) {
	var _ processHandle = (*fakeProcess)(nil)
}
