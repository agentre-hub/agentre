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
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

type ProtobufInboundDeps struct {
	Peripheral wireinbound.PeripheralDeps
	// Engine 是引擎探测一族(engine.* / cli.resolvePath)的端口集。它与 Peripheral
	// 分开,是因为这一族的族闸门是**端口**:两种执行端对它的准入并不相同。
	Engine wireinbound.EngineDeps
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
	DeleteSession           func(context.Context, string, devicefp.Initiator) error
	// AbortSession 把这一条会话正在跑的那一轮停下来。它与 CancelSteerSession 不同:
	// 那一条撤的是还没被取走的排队消息,这一条停的是**已经在跑**的那一轮。
	AbortSession   func(context.Context, string) error
	SetModelTarget func(context.Context, string, string, string) error
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
	wireinbound.RegisterEngineMethods(registry, deps.Engine)
	wireinbound.RegisterSessionMethods(registry, peerSessionPorts(deps))
	return registry
}

// peerSessionPorts 把桌面端的这一份依赖装配成会话族的端口。
//
// 这里只剩**端口实现**:归属校验(requireOwnOrigin 在 composition 那一层)、桌面端
// 独有的对话 id 前置校验、把实时流推回这条连接的订阅者,以及线上载体 ↔ 领域值的那
// 一跳 —— 桌面端的依赖说的是 chat_svc 的领域值,而端口说 protobuf(agentred 的
// handler 本来就说它,见 wireinbound.SessionPorts)。
//
// 那一跳**不在这里逐字段搬**:每一处调的都是 wireinbound 导出的映射函数,字段清单
// 因此仍然只有一份 —— 加第 17 个字段时改那一处,两种执行端一起跟上。哪些方法存在、
// 闸门与错误映射怎么套、缺席怎么办,都还在 wireinbound。
//
// 某一格的依赖缺席时对应端口留 nil,那个方法于是**不注册** —— 调用方收到
// method not found,而不是一个「本机没装这个能力」的 -32603。协议里「这台机器办不到」
// 只有前一种说法,调用方据它换一台机器。
func peerSessionPorts(deps ProtobufInboundDeps) wireinbound.SessionPorts {
	ports := wireinbound.SessionPorts{
		Auth:           wireinbound.RequireAuthenticated,
		Error:          protobufPeerError,
		ActivityRollup: deps.ActivityRollup,
	}
	if deps.Capabilities != nil {
		ports.Capabilities = func(ctx context.Context, request *agentrewire.RuntimeCapabilitiesRequest) (*agentrewire.RuntimeCapabilitiesResponse, error) {
			value, err := deps.Capabilities(ctx, wireinbound.CapabilitiesParamsOf(request))
			if err != nil {
				return nil, err
			}
			return wireinbound.RuntimeCapabilitiesResponseOf(value), nil
		}
	}
	if deps.ListSessions != nil {
		ports.List = func(ctx context.Context, request *agentrewire.SessionListRequest) (*agentrewire.SessionListResponse, error) {
			value, err := deps.ListSessions(ctx, wireinbound.SessionListParamsOf(request))
			if err != nil {
				return nil, err
			}
			return wireinbound.SessionListResponseOf(*value), nil
		}
	}
	if deps.CountSessions != nil {
		ports.Counts = func(ctx context.Context) (*agentrewire.SessionCountsResponse, error) {
			value, err := deps.CountSessions(ctx)
			if err != nil {
				return nil, err
			}
			return wireinbound.SessionCountsResponseOf(*value), nil
		}
	}
	if deps.AbortSession != nil {
		ports.Abort = func(ctx context.Context, request *agentrewire.RuntimeAbortRequest) (*agentrewire.RuntimeAbortResponse, error) {
			if err := deps.AbortSession(ctx, request.GetConversationId()); err != nil {
				return nil, err
			}
			// turn_kind 留空:桌面端这一侧的停止走 chat_svc.Stop,它只报「abort 路径
			// 已经触发」,交不出被中断那一轮的类型。控制台不读这一格(按下即 await,
			// 界面按 StreamAborted 事件翻),所以留空是如实,不是漏搬。
			return &agentrewire.RuntimeAbortResponse{}, nil
		}
	}
	if deps.AttachSession != nil {
		ports.Attach = func(ctx context.Context, request *agentrewire.SessionAttachRequest) (*agentrewire.SessionAttachResponse, error) {
			if err := conversationid.Validate(request.GetConversationId()); err != nil {
				return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "invalid conversation id"}
			}
			conn := protorpc.ConnFromContext(ctx)
			if conn == nil {
				return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "session attach unavailable"}
			}
			value, err := deps.AttachSession(ctx, wireinbound.SessionAttachParamsOf(request), protobufPeerSubscriber{conn})
			if err != nil {
				return nil, err
			}
			return wireinbound.SessionAttachResponseOf(value), nil
		}
	}
	if deps.PullSession != nil {
		ports.Pull = func(ctx context.Context, request *agentrewire.SessionPullRequest) (*agentrewire.SessionPullResponse, error) {
			conn := protorpc.ConnFromContext(ctx)
			if conn == nil {
				return nil, &protorpc.Error{Code: protorpc.CodeInternal, Message: "session pull unavailable"}
			}
			value, err := deps.PullSession(ctx, wireinbound.SessionPullParamsOf(request), protobufPeerSubscriber{conn})
			if err != nil {
				return nil, err
			}
			return wireinbound.SessionPullResponseOf(value)
		}
	}
	if deps.PendingWaiters != nil {
		ports.PendingWaiters = func(ctx context.Context, request *agentrewire.SessionPendingWaitersRequest) (*agentrewire.SessionPendingWaitersResponse, error) {
			value, err := deps.PendingWaiters(ctx, wireinbound.SessionPendingWaitersParamsOf(request))
			if err != nil {
				return nil, err
			}
			return protowire.PendingWaitersResponseToProto(value), nil
		}
	}
	if deps.DeleteSession != nil {
		ports.Delete = func(ctx context.Context, request *agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
			params := wireinbound.SessionDeleteParamsOf(request)
			if err := conversationid.Validate(params.ConversationID); err != nil {
				return nil, &protorpc.Error{Code: protorpc.CodeInvalidParams, Message: "invalid conversation id"}
			}
			if err := deps.DeleteSession(ctx, params.ConversationID, params.PeerFingerprint); err != nil {
				return nil, err
			}
			// 交回的是删除的**后置条件**:应答返回时这一端已经没有这条会话了。
			return &agentrewire.SessionDeleteResponse{Deleted: true}, nil
		}
	}
	if deps.SetModelTarget != nil {
		ports.SetModelTarget = func(ctx context.Context, request *agentrewire.SetModelTargetRequest) (*agentrewire.SetModelTargetResponse, error) {
			params := wireinbound.SetModelTargetParamsOf(request)
			if err := deps.SetModelTarget(ctx, params.ConversationID, params.ProviderKey, params.ModelKey); err != nil {
				return nil, err
			}
			return &agentrewire.SetModelTargetResponse{}, nil
		}
	}
	if deps.SetReasoningEffort != nil {
		ports.SetReasoningEffort = func(ctx context.Context, request *agentrewire.SetSessionReasoningEffortRequest) (*agentrewire.SetSessionReasoningEffortResponse, error) {
			params := wireinbound.SetSessionReasoningEffortParamsOf(request)
			if err := deps.SetReasoningEffort(ctx, params.ConversationID, params.ReasoningEffort); err != nil {
				return nil, err
			}
			return &agentrewire.SetSessionReasoningEffortResponse{}, nil
		}
	}
	if deps.SetPermissionMode != nil {
		ports.SetPermissionMode = func(ctx context.Context, request *agentrewire.RuntimeSetPermissionModeRequest) (*agentrewire.Empty, error) {
			params := wireinbound.SetPermissionModeParamsOf(request)
			if err := deps.SetPermissionMode(ctx, params.ConversationID, params.Mode); err != nil {
				return nil, err
			}
			return &agentrewire.Empty{}, nil
		}
	}
	if deps.RunSession != nil {
		ports.Run = func(ctx context.Context, request *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
			// 桌面端把「请求体解不开」并进了同一个错误映射,因而答 -32603 internal;
			// agentred 那一侧的 handler 自己说 protobuf,压根没有这一跳。差异既存
			// (两条路其实都不可达:RunRequestFromProto 只在请求为 nil 或 backend
			// 无法 json.Marshal 时失败),这里如实保留。
			params, err := protowire.RunRequestFromProto(request)
			if err != nil {
				return nil, err
			}
			sent, err := deps.RunSession(ctx, params, chat_svc.PeerSessionSource{Device: devicefp.Initiator(protorpc.ConnFromContext(ctx).Auth().DeviceFingerprint), Name: params.SourceDeviceName})
			if err != nil {
				return nil, err
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
			return wireinbound.RuntimeRunResponseOf(ack), nil
		}
	}
	if deps.SteerSession != nil {
		ports.Steer = func(ctx context.Context, request *agentrewire.RuntimeSteerRequest) (*agentrewire.RuntimeSteerResponse, error) {
			enqueued, err := deps.SteerSession(ctx, wireinbound.SteerParamsOf(request), chat_svc.PeerSessionSource{Device: devicefp.Initiator(protorpc.ConnFromContext(ctx).Auth().DeviceFingerprint)})
			if err != nil {
				return nil, err
			}
			// 交回**入队侧**认的那个号,不是请求里那个:chat_svc.enqueue 自己 newQueuedID()。
			if enqueued == nil {
				return wireinbound.RuntimeSteerResponseOf(remotewire.SteerResult{}), nil
			}
			return wireinbound.RuntimeSteerResponseOf(remotewire.SteerResult{QueuedID: enqueued.QueuedID, Cancellable: enqueued.Cancellable}), nil
		}
	}
	if deps.CancelSteerSession != nil {
		ports.CancelSteer = func(ctx context.Context, request *agentrewire.RuntimeCancelSteerRequest) (*agentrewire.RuntimeCancelSteerResponse, error) {
			result, err := deps.CancelSteerSession(ctx, wireinbound.CancelSteerParamsOf(request))
			if err != nil {
				return nil, err
			}
			if result == nil {
				return wireinbound.RuntimeCancelSteerResponseOf(remotewire.CancelSteerResult{}), nil
			}
			return wireinbound.RuntimeCancelSteerResponseOf(remotewire.CancelSteerResult{Removed: result.Removed}), nil
		}
	}
	if deps.SubmitAnswer != nil {
		ports.SubmitAnswer = func(ctx context.Context, request *agentrewire.RuntimeSubmitAnswerRequest) (*agentrewire.PeerSessionControlResponse, error) {
			value, err := deps.SubmitAnswer(ctx, wireinbound.SubmitAnswerParamsOf(request))
			if err != nil {
				return nil, err
			}
			return wireinbound.PeerSessionControlResponseOf(remotewire.PeerSessionControlResult{AlreadyHandled: value.AlreadyHandled}), nil
		}
	}
	if deps.SubmitToolPermission != nil {
		ports.SubmitToolPermission = func(ctx context.Context, request *agentrewire.RuntimeSubmitToolPermissionRequest) (*agentrewire.PeerSessionControlResponse, error) {
			value, err := deps.SubmitToolPermission(ctx, wireinbound.SubmitToolPermissionParamsOf(request))
			if err != nil {
				return nil, err
			}
			return wireinbound.PeerSessionControlResponseOf(remotewire.PeerSessionControlResult{AlreadyHandled: value.AlreadyHandled}), nil
		}
	}
	return ports
}
