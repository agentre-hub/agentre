package wireinbound

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// RegisterSessionMethods 把会话族挂上 registry。
//
// 本函数是**方法号 ↔ 请求消息类型**这张配对表在入站这一侧的唯一所在:加一个会话族
// 方法只能改这里,两种执行端于是不可能一边挂上、一边漏掉(session.list 的
// conversation_ids 就是那样漏过一次的)。
//
// **端口缺席 ⇒ 那一个方法不注册**,与外围族同一条判据:调用方收到 method not found,
// 协议里「这台机器办不到」只有这一种说法。逐个方法判而不是整族判,是因为两种执行端
// 的端口集本就不完全一样,而且同一个宿主还会分两处调它 —— agentred 的清单族挂在
// daemon 级 registry 上(它不依赖某一条连接),runtime 族与 attach 挂在**连接**级
// registry 上(它们要认领这条连接、要按连接持有 runtime handler)。两次调用各交出
// 自己那一半端口,另一半留 nil。
func RegisterSessionMethods(registry *protorpc.Registry, ports SessionPorts) {
	// 闸门与错误映射缺一不可:没有闸门就没有「谁能问」,没有映射就答不出话。
	// 两者任一缺席时整族不注册 —— 失效方向是关掉,不是敞开。
	if ports.Auth == nil || ports.Error == nil {
		return
	}
	registerSessionCatalogMethods(registry, ports)
	registerSessionControlMethods(registry, ports)
	registerSessionRuntimeMethods(registry, ports)
}

// gated 把闸门套在每一个 handler 的最前面。闸门先于任何解包与副作用 —— 未鉴权的
// 连接连「这条会话存不存在」都不该问得出来。
//
// 闸门以**函数**传入而不是整个端口集:引擎探测一族(engine.go)用的是同一道闸门语义,
// 但它的端口集是另一个类型 —— 两族共用这一个 helper,「先过闸门再进处理函数」这件事
// 因此只有一处实现。
func gated[Req any, Resp any](auth func(context.Context) error, handler func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, request Req) (Resp, error) {
		var zero Resp
		if err := auth(ctx); err != nil {
			return zero, err
		}
		return handler(ctx, request)
	}
}

// forward 是端口这一跳的全部内容:调它、错误按宿主自己的映射折上线。
//
// 端口直接收发线上的载体,所以这里没有字段搬运可做 —— 需要领域值的那一侧
// (桌面端)在自己的装配处调本包导出的映射函数,字段清单因此仍只有一份。
func forward[Req any, Resp any](ports SessionPorts, port func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
	return gated(ports.Auth, func(ctx context.Context, request Req) (Resp, error) {
		var zero Resp
		response, err := port(ctx, request)
		if err != nil {
			return zero, ports.Error(err)
		}
		return response, nil
	})
}

func registerSessionCatalogMethods(registry *protorpc.Registry, ports SessionPorts) {
	if ports.List != nil {
		// ConversationIDs 是点名收窄:详情页要的是**一条**会话的摘要,不带它
		// 就得把整台机器的清单翻一遍去找。两种执行端此前只有一种认得它。
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST), func() *agentrewire.SessionListRequest { return &agentrewire.SessionListRequest{} }, forward(ports, ports.List))
	}
	if ports.Counts != nil {
		// 会话计数:调用方(设备卡片)要的从来不是清单 —— 拿清单去数,就得先把整台
		// 机器搬过线。
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_COUNTS), func() *agentrewire.SessionCountsRequest { return &agentrewire.SessionCountsRequest{} }, gated(ports.Auth, func(ctx context.Context, _ *agentrewire.SessionCountsRequest) (*agentrewire.SessionCountsResponse, error) {
			response, err := ports.Counts(ctx)
			if err != nil {
				return nil, ports.Error(err)
			}
			return response, nil
		}))
	}
	if ports.ActivityRollup != nil {
		// 活跃统计的纯计数上报:回包里只有天、维度和一个计数,没有标题、路径与内容。
		// 不带 since_day 的一次调用就是「回填」。
		//
		// 这一格的端口收的是领域值(两种执行端交出的都是 activityrollup.Bucket,
		// 没有哪一侧说 protobuf),所以折成线格式这一步留在这里。
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_ACTIVITY_ROLLUP), func() *agentrewire.ActivityRollupRequest { return &agentrewire.ActivityRollupRequest{} }, gated(ports.Auth, func(ctx context.Context, request *agentrewire.ActivityRollupRequest) (*agentrewire.ActivityRollupResponse, error) {
			buckets, err := ports.ActivityRollup(ctx, request.GetSinceDay(), request.GetTimeZone())
			if err != nil {
				return nil, ports.Error(err)
			}
			return ActivityRollupResponseOf(buckets), nil
		}))
	}
}

