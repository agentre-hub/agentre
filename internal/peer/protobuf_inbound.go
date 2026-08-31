package peer

import (
	"context"
	"errors"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/protobufadapter"
	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

type ProtobufInboundDeps struct {
	Peripheral   protobufadapter.PeripheralDeps
	Capabilities func(context.Context, string) (*agentrewire.RuntimeCapabilitiesResponse, error)
	ListSessions func(ctx context.Context, keyword string) (*remotewire.SessionListResult, error)
	// ActivityRollup 交出按天 × 维度的会话计数。回包里没有标题、路径与内容。
	ActivityRollup       func(context.Context, string, string) ([]activityrollup.Bucket, error)
	AttachSession        func(context.Context, remotewire.SessionAttachParams, chat_svc.PeerSessionSubscriber) (remotewire.SessionAttachResult, error)
	PullSession          func(context.Context, remotewire.SessionPullParams, chat_svc.PeerSessionSubscriber) (remotewire.SessionPullResult, error)
	PendingWaiters       func(context.Context, remotewire.SessionPendingWaitersParams) (remotewire.SessionPendingWaitersResult, error)
	DeleteSession        func(context.Context, string, string) error
	SetModelTarget       func(context.Context, string, string, string) error
	SetPermissionMode    func(context.Context, string, string) error
	RunSession           func(context.Context, remotewire.RunParams, chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error)
	SteerSession         func(context.Context, remotewire.SteerParams, chat_svc.PeerSessionSource) error
	SubmitAnswer         func(context.Context, remotewire.SubmitAnswerParams) (chat_svc.PeerSessionControlResult, error)
	SubmitToolPermission func(context.Context, remotewire.SubmitToolPermissionParams) (chat_svc.PeerSessionControlResult, error)
}

type protobufPeerSubscriber struct{ conn *protorpc.Conn }

func (s protobufPeerSubscriber) Notify(method string, params any) error {
	notification, err := protowire.WireNotificationToProto(method, params)
	if err != nil {
		return err
	}
	return s.conn.Notify(notification)
}
func (s protobufPeerSubscriber) Done() <-chan struct{} { return s.conn.Done() }
func (s protobufPeerSubscriber) PeerSessionSubscriberKey() string {
	return fmt.Sprintf("protobuf-conn:%p", s.conn)
}

func protobufPeerError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, chat_svc.ErrPeerSessionNotFound) {
		return &protorpc.Error{Code: -32002, Message: err.Error()}
	}
	var protobufErr *protorpc.Error
	if errors.As(err, &protobufErr) {
		return protobufErr
	}
	var rpcErr *rpcerror.Error
	if errors.As(err, &rpcErr) {
		return protobufadapter.ConvertError(rpcErr)
	}
	return &protorpc.Error{Code: protorpc.CodeInternal, Message: err.Error()}
}

func NewProtobufInboundRegistry(deps ProtobufInboundDeps) *protorpc.Registry {
	registry := protorpc.NewRegistry()
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_AUTH_ACCOUNT),
		func() *agentrewire.AuthAccountRequest { return &agentrewire.AuthAccountRequest{} },
		func(ctx context.Context, request *agentrewire.AuthAccountRequest) (*agentrewire.AuthAccountResponse, error) {
			// The peer-to-peer handshake carries the same protocol version as
			// the daemon's, and gates on it first for the same reason: two
			// desktops on different revisions must say so, not fail as
			// "unauthorized".
			if reason := wireversion.Reject(request.ProtocolVersion, request.MinSupportedProtocolVersion); reason != "" {
				logger.Ctx(ctx).Warn("peer.authAccount: rejected handshake",
					zap.String("peerProtocolVersion", request.ProtocolVersion),
					zap.String("peerMinSupportedProtocolVersion", request.MinSupportedProtocolVersion),
					zap.String("localProtocolVersion", wireversion.Protocol),
					zap.String("localMinSupportedProtocolVersion", wireversion.MinSupported))
				return nil, &protorpc.Error{Code: rpcerror.CodeProtocolVersion, Message: reason}
			}
			if request.Credential == "" || request.DeviceFingerprint == "" {
				return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "credential and device fingerprint required"}
			}
			conn := protorpc.ConnFromContext(ctx)
			if conn == nil {
				return nil, &protorpc.Error{Code: -32001, Message: "unauthorized"}
			}
			conn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: request.DeviceFingerprint})
			return &agentrewire.AuthAccountResponse{Ok: true, ProtocolVersion: wireversion.Protocol, MinSupportedProtocolVersion: wireversion.MinSupported}, nil
		})
	protobufadapter.RegisterPeripheralMethods(registry, deps.Peripheral)
	if deps.Capabilities != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES), func() *agentrewire.RuntimeCapabilitiesRequest { return &agentrewire.RuntimeCapabilitiesRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, request *agentrewire.RuntimeCapabilitiesRequest) (*agentrewire.RuntimeCapabilitiesResponse, error) {
			return deps.Capabilities(ctx, request.BackendType)
		}))
	}
	registerPeerSessionMethods(registry, deps)
	return registry
}

