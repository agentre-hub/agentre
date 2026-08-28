package fakepeer

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/client"
	"github.com/agentre-hub/agentre/internal/daemon/identity"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestServerGivenProductionClientThenNegotiatesBinaryProtobufAndServesTypedAuthSessionRuntime(t *testing.T) {
	server := startTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := client.DialProtobuf(ctx, client.Options{URL: server.URL()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	auth, err := cli.AuthConnect(ctx, &agentrewire.AuthConnectRequest{
		DeviceFingerprint:         testDeviceFingerprint,
		DeviceToken:               testDeviceAuthValue,
		ExpectedDaemonFingerprint: identity.DaemonFingerprint(testInstanceUUID),
	})
	require.NoError(t, err)
	require.True(t, auth.GetOk())

	sessions, err := protorpc.CallMethod(ctx, cli.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST),
		&agentrewire.SessionListRequest{}, func() *agentrewire.SessionListResponse { return &agentrewire.SessionListResponse{} })
	require.NoError(t, err)
	assert.Empty(t, sessions.GetSessions())
	caps, err := protorpc.CallMethod(ctx, cli.Conn(), uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES),
		&agentrewire.RuntimeCapabilitiesRequest{BackendType: "claudecode"}, func() *agentrewire.RuntimeCapabilitiesResponse { return &agentrewire.RuntimeCapabilitiesResponse{} })
	require.NoError(t, err)
	require.NotNil(t, caps.GetPermissionMode())
}

func TestServerRejectsUnsupportedOrMissingProtobufSubprotocol(t *testing.T) {
	server := startTestServer(t)
	for _, protocols := range [][]string{nil, {"unsupported-protocol"}} {
		dialer := websocket.Dialer{Subprotocols: protocols}
		conn, response, err := dialer.Dial(server.URL(), nil)
		if conn != nil {
			_ = conn.Close()
		}
		require.Error(t, err)
		if response != nil {
			assert.Equal(t, http.StatusBadRequest, response.StatusCode)
			_ = response.Body.Close()
		}
	}
}
