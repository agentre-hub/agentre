package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// Given agentred 接下一条连接,When 看这条连接的注册面,Then 转发流族四个方法都在
// 上面,而且都在鉴权闸门后面。
//
// 这一族挂的是**连接级**注册面(与 terminal.* 同形),不在 daemon 级那一份上,所以
// wireinbound 的契约守卫问不到它 —— 少了这条用例,整族可以一行不落地写完却从没被挂上,
// 编译绿、测试绿、浏览器打不开。
func TestBindProtobufConn_GivenAnAgentredConnection_WhenItIsBound_ThenThePortForwardStreamFamilyIsRegistered(t *testing.T) {
	daemon, err := New(Options{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { closeDB(daemon.db) })

	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, daemon.protobufRegistry.Clone())
	daemon.bindProtobufConn(server)
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
		assert.True(t, registered[uint32(method)], "agentred 的每条连接上必须挂着 %s", method)
	}

	// 没握手的对端连一条流都不该开得动。答的必须是 -32001 而不是 -32601:后者说的是
	// 「这台机器没有这个能力」,那是另一件事,调用方据它换一台机器。
	_, err = protorpc.CallMethod(ctx, client,
		uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_OPEN),
		&agentrewire.PortForwardOpenRequest{StreamId: "s1", Port: 3000, Method: "GET", Path: "/"},
		func() *agentrewire.PortForwardOpenResponse { return &agentrewire.PortForwardOpenResponse{} })
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	assert.EqualValues(t, -32001, rpcErr.Code, "转发流族必须在鉴权闸门后面")
}
