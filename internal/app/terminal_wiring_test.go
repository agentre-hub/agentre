package app

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/internal/pkg/pty/remote"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"

	"github.com/stretchr/testify/require"
)

type terminalWiringDaemonClient struct {
	mu          sync.Mutex
	conn        *protorpc.Conn
	server      *protorpc.Conn
	closed      chan struct{}
	closeOnce   sync.Once
	openErr     error
	closeErrors []error
	closeCalls  int
}

func newTerminalWiringDaemonClient() *terminalWiringDaemonClient {
	a, b := terminalWiringPipePair()
	client := &terminalWiringDaemonClient{closed: make(chan struct{})}
	serverRegistry := protorpc.NewRegistry()
	protorpc.RegisterMethod(serverRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN), func() *agentrewire.TerminalOpenRequest { return &agentrewire.TerminalOpenRequest{} }, func(_ context.Context, request *agentrewire.TerminalOpenRequest) (*agentrewire.TerminalOpenResponse, error) {
		client.mu.Lock()
		defer client.mu.Unlock()
		if client.openErr != nil {
			return nil, client.openErr
		}
		return &agentrewire.TerminalOpenResponse{TerminalId: request.TerminalId}, nil
	})
	protorpc.RegisterMethod(serverRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_WRITE), func() *agentrewire.TerminalWriteRequest { return &agentrewire.TerminalWriteRequest{} }, func(context.Context, *agentrewire.TerminalWriteRequest) (*agentrewire.Empty, error) {
		return &agentrewire.Empty{}, nil
	})
	protorpc.RegisterMethod(serverRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_RESIZE), func() *agentrewire.TerminalResizeRequest { return &agentrewire.TerminalResizeRequest{} }, func(context.Context, *agentrewire.TerminalResizeRequest) (*agentrewire.Empty, error) {
		return &agentrewire.Empty{}, nil
	})
	protorpc.RegisterMethod(serverRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_CLOSE), func() *agentrewire.TerminalCloseRequest { return &agentrewire.TerminalCloseRequest{} }, func(context.Context, *agentrewire.TerminalCloseRequest) (*agentrewire.Empty, error) {
		client.mu.Lock()
		defer client.mu.Unlock()
		client.closeCalls++
		if client.closeCalls <= len(client.closeErrors) {
			return nil, client.closeErrors[client.closeCalls-1]
		}
		return &agentrewire.Empty{}, nil
	})
	client.conn = protorpc.NewConn(a, protorpc.NewRegistry())
	client.server = protorpc.NewConn(b, serverRegistry)
	go client.conn.Serve(context.Background())
	go client.server.Serve(context.Background())
	return client
}

func (c *terminalWiringDaemonClient) Conn() *protorpc.Conn    { return c.conn }
func (c *terminalWiringDaemonClient) Closed() <-chan struct{} { return c.closed }

func (c *terminalWiringDaemonClient) Close() error {
	c.closeConnection()
	return nil
}

func (c *terminalWiringDaemonClient) dispatch(method string, payload any) error {
	var notification *agentrewire.RpcNotification
	switch event := payload.(type) {
	case protocol.TerminalDataEvent:
		notification = &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_TerminalData{TerminalData: &agentrewire.TerminalDataNotification{TerminalId: event.TerminalID, Data: event.Data}}}
	case protocol.TerminalExitEvent:
		notification = &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_TerminalExit{TerminalExit: &agentrewire.TerminalExitNotification{TerminalId: event.TerminalID, Code: int32(event.Code), Reason: event.Reason, Message: event.Msg}}}
	default:
		return errors.New("unsupported terminal notification")
	}
	return c.server.Notify(notification)
}

func (c *terminalWiringDaemonClient) push(t *testing.T, method string, payload any) {
	t.Helper()
	require.NoError(t, c.dispatch(method, payload), "dispatch %s", method)
}

func (c *terminalWiringDaemonClient) closeConnection() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
		_ = c.server.Close()
	})
}

type terminalWiringPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func terminalWiringPipePair() (*terminalWiringPipe, *terminalWiringPipe) {
	a, b := make(chan []byte, 64), make(chan []byte, 64)
	done := make(chan struct{})
	once := &sync.Once{}
	return &terminalWiringPipe{in: a, out: b, done: done, once: once}, &terminalWiringPipe{in: b, out: a, done: done, once: once}
}

func (p *terminalWiringPipe) ReadFrame() ([]byte, error) {
	select {
	case payload := <-p.in:
		return payload, nil
	case <-p.done:
		return nil, io.EOF
	}
}

func (p *terminalWiringPipe) WriteFrame(payload []byte) error {
	select {
	case p.out <- append([]byte(nil), payload...):
		return nil
	case <-p.done:
		return io.EOF
	}
}

func (p *terminalWiringPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *terminalWiringPipe) Done() <-chan struct{} { return p.done }

type terminalWiringBorrower struct {
	mu       sync.Mutex
	client   remote.DaemonClient
	borrows  int
	releases int
}

func (b *terminalWiringBorrower) Borrow(
	context.Context,
	int64,
) (remote.DaemonClient, func(), error) {
	b.mu.Lock()
	b.borrows++
	client := b.client
	b.mu.Unlock()
	var once sync.Once
	return client, func() {
		once.Do(func() {
			b.mu.Lock()
			b.releases++
			b.mu.Unlock()
		})
	}, nil
}

func (b *terminalWiringBorrower) setClient(client remote.DaemonClient) {
	b.mu.Lock()
	b.client = client
	b.mu.Unlock()
}

func (b *terminalWiringBorrower) counts() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.borrows, b.releases
}

func requireTerminalWiringData(
	t *testing.T,
	client *terminalWiringDaemonClient,
	h pty.Handle,
	terminalID string,
	data string,
) {
	t.Helper()
	require.NoError(t, client.dispatch("terminal.data", protocol.TerminalDataEvent{
		TerminalID: terminalID,
		Data:       []byte(data),
	}))
	select {
	case got := <-h.Data():
		require.Equal(t, data, string(got))
	case <-time.After(time.Second):
		t.Fatal("terminal data not delivered")
	}
	select {
	case extra := <-h.Data():
		t.Fatalf("terminal data delivered more than once: %q", string(extra))
	case <-time.After(25 * time.Millisecond):
	}
}

func requireTerminalWiringOutcome(t *testing.T, h pty.Handle, reason string) {
	t.Helper()
	select {
	case info, ok := <-h.Exit():
		require.True(t, ok)
		require.Equal(t, reason, info.Reason)
	case <-time.After(time.Second):
		t.Fatal("terminal outcome not delivered")
	}
}

func requireTerminalWiringExit(
	t *testing.T,
	client *terminalWiringDaemonClient,
	h pty.Handle,
	terminalID string,
) {
	t.Helper()
	client.push(t, "terminal.exit", protocol.TerminalExitEvent{
		TerminalID: terminalID,
		Reason:     "natural",
	})
	requireTerminalWiringOutcome(t, h, "natural")
}

func terminalWiringCachedGeneration(
	wiring *terminalRemoteWiring,
	deviceID int64,
) (int, <-chan struct{}) {
	wiring.adapters.mu.Lock()
	defer wiring.adapters.mu.Unlock()
	entry := wiring.adapters.entries[deviceID]
	if entry == nil {
		return len(wiring.adapters.entries), nil
	}
	return len(wiring.adapters.entries), entry.closed
}

