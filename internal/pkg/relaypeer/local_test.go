package relaypeer

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

// selfMachine 是「这台机器自己」:答 auth.account(把凭据认成 fp:<凭据>)与 health.ping。
func selfMachine(t *testing.T, end protorpc.PayloadChannel) {
	t.Helper()
	reg := protorpc.NewRegistry()
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		func() *agentrewire.AuthAccountRequest { return &agentrewire.AuthAccountRequest{} },
		func(ctx context.Context, req *agentrewire.AuthAccountRequest) (*agentrewire.AuthAccountResponse, error) {
			fp := "fp:" + req.GetCredential()
			protorpc.ConnFromContext(ctx).SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: fp})
			return &agentrewire.AuthAccountResponse{Ok: true, ProtocolVersion: wireversion.Protocol, PeerFingerprint: fp}, nil
		})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_HEALTH_PING),
		func() *agentrewire.HealthPingRequest { return &agentrewire.HealthPingRequest{} },
		func(ctx context.Context, _ *agentrewire.HealthPingRequest) (*agentrewire.HealthPingResponse, error) {
			return &agentrewire.HealthPingResponse{InstanceUuid: protorpc.ConnFromContext(ctx).Auth().DeviceFingerprint}, nil
		})
	conn := protorpc.NewConn(protorpc.NewPayloadFrameConn(end), reg)
	go conn.Serve(context.Background())
}

func TestOpenChannel_GivenAnInProcessPipeToThisMachine_WhenOpened_ThenItAuthenticatesAndCallsGoThroughWithoutARelay(t *testing.T) {
	client, server := Pipe()
	selfMachine(t, server)

	conn, err := OpenChannel(context.Background(), client, "device-token")
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	assert.Equal(t, "fp:device-token", conn.SelfFingerprint, "the identity comes from the same auth.account handshake as over the relay")
	ping, err := wirecall.HealthPing(context.Background(), conn, &agentrewire.HealthPingRequest{})
	require.NoError(t, err)
	assert.Equal(t, "fp:device-token", ping.GetInstanceUuid())

	require.NoError(t, conn.Close())
	select {
	case <-server.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("closing one end of the pipe must end the other")
	}
}

func TestOpenChannel_GivenTheOtherEndIsGone_WhenOpened_ThenItFailsAsUnavailable(t *testing.T) {
	client, server := Pipe()
	require.NoError(t, server.Close())

	_, err := OpenChannel(context.Background(), client, "device-token")
	assert.ErrorIs(t, err, ErrRelayUnavailable)
}

// Given one end wrote its last payload and then closed the pipe, When the other
// end reads, Then it gets that payload before io.EOF — a reply written just
// before the close (the relay's error frame, a final answer) is never dropped.
func TestPipe_GivenAPayloadWrittenBeforeClose_ThenTheReaderGetsItBeforeEOF(t *testing.T) {
	for range 200 {
		a, b := Pipe()
		require.NoError(t, a.WritePayload([]byte("last")))
		require.NoError(t, a.Close())

		got, err := b.ReadPayload()
		require.NoError(t, err)
		assert.Equal(t, []byte("last"), got)
		_, err = b.ReadPayload()
		require.ErrorIs(t, err, io.EOF)
	}
}
