package wireinbound

import (
	"context"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// RegisterSessionMethods 把会话族的线形状挂上 registry。
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
func gated[Req any, Resp any](ports SessionPorts, handler func(context.Context, Req) (Resp, error)) func(context.Context, Req) (Resp, error) {
	return func(ctx context.Context, request Req) (Resp, error) {
		var zero Resp
		if err := ports.Auth(ctx); err != nil {
			return zero, err
		}
		return handler(ctx, request)
	}
}

func registerSessionCatalogMethods(registry *protorpc.Registry, ports SessionPorts) {
	if ports.List != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST), func() *agentrewire.SessionListRequest { return &agentrewire.SessionListRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.SessionListRequest) (*agentrewire.SessionListResponse, error) {
			// ConversationIDs 是点名收窄:详情页要的是**一条**会话的摘要,不带它
			// 就得把整台机器的清单翻一遍去找。两种执行端此前只有一种认得它。
			result, err := ports.List(ctx, remotewire.SessionListParams{
				Keyword:         request.GetKeyword(),
				Cursor:          request.GetCursor(),
				Limit:           int(request.GetLimit()),
				ConversationIDs: request.GetConversationIds(),
			})
			if err != nil {
				return nil, ports.Error(err)
			}
			response := &agentrewire.SessionListResponse{Cursor: result.Cursor, HasMore: result.HasMore, Total: result.Total}
			for _, session := range result.Sessions {
				response.Sessions = append(response.Sessions, protowire.SessionSummaryToProto(session))
			}
			return response, nil
		}))
	}
	if ports.Counts != nil {
		// 会话计数:调用方(设备卡片)要的从来不是清单 —— 拿清单去数,就得先把整台
		// 机器搬过线。
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_COUNTS), func() *agentrewire.SessionCountsRequest { return &agentrewire.SessionCountsRequest{} }, gated(ports, func(ctx context.Context, _ *agentrewire.SessionCountsRequest) (*agentrewire.SessionCountsResponse, error) {
			result, err := ports.Counts(ctx)
			if err != nil {
				return nil, ports.Error(err)
			}
			return &agentrewire.SessionCountsResponse{Total: result.Total, Running: result.Running, Waiting: result.Waiting}, nil
		}))
	}
	if ports.ActivityRollup != nil {
		// 活跃统计的纯计数上报:回包里只有天、维度和一个计数,没有标题、路径与内容。
		// 不带 since_day 的一次调用就是「回填」。
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_ACTIVITY_ROLLUP), func() *agentrewire.ActivityRollupRequest { return &agentrewire.ActivityRollupRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.ActivityRollupRequest) (*agentrewire.ActivityRollupResponse, error) {
			buckets, err := ports.ActivityRollup(ctx, request.GetSinceDay(), request.GetTimeZone())
			if err != nil {
				return nil, ports.Error(err)
			}
			response := &agentrewire.ActivityRollupResponse{Buckets: make([]*agentrewire.ActivityDailyBucket, 0, len(buckets))}
			for _, bucket := range buckets {
				response.Buckets = append(response.Buckets, &agentrewire.ActivityDailyBucket{
					Day: bucket.Day, AgentSyncId: bucket.AgentSyncID, BackendType: bucket.BackendType,
					ProviderKey: bucket.ProviderKey, ModelKey: bucket.ModelKey,
					ProjectSyncId: bucket.ProjectSyncID, SessionCount: bucket.SessionCount,
				})
			}
			return response, nil
		}))
	}
}

