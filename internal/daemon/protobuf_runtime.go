package daemon

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/connection"
	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/pty/local"
	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
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

func (d *Daemon) claimProtobuf(ctx context.Context, conversationID string, peer string) (claimTicket, error) {
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
		ticket, err := d.claimProtobuf(ctx, req.ConversationId, req.PeerFingerprint)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		result, err := rh.DrainPending(ctx, remotewire.DrainParams{ConversationID: req.ConversationId, PeerFingerprint: req.PeerFingerprint})
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		response := &agentrewire.RuntimeDrainPendingResponse{}
		for _, steer := range result.Steers {
			response.Steers = append(response.Steers, &agentrewire.ConsumedSteer{QueuedId: steer.QueuedID, Text: steer.Text, SourcePeer: steer.SourcePeer, SourceName: steer.SourceName})
		}
		return response, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT), func() *agentrewire.RuntimeAbortRequest { return &agentrewire.RuntimeAbortRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeAbortRequest) (*agentrewire.RuntimeAbortResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.ConversationId, req.PeerFingerprint)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		result, err := rh.Abort(ctx, remotewire.AbortParams{ConversationID: req.ConversationId, PeerFingerprint: req.PeerFingerprint, TurnToken: req.TurnToken})
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return &agentrewire.RuntimeAbortResponse{TurnKind: string(result.TurnKind)}, nil
	})
	registerEmptyControl(reg, agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STOP_BACKGROUND_TASK, func() *agentrewire.RuntimeStopBackgroundTaskRequest {
		return &agentrewire.RuntimeStopBackgroundTaskRequest{}
	}, func(ctx context.Context, req *agentrewire.RuntimeStopBackgroundTaskRequest) error {
		_, err := rh.StopBackgroundTask(ctx, remotewire.StopBackgroundTaskParams{ConversationID: req.ConversationId, PeerFingerprint: req.PeerFingerprint, TaskID: req.TaskId})
		return err
	}, d, guard)
	registerGoal := func(method agentrewire.RpcMethod, handler func(context.Context, remotewire.GoalParams) (remotewire.GoalResult, error)) {
		protorpc.RegisterMethod(reg, uint32(method), func() *agentrewire.RuntimeGoalRequest { return &agentrewire.RuntimeGoalRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeGoalRequest) (*agentrewire.RuntimeGoalResponse, error) {
			if err := guard(ctx); err != nil {
				return nil, err
			}
			params, err := protowire.GoalRequestFromProto(req)
			if err != nil {
				return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: err.Error()}
			}
			ticket, err := d.claimProtobuf(ctx, params.ConversationID, params.PeerFingerprint)
			if err != nil {
				return nil, protobufRuntimeError(err)
			}
			result, err := handler(ctx, params)
			if err != nil {
				d.conns.undoClaim(ticket)
				return nil, protobufRuntimeError(err)
			}
			return &agentrewire.RuntimeGoalResponse{Goal: goalToProto(result.Goal)}, nil
		})
	}
	registerGoal(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_GET, rh.GetGoal)
	registerGoal(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_SET, rh.SetGoal)
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_CLEAR), func() *agentrewire.RuntimeGoalRequest { return &agentrewire.RuntimeGoalRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeGoalRequest) (*agentrewire.RuntimeGoalClearResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		params, err := protowire.GoalRequestFromProto(req)
		if err != nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: err.Error()}
		}
		ticket, err := d.claimProtobuf(ctx, params.ConversationID, params.PeerFingerprint)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		result, err := rh.ClearGoal(ctx, params)
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return &agentrewire.RuntimeGoalClearResponse{Cleared: result.Cleared}, nil
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
		ticket, err := d.claimProtobuf(ctx, req.GetConversationId(), req.GetPeerFingerprint())
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

func goalToProto(goal *agentruntime.Goal) *agentrewire.Goal {
	if goal == nil {
		return nil
	}
	var budget *int32
	if goal.TokenBudget != nil {
		value := int32(*goal.TokenBudget)
		budget = &value
	}
	return &agentrewire.Goal{ThreadId: goal.ThreadID, Objective: goal.Objective, Status: goal.Status, TokenBudget: budget, TokensUsed: int32(goal.TokensUsed), TimeUsedSeconds: int32(goal.TimeUsedSeconds), CreatedAt: goal.CreatedAt, UpdatedAt: goal.UpdatedAt}
}

