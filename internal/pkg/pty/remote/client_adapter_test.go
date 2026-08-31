package remote_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/pty/remote"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubDaemonClient struct {
	conn      *protorpc.Conn
	server    *protorpc.Conn
	mu        sync.Mutex
	handlers  map[string]func(context.Context, json.RawMessage) (any, error)
	callFn    func(context.Context, string, any, any) error
	closed    chan struct{}
	closeOnce sync.Once
	closeErr  error
}

type adapterTestPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func adapterTestPipePair() (*adapterTestPipe, *adapterTestPipe) {
	a, b := make(chan []byte, 16), make(chan []byte, 16)
	d := make(chan struct{})
	o := &sync.Once{}
	return &adapterTestPipe{a, b, d, o}, &adapterTestPipe{b, a, d, o}
}
func (p *adapterTestPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}
func (p *adapterTestPipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}
func (p *adapterTestPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *adapterTestPipe) Done() <-chan struct{} { return p.done }

func newStubDaemonClient() *stubDaemonClient {
	a, b := adapterTestPipePair()
	serverRegistry := protorpc.NewRegistry()
	clientConn := protorpc.NewConn(a, protorpc.NewRegistry())
	s := &stubDaemonClient{conn: clientConn, closed: make(chan struct{}), handlers: map[string]func(context.Context, json.RawMessage) (any, error){}}
	protorpc.RegisterMethod(serverRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN), func() *agentrewire.TerminalOpenRequest { return &agentrewire.TerminalOpenRequest{} }, func(ctx context.Context, request *agentrewire.TerminalOpenRequest) (*agentrewire.TerminalOpenResponse, error) {
		result := protocol.TerminalOpenResult{}
		err := s.Call(ctx, "terminal.open", protocol.TerminalOpenParams{TerminalID: request.TerminalId, Cwd: request.Cwd, Shell: request.Shell, Command: request.Command, Env: request.Env, Cols: uint16(request.Cols), Rows: uint16(request.Rows)}, &result)
		return &agentrewire.TerminalOpenResponse{TerminalId: result.TerminalID}, err
	})
	protorpc.RegisterMethod(serverRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_CLOSE), func() *agentrewire.TerminalCloseRequest { return &agentrewire.TerminalCloseRequest{} }, func(ctx context.Context, request *agentrewire.TerminalCloseRequest) (*agentrewire.Empty, error) {
		err := s.Call(ctx, "terminal.close", protocol.TerminalCloseParams{TerminalID: request.TerminalId, CancelPendingOpen: request.CancelPendingOpen}, &struct{}{})
		return &agentrewire.Empty{}, err
	})
	protorpc.RegisterMethod(serverRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_WRITE), func() *agentrewire.TerminalWriteRequest { return &agentrewire.TerminalWriteRequest{} }, func(context.Context, *agentrewire.TerminalWriteRequest) (*agentrewire.Empty, error) {
		return &agentrewire.Empty{}, nil
	})
	protorpc.RegisterMethod(serverRegistry, uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_RESIZE), func() *agentrewire.TerminalResizeRequest { return &agentrewire.TerminalResizeRequest{} }, func(context.Context, *agentrewire.TerminalResizeRequest) (*agentrewire.Empty, error) {
		return &agentrewire.Empty{}, nil
	})
	serverConn := protorpc.NewConn(b, serverRegistry)
	s.server = serverConn
	ctx := context.Background()
	go clientConn.Serve(ctx)
	go serverConn.Serve(ctx)
	return s
}

func (s *stubDaemonClient) Conn() *protorpc.Conn { return s.conn }

func (s *stubDaemonClient) Call(ctx context.Context, method string, params any, out any) error {
	s.mu.Lock()
	fn := s.callFn
	s.mu.Unlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, method, params, out)
}

func (s *stubDaemonClient) setCall(fn func(context.Context, string, any, any) error) {
	s.mu.Lock()
	s.callFn = fn
	s.mu.Unlock()
}

