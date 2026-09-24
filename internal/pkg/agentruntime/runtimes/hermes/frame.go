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

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// errSessionClosed marks an RPC attempted on a gateway connection that has
// already been torn down (explicit Close or a lost socket). It is not a protocol
// failure: the turn it belonged to is over. Control paths translate it into
// "no in-flight turn" instead of surfacing a raw write error, which is what a
// Stop click landing on the tail of a turn would otherwise see.
var errSessionClosed = errors.New("hermes gateway: closed")

// errServerRequestNotOpen marks an answer for a server->client request that is
// not waiting on this connection: never received, or already answered. Hermes
// drops such a response anyway; refusing it locally keeps answers exactly-once.
var errServerRequestNotOpen = errors.New("hermes gateway: server request is not open")

// routedServerRequests are the server->client request methods the turn answers
// itself (asynchronously, by id). Every other method is answered -32601 on the
// read loop so Hermes withdraws it instead of waiting (and still leaves a
// transcript notice), unless it is one of unsupportedServerRequests below.
var routedServerRequests = map[string]bool{
	"approval": true,
	"clarify":  true,
}

// Reverse-request methods Hermes may send that carry no card in Agentre yet
// (spec 2026-09-17 "Unsupported Hermes requests", design decision 4). Each one
// is answered immediately on the read loop with the contract's {"value": ""}
// instead of -32601, and separately handed to the turn (as EventUnsupportedRequest,
// method only — never params) so it can leave a transcript notice; the table
// below is the one list of these methods and maps each onto the readable
// purpose category that notice names.
const (
	methodSudo              = "sudo"
	methodSecret            = "secret"
	methodVaultUnlockPrompt = "vault.unlock_prompt"
	methodVaultSaveLogin    = "vault.save_login"
	methodVaultCode         = "vault.code"
	methodTerminalRead      = "terminal.read"
	methodPreviewRead       = "preview.read"
	methodWindowRead        = "window.read"
	methodPreviewAct        = "preview.act"
	methodTour              = "tour"
)

var unsupportedServerRequests = map[string]agentruntime.UnsupportedRequestPurpose{
	methodSudo:              agentruntime.UnsupportedRequestSudoPassword,
	methodSecret:            agentruntime.UnsupportedRequestSecret,
	methodVaultUnlockPrompt: agentruntime.UnsupportedRequestVaultUnlock,
	methodVaultSaveLogin:    agentruntime.UnsupportedRequestVaultSaveLogin,
	methodVaultCode:         agentruntime.UnsupportedRequestVaultCode,
	methodTerminalRead:      agentruntime.UnsupportedRequestTerminalRead,
	methodPreviewRead:       agentruntime.UnsupportedRequestPreviewRead,
	methodWindowRead:        agentruntime.UnsupportedRequestWindowRead,
	methodPreviewAct:        agentruntime.UnsupportedRequestPreviewAct,
	methodTour:              agentruntime.UnsupportedRequestTour,
}

func isUnsupportedServerRequest(method string) bool {
	_, ok := unsupportedServerRequests[method]
	return ok
}

// unsupportedRequestResult is the contract's immediate answer for every method
// in unsupportedServerRequests.
var unsupportedRequestResult = map[string]string{"value": ""}

// ServerRequest is one server->client JSON-RPC request handed to the turn as an
// EventServerRequest. The turn answers it later with RespondServerRequest or
// RejectServerRequest under the same ID.
type ServerRequest struct {
	ID     string
	Method string
	Params json.RawMessage
}

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
	// serverRequests maps an open server->client request id to the raw id the
	// response must echo verbatim.
	serverRequests map[string]json.RawMessage

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

		serverRequests: map[string]json.RawMessage{},
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
		return c.routeServerRequest(frame)
	}
	c.deliver(frame)
	return nil
}

