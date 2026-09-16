package openclawgateway

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testWebSocketDialer func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)

func (f testWebSocketDialer) DialContext(
	ctx context.Context,
	urlStr string,
	requestHeader http.Header,
) (*websocket.Conn, *http.Response, error) {
	return f(ctx, urlStr, requestHeader)
}

type closeTrackingBody struct {
	closed atomic.Bool
}

func (*closeTrackingBody) Read([]byte) (int, error) { return 0, io.EOF }

func (b *closeTrackingBody) Close() error {
	b.closed.Store(true)
	return nil
}

type testRequestFrame struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type testConnectParams struct {
	MinProtocol int `json:"minProtocol"`
	MaxProtocol int `json:"maxProtocol"`
	Client      struct {
		ID       string `json:"id"`
		Version  string `json:"version"`
		Platform string `json:"platform"`
		Mode     string `json:"mode"`
	} `json:"client"`
	Role   string   `json:"role"`
	Scopes []string `json:"scopes"`
	Device struct {
		ID        string `json:"id"`
		PublicKey string `json:"publicKey"`
		Signature string `json:"signature"`
		SignedAt  int64  `json:"signedAt"`
		Nonce     string `json:"nonce"`
	} `json:"device"`
	Auth struct {
		Token string `json:"token"`
	} `json:"auth"`
}

