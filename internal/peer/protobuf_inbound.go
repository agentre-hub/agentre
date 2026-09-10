package peer

import (
	"context"
	"errors"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
	"github.com/agentre-hub/agentre/internal/pkg/wireinbound"
	"github.com/agentre-hub/agentre/internal/pkg/wireversion"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

type ProtobufInboundDeps struct {
	Peripheral wireinbound.PeripheralDeps
	// Capabilities 交出这台机器上某个 backend 的能力矩阵。它交的是**领域值**而不是
	// 线上的应答:折成线格式那一步与 agentred 逐字相同,已经收进 wireinbound。
	Capabilities func(context.Context, remotewire.CapabilitiesParams) (remotewire.CapabilitiesResult, error)
	ListSessions func(ctx context.Context, params remotewire.SessionListParams) (*remotewire.SessionListResult, error)
	// CountSessions 交出三个数(一共 / 在跑 / 在等你)。它与 ListSessions 分开,是因为
	// 调用方(设备卡片)要的从来不是清单 —— 拿清单去数,就得先把整台机器搬过线。
	CountSessions func(ctx context.Context) (*remotewire.SessionCountsResult, error)
	// ActivityRollup 交出按天 × 维度的会话计数。回包里没有标题、路径与内容。
	ActivityRollup func(context.Context, string, string) ([]activityrollup.Bucket, error)
	// VerifyAccountCredential 验证入站对端出示的账号凭据,交出凭据里那个**已验签的**
	// 对端身份(pfp claim,决策 8)。生产装配是 newAccountCredentialVerifier。
	//
	// 它是 nil 表示本进程此刻没有验证能力(未登录、公钥取不到、单测未装配):那时
	// 握手一律拒绝。曾经这条路上凭据只需非空、指纹只需自报,任何人都能自称任何对端。
	VerifyAccountCredential func(ctx context.Context, credential string) (string, error)
	AttachSession           func(context.Context, remotewire.SessionAttachParams, chat_svc.PeerSessionSubscriber) (remotewire.SessionAttachResult, error)
	PullSession             func(context.Context, remotewire.SessionPullParams, chat_svc.PeerSessionSubscriber) (remotewire.SessionPullResult, error)
	PendingWaiters          func(context.Context, remotewire.SessionPendingWaitersParams) (remotewire.SessionPendingWaitersResult, error)
	DeleteSession           func(context.Context, string, string) error
	SetModelTarget          func(context.Context, string, string, string) error
	// SetReasoningEffort 把浏览器选的会话思考力度转调进桌面端的 chat_svc,与
	// SetModelTarget 同族:空串是要写下去的值(改回跟随后端配置),不是「不改」。
	SetReasoningEffort func(context.Context, string, string) error
	SetPermissionMode  func(context.Context, string, string) error
	RunSession         func(context.Context, remotewire.RunParams, chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error)
	// SteerSession 入队一条插话,并把 chat_svc 认的那个 queuedID 与「撤不撤得掉」
	// 交回去 —— 调用方拿自己造的号对不上 SteerConsumed(入队侧另造了一个)。
	SteerSession func(context.Context, remotewire.SteerParams, chat_svc.PeerSessionSource) (*chat_svc.EnqueueResponse, error)
	// CancelSteerSession 撤回还没被取走的排队消息(空 queuedID = 清空整条队列)。
	// 与 SteerSession 成对:少了它,浏览器上那颗撤回键只会撞 method not found。
	CancelSteerSession   func(context.Context, remotewire.CancelSteerParams) (*chat_svc.CancelQueuedResponse, error)
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
		return wireinbound.ConvertError(rpcErr)
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
			if request.Credential == "" {
				return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "credential required"}
			}
			conn := protorpc.ConnFromContext(ctx)
			if conn == nil {
				return nil, &protorpc.Error{Code: -32001, Message: "unauthorized"}
			}
			// 决策 8:先验凭据,再从验过的凭据里取身份。没有验证能力就没有握手 ——
			// 采信一个没验过的字符串,和不鉴权是同一件事。
			if deps.VerifyAccountCredential == nil {
				logger.Ctx(ctx).Warn("peer.authAccount: no credential verifier configured, refusing handshake")
				return nil, &protorpc.Error{Code: -32001, Message: "unauthorized"}
			}
			fingerprint, err := deps.VerifyAccountCredential(ctx, request.Credential)
			if err != nil || fingerprint == "" {
				logger.Ctx(ctx).Warn("peer.authAccount: credential rejected", zap.Error(err))
				return nil, &protorpc.Error{Code: -32001, Message: "unauthorized"}
			}
			conn.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: fingerprint})
			// 回写对端认定的身份:调用方在请求体里已经报不了自己是谁,它在这条连接上
			// 的身份(conversation_id 的派生输入)只能由这里说了算。
			return &agentrewire.AuthAccountResponse{
				Ok: true, PeerFingerprint: fingerprint,
				ProtocolVersion: wireversion.Protocol, MinSupportedProtocolVersion: wireversion.MinSupported,
			}, nil
		})
	wireinbound.RegisterPeripheralMethods(registry, deps.Peripheral)
	wireinbound.RegisterSessionMethods(registry, peerSessionPorts(deps))
	return registry
}

