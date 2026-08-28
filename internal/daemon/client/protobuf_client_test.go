package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestDialProtobuf_GivenBinaryDaemon_WhenCallingTypedMethod_ThenNegotiatesProtocolAndSignalsDisconnect(t *testing.T) {
	gotSubprotocol := make(chan string, 1)
	gotRequest := make(chan *agentrewire.AuthAccountRequest, 1)
	closePeer := make(chan struct{})
	upgrader := websocket.Upgrader{Subprotocols: []string{protorpc.Subprotocol}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		gotSubprotocol <- ws.Subprotocol()
		kind, payload, err := ws.ReadMessage()
		if err != nil {
			return
		}
		assert.Equal(t, websocket.BinaryMessage, kind)
		var frame agentrewire.RpcFrame
		if !assert.NoError(t, proto.Unmarshal(payload, &frame)) {
			return
		}
		rpcRequest := frame.GetRequest()
		assert.Equal(t, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT), rpcRequest.GetMethodId())
		request := &agentrewire.AuthAccountRequest{}
		if !assert.NoError(t, proto.Unmarshal(rpcRequest.GetEncodedPayload(), request)) {
			return
		}
		gotRequest <- request
		encodedResponse, err := proto.Marshal(&agentrewire.AuthAccountResponse{Ok: true, InstanceUuid: "uuid-1", ProtocolVersion: wireversion.Protocol})
		if err != nil {
			return
		}
		response, err := proto.Marshal(&agentrewire.RpcFrame{Id: frame.GetId(), Body: &agentrewire.RpcFrame_Response{Response: &agentrewire.Response{MethodId: rpcRequest.GetMethodId(), EncodedPayload: encodedResponse}}})
		if err != nil {
			return
		}
		if err := ws.WriteMessage(websocket.BinaryMessage, response); err != nil {
			return
		}
		<-closePeer
	}))
	defer server.Close()

	client, err := DialProtobuf(t.Context(), Options{URL: "ws" + strings.TrimPrefix(server.URL, "http")})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	response, err := client.AuthAccount(t.Context(), &agentrewire.AuthAccountRequest{Credential: "token", DeviceFingerprint: "sha256:desktop"})
	require.NoError(t, err)
	assert.True(t, response.GetOk())
	assert.Equal(t, "uuid-1", response.GetInstanceUuid())
	assert.Equal(t, protorpc.Subprotocol, <-gotSubprotocol)
	request := <-gotRequest
	assert.Equal(t, "token", request.GetCredential())
	assert.Equal(t, "sha256:desktop", request.GetDeviceFingerprint())

	close(closePeer)
	select {
	case <-client.Closed():
	case <-time.After(time.Second):
		t.Fatal("peer disconnect did not close the protobuf client")
	}
}

func TestDialRelayProtobuf_GivenAccountCredential_WhenAuthenticating_ThenUsesTypedPayload(t *testing.T) {
	got := make(chan *agentrewire.AuthAccountRequest, 1)
	upgrader := websocket.Upgrader{Subprotocols: []string{protorpc.Subprotocol}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer relay-token", r.Header.Get("Authorization"))
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		_, payload, err := ws.ReadMessage()
		if err != nil {
			return
		}
		var frame agentrewire.RpcFrame
		if proto.Unmarshal(payload, &frame) != nil {
			return
		}
		rpcRequest := frame.GetRequest()
		request := &agentrewire.AuthAccountRequest{}
		if proto.Unmarshal(rpcRequest.GetEncodedPayload(), request) != nil {
			return
		}
		got <- request
		encodedResponse, _ := proto.Marshal(&agentrewire.AuthAccountResponse{Ok: true, ProtocolVersion: wireversion.Protocol})
		payload, _ = proto.Marshal(&agentrewire.RpcFrame{Id: frame.GetId(), Body: &agentrewire.RpcFrame_Response{Response: &agentrewire.Response{MethodId: rpcRequest.GetMethodId(), EncodedPayload: encodedResponse}}})
		_ = ws.WriteMessage(websocket.BinaryMessage, payload)
		<-r.Context().Done()
	}))
	defer server.Close()

	client, err := DialRelayProtobuf(context.Background(), RelayOptions{
		URL:               "ws" + strings.TrimPrefix(server.URL, "http"),
		AccessToken:       "relay-token",
		DeviceFingerprint: "sha256:desktop",
	})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	request := <-got
	assert.Equal(t, "relay-token", request.GetCredential())
	assert.Equal(t, "sha256:desktop", request.GetDeviceFingerprint())
}

