// Package protorpctest provides an in-memory typed RPC bridge for tests that
// still script the process-local DaemonClientPort boundary. Production code
// never imports this package.
package protorpctest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type testPipe struct {
	in, out chan []byte
	done    chan struct{}
	once    *sync.Once
}

func testPipePair() (*testPipe, *testPipe) {
	a, b := make(chan []byte, 128), make(chan []byte, 128)
	done := make(chan struct{})
	once := &sync.Once{}
	return &testPipe{a, b, done, once}, &testPipe{b, a, done, once}
}
func (p *testPipe) ReadFrame() ([]byte, error) {
	select {
	case b := <-p.in:
		return b, nil
	case <-p.done:
		return nil, io.EOF
	}
}
func (p *testPipe) WriteFrame(b []byte) error {
	select {
	case p.out <- append([]byte(nil), b...):
		return nil
	case <-p.done:
		return io.EOF
	}
}
func (p *testPipe) Close() error          { p.once.Do(func() { close(p.done) }); return nil }
func (p *testPipe) Done() <-chan struct{} { return p.done }

type legacyTestPort interface{ agentruntime.DaemonClientPort }

const barrierMethodID uint32 = 0xffff_ff00

type protobufTestConnection struct {
	conn   *protorpc.Conn
	legacy legacyTestPort
}

func testRPCError(err error) error {
	var value *rpcerror.Error
	if errors.As(err, &value) {
		return &protorpc.Error{Code: int32(value.Code), Message: value.Message}
	}
	return err
}

func (c *protobufTestConnection) Conn() *protorpc.Conn    { return c.conn }
func (c *protobufTestConnection) Closed() <-chan struct{} { return c.legacy.Closed() }
func (c *protobufTestConnection) Close() error            { _ = c.conn.Close(); return c.legacy.Close() }

// WrapConnection exposes a legacy test port through the canonical typed
// Protobuf connection, so service tests exercise the real encoding boundary.
func WrapConnection(legacy legacyTestPort) *protobufTestConnection {
	clientPipe, serverPipe := testPipePair()
	registry := protorpc.NewRegistry()
	registerLegacyTestMethods(registry, legacy)
	protorpc.RegisterMethod(registry, barrierMethodID, func() *agentrewire.Empty { return &agentrewire.Empty{} }, func(context.Context, *agentrewire.Empty) (*agentrewire.Empty, error) {
		return &agentrewire.Empty{}, nil
	})
	clientConn := protorpc.NewConn(clientPipe, protorpc.NewRegistry())
	serverConn := protorpc.NewConn(serverPipe, registry)
	for _, method := range []string{wire.NotifyEvent, wire.NotifyRunResultDone, wire.NotifyAutonomousTurnStarted, wire.NotifyAutonomousTurnEvent, wire.NotifyAutonomousTurnDone} {
		name := method
		legacy.Handle(name, func(_ context.Context, raw json.RawMessage) (any, error) {
			value, err := wire.DecodeNotificationParams(name, raw)
			if err != nil {
				return nil, err
			}
			if value == nil {
				return nil, fmt.Errorf("unknown notification %q", name)
			}
			notification, err := protowire.WireNotificationToProto(name, value)
			if err != nil {
				return nil, err
			}
			return nil, serverConn.Notify(notification)
		})
	}
	go clientConn.Serve(context.Background())
	go serverConn.Serve(context.Background())
	return &protobufTestConnection{conn: clientConn, legacy: legacy}
}

// Barrier waits until the client has consumed every frame written before this
// request. It gives asynchronous typed notification tests a real transport
// happens-before boundary without sleeps or weakened assertions.
func Barrier(ctx context.Context, conn interface{ Conn() *protorpc.Conn }) error {
	_, err := protorpc.CallMethod(ctx, conn.Conn(), barrierMethodID, &agentrewire.Empty{}, func() *agentrewire.Empty { return &agentrewire.Empty{} })
	return err
}

