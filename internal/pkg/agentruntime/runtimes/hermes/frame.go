package hermes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// errSessionClosed marks an RPC attempted on a gateway connection that has
// already been torn down (explicit Close or a lost socket). It is not a protocol
// failure: the turn it belonged to is over. Control paths translate it into
// "no in-flight turn" instead of surfacing a raw write error, which is what a
// Stop click landing on the tail of a turn would otherwise see.
var errSessionClosed = errors.New("hermes gateway: closed")

// rpcRequest is a client->server JSON-RPC 2.0 request. IDs are strings so the
// pairing key never depends on json.Number formatting.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcFrame is one decoded wire frame. A response carries ID + Result/Error; an
// event carries Method="event" + Params; a server->client request carries
// Method + ID and is answered with the same ID.
type rpcFrame struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("hermes gateway: rpc error %d: %s", e.Code, e.Message)
}

// rpcCallError carries the JSON-RPC error so callers can recognize machine
// readable data (e.g. data.reason) rather than string matching the message.
type rpcCallError struct {
	Code    int
	Message string
	Data    json.RawMessage
	Method  string
}

func (e *rpcCallError) Error() string {
	return fmt.Sprintf("hermes gateway: %s failed (%d): %s", e.Method, e.Code, e.Message)
}

// rpcConn is the transport-agnostic JSON-RPC 2.0 codec shared by every Hermes
// transport. It owns the request id sequence, the pending-response map, event
// decoding, reverse-request answering and the close signal; a transport only
// feeds it newline-delimited frames and supplies a serialized writer
// (WebSocket text frames today).
//
// Keeping the codec separate is what lets the WS client reuse the exact pairing
// and event semantics of the protocol instead of reimplementing them.
type rpcConn struct {
	writeMu sync.Mutex
	write   func([]byte) error

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan rpcFrame
	readErr error

	events    chan Event
	ready     chan struct{}
	readyOnce sync.Once

	closeCh   chan struct{}
	closeOnce sync.Once

	done     chan struct{}
	doneOnce sync.Once

	eventsOnce sync.Once
}

