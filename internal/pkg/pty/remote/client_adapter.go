package remote

import (
	"context"
	"errors"
	"sync"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// DaemonClient is the subset of internal/daemon/client.Client and
// agentruntime.DaemonClientPort that ClientAdapter consumes. Declared here
// to avoid this package depending on daemon/client; production wires the
// real *client.Client.
type DaemonClient interface {
	Conn() *protorpc.Conn
	Closed() <-chan struct{}
	Close() error
}

const (
	// terminalQueueCapacity mirrors the daemon-side throttle queue. With the
	// daemon's 8 KiB PTY reads, 256 pending binary frames cap one terminal near
	// 2.7 MiB, plus at most one frame already handed to the delivery worker.
	terminalQueueCapacity = 256
	terminalQueueLowWater = terminalQueueCapacity / 2
)

var (
	terminalThrottleData = []byte("\r\n[--- output throttled ---]\r\n")
)

// ClientAdapter wraps a single daemon client and demuxes per-terminal push
// events. Each Subscribe call atomically installs one data/exit generation.
// Notification handlers only append to that generation's bounded FIFO under
// mu; its sole delivery worker owns channel sends and closure.
type ClientAdapter struct {
	client           DaemonClient
	connectionClosed <-chan struct{}

	mu     sync.Mutex
	subs   map[string]*terminalSubscription
	closed bool
}

type terminalSubscription struct {
	data   chan protocol.TerminalDataEvent
	exit   chan protocol.TerminalExitEvent
	wake   chan struct{}
	cancel chan struct{}
	done   chan struct{}

	head   *terminalDataNode
	tail   *terminalDataNode
	queued int

	throttled    bool
	markerQueued bool
	ending       bool
	canceled     bool
	hasExit      bool
	exitEvent    protocol.TerminalExitEvent
}

type terminalDataNode struct {
	event  protocol.TerminalDataEvent
	marker bool
	next   *terminalDataNode
}

type deliveryState uint8

const (
	deliveryWait deliveryState = iota
	deliveryData
	deliveryEnd
	deliveryCanceled
)

// NewClientAdapter wires up the push-event demux. Spawns one goroutine for
// connection-close detection. The handler registrations are register-once;
// constructing a second ClientAdapter against the same client would overwrite
// them, so callers keep at most one adapter per client instance.
func NewClientAdapter(c DaemonClient) *ClientAdapter {
	closed := c.Closed()
	a := &ClientAdapter{
		client:           c,
		connectionClosed: closed,
		subs:             map[string]*terminalSubscription{},
	}
	c.Conn().Registry().SubscribeNotification(a.handleNotification)
	if closed != nil {
		go a.watchClose(closed)
	}
	return a
}

// Call passes through to the underlying client.
func (a *ClientAdapter) Call(ctx context.Context, method string, params any, out any) error {
	switch method {
	case "terminal.open":
		request := params.(protocol.TerminalOpenParams)
		// conversation_id 不置:LAN 直连这条路上的终端不挂在任何一条对话下(它一直是
		// 零值,daemon 侧也从不读它),没有身份可报就如实留空,而不是编一个。
		response, err := protorpc.CallMethod(ctx, a.client.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_OPEN), &agentrewire.TerminalOpenRequest{TerminalId: request.TerminalID, Cwd: request.Cwd, Shell: request.Shell, Command: request.Command, Env: request.Env, Cols: uint32(request.Cols), Rows: uint32(request.Rows)}, func() *agentrewire.TerminalOpenResponse { return &agentrewire.TerminalOpenResponse{} })
		if err == nil {
			out.(*protocol.TerminalOpenResult).TerminalID = response.TerminalId
		}
		return err
	case "terminal.write":
		request := params.(protocol.TerminalWriteParams)
		_, err := protorpc.CallMethod(ctx, a.client.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_WRITE), &agentrewire.TerminalWriteRequest{TerminalId: request.TerminalID, Data: []byte(request.Data)}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
		return err
	case "terminal.resize":
		request := params.(protocol.TerminalResizeParams)
		_, err := protorpc.CallMethod(ctx, a.client.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_RESIZE), &agentrewire.TerminalResizeRequest{TerminalId: request.TerminalID, Cols: uint32(request.Cols), Rows: uint32(request.Rows)}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
		return err
	case "terminal.close":
		request := params.(protocol.TerminalCloseParams)
		_, err := protorpc.CallMethod(ctx, a.client.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_TERMINAL_CLOSE), &agentrewire.TerminalCloseRequest{TerminalId: request.TerminalID, CancelPendingOpen: request.CancelPendingOpen}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
		return err
	default:
		return errors.New("remote terminal: unsupported method")
	}
}