func registerSessionControlMethods(registry *protorpc.Registry, ports SessionPorts) {
	if ports.Attach != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH), func() *agentrewire.SessionAttachRequest { return &agentrewire.SessionAttachRequest{} }, forward(ports, ports.Attach))
	}
	if ports.Pull != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL), func() *agentrewire.SessionPullRequest { return &agentrewire.SessionPullRequest{} }, forward(ports, ports.Pull))
	}
	if ports.PendingWaiters != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS), func() *agentrewire.SessionPendingWaitersRequest { return &agentrewire.SessionPendingWaitersRequest{} }, forward(ports, ports.PendingWaiters))
	}
	if ports.Delete != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE), func() *agentrewire.SessionDeleteRequest { return &agentrewire.SessionDeleteRequest{} }, forward(ports, ports.Delete))
	}
	if ports.SetModelTarget != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET), func() *agentrewire.SetModelTargetRequest { return &agentrewire.SetModelTargetRequest{} }, forward(ports, ports.SetModelTarget))
	}
	if ports.SetReasoningEffort != nil {
		// 空串是要写下去的值(改回跟随后端配置),不是「不改」—— 与 SetModelTarget 同族。
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_REASONING_EFFORT), func() *agentrewire.SetSessionReasoningEffortRequest {
			return &agentrewire.SetSessionReasoningEffortRequest{}
		}, forward(ports, ports.SetReasoningEffort))
	}
}

func registerSessionRuntimeMethods(registry *protorpc.Registry, ports SessionPorts) {
	if ports.Capabilities != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES), func() *agentrewire.RuntimeCapabilitiesRequest { return &agentrewire.RuntimeCapabilitiesRequest{} }, forward(ports, ports.Capabilities))
	}
	if ports.Run != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} }, forward(ports, ports.Run))
	}
	if ports.Steer != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER), func() *agentrewire.RuntimeSteerRequest { return &agentrewire.RuntimeSteerRequest{} }, forward(ports, ports.Steer))
	}
	if ports.CancelSteer != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CANCEL_STEER), func() *agentrewire.RuntimeCancelSteerRequest { return &agentrewire.RuntimeCancelSteerRequest{} }, forward(ports, ports.CancelSteer))
	}
	if ports.SubmitAnswer != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER), func() *agentrewire.RuntimeSubmitAnswerRequest { return &agentrewire.RuntimeSubmitAnswerRequest{} }, forward(ports, ports.SubmitAnswer))
	}
	if ports.SubmitToolPermission != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION), func() *agentrewire.RuntimeSubmitToolPermissionRequest {
			return &agentrewire.RuntimeSubmitToolPermissionRequest{}
		}, forward(ports, ports.SubmitToolPermission))
	}
	if ports.Abort != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT), func() *agentrewire.RuntimeAbortRequest { return &agentrewire.RuntimeAbortRequest{} }, forward(ports, ports.Abort))
	}
	if ports.SetPermissionMode != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE), func() *agentrewire.RuntimeSetPermissionModeRequest {
			return &agentrewire.RuntimeSetPermissionModeRequest{}
		}, forward(ports, ports.SetPermissionMode))
	}
}

