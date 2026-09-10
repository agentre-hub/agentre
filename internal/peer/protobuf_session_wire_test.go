package peer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/internal/pkg/activityrollup"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	remotewire "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 本文件是**表征测试**(characterization test):它不主张会话族这 16 个方法「应该」
// 怎么答,只钉住桌面端此刻在线上答的是什么 —— 每一条都覆盖两件事:
//
//  1. 未鉴权时的错误码与 **message 原文**。桌面端走 wireinbound.Authenticated,
//     答的是 -32001 + 小写的 "unauthorized";agentred 同一族走 requireProtobufAuth,
//     答的是大写 U 的 "Unauthorized"。码相同、字面不同,而 message 也在线上 ——
//     这条差异是既存事实,闸门因此只能是端口,不能在共用注册面里统一掉。
//  2. 已鉴权时这一条应答长什么样(逐字段),外加桌面端**独有**的两条映射:
//     conversationid 校验的 -32602,以及 chat_svc.ErrPeerSessionNotFound 的 -32002。
//     后者是 protobufPeerError 比 agentred 的 protobufError 多认的那一种,错误映射
//     因此同样只能是端口。
//
// 会话族的正文正在从两个宿主收进 internal/pkg/wireinbound。这组用例是「零线上行为
// 变化」的证据:重构前后它必须一个字不改地绿。
type peerSessionWireCase struct {
	name        string
	method      agentrewire.RpcMethod
	request     proto.Message
	newResponse func() proto.Message
	assert      func(t *testing.T, response proto.Message, err error)
}

func requirePeerWireError(t *testing.T, err error, code int32, message string) {
	t.Helper()
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, code, rpcErr.Code)
	require.Equal(t, message, rpcErr.Message)
}

// peerSessionWireDeps 是一份**全都装上**的端口:每一格都交出一个可辨认的值,
// 这样应答里少搬一格都会被下面的断言逮到。
func peerSessionWireDeps() ProtobufInboundDeps {
	return ProtobufInboundDeps{
		Capabilities: func(context.Context, remotewire.CapabilitiesParams) (remotewire.CapabilitiesResult, error) {
			return remotewire.CapabilitiesResult{Capabilities: capability.Capabilities{
				Set:                map[capability.Capability]bool{capability.CapSteer: true},
				PermissionModeMeta: capability.PermissionModeMeta{AllowedModes: []string{"default", "plan"}, DefaultMode: "default", SwitchableDuringTurn: true, Order: []string{"default", "plan"}},
			}}, nil
		},
		ListSessions: func(context.Context, remotewire.SessionListParams) (*remotewire.SessionListResult, error) {
			return &remotewire.SessionListResult{
				Sessions: []remotewire.SessionSummary{{ConversationID: convID(7), Title: "remote"}},
				Cursor:   "20", HasMore: true, Total: 44,
			}, nil
		},
		CountSessions: func(context.Context) (*remotewire.SessionCountsResult, error) {
			return &remotewire.SessionCountsResult{Total: 3500, Running: 2, Waiting: 1}, nil
		},
		ActivityRollup: func(context.Context, string, string) ([]activityrollup.Bucket, error) {
			return []activityrollup.Bucket{{Day: "2026-09-08", AgentSyncID: "agent-1", BackendType: "claudecode", ProviderKey: "prov", ModelKey: "model", ProjectSyncID: "project-1", SessionCount: 5}}, nil
		},
		AttachSession: func(context.Context, remotewire.SessionAttachParams, chat_svc.PeerSessionSubscriber) (remotewire.SessionAttachResult, error) {
			return remotewire.SessionAttachResult{ConversationID: convID(7), BackendType: "claudecode", LifecycleState: "idle", LatestSeq: 12}, nil
		},
		PullSession: func(context.Context, remotewire.SessionPullParams, chat_svc.PeerSessionSubscriber) (remotewire.SessionPullResult, error) {
			return remotewire.SessionPullResult{Cursor: 4, HasMore: true, OldestSeq: 1}, nil
		},
		PendingWaiters: func(context.Context, remotewire.SessionPendingWaitersParams) (remotewire.SessionPendingWaitersResult, error) {
			return remotewire.SessionPendingWaitersResult{
				ToolPermissions: []agentruntime.PendingToolPermission{{RequestID: "tool-1", ToolName: "Bash", Input: []byte(`{"cmd":"ls"}`)}},
			}, nil
		},
		DeleteSession:      func(context.Context, string, string) error { return nil },
		SetModelTarget:     func(context.Context, string, string, string) error { return nil },
		SetReasoningEffort: func(context.Context, string, string) error { return nil },
		SetPermissionMode:  func(context.Context, string, string) error { return nil },
		RunSession: func(context.Context, remotewire.RunParams, chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error) {
			return &chat_svc.SendResponse{SessionID: 42, UserMessageSeq: 8, UserMessageMinSeq: 7}, nil
		},
		SteerSession: func(context.Context, remotewire.SteerParams, chat_svc.PeerSessionSource) (*chat_svc.EnqueueResponse, error) {
			return &chat_svc.EnqueueResponse{SessionID: 42, Queued: true, QueuedID: "desktop-42", Cancellable: true}, nil
		},
		CancelSteerSession: func(context.Context, remotewire.CancelSteerParams) (*chat_svc.CancelQueuedResponse, error) {
			return &chat_svc.CancelQueuedResponse{Removed: []string{"desktop-42"}}, nil
		},
		SubmitAnswer: func(context.Context, remotewire.SubmitAnswerParams) (chat_svc.PeerSessionControlResult, error) {
			return chat_svc.PeerSessionControlResult{AlreadyHandled: true}, nil
		},
		SubmitToolPermission: func(context.Context, remotewire.SubmitToolPermissionParams) (chat_svc.PeerSessionControlResult, error) {
			return chat_svc.PeerSessionControlResult{AlreadyHandled: true}, nil
		},
	}
}

