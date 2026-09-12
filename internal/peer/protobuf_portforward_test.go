package peer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// Given 桌面端作为被访问的一方接下一条连接,When 看这条连接的注册面,Then 转发流族
// 四个方法都在上面,而且都在鉴权闸门后面。
//
// 断言打在**生产装配**上(newDevicePortForward + bindProtobufConn):测试里自己拼一份
// 会把「桌面端的每条连接到底挂了什么」偷换成「我这条用例挂了什么」,而后者永远是对的。
//
// 这一族不在 daemon 级注册面上,contract guard 那条也就问不到它 —— 少了这条用例,整族
// 可以一行不落地写完却从没被挂上去:编译绿、测试绿、浏览器打不开。
func TestBindProtobufConn_GivenADesktopBeingAccessed_WhenAConnectionArrives_ThenTheStreamFamilyIsRegistered(t *testing.T) {
	inbound := &Inbound{
		protobufRegistry: protorpc.NewRegistry(),
		portForward:      newDevicePortForward(),
	}

	clientTransport, serverTransport := peerProtoPipePair()
	server := protorpc.NewConn(serverTransport, inbound.protobufRegistry.Clone())
	inbound.bindProtobufConn(server)
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Serve(ctx)
	go server.Serve(ctx)

	registered := make(map[uint32]bool)
	for _, method := range server.Registry().RegisteredMethods() {
		registered[method] = true
	}
	for _, method := range []agentrewire.RpcMethod{
		agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_OPEN,
		agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_WRITE,
		agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_CLOSE,
		agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_ACK,
	} {
		assert.True(t, registered[uint32(method)], "桌面端的每条连接上必须挂着 %s", method)
	}

	// 没握手的对端连一条流都不该开得动。答的必须是 -32001 而不是 -32601:后者说的是
	// 「这台机器没有这个能力」,那是另一件事,调用方据它换一台机器。
	_, err := protorpc.CallMethod(ctx, client,
		uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_OPEN),
		&agentrewire.PortForwardOpenRequest{StreamId: "s1", Port: 3000, Method: "GET", Path: "/"},
		func() *agentrewire.PortForwardOpenResponse { return &agentrewire.PortForwardOpenResponse{} })
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.EqualValues(t, rpcerror.CodeUnauthorized, rpcErr.Code, "转发流族必须在鉴权闸门后面")
}
