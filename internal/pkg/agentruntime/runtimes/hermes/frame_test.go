package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedTransport records every line the codec writes and lets a test answer
// synchronously (a WebSocket server reply is just a line handed back in).
type scriptedTransport struct {
	mu       sync.Mutex
	lines    [][]byte
	writeErr error
	onWrite  func([]byte)
}

func (s *scriptedTransport) write(line []byte) error {
	s.mu.Lock()
	s.lines = append(s.lines, append([]byte(nil), line...))
	err := s.writeErr
	onWrite := s.onWrite
	s.mu.Unlock()
	if err != nil {
		return err
	}
	if onWrite != nil {
		onWrite(line)
	}
	return nil
}

func (s *scriptedTransport) sent() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([][]byte, len(s.lines))
	copy(out, s.lines)
	return out
}

func TestRPCConn_CallPairsResponseByStringID(t *testing.T) {
	Convey("Given a codec whose transport echoes a response for the request id", t, func() {
		transport := &scriptedTransport{}
		conn := newRPCConn(transport.write)
		transport.onWrite = func(line []byte) {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := json.Unmarshal(line, &req); err != nil {
				return
			}
			// The gateway stamps the request id verbatim; numeric ids must not be
			// assumed.
			reply := append(append([]byte(`{"jsonrpc":"2.0","id":`), req.ID...), []byte(`,"result":{"ok":true}}`)...)
			_ = conn.handleLine(reply)
		}

		result, err := conn.call(context.Background(), "session.usage", map[string]any{"session_id": "live-1"})

		require.NoError(t, err)
		assert.JSONEq(t, `{"ok":true}`, string(result))
		sent := transport.sent()
		require.Len(t, sent, 1)
		var request struct {
			ID      json.RawMessage `json:"id"`
			JSONRPC string          `json:"jsonrpc"`
			Method  string          `json:"method"`
		}
		require.NoError(t, json.Unmarshal(sent[0], &request))
		assert.Equal(t, "2.0", request.JSONRPC)
		assert.Equal(t, "session.usage", request.Method)
		assert.True(t, json.Valid(request.ID))
		var id string
		require.NoError(t, json.Unmarshal(request.ID, &id), "request ids must be strings")
		assert.NotEmpty(t, id)
	})
}

func TestRPCConn_EventsAndGatewayReady(t *testing.T) {
	Convey("Given a codec", t, func() {
		conn := newRPCConn((&scriptedTransport{}).write)

		Convey("When gateway.ready arrives Then WaitReady unblocks", func() {
			require.NoError(t, conn.handleLine([]byte(`{"jsonrpc":"2.0","method":"event","params":{"type":"gateway.ready","payload":{}}}`)))
			require.NoError(t, conn.WaitReady(context.Background()))
		})

		Convey("When a typed event arrives Then it is streamed", func() {
			require.NoError(t, conn.handleLine([]byte(`{"jsonrpc":"2.0","method":"event","params":{"type":"message.delta","session_id":"live-1","payload":{"text":"hi"}}}`)))
			select {
			case ev := <-conn.Events():
				So(ev.Kind, ShouldEqual, EventMessageDelta)
				So(ev.Session, ShouldEqual, "live-1")
			case <-time.After(time.Second):
				t.Fatal("expected an event")
			}
		})

		Convey("When a junk line arrives Then it is dropped without wedging", func() {
			require.NoError(t, conn.handleLine([]byte(`{not json`)))
		})
	})
}

// A server->client request the host has no seam for yet — and is not one of
// the known-unsupported methods answered immediately (see below) — must be
// answered with the SAME id and -32601 right away, so Hermes never waits on it,
// and the turn still learns of it (method only, never params) so it can leave
// the transcript notice spec 2026-09-17 "Unsupported Hermes requests" requires
// for methods Agentre does not recognize.
func TestRPCConn_UnroutedReverseRequestAnsweredWithSameIDAndMethodNotFound(t *testing.T) {
	Convey("Given a gateway that sends a reverse request without a host seam", t, func() {
		transport := &scriptedTransport{}
		conn := newRPCConn(transport.write)

		err := conn.handleLine([]byte(`{"jsonrpc":"2.0","id":"srq-000000000009","method":"window.write","params":{"session_id":"live-1","command":"apt install"}}`))

		require.NoError(t, err)
		sent := transport.sent()
		require.Len(t, sent, 1)
		var response struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Error   *rpcError       `json:"error"`
		}
		require.NoError(t, json.Unmarshal(sent[0], &response))
		assert.Equal(t, "2.0", response.JSONRPC)
		assert.Equal(t, `"srq-000000000009"`, string(response.ID))
		require.NotNil(t, response.Error)
		assert.Equal(t, -32601, response.Error.Code)
		select {
		case ev := <-conn.Events():
			So(ev.Kind, ShouldEqual, EventUnsupportedRequest)
			So(ev.Method, ShouldEqual, "window.write")
			So(ev.Request, ShouldBeNil)
			So(ev.Payload, ShouldBeNil)
		case <-time.After(time.Second):
			t.Fatal("expected an unsupported-request notice event for the unknown method")
		}
	})
}

