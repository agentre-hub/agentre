package peer_svc_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/peer"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/service/chat_svc"
	"github.com/agentre-hub/agentre/internal/service/peer_svc"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// 对端一条会话**空闲**时,插话(runtime.steer)没有轮次可插,对端一律回
// ErrNoActiveTurn —— 用户在 Peer Tab 里对着一条闲着的会话怎么发都发不出去。
// 控制台在这种局面下发的是 runtime.run:对端的 RunPeerSession 解析得出这条对话,
// 于是在**同一条**对话上起新一轮。桌面端出站此前缺这一条路。
//
// 判据是「同一条对话」:RunFresh 自己铸号,拿它顶替等于在对端另开一条会话,
// 用户绑着的那条仍然空着。
func TestPeerSvc_GivenIdleRemoteConversation_WhenRun_ThenFreshTurnLandsOnThatSameConversation(t *testing.T) {
	var got wire.RunParams
	var source chat_svc.PeerSessionSource
	url := fakePeerServer(t, peer.ProtobufInboundDeps{RunSession: func(_ context.Context, p wire.RunParams, src chat_svc.PeerSessionSource) (*chat_svc.SendResponse, error) {
		got = p
		source = src
		return &chat_svc.SendResponse{SessionID: 42}, nil
	}})
	svc, _ := newTestSvc(t, url)

	ack, err := svc.Run(context.Background(), peer_svc.RunRequest{
		Fingerprint:    "sha256:peer-desktop",
		ConversationID: convID(7),
		UserText:       "接着干",
	})
	require.NoError(t, err)

	assert.Equal(t, convID(7), got.ConversationID,
		"既有对话的身份必须原样过线 —— 铸个新号就是在对端另开一条会话")
	assert.False(t, got.FreshSession,
		"这条对话对端已经有了:声明「必须全新」会让别的执行端丢掉上下文")
	assert.Equal(t, "接着干", got.UserText)
	assert.Equal(t, devicefp.Initiator("sha256:local-desktop"), got.SourceDevice,
		"本机在这条通道上是发起方,这一轮的用户消息要记在本机名下")
	assert.Equal(t, devicefp.Initiator("sha256:test-peer"), source.Device,
		"入站那一侧仍按连接认下的身份盖章")
	assert.Equal(t, convID(7), ack.ConversationID)
}

// 空指纹 / 空对话号是调用方的 bug,不该变成一次拨号。
func TestPeerSvc_GivenMissingAddressing_WhenRun_ThenRejectsWithoutDialing(t *testing.T) {
	svc, _ := newTestSvc(t, fakePeerServer(t, peer.ProtobufInboundDeps{}))

	_, err := svc.Run(context.Background(), peer_svc.RunRequest{
		ConversationID: convID(7), UserText: "x",
	})
	require.Error(t, err)

	_, err = svc.Run(context.Background(), peer_svc.RunRequest{
		Fingerprint: "sha256:peer-desktop", UserText: "x",
	})
	require.Error(t, err)
}