func (a *ClientAdapter) handleNotification(_ context.Context, notification *agentrewire.RpcNotification) error {
	if event := notification.GetTerminalData(); event != nil {
		a.enqueueData(protocol.TerminalDataEvent{TerminalID: event.TerminalId, Data: append([]byte(nil), event.Data...)})
	}
	if event := notification.GetTerminalExit(); event != nil {
		a.enqueueExit(protocol.TerminalExitEvent{TerminalID: event.TerminalId, Code: int(event.Code), Reason: event.Reason, Msg: event.Message})
	}
	return nil
}

// Closed exposes the stable connection-generation signal used by cleanup
// guardians to retain ownership until the shared daemon connection is gone.
func (a *ClientAdapter) Closed() <-chan struct{} {
	return a.connectionClosed
}

// Subscribe atomically creates and registers one data/exit channel pair for a
// terminal ID. A newer registration replaces and closes the older generation;
// its later Unsubscribe cannot remove the replacement because identity is
// checked against both exact channel references.
func (a *ClientAdapter) Subscribe(terminalID string) Subscription {
	sub := newTerminalSubscription()
	var previousDone <-chan struct{}
	a.mu.Lock()
	closed := a.closed
	if closed {
		a.finishSubscriptionLocked(sub, nil)
	} else {
		previous := a.subs[terminalID]
		a.subs[terminalID] = sub
		if previous != nil {
			a.cancelSubscriptionLocked(previous)
			previousDone = previous.done
		}
	}
	a.mu.Unlock()
	if previousDone != nil {
		<-previousDone
	}
	go a.deliver(terminalID, sub)
	if closed {
		<-sub.done
	}
	return subscriptionView(sub)
}

// Unsubscribe removes and closes only the exact generation returned by
// Subscribe. It is safe after exit, connection loss, or replacement.
func (a *ClientAdapter) Unsubscribe(terminalID string, subscription Subscription) {
	var done <-chan struct{}
	a.mu.Lock()
	sub := a.subs[terminalID]
	if sub != nil && sub.data == subscription.Data && sub.exit == subscription.Exit {
		delete(a.subs, terminalID)
		a.cancelSubscriptionLocked(sub)
		done = sub.done
	}
	a.mu.Unlock()
	if done != nil {
		<-done
	}
}

// Abort closes the wrapped shared connection when an RPC cannot be
// acknowledged. Production daemon clients implement Close; keeping it as an
// optional narrow assertion avoids coupling the demux interface to unrelated
// client operations while still reporting whether the safety fallback ran.
func (a *ClientAdapter) Abort() error {
	return a.client.Close()
}

