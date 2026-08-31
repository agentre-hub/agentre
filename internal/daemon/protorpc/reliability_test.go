package protorpc_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// A handler runs on its own goroutine (conn.Serve dispatches `go c.handle`), so a
// panic inside one is not recoverable by the caller — it takes the whole process
// down. That regressed once already: a nil deref in the claudecode runtime killed
// agentred, and every session on that machine hung in "generating" with nothing on
// screen. Given a registered handler that panics, When a peer calls it, Then the
// peer must get a structured internal error and both ends must keep serving.
func TestConnDispatch_GivenAHandlerThatPanics_WhenCalled_ThenPeerGetsInternalErrorAndTheConnectionKeepsServing(t *testing.T) {
	clientTransport, serverTransport := pipePair()
	serverRegistry := protorpc.NewRegistry()
	calls := 0
	protorpc.RegisterMethod(
		serverRegistry,
		mcpProxyMethodID,
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) {
			calls++
			if calls == 1 {
				var boom *int
				_ = *boom // nil deref, the shape of the original crash
			}
			return &agentrewire.Empty{}, nil
		},
	)
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, serverRegistry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	_, err := protorpc.CallMethod(ctx, client, mcpProxyMethodID, &agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, protorpc.CodeInternal, rpcErr.Code)

	// The connection is still the same one: a panic must cost the caller one
	// request, not the session.
	_, err = protorpc.CallMethod(ctx, client, mcpProxyMethodID, &agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.NoError(t, err)
}

// Notification subscribers run synchronously on the read loop itself, so a panic
// there does not merely fail one delivery — it kills the goroutine that reads every
// response, notification and cancel for the connection.
func TestConnNotifications_GivenASubscriberThatPanics_WhenANotificationArrives_ThenTheReadLoopKeepsServing(t *testing.T) {
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
	delivered := make(chan struct{}, 1)
	clientRegistry := protorpc.NewRegistry()
	clientRegistry.RegisterNotification(func(context.Context, *agentrewire.RpcNotification) {
		select {
		case delivered <- struct{}{}:
		default:
		}
		panic("subscriber boom")
	})
	client := protorpc.NewConn(clientTransport, clientRegistry)
	server := protorpc.NewConn(serverTransport, serverRegistry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	require.NoError(t, server.Notify(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: &agentrewire.RuntimeEventNotification{Seq: 1}},
	}))
	select {
	case <-delivered:
	case <-time.After(2 * time.Second):
		t.Fatal("notification was never delivered")
	}

	_, err := protorpc.CallMethod(ctx, client, mcpProxyMethodID, &agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.NoError(t, err)
}

// A wedged peer — alive at the TCP level, no longer answering — used to hang every
// caller forever: `call` selects on the response, the caller's ctx and the transport,
// and Wails bindings hand down a ctx with no deadline at all. Given such a peer,
// When a call is made without a deadline, Then it must fail on the connection's own
// call budget.
func TestConnCall_GivenNoCallerDeadlineAndASilentPeer_WhenCalled_ThenItFailsWithTheDefaultCallTimeout(t *testing.T) {
	clientTransport, _ := pipePair() // the peer never serves: nothing ever answers
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry(),
		protorpc.WithCallTimeout(80*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)

	start := time.Now()
	_, err := protorpc.CallMethod(context.Background(), client, mcpProxyMethodID, &agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 2*time.Second)
}