func TestDialProtobuf_GivenInflightCall_WhenContextIsCanceled_ThenSendsMatchingCancelFrame(t *testing.T) {
	requestID := make(chan uint64, 1)
	canceledID := make(chan uint64, 1)
	upgrader := websocket.Upgrader{Subprotocols: []string{protorpc.Subprotocol}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = ws.Close() }()
		for {
			kind, payload, err := ws.ReadMessage()
			if err != nil {
				return
			}
			assert.Equal(t, websocket.BinaryMessage, kind)
			var frame agentrewire.RpcFrame
			if !assert.NoError(t, proto.Unmarshal(payload, &frame)) {
				return
			}
			if frame.GetRequest() != nil {
				requestID <- frame.GetId()
			}
			if frame.GetCancel() != nil {
				canceledID <- frame.GetCancel().GetRequestId()
				return
			}
		}
	}))
	defer server.Close()

	client, err := DialProtobuf(t.Context(), Options{URL: "ws" + strings.TrimPrefix(server.URL, "http")})
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	callCtx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		callErr := protorpc.CallMessage(callCtx, client.Conn(),
			uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST),
			&agentrewire.SessionListRequest{}, &agentrewire.SessionListResponse{})
		result <- callErr
	}()
	id := <-requestID
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	assert.Equal(t, id, <-canceledID)
}

func TestBuildTLSConfigModes(t *testing.T) {
	tests := []struct {
		name     string
		mode     TLSMode
		pem      string
		wantSkip bool
		wantErr  bool
	}{
		{"default", TLSDefault, "", false, false},
		{"empty defaults", "", "", false, false},
		{"skip verify", TLSSkipVerify, "", true, false},
		{"bad pin", TLSPinCert, "not a pem", false, true},
		{"bad CA", TLSCABundle, "not a pem", false, true},
		{"unknown", TLSMode("nonsense"), "", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := BuildTLSConfig(tc.mode, tc.pem)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSkip, cfg.InsecureSkipVerify)
		})
	}
}

func TestDialRelayProtobufClassifiesHandshakeFailures(t *testing.T) {
	tests := []struct {
		name   string
		status int
		want   error
	}{
		{"never registered", http.StatusNotFound, ErrRelayDaemonNotFound},
		{"offline", http.StatusConflict, ErrRelayDaemonOffline},
		{"forward failed", http.StatusBadGateway, ErrRelayForwardFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status) }))
			defer server.Close()
			_, err := DialRelayProtobuf(t.Context(), RelayOptions{URL: "ws" + strings.TrimPrefix(server.URL, "http"), AccessToken: "token", DeviceFingerprint: "sha256:desktop"})
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.want)
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	_, err := DialRelayProtobuf(t.Context(), RelayOptions{URL: "ws" + strings.TrimPrefix(server.URL, "http")})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRelayDaemonNotFound)
	assert.NotErrorIs(t, err, ErrRelayDaemonOffline)
	assert.NotErrorIs(t, err, ErrRelayForwardFailed)
}

// Given RaceProtobuf has picked a winning path, When the daemon sends a reverse
// request over that connection, Then the handler must run under a live context:
// RaceProtobuf cancels every dial context (including the winner's) before it
// returns, so a connection that treats the dial context as the request context
// hands every reverse handler an already-canceled ctx.
func TestRaceProtobuf_GivenWinningPath_WhenDaemonSendsReverseRequest_ThenHandlerContextIsNotCanceled(t *testing.T) {
	serverConns := make(chan *protorpc.Conn, 1)
	server := protorpc.NewLANServer(protorpc.LANOpts{
		Host:     "127.0.0.1",
		Registry: protorpc.NewRegistry(),
		OnConn:   func(conn *protorpc.Conn) { serverConns <- conn },
	})
	serverCtx, stopServer := context.WithCancel(context.Background())
	defer stopServer()
	go func() { _ = server.Run(serverCtx) }()
	require.Eventually(t, func() bool { return server.Addr() != "" }, 5*time.Second, 5*time.Millisecond)

	dialCtx, cancelDial := context.WithCancel(context.Background())
	defer cancelDial()
	winner, err := RaceProtobuf(dialCtx, ProtobufPath{
		Name:        "direct",
		Fingerprint: "sha256:daemon",
		Dial: func(ctx context.Context) (ProtobufConnection, error) {
			return DialProtobuf(ctx, Options{URL: server.URL()})
		},
	})
	require.NoError(t, err)
	defer func() { _ = winner.Close() }()

	handlerCtxErr := make(chan error, 1)
	protorpc.RegisterMethod(
		winner.Conn().Registry(),
		uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY),
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
		func(ctx context.Context, _ *agentrewire.Empty) (*agentrewire.Empty, error) {
			handlerCtxErr <- ctx.Err()
			return &agentrewire.Empty{}, nil
		},
	)

	var serverConn *protorpc.Conn
	select {
	case serverConn = <-serverConns:
	case <-time.After(5 * time.Second):
		t.Fatal("daemon side never observed the accepted connection")
	}
	_, err = protorpc.CallMethod(
		context.Background(),
		serverConn,
		uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY),
		&agentrewire.Empty{},
		func() *agentrewire.Empty { return &agentrewire.Empty{} },
	)
	require.NoError(t, err)

	select {
	case ctxErr := <-handlerCtxErr:
		require.NoError(t, ctxErr, "reverse request handler ran under a canceled context")
	case <-time.After(5 * time.Second):
		t.Fatal("reverse request never reached the handler")
	}
}
