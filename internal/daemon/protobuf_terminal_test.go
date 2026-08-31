package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type protobufTerminalBackend struct{ handle *protobufTerminalHandle }

func (b protobufTerminalBackend) Open(context.Context, pty.Spec) (handlers.PTYHandle, error) {
	return b.handle, nil
}

type protobufTerminalHandle struct {
	data    chan []byte
	exit    chan pty.ExitInfo
	written chan []byte
	resized chan [2]uint16
	closed  chan struct{}
}

func newProtobufTerminalHandle() *protobufTerminalHandle {
	return &protobufTerminalHandle{
		data: make(chan []byte, 1), exit: make(chan pty.ExitInfo, 1), written: make(chan []byte, 1),
		resized: make(chan [2]uint16, 1), closed: make(chan struct{}, 1),
	}
}

func (h *protobufTerminalHandle) Write(data []byte) (int, error) {
	h.written <- append([]byte(nil), data...)
	return len(data), nil
}
func (h *protobufTerminalHandle) Resize(cols, rows uint16) error {
	h.resized <- [2]uint16{cols, rows}
	return nil
}
func (h *protobufTerminalHandle) Close() error              { h.closed <- struct{}{}; return nil }
func (h *protobufTerminalHandle) Data() <-chan []byte       { return h.data }
func (h *protobufTerminalHandle) Exit() <-chan pty.ExitInfo { return h.exit }

func TestProtobufTerminalMethodsPreserveBinaryData(t *testing.T) {
	t.Run("given authenticated peers, when terminal methods and events cross protobuf, then bytes remain unchanged", func(t *testing.T) {
		clientTransport, serverTransport := protobufTestPipePair()
		clientRegistry := protorpc.NewRegistry()
		notifications := make(chan *agentrewire.RpcNotification, 2)
		clientRegistry.RegisterNotification(func(_ context.Context, notification *agentrewire.RpcNotification) {
			notifications <- notification
		})
		client := protorpc.NewConn(clientTransport, clientRegistry)
		serverRegistry := protorpc.NewRegistry()
		server := protorpc.NewConn(serverTransport, serverRegistry)
		server.SetAuth(protorpc.AuthState{Authenticated: true})
		handle := newProtobufTerminalHandle()
		terminal := handlers.NewTerminalHandlers(protobufTerminalBackend{handle: handle}, newProtobufTerminalEmitter(server))
		registerProtobufTerminalMethods(serverRegistry, terminal)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go client.Serve(ctx)
		go server.Serve(ctx)

		opened, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN),
			&agentrewire.TerminalOpenRequest{TerminalId: "term-1", Cols: 80, Rows: 24},
			func() *agentrewire.TerminalOpenResponse { return &agentrewire.TerminalOpenResponse{} })
		require.NoError(t, err)
		require.Equal(t, "term-1", opened.TerminalId)

		binary := []byte{0, 0xff, 0xe2}
		_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_WRITE),
			&agentrewire.TerminalWriteRequest{TerminalId: "term-1", Data: binary}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
		require.NoError(t, err)
		require.Equal(t, binary, <-handle.written)

		_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_RESIZE),
			&agentrewire.TerminalResizeRequest{TerminalId: "term-1", Cols: 132, Rows: 43}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
		require.NoError(t, err)
		require.Equal(t, [2]uint16{132, 43}, <-handle.resized)

		handle.data <- binary
		close(handle.data)
		handle.exit <- pty.ExitInfo{Code: 7, Reason: "natural", Msg: "done"}
		close(handle.exit)

		dataNotification := <-notifications
		require.Equal(t, "term-1", dataNotification.GetTerminalData().TerminalId)
		require.Equal(t, binary, dataNotification.GetTerminalData().Data)
		exitNotification := <-notifications
		require.Equal(t, int32(7), exitNotification.GetTerminalExit().Code)
		require.Equal(t, "done", exitNotification.GetTerminalExit().Message)
	})

	t.Run("given an unauthenticated peer, when opening a terminal, then the method is rejected", func(t *testing.T) {
		clientTransport, serverTransport := protobufTestPipePair()
		client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
		serverRegistry := protorpc.NewRegistry()
		server := protorpc.NewConn(serverTransport, serverRegistry)
		terminal := handlers.NewTerminalHandlers(protobufTerminalBackend{handle: newProtobufTerminalHandle()}, newProtobufTerminalEmitter(server))
		registerProtobufTerminalMethods(serverRegistry, terminal)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		go client.Serve(ctx)
		go server.Serve(ctx)

		_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN),
			&agentrewire.TerminalOpenRequest{TerminalId: "term-1", Cols: 80, Rows: 24},
			func() *agentrewire.TerminalOpenResponse { return &agentrewire.TerminalOpenResponse{} })
		var rpcErr *protorpc.Error
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, int32(-32001), rpcErr.Code)
	})

	t.Run("given an active terminal, when close is called, then the backend handle is closed", func(t *testing.T) {
		clientTransport, serverTransport := protobufTestPipePair()
		client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
		serverRegistry := protorpc.NewRegistry()
		server := protorpc.NewConn(serverTransport, serverRegistry)
		server.SetAuth(protorpc.AuthState{Authenticated: true})
		handle := newProtobufTerminalHandle()
		terminal := handlers.NewTerminalHandlers(protobufTerminalBackend{handle: handle}, newProtobufTerminalEmitter(server))
		registerProtobufTerminalMethods(serverRegistry, terminal)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		go client.Serve(ctx)
		go server.Serve(ctx)

		_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN),
			&agentrewire.TerminalOpenRequest{TerminalId: "term-close", Cols: 80, Rows: 24},
			func() *agentrewire.TerminalOpenResponse { return &agentrewire.TerminalOpenResponse{} })
		require.NoError(t, err)
		_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_CLOSE),
			&agentrewire.TerminalCloseRequest{TerminalId: "term-close"}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
		require.NoError(t, err)
		select {
		case <-handle.closed:
		case <-ctx.Done():
			t.Fatal("terminal backend was not closed")
		}
	})
}

