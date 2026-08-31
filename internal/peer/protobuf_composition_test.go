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
	// 畸形帧之后这条连接照常应答:下一次 RPC 拿得到**注册表给的回答**。这里那个回答
	// 是「拒绝」——生产装配下没有登录、验不了凭据,一个编出来的凭据当然不该握手成功
	// (决策 8);它同样证明连接没被那一帧带走。
	_, err := protorpc.CallMethod(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		&agentrewire.AuthAccountRequest{Credential: "credential", ProtocolVersion: wireversion.Protocol},
		func() *agentrewire.AuthAccountResponse { return &agentrewire.AuthAccountResponse{} })
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, int32(-32001), rpcErr.Code)
	require.False(t, server.Auth().Authenticated)
}