func peerSessionWireCases() []peerSessionWireCase {
	return []peerSessionWireCase{
		{
			name: "session.list", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST,
			request:     &agentrewire.SessionListRequest{Keyword: "happy", Limit: 20, Cursor: "0"},
			newResponse: func() proto.Message { return &agentrewire.SessionListResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.SessionListResponse)
				require.Len(t, value.GetSessions(), 1)
				require.Equal(t, convID(7), value.GetSessions()[0].GetConversationId())
				require.Equal(t, "remote", value.GetSessions()[0].GetTitle())
				require.Equal(t, "20", value.GetCursor())
				require.True(t, value.GetHasMore())
				require.Equal(t, int64(44), value.GetTotal())
			},
		},
		{
			name: "session.counts", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_COUNTS,
			request:     &agentrewire.SessionCountsRequest{},
			newResponse: func() proto.Message { return &agentrewire.SessionCountsResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.SessionCountsResponse)
				require.Equal(t, int64(3500), value.GetTotal())
				require.Equal(t, int64(2), value.GetRunning())
				require.Equal(t, int64(1), value.GetWaiting())
			},
		},
		{
			name: "activity.rollup", method: agentrewire.RpcMethod_RPC_METHOD_ACTIVITY_ROLLUP,
			request:     &agentrewire.ActivityRollupRequest{SinceDay: "2026-09-01", TimeZone: "Asia/Shanghai"},
			newResponse: func() proto.Message { return &agentrewire.ActivityRollupResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				buckets := response.(*agentrewire.ActivityRollupResponse).GetBuckets()
				require.Len(t, buckets, 1)
				require.Equal(t, "2026-09-08", buckets[0].GetDay())
				require.Equal(t, "agent-1", buckets[0].GetAgentSyncId())
				require.Equal(t, "claudecode", buckets[0].GetBackendType())
				require.Equal(t, "prov", buckets[0].GetProviderKey())
				require.Equal(t, "model", buckets[0].GetModelKey())
				require.Equal(t, "project-1", buckets[0].GetProjectSyncId())
				require.Equal(t, int32(5), buckets[0].GetSessionCount())
			},
		},
		{
			name: "session.pull", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL,
			request:     &agentrewire.SessionPullRequest{ConversationId: convID(7), Cursor: 3, Limit: 10},
			newResponse: func() proto.Message { return &agentrewire.SessionPullResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.SessionPullResponse)
				require.Equal(t, int64(4), value.GetCursor())
				require.True(t, value.GetHasMore())
				require.Equal(t, int64(1), value.GetOldestSeq())
			},
		},
		{
			name: "session.pendingWaiters", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS,
			request:     &agentrewire.SessionPendingWaitersRequest{ConversationId: convID(7)},
			newResponse: func() proto.Message { return &agentrewire.SessionPendingWaitersResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.SessionPendingWaitersResponse)
				require.Len(t, value.GetToolPermissions(), 1)
				require.Equal(t, "tool-1", value.GetToolPermissions()[0].GetRequestId())
				require.Equal(t, "Bash", value.GetToolPermissions()[0].GetToolName())
				require.Equal(t, []byte(`{"cmd":"ls"}`), value.GetToolPermissions()[0].GetInput())
			},
		},
		{
			name: "session.delete", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE,
			request:     &agentrewire.SessionDeleteRequest{ConversationId: convID(7)},
			newResponse: func() proto.Message { return &agentrewire.SessionDeleteResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				// 桌面端答的是删除的**后置条件**,恒为 true —— 端口只交出 error。
				require.True(t, response.(*agentrewire.SessionDeleteResponse).GetDeleted())
			},
		},
		{
			name: "session.setModelTarget", method: agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET,
			request:     &agentrewire.SetModelTargetRequest{ConversationId: convID(7), ProviderKey: "prov", ModelKey: "model"},
			newResponse: func() proto.Message { return &agentrewire.SetModelTargetResponse{} },
			assert:      func(t *testing.T, _ proto.Message, err error) { require.NoError(t, err) },
		},
		{
			name: "session.setReasoningEffort", method: agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_REASONING_EFFORT,
			request:     &agentrewire.SetSessionReasoningEffortRequest{ConversationId: convID(7), ReasoningEffort: "xhigh"},
			newResponse: func() proto.Message { return &agentrewire.SetSessionReasoningEffortResponse{} },
			assert:      func(t *testing.T, _ proto.Message, err error) { require.NoError(t, err) },
		},
		{
			name: "runtime.capabilities", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES,
			request:     &agentrewire.RuntimeCapabilitiesRequest{BackendType: "claudecode"},
			newResponse: func() proto.Message { return &agentrewire.RuntimeCapabilitiesResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.RuntimeCapabilitiesResponse)
				require.Len(t, value.GetCapabilities(), 1)
				require.Equal(t, "steer", value.GetCapabilities()[0].GetName())
				require.True(t, value.GetCapabilities()[0].GetEnabled())
				require.Equal(t, []string{"default", "plan"}, value.GetPermissionMode().GetAllowedModes())
				require.Equal(t, "default", value.GetPermissionMode().GetDefaultMode())
				require.True(t, value.GetPermissionMode().GetSwitchableDuringTurn())
				require.Equal(t, []string{"default", "plan"}, value.GetPermissionMode().GetOrder())
			},
		},
		{
			name: "session.attach", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH,
			request:     &agentrewire.SessionAttachRequest{ConversationId: convID(7)},
			newResponse: func() proto.Message { return &agentrewire.SessionAttachResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.SessionAttachResponse)
				require.Equal(t, convID(7), value.GetConversationId())
				require.Equal(t, "claudecode", value.GetBackendType())
				require.Equal(t, "idle", value.GetLifecycleState())
				require.Equal(t, int64(12), value.GetLatestSeq())
			},
		},
		{
			name: "runtime.run", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN,
			request:     &agentrewire.RuntimeRunRequest{ConversationId: convID(99), UserText: "go"},
			newResponse: func() proto.Message { return &agentrewire.RuntimeRunResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.RuntimeRunResponse)
				// 对话身份是**请求里**那一个:桌面端做宿主时从不改写它。
				require.Equal(t, convID(99), value.GetConversationId())
				require.Equal(t, int64(8), value.GetUserMessageSeq())
				require.Equal(t, int64(7), value.GetUserMessageMinSeq())
				// 桌面端这一侧从不填这三格 —— 它们是 agentred 的 RunAck 才有的东西。
				require.Empty(t, value.GetProviderSessionId())
				require.Empty(t, value.GetLaunchPermissionMode())
				require.Empty(t, value.GetProviderFallbackKey())
			},
		},
		{
			name: "runtime.steer", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER,
			request:     &agentrewire.RuntimeSteerRequest{ConversationId: convID(7), QueuedId: "browser-local-1", Text: "continue"},
			newResponse: func() proto.Message { return &agentrewire.RuntimeSteerResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.RuntimeSteerResponse)
				require.Equal(t, "desktop-42", value.GetQueuedId())
				require.True(t, value.GetCancellable())
			},
		},
		{
			name: "runtime.cancelSteer", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CANCEL_STEER,
			request:     &agentrewire.RuntimeCancelSteerRequest{ConversationId: convID(7), QueuedId: "desktop-42"},
			newResponse: func() proto.Message { return &agentrewire.RuntimeCancelSteerResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				require.Equal(t, []string{"desktop-42"}, response.(*agentrewire.RuntimeCancelSteerResponse).GetRemoved())
			},
		},
		{
			name: "runtime.submitAnswer", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER,
			request: &agentrewire.RuntimeSubmitAnswerRequest{
				ConversationId: convID(7), RequestId: "ask-1",
				Answers: []*agentrewire.AskAnswer{{QuestionIndex: 0, Labels: []string{"yes"}, OtherText: "why not"}},
			},
			newResponse: func() proto.Message { return &agentrewire.PeerSessionControlResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				// 桌面端这一侧把 chat_svc 的判定交回去 —— agentred 那一侧恒为 false。
				require.True(t, response.(*agentrewire.PeerSessionControlResponse).GetAlreadyHandled())
			},
		},
		{
			name: "runtime.submitToolPermission", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION,
			request:     &agentrewire.RuntimeSubmitToolPermissionRequest{ConversationId: convID(7), RequestId: "tool-1", Allow: true, AlwaysAllowSession: true},
			newResponse: func() proto.Message { return &agentrewire.PeerSessionControlResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				require.True(t, response.(*agentrewire.PeerSessionControlResponse).GetAlreadyHandled())
			},
		},
		{
			name: "runtime.setPermissionMode", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE,
			request:     &agentrewire.RuntimeSetPermissionModeRequest{ConversationId: convID(7), Mode: "plan"},
			newResponse: func() proto.Message { return &agentrewire.Empty{} },
			assert:      func(t *testing.T, _ proto.Message, err error) { require.NoError(t, err) },
		},
	}
}