// ── 线上载体 ↔ 领域值:只有这一份 ─────────────────────────────────────────────
//
// 端口说 protobuf(见 SessionPorts 的说明),而桌面端的依赖说 chat_svc 的领域值。
// 那一跳的字段搬运集中在下面这一组导出函数里,由桌面端的 peerSessionPorts 调用。
// 它们是**唯一**一份:proto3 分辨不了「没发这一格」与「发了零值」,漏搬一格既不影响
// 编译、往返测试也不会红 —— 发的是零值、解的也是零值,只是标题变空、最后活动时间变
// 0。摘要那一格的完备性另有反射 + descriptor 双向守卫钉着
// (protowire.TestSessionSummaryPreservesEvery*Field)。

// SessionListParamsOf 解出清单请求的收窄条件与分页。
func SessionListParamsOf(request *agentrewire.SessionListRequest) remotewire.SessionListParams {
	return remotewire.SessionListParams{
		Keyword:         request.GetKeyword(),
		Cursor:          request.GetCursor(),
		Limit:           int(request.GetLimit()),
		ConversationIDs: request.GetConversationIds(),
	}
}

// SessionListResponseOf 把一页会话摘要编上线。
func SessionListResponseOf(result remotewire.SessionListResult) *agentrewire.SessionListResponse {
	response := &agentrewire.SessionListResponse{Cursor: result.Cursor, HasMore: result.HasMore, Total: result.Total}
	for _, session := range result.Sessions {
		response.Sessions = append(response.Sessions, protowire.SessionSummaryToProto(session))
	}
	return response
}

// SessionCountsResponseOf 把三个数(一共 / 在跑 / 在等你)编上线。
func SessionCountsResponseOf(result remotewire.SessionCountsResult) *agentrewire.SessionCountsResponse {
	return &agentrewire.SessionCountsResponse{Total: result.Total, Running: result.Running, Waiting: result.Waiting}
}

// ActivityRollupResponseOf 把按天 × 维度的计数编上线。
func ActivityRollupResponseOf(buckets []activityrollup.Bucket) *agentrewire.ActivityRollupResponse {
	response := &agentrewire.ActivityRollupResponse{Buckets: make([]*agentrewire.ActivityDailyBucket, 0, len(buckets))}
	for _, bucket := range buckets {
		response.Buckets = append(response.Buckets, &agentrewire.ActivityDailyBucket{
			Day: bucket.Day, AgentSyncId: bucket.AgentSyncID, BackendType: bucket.BackendType,
			ProviderKey: bucket.ProviderKey, ModelKey: bucket.ModelKey,
			ProjectSyncId: bucket.ProjectSyncID, SessionCount: bucket.SessionCount,
		})
	}
	return response
}

// SessionAttachParamsOf 解出接管请求点名的那条会话。
func SessionAttachParamsOf(request *agentrewire.SessionAttachRequest) remotewire.SessionAttachParams {
	return remotewire.SessionAttachParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint())}
}

// SessionAttachResponseOf 把接管的结果编上线。
func SessionAttachResponseOf(result remotewire.SessionAttachResult) *agentrewire.SessionAttachResponse {
	return &agentrewire.SessionAttachResponse{ConversationId: result.ConversationID, BackendType: result.BackendType, LifecycleState: result.LifecycleState, LatestSeq: result.LatestSeq}
}

// SessionPullParamsOf 解出增量拉取的游标与页大小。
func SessionPullParamsOf(request *agentrewire.SessionPullRequest) remotewire.SessionPullParams {
	return remotewire.SessionPullParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint()), Cursor: request.GetCursor(), Limit: int(request.GetLimit())}
}

// SessionPullResponseOf 把一页补齐编上线。
func SessionPullResponseOf(result remotewire.SessionPullResult) (*agentrewire.SessionPullResponse, error) {
	response := &agentrewire.SessionPullResponse{Cursor: result.Cursor, HasMore: result.HasMore, OldestSeq: result.OldestSeq}
	for _, entry := range result.Notifications {
		journaled, err := JournaledNotificationToProto(entry)
		if err != nil {
			return nil, err
		}
		response.Notifications = append(response.Notifications, journaled)
	}
	return response, nil
}

