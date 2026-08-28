package remote

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pkgpty "github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"

	"github.com/stretchr/testify/require"
)

type contextWaitingClient struct {
	data chan protocol.TerminalDataEvent
	exit chan protocol.TerminalExitEvent

	active      atomic.Int32
	writeCalls  atomic.Int32
	resizeCalls atomic.Int32

	mu             sync.Mutex
	deadlineBudget []time.Duration
}

func newContextWaitingClient() *contextWaitingClient {
	return &contextWaitingClient{
		data: make(chan protocol.TerminalDataEvent),
		exit: make(chan protocol.TerminalExitEvent),
	}
}

func (c *contextWaitingClient) Call(ctx context.Context, method string, params any, out any) error {
	switch method {
	case "terminal.open":
		terminalID := params.(protocol.TerminalOpenParams).TerminalID
		out.(*protocol.TerminalOpenResult).TerminalID = terminalID
		return nil
	case "terminal.write", "terminal.resize":
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("operation context has no deadline")
		}
		c.mu.Lock()
		c.deadlineBudget = append(c.deadlineBudget, time.Until(deadline))
		c.mu.Unlock()
		if method == "terminal.write" {
			c.writeCalls.Add(1)
		} else {
			c.resizeCalls.Add(1)
		}
		c.active.Add(1)
		defer c.active.Add(-1)
		<-ctx.Done()
		return ctx.Err()
	case "terminal.close":
		return nil
	default:
		return fmt.Errorf("unexpected method %s", method)
	}
}

func (c *contextWaitingClient) Subscribe(string) Subscription {
	return Subscription{Data: c.data, Exit: c.exit}
}

func (*contextWaitingClient) Unsubscribe(string, Subscription) {}
func (*contextWaitingClient) Abort() error                     { return nil }

func (c *contextWaitingClient) deadlines() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.deadlineBudget...)
}

type deadlineCloseClient struct {
	data chan protocol.TerminalDataEvent
	exit chan protocol.TerminalExitEvent

	abortErr         error
	closeCalls       atomic.Int32
	abortCalls       atomic.Int32
	unsubscribeCalls atomic.Int32
	closeStarted     chan struct{}
	started          sync.Once
}

func newDeadlineCloseClient(abortErr error) *deadlineCloseClient {
	return &deadlineCloseClient{
		data:         make(chan protocol.TerminalDataEvent),
		exit:         make(chan protocol.TerminalExitEvent),
		abortErr:     abortErr,
		closeStarted: make(chan struct{}),
	}
}

func (c *deadlineCloseClient) Call(ctx context.Context, method string, params any, out any) error {
	switch method {
	case "terminal.open":
		terminalID := params.(protocol.TerminalOpenParams).TerminalID
		out.(*protocol.TerminalOpenResult).TerminalID = terminalID
		return nil
	case "terminal.write", "terminal.resize":
		return nil
	case "terminal.close":
		call := c.closeCalls.Add(1)
		if call > 1 {
			return nil
		}
		c.started.Do(func() { close(c.closeStarted) })
		if _, ok := ctx.Deadline(); !ok {
			return errors.New("close context has no deadline")
		}
		<-ctx.Done()
		return ctx.Err()
	default:
		return fmt.Errorf("unexpected method %s", method)
	}
}

func (c *deadlineCloseClient) Subscribe(string) Subscription {
	return Subscription{Data: c.data, Exit: c.exit}
}

func (c *deadlineCloseClient) Unsubscribe(string, Subscription) {
	c.unsubscribeCalls.Add(1)
}

func (c *deadlineCloseClient) Abort() error {
	c.abortCalls.Add(1)
	return c.abortErr
}

func requireKilledAndClosed(t *testing.T, handle pkgpty.Handle) {
	t.Helper()
	select {
	case info, ok := <-handle.Exit():
		require.True(t, ok, "exit channel closed without an outcome")
		require.Equal(t, "killed", info.Reason)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("handle did not settle after close")
	}
	_, ok := <-handle.Exit()
	require.False(t, ok, "exit channel must close after one outcome")
	_, ok = <-handle.Data()
	require.False(t, ok, "data channel must close with the handle")
}