// Design decision 4: sudo/secret/vault.*/terminal.read/preview.*/window.read/
// tour are answered immediately with the contract's {"value": ""} (not
// -32601), and the turn separately learns which method so it can leave a
// transcript notice — but the raw params (which may carry a password, a
// secret value or an unlock code) must never reach that event or a log line.
func TestRPCConn_UnsupportedReverseRequestAnsweredImmediatelyWithEmptyValue(t *testing.T) {
	Convey("Given a gateway that sends a known-unsupported reverse request", t, func() {
		for _, method := range []string{
			methodSudo, methodSecret, methodVaultUnlockPrompt, methodVaultSaveLogin, methodVaultCode,
			methodTerminalRead, methodPreviewRead, methodWindowRead, methodPreviewAct, methodTour,
		} {
			Convey("method="+method, func() {
				transport := &scriptedTransport{}
				conn := newRPCConn(transport.write)

				line := []byte(`{"jsonrpc":"2.0","id":"srq-000000000009","method":"` + method +
					`","params":{"session_id":"live-1","password":"hunter2","secret":"topsecret-value"}}`)
				err := conn.handleLine(line)

				require.NoError(t, err)
				sent := transport.sent()
				require.Len(t, sent, 1)
				assert.JSONEq(t, `{"jsonrpc":"2.0","id":"srq-000000000009","result":{"value":""}}`, string(sent[0]))
				assert.NotContains(t, string(sent[0]), "hunter2")

				select {
				case ev := <-conn.Events():
					So(ev.Kind, ShouldEqual, EventUnsupportedRequest)
					So(ev.Method, ShouldEqual, method)
					So(ev.Session, ShouldEqual, "")
					So(ev.Request, ShouldBeNil)
					So(ev.Payload, ShouldBeNil)
				case <-time.After(time.Second):
					t.Fatal("expected an unsupported-request notice event")
				}
			})
		}
	})
}

// An unrecognized reverse request must still be refused -32601, not treated as
// unsupported-but-answerable — the closed vocabulary in design decision 4 is
// exhaustive, and anything outside it keeps today's behavior.
func TestRPCConn_UnknownReverseRequestStillMethodNotFound(t *testing.T) {
	Convey("Given a gateway that sends a totally unknown reverse request", t, func() {
		transport := &scriptedTransport{}
		conn := newRPCConn(transport.write)

		err := conn.handleLine([]byte(`{"jsonrpc":"2.0","id":"srq-000000000010","method":"totally.unknown","params":{}}`))

		require.NoError(t, err)
		sent := transport.sent()
		require.Len(t, sent, 1)
		var response struct {
			Error *rpcError `json:"error"`
		}
		require.NoError(t, json.Unmarshal(sent[0], &response))
		require.NotNil(t, response.Error)
		assert.Equal(t, -32601, response.Error.Code)
	})
}

// The approval server->client request is handed to the turn and answered later
// by id: the codec must not answer it itself.
func TestRPCConn_ApprovalReverseRequestIsHandedToTheTurnAndAnsweredByID(t *testing.T) {
	Convey("Given a gateway that sends an approval server request", t, func() {
		transport := &scriptedTransport{}
		conn := newRPCConn(transport.write)

		err := conn.handleLine([]byte(`{"jsonrpc":"2.0","id":"srq-0123456789ab","method":"approval","params":{"session_id":"live-1","request_id":"appr-1","command":"rm -rf x","choices":["once","deny"]}}`))

		require.NoError(t, err)
		assert.Empty(t, transport.sent(), "the codec must leave the approval unanswered for the user")
		var ev Event
		select {
		case ev = <-conn.Events():
		case <-time.After(time.Second):
			t.Fatal("the approval request never reached the turn")
		}
		So(ev.Kind, ShouldEqual, EventServerRequest)
		So(ev.Session, ShouldEqual, "live-1")
		require.NotNil(t, ev.Request)
		So(ev.Request.ID, ShouldEqual, "srq-0123456789ab")
		So(ev.Request.Method, ShouldEqual, "approval")
		assert.JSONEq(t, `{"session_id":"live-1","request_id":"appr-1","command":"rm -rf x","choices":["once","deny"]}`, string(ev.Request.Params))

		Convey("When the turn responds Then the result is sent under the same id exactly once", func() {
			require.NoError(t, conn.RespondServerRequest("srq-0123456789ab", map[string]any{"choice": "once"}))
			sent := transport.sent()
			require.Len(t, sent, 1)
			assert.JSONEq(t, `{"jsonrpc":"2.0","id":"srq-0123456789ab","result":{"choice":"once"}}`, string(sent[0]))

			err := conn.RespondServerRequest("srq-0123456789ab", map[string]any{"choice": "deny"})
			require.ErrorIs(t, err, errServerRequestNotOpen)
			assert.Len(t, transport.sent(), 1, "a second answer must not reach Hermes")
		})

		Convey("When the turn rejects Then an error response is sent under the same id", func() {
			require.NoError(t, conn.RejectServerRequest("srq-0123456789ab"))
			sent := transport.sent()
			require.Len(t, sent, 1)
			var response struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
				Error  *rpcError       `json:"error"`
			}
			require.NoError(t, json.Unmarshal(sent[0], &response))
			assert.Equal(t, `"srq-0123456789ab"`, string(response.ID))
			assert.Empty(t, response.Result)
			require.NotNil(t, response.Error)
		})

		Convey("When the connection is closed Then answering reports the closed session", func() {
			conn.signalClose()
			err := conn.RespondServerRequest("srq-0123456789ab", map[string]any{"choice": "once"})
			require.ErrorIs(t, err, errSessionClosed)
			assert.Empty(t, transport.sent())
		})
	})
}