// SessionPendingWaitersParamsOf 解出待决策查询点名的那条会话。
func SessionPendingWaitersParamsOf(request *agentrewire.SessionPendingWaitersRequest) remotewire.SessionPendingWaitersParams {
	return remotewire.SessionPendingWaitersParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint())}
}

// SessionDeleteParamsOf 解出删除请求点名的那条会话。
func SessionDeleteParamsOf(request *agentrewire.SessionDeleteRequest) remotewire.SessionDeleteParams {
	return remotewire.SessionDeleteParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint())}
}

// SetModelTargetParamsOf 解出会话级 ModelTarget 的两格。
func SetModelTargetParamsOf(request *agentrewire.SetModelTargetRequest) remotewire.SetModelTargetParams {
	return remotewire.SetModelTargetParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint()), ProviderKey: request.GetProviderKey(), ModelKey: request.GetModelKey()}
}

// SetSessionReasoningEffortParamsOf 解出会话级思考力度。
func SetSessionReasoningEffortParamsOf(request *agentrewire.SetSessionReasoningEffortRequest) remotewire.SetSessionReasoningEffortParams {
	return remotewire.SetSessionReasoningEffortParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint()), ReasoningEffort: request.GetReasoningEffort()}
}

// CapabilitiesParamsOf 解出要问哪个 backend 的能力矩阵。
func CapabilitiesParamsOf(request *agentrewire.RuntimeCapabilitiesRequest) remotewire.CapabilitiesParams {
	return remotewire.CapabilitiesParams{BackendType: request.GetBackendType()}
}

// RuntimeCapabilitiesResponseOf 把能力矩阵编上线。
func RuntimeCapabilitiesResponseOf(result remotewire.CapabilitiesResult) *agentrewire.RuntimeCapabilitiesResponse {
	return protowire.CapabilitiesToProto(result.Capabilities)
}

// RuntimeRunResponseOf 把一轮的受理回执编上线。
//
// ConversationId 交回的是**这一端认定**的那条对话身份。桌面端做宿主时回的是发起端
// 铸的那一个(它可能新建了一行来承载,但号不改写);agentred 回的是 RunAck 里那一个。
// UserMessageSeq / UserMessageMinSeq 则是宿主发的号:发起方据它把游标推进到「我已经
// 持有的内容」,0 = 宿主没给,不推进。
func RuntimeRunResponseOf(ack remotewire.RunAck) *agentrewire.RuntimeRunResponse {
	return &agentrewire.RuntimeRunResponse{
		ConversationId: ack.ConversationID, ProviderSessionId: ack.ProviderSessionID,
		LaunchPermissionMode: ack.LaunchPermissionMode, ProviderFallbackKey: ack.ProviderFallbackKey,
		UserMessageSeq: ack.UserMessageSeq, UserMessageMinSeq: ack.UserMessageMinSeq,
	}
}

// SteerParamsOf 解出一条插话。
func SteerParamsOf(request *agentrewire.RuntimeSteerRequest) remotewire.SteerParams {
	return remotewire.SteerParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint()), QueuedID: request.GetQueuedId(), Text: request.GetText()}
}

// RuntimeSteerResponseOf 把入队回执编上线。
//
// 回的是**执行端**认的那个号,不是请求里那个:桌面端的 chat_svc 入队时另造一个。
// 调用方拿它去对 SteerConsumed.queuedId 才对得上。
func RuntimeSteerResponseOf(result remotewire.SteerResult) *agentrewire.RuntimeSteerResponse {
	return &agentrewire.RuntimeSteerResponse{QueuedId: result.QueuedID, Cancellable: result.Cancellable}
}

// CancelSteerParamsOf 解出要撤回哪一条排队消息(空 queuedID = 清空整条队列)。
func CancelSteerParamsOf(request *agentrewire.RuntimeCancelSteerRequest) remotewire.CancelSteerParams {
	return remotewire.CancelSteerParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint()), QueuedID: request.GetQueuedId()}
}

// RuntimeCancelSteerResponseOf 把撤回结果编上线。
func RuntimeCancelSteerResponseOf(result remotewire.CancelSteerResult) *agentrewire.RuntimeCancelSteerResponse {
	return &agentrewire.RuntimeCancelSteerResponse{Removed: result.Removed}
}

