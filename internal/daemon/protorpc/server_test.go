package protorpc_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestLANServerNegotiatesBinaryProtobufSubprotocol(t *testing.T) {
	require.Equal(t, "agentre-protobuf", protorpc.Subprotocol)

	registry := protorpc.NewRegistry()
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING),
		func() *agentrewire.HealthPingRequest { return &agentrewire.HealthPingRequest{} },
		func(context.Context, *agentrewire.HealthPingRequest) (*agentrewire.HealthPingResponse, error) {
			return &agentrewire.HealthPingResponse{InstanceUuid: "daemon"}, nil
		})
	server := protorpc.NewLANServer(protorpc.LANOpts{Host: "127.0.0.1", Port: 0, Registry: registry})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = server.Run(ctx) }()
	require.Eventually(t, func() bool { return server.Addr() != "" }, time.Second, 10*time.Millisecond)

	dialer := websocket.Dialer{Subprotocols: []string{protorpc.Subprotocol}}
	ws, response, err := dialer.Dial(server.URL(), http.Header{})
	require.NoError(t, err)
	if response != nil {
		t.Cleanup(func() { _ = response.Body.Close() })
	}
	t.Cleanup(func() { _ = ws.Close() })
	require.Equal(t, protorpc.Subprotocol, ws.Subprotocol())
	client := protorpc.NewConn(protorpc.NewWebSocketFrameConn(ws), protorpc.NewRegistry())
	go client.Serve(ctx)
	result, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING), &agentrewire.HealthPingRequest{}, func() *agentrewire.HealthPingResponse { return &agentrewire.HealthPingResponse{} })
	require.NoError(t, err)
	require.Equal(t, "daemon", result.InstanceUuid)

	wrongDialer := websocket.Dialer{Subprotocols: []string{"unsupported-protocol"}}
	wrong, wrongResponse, err := wrongDialer.Dial(server.URL(), nil)
	if wrong != nil {
		_ = wrong.Close()
	}
	if wrongResponse != nil {
		_ = wrongResponse.Body.Close()
	}
	require.Error(t, err)
}
