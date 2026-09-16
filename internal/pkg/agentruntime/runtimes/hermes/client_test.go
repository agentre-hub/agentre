package hermes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newHermesTestServer serves the loopback token page at `/` and upgrades
// `/api/ws` to a scripted handler.
func newHermesTestServer(t *testing.T, token string, wsHandler func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, "<!doctype html><html><head><script>window.__HERMES_SESSION_TOKEN__=%q;window.__HERMES_AUTH_REQUIRED__=false;</script></head><body>ok</body></html>", token)
	})
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		wsHandler(conn)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func writeReady(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","method":"event","params":{"type":"gateway.ready","payload":{}}}`)))
}

// holdConn keeps a test WebSocket handler alive until the client disconnects.
func holdConn(conn *websocket.Conn) {
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

func TestFetchSessionToken_GivenLoopbackPage_ThenToken(t *testing.T) {
	server := newHermesTestServer(t, "tok-123", func(*websocket.Conn) {})

	token, err := fetchSessionToken(context.Background(), server.URL, nil)

	require.NoError(t, err)
	assert.Equal(t, "tok-123", token)
}

func TestFetchSessionToken_FailuresAreClassified(t *testing.T) {
	ConveyFailures := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, body: "no", want: ErrGatewayUnauthorized},
		{name: "forbidden", status: http.StatusForbidden, body: "no", want: ErrGatewayUnauthorized},
		{name: "gated 404", status: http.StatusNotFound, body: `{"error":"web UI disabled"}`, want: ErrGatewayUnauthorized},
		{name: "auth marker without token", status: http.StatusOK, body: `window.__HERMES_AUTH_REQUIRED__=true;`, want: ErrGatewayUnauthorized},
		// A live gated serve redirects "/" to /login and answers there with the
		// sign-in form: HTML, no token, no marker. Verified against a real serve.
		{name: "gated login page", status: http.StatusOK, body: `<html><head><title>Sign in — Hermes Agent</title></head></html>`, want: ErrGatewayUnauthorized},
		{name: "token missing", status: http.StatusOK, body: `not a hermes serve`, want: ErrGatewayProtocol},
	}
	for _, tc := range ConveyFailures {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)

			_, err := fetchSessionToken(context.Background(), server.URL, nil)

			require.Error(t, err)
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

func TestFetchSessionToken_GivenUnreachable_ThenUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	_, err := fetchSessionToken(context.Background(), url, nil)

	require.ErrorIs(t, err, ErrGatewayUnreachable)
}

func TestDialGateway_SendsSessionTokenQuery(t *testing.T) {
	received := make(chan string, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `window.__HERMES_SESSION_TOKEN__="tok-42";`)
	})
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) {
		received <- r.URL.Query().Get("token")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		writeReady(t, conn)
		holdConn(conn)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	sess, err := dialGateway(context.Background(), server.URL, nil, nil)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()
	require.NoError(t, sess.WaitReady(context.Background()))

	select {
	case token := <-received:
		assert.Equal(t, "tok-42", token)
	case <-time.After(time.Second):
		t.Fatal("server never saw the handshake")
	}
}

// Hermes batches frames: one WebSocket text message can carry several
// newline-delimited JSON frames. The client must split and dispatch each.
func TestDialGateway_MultiLineMessageIsSplitIntoFrames(t *testing.T) {
	server := newHermesTestServer(t, "tok", func(conn *websocket.Conn) {
		payload := `{"jsonrpc":"2.0","method":"event","params":{"type":"gateway.ready","payload":{}}}` + "\n" +
			`{"jsonrpc":"2.0","method":"event","params":{"type":"message.delta","session_id":"live-1","payload":{"text":"hi"}}}` + "\n"
		_ = conn.WriteMessage(websocket.TextMessage, []byte(payload))
		holdConn(conn)
	})

	sess, err := dialGateway(context.Background(), server.URL, nil, nil)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()
	require.NoError(t, sess.WaitReady(context.Background()))

	select {
	case ev := <-sess.Events():
		assert.Equal(t, EventGatewayReady, ev.Kind)
	case <-time.After(2 * time.Second):
		t.Fatal("gateway.ready was not streamed")
	}
	select {
	case ev := <-sess.Events():
		assert.Equal(t, EventMessageDelta, ev.Kind)
		assert.Equal(t, "live-1", ev.Session)
	case <-time.After(2 * time.Second):
		t.Fatal("the second frame in a multi-line message was dropped")
	}
}