func newTestGateway(t *testing.T, handler func(*websocket.Conn, int)) string {
	t.Helper()
	var connectionCount atomic.Int32
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		handler(conn, int(connectionCount.Add(1)))
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func writeTestJSON(t *testing.T, conn *websocket.Conn, value any) {
	t.Helper()
	require.NoError(t, conn.WriteJSON(value))
}

func readTestRequest(t *testing.T, conn *websocket.Conn) testRequestFrame {
	t.Helper()
	var frame testRequestFrame
	require.NoError(t, conn.ReadJSON(&frame))
	return frame
}

func writeChallenge(t *testing.T, conn *websocket.Conn, nonce string) {
	t.Helper()
	writeTestJSON(t, conn, map[string]any{
		"type": "event", "event": "connect.challenge", "seq": 1,
		"payload": map[string]any{"nonce": nonce, "ts": time.Now().UnixMilli()},
	})
}

func helloPayload(protocol int, scopes []string, connID string) map[string]any {
	return map[string]any{
		"type":     "hello-ok",
		"protocol": protocol,
		"server":   map[string]any{"version": "2026.7.1-2", "connId": connID},
		"features": map[string]any{
			"methods": []string{"agent", "agent.wait", "chat.abort", "agents.list", "models.list", "exec.approval.list", "exec.approval.resolve"},
			"events":  []string{"agent", "chat", "exec.approval.requested", "exec.approval.resolved"},
		},
		"snapshot": map[string]any{
			"presence": []any{}, "health": map[string]any{},
			"stateVersion": map[string]any{"presence": 1, "health": 1}, "uptimeMs": 100,
		},
		"auth":   map[string]any{"role": "operator", "scopes": scopes},
		"policy": map[string]any{"maxPayload": 1048576, "maxBufferedBytes": 2097152, "tickIntervalMs": 30000},
	}
}

func writeHello(t *testing.T, conn *websocket.Conn, requestID string, protocol int, scopes []string, connID string) {
	t.Helper()
	writeTestJSON(t, conn, map[string]any{
		"type": "res", "id": requestID, "ok": true,
		"payload": helloPayload(protocol, scopes, connID),
	})
}

func testIdentity(t *testing.T) *DeviceIdentity {
	t.Helper()
	identity, err := NewDeviceIdentityFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	require.NoError(t, err)
	return identity
}

func TestClientChallengeConnectAndOutOfOrderResponses(t *testing.T) {
	authValue := strings.Repeat("a", 37)
	gatewayURL := newTestGateway(t, func(conn *websocket.Conn, _ int) {
		const nonce = "challenge-nonce"
		writeChallenge(t, conn, nonce)
		connect := readTestRequest(t, conn)
		require.Equal(t, "connect", connect.Method, "connect must be the first client frame")

		var params testConnectParams
		require.NoError(t, json.Unmarshal(connect.Params, &params))
		require.Equal(t, ProtocolVersion, params.MinProtocol)
		require.Equal(t, ProtocolVersion, params.MaxProtocol)
		require.Equal(t, "cli", params.Client.ID)
		require.Equal(t, "cli", params.Client.Mode)
		require.Equal(t, "operator", params.Role)
		require.Equal(t, RequiredOperatorScopes, params.Scopes)
		require.Equal(t, authValue, params.Auth.Token)
		require.Equal(t, nonce, params.Device.Nonce)

		publicKey, err := base64.RawURLEncoding.DecodeString(params.Device.PublicKey)
		require.NoError(t, err)
		digest := sha256.Sum256(publicKey)
		require.Equal(t, hex.EncodeToString(digest[:]), params.Device.ID)
		signature, err := base64.RawURLEncoding.DecodeString(params.Device.Signature)
		require.NoError(t, err)
		payload := BuildDeviceAuthPayload(
			params.Device.ID, params.Client.ID, params.Client.Mode, params.Role,
			params.Scopes, params.Device.SignedAt, authValue, nonce, params.Client.Platform, "",
		)
		require.True(t, ed25519.Verify(ed25519.PublicKey(publicKey), []byte(payload), signature))

		writeHello(t, conn, connect.ID, ProtocolVersion, RequiredOperatorScopes, "conn-1")

		requests := []testRequestFrame{readTestRequest(t, conn), readTestRequest(t, conn)}
		for i := len(requests) - 1; i >= 0; i-- {
			request := requests[i]
			writeTestJSON(t, conn, map[string]any{
				"type": "res", "id": request.ID, "ok": true,
				"payload": map[string]any{"method": request.Method},
			})
		}
	})

	client, err := NewClient(Config{
		URL: gatewayURL, Token: authValue, Identity: testIdentity(t),
		ClientVersion: "test-version", Platform: "linux",
	})
	require.NoError(t, err)
	defer client.Close()

	hello, err := client.Start(context.Background())
	require.NoError(t, err)
	assert.Equal(t, ProtocolVersion, hello.Protocol)
	assert.Equal(t, "2026.7.1-2", hello.Server.Version)
	assert.Equal(t, RequiredOperatorScopes, hello.Auth.Scopes)

	type response struct {
		Method string `json:"method"`
	}
	var first, second response
	var firstErr, secondErr error
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		firstErr = client.Call(context.Background(), "first", map[string]any{"n": 1}, &first)
	}()
	go func() {
		defer wait.Done()
		secondErr = client.Call(context.Background(), "second", map[string]any{"n": 2}, &second)
	}()
	wait.Wait()
	require.NoError(t, firstErr)
	require.NoError(t, secondErr)
	assert.Equal(t, "first", first.Method)
	assert.Equal(t, "second", second.Method)
}

