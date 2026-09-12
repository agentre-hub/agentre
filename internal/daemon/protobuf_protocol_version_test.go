package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
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
		&agentrewire.AuthPairRequest{Code: code, DeviceName: "desktop", DeviceFingerprint: "device-1", ProtocolVersion: wireversion.Protocol, MinSupportedProtocolVersion: wireversion.MinSupported},
		func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })

	require.NoError(t, err)
	require.NotEmpty(t, response.GetDeviceToken())
	require.Equal(t, wireversion.Protocol, response.GetProtocolVersion())
	require.Equal(t, wireversion.MinSupported, response.GetMinSupportedProtocolVersion())
}

// newerThanThisBuild 是**相对本 build 的 Protocol** 往上的一档,用来演「对端比我新」。
// 它写死成字面量(推算出来的值会随 Protocol 一起漂),所以每次抬协议版本都要跟着抬 ——
// 忘了抬就等于本 build 自己的版本,下面那条用例会从「拒绝」退化成同义反复,因此用例里
// 先断言它确实不等于本 build 的号。
const newerThanThisBuild = "0.9.0"

// Given 一台比 daemon 新一档的桌面端,它出示的 min_supported 还能覆盖 daemon 的版本
// (这正是从前的区间协商会放行的那一形态),When 它来配对,Then daemon 照样拒绝 ——
// 本轮判据降成相等,`make agentred-deploy` 造成的版本漂移不再被悄悄放过,而是在握手处
// 说出来。
func TestAuthPair_GivenCallerAdvertisesANewerProtocolVersion_WhenPairing_ThenRefusedWithProtocolVersionCode(t *testing.T) {
	client, daemon, ctx := protobufHandshakeConns(t)
	code, err := daemon.pairing.Generate()
	require.NoError(t, err)
	require.NotEqual(t, wireversion.Protocol, newerThanThisBuild,
		"抬协议版本时要把 newerThanThisBuild 一并抬:与本 build 相等就不再是「更新的对端」")

	_, err = protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_PAIR),
		&agentrewire.AuthPairRequest{
			Code: code, DeviceName: "desktop", DeviceFingerprint: "device-1",
			ProtocolVersion: newerThanThisBuild, MinSupportedProtocolVersion: wireversion.MinSupported,
		},
		func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, rpcerror.CodeProtocolVersion, rpcErr.Code)
	require.Contains(t, rpcErr.Message, newerThanThisBuild)
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
	require.Contains(t, rpcErr.Message, wireversion.Protocol,
		"the rejection must name this build's own version, so the operator knows which release to deploy")
}

// Given a caller that leaves the protocol version field unset, When it
// authenticates, Then proto3's zero value must be refused rather than waved
// through — an empty field is otherwise indistinguishable from a matching one.
func TestAuthConnect_GivenCallerOmitsTheProtocolVersion_WhenConnecting_ThenRefusedAsTooOld(t *testing.T) {
	client, _, ctx := protobufHandshakeConns(t)

	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_CONNECT),
		&agentrewire.AuthConnectRequest{DeviceFingerprint: "device-1", DeviceToken: "token"},
		func() *agentrewire.AuthConnectResponse { return &agentrewire.AuthConnectResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, rpcerror.CodeProtocolVersion, rpcErr.Code)
	require.Contains(t, rpcErr.Message, wireversion.MinSupported)
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
	require.Contains(t, rpcErr.Message, wireversion.MinSupported)
}
