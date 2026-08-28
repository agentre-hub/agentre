package daemon

import (
	"context"

	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/daemon/connection"
	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/pty/local"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
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

func (d *Daemon) claimProtobuf(ctx context.Context, sessionID int64, peer string) (claimTicket, error) {
	resolved, err := handlers.ResolveSessionPeer(ctx, peer, d.claimedAccountID)
	if err != nil {
		return claimTicket{}, err
	}
	return d.conns.claimFor(protorpc.ConnFromContext(ctx), resolved, sessionID), nil
}

func (d *Daemon) registerProtobufRuntimeMethods(reg *protorpc.Registry, conn *protorpc.Conn, rh *handlers.RuntimeHandlers) {
	guard := func(ctx context.Context) error { return requireProtobufAuth(ctx) }
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES), func() *agentrewire.RuntimeCapabilitiesRequest { return &agentrewire.RuntimeCapabilitiesRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeCapabilitiesRequest) (*agentrewire.RuntimeCapabilitiesResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		result, err := rh.Capabilities(ctx, remotewire.CapabilitiesParams{BackendType: req.BackendType})
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		response := &agentrewire.RuntimeCapabilitiesResponse{PermissionMode: &agentrewire.PermissionModeMeta{AllowedModes: result.Capabilities.PermissionModeMeta.AllowedModes, DefaultMode: result.Capabilities.PermissionModeMeta.DefaultMode, SwitchableDuringTurn: result.Capabilities.PermissionModeMeta.SwitchableDuringTurn, Order: result.Capabilities.PermissionModeMeta.Order, LaunchDefaultMode: result.Capabilities.PermissionModeMeta.LaunchDefaultMode}}
		for name, enabled := range result.Capabilities.Set {
			response.Capabilities = append(response.Capabilities, &agentrewire.CapabilityEntry{Name: string(name), Enabled: enabled})
		}
		return response, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		params, err := protowire.RunRequestFromProto(req)
		if err != nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: err.Error()}
		}
		ticket, err := d.claimProtobuf(ctx, params.SessionID, params.PeerFingerprint)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		result, err := rh.Run(ctx, params)
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return &agentrewire.RuntimeRunResponse{SessionId: result.SessionID, ProviderSessionId: result.ProviderSessionID, LaunchPermissionMode: result.LaunchPermissionMode, ProviderFallbackKey: result.ProviderFallbackKey}, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER), func() *agentrewire.RuntimeSteerRequest { return &agentrewire.RuntimeSteerRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeSteerRequest) (*agentrewire.Empty, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.SessionId, req.PeerFingerprint)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		_, err = rh.Steer(ctx, remotewire.SteerParams{SessionID: req.SessionId, PeerFingerprint: req.PeerFingerprint, QueuedID: req.QueuedId, Text: req.Text})
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return &agentrewire.Empty{}, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CANCEL_STEER), func() *agentrewire.RuntimeCancelSteerRequest { return &agentrewire.RuntimeCancelSteerRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeCancelSteerRequest) (*agentrewire.RuntimeCancelSteerResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.SessionId, req.PeerFingerprint)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		result, err := rh.CancelSteer(ctx, remotewire.CancelSteerParams{SessionID: req.SessionId, PeerFingerprint: req.PeerFingerprint, QueuedID: req.QueuedId})
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return &agentrewire.RuntimeCancelSteerResponse{Removed: result.Removed}, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_DRAIN_PENDING), func() *agentrewire.RuntimeDrainPendingRequest { return &agentrewire.RuntimeDrainPendingRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeDrainPendingRequest) (*agentrewire.RuntimeDrainPendingResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.SessionId, req.PeerFingerprint)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		result, err := rh.DrainPending(ctx, remotewire.DrainParams{SessionID: req.SessionId, PeerFingerprint: req.PeerFingerprint})
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
		ticket, err := d.claimProtobuf(ctx, req.SessionId, req.PeerFingerprint)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		result, err := rh.Abort(ctx, remotewire.AbortParams{SessionID: req.SessionId, PeerFingerprint: req.PeerFingerprint, TurnToken: req.TurnToken})
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return &agentrewire.RuntimeAbortResponse{TurnKind: string(result.TurnKind)}, nil
	})
	registerEmptyControl(reg, agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STOP_BACKGROUND_TASK, func() *agentrewire.RuntimeStopBackgroundTaskRequest {
		return &agentrewire.RuntimeStopBackgroundTaskRequest{}
	}, func(ctx context.Context, req *agentrewire.RuntimeStopBackgroundTaskRequest) error {
		_, err := rh.StopBackgroundTask(ctx, remotewire.StopBackgroundTaskParams{SessionID: req.SessionId, PeerFingerprint: req.PeerFingerprint, TaskID: req.TaskId})
		return err
	}, d, guard)
	registerEmptyControl(reg, agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE, func() *agentrewire.RuntimeSetPermissionModeRequest {
		return &agentrewire.RuntimeSetPermissionModeRequest{}
	}, func(ctx context.Context, req *agentrewire.RuntimeSetPermissionModeRequest) error {
		_, err := rh.SetPermissionMode(ctx, remotewire.SetPermissionModeParams{SessionID: req.SessionId, PeerFingerprint: req.PeerFingerprint, Mode: req.Mode})
		return err
	}, d, guard)
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER), func() *agentrewire.RuntimeSubmitAnswerRequest { return &agentrewire.RuntimeSubmitAnswerRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeSubmitAnswerRequest) (*agentrewire.PeerSessionControlResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.SessionId, req.PeerFingerprint)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		questions := make([]agentruntime.AskQuestion, 0, len(req.Questions))
		for _, question := range req.Questions {
			questions = append(questions, askQuestionFromProto(question))
		}
		answers := make([]agentruntime.AskAnswer, 0, len(req.Answers))
		for _, answer := range req.Answers {
			answers = append(answers, agentruntime.AskAnswer{QuestionIndex: int(answer.QuestionIndex), Labels: append([]string(nil), answer.Labels...), OtherText: answer.OtherText})
		}
		_, err = rh.SubmitAnswer(ctx, remotewire.SubmitAnswerParams{SessionID: req.SessionId, PeerFingerprint: req.PeerFingerprint, RequestID: req.RequestId, Questions: questions, Answers: answers, Skipped: req.Skipped})
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return &agentrewire.PeerSessionControlResponse{}, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION), func() *agentrewire.RuntimeSubmitToolPermissionRequest {
		return &agentrewire.RuntimeSubmitToolPermissionRequest{}
	}, func(ctx context.Context, req *agentrewire.RuntimeSubmitToolPermissionRequest) (*agentrewire.PeerSessionControlResponse, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.SessionId, req.PeerFingerprint)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		_, err = rh.SubmitToolPermission(ctx, remotewire.SubmitToolPermissionParams{SessionID: req.SessionId, PeerFingerprint: req.PeerFingerprint, RequestID: req.RequestId, Allow: req.Allow, AlwaysAllowSession: req.AlwaysAllowSession, DenyReason: req.DenyReason})
		if err != nil {
			d.conns.undoClaim(ticket)
			return nil, protobufRuntimeError(err)
		}
		return &agentrewire.PeerSessionControlResponse{}, nil
	})
	registerGoal := func(method agentrewire.RpcMethod, handler func(context.Context, remotewire.GoalParams) (remotewire.GoalResult, error)) {
		protorpc.RegisterMethod(reg, uint32(method), func() *agentrewire.RuntimeGoalRequest { return &agentrewire.RuntimeGoalRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeGoalRequest) (*agentrewire.RuntimeGoalResponse, error) {
			if err := guard(ctx); err != nil {
				return nil, err
			}
			params, err := protowire.GoalRequestFromProto(req)
			if err != nil {
				return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: err.Error()}
			}
			ticket, err := d.claimProtobuf(ctx, params.SessionID, params.PeerFingerprint)
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
		ticket, err := d.claimProtobuf(ctx, params.SessionID, params.PeerFingerprint)
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
	d.registerProtobufAttach(reg, conn, rh)
}

func registerEmptyControl[Req interface {
	proto.Message
	GetSessionId() int64
	GetPeerFingerprint() string
}](reg *protorpc.Registry, method agentrewire.RpcMethod, factory func() Req, handler func(context.Context, Req) error, d *Daemon, guard func(context.Context) error) {
	protorpc.RegisterMethod(reg, uint32(method), factory, func(ctx context.Context, req Req) (*agentrewire.Empty, error) {
		if err := guard(ctx); err != nil {
			return nil, err
		}
		ticket, err := d.claimProtobuf(ctx, req.GetSessionId(), req.GetPeerFingerprint())
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

func (d *Daemon) registerProtobufAttach(reg *protorpc.Registry, conn *protorpc.Conn, rh *handlers.RuntimeHandlers) {
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH), func() *agentrewire.SessionAttachRequest { return &agentrewire.SessionAttachRequest{} }, func(ctx context.Context, req *agentrewire.SessionAttachRequest) (*agentrewire.SessionAttachResponse, error) {
		if err := requireProtobufAuth(ctx); err != nil {
			return nil, err
		}
		peer, err := handlers.ResolveSessionPeer(ctx, req.PeerFingerprint, d.claimedAccountID)
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		result, err := d.catchup.Attach(ctx, remotewire.SessionAttachParams{SessionID: req.SessionId, PeerFingerprint: req.PeerFingerprint})
		if err != nil {
			return nil, protobufRuntimeError(err)
		}
		rh.AdoptForPeer(peer, result.SessionID, agent_backend_entity.BackendType(result.BackendType))
		d.conns.claimFor(conn, peer, result.SessionID)
		return &agentrewire.SessionAttachResponse{SessionId: result.SessionID, BackendType: result.BackendType, LifecycleState: result.LifecycleState, LatestSeq: result.LatestSeq}, nil
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

func askQuestionFromProto(question *agentrewire.AskQuestion) agentruntime.AskQuestion {
	result := agentruntime.AskQuestion{ID: question.Id, Question: question.Question, Header: question.Header, MultiSelect: question.MultiSelect, IsOther: question.IsOther, IsSecret: question.IsSecret}
	for _, option := range question.Options {
		result.Options = append(result.Options, agentruntime.AskOption{Label: option.Label, Description: option.Description, Preview: option.Preview})
	}
	return result
}
