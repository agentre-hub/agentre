package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func protobufHandshakeConns(t *testing.T) (*protorpc.Conn, *Daemon, context.Context) {
	t.Helper()
	daemon, err := New(Options{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { closeDB(daemon.db) })
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, daemon.protobufRegistry.Clone())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)
	return client, daemon, ctx
}

// Given a desktop built from the same wire package, When it pairs, Then the
// daemon accepts it and answers with its own protocol version — the desktop
// verifies us in the same breath, so a silent response is a failed handshake.
func TestAuthPair_GivenCallerAdvertisesTheSameProtocolVersion_WhenPairing_ThenAcceptedAndDaemonAnswersItsVersion(t *testing.T) {
	client, daemon, ctx := protobufHandshakeConns(t)
	code, err := daemon.pairing.Generate()
	require.NoError(t, err)

	response, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR),
		&agentrewire.AuthPairRequest{Code: code, DeviceName: "desktop", DeviceFingerprint: "device-1", ProtocolVersion: wireversion.Protocol},
		func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })

	require.NoError(t, err)
	require.NotEmpty(t, response.GetDeviceToken())
	require.Equal(t, wireversion.Protocol, response.GetProtocolVersion())
	require.Equal(t, wireversion.MinSupported, response.GetMinSupportedProtocolVersion())
}

// Given a desktop one minor ahead of the daemon but still declaring a floor
// that covers the daemon's Protocol, When it pairs, Then the handshake
// succeeds even though the two sides report different protocol_version
// strings — this is the version window's whole point: `make agentred-deploy`
// makes skew routine, and a window lets routine skew through instead of
// treating every mismatch as fatal.
func TestAuthPair_GivenCallerAdvertisesADifferentButCompatibleWindow_WhenPairing_ThenAccepted(t *testing.T) {
	client, daemon, ctx := protobufHandshakeConns(t)
	code, err := daemon.pairing.Generate()
	require.NoError(t, err)

	response, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR),
		&agentrewire.AuthPairRequest{
			Code: code, DeviceName: "desktop", DeviceFingerprint: "device-1",
			ProtocolVersion: "0.2.0", MinSupportedProtocolVersion: wireversion.MinSupported,
		},
		func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })

	require.NoError(t, err)
	require.NotEmpty(t, response.GetDeviceToken())
}

// Given a desktop whose declared floor already excludes the daemon's Protocol
// — it moved past a breaking change the daemon predates — When it pairs, Then
// the handshake is refused even though the caller's protocol_version alone
// would have fallen inside the daemon's own window: Match requires both
// directions, not just one.
func TestAuthPair_GivenCallerFloorExcludesTheDaemon_WhenPairing_ThenRefusedWithProtocolVersionCode(t *testing.T) {
	client, daemon, ctx := protobufHandshakeConns(t)
	code, err := daemon.pairing.Generate()
	require.NoError(t, err)

	_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR),
		&agentrewire.AuthPairRequest{
			Code: code, DeviceName: "desktop", DeviceFingerprint: "device-1",
			ProtocolVersion: "0.5.0", MinSupportedProtocolVersion: "0.3.0",
		},
		func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, rpcerror.CodeProtocolVersion, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "0.5.0")
}

// Given a desktop from another revision, When it pairs, Then the daemon refuses
// under a code of its own rather than "invalid params" — the desktop folds that
// code back into its version-mismatch sentinel, so an old desktop talking to a
// new agentred gets the same story as the reverse.
func TestAuthPair_GivenCallerAdvertisesAnotherProtocolVersion_WhenPairing_ThenRefusedWithProtocolVersionCode(t *testing.T) {
	client, daemon, ctx := protobufHandshakeConns(t)
	code, err := daemon.pairing.Generate()
	require.NoError(t, err)

	_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR),
		&agentrewire.AuthPairRequest{Code: code, DeviceName: "desktop", DeviceFingerprint: "device-1", ProtocolVersion: "0.0.9"},
		func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, rpcerror.CodeProtocolVersion, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "0.0.9")
	require.Contains(t, rpcErr.Message, wireversion.Protocol)
	require.Contains(t, rpcErr.Message, wireversion.MinSupported,
		"the rejection must name this build's whole window, not just its Protocol")
}

// Given a caller that predates protocol versioning, When it authenticates,
// Then proto3's zero value must be refused as "too old" rather than waved
// through — this is the direction where an unversioned peer is otherwise
// indistinguishable from a matching one.
func TestAuthConnect_GivenCallerOmitsTheProtocolVersion_WhenConnecting_ThenRefusedAsTooOld(t *testing.T) {
	client, _, ctx := protobufHandshakeConns(t)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT),
		&agentrewire.AuthConnectRequest{DeviceFingerprint: "device-1", DeviceToken: "token"},
		func() *agentrewire.AuthConnectResponse { return &agentrewire.AuthConnectResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, rpcerror.CodeProtocolVersion, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "no protocol version")
}

// The account handshake carries the same version field and must gate on it too;
// it is the one the relay and the web console use.
func TestAuthAccount_GivenCallerOmitsTheProtocolVersion_WhenAuthenticating_ThenRefusedAsTooOld(t *testing.T) {
	client, _, ctx := protobufHandshakeConns(t)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		&agentrewire.AuthAccountRequest{Credential: "token"},
		func() *agentrewire.AuthAccountResponse { return &agentrewire.AuthAccountResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, rpcerror.CodeProtocolVersion, rpcErr.Code)
	require.Contains(t, rpcErr.Message, "no protocol version")
}
