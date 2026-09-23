package peer

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo"
	"github.com/agentre-hub/agentre/internal/repository/chat_repo/mock_chat_repo"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 控制台答工具审批(spec 问题 5 / 决策 13):toolApproval.answer 按 (对话, requestId)
// 回答任意 toolKey 的 tool_approval 卡。桌面端作为 relay 目标时,这一答必须落到本机
// UI 批准时走的**同一个入口**(chat_svc.Chat().AnswerToolApproval,Wails 的
// AnswerToolApproval 绑定调的就是它)—— 另起一套审批存储,两边就会各自以为那张卡还挂着。
//
// 用例打在生产装配(productionProtobufInboundDeps)上:自己拼一份 deps 证明的只是
// 「我这条用例挂了什么」。

// approvalChat 是本机审批入口的替身:只有 AnswerToolApproval 是真的,其余方法一碰就
// nil panic —— 这一答若绕去别的方法,用例会当场炸出来。
type approvalChat struct {
	chat_svc.ChatSvc
	waiters  map[string]chan bool
	answered []int64
}

func (c *approvalChat) AnswerToolApproval(_ context.Context, sessionID int64, requestID string, allow bool) error {
	waiter, ok := c.waiters[requestID]
	if !ok {
		return fmt.Errorf("request %s not found", requestID)
	}
	delete(c.waiters, requestID)
	c.answered = append(c.answered, sessionID)
	waiter <- allow
	return nil
}

// registerToolApprovalChat 装上本机会话仓储(对话 1 → 本地主键 41,其余对话一律没有)
// 与给定的审批入口。
func registerToolApprovalChat(t *testing.T, chat chat_svc.ChatSvc) {
	t.Helper()
	ctrl := gomock.NewController(t)
	sessions := mock_chat_repo.NewMockSessionRepo(ctrl)
	sessions.EXPECT().FindByConversationID(gomock.Any(), convID(1)).
		Return(&chat_entity.Session{ID: 41, ConversationID: convID(1)}, nil).AnyTimes()
	sessions.EXPECT().FindByConversationID(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	prevChat, prevSessions := chat_svc.Chat(), chat_repo.Session()
	chat_repo.RegisterSession(sessions)
	chat_svc.RegisterChat(chat)
	t.Cleanup(func() {
		chat_svc.RegisterChat(prevChat)
		chat_repo.RegisterSession(prevSessions)
	})
}

func callToolApprovalAnswer(t *testing.T, authenticated bool, request *agentrewire.ToolApprovalAnswerRequest) error {
	t.Helper()
	client, ctx := peerSessionWireRig(t, productionProtobufInboundDeps(newDevicePortForward()), authenticated)
	return protorpc.CallMessage(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_TOOL_APPROVAL_ANSWER),
		request, &agentrewire.ToolApprovalAnswerResponse{})
}

func TestDesktopToolApprovalAnswer_GivenAPendingApproval_WhenTheConsoleAnswers_ThenTheLocalWaiterWakes(t *testing.T) {
	waiter := make(chan bool, 1)
	chat := &approvalChat{waiters: map[string]chan bool{"ctl-1": waiter}}
	registerToolApprovalChat(t, chat)

	err := callToolApprovalAnswer(t, true, &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: convID(1), RequestId: "ctl-1", Allow: true,
	})

	require.NoError(t, err)
	select {
	case allow := <-waiter:
		require.True(t, allow, "控制台批准了,挂起的调用却收到拒绝")
	default:
		t.Fatal("控制台答了,本机挂起的那次写工具调用没有被唤醒")
	}
	require.Equal(t, []int64{41}, chat.answered, "答到的不是被点名那条对话的本地会话")
}

func TestDesktopToolApprovalAnswer_GivenADeny_ThenTheWaiterReceivesFalse(t *testing.T) {
	waiter := make(chan bool, 1)
	registerToolApprovalChat(t, &approvalChat{waiters: map[string]chan bool{"org-1": waiter}})

	err := callToolApprovalAnswer(t, true, &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: convID(1), RequestId: "org-1", Allow: false,
	})

	require.NoError(t, err)
	require.False(t, <-waiter)
}

// 过期的 requestId 打在**真的**本机审批入口上:没有任何挂起的卡,答它必须是一条说得清
// 的错误,而不是空成功(控制台会以为批准生效了)。
func TestDesktopToolApprovalAnswer_GivenAStaleRequestID_ThenAnswersNoPendingApproval(t *testing.T) {
	registerToolApprovalChat(t, chat_svc.NewChat(chat_svc.NoopEmitter{}))

	err := callToolApprovalAnswer(t, true, &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: convID(1), RequestId: "gone", Allow: true,
	})

	requirePeerWireError(t, err, protorpc.CodeInvalidParams, `no pending tool approval "gone" in this session`)
}

func TestDesktopToolApprovalAnswer_GivenAnUnknownConversation_ThenAnswersSessionNotFound(t *testing.T) {
	chat := &approvalChat{waiters: map[string]chan bool{"ctl-1": make(chan bool, 1)}}
	registerToolApprovalChat(t, chat)

	err := callToolApprovalAnswer(t, true, &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: convID(404), RequestId: "ctl-1", Allow: true,
	})

	requirePeerWireError(t, err, -32002, chat_svc.ErrPeerSessionNotFound.Error())
	require.Empty(t, chat.answered, "对话不在本机,却有一张卡被答了")
}

func TestDesktopToolApprovalAnswer_GivenAMalformedConversationID_ThenAnswersInvalidParams(t *testing.T) {
	registerToolApprovalChat(t, &approvalChat{waiters: map[string]chan bool{}})

	err := callToolApprovalAnswer(t, true, &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: "not-a-conversation", RequestId: "ctl-1", Allow: true,
	})

	requirePeerWireError(t, err, protorpc.CodeInvalidParams, "invalid conversation id")
}

func TestDesktopToolApprovalAnswer_GivenNoAuth_ThenAnswersLowercaseUnauthorized(t *testing.T) {
	chat := &approvalChat{waiters: map[string]chan bool{"ctl-1": make(chan bool, 1)}}
	registerToolApprovalChat(t, chat)

	err := callToolApprovalAnswer(t, false, &agentrewire.ToolApprovalAnswerRequest{
		ConversationId: convID(1), RequestId: "ctl-1", Allow: true,
	})

	requirePeerWireError(t, err, -32001, "unauthorized")
	require.Empty(t, chat.answered, "未鉴权的连接答掉了一张审批卡")
}
