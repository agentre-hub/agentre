package peer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/protorpc"
)

// 控制台对桌面端会话的「停止」此前是一颗死按钮。
//
// SessionDetailHeader 无条件渲染它,按下去发 runtime.abort;桌面端不认识这个方法,
// -32601 被 :135 的 catch{} 吞掉 —— 界面上什么都不会发生,那一轮继续跑。桌面端
// 已经接得住同一条会话的 run / steer / 回答提问,唯独停不下来。
func TestDesktopServesRuntimeAbort(t *testing.T) {
	var stopped string
	deps := peerSessionWireDeps()
	deps.AbortSession = func(_ context.Context, conversationID string) error {
		stopped = conversationID
		return nil
	}
	client, ctx := peerSessionWireRig(t, deps, true)

	response := &agentrewire.RuntimeAbortResponse{}
	err := protorpc.CallMessage(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT),
		&agentrewire.RuntimeAbortRequest{ConversationId: convID(7)}, response)

	require.NoError(t, err, "桌面端答不出 runtime.abort:控制台上那颗停止键按下去毫无反应")
	require.Equal(t, convID(7), stopped, "停的不是被点名的那一条会话")
}

// 与会话族其余方法同一道闸门、同一句拒绝语。单独钉它,是因为这一格是**新加**的:
// 一个忘了套闸门的注册会让任何拨得进来的连接停掉别人正在跑的那一轮。
func TestDesktopRuntimeAbortRequiresAuth(t *testing.T) {
	deps := peerSessionWireDeps()
	deps.AbortSession = func(context.Context, string) error {
		t.Fatal("未鉴权的连接停掉了一轮")
		return nil
	}
	client, ctx := peerSessionWireRig(t, deps, false)

	err := protorpc.CallMessage(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT),
		&agentrewire.RuntimeAbortRequest{ConversationId: convID(7)}, &agentrewire.RuntimeAbortResponse{})

	requirePeerWireError(t, err, -32001, "unauthorized")
}

// 端口缺席 ⇒ 不注册,而不是回一个空成功。这是这一族的纪律:调用方据 method not found
// 判定「这台机器不管这事」;一个空成功会让它以为那一轮已经停了。
func TestDesktopRuntimeAbortAbsentPortIsNotRegistered(t *testing.T) {
	deps := peerSessionWireDeps()
	deps.AbortSession = nil
	client, ctx := peerSessionWireRig(t, deps, true)

	err := protorpc.CallMessage(ctx, client, uint32(agentrewire.RpcMethod_RPC_METHOD_RUNTIME_ABORT),
		&agentrewire.RuntimeAbortRequest{ConversationId: convID(7)}, &agentrewire.RuntimeAbortResponse{})

	require.ErrorContains(t, err, "method not found", "端口没装,方法却挂上了")
}