func TestClientHandshakeFailures(t *testing.T) {
	t.Run("Given the HTTP upgrade is rejected when dialing then the response body is closed", func(t *testing.T) {
		body := &closeTrackingBody{}
		client, err := NewClient(Config{
			URL: "ws://127.0.0.1:18789", Identity: testIdentity(t), Platform: "linux",
		})
		require.NoError(t, err)
		client.dialer = testWebSocketDialer(func(
			context.Context,
			string,
			http.Header,
		) (*websocket.Conn, *http.Response, error) {
			return nil, &http.Response{Body: body}, errors.New("upgrade rejected")
		})

		_, err = client.Start(context.Background())
		require.Error(t, err)
		assert.True(t, body.closed.Load(), "the rejected handshake response body must be closed")
	})

	t.Run("Given authentication is rejected when connecting then the structured error is returned without the credential", func(t *testing.T) {
		authValue := strings.Repeat("z", 41)
		gatewayURL := newTestGateway(t, func(conn *websocket.Conn, _ int) {
			writeChallenge(t, conn, "auth-nonce")
			request := readTestRequest(t, conn)
			writeTestJSON(t, conn, map[string]any{
				"type": "res", "id": request.ID, "ok": false,
				"error": map[string]any{
					"code": "AUTH_TOKEN_MISMATCH", "message": "bad credential " + authValue,
					"retryable": false,
				},
			})
		})
		client, err := NewClient(Config{URL: gatewayURL, Token: authValue, Identity: testIdentity(t), Platform: "linux"})
		require.NoError(t, err)
		_, err = client.Start(context.Background())
		require.Error(t, err)
		var rpcErr *RPCError
		require.ErrorAs(t, err, &rpcErr)
		assert.Equal(t, "AUTH_TOKEN_MISMATCH", rpcErr.Code)
		assert.NotContains(t, err.Error(), authValue)
	})

	t.Run("Given a required scope is not granted when connecting then the connection is rejected", func(t *testing.T) {
		gatewayURL := newTestGateway(t, func(conn *websocket.Conn, _ int) {
			writeChallenge(t, conn, "scope-nonce")
			request := readTestRequest(t, conn)
			writeHello(t, conn, request.ID, ProtocolVersion, []string{"operator.read", "operator.write"}, "scope-conn")
		})
		client, err := NewClient(Config{URL: gatewayURL, Identity: testIdentity(t), Platform: "linux"})
		require.NoError(t, err)
		_, err = client.Start(context.Background())
		assert.ErrorIs(t, err, ErrRequiredScopeMissing)
	})

	t.Run("Given the negotiated protocol is outside protocol 4 when connecting then it is rejected", func(t *testing.T) {
		gatewayURL := newTestGateway(t, func(conn *websocket.Conn, _ int) {
			writeChallenge(t, conn, "protocol-nonce")
			request := readTestRequest(t, conn)
			writeHello(t, conn, request.ID, 5, RequiredOperatorScopes, "protocol-conn")
		})
		client, err := NewClient(Config{URL: gatewayURL, Identity: testIdentity(t), Platform: "linux"})
		require.NoError(t, err)
		_, err = client.Start(context.Background())
		assert.ErrorIs(t, err, ErrProtocolMismatch)
	})
}

func TestClientPreservesStructuredApprovalTerminalReason(t *testing.T) {
	t.Run("Given an approval resolve loses a client race when the Gateway rejects it then the terminal reason remains structured", func(t *testing.T) {
		gatewayURL := newTestGateway(t, func(conn *websocket.Conn, _ int) {
			writeChallenge(t, conn, "approval-race-nonce")
			connect := readTestRequest(t, conn)
			writeHello(t, conn, connect.ID, ProtocolVersion, RequiredOperatorScopes, "approval-race-conn")
			request := readTestRequest(t, conn)
			require.Equal(t, "exec.approval.resolve", request.Method)
			writeTestJSON(t, conn, map[string]any{
				"type": "res", "id": request.ID, "ok": false,
				"error": map[string]any{
					"code": "INVALID_REQUEST", "message": "approval already resolved",
					"details": map[string]any{"reason": "APPROVAL_ALREADY_RESOLVED"},
				},
			})
		})
		client, err := NewClient(Config{URL: gatewayURL, Identity: testIdentity(t), Platform: "linux"})
		require.NoError(t, err)
		defer client.Close()
		_, err = client.Start(context.Background())
		require.NoError(t, err)

		err = client.Call(context.Background(), "exec.approval.resolve", map[string]any{
			"id": "approval-1", "decision": "allow-once",
		}, nil)
		var rpcErr *RPCError
		require.ErrorAs(t, err, &rpcErr)
		assert.Equal(t, "APPROVAL_ALREADY_RESOLVED", rpcErr.Reason)
	})
}