func (s *stubDaemonClient) Handle(method string, fn func(context.Context, json.RawMessage) (any, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[method] = fn
}

func (s *stubDaemonClient) Closed() <-chan struct{} { return s.closed }

func (s *stubDaemonClient) Close() error {
	s.mu.Lock()
	closeErr := s.closeErr
	s.mu.Unlock()
	if closeErr != nil {
		return closeErr
	}
	s.closeOnce.Do(func() { close(s.closed); _ = s.conn.Close() })
	return nil
}

func (s *stubDaemonClient) setCloseError(err error) {
	s.mu.Lock()
	s.closeErr = err
	s.mu.Unlock()
}

func (s *stubDaemonClient) dispatch(method string, payload any) error {
	var notification *agentrewire.RpcNotification
	switch event := payload.(type) {
	case protocol.TerminalDataEvent:
		notification = &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_TerminalData{TerminalData: &agentrewire.TerminalDataNotification{TerminalId: event.TerminalID, Data: event.Data}}}
	case protocol.TerminalExitEvent:
		notification = &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_TerminalExit{TerminalExit: &agentrewire.TerminalExitNotification{TerminalId: event.TerminalID, Code: int32(event.Code), Reason: event.Reason, Message: event.Msg}}}
	default:
		return fmt.Errorf("unsupported notification payload %T for %s", payload, method)
	}
	err := s.server.Notify(notification)
	if errors.Is(err, protorpc.ErrConnClosed) {
		return nil
	}
	if err == nil {
		time.Sleep(50 * time.Microsecond)
	}
	return err
}

func (s *stubDaemonClient) push(t *testing.T, method string, payload any) {
	t.Helper()
	var notification *agentrewire.RpcNotification
	switch event := payload.(type) {
	case protocol.TerminalDataEvent:
		notification = &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_TerminalData{TerminalData: &agentrewire.TerminalDataNotification{TerminalId: event.TerminalID, Data: event.Data}}}
	case protocol.TerminalExitEvent:
		notification = &agentrewire.RpcNotification{Payload: &agentrewire.RpcNotification_TerminalExit{TerminalExit: &agentrewire.TerminalExitNotification{TerminalId: event.TerminalID, Code: int32(event.Code), Reason: event.Reason, Message: event.Msg}}}
	default:
		t.Fatalf("unsupported notification payload %T for %s", payload, method)
	}
	err := s.server.Notify(notification)
	if errors.Is(err, protorpc.ErrConnClosed) {
		return
	}
	require.NoError(t, err)
	time.Sleep(50 * time.Microsecond)
}

func TestClientAdapter_GivenWrappedConnectionCloseFailureWhenAbortedThenReportsFailure(t *testing.T) {
	closeErr := fmt.Errorf("websocket close failed")
	c := newStubDaemonClient()
	c.setCloseError(closeErr)
	a := remote.NewClientAdapter(c)

	require.ErrorIs(t, a.Abort(), closeErr)
	c.setCloseError(nil)
	require.NoError(t, a.Abort())
}

func TestClientAdapter_GivenWrappedConnectionWhenObservedThenExposesItsStableClosedSignal(t *testing.T) {
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)

	require.Equal(t, c.Closed(), a.Closed())
	select {
	case <-a.Closed():
		t.Fatal("adapter reported the connection closed before its wrapped client")
	default:
	}
	require.NoError(t, a.Abort())
	select {
	case <-a.Closed():
	case <-time.After(time.Second):
		t.Fatal("adapter did not expose the wrapped connection close")
	}
}

func TestClientAdapter_GivenAtomicSubscriptionsWhenDataArrivesThenDemuxesByTerminalID(t *testing.T) {
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)
	t.Cleanup(func() { _ = a.Abort() })
	subA := a.Subscribe("term-a")
	subB := a.Subscribe("term-b")

	c.push(t, "terminal.data", protocol.TerminalDataEvent{TerminalID: "term-a", Data: []byte("alpha")})
	c.push(t, "terminal.data", protocol.TerminalDataEvent{TerminalID: "term-b", Data: []byte("beta")})

	select {
	case ev := <-subA.Data:
		assert.Equal(t, []byte("alpha"), ev.Data)
	case <-time.After(time.Second):
		t.Fatal("no data for term-a")
	}
	select {
	case ev := <-subB.Data:
		assert.Equal(t, []byte("beta"), ev.Data)
	case <-time.After(time.Second):
		t.Fatal("no data for term-b")
	}
}

