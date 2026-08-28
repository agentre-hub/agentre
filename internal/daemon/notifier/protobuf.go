package notifier

import (
	"context"
	"fmt"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type Protobuf struct{ conn *protorpc.Conn }

func NewProtobuf(conn *protorpc.Conn) *Protobuf { return &Protobuf{conn: conn} }

// Notify 把发送侧已经转换好的通知原样写上连接。这里**不做转换** —— 转换在
// handlers 的会话出口那一次就做完了(它还要把同一条消息落库),再转一次等于给每个
// 流式 token 白付一次 JSON 解码加一整棵消息树。
func (n *Protobuf) Notify(notification *agentrewire.RpcNotification) error {
	return n.conn.Notify(notification)
}

func (n *Protobuf) Request(ctx context.Context, method string, params any, result any) error {
	if method != wire.MethodMCPProxy {
		return fmt.Errorf("daemon: protobuf reverse request has no typed adapter for %q", method)
	}
	request, ok := params.(wire.MCPProxyRequest)
	if !ok {
		return fmt.Errorf("daemon: protobuf MCP request has type %T", params)
	}
	response, err := protorpc.CallMethod(ctx, n.conn, uint32(agentrewire.RpcMethod_RPC_METHOD_MCP_PROXY),
		protowire.MCPProxyRequestToProto(request), func() *agentrewire.MCPProxyResponse { return &agentrewire.MCPProxyResponse{} })
	if err != nil {
		return err
	}
	out, ok := result.(*wire.MCPProxyResponse)
	if !ok {
		return fmt.Errorf("daemon: protobuf MCP response has type %T", result)
	}
	*out = protowire.MCPProxyResponseFromProto(response)
	return nil
}