// peerSessionWireRig 起一条 pipe 连接,authenticated 决定服务端这条连接认不认账号。
func peerSessionWireRig(t *testing.T, deps ProtobufInboundDeps, authenticated bool) (*protorpc.Conn, context.Context) {
	t.Helper()
	registry := NewProtobufInboundRegistry(deps)
	clientTransport, serverTransport := peerProtoPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, registry)
	if authenticated {
		server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: "sha256:caller"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)
	return client, ctx
}

// TestPeerSessionWire_GivenNoAuth_ThenAnswersLowercaseUnauthorized 钉住闸门这一格:
// 桌面端的拒绝语是码 -32001 + 小写 "unauthorized"。agentred 同一族答大写
// "Unauthorized" —— message 在线上,两者不可混。
func TestPeerSessionWire_GivenNoAuth_ThenAnswersLowercaseUnauthorized(t *testing.T) {
	for _, tc := range peerSessionWireCases() {
		t.Run(tc.name, func(t *testing.T) {
			client, ctx := peerSessionWireRig(t, peerSessionWireDeps(), false)
			err := protorpc.CallMessage(ctx, client, uint32(tc.method), tc.request, tc.newResponse())
			requirePeerWireError(t, err, -32001, "unauthorized")
		})
	}
}

// TestPeerSessionWire_GivenAuth_ThenAnswersTheRecordedShape 钉住已鉴权时每一条应答
// 的逐个字段 —— 少搬一格就红。
func TestPeerSessionWire_GivenAuth_ThenAnswersTheRecordedShape(t *testing.T) {
	for _, tc := range peerSessionWireCases() {
		t.Run(tc.name, func(t *testing.T) {
			client, ctx := peerSessionWireRig(t, peerSessionWireDeps(), true)
			response := tc.newResponse()
			err := protorpc.CallMessage(ctx, client, uint32(tc.method), tc.request, response)
			tc.assert(t, response, err)
		})
	}
}

