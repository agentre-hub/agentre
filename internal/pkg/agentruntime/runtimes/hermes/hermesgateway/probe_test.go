package hermesgateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/hermes/hermesauth"
)

// TestHermesgatewayRegistersNoRuntime pins the leaf property agentred relies on
// to probe a serve without advertising the Hermes runtime.
func TestHermesgatewayRegistersNoRuntime(t *testing.T) {
	assert.Nil(t, agentruntime.RuntimeFor(agent_backend_entity.TypeHermes))
}

type scriptedCreds struct {
	token string
	err   error
}

func (c scriptedCreds) AccessToken(context.Context, string, string) (string, error) {
	return c.token, c.err
}

func (scriptedCreds) Invalidate(string) {}

func upgradeThen(t *testing.T, w http.ResponseWriter, r *http.Request, script func(*websocket.Conn)) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer func() { _ = conn.Close() }()
	script(conn)
}

func sendReadyAndHold(conn *websocket.Conn) {
	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","method":"event","params":{"type":"gateway.ready","payload":{}}}`))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func loopbackServe(t *testing.T, script func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `window.__HERMES_SESSION_TOKEN__="tok";`)
	})
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) { upgradeThen(t, w, r, script) })
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func gatedServe(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", http.NotFound)
	mux.HandleFunc("/api/auth/ws-ticket", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer good-access" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ticket":"t-1","ttl_seconds":30}`))
	})
	mux.HandleFunc("/api/ws", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ticket") != "t-1" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		upgradeThen(t, w, r, sendReadyAndHold)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestProbe_GivenALoopbackServeThatSendsReady_ThenNamesTheEndpoint(t *testing.T) {
	server := loopbackServe(t, sendReadyAndHold)

	reply, err := Probe(context.Background(), ProbeRequest{URL: server.URL})

	require.NoError(t, err)
	assert.Equal(t, server.URL, reply)
}

func TestProbe_GivenAGatedServeAndAStoredLogin_ThenConnectsWithATicket(t *testing.T) {
	server := gatedServe(t)

	_, err := Probe(context.Background(), ProbeRequest{URL: server.URL, AuthProvider: "basic", Credentials: scriptedCreds{token: "good-access"}})

	require.NoError(t, err)
}

func TestProbe_GivenAGatedServeWithoutLogin_ThenLoginRequired(t *testing.T) {
	server := gatedServe(t)

	_, err := Probe(context.Background(), ProbeRequest{URL: server.URL, Credentials: scriptedCreds{err: hermesauth.ErrLoginRequired}})

	require.ErrorIs(t, err, hermesauth.ErrLoginRequired)
}

func TestProbe_GivenTheServerClosesBeforeReady_ThenClosedError(t *testing.T) {
	server := loopbackServe(t, func(conn *websocket.Conn) {
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"))
	})

	_, err := Probe(context.Background(), ProbeRequest{URL: server.URL})

	require.ErrorIs(t, err, ErrGatewayClosed)
}

func TestProbe_GivenNothingListens_ThenUnreachable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()

	_, err := Probe(context.Background(), ProbeRequest{URL: url})

	require.ErrorIs(t, err, ErrGatewayUnreachable)
}

func TestProbe_GivenNoURL_ThenError(t *testing.T) {
	_, err := Probe(context.Background(), ProbeRequest{})
	require.Error(t, err)
}
