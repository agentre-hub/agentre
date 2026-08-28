package protorpc_test

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type pipe struct {
	in   chan []byte
	out  chan []byte
	done chan struct{}
	// once is shared by both ends: they also share done, so a per-end Once
	// would panic as soon as both connections close their transport.
	once *sync.Once
}

func pipePair() (*pipe, *pipe) {
	a, b := make(chan []byte, 16), make(chan []byte, 16)
	done := make(chan struct{})
	once := &sync.Once{}
	return &pipe{in: a, out: b, done: done, once: once}, &pipe{in: b, out: a, done: done, once: once}
}

func (p *pipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}
func (p *pipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}
func (p *pipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *pipe) Done() <-chan struct{} { return p.done }

const mcpProxyMethodID = uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY)

// Given a connection whose peer registered no handler for the requested method ID,
// When the request is dispatched, Then the caller gets a method-not-found RPC error
// rather than a hang or a malformed response.
func TestConnDispatch_GivenUnregisteredMethodID_WhenCalled_ThenReturnsMethodNotFound(t *testing.T) {
	clientTransport, serverTransport := pipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, protorpc.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, mcpProxyMethodID, &agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, protorpc.CodeMethodNotFound, rpcErr.Code)
}

func TestConnExposesConnectionStateToHandlers(t *testing.T) {
	t.Run("given an authenticated connection, when a request is dispatched, then the handler sees the same connection and auth state", func(t *testing.T) {
		clientTransport, serverTransport := pipePair()
		serverRegistry := protorpc.NewRegistry()
		server := protorpc.NewConn(serverTransport, serverRegistry)
		server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "device-1", AccountID: "account-1"})
		protorpc.RegisterMethod(
			serverRegistry,
			mcpProxyMethodID,
			func() *agentrewire.Empty { return &agentrewire.Empty{} },
			func(ctx context.Context, _ *agentrewire.Empty) (*agentrewire.Empty, error) {
				require.Same(t, server, protorpc.ConnFromContext(ctx))
				require.Equal(t, server.Auth(), protorpc.ConnFromContext(ctx).Auth())
				return &agentrewire.Empty{}, nil
			},
		)
		client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go client.Serve(ctx)
		go server.Serve(ctx)

		_, err := protorpc.CallMethod(ctx, client, mcpProxyMethodID, &agentrewire.Empty{},
			func() *agentrewire.Empty { return &agentrewire.Empty{} })
		require.NoError(t, err)
		require.Same(t, serverRegistry, server.Registry())
	})

	t.Run("given a connection without a transport, when state is inspected, then it remains usable without serving", func(t *testing.T) {
		conn := protorpc.NewConn(nil, protorpc.NewRegistry())
		conn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceName: "desktop"})

		require.True(t, conn.Auth().Authenticated)
		require.Equal(t, "desktop", conn.Auth().DeviceName)
		require.NotNil(t, conn.Done())
		require.NoError(t, conn.Close())
	})
}

// Notifications carry a per-session sequence that the receiver replays in order, so
// the read loop must dispatch them synchronously. Given a peer that emits a burst of
// notifications while a request is also in flight, When they are received, Then they
// must surface in exactly the order they were sent.
func TestConnNotifications_GivenABurstFromThePeer_WhenReceived_ThenTheyKeepSendOrder(t *testing.T) {
	clientTransport, serverTransport := pipePair()
	serverRegistry := protorpc.NewRegistry()
	protorpc.RegisterMethod(
		serverRegistry,
		mcpProxyMethodID,
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) {
			return &agentrewire.Empty{}, nil
		},
	)
	sent := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	received := make(chan int64, len(sent))
	clientRegistry := protorpc.NewRegistry()
	clientRegistry.RegisterNotification(func(_ context.Context, notification *agentrewire.RpcNotification) {
		received <- notification.GetRuntimeEvent().GetSeq()
	})
	client := protorpc.NewConn(clientTransport, clientRegistry)
	server := protorpc.NewConn(serverTransport, serverRegistry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, mcpProxyMethodID, &agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.NoError(t, err)

	for _, seq := range sent {
		require.NoError(t, server.Notify(&agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_RuntimeEvent{
			RuntimeEvent: &agentrewire.RuntimeEventNotification{SessionId: 42, Seq: seq},
		}}))
	}

	var got []int64
	for range sent {
		select {
		case seq := <-received:
			got = append(got, seq)
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d notifications arrived", len(got), len(sent))
		}
	}
	require.Equal(t, sent, got, "notifications must not be reordered by the read loop")
}

