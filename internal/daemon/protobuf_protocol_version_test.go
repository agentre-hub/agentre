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

// oneMinorAhead / twoMinorsAhead 是**相对本 build 的 Protocol** 往上的两档,用来演
// 「对端比我新」的两种局面。它们写死成字面量(与 previousProtocol 同一条理由:推算出来
// 的值会随 Protocol 一起漂),所以每次抬协议版本都要跟着抬 —— 忘了抬,oneMinorAhead
// 就等于本 build 自己的版本,那条「领先一档仍放行」的用例照旧绿着,却不再验它声称的
// 东西。下面 TestProtocolVersionFixtures_... 是防止这件事静默发生的守卫。
const (
	oneMinorAhead  = "0.5.0"
	twoMinorsAhead = "0.6.0"
)

// 这两个 fixture 必须真的高于本 build 的 Protocol,否则上下两条用例都退化成同义反复。
func TestProtocolVersionFixtures_MustStayAheadOfThisBuild(t *testing.T) {
	require.NotEqual(t, wireversion.Protocol, oneMinorAhead,
		"抬协议版本时要把 oneMinorAhead 一并抬:与本 build 相等就不再是「领先一档」")
	require.NotEqual(t, wireversion.Protocol, twoMinorsAhead,
		"抬协议版本时要把 twoMinorsAhead 一并抬")
	require.True(t, wireversion.Match(oneMinorAhead, wireversion.MinSupported),
		"oneMinorAhead 配本 build 的 floor 必须仍在窗口内 —— 这一条是「领先一档仍放行」的前提")
	require.False(t, wireversion.Match(twoMinorsAhead, oneMinorAhead),
		"twoMinorsAhead 配 oneMinorAhead 这个 floor 必须落在窗口外 —— 这是「floor 把我关在门外」的前提")
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
			// One minor ahead of this build's own Protocol, but declaring a
			// floor (wireversion.MinSupported) that still covers it.
			Code: code, DeviceName: "desktop", DeviceFingerprint: "device-1",
			ProtocolVersion: oneMinorAhead, MinSupportedProtocolVersion: wireversion.MinSupported,
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
			// Floor one minor ahead of this build's own Protocol
			// (wireversion.Protocol), so it no longer covers this build.
			Code: code, DeviceName: "desktop", DeviceFingerprint: "device-1",
			ProtocolVersion: twoMinorsAhead, MinSupportedProtocolVersion: oneMinorAhead,
		},
		func() *agentrewire.AuthPairResponse { return &agentrewire.AuthPairResponse{} })

	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, rpcerror.CodeProtocolVersion, rpcErr.Code)
	require.Contains(t, rpcErr.Message, twoMinorsAhead)
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