func registerLegacyTestMethods(reg *protorpc.Registry, legacy legacyTestPort) {
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST), func() *agentrewire.SessionListRequest { return &agentrewire.SessionListRequest{} }, func(ctx context.Context, _ *agentrewire.SessionListRequest) (*agentrewire.SessionListResponse, error) {
		var out wire.SessionListResult
		if err := legacy.Call(ctx, wire.MethodSessionList, struct{}{}, &out); err != nil {
			return nil, testRPCError(err)
		}
		response := &agentrewire.SessionListResponse{}
		for _, s := range out.Sessions {
			response.Sessions = append(response.Sessions, &agentrewire.SessionSummary{SessionId: s.SessionID, PeerFingerprint: s.PeerFingerprint, AgentId: s.AgentID, Title: s.Title, AgentSyncId: s.AgentSyncID, ProviderSessionId: s.ProviderSessionID, Cwd: s.Cwd, ProjectSyncId: s.ProjectSyncID, BackendType: s.BackendType, LifecycleState: s.LifecycleState, WaitingForInput: s.WaitingForInput, LatestSeq: s.LatestSeq, LastMessageAt: s.LastMessageAt, ProviderKey: s.ProviderKey, ModelKey: s.ModelKey})
		}
		return response, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH), func() *agentrewire.SessionAttachRequest { return &agentrewire.SessionAttachRequest{} }, func(ctx context.Context, req *agentrewire.SessionAttachRequest) (*agentrewire.SessionAttachResponse, error) {
		var out wire.SessionAttachResult
		if err := legacy.Call(ctx, wire.MethodSessionAttach, wire.SessionAttachParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint()}, &out); err != nil {
			return nil, err
		}
		return &agentrewire.SessionAttachResponse{SessionId: out.SessionID, BackendType: out.BackendType, LifecycleState: out.LifecycleState, LatestSeq: out.LatestSeq}, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL), func() *agentrewire.SessionPullRequest { return &agentrewire.SessionPullRequest{} }, func(ctx context.Context, req *agentrewire.SessionPullRequest) (*agentrewire.SessionPullResponse, error) {
		var out wire.SessionPullResult
		if err := legacy.Call(ctx, wire.MethodSessionPull, wire.SessionPullParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint(), Cursor: req.GetCursor(), Limit: int(req.GetLimit())}, &out); err != nil {
			return nil, err
		}
		response := &agentrewire.SessionPullResponse{Cursor: out.Cursor, HasMore: out.HasMore, OldestSeq: out.OldestSeq}
		for _, n := range out.Notifications {
			// 认不出的 method / 建不出帧的载荷:交一条空 payload,让客户端走它自己
			// 那条「这条通知我映射不出来 → 跳掉这一格游标」的路径。这里替真 daemon
			// 模拟的正是「新版 daemon 发来第六类通知」,所以不能整页失败。
			decoded, err := protowire.WireNotificationToProto(n.Method, n.Params)
			if n.Params == nil || err != nil {
				response.Notifications = append(response.Notifications, &agentrewire.JournaledNotification{
					Seq: n.Seq, Payload: &agentrewire.RpcNotification{},
				})
				continue
			}
			protowire.SetNotificationSeq(decoded, n.Seq)
			response.Notifications = append(response.Notifications, &agentrewire.JournaledNotification{Seq: n.Seq, Payload: decoded})
		}
		return response, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS), func() *agentrewire.SessionPendingWaitersRequest { return &agentrewire.SessionPendingWaitersRequest{} }, func(ctx context.Context, req *agentrewire.SessionPendingWaitersRequest) (*agentrewire.SessionPendingWaitersResponse, error) {
		var out wire.SessionPendingWaitersResult
		if err := legacy.Call(ctx, wire.MethodSessionPendingWaiters, wire.SessionPendingWaitersParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint()}, &out); err != nil {
			return nil, err
		}
		return protowire.PendingWaitersResponseToProto(out), nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
		p, e := protowire.RunRequestFromProto(req)
		if e != nil {
			return nil, e
		}
		var out wire.RunAck
		if e = legacy.Call(ctx, wire.MethodRun, p, &out); e != nil {
			return nil, e
		}
		return &agentrewire.RuntimeRunResponse{SessionId: out.SessionID, ProviderSessionId: out.ProviderSessionID, LaunchPermissionMode: out.LaunchPermissionMode, ProviderFallbackKey: out.ProviderFallbackKey}, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES), func() *agentrewire.RuntimeCapabilitiesRequest { return &agentrewire.RuntimeCapabilitiesRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeCapabilitiesRequest) (*agentrewire.RuntimeCapabilitiesResponse, error) {
		var out wire.CapabilitiesResult
		if e := legacy.Call(ctx, wire.MethodCapabilities, wire.CapabilitiesParams{BackendType: req.GetBackendType()}, &out); e != nil {
			return nil, e
		}
		response := &agentrewire.RuntimeCapabilitiesResponse{}
		for k, v := range out.Capabilities.Set {
			response.Capabilities = append(response.Capabilities, &agentrewire.CapabilityEntry{Name: string(k), Enabled: v})
		}
		m := out.Capabilities.PermissionModeMeta
		response.PermissionMode = &agentrewire.PermissionModeMeta{AllowedModes: m.AllowedModes, DefaultMode: m.DefaultMode, SwitchableDuringTurn: m.SwitchableDuringTurn, Order: m.Order, LaunchDefaultMode: m.LaunchDefaultMode}
		return response, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER), func() *agentrewire.RuntimeSteerRequest { return &agentrewire.RuntimeSteerRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeSteerRequest) (*agentrewire.Empty, error) {
		err := legacy.Call(ctx, wire.MethodSteer, wire.SteerParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint(), QueuedID: req.GetQueuedId(), Text: req.GetText()}, &wire.OK{})
		return &agentrewire.Empty{}, testRPCError(err)
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CANCEL_STEER), func() *agentrewire.RuntimeCancelSteerRequest { return &agentrewire.RuntimeCancelSteerRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeCancelSteerRequest) (*agentrewire.RuntimeCancelSteerResponse, error) {
		var out wire.CancelSteerResult
		if err := legacy.Call(ctx, wire.MethodCancelSteer, wire.CancelSteerParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint(), QueuedID: req.GetQueuedId()}, &out); err != nil {
			return nil, testRPCError(err)
		}
		return &agentrewire.RuntimeCancelSteerResponse{Removed: out.Removed}, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_DRAIN_PENDING), func() *agentrewire.RuntimeDrainPendingRequest { return &agentrewire.RuntimeDrainPendingRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeDrainPendingRequest) (*agentrewire.RuntimeDrainPendingResponse, error) {
		var out wire.DrainResult
		if err := legacy.Call(ctx, wire.MethodDrainPending, wire.DrainParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint()}, &out); err != nil {
			return nil, testRPCError(err)
		}
		response := &agentrewire.RuntimeDrainPendingResponse{}
		for _, steer := range out.Steers {
			response.Steers = append(response.Steers, &agentrewire.ConsumedSteer{QueuedId: steer.QueuedID, Text: steer.Text, SourcePeer: steer.SourcePeer, SourceName: steer.SourceName})
		}
		return response, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT), func() *agentrewire.RuntimeAbortRequest { return &agentrewire.RuntimeAbortRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeAbortRequest) (*agentrewire.RuntimeAbortResponse, error) {
		var out wire.AbortResult
		if err := legacy.Call(ctx, wire.MethodAbort, wire.AbortParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint(), TurnToken: req.GetTurnToken()}, &out); err != nil {
			return nil, testRPCError(err)
		}
		return &agentrewire.RuntimeAbortResponse{TurnKind: string(out.TurnKind)}, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STOP_BACKGROUND_TASK), func() *agentrewire.RuntimeStopBackgroundTaskRequest {
		return &agentrewire.RuntimeStopBackgroundTaskRequest{}
	}, func(ctx context.Context, req *agentrewire.RuntimeStopBackgroundTaskRequest) (*agentrewire.Empty, error) {
		err := legacy.Call(ctx, wire.MethodStopBackgroundTask, wire.StopBackgroundTaskParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint(), TaskID: req.GetTaskId()}, &wire.OK{})
		return &agentrewire.Empty{}, testRPCError(err)
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE), func() *agentrewire.RuntimeSetPermissionModeRequest {
		return &agentrewire.RuntimeSetPermissionModeRequest{}
	}, func(ctx context.Context, req *agentrewire.RuntimeSetPermissionModeRequest) (*agentrewire.Empty, error) {
		err := legacy.Call(ctx, wire.MethodSetPermissionMode, wire.SetPermissionModeParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint(), Mode: req.GetMode()}, &wire.OK{})
		return &agentrewire.Empty{}, testRPCError(err)
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER), func() *agentrewire.RuntimeSubmitAnswerRequest { return &agentrewire.RuntimeSubmitAnswerRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeSubmitAnswerRequest) (*agentrewire.PeerSessionControlResponse, error) {
		var out wire.PeerSessionControlResult
		params := wire.SubmitAnswerParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint(), RequestID: req.GetRequestId(), Skipped: req.GetSkipped()}
		for _, question := range req.GetQuestions() {
			value := agentruntime.AskQuestion{ID: question.GetId(), Question: question.GetQuestion(), Header: question.GetHeader(), MultiSelect: question.GetMultiSelect(), IsOther: question.GetIsOther(), IsSecret: question.GetIsSecret()}
			for _, option := range question.GetOptions() {
				value.Options = append(value.Options, agentruntime.AskOption{Label: option.GetLabel(), Description: option.GetDescription(), Preview: option.GetPreview()})
			}
			params.Questions = append(params.Questions, value)
		}
		for _, answer := range req.GetAnswers() {
			params.Answers = append(params.Answers, agentruntime.AskAnswer{QuestionIndex: int(answer.GetQuestionIndex()), Labels: append([]string(nil), answer.GetLabels()...), OtherText: answer.GetOtherText()})
		}
		if err := legacy.Call(ctx, wire.MethodSubmitAnswer, params, &out); err != nil {
			return nil, testRPCError(err)
		}
		return &agentrewire.PeerSessionControlResponse{AlreadyHandled: out.AlreadyHandled}, nil
	})
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION), func() *agentrewire.RuntimeSubmitToolPermissionRequest {
		return &agentrewire.RuntimeSubmitToolPermissionRequest{}
	}, func(ctx context.Context, req *agentrewire.RuntimeSubmitToolPermissionRequest) (*agentrewire.PeerSessionControlResponse, error) {
		var out wire.PeerSessionControlResult
		err := legacy.Call(ctx, wire.MethodSubmitToolPermission, wire.SubmitToolPermissionParams{SessionID: req.GetSessionId(), PeerFingerprint: req.GetPeerFingerprint(), RequestID: req.GetRequestId(), Allow: req.GetAllow(), AlwaysAllowSession: req.GetAlwaysAllowSession(), DenyReason: req.GetDenyReason()}, &out)
		if err != nil {
			return nil, testRPCError(err)
		}
		return &agentrewire.PeerSessionControlResponse{AlreadyHandled: out.AlreadyHandled}, nil
	})
	registerGoal := func(methodID agentrewire.RpcMethod, method string) {
		protorpc.RegisterMethod(reg, uint32(methodID), func() *agentrewire.RuntimeGoalRequest { return &agentrewire.RuntimeGoalRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeGoalRequest) (*agentrewire.RuntimeGoalResponse, error) {
			params, err := protowire.GoalRequestFromProto(req)
			if err != nil {
				return nil, err
			}
			var out wire.GoalResult
			if err = legacy.Call(ctx, method, params, &out); err != nil {
				return nil, testRPCError(err)
			}
			return &agentrewire.RuntimeGoalResponse{Goal: testGoalToProto(out.Goal)}, nil
		})
	}
	registerGoal(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_GET, wire.MethodGetGoal)
	registerGoal(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_SET, wire.MethodSetGoal)
	protorpc.RegisterMethod(reg, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_GOAL_CLEAR), func() *agentrewire.RuntimeGoalRequest { return &agentrewire.RuntimeGoalRequest{} }, func(ctx context.Context, req *agentrewire.RuntimeGoalRequest) (*agentrewire.RuntimeGoalClearResponse, error) {
		params, err := protowire.GoalRequestFromProto(req)
		if err != nil {
			return nil, err
		}
		var out wire.GoalClearResult
		if err = legacy.Call(ctx, wire.MethodClearGoal, params, &out); err != nil {
			return nil, testRPCError(err)
		}
		return &agentrewire.RuntimeGoalClearResponse{Cleared: out.Cleared}, nil
	})
}

func testGoalToProto(goal *agentruntime.Goal) *agentrewire.Goal {
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