func TestTerminalProductionWiring_GivenTwoLiveTerminalsOnOneDaemonClient_WhenOpened_ThenSharesDemuxAndOwnsOneLeaseEach(t *testing.T) {
	client := newTerminalWiringDaemonClient()
	t.Cleanup(client.closeConnection)
	borrower := &terminalWiringBorrower{client: client}
	wiring := newTerminalRemoteWiring(borrower.Borrow)

	firstBackend, err := wiring.Backend("42")
	require.NoError(t, err)
	secondBackend, err := wiring.Backend("42")
	require.NoError(t, err)
	borrows, releases := borrower.counts()
	require.Zero(t, borrows, "backend selection must not borrow before PTYBackend.Open")
	require.Zero(t, releases)

	first, err := firstBackend.Open(context.Background(), pty.Spec{TerminalID: "terminal-first", Cwd: "/repo"})
	require.NoError(t, err)
	second, err := secondBackend.Open(context.Background(), pty.Spec{
		TerminalID: "terminal-second", Cwd: "/repo", Command: "go test",
	})
	require.NoError(t, err)
	borrows, releases = borrower.counts()
	require.Equal(t, 2, borrows)
	require.Zero(t, releases, "live handles must retain their independent leases")

	requireTerminalWiringData(t, client, first, "terminal-first", "first")
	requireTerminalWiringData(t, client, second, "terminal-second", "second")
	requireTerminalWiringExit(t, client, first, "terminal-first")
	requireTerminalWiringExit(t, client, second, "terminal-second")
	require.Eventually(t, func() bool {
		_, released := borrower.counts()
		return released == 2
	}, time.Second, time.Millisecond)

	require.NoError(t, first.Close())
	require.NoError(t, second.Close())
	_, releases = borrower.counts()
	require.Equal(t, 2, releases, "settled handles must not release twice")
}

func TestTerminalProductionWiring_GivenConcurrentOpensOnSharedClient_WhenStarted_ThenRegistersHandlersOnceAndReleasesEveryLease(t *testing.T) {
	const opens = 32
	client := newTerminalWiringDaemonClient()
	t.Cleanup(client.closeConnection)
	borrower := &terminalWiringBorrower{client: client}
	wiring := newTerminalRemoteWiring(borrower.Borrow)
	type openResult struct {
		handle pty.Handle
		err    error
	}
	results := make(chan openResult, opens)

	for i := range opens {
		go func() {
			backend, err := wiring.Backend("42")
			if err != nil {
				results <- openResult{err: err}
				return
			}
			h, err := backend.Open(context.Background(), pty.Spec{
				TerminalID: "terminal-" + strconv.Itoa(i), Cwd: "/repo",
			})
			results <- openResult{handle: h, err: err}
		}()
	}

	handles := make([]pty.Handle, 0, opens)
	for range opens {
		result := <-results
		require.NoError(t, result.err)
		handles = append(handles, result.handle)
	}
	borrows, releases := borrower.counts()
	require.Equal(t, opens, borrows)
	require.Zero(t, releases)

	for _, h := range handles {
		require.NoError(t, h.Close())
	}
	for _, h := range handles {
		requireTerminalWiringOutcome(t, h, "killed")
	}
	require.Eventually(t, func() bool {
		_, released := borrower.counts()
		return released == opens
	}, time.Second, time.Millisecond)
}

func TestTerminalProductionWiring_GivenBackendSelected_WhenStartStopsBeforeOpen_ThenBorrowsNothing(t *testing.T) {
	borrower := &terminalWiringBorrower{client: newTerminalWiringDaemonClient()}
	wiring := newTerminalRemoteWiring(borrower.Borrow)

	backend, err := wiring.Backend("42")

	require.NoError(t, err)
	require.NotNil(t, backend)
	borrows, releases := borrower.counts()
	require.Zero(t, borrows)
	require.Zero(t, releases)
}

