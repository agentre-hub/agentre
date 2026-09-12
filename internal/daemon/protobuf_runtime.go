package daemon

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/connection"
	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/portforward"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/pty/local"
	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

func protobufRuntimeError(err error) error {
	if err == nil {
		return nil
	}
	if rpcErr := remotewire.ToRPCError(err); rpcErr != nil {
		return &protorpc.Error{Code: int32(rpcErr.Code), Message: rpcErr.Message}
	}
	return protobufError(err)
}

func (d *Daemon) bindProtobufConn(conn *protorpc.Conn) {
	key := connection.Protobuf(conn)
	rh := d.newRuntimeHandlers()
	d.runtimeMu.Lock()
	d.runtimeHandlers[key] = rh
	d.runtimeMu.Unlock()
	bindProtobufTerminal(conn, localPTYBackendAdapter{be: local.NewBackend()})
	// 转发流族与终端族同形:挂连接级注册面,连接一断就把这条连接上开着的流全关掉,
	// 不留悬挂的流,也不留悬挂的本机 socket。
	portforward.BindConn(conn, d.portForward, requireProtobufAuth)
	d.registerProtobufRuntimeMethods(conn.Registry(), conn, rh)
	go func() {
		<-conn.Done()
		d.conns.remove(conn)
		cleanupCtx, cancel := context.WithTimeout(context.Background(), daemonConnectionCleanupTimeout)
		defer cancel()
		_ = rh.Close(cleanupCtx)
		d.runtimeMu.Lock()
		if d.runtimeHandlers[key] == rh {
			delete(d.runtimeHandlers, key)
		}
		d.runtimeMu.Unlock()
	}()
}

func (d *Daemon) claimProtobuf(ctx context.Context, conversationID string, peer devicefp.Initiator) (claimTicket, error) {
	resolved, err := handlers.ResolveSessionPeer(ctx, peer, d.loggedInAccountID)
	if err != nil {
		return claimTicket{}, err
	}
	return d.conns.claimFor(protorpc.ConnFromContext(ctx), resolved, conversationID), nil
}

func (d *Daemon) registerProtobufRuntimeMethods(reg *protorpc.Registry, conn *protorpc.Conn, rh *handlers.RuntimeHandlers) {
	guard := func(ctx context.Context) error { return requireProtobufAuth(ctx) }
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_DRAIN_PENDING), func() *agentrewire.RuntimeDrainPendingRequest { return &agentrewire.RuntimeDrainPendingRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeDrainPendingRequest) (*agentrewire.RuntimeDrainPendingResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.ConversationId, devicefp.Initiator(req.PeerFingerprint))
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		response, err := rh.DrainPending(ctx, req)
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return response, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT), func() *agentrewire.RuntimeAbortRequest { return &agentrewire.RuntimeAbortRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeAbortRequest) (*agentrewire.RuntimeAbortResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.ConversationId, devicefp.Initiator(req.PeerFingerprint))
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		response, err := rh.Abort(ctx, req)
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return response, nil
	})
	registerEmptyControl(reg, agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STOP_BACKGROUND_TASK, func() *agentrewire.RuntimeStopBackgroundTaskRequest {
		return &agentrewire.RuntimeStopBackgroundTaskRequest{}
	}, func(ctx context.Context, req *agentrewire.RuntimeStopBackgroundTaskRequest) error {
		_, err := rh.StopBackgroundTask(ctx, req)
		return err
	}, d, guard)
	registerGoal := func(method agentrewire.RpcMethod, handler func(context.Context, *agentrewire.RuntimeGoalRequest) (*agentrewire.RuntimeGoalResponse, error)) {
		protorpc.RegisterMethod(reg, uint32(method), func() *agentrewire.RuntimeGoalRequest { return &agentrewire.RuntimeGoalRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeGoalRequest) (*agentrewire.RuntimeGoalResponse, error) {
			if err := guard(ctx); err != nil {
				return nil, err
			}
			ticket, err := d.claimProtobuf(ctx, req.GetConversationId(), devicefp.Initiator(req.GetPeerFingerprint()))
			if err != nil {
				return nil, protobufRuntimeError(err)
			}
			response, err := handler(ctx, req)
			if err != nil {
				d.conns.undoClaim(ticket)
				return nil, protobufRuntimeError(err)
			}
			return response, nil
		})
	}
	registerGoal(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_GET, rh.GetGoal)
	registerGoal(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_SET, rh.SetGoal)
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_CLEAR), func() *agentrewire.RuntimeGoalRequest { return &agentrewire.RuntimeGoalRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeGoalRequest) (*agentrewire.RuntimeGoalClearResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.GetConversationId(), devicefp.Initiator(req.GetPeerFingerprint()))
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		response, err := rh.ClearGoal(ctx, req)
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return response, nil
	})
	// 会话族里**依赖这条连接**的那一半:runtime 族要认领这条连接、attach 还要把
	// 这条会话接到这条连接上。它们因此挂在连接级 registry 上,而不是 daemon 级。
	wireinbound.RegisterSessionMethods(reg, d.connSessionPorts(conn, rh))
}