func registerSessionControlMethods(registry *protorpc.Registry, ports SessionPorts) {
	if ports.Attach != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH), func() *agentrewire.SessionAttachRequest { return &agentrewire.SessionAttachRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.SessionAttachRequest) (*agentrewire.SessionAttachResponse, error) {
			result, err := ports.Attach(ctx, remotewire.SessionAttachParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint})
			if err != nil {
				return nil, ports.Error(err)
			}
			return &agentrewire.SessionAttachResponse{ConversationId: result.ConversationID, BackendType: result.BackendType, LifecycleState: result.LifecycleState, LatestSeq: result.LatestSeq}, nil
		}))
	}
	if ports.Pull != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL), func() *agentrewire.SessionPullRequest { return &agentrewire.SessionPullRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.SessionPullRequest) (*agentrewire.SessionPullResponse, error) {
			result, err := ports.Pull(ctx, remotewire.SessionPullParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, Cursor: request.Cursor, Limit: int(request.Limit)})
			if err != nil {
				return nil, ports.Error(err)
			}
			response := &agentrewire.SessionPullResponse{Cursor: result.Cursor, HasMore: result.HasMore, OldestSeq: result.OldestSeq}
			for _, entry := range result.Notifications {
				journaled, convertErr := JournaledNotificationToProto(entry)
				if convertErr != nil {
					return nil, ports.Error(convertErr)
				}
				response.Notifications = append(response.Notifications, journaled)
			}
			return response, nil
		}))
	}
	if ports.PendingWaiters != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS), func() *agentrewire.SessionPendingWaitersRequest { return &agentrewire.SessionPendingWaitersRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.SessionPendingWaitersRequest) (*agentrewire.SessionPendingWaitersResponse, error) {
			result, err := ports.PendingWaiters(ctx, remotewire.SessionPendingWaitersParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint})
			if err != nil {
				return nil, ports.Error(err)
			}
			return protowire.PendingWaitersResponseToProto(result), nil
		}))
	}
	if ports.Delete != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE), func() *agentrewire.SessionDeleteRequest { return &agentrewire.SessionDeleteRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.SessionDeleteRequest) (*agentrewire.SessionDeleteResponse, error) {
			result, err := ports.Delete(ctx, remotewire.SessionDeleteParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint})
			if err != nil {
				return nil, ports.Error(err)
			}
			return &agentrewire.SessionDeleteResponse{Deleted: result.Deleted}, nil
		}))
	}
	if ports.SetModelTarget != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET), func() *agentrewire.SetModelTargetRequest { return &agentrewire.SetModelTargetRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.SetModelTargetRequest) (*agentrewire.SetModelTargetResponse, error) {
			if err := ports.SetModelTarget(ctx, remotewire.SetModelTargetParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, ProviderKey: request.ProviderKey, ModelKey: request.ModelKey}); err != nil {
				return nil, ports.Error(err)
			}
			return &agentrewire.SetModelTargetResponse{}, nil
		}))
	}
	if ports.SetReasoningEffort != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_REASONING_EFFORT), func() *agentrewire.SetSessionReasoningEffortRequest {
			return &agentrewire.SetSessionReasoningEffortRequest{}
		}, gated(ports, func(ctx context.Context, request *agentrewire.SetSessionReasoningEffortRequest) (*agentrewire.SetSessionReasoningEffortResponse, error) {
			// 空串是要写下去的值(改回跟随后端配置),不是「不改」—— 与 SetModelTarget 同族。
			if err := ports.SetReasoningEffort(ctx, remotewire.SetSessionReasoningEffortParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, ReasoningEffort: request.ReasoningEffort}); err != nil {
				return nil, ports.Error(err)
			}
			return &agentrewire.SetSessionReasoningEffortResponse{}, nil
		}))
	}
}