func TestTerminalProductionWiring_GivenRemoteOpenFails_WhenOpened_ThenReleasesBorrowImmediately(t *testing.T) {
	openErr := errors.New("terminal.open failed")
	client := newTerminalWiringDaemonClient()
	client.openErr = openErr
	t.Cleanup(client.closeConnection)
	borrower := &terminalWiringBorrower{client: client}
	wiring := newTerminalRemoteWiring(borrower.Borrow)
	backend, err := wiring.Backend("42")
	require.NoError(t, err)

	h, err := backend.Open(context.Background(), pty.Spec{TerminalID: "terminal-open-failure", Cwd: "/repo"})

	require.Nil(t, h)
	require.EqualError(t, err, openErr.Error())
	borrows, releases := borrower.counts()
	require.Equal(t, 1, borrows)
	require.Equal(t, 1, releases)
}

func TestTerminalProductionWiring_GivenCloseFails_WhenRetried_ThenRetainsLeaseUntilConfirmedOutcome(t *testing.T) {
	closeErr := errors.New("terminal.close failed")
	client := newTerminalWiringDaemonClient()
	client.closeErrors = []error{closeErr, nil}
	t.Cleanup(client.closeConnection)
	borrower := &terminalWiringBorrower{client: client}
	wiring := newTerminalRemoteWiring(borrower.Borrow)
	backend, err := wiring.Backend("42")
	require.NoError(t, err)
	h, err := backend.Open(context.Background(), pty.Spec{TerminalID: "terminal-close-retry", Cwd: "/repo"})
	require.NoError(t, err)

	require.EqualError(t, h.Close(), closeErr.Error())
	_, releases := borrower.counts()
	require.Zero(t, releases)

	require.NoError(t, h.Close())
	select {
	case info := <-h.Exit():
		require.Equal(t, "killed", info.Reason)
	case <-time.After(time.Second):
		t.Fatal("confirmed close did not publish a killed outcome")
	}
	require.Eventually(t, func() bool {
		_, released := borrower.counts()
		return released == 1
	}, time.Second, time.Millisecond)
}

func TestTerminalProductionWiring_GivenReplacementClientForSameDevice_WhenOpened_ThenUsesNewAdapterWithoutDisruptingOldHandle(t *testing.T) {
	oldClient := newTerminalWiringDaemonClient()
	newClient := newTerminalWiringDaemonClient()
	t.Cleanup(oldClient.closeConnection)
	t.Cleanup(newClient.closeConnection)
	borrower := &terminalWiringBorrower{client: oldClient}
	wiring := newTerminalRemoteWiring(borrower.Borrow)
	oldBackend, err := wiring.Backend("42")
	require.NoError(t, err)
	oldHandle, err := oldBackend.Open(context.Background(), pty.Spec{TerminalID: "terminal-old", Cwd: "/old"})
	require.NoError(t, err)
	requireTerminalWiringData(t, oldClient, oldHandle, "terminal-old", "old-ready")

	borrower.setClient(newClient)
	newBackend, err := wiring.Backend("42")
	require.NoError(t, err)
	newHandle, err := newBackend.Open(context.Background(), pty.Spec{TerminalID: "terminal-new", Cwd: "/new"})
	require.NoError(t, err)

	cacheSize, generation := terminalWiringCachedGeneration(wiring, 42)
	require.Equal(t, 1, cacheSize, "the cache keeps only the current generation per device")
	require.Equal(t, (<-chan struct{})(newClient.closed), generation)
	requireTerminalWiringData(t, oldClient, oldHandle, "terminal-old", "old-still-live")
	requireTerminalWiringData(t, newClient, newHandle, "terminal-new", "new-live")

	oldClient.closeConnection()
	requireTerminalWiringOutcome(t, oldHandle, "connection_lost")
	require.Eventually(t, func() bool {
		size, current := terminalWiringCachedGeneration(wiring, 42)
		return size == 1 && current == newClient.closed
	}, time.Second, time.Millisecond, "closing the old generation must not evict the replacement")
	requireTerminalWiringData(t, newClient, newHandle, "terminal-new", "new-still-live")
	requireTerminalWiringExit(t, newClient, newHandle, "terminal-new")
	require.Eventually(t, func() bool {
		_, released := borrower.counts()
		return released == 2
	}, time.Second, time.Millisecond)
}