func TestHandle_GivenWriteAndResizeCallsWaitingOnContextWhenOperationDeadlineExpiresThenAllReturnWithoutActiveCalls(t *testing.T) {
	const operationCount = 32
	client := newContextWaitingClient()
	backend := newBackend(client, nil, 25*time.Millisecond)
	handle, err := backend.Open(context.Background(), pkgpty.Spec{
		TerminalID: "operation-deadlines",
		Cwd:        "/repo",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Close() })

	results := make(chan error, operationCount)
	start := make(chan struct{})
	for i := 0; i < operationCount; i++ {
		go func(index int) {
			<-start
			if index%2 == 0 {
				written, writeErr := handle.Write([]byte("x"))
				if written != 0 {
					results <- fmt.Errorf("Write returned %d bytes on deadline", written)
					return
				}
				results <- writeErr
				return
			}
			results <- handle.Resize(80, 24)
		}(i)
	}
	close(start)

	for i := 0; i < operationCount; i++ {
		select {
		case operationErr := <-results:
			require.ErrorIs(t, operationErr, context.DeadlineExceeded)
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("operation %d did not return after its deadline", i)
		}
	}
	require.Zero(t, client.active.Load(), "deadline returns must not leave Call goroutines active")
	require.Equal(t, int32(operationCount/2), client.writeCalls.Load())
	require.Equal(t, int32(operationCount/2), client.resizeCalls.Load())
	deadlines := client.deadlines()
	require.Len(t, deadlines, operationCount)
	for _, budget := range deadlines {
		require.Positive(t, budget)
		require.LessOrEqual(t, budget, 50*time.Millisecond)
	}
	require.NoError(t, handle.Close())
}

func TestHandle_GivenConcurrentCloseCallersAndBlockedRPCWhenDeadlineExpiresThenAbortsOnceAndSharesTheDeadlineOutcome(t *testing.T) {
	const callers = 8
	client := newDeadlineCloseClient(nil)
	var releases atomic.Int32
	backend := newBackend(client, func() { releases.Add(1) }, 100*time.Millisecond)
	handle, err := backend.Open(context.Background(), pkgpty.Spec{
		TerminalID: "close-deadline-abort",
		Cwd:        "/repo",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Close() })

	results := make(chan error, callers)
	go func() { results <- handle.Close() }()
	<-client.closeStarted
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(callers - 1)
	for i := 1; i < callers; i++ {
		go func() {
			ready.Done()
			<-start
			results <- handle.Close()
		}()
	}
	ready.Wait()
	close(start)

	firstErr := <-results
	require.ErrorIs(t, firstErr, context.DeadlineExceeded)
	for i := 1; i < callers; i++ {
		closeErr := <-results
		require.Equal(t, firstErr, closeErr, "concurrent callers must share one close outcome")
	}
	require.Equal(t, int32(1), client.closeCalls.Load(), "concurrent callers must share one close RPC")
	require.Equal(t, int32(1), client.abortCalls.Load(), "the suspect shared connection must abort once")
	require.Equal(t, int32(1), releases.Load(), "successful abort settlement must release the lease once")
	require.NoError(t, handle.Close(), "an abort-settled handle must remain idempotently closed")
	requireKilledAndClosed(t, handle)
	require.Eventually(t, func() bool { return client.unsubscribeCalls.Load() == 1 },
		500*time.Millisecond, time.Millisecond)
}

func TestHandle_GivenCloseDeadlineAndAbortFailureWhenRetriedThenRetainsHandleAndLeaseUntilSuccessfulClose(t *testing.T) {
	abortErr := errors.New("websocket close failed")
	client := newDeadlineCloseClient(abortErr)
	var releases atomic.Int32
	backend := newBackend(client, func() { releases.Add(1) }, 25*time.Millisecond)
	handle, err := backend.Open(context.Background(), pkgpty.Spec{
		TerminalID: "close-deadline-abort-failure",
		Cwd:        "/repo",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = handle.Close() })

	closeErr := handle.Close()
	require.ErrorIs(t, closeErr, context.DeadlineExceeded)
	require.ErrorIs(t, closeErr, abortErr)
	require.Equal(t, int32(1), client.abortCalls.Load())
	require.Zero(t, releases.Load(), "failed abort must retain the borrowed connection lease")
	select {
	case info, ok := <-handle.Exit():
		t.Fatalf("failed abort settled the handle: ok=%v info=%+v", ok, info)
	default:
	}
	written, err := handle.Write([]byte("x"))
	require.NoError(t, err, "failed abort must leave the same handle retryable")
	require.Equal(t, 1, written)

	require.NoError(t, handle.Close(), "a later non-timeout close acknowledgement must settle the handle")
	require.Equal(t, int32(2), client.closeCalls.Load())
	require.Equal(t, int32(1), releases.Load())
	requireKilledAndClosed(t, handle)
}