// SubmitAnswerParamsOf 解出一次提问的答复。
func SubmitAnswerParamsOf(request *agentrewire.RuntimeSubmitAnswerRequest) remotewire.SubmitAnswerParams {
	questions := make([]agentruntime.AskQuestion, 0, len(request.GetQuestions()))
	for _, question := range request.GetQuestions() {
		questions = append(questions, askQuestionFromProto(question))
	}
	answers := make([]agentruntime.AskAnswer, 0, len(request.GetAnswers()))
	for _, answer := range request.GetAnswers() {
		answers = append(answers, agentruntime.AskAnswer{QuestionIndex: int(answer.GetQuestionIndex()), Labels: append([]string(nil), answer.GetLabels()...), OtherText: answer.GetOtherText()})
	}
	return remotewire.SubmitAnswerParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint()), RequestID: request.GetRequestId(), Questions: questions, Answers: answers, Skipped: request.GetSkipped()}
}

// SubmitToolPermissionParamsOf 解出一次工具授权的答复。
func SubmitToolPermissionParamsOf(request *agentrewire.RuntimeSubmitToolPermissionRequest) remotewire.SubmitToolPermissionParams {
	return remotewire.SubmitToolPermissionParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint()), RequestID: request.GetRequestId(), Allow: request.GetAllow(), AlwaysAllowSession: request.GetAlwaysAllowSession(), DenyReason: request.GetDenyReason()}
}

// PeerSessionControlResponseOf 把一次轮内控制的结果编上线。
//
// AlreadyHandled 说的是别的端点抢先答了。宿主答不出这件事时留零值 —— 零值保住的
// 正是「本次提交成功」这个原义。
func PeerSessionControlResponseOf(result remotewire.PeerSessionControlResult) *agentrewire.PeerSessionControlResponse {
	return &agentrewire.PeerSessionControlResponse{AlreadyHandled: result.AlreadyHandled}
}

// SetPermissionModeParamsOf 解出要切到哪一档权限模式。
func SetPermissionModeParamsOf(request *agentrewire.RuntimeSetPermissionModeRequest) remotewire.SetPermissionModeParams {
	return remotewire.SetPermissionModeParams{ConversationID: request.GetConversationId(), PeerFingerprint: devicefp.Initiator(request.GetPeerFingerprint()), Mode: request.GetMode()}
}

func askQuestionFromProto(question *agentrewire.AskQuestion) agentruntime.AskQuestion {
	result := agentruntime.AskQuestion{ID: question.GetId(), Question: question.GetQuestion(), Header: question.GetHeader(), MultiSelect: question.GetMultiSelect(), IsOther: question.GetIsOther(), IsSecret: question.GetIsSecret()}
	for _, option := range question.GetOptions() {
		result.Options = append(result.Options, agentruntime.AskOption{Label: option.GetLabel(), Description: option.GetDescription(), Preview: option.GetPreview()})
	}
	return result
}

// JournaledNotificationToProto 把补齐交出的一行投影到线上的载体。
//
// 单独一个函数而不是留在注册闭包里,是因为这一跳有三样东西必须一起对:seq 盖进载荷
// (客户端按 method 解出的帧里没有它)、seq 留在载体上,以及**发生时刻**原样转交。
// 时刻是最容易在这类逐字段搬运里被漏掉的一样,而漏掉之后没有任何东西会报错 ——
// 下游只是安静地少一列,要到浏览器控制台的转录上才看得出来。
func JournaledNotificationToProto(entry remotewire.JournaledNotification) (*agentrewire.JournaledNotification, error) {
	notification, err := protowire.WireNotificationToProto(entry.Method, entry.Params)
	if err != nil {
		return nil, err
	}
	protowire.SetNotificationSeq(notification, entry.Seq)
	return &agentrewire.JournaledNotification{
		Seq:     entry.Seq,
		Payload: notification,
		// 报不出时刻的对端交出 0,这里照样转交 0:「不知道」不能在中途被补成当下。
		Createtime: entry.Createtime,
	}, nil
}