// Some methods are legitimately long: runtime.run prepares a remote workspace (it may
// clone), and mcp.proxy carries one MCP tool call back to the desktop gateway. They
// opt out explicitly rather than being truncated by the default budget.
func TestConnCall_GivenTheCallerOptedOutOfTheCallTimeout_WhenThePeerIsSilent_ThenItKeepsWaiting(t *testing.T) {
	clientTransport, _ := pipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry(),
		protorpc.WithCallTimeout(50*time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)

	failed := make(chan error, 1)
	go func() {
		_, err := protorpc.CallMethod(protorpc.WithoutCallTimeout(context.Background()), client,
			mcpProxyMethodID, &agentrewire.Empty{}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
		failed <- err
	}()

	select {
	case err := <-failed:
		t.Fatalf("opted-out call must not time out, got %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	// Only the connection dying ends it.
	require.NoError(t, client.Close())
	select {
	case err := <-failed:
		require.True(t, errors.Is(err, protorpc.ErrConnClosed))
	case <-time.After(2 * time.Second):
		t.Fatal("closing the connection must wake the opted-out caller")
	}
}

// A caller that set its own deadline keeps it: the connection budget is a backstop
// for callers that set none, never a ceiling that shortens an explicit one.
func TestConnCall_GivenTheCallerSetItsOwnDeadline_WhenCalled_ThenTheCallerDeadlineWins(t *testing.T) {
	clientTransport, _ := pipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry(),
		protorpc.WithCallTimeout(time.Hour))
	serveCtx, cancelServe := context.WithCancel(context.Background())
	defer cancelServe()
	go client.Serve(serveCtx)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := protorpc.CallMethod(ctx, client, mcpProxyMethodID, &agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), 2*time.Second)
}

// 一条解不开的帧今天是 `continue` 掉的:没有日志、没有计数。线上排「对端说发了、
// 这边没收到」时,这一层什么都不说,只能靠猜。Given 一条坏帧,When 读循环收到它,
// Then 必须留下可 grep 的记录,而连接照常服务(坏帧不是断线理由)。
func TestConnServe_GivenAnUndecodableFrame_WhenReceived_ThenItIsRecordedAndTheConnectionKeepsServing(t *testing.T) {
	clientTransport, serverTransport := pipePair()
	serverRegistry := protorpc.NewRegistry()
	protorpc.RegisterMethod(serverRegistry, mcpProxyMethodID,
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) {
			return &agentrewire.Empty{}, nil
		})
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, serverRegistry)
	go client.Serve(ctx)
	go server.Serve(ctx)

	require.NoError(t, clientTransport.WriteFrame([]byte{0xff, 0xff, 0xff, 0xff}))

	require.Eventually(t, func() bool {
		return logs.FilterMessageSnippet("undecodable frame").Len() > 0
	}, 2*time.Second, 10*time.Millisecond, "坏帧必须留下记录")

	_, err := protorpc.CallMethod(ctx, client, mcpProxyMethodID, &agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} })
	require.NoError(t, err)
}

// 读循环退出就是这条连接的死亡时刻,而它今天是静悄悄退的 —— 「connection closed」
// 之后到底发生过什么,日志里一个字也没有。
func TestConnServe_GivenTheTransportFails_WhenTheReadLoopExits_ThenTheReasonIsRecorded(t *testing.T) {
	clientTransport, serverTransport := pipePair()
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	done := make(chan struct{})
	go func() { client.Serve(ctx); close(done) }()

	require.NoError(t, serverTransport.Close())

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("read loop did not exit")
	}
	require.NotZero(t, logs.FilterMessageSnippet("read loop exited").Len(),
		"连接死亡必须留下一条带原因的记录")
}

// write 今天把底层错误整个吞掉换成 ErrConnClosed:调用方只知道「连接关了」,不知道
// 是写超时、是 broken pipe 还是对端发了 close 帧。哨兵要留(调用方按它分支),原因
// 也要留。
func TestConnWrite_GivenTheTransportRejectsAWrite_WhenNotifying_ThenTheSentinelKeepsTheCause(t *testing.T) {
	transport := &failingWriteConn{err: errors.New("i/o timeout on write"), done: make(chan struct{})}
	conn := protorpc.NewConn(transport, protorpc.NewRegistry())

	err := conn.Notify(&agentrewire.RpcNotification{})

	require.ErrorIs(t, err, protorpc.ErrConnClosed)
	require.Contains(t, err.Error(), "i/o timeout on write")
}

type failingWriteConn struct {
	err  error
	done chan struct{}
}

func (c *failingWriteConn) ReadFrame() ([]byte, error) { <-c.done; return nil, io.EOF }
func (c *failingWriteConn) WriteFrame([]byte) error    { return c.err }
func (c *failingWriteConn) Close() error               { close(c.done); return nil }
func (c *failingWriteConn) Done() <-chan struct{}      { return c.done }