// routeServerRequest hands a routed server->client request to the turn through
// the ordered event stream (so a later request.cancel can never overtake it),
// answers a known-unsupported method immediately (see unsupportedServerRequests),
// and answers every other method -32601 at once; both of the latter leave a
// transcript notice.
func (c *rpcConn) routeServerRequest(frame rpcFrame) error {
	id := idString(frame.ID)
	switch {
	case routedServerRequests[frame.Method] && id != "":
		return c.routeTurnRequest(frame, id)
	case isUnsupportedServerRequest(frame.Method) && id != "":
		return c.answerUnsupportedRequest(frame)
	default:
		_ = c.writeResponse(frame.ID, nil, &rpcError{Code: -32601, Message: "method not found"})
		if id == "" {
			return nil
		}
		// An unrecognized request still leaves a transcript notice (spec
		// 2026-09-17 "Unsupported Hermes requests"); like the known-unsupported
		// path, only the method travels, never the params.
		return c.queueUnsupportedNotice(frame.Method)
	}
}

// routeTurnRequest hands an approval/clarify request to the turn, to be
// answered later by id via RespondServerRequest/RejectServerRequest.
func (c *rpcConn) routeTurnRequest(frame rpcFrame, id string) error {
	var params struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(frame.Params, &params)
	c.mu.Lock()
	c.serverRequests[id] = append(json.RawMessage(nil), frame.ID...)
	c.mu.Unlock()
	ev := Event{
		Kind:    EventServerRequest,
		Session: params.SessionID,
		Request: &ServerRequest{ID: id, Method: frame.Method, Params: append(json.RawMessage(nil), frame.Params...)},
	}
	select {
	case c.events <- ev:
		return nil
	case <-c.closeCh:
		return errSessionClosed
	}
}

// answerUnsupportedRequest answers a known reverse-request method Agentre has
// no card for with the contract's {"value": ""} (design decision 4), then
// queues a turn-ordered EventUnsupportedRequest carrying only the method name
// so the turn can leave a transcript notice. frame.Params is deliberately
// never read here: sudo/secret/vault.* carry a password, secret value or
// unlock code that must never reach an event, the transcript or a log line
// (spec 2026-09-17 "Unsupported Hermes requests").
func (c *rpcConn) answerUnsupportedRequest(frame rpcFrame) error {
	_ = c.writeResponse(frame.ID, unsupportedRequestResult, nil)
	return c.queueUnsupportedNotice(frame.Method)
}

// queueUnsupportedNotice hands the turn an EventUnsupportedRequest carrying
// only the method name of a reverse request that was already answered.
func (c *rpcConn) queueUnsupportedNotice(method string) error {
	select {
	case c.events <- Event{Kind: EventUnsupportedRequest, Method: method}:
		return nil
	case <-c.closeCh:
		return errSessionClosed
	}
}

// RespondServerRequest answers an open server->client request with result under
// its original id. Each request is answered at most once.
func (c *rpcConn) RespondServerRequest(id string, result any) error {
	return c.answerServerRequest(id, result, nil)
}

// RejectServerRequest answers an open server->client request with a JSON-RPC
// error: the host cannot answer it, so Hermes withdraws the request.
func (c *rpcConn) RejectServerRequest(id string) error {
	return c.answerServerRequest(id, nil, &rpcError{Code: -32000, Message: "request cannot be answered"})
}

func (c *rpcConn) answerServerRequest(id string, result any, rpcErr *rpcError) error {
	if c.isFinished() {
		return errSessionClosed
	}
	c.mu.Lock()
	rawID, open := c.serverRequests[id]
	delete(c.serverRequests, id)
	c.mu.Unlock()
	if !open {
		return errServerRequestNotOpen
	}
	if err := c.writeResponse(rawID, result, rpcErr); err != nil {
		if c.isFinished() {
			return errSessionClosed
		}
		return fmt.Errorf("hermes gateway: answer server request: %w", err)
	}
	return nil
}

// writeResponse writes one client->server response frame echoing rawID.
func (c *rpcConn) writeResponse(rawID json.RawMessage, result any, rpcErr *rpcError) error {
	response := map[string]any{"jsonrpc": "2.0", "id": rawID}
	if rpcErr != nil {
		response["error"] = rpcErr
	} else {
		response["result"] = result
	}
	payload, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return c.writeLine(payload)
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
