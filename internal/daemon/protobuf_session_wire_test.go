package daemon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 本文件是**表征测试**(characterization test):它不主张会话族这 16 个方法「应该」
// 怎么答,只钉住 agentred 此刻在线上答的是什么 —— 每一条都覆盖两件事:
//
//  1. 未鉴权时的错误码与 **message 原文**。agentred 走 requireProtobufAuth,
//     答的是 rpcerror.ErrUnauthorized,message 是大写 U 的 "Unauthorized";
//     桌面端走 wireinbound.Authenticated,答的是小写 "unauthorized"。两者码相同、
//     字面不同,而 message 也在线上 —— 这条差异是既存事实,不许在重构里被抹平。
//  2. 已鉴权时这一条应答长什么样:空库上要么是成功应答的字段,要么是那一条失败的
//     码 + message 原文(后者同时钉住了本宿主的错误映射 protobufError /
//     protobufRuntimeError 的取舍)。
//
// 会话族的正文正在从两个宿主收进 internal/pkg/wireinbound。这组用例是「零线上行为
// 变化」的证据:重构前后它必须一个字不改地绿。
type sessionWireCase struct {
	name    string
	method  agentrewire.RpcMethod
	request proto.Message
	// newResponse 造一个空应答容器 —— 成功路径上要按字段断言,所以不能只看 error。
	newResponse func() proto.Message
	// assert 在**已鉴权**连接上跑。
	assert func(t *testing.T, response proto.Message, err error)
}

// requireWireError 把一次调用的失败钉成 (码, message 原文) 二元组。
func requireWireError(t *testing.T, err error, code int32, message string) {
	t.Helper()
	var rpcErr *protorpc.Error
	require.ErrorAs(t, err, &rpcErr)
	require.Equal(t, code, rpcErr.Code)
	require.Equal(t, message, rpcErr.Message)
}