func TestDialGateway_HandshakeRejectedClassifiesUnauthorized(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `window.__HERMES_SESSION_TOKEN__="tok";`)
	})
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, _ *http.Request) {
		// Mirrors the gated serve closing the upgrade before accept.
		w.WriteHeader(http.StatusForbidden)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	_, err := dialGateway(context.Background(), server.URL, nil, nil)

	require.ErrorIs(t, err, ErrGatewayUnauthorized)
}

func TestGatewaySession_GivenCleanServerClose_ThenGatewayClosed(t *testing.T) {
	server := newHermesTestServer(t, "tok", func(conn *websocket.Conn) {
		writeReady(t, conn)
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"))
	})

	sess, err := dialGateway(context.Background(), server.URL, nil, nil)
	require.NoError(t, err)
	require.NoError(t, sess.WaitReady(context.Background()))

	waitFor(t, 2*time.Second, func() bool { return sess.Err() != nil })
	assert.ErrorIs(t, sess.Err(), ErrGatewayClosed)
}

// A server restart / dropped socket is not a clean "turn ended": it classifies
// as lost so the failure can be reported honestly.
func TestGatewaySession_GivenAbruptClose_ThenGatewayLost(t *testing.T) {
	server := newHermesTestServer(t, "tok", func(conn *websocket.Conn) {
		writeReady(t, conn)
		_ = conn.UnderlyingConn().Close()
	})

	sess, err := dialGateway(context.Background(), server.URL, nil, nil)
	require.NoError(t, err)
	require.NoError(t, sess.WaitReady(context.Background()))

	waitFor(t, 2*time.Second, func() bool { return sess.Err() != nil })
	assert.ErrorIs(t, sess.Err(), ErrGatewayLost)
}

// After Close, an RPC must report "closed" (which Abort maps to no active turn)
// rather than a raw socket write error.
func TestGatewaySession_InterruptAfterCloseIsSessionClosed(t *testing.T) {
	server := newHermesTestServer(t, "tok", func(conn *websocket.Conn) {
		writeReady(t, conn)
		holdConn(conn)
	})

	sess, err := dialGateway(context.Background(), server.URL, nil, nil)
	require.NoError(t, err)
	require.NoError(t, sess.WaitReady(context.Background()))
	require.NoError(t, sess.Close(context.Background()))

	err = sess.Interrupt(context.Background(), "live-1")

	require.ErrorIs(t, err, errSessionClosed)
	assert.NotContains(t, strings.ToLower(err.Error()), "broken pipe")
}

func TestDialGateway_GivenDialFailure_ThenUnreachable(t *testing.T) {
	server := newHermesTestServer(t, "tok", func(*websocket.Conn) {})
	scripted := testDialer(func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
		return nil, nil, errors.New("connection refused")
	})

	_, err := dialGateway(context.Background(), server.URL, scripted, nil)

	require.ErrorIs(t, err, ErrGatewayUnreachable)
}

func TestDialGateway_GivenDialUnauthorized_ThenUnauthorized(t *testing.T) {
	server := newHermesTestServer(t, "tok", func(*websocket.Conn) {})
	scripted := testDialer(func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error) {
		return nil, &http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized"}, errors.New("bad handshake")
	})

	_, err := dialGateway(context.Background(), server.URL, scripted, nil)

	require.ErrorIs(t, err, ErrGatewayUnauthorized)
}

type testDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

func (f testDialer) DialContext(ctx context.Context, urlStr string, requestHeader http.Header) (*websocket.Conn, *http.Response, error) {
	return f(ctx, urlStr, requestHeader)
}

func TestProbe_GivenReadyGateway_ThenNamesEndpoint(t *testing.T) {
	server := newHermesTestServer(t, "tok", func(conn *websocket.Conn) {
		writeReady(t, conn)
		holdConn(conn)
	})

	reply, err := Probe(context.Background(), ProbeRequest{URL: server.URL})

	require.NoError(t, err)
	assert.Equal(t, server.URL, reply)
}

func TestProbe_GivenNoURL_ThenError(t *testing.T) {
	_, err := Probe(context.Background(), ProbeRequest{})
	require.Error(t, err)
}