// The clarify server->client request is routed the same way approval is: handed
// to the turn and answered later by id, not auto-declined on the read loop.
func TestRPCConn_ClarifyReverseRequestIsHandedToTheTurn(t *testing.T) {
	Convey("Given a gateway that sends a clarify server request", t, func() {
		transport := &scriptedTransport{}
		conn := newRPCConn(transport.write)

		err := conn.handleLine([]byte(`{"jsonrpc":"2.0","id":"srq-clarify0001","method":"clarify","params":{"session_id":"live-1","question":"Continue?","choices":["yes","no"],"multi_select":false}}`))

		require.NoError(t, err)
		assert.Empty(t, transport.sent(), "the codec must leave clarify unanswered for the user")
		var ev Event
		select {
		case ev = <-conn.Events():
		case <-time.After(time.Second):
			t.Fatal("the clarify request never reached the turn")
		}
		So(ev.Kind, ShouldEqual, EventServerRequest)
		require.NotNil(t, ev.Request)
		So(ev.Request.ID, ShouldEqual, "srq-clarify0001")
		So(ev.Request.Method, ShouldEqual, "clarify")

		require.NoError(t, conn.RespondServerRequest("srq-clarify0001", map[string]any{"answer": "yes"}))
		sent := transport.sent()
		require.Len(t, sent, 1)
		assert.JSONEq(t, `{"jsonrpc":"2.0","id":"srq-clarify0001","result":{"answer":"yes"}}`, string(sent[0]))
	})
}

func TestRPCConn_AnswerForUnknownServerRequestIsRefused(t *testing.T) {
	Convey("Given no open server request", t, func() {
		transport := &scriptedTransport{}
		conn := newRPCConn(transport.write)

		err := conn.RespondServerRequest("srq-never", map[string]any{"choice": "once"})

		require.ErrorIs(t, err, errServerRequestNotOpen)
		assert.Empty(t, transport.sent())
	})
}

func TestRPCConn_UnregisteredResponseIDIsIgnored(t *testing.T) {
	Convey("Given a response for an unknown id", t, func() {
		conn := newRPCConn((&scriptedTransport{}).write)
		require.NoError(t, conn.handleLine([]byte(`{"jsonrpc":"2.0","id":"nope","result":{}}`)))
	})
}

func TestRPCConn_CallOnClosedReturnsSessionClosed(t *testing.T) {
	Convey("Given a deliberately closed codec", t, func() {
		conn := newRPCConn(func([]byte) error { return errors.New("write |1: file already closed") })
		conn.signalClose()

		_, err := conn.call(context.Background(), "session.interrupt", map[string]any{})

		require.ErrorIs(t, err, errSessionClosed)
		assert.NotContains(t, err.Error(), "file already closed")
	})
}

// A failing write racing the close must be reported as a finished session: the
// close signal runs first, so isFinished is already true.
func TestRPCConn_WriteFailureAfterCloseIsSessionClosed(t *testing.T) {
	Convey("Given the close signal fires between the check and the write", t, func() {
		var conn *rpcConn
		conn = newRPCConn(func([]byte) error {
			conn.signalClose()
			return errors.New("broken pipe")
		})

		_, err := conn.call(context.Background(), "session.interrupt", map[string]any{})

		require.ErrorIs(t, err, errSessionClosed)
	})
}

func TestRPCConn_FailConnectionUnblocksPendingAndClosesEvents(t *testing.T) {
	Convey("Given an in-flight call and a dead connection", t, func() {
		written := make(chan struct{}, 1)
		transport := &scriptedTransport{onWrite: func([]byte) { written <- struct{}{} }}
		conn := newRPCConn(transport.write)
		done := make(chan error, 1)
		go func() {
			_, err := conn.call(context.Background(), "session.usage", map[string]any{})
			done <- err
		}()
		select {
		case <-written:
		case <-time.After(time.Second):
			t.Fatal("request was never written")
		}

		conn.failConnection(ErrGatewayLost)

		select {
		case err := <-done:
			require.ErrorIs(t, err, ErrGatewayLost)
		case <-time.After(time.Second):
			t.Fatal("pending call did not unblock")
		}
		require.ErrorIs(t, conn.Err(), ErrGatewayLost)
		_, open := <-conn.Events()
		assert.False(t, open, "events must be closed after the transport dies")
	})
}