// TestPeerSessionWire_GivenAMalformedConversationID_ThenAnswersInvalidParams 钉住
// 桌面端**独有**的那道前置校验:delete 与 attach 在动手之前先验对话 id。agentred
// 这两条上没有这一道 —— 它是宿主自己的事,所以留在端口实现里,不进共用注册面。
func TestPeerSessionWire_GivenAMalformedConversationID_ThenAnswersInvalidParams(t *testing.T) {
	for _, tc := range []struct {
		name     string
		method   agentrewire.RpcMethod
		request  proto.Message
		response proto.Message
	}{
		{"session.delete", agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE, &agentrewire.SessionDeleteRequest{ConversationId: "not-a-uuid"}, &agentrewire.SessionDeleteResponse{}},
		{"session.attach", agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH, &agentrewire.SessionAttachRequest{ConversationId: "not-a-uuid"}, &agentrewire.SessionAttachResponse{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, ctx := peerSessionWireRig(t, peerSessionWireDeps(), true)
			err := protorpc.CallMessage(ctx, client, uint32(tc.method), tc.request, tc.response)
			requirePeerWireError(t, err, -32602, "invalid conversation id")
		})
	}
}

// TestPeerSessionWire_GivenAMissingPeerSession_ThenAnswersMinus32002 钉住
// protobufPeerError 比 agentred 的 protobufError 多认的那一种:
// chat_svc.ErrPeerSessionNotFound 换来 -32002,而不是笼统的 -32603。两宿主的错误
// 接受集本就不同 —— 错误映射因此只能是端口,不能在共用注册面里统一掉。
func TestPeerSessionWire_GivenAMissingPeerSession_ThenAnswersMinus32002(t *testing.T) {
	deps := peerSessionWireDeps()
	deps.DeleteSession = func(context.Context, string, string) error { return chat_svc.ErrPeerSessionNotFound }
	client, ctx := peerSessionWireRig(t, deps, true)

	err := protorpc.CallMessage(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE),
		&agentrewire.SessionDeleteRequest{ConversationId: convID(7)}, &agentrewire.SessionDeleteResponse{})

	requirePeerWireError(t, err, -32002, chat_svc.ErrPeerSessionNotFound.Error())
}