// peerSessionPorts 把桌面端的这一份依赖装配成会话族的端口。
//
// 这里只剩**端口实现**:归属校验(requireOwnOrigin 在 composition 那一层)、桌面端
// 独有的对话 id 前置校验、以及把实时流推回这条连接的订阅者。线形状本身(解请求 →
// 编应答)住在 internal/pkg/wireinbound,两种执行端共用同一份。
//
// 某一格的依赖缺席时对应端口留 nil,那个方法于是**不注册** —— 调用方收到
// method not found,而不是一个「本机没装这个能力」的 -32603。协议里「这台机器办不到」
// 只有前一种说法,调用方据它换一台机器。
func peerSessionPorts(deps ProtobufInboundDeps) wireinbound.SessionPorts {
	ports := wireinbound.SessionPorts{
		Auth:  wireinbound.RequireAuthenticated,
		Error: protobufPeerError,
		// 桌面端把「请求体解不开」并进了同一个映射,因而答 -32603 internal;
		// agentred 那一侧答 -32602 invalid params。差异既存,这里如实保留。
		DecodeError:    protobufPeerError,
		Capabilities:   deps.Capabilities,
		PendingWaiters: deps.PendingWaiters,
		ActivityRollup: deps.ActivityRollup,
	}
	if deps.ListSessions != nil {
		ports.List = func(ctx context.Context, params remotewire.SessionListParams) (remotewire.SessionListResult, error) {
			value, err := deps.ListSessions(ctx, params)
			if err != nil {
				return remotewire.SessionListResult{}, err
			}
			return *value, nil
		}
	}
	if deps.CountSessions != nil {
		ports.Counts = func(ctx context.Context) (remotewire.SessionCountsResult, error) {
			value, err := deps.CountSessions(ctx)
			if err != nil {
				return remotewire.SessionCountsResult{}, err
			}
			return *value, nil
		}
	}
	if deps.AttachSession != nil {
		ports.Attach = func(ctx context.Context, params remotewire.SessionAttachParams) (remotewire.SessionAttachResult, error) {
			if err := conversationid.Validate(params.ConversationID); err != nil {
				return remotewire.SessionAttachResult{}, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "invalid conversation id"}
			}
			conn := protorpc.ConnFromContext(ctx)
			if conn == nil {
				return remotewire.SessionAttachResult{}, &protorpc.Error{Code: protorpc.CodeInternal, Message: "session attach unavailable"}
			}
			return deps.AttachSession(ctx, params, protobufPeerSubscriber{conn})
		}
	}
	if deps.PullSession != nil {
		ports.Pull = func(ctx context.Context, params remotewire.SessionPullParams) (remotewire.SessionPullResult, error) {
			conn := protorpc.ConnFromContext(ctx)
			if conn == nil {
				return remotewire.SessionPullResult{}, &protorpc.Error{Code: protorpc.CodeInternal, Message: "session pull unavailable"}
			}
			return deps.PullSession(ctx, params, protobufPeerSubscriber{conn})
		}
	}
	if deps.DeleteSession != nil {
		ports.Delete = func(ctx context.Context, params remotewire.SessionDeleteParams) (remotewire.SessionDeleteResult, error) {
			if err := conversationid.Validate(params.ConversationID); err != nil {
				return remotewire.SessionDeleteResult{}, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "invalid conversation id"}
			}
			if err := deps.DeleteSession(ctx, params.ConversationID, params.PeerFingerprint); err != nil {
				return remotewire.SessionDeleteResult{}, err
			}
			// 交回的是删除的**后置条件**:应答返回时这一端已经没有这条会话了。
			return remotewire.SessionDeleteResult{Deleted: true}, nil
		}
	}
	if deps.SetModelTarget != nil {
		ports.SetModelTarget = func(ctx context.Context, params remotewire.SetModelTargetParams) error {
			return deps.SetModelTarget(ctx, params.ConversationID, params.ProviderKey, params.ModelKey)
		}
	}
	if deps.SetReasoningEffort != nil {
		ports.SetReasoningEffort = func(ctx context.Context, params remotewire.SetSessionReasoningEffortParams) error {
			return deps.SetReasoningEffort(ctx, params.ConversationID, params.ReasoningEffort)
		}
	}
	if deps.SetPermissionMode != nil {
		ports.SetPermissionMode = func(ctx context.Context, params remotewire.SetPermissionModeParams) error {
			return deps.SetPermissionMode(ctx, params.ConversationID, params.Mode)
		}
	}
	if deps.RunSession != nil {
		ports.Run = func(ctx context.Context, params remotewire.RunParams) (remotewire.RunAck, error) {
			sent, err := deps.RunSession(ctx, params, chat_svc.PeerSessionSource{Device: protorpc.ConnFromContext(ctx).Auth().DeviceFingerprint, Name: params.SourceDeviceName})
			if err != nil {
				return remotewire.RunAck{}, err
			}
			// 交回调用方送来的那条对话身份:本机可能是**新建**了一行来承载它(R17),
			// 但对话的身份仍是发起端铸的那一个 —— daemon / 桌面端都从不发号。
			//
			// UserMessageSeq 是本机作为宿主发的号,服务没交回响应时留 0 ——
			// 发起方据此不推进游标。
			ack := remotewire.RunAck{ConversationID: params.ConversationID}
			if sent != nil {
				ack.UserMessageSeq = sent.UserMessageSeq
				ack.UserMessageMinSeq = sent.UserMessageMinSeq
			}
			return ack, nil
		}
	}
	if deps.SteerSession != nil {
		ports.Steer = func(ctx context.Context, params remotewire.SteerParams) (remotewire.SteerResult, error) {
			enqueued, err := deps.SteerSession(ctx, params, chat_svc.PeerSessionSource{Device: protorpc.ConnFromContext(ctx).Auth().DeviceFingerprint})
			if err != nil {
				return remotewire.SteerResult{}, err
			}
			// 交回**入队侧**认的那个号,不是请求里那个:chat_svc.enqueue 自己 newQueuedID()。
			if enqueued == nil {
				return remotewire.SteerResult{}, nil
			}
			return remotewire.SteerResult{QueuedID: enqueued.QueuedID, Cancellable: enqueued.Cancellable}, nil
		}
	}
	if deps.CancelSteerSession != nil {
		ports.CancelSteer = func(ctx context.Context, params remotewire.CancelSteerParams) (remotewire.CancelSteerResult, error) {
			result, err := deps.CancelSteerSession(ctx, params)
			if err != nil {
				return remotewire.CancelSteerResult{}, err
			}
			if result == nil {
				return remotewire.CancelSteerResult{}, nil
			}
			return remotewire.CancelSteerResult{Removed: result.Removed}, nil
		}
	}
	if deps.SubmitAnswer != nil {
		ports.SubmitAnswer = func(ctx context.Context, params remotewire.SubmitAnswerParams) (remotewire.PeerSessionControlResult, error) {
			value, err := deps.SubmitAnswer(ctx, params)
			return remotewire.PeerSessionControlResult{AlreadyHandled: value.AlreadyHandled}, err
		}
	}
	if deps.SubmitToolPermission != nil {
		ports.SubmitToolPermission = func(ctx context.Context, params remotewire.SubmitToolPermissionParams) (remotewire.PeerSessionControlResult, error) {
			value, err := deps.SubmitToolPermission(ctx, params)
			return remotewire.PeerSessionControlResult{AlreadyHandled: value.AlreadyHandled}, err
		}
	}
	return ports
}