func registerPeerSessionMethods(registry *protorpc.Registry, deps ProtobufInboundDeps) {
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS), func() *agentrewire.SessionPendingWaitersRequest { return &agentrewire.SessionPendingWaitersRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.SessionPendingWaitersRequest) (*agentrewire.SessionPendingWaitersResponse, error) {
		if deps.PendingWaiters == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "pending waiters unavailable"}
		}
		value, err := deps.PendingWaiters(ctx, remotewire.SessionPendingWaitersParams{ConversationID: req.ConversationId, PeerFingerprint: req.PeerFingerprint})
		if err != nil {
			return nil, protobufPeerError(err)
		}
		return protowire.PendingWaitersResponseToProto(value), nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE), func() *agentrewire.SessionDeleteRequest { return &agentrewire.SessionDeleteRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
		if err := conversationid.Validate(req.ConversationId); err != nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "invalid conversation id"}
		}
		if deps.DeleteSession == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "delete unavailable"}
		}
		if err := deps.DeleteSession(ctx, req.ConversationId, req.PeerFingerprint); err != nil {
			return nil, protobufPeerError(err)
		}
		return &agentrewire.SessionDeleteResponse{Deleted: true}, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET), func() *agentrewire.SetModelTargetRequest { return &agentrewire.SetModelTargetRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.SetModelTargetRequest) (*agentrewire.SetModelTargetResponse, error) {
		if deps.SetModelTarget == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "model target unavailable"}
		}
		if err := deps.SetModelTarget(ctx, req.ConversationId, req.ProviderKey, req.ModelKey); err != nil {
			return nil, protobufPeerError(err)
		}
		return &agentrewire.SetModelTargetResponse{}, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE), func() *agentrewire.RuntimeSetPermissionModeRequest {
		return &agentrewire.RuntimeSetPermissionModeRequest{}
	}, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.RuntimeSetPermissionModeRequest) (*agentrewire.Empty, error) {
		if deps.SetPermissionMode == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "permission mode unavailable"}
		}
		if err := deps.SetPermissionMode(ctx, req.ConversationId, req.Mode); err != nil {
			return nil, protobufPeerError(err)
		}
		return &agentrewire.Empty{}, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST), func() *agentrewire.SessionListRequest { return &agentrewire.SessionListRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.SessionListRequest) (*agentrewire.SessionListResponse, error) {
		if deps.ListSessions == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "session list unavailable"}
		}
		value, err := deps.ListSessions(ctx, req.GetKeyword())
		if err != nil {
			return nil, protobufPeerError(err)
		}
		out := &agentrewire.SessionListResponse{}
		for _, s := range value.Sessions {
			out.Sessions = append(out.Sessions, &agentrewire.SessionSummary{ConversationId: s.ConversationID, PeerFingerprint: s.PeerFingerprint, AgentId: s.AgentID, Title: s.Title, AgentSyncId: s.AgentSyncID, ProviderSessionId: s.ProviderSessionID, Cwd: s.Cwd, ProjectSyncId: s.ProjectSyncID, BackendType: s.BackendType, LifecycleState: s.LifecycleState, WaitingForInput: s.WaitingForInput, LatestSeq: s.LatestSeq, LastMessageAt: s.LastMessageAt, ProviderKey: s.ProviderKey, ModelKey: s.ModelKey})
		}
		return out, nil
	}))
	// 活跃统计的纯计数上报,与 agentred 同一个 RPC:服务端不必分辨对面是哪一种端。
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_ACTIVITY_ROLLUP), func() *agentrewire.ActivityRollupRequest { return &agentrewire.ActivityRollupRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.ActivityRollupRequest) (*agentrewire.ActivityRollupResponse, error) {
		if deps.ActivityRollup == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "activity rollup unavailable"}
		}
		buckets, err := deps.ActivityRollup(ctx, req.GetSinceDay(), req.GetTimeZone())
		if err != nil {
			return nil, protobufPeerError(err)
		}
		out := &agentrewire.ActivityRollupResponse{Buckets: make([]*agentrewire.ActivityDailyBucket, 0, len(buckets))}
		for _, b := range buckets {
			out.Buckets = append(out.Buckets, &agentrewire.ActivityDailyBucket{Day: b.Day, AgentSyncId: b.AgentSyncID, BackendType: b.BackendType, ProviderKey: b.ProviderKey, ModelKey: b.ModelKey, ProjectSyncId: b.ProjectSyncID, SessionCount: b.SessionCount})
		}
		return out, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH), func() *agentrewire.SessionAttachRequest { return &agentrewire.SessionAttachRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.SessionAttachRequest) (*agentrewire.SessionAttachResponse, error) {
		if err := conversationid.Validate(req.ConversationId); err != nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "invalid conversation id"}
		}
		conn := protorpc.ConnFromContext(ctx)
		if deps.AttachSession == nil || conn == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "session attach unavailable"}
		}
		v, err := deps.AttachSession(ctx, remotewire.SessionAttachParams{ConversationID: req.ConversationId, PeerFingerprint: req.PeerFingerprint}, protobufPeerSubscriber{conn})
		if err != nil {
			return nil, protobufPeerError(err)
		}
		return &agentrewire.SessionAttachResponse{ConversationId: v.ConversationID, BackendType: v.BackendType, LifecycleState: v.LifecycleState, LatestSeq: v.LatestSeq}, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL), func() *agentrewire.SessionPullRequest { return &agentrewire.SessionPullRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.SessionPullRequest) (*agentrewire.SessionPullResponse, error) {
		conn := protorpc.ConnFromContext(ctx)
		if deps.PullSession == nil || conn == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "session pull unavailable"}
		}
		v, err := deps.PullSession(ctx, remotewire.SessionPullParams{ConversationID: req.ConversationId, PeerFingerprint: req.PeerFingerprint, Cursor: req.Cursor, Limit: int(req.Limit)}, protobufPeerSubscriber{conn})
		if err != nil {
			return nil, protobufPeerError(err)
		}
		out := &agentrewire.SessionPullResponse{Cursor: v.Cursor, HasMore: v.HasMore, OldestSeq: v.OldestSeq}
		for _, e := range v.Notifications {
			notification, x := protowire.WireNotificationToProto(e.Method, e.Params)
			if x != nil {
				return nil, protobufPeerError(x)
			}
			protowire.SetNotificationSeq(notification, e.Seq)
			out.Notifications = append(out.Notifications, &agentrewire.JournaledNotification{Seq: e.Seq, Payload: notification})
		}
		return out, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
		p, err := protowire.RunRequestFromProto(req)
		if err != nil {
			return nil, protobufPeerError(err)
		}
		if deps.RunSession == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "run unavailable"}
		}
		if _, err := deps.RunSession(ctx, p, chat_svc.PeerSessionSource{Device: protorpc.ConnFromContext(ctx).Auth().DeviceFingerprint, Name: p.SourceDeviceName}); err != nil {
			return nil, protobufPeerError(err)
		}
		// 交回调用方送来的那条对话身份:本机可能是**新建**了一行来承载它(R17),
		// 但对话的身份仍是发起端铸的那一个 —— daemon / 桌面端都从不发号。
		return &agentrewire.RuntimeRunResponse{ConversationId: p.ConversationID}, nil
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER), func() *agentrewire.RuntimeSteerRequest { return &agentrewire.RuntimeSteerRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.RuntimeSteerRequest) (*agentrewire.Empty, error) {
		if deps.SteerSession == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "steer unavailable"}
		}
		err := deps.SteerSession(ctx, remotewire.SteerParams{ConversationID: req.ConversationId, PeerFingerprint: req.PeerFingerprint, QueuedID: req.QueuedId, Text: req.Text}, chat_svc.PeerSessionSource{Device: protorpc.ConnFromContext(ctx).Auth().DeviceFingerprint})
		return &agentrewire.Empty{}, protobufPeerError(err)
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER), func() *agentrewire.RuntimeSubmitAnswerRequest { return &agentrewire.RuntimeSubmitAnswerRequest{} }, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.RuntimeSubmitAnswerRequest) (*agentrewire.PeerSessionControlResponse, error) {
		if deps.SubmitAnswer == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "answer unavailable"}
		}
		answers := make([]agentruntime.AskAnswer, 0, len(req.Answers))
		for _, a := range req.Answers {
			answers = append(answers, agentruntime.AskAnswer{QuestionIndex: int(a.QuestionIndex), Labels: a.Labels, OtherText: a.OtherText})
		}
		v, err := deps.SubmitAnswer(ctx, remotewire.SubmitAnswerParams{ConversationID: req.ConversationId, PeerFingerprint: req.PeerFingerprint, RequestID: req.RequestId, Answers: answers, Skipped: req.Skipped})
		return &agentrewire.PeerSessionControlResponse{AlreadyHandled: v.AlreadyHandled}, protobufPeerError(err)
	}))
	protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION), func() *agentrewire.RuntimeSubmitToolPermissionRequest {
		return &agentrewire.RuntimeSubmitToolPermissionRequest{}
	}, protobufadapter.Authenticated(func(ctx context.Context, req *agentrewire.RuntimeSubmitToolPermissionRequest) (*agentrewire.PeerSessionControlResponse, error) {
		if deps.SubmitToolPermission == nil {
			return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "permission unavailable"}
		}
		v, err := deps.SubmitToolPermission(ctx, remotewire.SubmitToolPermissionParams{ConversationID: req.ConversationId, PeerFingerprint: req.PeerFingerprint, RequestID: req.RequestId, Allow: req.Allow, AlwaysAllowSession: req.AlwaysAllowSession, DenyReason: req.DenyReason})
		return &agentrewire.PeerSessionControlResponse{AlreadyHandled: v.AlreadyHandled}, protobufPeerError(err)
	}))
}