func registerSessionRuntimeMethods(registry *protorpc.Registry, ports SessionPorts) {
	if ports.Capabilities != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES), func() *agentrewire.RuntimeCapabilitiesRequest { return &agentrewire.RuntimeCapabilitiesRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.RuntimeCapabilitiesRequest) (*agentrewire.RuntimeCapabilitiesResponse, error) {
			result, err := ports.Capabilities(ctx, remotewire.CapabilitiesParams{BackendType: request.BackendType})
			if err != nil {
				return nil, ports.Error(err)
			}
			meta := result.Capabilities.PermissionModeMeta
			response := &agentrewire.RuntimeCapabilitiesResponse{PermissionMode: &agentrewire.PermissionModeMeta{AllowedModes: meta.AllowedModes, DefaultMode: meta.DefaultMode, SwitchableDuringTurn: meta.SwitchableDuringTurn, Order: meta.Order, LaunchDefaultMode: meta.LaunchDefaultMode}}
			for name, enabled := range result.Capabilities.Set {
				response.Capabilities = append(response.Capabilities, &agentrewire.CapabilityEntry{Name: string(name), Enabled: enabled})
			}
			return response, nil
		}))
	}
	if ports.Run != nil && ports.DecodeError != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN), func() *agentrewire.RuntimeRunRequest { return &agentrewire.RuntimeRunRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.RuntimeRunRequest) (*agentrewire.RuntimeRunResponse, error) {
			params, err := protowire.RunRequestFromProto(request)
			if err != nil {
				return nil, ports.DecodeError(err)
			}
			ack, err := ports.Run(ctx, params)
			if err != nil {
				return nil, ports.Error(err)
			}
			// ConversationId 交回的是**这一端认定**的那条对话身份。桌面端做宿主时
			// 回的是发起端铸的那一个(它可能新建了一行来承载,但号不改写);agentred
			// 回的是 RunAck 里那一个。UserMessageSeq / UserMessageMinSeq 则是宿主发的
			// 号:发起方据它把游标推进到「我已经持有的内容」,0 = 宿主没给,不推进。
			return &agentrewire.RuntimeRunResponse{
				ConversationId: ack.ConversationID, ProviderSessionId: ack.ProviderSessionID,
				LaunchPermissionMode: ack.LaunchPermissionMode, ProviderFallbackKey: ack.ProviderFallbackKey,
				UserMessageSeq: ack.UserMessageSeq, UserMessageMinSeq: ack.UserMessageMinSeq,
			}, nil
		}))
	}
	if ports.Steer != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER), func() *agentrewire.RuntimeSteerRequest { return &agentrewire.RuntimeSteerRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.RuntimeSteerRequest) (*agentrewire.RuntimeSteerResponse, error) {
			result, err := ports.Steer(ctx, remotewire.SteerParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, QueuedID: request.QueuedId, Text: request.Text})
			if err != nil {
				return nil, ports.Error(err)
			}
			// 回的是**执行端**认的那个号,不是请求里那个:桌面端的 chat_svc 入队时
			// 另造一个。调用方拿它去对 SteerConsumed.queuedId 才对得上。
			return &agentrewire.RuntimeSteerResponse{QueuedId: result.QueuedID, Cancellable: result.Cancellable}, nil
		}))
	}
	if ports.CancelSteer != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CANCEL_STEER), func() *agentrewire.RuntimeCancelSteerRequest { return &agentrewire.RuntimeCancelSteerRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.RuntimeCancelSteerRequest) (*agentrewire.RuntimeCancelSteerResponse, error) {
			result, err := ports.CancelSteer(ctx, remotewire.CancelSteerParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, QueuedID: request.QueuedId})
			if err != nil {
				return nil, ports.Error(err)
			}
			return &agentrewire.RuntimeCancelSteerResponse{Removed: result.Removed}, nil
		}))
	}
	if ports.SubmitAnswer != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER), func() *agentrewire.RuntimeSubmitAnswerRequest { return &agentrewire.RuntimeSubmitAnswerRequest{} }, gated(ports, func(ctx context.Context, request *agentrewire.RuntimeSubmitAnswerRequest) (*agentrewire.PeerSessionControlResponse, error) {
			questions := make([]agentruntime.AskQuestion, 0, len(request.Questions))
			for _, question := range request.Questions {
				questions = append(questions, askQuestionFromProto(question))
			}
			answers := make([]agentruntime.AskAnswer, 0, len(request.Answers))
			for _, answer := range request.Answers {
				answers = append(answers, agentruntime.AskAnswer{QuestionIndex: int(answer.QuestionIndex), Labels: append([]string(nil), answer.Labels...), OtherText: answer.OtherText})
			}
			result, err := ports.SubmitAnswer(ctx, remotewire.SubmitAnswerParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, RequestID: request.RequestId, Questions: questions, Answers: answers, Skipped: request.Skipped})
			if err != nil {
				return nil, ports.Error(err)
			}
			// AlreadyHandled 说的是别的端点抢先答了。宿主答不出这件事时留零值 ——
			// 零值保住的正是「本次提交成功」这个原义。
			return &agentrewire.PeerSessionControlResponse{AlreadyHandled: result.AlreadyHandled}, nil
		}))
	}
	if ports.SubmitToolPermission != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION), func() *agentrewire.RuntimeSubmitToolPermissionRequest {
			return &agentrewire.RuntimeSubmitToolPermissionRequest{}
		}, gated(ports, func(ctx context.Context, request *agentrewire.RuntimeSubmitToolPermissionRequest) (*agentrewire.PeerSessionControlResponse, error) {
			result, err := ports.SubmitToolPermission(ctx, remotewire.SubmitToolPermissionParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, RequestID: request.RequestId, Allow: request.Allow, AlwaysAllowSession: request.AlwaysAllowSession, DenyReason: request.DenyReason})
			if err != nil {
				return nil, ports.Error(err)
			}
			return &agentrewire.PeerSessionControlResponse{AlreadyHandled: result.AlreadyHandled}, nil
		}))
	}
	if ports.SetPermissionMode != nil {
		protorpc.RegisterMethod(registry, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE), func() *agentrewire.RuntimeSetPermissionModeRequest {
			return &agentrewire.RuntimeSetPermissionModeRequest{}
		}, gated(ports, func(ctx context.Context, request *agentrewire.RuntimeSetPermissionModeRequest) (*agentrewire.Empty, error) {
			if err := ports.SetPermissionMode(ctx, remotewire.SetPermissionModeParams{ConversationID: request.ConversationId, PeerFingerprint: request.PeerFingerprint, Mode: request.Mode}); err != nil {
				return nil, ports.Error(err)
			}
			return &agentrewire.Empty{}, nil
		}))
	}
}

func askQuestionFromProto(question *agentrewire.AskQuestion) agentruntime.AskQuestion {
	result := agentruntime.AskQuestion{ID: question.Id, Question: question.Question, Header: question.Header, MultiSelect: question.MultiSelect, IsOther: question.IsOther, IsSecret: question.IsSecret}
	for _, option := range question.Options {
		result.Options = append(result.Options, agentruntime.AskOption{Label: option.Label, Description: option.Description, Preview: option.Preview})
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