// Terminal traffic is raw PTY output: it is not valid UTF-8 and must survive the
// round trip byte for byte, both in a request payload and in a notification.
func TestConnTerminalCarriesRawBinaryData(t *testing.T) {
	writeMethodID := uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_WRITE)
	clientTransport, serverTransport := pipePair()
	serverRegistry := protorpc.NewRegistry()
	protorpc.RegisterMethod(
		serverRegistry,
		writeMethodID,
		func() *agentrewire.TerminalWriteRequest { return &agentrewire.TerminalWriteRequest{} },
		func(_ context.Context, req *agentrewire.TerminalWriteRequest) (*agentrewire.Empty, error) {
			require.Equal(t, []byte{0, 255, 1}, req.Data)
			return &agentrewire.Empty{}, nil
		},
	)
	events := make(chan []byte, 1)
	clientRegistry := protorpc.NewRegistry()
	clientRegistry.RegisterNotification(func(_ context.Context, n *agentrewire.RpcNotification) {
		events <- n.GetTerminalData().GetData()
	})
	client := protorpc.NewConn(clientTransport, clientRegistry)
	server := protorpc.NewConn(serverTransport, serverRegistry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, writeMethodID,
		&agentrewire.TerminalWriteRequest{TerminalId: "term", Data: []byte{0, 255, 1}},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.NoError(t, err)

	require.NoError(t, server.Notify(&agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_TerminalData{
		TerminalData: &agentrewire.TerminalDataNotification{TerminalId: "term", Data: []byte{255, 0}},
	}}))
	require.Equal(t, []byte{255, 0}, <-events)
}

// Given a call waiting on a peer that will never answer, When the connection is
// closed locally, Then the caller must be woken with ErrConnClosed instead of
// blocking forever.
func TestConnCloseWakesPendingCall(t *testing.T) {
	clientTransport, _ := pipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	done := make(chan error, 1)
	go func() {
		_, err := protorpc.CallMethod(context.Background(), client, mcpProxyMethodID, &agentrewire.Empty{},
			func() *agentrewire.Empty { return &agentrewire.Empty{} })
		done <- err
	}()
	require.NoError(t, client.Close())
	select {
	case err := <-done:
		require.True(t, errors.Is(err, protorpc.ErrConnClosed))
	case <-time.After(time.Second):
		t.Fatal("pending call remained blocked after close")
	}
}

// Given a handler is still in flight, When the serve read loop exits because the
// transport ended, Then the handler's context must be canceled — otherwise a peer
// disconnect leaves handler goroutines running against a dead connection.
func TestConnServe_GivenInflightHandler_WhenTransportEnds_ThenHandlerContextIsCanceled(t *testing.T) {
	methodID := uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY)
	clientTransport, serverTransport := pipePair()
	handlerStarted := make(chan struct{})
	handlerCanceled := make(chan struct{})
	serverRegistry := protorpc.NewRegistry()
	protorpc.RegisterMethod(
		serverRegistry,
		methodID,
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(ctx context.Context, _ *agentrewire.Empty) (*agentrewire.Empty, error) {
			close(handlerStarted)
			<-ctx.Done()
			close(handlerCanceled)
			return &agentrewire.Empty{}, nil
		},
	)
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, serverRegistry)
	go client.Serve(context.Background())
	go server.Serve(context.Background())

	go func() {
		_, _ = protorpc.CallMethod(context.Background(), client, methodID, &agentrewire.Empty{},
			func() *agentrewire.Empty { return &agentrewire.Empty{} })
	}()
	select {
	case <-handlerStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("handler never started")
	}

	require.NoError(t, client.Close())
	select {
	case <-handlerCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("serve loop exit left the in-flight handler context alive")
	}
}

// Given an in-flight generic request, When the caller cancels, Then the peer's
// handler context must be canceled too.
func TestConnCancellation_GivenGenericMethodRequest_WhenCallerCancels_ThenHandlerContextIsCanceled(t *testing.T) {
	methodID := uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY)
	clientTransport, serverTransport := pipePair()
	handlerCanceled := make(chan struct{})
	serverRegistry := protorpc.NewRegistry()
	protorpc.RegisterMethod(
		serverRegistry,
		methodID,
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(ctx context.Context, _ *agentrewire.Empty) (*agentrewire.Empty, error) {
			<-ctx.Done()
			close(handlerCanceled)
			return nil, ctx.Err()
		},
	)
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, serverRegistry)
	serveCtx, stopServe := context.WithCancel(context.Background())
	defer stopServe()
	go client.Serve(serveCtx)
	go server.Serve(serveCtx)

	callCtx, cancelCall := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		_, err := protorpc.CallMethod(callCtx, client, methodID, &agentrewire.Empty{},
			func() *agentrewire.Empty { return &agentrewire.Empty{} })
		callDone <- err
	}()
	cancelCall()
	require.ErrorIs(t, <-callDone, context.Canceled)
	select {
	case <-handlerCanceled:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel frame did not cancel the remote generic-method handler")
	}
}