func TestClientAdapter_GivenFastStartupBurstBeforeConsumerStartsWhenExitArrivesThenDeliversEveryFrameFIFOFirst(t *testing.T) {
	const frameCount = 128
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)
	t.Cleanup(func() { _ = a.Abort() })
	sub := a.Subscribe("term-fast-startup")

	producerDone := make(chan error, 1)
	go func() {
		for i := 0; i < frameCount; i++ {
			if err := c.dispatch("terminal.data", protocol.TerminalDataEvent{
				TerminalID: "term-fast-startup",
				Data:       []byte(fmt.Sprintf("frame-%03d", i)),
			}); err != nil {
				producerDone <- err
				return
			}
		}
		producerDone <- c.dispatch("terminal.exit", protocol.TerminalExitEvent{
			TerminalID: "term-fast-startup",
			Code:       0,
			Reason:     "natural",
		})
	}()

	select {
	case err := <-producerDone:
		require.NoError(t, err, "notification handlers must not wait for the consumer")
	case <-time.After(time.Second):
		t.Fatal("startup notification handlers blocked on an unread subscription")
	}
	select {
	case ev, ok := <-sub.Exit:
		t.Fatalf("exit became observable before accepted data drained: ok=%v event=%+v", ok, ev)
	default:
	}

	for i := 0; i < frameCount; i++ {
		select {
		case ev, ok := <-sub.Data:
			require.Truef(t, ok, "data closed after %d of %d frames", i, frameCount)
			require.Equal(t, []byte(fmt.Sprintf("frame-%03d", i)), ev.Data)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for frame %d of %d", i, frameCount)
		}
	}
	select {
	case _, ok := <-sub.Data:
		require.False(t, ok, "data channel must close after the accepted burst")
	case <-time.After(time.Second):
		t.Fatal("data channel did not close after the accepted burst")
	}
	select {
	case ev, ok := <-sub.Exit:
		require.True(t, ok, "exit channel closed without the daemon outcome")
		require.Equal(t, "natural", ev.Reason)
	case <-time.After(time.Second):
		t.Fatal("exit did not follow the accepted burst")
	}
	_, ok := <-sub.Exit
	require.False(t, ok, "exit channel must close after exactly one outcome")
}

func TestClientAdapter_GivenBlockedConsumerAndOverCapBurstWhenExitArrivesThenBoundsFIFOWithOneThrottleMarkerAndNewestTail(t *testing.T) {
	const (
		queueCapacity = 256
		frameCount    = queueCapacity * 4
	)
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)
	t.Cleanup(func() { _ = a.Abort() })
	sub := a.Subscribe("term-over-cap")

	producerDone := make(chan error, 1)
	go func() {
		for i := 0; i < frameCount; i++ {
			if err := c.dispatch("terminal.data", protocol.TerminalDataEvent{
				TerminalID: "term-over-cap",
				Data:       []byte(fmt.Sprintf("frame-%04d", i)),
			}); err != nil {
				producerDone <- err
				return
			}
		}
		producerDone <- c.dispatch("terminal.exit", protocol.TerminalExitEvent{
			TerminalID: "term-over-cap",
			Code:       0,
			Reason:     "natural",
		})
	}()

	select {
	case err := <-producerDone:
		require.NoError(t, err, "over-cap notification handlers must not wait for the consumer")
	case <-time.After(time.Second):
		t.Fatal("over-cap notification handlers blocked on an unread subscription")
	}

	var events []protocol.TerminalDataEvent
	for ev := range sub.Data {
		events = append(events, ev)
	}
	require.LessOrEqual(t, len(events), queueCapacity+1,
		"the bounded queue may have at most one frame already handed to its worker")
	require.NotEmpty(t, events)

	throttleData := []byte("\r\n[--- output throttled ---]\r\n")
	markerCount := 0
	markerIndex := -1
	lastFrame := -1
	for i, ev := range events {
		if bytes.Equal(ev.Data, throttleData) {
			markerCount++
			markerIndex = i
			continue
		}
		var frame int
		_, err := fmt.Sscanf(string(ev.Data), "frame-%04d", &frame)
		require.NoError(t, err)
		require.Greater(t, frame, lastFrame, "retained data must stay FIFO around the marker")
		lastFrame = frame
	}
	require.Equal(t, 1, markerCount, "one overload episode must not create a marker storm")
	require.GreaterOrEqual(t, markerIndex, 0)
	require.Less(t, markerIndex, len(events)-1, "the marker must precede retained newest data")
	require.Equal(t, frameCount-1, lastFrame, "the newest output tail must be retained")

	select {
	case ev, ok := <-sub.Exit:
		require.True(t, ok, "exit channel closed without the daemon outcome")
		require.Equal(t, "natural", ev.Reason)
	case <-time.After(time.Second):
		t.Fatal("exit did not follow the bounded data queue")
	}
	_, ok := <-sub.Exit
	require.False(t, ok, "exit channel must close after exactly one outcome")
}