func sessionWireCases() []sessionWireCase {
	missing := convID(4242)
	return []sessionWireCase{
		{
			name: "session.list", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_LIST,
			request:     &agentrewire.SessionListRequest{Limit: 20},
			newResponse: func() proto.Message { return &agentrewire.SessionListResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.SessionListResponse)
				require.Empty(t, value.GetSessions())
				require.False(t, value.GetHasMore())
				require.Zero(t, value.GetTotal())
				require.Empty(t, value.GetCursor())
			},
		},
		{
			name: "session.counts", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_COUNTS,
			request:     &agentrewire.SessionCountsRequest{},
			newResponse: func() proto.Message { return &agentrewire.SessionCountsResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.SessionCountsResponse)
				require.Zero(t, value.GetTotal())
				require.Zero(t, value.GetRunning())
				require.Zero(t, value.GetWaiting())
			},
		},
		{
			name: "activity.rollup", method: agentrewire.RpcMethod_RPC_METHOD_ACTIVITY_ROLLUP,
			request:     &agentrewire.ActivityRollupRequest{},
			newResponse: func() proto.Message { return &agentrewire.ActivityRollupResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				require.Empty(t, response.(*agentrewire.ActivityRollupResponse).GetBuckets())
			},
		},
		{
			name: "session.pull", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_PULL,
			request:     &agentrewire.SessionPullRequest{ConversationId: missing, Limit: 10},
			newResponse: func() proto.Message { return &agentrewire.SessionPullResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				// 空库上「没有这条会话」不是错:补齐交出空的一页。
				require.NoError(t, err)
				value := response.(*agentrewire.SessionPullResponse)
				require.Empty(t, value.GetNotifications())
				require.False(t, value.GetHasMore())
			},
		},
		{
			name: "session.pendingWaiters", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_PENDING_WAITERS,
			request:     &agentrewire.SessionPendingWaitersRequest{ConversationId: missing},
			newResponse: func() proto.Message { return &agentrewire.SessionPendingWaitersResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.SessionPendingWaitersResponse)
				require.Empty(t, value.GetToolPermissions())
				require.Empty(t, value.GetAskUserQuestions())
			},
		},
		{
			name: "session.delete", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_DELETE,
			request:     &agentrewire.SessionDeleteRequest{ConversationId: missing},
			newResponse: func() proto.Message { return &agentrewire.SessionDeleteResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				require.True(t, response.(*agentrewire.SessionDeleteResponse).GetDeleted())
			},
		},
		{
			name: "session.setModelTarget", method: agentrewire.RpcMethod_RPC_METHOD_SET_MODEL_TARGET,
			request:     &agentrewire.SetModelTargetRequest{ConversationId: missing, ProviderKey: "p", ModelKey: "m"},
			newResponse: func() proto.Message { return &agentrewire.SetModelTargetResponse{} },
			assert: func(t *testing.T, _ proto.Message, err error) {
				requireWireError(t, err, -32002, "Session not found")
			},
		},
		{
			name: "session.setReasoningEffort", method: agentrewire.RpcMethod_RPC_METHOD_SET_SESSION_REASONING_EFFORT,
			request:     &agentrewire.SetSessionReasoningEffortRequest{ConversationId: missing, ReasoningEffort: "high"},
			newResponse: func() proto.Message { return &agentrewire.SetSessionReasoningEffortResponse{} },
			assert: func(t *testing.T, _ proto.Message, err error) {
				requireWireError(t, err, -32002, "Session not found")
			},
		},
		{
			name: "runtime.capabilities", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CAPABILITIES,
			request:     &agentrewire.RuntimeCapabilitiesRequest{BackendType: "claudecode"},
			newResponse: func() proto.Message { return &agentrewire.RuntimeCapabilitiesResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				value := response.(*agentrewire.RuntimeCapabilitiesResponse)
				require.NotEmpty(t, value.GetCapabilities())
				require.NotNil(t, value.GetPermissionMode())
			},
		},
		{
			name: "session.attach", method: agentrewire.RpcMethod_RPC_METHOD_SESSION_ATTACH,
			request:     &agentrewire.SessionAttachRequest{ConversationId: missing},
			newResponse: func() proto.Message { return &agentrewire.SessionAttachResponse{} },
			assert: func(t *testing.T, _ proto.Message, err error) {
				// protobufRuntimeError 先过 remotewire.ToRPCError,哨兵因此换来一个领域码。
				requireWireError(t, err, -32014, "agentruntime: provider session no longer exists")
			},
		},
		{
			name: "runtime.run", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_RUN,
			request:     &agentrewire.RuntimeRunRequest{ConversationId: missing, UserText: "go"},
			newResponse: func() proto.Message { return &agentrewire.RuntimeRunResponse{} },
			assert: func(t *testing.T, _ proto.Message, err error) {
				requireWireError(t, err, -32603, `backend "" not registered`)
			},
		},
		{
			name: "runtime.steer", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_STEER,
			request:     &agentrewire.RuntimeSteerRequest{ConversationId: missing, Text: "more"},
			newResponse: func() proto.Message { return &agentrewire.RuntimeSteerResponse{} },
			assert: func(t *testing.T, _ proto.Message, err error) {
				requireWireError(t, err, -32010, "agentruntime: no active turn for session")
			},
		},
		{
			name: "runtime.cancelSteer", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_CANCEL_STEER,
			request:     &agentrewire.RuntimeCancelSteerRequest{ConversationId: missing},
			newResponse: func() proto.Message { return &agentrewire.RuntimeCancelSteerResponse{} },
			assert: func(t *testing.T, _ proto.Message, err error) {
				requireWireError(t, err, -32010, "agentruntime: no active turn for session")
			},
		},
		{
			name: "runtime.submitAnswer", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_ANSWER,
			request: &agentrewire.RuntimeSubmitAnswerRequest{
				ConversationId: missing, RequestId: "ask-1",
				Questions: []*agentrewire.AskQuestion{{Id: "q1", Question: "?"}},
				Answers:   []*agentrewire.AskAnswer{{QuestionIndex: 0, Labels: []string{"yes"}}},
			},
			newResponse: func() proto.Message { return &agentrewire.PeerSessionControlResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				// agentred 从不填 already_handled —— rh.SubmitAnswer 交出的 wire.OK 里没有这一格。
				require.False(t, response.(*agentrewire.PeerSessionControlResponse).GetAlreadyHandled())
			},
		},
		{
			name: "runtime.submitToolPermission", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SUBMIT_TOOL_PERMISSION,
			request:     &agentrewire.RuntimeSubmitToolPermissionRequest{ConversationId: missing, RequestId: "tool-1", Allow: true},
			newResponse: func() proto.Message { return &agentrewire.PeerSessionControlResponse{} },
			assert: func(t *testing.T, response proto.Message, err error) {
				require.NoError(t, err)
				require.False(t, response.(*agentrewire.PeerSessionControlResponse).GetAlreadyHandled())
			},
		},
		{
			name: "runtime.setPermissionMode", method: agentrewire.RpcMethod_RPC_METHOD_RUNTIME_SET_PERMISSION_MODE,
			request:     &agentrewire.RuntimeSetPermissionModeRequest{ConversationId: missing, Mode: "plan"},
			newResponse: func() proto.Message { return &agentrewire.Empty{} },
			assert: func(t *testing.T, _ proto.Message, err error) {
				requireWireError(t, err, -32010, "agentruntime: no active turn for session")
			},
		},
	}
}

