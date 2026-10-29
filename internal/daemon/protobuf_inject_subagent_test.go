package daemon

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
	"github.com/agentre-hub/agentre/pkg/wire/wirecall"
)

const injectRigConversation = "00000000-0000-7000-8000-000000000042"

// Given 一条未鉴权的连接
// When 经 wirecall.RuntimeInjectSubagentEvents 注入
// Then agentred 答 Unauthorized —— 这个口子写的是父转录,不对匿名连接开放
func TestDaemonInjectSubagentEvents_GivenNoAuth_ThenAnswersUnauthorized(t *testing.T) {
	client, _, ctx := sessionWireRig(t)

	_, err := wirecall.RuntimeInjectSubagentEvents(ctx, wirecall.On(client), &agentrewire.RuntimeInjectSubagentEventsRequest{
		ConversationId: injectRigConversation, ToolCallId: "toolu_sh",
	})

	requireWireError(t, err, -32001, "Unauthorized")
}

// Given 一条已鉴权的连接,会话里既没有在跑的轮也没有这张卡
// When 经 wirecall 注入一批事件
// Then 方法已注册、请求能被解开,应答是成功的 Empty(找不到卡不是错误)
func TestDaemonInjectSubagentEvents_GivenAuthAndUnknownCard_ThenSucceedsAsNoOp(t *testing.T) {
	client, server, ctx := sessionWireRig(t)
	server.SetAuth(protorpc.AuthState{Authenticated: true, DeviceFingerprint: string(rigDeviceFingerprint)})

	response, err := wirecall.RuntimeInjectSubagentEvents(ctx, wirecall.On(client), &agentrewire.RuntimeInjectSubagentEventsRequest{
		ConversationId: injectRigConversation, ToolCallId: "toolu_sh",
		Events: []*agentrewire.RuntimeEventNotification{{
			Event: &agentrewire.RuntimeEventNotification_SubagentStarted{SubagentStarted: &agentrewire.SubagentEvent{
				ToolCallId: "toolu_sh", Info: &agentrewire.SubagentInfo{Origin: "agrctl"},
			}},
		}},
	})

	require.NoError(t, err)
	require.NotNil(t, response)
}
