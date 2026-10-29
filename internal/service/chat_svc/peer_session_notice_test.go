package chat_svc

import (
	"context"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/chat_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
)

// Given 一位对端(浏览器控制台 / 桌面端 Peer Tab)已经 attach 在一条会话上;
// When  用户在会话里改了某个会话级设置(思考力度 / ModelTarget / CLI 模型),chat_svc
// 落下一条持久切换 notice,随后下一轮的用户消息开轮;
// Then  notice 作为**持久帧**立即发给对端,取到的号落在下一轮的用户消息之前 —— 补齐时
// 它就停在切换发生的地方。
//
// 从前 appendSessionNotice 只落库不发布:对端当场看不到 notice,而它的帧一直没号,
// 直到下一次建编号宇宙(宿主重启后的首次 attach)才由 NumberFrames 惰性补号 —— 补到的
// 是末尾号,于是那条 notice 被排到了之后那一整轮的回答后面。
//
// 三种切换共用 appendSessionNotice 这一个落库骨架,这里驱动其中两条公开入口;CLI 模型
// 那一条要起 runner,走的是同一个骨架。轮末偏离提示是那一轮自己 assistant 消息里的块,
// 随轮收口(PublishTurnDone)发布,不经这里。
func TestSessionSettingNotice_GivenAttachedPeer_ThenPublishedInPlaceBeforeTheNextTurn(t *testing.T) {
	cases := []struct {
		name   string
		sess   chat_entity.Session
		expect func(deps *peerSessionTestDeps, ctx context.Context)
		apply  func(svc *chatSvc, ctx context.Context) error
	}{
		{
			name: "思考力度切换",
			sess: chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE},
			expect: func(deps *peerSessionTestDeps, ctx context.Context) {
				deps.session.EXPECT().UpdateReasoningEffort(gomock.Any(), int64(41), "high").Return(nil)
			},
			apply: func(svc *chatSvc, ctx context.Context) error {
				_, err := svc.SetChatSessionReasoningEffort(ctx, &SetChatSessionReasoningEffortRequest{SessionID: 41, ReasoningEffort: "high"})
				return err
			},
		},
		{
			name: "ModelTarget 改回跟随 Agent 绑定",
			sess: chat_entity.Session{ID: 41, AgentID: 7, AgentStatus: "idle", Status: consts.ACTIVE, ProviderKey: "acme"},
			expect: func(deps *peerSessionTestDeps, ctx context.Context) {
				deps.session.EXPECT().UpdateModelTarget(gomock.Any(), int64(41), "", "").Return(nil)
			},
			apply: func(svc *chatSvc, ctx context.Context) error {
				_, err := svc.SetChatSessionModelTarget(ctx, &SetChatSessionModelTargetRequest{SessionID: 41})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := setupPeerSessionTest(t)
			ctx := context.Background()
			sess := tc.sess
			deps.session.EXPECT().Find(gomock.Any(), int64(41)).DoAndReturn(
				func(context.Context, int64) (*chat_entity.Session, error) {
					cp := sess
					return &cp, nil
				}).AnyTimes()
			deps.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
			deps.backend.EXPECT().Find(gomock.Any(), int64(11)).Return(&agent_backend_entity.AgentBackend{
				ID: 11, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
			}, nil).AnyTimes()
			// 落库的转录:attach 时为空,之后按建行顺序长出 notice 与下一轮的用户消息。
			var stored []*chat_entity.Message
			deps.message.EXPECT().List(gomock.Any(), int64(41)).DoAndReturn(
				func(context.Context, int64) ([]*chat_entity.Message, error) {
					return append([]*chat_entity.Message(nil), stored...), nil
				}).AnyTimes()
			deps.message.EXPECT().CreateAtNextSeq(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, msgs ...*chat_entity.Message) error {
					require.Len(t, msgs, 1)
					msgs[0].ID, msgs[0].Seq = 70, 1
					stored = append(stored, msgs[0])
					return nil
				}).Times(1)
			tc.expect(deps, ctx)

			subscriber := newRecordingPeerSubscriber()
			_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, subscriber)
			require.NoError(t, err)

			require.NoError(t, tc.apply(deps.svc, ctx))
			// 下一轮开轮:用户那一行作为持久帧发出(start_turn 的同一个发布点)。
			userMsg := &chat_entity.Message{
				ID: 71, SessionID: 41, Role: "user", Seq: 2,
				BlocksJSON: `[{"type":"text","data":{"text":"next"}}]`,
			}
			stored = append(stored, userMsg)
			_, userSeq := deps.svc.publishPeerMessageFrames(ctx, 41, userMsg, true)
			require.NotZero(t, userSeq)

			var notice *wire.EventFrame
			require.Eventually(t, func() bool {
				for _, record := range subscriber.notifications() {
					frame, ok := record.params.(wire.EventFrame)
					if !ok {
						continue
					}
					if block, isBlock := frame.Event.(agentruntime.UnrecognizedBlock); isBlock && block.BlockType == "notice" {
						notice = &frame
						return true
					}
				}
				return false
			}, time.Second, time.Millisecond, "已 attach 的对端当场就该收到这条切换 notice")
			assert.False(t, notice.Preview, "notice 必须是持久帧:预览帧不带号、不进补齐")
			assert.NotZero(t, notice.Seq)
			assert.Less(t, notice.Seq, userSeq, "notice 的号要排在它之后那一轮的用户消息之前")

			// 宿主重启:编号台账活下来,编号宇宙从落库的转录重建。补齐按号排序,notice 仍在
			// 下一轮之前 —— 从前它在这里才惰性补到末尾号。
			deps.svc = NewChat(NoopEmitter{}).(*chatSvc)
			rejoined := newRecordingPeerSubscriber()
			_, err = deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, rejoined)
			require.NoError(t, err)
			pulled, err := deps.svc.PullPeerSession(ctx, wire.SessionPullParams{ConversationID: convID(41), Limit: 100}, rejoined)
			require.NoError(t, err)
			var kinds []string
			for _, n := range pulled.Notifications {
				frame := n.Params.(*wire.EventFrame)
				switch event := frame.Event.(type) {
				case agentruntime.UnrecognizedBlock:
					kinds = append(kinds, "notice:"+event.BlockType)
				case agentruntime.UserMessageEvent:
					kinds = append(kinds, "user:"+event.Text)
				}
			}
			assert.Equal(t, []string{"notice:notice", "user:next"}, kinds, "重启后的补齐里 notice 停在切换发生的位置")
		})
	}
}