func newRPCConn(write func([]byte) error) *rpcConn {
	return &rpcConn{
		write:   write,
		pending: map[string]chan rpcFrame{},
		events:  make(chan Event, gatewayEventsBuffer),
		ready:   make(chan struct{}),
		closeCh: make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Events streams decoded event frames; it closes when the transport ends.
func (c *rpcConn) Events() <-chan Event { return c.events }

// Err reports the terminal transport error (nil while the stream is healthy).
func (c *rpcConn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
}

// WaitReady blocks until the gateway.ready frame arrives, the transport dies, or
// ctx/the ready timeout elapses. Nothing may be assumed before this frame.
func (c *rpcConn) WaitReady(ctx context.Context) error {
	timer := time.NewTimer(gatewayReadyTimeout)
	defer timer.Stop()
	select {
	case <-c.ready:
		return nil
	case <-c.done:
		if err := c.Err(); err != nil {
			return err
		}
		return errors.New("hermes gateway: connection ended before gateway.ready")
	case <-c.closeCh:
		return errSessionClosed
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("hermes gateway: timed out waiting for gateway.ready")
	}
}

// signalClose marks the connection as deliberately torn down. It must run before
// the socket is closed so a racing write failure is reported as "closed" (a
// finished turn) rather than a protocol error.
func (c *rpcConn) signalClose() {
	c.closeOnce.Do(func() {
		close(c.closeCh)
		c.failPending(errSessionClosed)
	})
}

// failConnection records the terminal transport error, closes the event stream
// and unblocks every pending call. It is idempotent.
func (c *rpcConn) failConnection(err error) {
	if err == nil {
		err = errors.New("hermes gateway: connection ended")
	}
	c.mu.Lock()
	if c.readErr == nil {
		c.readErr = err
	}
	c.mu.Unlock()
	c.doneOnce.Do(func() { close(c.done) })
	c.failPending(err)
	c.eventsOnce.Do(func() { close(c.events) })
}

// isClosed reports whether the connection has been deliberately closed.
func (c *rpcConn) isClosed() bool {
	select {
	case <-c.closeCh:
		return true
	default:
		return false
	}
}

// isFinished reports whether the connection is unusable — deliberately closed or
// already failed. It is the race-free authority for "this turn is over", never
// the write error text.
func (c *rpcConn) isFinished() bool {
	if c.isClosed() {
		return true
	}
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

// handleLine decodes and routes one newline-delimited wire line. A non-nil error
// means the transport should stop feeding frames (the close signal fired).
func (c *rpcConn) handleLine(line []byte) error {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return nil
	}
	var frame rpcFrame
	if err := json.Unmarshal(line, &frame); err != nil {
		// A separate process may drift its schema; a junk line must not wedge a
		// live turn.
		return nil //nolint:nilerr // intentional: drop the bad frame, keep the stream
	}
	if frame.Method == "event" {
		ev, ok := decodeEventFrame(frame.Params)
		if !ok {
			return nil
		}
		if ev.Kind == EventGatewayReady {
			c.readyOnce.Do(func() { close(c.ready) })
		}
		select {
		case c.events <- ev:
			return nil
		case <-c.closeCh:
			return errSessionClosed
		}
	}
	if len(frame.ID) == 0 {
		return nil
	}
	if frame.Method != "" {
		c.answerReverseRequest(frame)
		return nil
	}
	c.deliver(frame)
	return nil
}

// answerReverseRequest answers a server->client JSON-RPC request with the same
// id. No reverse method is implemented yet (capability is abort-only, and
// approvals/clarify arrive as events), so every card gets -32601 method not
// found instead of leaving the server waiting.
func (c *rpcConn) answerReverseRequest(frame rpcFrame) {
	response := map[string]any{
		"jsonrpc": "2.0",
		"id":      json.RawMessage(frame.ID),
		"error":   &rpcError{Code: -32601, Message: "method not found"},
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return
	}
	_ = c.writeLine(payload)
}

func (c *rpcConn) deliver(frame rpcFrame) {
	id := idString(frame.ID)
	if id == "" {
		return
	}
	c.mu.Lock()
	ch := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- frame:
		default:
		}
	}
}

func (c *rpcConn) failPending(err error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = map[string]chan rpcFrame{}
	c.mu.Unlock()
	for _, ch := range pending {
		select {
		case ch <- rpcFrame{Error: &rpcError{Code: -32000, Message: err.Error()}}:
		default:
		}
	}
}

func (c *rpcConn) writeLine(payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.write(payload)
}

// call writes one request and waits for the matching response. Pairing is by the
// string id echoed in the frame.
func (c *rpcConn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if c.isFinished() {
		return nil, errSessionClosed
	}
	id := strconv.FormatInt(atomic.AddInt64(&c.nextID, 1), 10)
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	ch := make(chan rpcFrame, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.writeLine(payload); err != nil {
		c.removePending(id)
		// Close()/a lost socket and the failing write race: report a finished
		// session, not a protocol failure — see errSessionClosed.
		if c.isFinished() {
			return nil, errSessionClosed
		}
		return nil, fmt.Errorf("hermes gateway: write %s: %w", method, err)
	}

	select {
	case frame := <-ch:
		if frame.Error != nil {
			if err := c.Err(); err != nil {
				return nil, err
			}
			return nil, &rpcCallError{Code: frame.Error.Code, Message: frame.Error.Message, Data: frame.Error.Data, Method: method}
		}
		return frame.Result, nil
	case <-ctx.Done():
		c.removePending(id)
		return nil, ctx.Err()
	case <-c.done:
		c.removePending(id)
		if err := c.Err(); err != nil {
			return nil, err
		}
		return nil, ErrGatewayClosed
	case <-c.closeCh:
		c.removePending(id)
		return nil, errSessionClosed
	}
}

func (c *rpcConn) removePending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func idString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String()
	}
	return strings.Trim(string(raw), `"`)
}
