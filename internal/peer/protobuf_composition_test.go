package peer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

func TestProductionInboundRejectsMalformedBinaryAndContinuesWithProtobuf(t *testing.T) {
	registry := NewProtobufInboundRegistry(productionProtobufInboundDeps())
	clientTransport, serverTransport := peerProtoPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	require.NoError(t, clientTransport.WriteFrame([]byte{0xff}))
	response, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		&agentrewire.AuthAccountRequest{Credential: "credential", DeviceFingerprint: "protobuf-peer", ProtocolVersion: wireversion.Protocol},
		func() *agentrewire.AuthAccountResponse { return &agentrewire.AuthAccountResponse{} })
	require.NoError(t, err)
	require.True(t, response.Ok)
	require.Equal(t, "protobuf-peer", server.Auth().DeviceFingerprint)
}