func TestClientEventsGapTimeoutAndReconnect(t *testing.T) {
	t.Run("Given an unknown event and a sequence gap when reading then the client remains usable and reports the gap", func(t *testing.T) {
		gatewayURL := newTestGateway(t, func(conn *websocket.Conn, _ int) {
			writeChallenge(t, conn, "event-nonce")
			connect := readTestRequest(t, conn)
			writeHello(t, conn, connect.ID, ProtocolVersion, RequiredOperatorScopes, "event-conn")
			writeTestJSON(t, conn, map[string]any{
				"type": "event", "event": "future.unknown", "seq": 2,
				"payload": map[string]any{"newField": true},
			})
			writeTestJSON(t, conn, map[string]any{
				"type": "event", "event": "agent", "seq": 4,
				"payload": map[string]any{"runId": "run-1", "seq": 1, "stream": "lifecycle", "ts": 1, "data": map[string]any{}},
			})
			request := readTestRequest(t, conn)
			writeTestJSON(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"ok": true}})
		})
		client, err := NewClient(Config{URL: gatewayURL, Identity: testIdentity(t), Platform: "linux"})
		require.NoError(t, err)
		defer client.Close()
		_, err = client.Start(context.Background())
		require.NoError(t, err)

		first := <-client.Events()
		second := <-client.Events()
		assert.Equal(t, "future.unknown", first.Name)
		assert.Equal(t, "agent", second.Name)
		<-client.Gaps()

		var response struct {
			OK bool `json:"ok"`
		}
		require.NoError(t, client.Call(context.Background(), "still.alive", map[string]any{}, &response))
		assert.True(t, response.OK)
	})

	t.Run("Given an RPC response does not arrive before the context deadline then Call times out", func(t *testing.T) {
		requestSeen := make(chan struct{})
		gatewayURL := newTestGateway(t, func(conn *websocket.Conn, _ int) {
			writeChallenge(t, conn, "timeout-nonce")
			connect := readTestRequest(t, conn)
			writeHello(t, conn, connect.ID, ProtocolVersion, RequiredOperatorScopes, "timeout-conn")
			_ = readTestRequest(t, conn)
			close(requestSeen)
			<-time.After(200 * time.Millisecond)
		})
		client, err := NewClient(Config{URL: gatewayURL, Identity: testIdentity(t), Platform: "linux"})
		require.NoError(t, err)
		defer client.Close()
		_, err = client.Start(context.Background())
		require.NoError(t, err)
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		err = client.Call(ctx, "never.responds", map[string]any{}, nil)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
		<-requestSeen
	})

	t.Run("Given the socket disconnects after ready when the Gateway returns then the client reconnects without replaying requests", func(t *testing.T) {
		gatewayURL := newTestGateway(t, func(conn *websocket.Conn, connection int) {
			writeChallenge(t, conn, "reconnect-nonce")
			connect := readTestRequest(t, conn)
			writeHello(t, conn, connect.ID, ProtocolVersion, RequiredOperatorScopes, "conn-"+string(rune('0'+connection)))
			if connection == 1 {
				time.Sleep(30 * time.Millisecond)
				_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "test reconnect"), time.Now().Add(time.Second))
				return
			}
			request := readTestRequest(t, conn)
			writeTestJSON(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"connection": connection}})
		})
		client, err := NewClient(Config{
			URL: gatewayURL, Identity: testIdentity(t), Platform: "linux",
			ReconnectInitial: 5 * time.Millisecond, ReconnectMax: 20 * time.Millisecond,
		})
		require.NoError(t, err)
		defer client.Close()
		_, err = client.Start(context.Background())
		require.NoError(t, err)
		<-client.Ready() // initial ready

		select {
		case <-client.Ready():
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for reconnect")
		}
		var response struct {
			Connection int `json:"connection"`
		}
		require.NoError(t, client.Call(context.Background(), "after.reconnect", map[string]any{}, &response))
		assert.Equal(t, 2, response.Connection)
	})
}