func TestClientAdapter_GivenExitWhenDeliveredThenClosesTheSameGenerationPair(t *testing.T) {
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)
	t.Cleanup(func() { _ = a.Abort() })
	sub := a.Subscribe("term-x")

	c.push(t, "terminal.exit", protocol.TerminalExitEvent{TerminalID: "term-x", Code: 0, Reason: "natural"})

	select {
	case ev := <-sub.Exit:
		assert.Equal(t, "natural", ev.Reason)
	case <-time.After(time.Second):
		t.Fatal("no exit event")
	}
	_, ok := <-sub.Exit
	assert.False(t, ok, "exit channel should be closed")
	_, ok = <-sub.Data
	assert.False(t, ok, "data channel should be closed")
}

func TestClientAdapter_GivenStaleUnsubscribeWhenANewGenerationExistsThenKeepsNewPairRegistered(t *testing.T) {
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)
	t.Cleanup(func() { _ = a.Abort() })
	first := a.Subscribe("term-generation")
	second := a.Subscribe("term-generation")
	require.NotEqual(t, first.Data, second.Data)
	require.NotEqual(t, first.Exit, second.Exit)

	a.Unsubscribe("term-generation", first)
	c.push(t, "terminal.data", protocol.TerminalDataEvent{
		TerminalID: "term-generation",
		Data:       []byte("current"),
	})

	select {
	case ev := <-second.Data:
		assert.Equal(t, []byte("current"), ev.Data)
	case <-time.After(time.Second):
		t.Fatal("stale unsubscribe removed the current generation")
	}
	_, ok := <-first.Data
	assert.False(t, ok, "replacement must close the stale data generation")
	_, ok = <-first.Exit
	assert.False(t, ok, "replacement must close the stale exit generation")
}

func TestClientAdapter_GivenConnectionCloseWhenSubscriptionsExistThenClosesAllAndFuturePairs(t *testing.T) {
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)
	sub1 := a.Subscribe("t1")
	sub2 := a.Subscribe("t2")

	require.NoError(t, a.Abort())

	for _, ch := range []<-chan protocol.TerminalExitEvent{sub1.Exit, sub2.Exit} {
		select {
		case _, ok := <-ch:
			assert.False(t, ok, "exit channel should be closed on connection close")
		case <-time.After(time.Second):
			t.Fatal("exit channel not closed within 1s")
		}
	}
	require.Eventually(t, func() bool {
		sub := a.Subscribe("after-close")
		select {
		case _, ok := <-sub.Exit:
			return !ok
		default:
			return false
		}
	}, time.Second, time.Millisecond, "subscription registered after close must already be closed")
}

func TestClientAdapter_GivenUnknownTerminalFloodWhenDeliveredThenDoesNotReplayOrAllocateSubscriptions(t *testing.T) {
	const unknownCount = 1000
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)
	t.Cleanup(func() { _ = a.Abort() })
	for i := 0; i < unknownCount; i++ {
		terminalID := fmt.Sprintf("ghost-%04d", i)
		c.push(t, "terminal.data", protocol.TerminalDataEvent{TerminalID: terminalID, Data: []byte("ignored")})
		c.push(t, "terminal.exit", protocol.TerminalExitEvent{TerminalID: terminalID, Reason: "natural"})
	}

	sub := a.Subscribe("ghost-0500")
	select {
	case ev, ok := <-sub.Data:
		t.Fatalf("unknown event was retained: ok=%v event=%+v", ok, ev)
	default:
	}
	select {
	case ev, ok := <-sub.Exit:
		t.Fatalf("unknown exit was retained: ok=%v event=%+v", ok, ev)
	default:
	}
}

func TestClientAdapter_GivenUnreadSpoolWhenUnsubscribedThenCancelsWorkerAndDiscardsQueuedFrames(t *testing.T) {
	const frameCount = 128
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)
	t.Cleanup(func() { _ = a.Abort() })
	sub := a.Subscribe("term-unsubscribe")
	for i := 0; i < frameCount; i++ {
		c.push(t, "terminal.data", protocol.TerminalDataEvent{
			TerminalID: "term-unsubscribe",
			Data:       []byte(fmt.Sprintf("queued-%03d", i)),
		})
	}
	c.push(t, "terminal.exit", protocol.TerminalExitEvent{
		TerminalID: "term-unsubscribe",
		Reason:     "natural",
	})

	// An open failure can unsubscribe after the daemon has already pushed an
	// exit; that exact generation must still be cancelable without a consumer.
	a.Unsubscribe("term-unsubscribe", sub)

	select {
	case ev, ok := <-sub.Data:
		require.Falsef(t, ok, "unsubscribe leaked queued data: %+v", ev)
	case <-time.After(time.Second):
		t.Fatal("blocked delivery worker did not stop after unsubscribe")
	}
	select {
	case ev, ok := <-sub.Exit:
		require.Falsef(t, ok, "unsubscribe published an exit value: %+v", ev)
	case <-time.After(time.Second):
		t.Fatal("exit channel did not close after unsubscribe")
	}
}