// claimThen 把 agentred 独有的「先认领这条连接、失败就撤回」包在端口实现外面。
//
// 认领必须先于调用:这一轮的事件要推回**这条**连接,而认领失败(点名了别人的 origin、
// 或对端不在这台机器的账号下)时那一轮根本不该起。调用失败则要把认领撤回来,否则这条
// 会话会一直挂在一条什么都没在跑的连接上。
func (d *Daemon) claimThen(ctx context.Context, conversationID, peerFingerprint string, call func() error) error {
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
// 都是 agentred 自己的事,留在这里;线形状住在 wireinbound,两种执行端共用同一份。
//
// Error 用的是 protobufRuntimeError 而不是 protobufError:runtime 这一族先过
// remotewire.ToRPCError,哨兵因此换来一个领域码(如 -32010 no active turn)。同一个
// 宿主的两族错误映射本就不同,这正是它必须是端口的原因。
func (d *Daemon) connSessionPorts(conn *protorpc.Conn, rh *handlers.RuntimeHandlers) wireinbound.SessionPorts {
	return wireinbound.SessionPorts{
		Auth:  requireProtobufAuth,
		Error: protobufRuntimeError,
		// 请求体解不开时 agentred 答 -32602 invalid params —— 与 Error 那条路分开,
		// 桌面端此刻在这一格上答的是 -32603。差异既存,不在这次收编里统一。
		DecodeError: func(err error) error {
			return &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: err.Error()}
		},
		Capabilities: rh.Capabilities,
		Attach: func(ctx context.Context, params remotewire.SessionAttachParams) (remotewire.SessionAttachResult, error) {
			peer, err := handlers.ResolveSessionPeer(ctx, params.PeerFingerprint, d.loggedInAccountID)
			if err != nil {
				return remotewire.SessionAttachResult{}, err
			}
			result, err := d.catchup.Attach(ctx, params)
			if err != nil {
				return remotewire.SessionAttachResult{}, err
			}
			rh.AdoptForPeer(peer, result.ConversationID, agent_backend_entity.BackendType(result.BackendType))
			d.conns.claimFor(conn, peer, result.ConversationID)
			return result, nil
		},
		Run: func(ctx context.Context, params remotewire.RunParams) (remotewire.RunAck, error) {
			var ack remotewire.RunAck
			err := d.claimThen(ctx, params.ConversationID, params.PeerFingerprint, func() (runErr error) {
				ack, runErr = rh.Run(ctx, params)
				return runErr
			})
			return ack, err
		},
		Steer: func(ctx context.Context, params remotewire.SteerParams) (remotewire.SteerResult, error) {
			var result remotewire.SteerResult
			err := d.claimThen(ctx, params.ConversationID, params.PeerFingerprint, func() (steerErr error) {
				result, steerErr = rh.Steer(ctx, params)
				return steerErr
			})
			return result, err
		},
		CancelSteer: func(ctx context.Context, params remotewire.CancelSteerParams) (remotewire.CancelSteerResult, error) {
			var result remotewire.CancelSteerResult
			err := d.claimThen(ctx, params.ConversationID, params.PeerFingerprint, func() (cancelErr error) {
				result, cancelErr = rh.CancelSteer(ctx, params)
				return cancelErr
			})
			return result, err
		},
		SubmitAnswer: func(ctx context.Context, params remotewire.SubmitAnswerParams) (remotewire.PeerSessionControlResult, error) {
			// agentred 从不填 already_handled —— rh.SubmitAnswer 交出的 wire.OK 里没有
			// 这一格,零值保住的正是「本次提交成功」这个原义。
			err := d.claimThen(ctx, params.ConversationID, params.PeerFingerprint, func() error {
				_, submitErr := rh.SubmitAnswer(ctx, params)
				return submitErr
			})
			return remotewire.PeerSessionControlResult{}, err
		},
		SubmitToolPermission: func(ctx context.Context, params remotewire.SubmitToolPermissionParams) (remotewire.PeerSessionControlResult, error) {
			err := d.claimThen(ctx, params.ConversationID, params.PeerFingerprint, func() error {
				_, submitErr := rh.SubmitToolPermission(ctx, params)
				return submitErr
			})
			return remotewire.PeerSessionControlResult{}, err
		},
		SetPermissionMode: func(ctx context.Context, params remotewire.SetPermissionModeParams) error {
			return d.claimThen(ctx, params.ConversationID, params.PeerFingerprint, func() error {
				_, modeErr := rh.SetPermissionMode(ctx, params)
				return modeErr
			})
		},
	}
}
