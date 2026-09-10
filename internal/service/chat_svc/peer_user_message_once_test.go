package chat_svc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// Given 一条由带设备身份的对端(浏览器控制台 / 手机)提交进来的轮次;
// When  数一数开轮这一刻发给对端的帧里有几条 user_message;
// Then  只该有一条,而且是**持久帧** —— 用户那句话在这条会话上只说了一次。
//
// 桌面端做宿主时从前发两条:publishPeerMessageFrames 把用户那一行作为持久帧发出去,
// 紧接着又 publishPeerEvent 一条同样内容的 user_message **预览帧**(R18 的发起方
// 标记)。拿帧重建转录的那两个面(浏览器控制台、桌面端 Peer Tab)对每条 user_message
// 都新建一条用户消息、不去重,于是同一句话画出两条;预览那条还挂在预览尾巴上,要等
// 下一个持久帧才消失 —— 助手思考的整段时间里它就那么并排杵着。
//
// agentred 做宿主时是同一个毛病、同一条修法,那一侧钉在
// TestRuntime_Run_GivenPeerSubmittedTurn_ThenTheUserMessageGoesOutExactlyOnce。
func TestEmitTurnStarted_GivenPeerSubmittedTurn_ThenTheUserMessageGoesOutExactlyOnce(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()

	deps.session.EXPECT().Find(ctx, int64(41)).Return(&chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle"}, nil)
	deps.agent.EXPECT().Find(ctx, int64(7)).Return(agentForPeerSession(), nil)
	deps.backend.EXPECT().Find(ctx, int64(11)).Return(nil, nil)
	deps.message.EXPECT().List(ctx, int64(41)).Return(nil, nil)

	subscriber := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
	require.NoError(t, err)

	userMsg := &chat_entity.Message{
		ID: 71, SessionID: 41, Role: "user", Seq: 1,
		BlocksJSON: `[{"type":"text","data":{"text":"看看目录",` +
			`"sourceDevice":"fp-web","sourceDeviceName":"Chrome · macOS"}}]`,
	}
	ts := &turnStart{
		svc:     deps.svc,
		sess:    &chat_entity.Session{ID: 41, ConversationID: convID(41)},
		userMsg: userMsg,
		extras:  turnExtras{peerSource: peerMessageSource{Device: "fp-web", Name: "Chrome · macOS"}},
	}
	ts.emitTurnStarted(ctx, "chat:41:1")

	var got []wire.EventFrame
	require.Eventually(t, func() bool {
		got = got[:0]
		for _, record := range subscriber.notifications() {
			frame, ok := record.params.(wire.EventFrame)
			if !ok {
				continue
			}
			if _, isUser := frame.Event.(agentruntime.UserMessageEvent); isUser {
				got = append(got, frame)
			}
		}
		return len(got) >= 1
	}, time.Second, time.Millisecond, "开轮这一刻至少要把用户那句话发出去一次")

	require.Len(t, got, 1, "同一句话只该发一条 user_message 帧,得到 %d 条", len(got))
	assert.False(t, got[0].Preview, "留下的那条必须是持久帧:预览帧不带号、不进转录、也不参与补齐")
	assert.Equal(t, "fp-web", got[0].Event.(agentruntime.UserMessageEvent).SourceDevice,
		"来源随用户那一行走,不靠另发一条帧捎带")
}
