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

// A server->client JSON-RPC request (approval / clarify in the gateway's
// vocabulary) must be answered with the SAME id. Nothing here implements those
// cards (capability is abort-only), so the honest answer is -32601, not silence.
func TestRPCConn_ReverseRequestAnsweredWithSameIDAndMethodNotFound(t *testing.T) {
	Convey("Given a gateway that sends a reverse approval request", t, func() {
		transport := &scriptedTransport{}
		conn := newRPCConn(transport.write)

		err := conn.handleLine([]byte(`{"jsonrpc":"2.0","id":"req-9","method":"approval.request","params":{"command":"rm -rf"}}`))

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
		assert.Equal(t, `"req-9"`, string(response.ID))
		require.NotNil(t, response.Error)
		assert.Equal(t, -32601, response.Error.Code)
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