func TestBindProtobufTerminalRegistersMethodsAndCleansUpOnDisconnect(t *testing.T) {
	t.Run("given a daemon-bound protobuf connection, when terminal open is called before auth, then method 21 reaches its auth guard", func(t *testing.T) {
		daemon, err := New(Options{DataDir: t.TempDir()})
		require.NoError(t, err)
		t.Cleanup(func() { closeDB(daemon.db) })
		clientTransport, serverTransport := protobufTestPipePair()
		client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
		server := protorpc.NewConn(serverTransport, daemon.protobufRegistry.Clone())
		daemon.bindProtobufConn(server)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go client.Serve(ctx)
		go server.Serve(ctx)

		_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN),
			&agentrewire.TerminalOpenRequest{TerminalId: "bound-term", Cols: 80, Rows: 24},
			func() *agentrewire.TerminalOpenResponse { return &agentrewire.TerminalOpenResponse{} })
		var rpcErr *protorpc.Error
		require.ErrorAs(t, err, &rpcErr)
		require.Equal(t, int32(-32001), rpcErr.Code)
	})

	t.Run("given an open terminal, when its protobuf connection closes, then the owned handle is closed", func(t *testing.T) {
		_, serverTransport := protobufTestPipePair()
		server := protorpc.NewConn(serverTransport, protorpc.NewRegistry())
		handle := newProtobufTerminalHandle()
		terminal := bindProtobufTerminal(server, protobufTerminalBackend{handle: handle})
		_, err := terminal.Open(context.Background(), protocol.TerminalOpenParams{TerminalID: "cleanup-term", Cols: 80, Rows: 24})
		require.NoError(t, err)

		require.NoError(t, server.Close())
		select {
		case <-handle.closed:
		case <-time.After(time.Second):
			t.Fatal("connection close did not close its terminal handle")
		}
	})
}