func registerEmptyControl[Req interface {
	proto.Message
	GetConversationId() string
	GetPeerFingerprint() string
}](reg *protorpc.Registry, method agentrewire.RpcMethod, factory func() Req, handler func(context.Context, Req) error, d *Daemon, guard func(context.Context) error) {
	protorpc.RegisterMethod(reg, uint32(method), factory, func(ctx context.Context, req Req) (*agentrewire.Empty, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.GetConversationId(), devicefp.Initiator(req.GetPeerFingerprint()))
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		if err := handler(ctx, req); err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return &agentrewire.Empty{}, nil
	})
}

// claimThen 把 agentred 独有的「先认领这条连接、失败就撤回」包在端口实现外面。
//
// 认领必须先于调用:这一轮的事件要推回**这条**连接,而认领失败(点名了别人的 origin、
// 或对端不在这台机器的账号下)时那一轮根本不该起。调用失败则要把认领撤回来,否则这条
// 会话会一直挂在一条什么都没在跑的连接上。
func (d *Daemon) claimThen(ctx context.Context, conversationID string, peerFingerprint devicefp.Initiator, call func() error) error {
	ticket, err := d.claimProtobuf(ctx, conversationID, peerFingerprint)
	if err != nil {
		return err
	}
	if err := call(); err != nil {
		d.conns.undoClaim(ticket)
		return err
	}
	return nil
}

// connSessionPorts 是会话族里**依赖这条连接**的那一半端口。
//
// 认领(claimThen)、接管(AdoptForPeer + claimFor)与按连接持有的 runtime handler
// 都是 agentred 自己的事,留在这里;哪些方法存在、闸门与错误映射怎么套、端口缺席
// 怎么办,住在 wireinbound,两种执行端共用同一份。
//
// 这一侧的 handler 自己就说 agentrewire,所以除了认领这一层包装,每一格都是直接
// 转交 —— 请求与应答一次都不翻译。认领要读的那两格(对话 id 与发起方指纹)从请求
// 上直接取。
//
// Error 用的是 protobufRuntimeError 而不是 protobufError:runtime 这一族先过
// remotewire.ToRPCError,哨兵因此换来一个领域码(如 -32010 no active turn)。同一个
// 宿主的两族错误映射本就不同,这正是它必须是端口的原因。
func (d *Daemon) connSessionPorts(conn *protorpc.Conn, rh *handlers.RuntimeHandlers) wireinbound.SessionPorts {
	return wireinbound.SessionPorts{
		Auth:         requireProtobufAuth,
		Error:        protobufRuntimeError,
		Capabilities: rh.Capabilities,
		Attach: func(ctx context.Context, request *agentrewire.SessionAttachRequest) (*agentrewire.SessionAttachResponse, error) {
			peer, err := handlers.ResolveSessionPeer(ctx, devicefp.Initiator(request.GetPeerFingerprint()), d.loggedInAccountID)
			if err != nil {
				return nil, err
			}
			response, err := d.catchup.Attach(ctx, request)
			if err != nil {
				return nil, err
			}
			rh.AdoptForPeer(peer, response.GetConversationId(), agent_backend_entity.BackendType(response.GetBackendType()))
			d.conns.claimFor(conn, peer, response.GetConversationId())
			return response, nil
		},
		Run:                  claimed(d, rh.Run),
		Steer:                claimed(d, rh.Steer),
		CancelSteer:          claimed(d, rh.CancelSteer),
		SubmitAnswer:         claimed(d, rh.SubmitAnswer),
		SubmitToolPermission: claimed(d, rh.SubmitToolPermission),
		SetPermissionMode:    claimed(d, rh.SetPermissionMode),
	}
}

// claimed 把 agentred 独有的「先认领这条连接、失败就撤回」包在一个端口实现外面。
//
// 泛型而不是逐个手写闭包:六个方法包的是同一件事,而认领要读的两格
// (conversation_id 与 peer_fingerprint)每个请求消息都有 getter —— 类型约束把
// 「这个请求答得出这两格」变成编译期的事,漏掉一个就编译不过。
func claimed[Req interface {
	GetConversationId() string
	GetPeerFingerprint() string
}, Resp any](d *Daemon, port func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, request Req) (Resp, error) {
		var response Resp
		err := d.claimThen(ctx, request.GetConversationId(), devicefp.Initiator(request.GetPeerFingerprint()), func() (callErr error) {
			response, callErr = port(ctx, request)
			return callErr
		})
		if err != nil {
			var zero Resp
			return zero, err
		}
		return response, nil
	}
}
