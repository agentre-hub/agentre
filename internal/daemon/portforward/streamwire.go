package portforward

import (
	"context"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 流族的注册面。它挂在**每一条连接**上,而声明族挂在 daemon 级注册面上
// (internal/pkg/wireinbound)——两者的差别不是风格,是生命周期:一张声明表属于这台
// 机器,一条转发流属于承载它的那条连接,连接没了流就该没。
//
// 两种执行端(agentred 的 bindProtobufConn、桌面端 peer 的每连接注册面)调的是这同一
// 个函数:规格「设备侧的目标限制」把两类设备并列写成「都可能是被访问的一方」,判定
// 各写一份就会对同一条声明给出两种答复,注册面各写一份则会让同一个方法号在两台机器上
// 认不同的参数。

// BindConn 把流族挂到这一条连接上,并让流的生命周期跟着它走(与 terminal.* 同形:
// conn.Done() 一到就 CloseAll,不留悬挂的流,也不留悬挂的本机 socket)。
//
// auth 是宿主自己那道闸门:两种执行端的拒绝语在线上不是同一句,所以它是参数而不是
// 本包自己写死的一句话。
func BindConn(conn *protorpc.Conn, gate *Handlers, auth func(context.Context) error) *Streams {
	streams := NewStreams(StreamOptions{Gate: gate, Notify: conn.Notify})
	RegisterStreamMethods(conn.Registry(), streams, auth)
	go func() {
		<-conn.Done()
		streams.CloseAll()
	}()
	return streams
}

// RegisterStreamMethods 挂上 open / write / close / ack 四个方法。
//
// 闸门缺席时不注册:一条能开转发流却不问「这个端口声明过没有」的连接,比没有这个能力
// 更糟——调用方收到 method not found,据此知道这台机器办不到,而不是拿到一条没人把关
// 的通路。
func RegisterStreamMethods(registry *protorpc.Registry, streams *Streams, auth func(context.Context) error) {
	if streams == nil || streams.gate == nil {
		return
	}
	if auth == nil {
		auth = func(context.Context) error { return nil }
	}
	guard := func(ctx context.Context) error { return auth(ctx) }

	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_OPEN),
		func() *agentrewire.PortForwardOpenRequest { return &agentrewire.PortForwardOpenRequest{} },
		func(ctx context.Context, request *agentrewire.PortForwardOpenRequest) (*agentrewire.PortForwardOpenResponse, error) {
			if err := guard(ctx); err != nil {
				return nil, err
			}
			return streams.Open(ctx, request)
		})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_WRITE),
		func() *agentrewire.PortForwardWriteRequest { return &agentrewire.PortForwardWriteRequest{} },
		func(ctx context.Context, request *agentrewire.PortForwardWriteRequest) (*agentrewire.Empty, error) {
			if err := guard(ctx); err != nil {
				return nil, err
			}
			return streams.Write(ctx, request)
		})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_CLOSE),
		func() *agentrewire.PortForwardCloseRequest { return &agentrewire.PortForwardCloseRequest{} },
		func(ctx context.Context, request *agentrewire.PortForwardCloseRequest) (*agentrewire.Empty, error) {
			if err := guard(ctx); err != nil {
				return nil, err
			}
			return streams.Close(ctx, request)
		})
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_PORT_FORWARD_ACK),
		func() *agentrewire.PortForwardAckRequest { return &agentrewire.PortForwardAckRequest{} },
		func(ctx context.Context, request *agentrewire.PortForwardAckRequest) (*agentrewire.Empty, error) {
			if err := guard(ctx); err != nil {
				return nil, err
			}
			return streams.Ack(ctx, request)
		})
}