func newTerminalSubscription() *terminalSubscription {
	return &terminalSubscription{
		data:   make(chan protocol.TerminalDataEvent),
		exit:   make(chan protocol.TerminalExitEvent, 1),
		wake:   make(chan struct{}, 1),
		cancel: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

func subscriptionView(sub *terminalSubscription) Subscription {
	return Subscription{Data: sub.data, Exit: sub.exit}
}

func (a *ClientAdapter) enqueueData(ev protocol.TerminalDataEvent) {
	a.mu.Lock()
	sub := a.subs[ev.TerminalID]
	if sub != nil && !sub.ending && !sub.canceled {
		enqueueTerminalData(sub, ev)
		signalSubscription(sub)
	}
	a.mu.Unlock()
}

func (a *ClientAdapter) enqueueExit(ev protocol.TerminalExitEvent) {
	a.mu.Lock()
	sub := a.subs[ev.TerminalID]
	if sub != nil {
		a.finishSubscriptionLocked(sub, &ev)
	}
	a.mu.Unlock()
}

func (a *ClientAdapter) watchClose(closed <-chan struct{}) {
	<-closed
	a.mu.Lock()
	a.closed = true
	for _, sub := range a.subs {
		// A connection close preserves bytes already accepted by the handler,
		// but carries no authoritative terminal.exit value.
		a.finishSubscriptionLocked(sub, nil)
	}
	a.mu.Unlock()
}

func enqueueTerminalData(sub *terminalSubscription, ev protocol.TerminalDataEvent) {
	if sub.queued == terminalQueueCapacity {
		if !sub.throttled {
			// Preserve FIFO around one explicit marker: retained older data stays
			// before it and the newest frame is appended after it. Two old data
			// frames make room for marker + newest without exceeding the cap.
			dropOldestTerminalData(sub)
			dropOldestTerminalData(sub)
			appendTerminalData(sub, protocol.TerminalDataEvent{
				TerminalID: ev.TerminalID,
				Data:       terminalThrottleData,
			}, true)
			sub.throttled = true
			sub.markerQueued = true
		} else {
			// A queued or recently delivered marker already represents this
			// sustained overload episode. Retain it and replace the oldest data
			// with the newest frame rather than creating marker storms.
			dropOldestTerminalData(sub)
		}
	}
	appendTerminalData(sub, ev, false)
}

func appendTerminalData(sub *terminalSubscription, ev protocol.TerminalDataEvent, marker bool) {
	node := &terminalDataNode{event: ev, marker: marker}
	if sub.tail == nil {
		sub.head = node
	} else {
		sub.tail.next = node
	}
	sub.tail = node
	sub.queued++
}

func dropOldestTerminalData(sub *terminalSubscription) {
	var previous *terminalDataNode
	for node := sub.head; node != nil; node = node.next {
		if node.marker {
			previous = node
			continue
		}
		if previous == nil {
			sub.head = node.next
		} else {
			previous.next = node.next
		}
		if sub.tail == node {
			sub.tail = previous
		}
		node.next = nil
		sub.queued--
		return
	}
}

func signalSubscription(sub *terminalSubscription) {
	select {
	case sub.wake <- struct{}{}:
	default:
	}
}

func (a *ClientAdapter) finishSubscriptionLocked(
	sub *terminalSubscription,
	exitEvent *protocol.TerminalExitEvent,
) {
	if sub.ending || sub.canceled {
		return
	}
	sub.ending = true
	if exitEvent != nil {
		sub.hasExit = true
		sub.exitEvent = *exitEvent
	}
	signalSubscription(sub)
}

func (a *ClientAdapter) cancelSubscriptionLocked(sub *terminalSubscription) {
	if sub.canceled {
		return
	}
	sub.canceled = true
	sub.head = nil
	sub.tail = nil
	sub.queued = 0
	sub.markerQueued = false
	close(sub.cancel)
}

func (a *ClientAdapter) deliver(terminalID string, sub *terminalSubscription) {
	defer func() {
		close(sub.data)
		close(sub.exit)
		close(sub.done)
		a.mu.Lock()
		if a.subs[terminalID] == sub {
			delete(a.subs, terminalID)
		}
		a.mu.Unlock()
	}()

	for {
		state, dataEvent, hasExit, exitEvent := a.nextDelivery(sub)
		switch state {
		case deliveryData:
			select {
			case sub.data <- dataEvent:
			case <-sub.cancel:
				return
			}
		case deliveryEnd:
			if hasExit {
				select {
				case sub.exit <- exitEvent:
				case <-sub.cancel:
					return
				}
			}
			return
		case deliveryCanceled:
			return
		case deliveryWait:
			select {
			case <-sub.wake:
			case <-sub.cancel:
				return
			}
		}
	}
}

func (a *ClientAdapter) nextDelivery(
	sub *terminalSubscription,
) (deliveryState, protocol.TerminalDataEvent, bool, protocol.TerminalExitEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if sub.canceled {
		return deliveryCanceled, protocol.TerminalDataEvent{}, false, protocol.TerminalExitEvent{}
	}
	if sub.head != nil {
		node := sub.head
		sub.head = node.next
		if sub.head == nil {
			sub.tail = nil
		}
		sub.queued--
		if node.marker {
			sub.markerQueued = false
		}
		if !sub.markerQueued && sub.queued <= terminalQueueLowWater {
			sub.throttled = false
		}
		node.next = nil
		return deliveryData, node.event, false, protocol.TerminalExitEvent{}
	}
	if sub.ending {
		return deliveryEnd, protocol.TerminalDataEvent{}, sub.hasExit, sub.exitEvent
	}
	return deliveryWait, protocol.TerminalDataEvent{}, false, protocol.TerminalExitEvent{}
}

// Compile-time assertions: ClientAdapter satisfies the terminal RPC and
// connection-lifecycle seams consumed by Backend.
var _ Client = (*ClientAdapter)(nil)
var _ closedClient = (*ClientAdapter)(nil)