// sessionWireRig 起一台空 agentred 并绑好一条连接:会话族一半挂在 daemon registry 上
// (清单 / 计数 / 拉取 / 删除 / 改档),另一半挂在**连接** registry 上(runtime 族 +
// attach)。两处一起才是这一族在线上的全貌。
func sessionWireRig(t *testing.T) (*protorpc.Conn, *protorpc.Conn, context.Context) {
	t.Helper()
	daemon, err := New(Options{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { closeDB(daemon.db) })
	clientTransport, serverTransport := protobufTestPipePair()
	client := protorpc.NewConn(clientTransport, protorpc.NewRegistry())
	server := protorpc.NewConn(serverTransport, daemon.protobufRegistry.Clone())
	daemon.bindProtobufConn(server)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go client.Serve(ctx)
	go server.Serve(ctx)
	return client, server, ctx
}

// TestDaemonSessionWire_GivenNoAuth_ThenAnswersUppercaseUnauthorized 钉住闸门这一格:
// agentred 的拒绝语是 rpcerror.ErrUnauthorized —— 码 -32001,message 大写 "Unauthorized"。
// 桌面端同一族答的是小写 "unauthorized"。message 在线上,两者不可混。
func TestDaemonSessionWire_GivenNoAuth_ThenAnswersUppercaseUnauthorized(t *testing.T) {
	for _, tc := range sessionWireCases() {
		t.Run(tc.name, func(t *testing.T) {
			client, _, ctx := sessionWireRig(t)
			err := protorpc.CallMessage(ctx, client, uint32(tc.method), tc.request, tc.newResponse())
			requireWireError(t, err, -32001, "Unauthorized")
		})
	}
}

// TestDaemonSessionWire_GivenAuth_ThenAnswersTheRecordedShape 钉住已鉴权时每一条的
// 应答形状 —— 成功的按字段,失败的按 (码, message 原文)。
func TestDaemonSessionWire_GivenAuth_ThenAnswersTheRecordedShape(t *testing.T) {
	for _, tc := range sessionWireCases() {
		t.Run(tc.name, func(t *testing.T) {
			client, server, ctx := sessionWireRig(t)
			server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: string(rigDeviceFingerprint)})
			response := tc.newResponse()
			err := protorpc.CallMessage(ctx, client, uint32(tc.method), tc.request, response)
			tc.assert(t, response, err)
		})
	}
}