func TestClientAdapter_GivenAcceptedBurstWhenConnectionClosesThenDrainsDataBeforeClosingExitWithoutValue(t *testing.T) {
	const frameCount = 128
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)
	sub := a.Subscribe("term-connection-close")
	for i := 0; i < frameCount; i++ {
		c.push(t, "terminal.data", protocol.TerminalDataEvent{
			TerminalID: "term-connection-close",
			Data:       []byte(fmt.Sprintf("accepted-%03d", i)),
		})
	}

	require.NoError(t, a.Abort())
	probe := a.Subscribe("connection-close-probe")
	select {
	case _, ok := <-probe.Exit:
		require.False(t, ok, "connection-close probe published an exit value")
	case <-time.After(time.Second):
		t.Fatal("connection close was not observed")
	}
	select {
	case ev, ok := <-sub.Exit:
		t.Fatalf("connection close ended the subscription before accepted data drained: ok=%v event=%+v", ok, ev)
	default:
	}

	for i := 0; i < frameCount; i++ {
		select {
		case ev, ok := <-sub.Data:
			require.Truef(t, ok, "data closed after %d of %d accepted frames", i, frameCount)
			require.Equal(t, []byte(fmt.Sprintf("accepted-%03d", i)), ev.Data)
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for accepted frame %d of %d", i, frameCount)
		}
	}
	select {
	case _, ok := <-sub.Data:
		require.False(t, ok, "data channel must close after draining accepted frames")
	case <-time.After(time.Second):
		t.Fatal("data channel did not close after connection-close drain")
	}
	select {
	case ev, ok := <-sub.Exit:
		require.Falsef(t, ok, "connection close published an exit value: %+v", ev)
	case <-time.After(time.Second):
		t.Fatal("exit channel did not close after connection-close drain")
	}
}

func TestClientAdapter_GivenDeliveryExitUnsubscribeAndWatchCloseRacesThenNeverUsesOrLeaksClosedGeneration(t *testing.T) {
	const (
		iterations     = 120
		framesPerBurst = 512
	)
	c := newStubDaemonClient()
	a := remote.NewClientAdapter(c)
	dataHandler := func(id string) error {
		return c.dispatch("terminal.data", protocol.TerminalDataEvent{TerminalID: id, Data: []byte("chunk")})
	}
	exitHandler := func(id string) error {
		return c.dispatch("terminal.exit", protocol.TerminalExitEvent{TerminalID: id, Reason: "natural"})
	}

	var operations sync.WaitGroup
	var consumers sync.WaitGroup
	errs := make(chan error, iterations*2+1)
	for i := 0; i < iterations; i++ {
		id := fmt.Sprintf("race-%03d", i)
		sub := a.Subscribe(id)
		consumers.Add(1)
		go func() {
			defer consumers.Done()
			for range sub.Data {
			}
			for range sub.Exit {
			}
		}()
		start := make(chan struct{})
		operations.Add(3)
		go func() {
			defer operations.Done()
			<-start
			for range framesPerBurst {
				if err := dataHandler(id); err != nil {
					errs <- err
					return
				}
			}
		}()
		go func() {
			defer operations.Done()
			<-start
			if err := exitHandler(id); err != nil {
				errs <- err
			}
		}()
		go func() {
			defer operations.Done()
			<-start
			a.Unsubscribe(id, sub)
		}()
		close(start)
	}
	operations.Add(1)
	go func() {
		defer operations.Done()
		if err := a.Abort(); err != nil {
			errs <- err
		}
	}()
	operations.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	consumerDone := make(chan struct{})
	go func() {
		consumers.Wait()
		close(consumerDone)
	}()
	select {
	case <-consumerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("subscription delivery workers did not all terminate")
	}
}

// SelfFingerprint 满足 client.ProtobufConnection:这个假连接从没握过手,本端指纹为空。
func (c *stubDaemonClient) SelfFingerprint() string { return "" }
