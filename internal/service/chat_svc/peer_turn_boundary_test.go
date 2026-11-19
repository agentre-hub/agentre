package chat_svc

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
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
	"github.com/agentre-hub/agentre/pkg/wire/turnstate"
)

/*
桌面端承载一轮时,对端(浏览器控制台、server 的账号镜像)要看得见这一轮的开始与结束。

它们判「这一轮结束了」只认 runtime.runResultDone,判「开始了」认 runtime.turnStarted,
与 agentred 承载时一样。此前桌面端一帧都不发:派到桌面端跑完的一轮,控制台上停止键与
三点一直留着,账号镜像那一行永远是 running(2026-11-19 线上)。
*/

func TestPeerTurnEndFrame_GivenFinishedTurn_ThenCarriesTheTurnStats(t *testing.T) {
	frame := peerTurnEndFrame(&chat_entity.Message{
		Model: "claude-opus-5-5", DurationMs: 6743, FirstTokenMs: 5129, TokensPerSec: 9.9,
		PromptTokens: 2, CompletionTokens: 63, CachedTokens: 13125, CacheCreationTokens: 12485,
	}, nil)

	assert.Equal(t, "claude-opus-5-5", frame.Model)
	assert.Equal(t, 6743, frame.DurationMs)
	assert.Equal(t, 5129, frame.FirstTokenMs)
	assert.InDelta(t, 9.9, frame.TokensPerSec, 0.001)
	require.NotNil(t, frame.Usage)
	assert.Equal(t, wire.UsageWire{PromptTokens: 2, CompletionTokens: 63, CachedTokens: 13125, CacheCreationTokens: 12485}, *frame.Usage)
	assert.Empty(t, frame.StopErrMsg)
	assert.False(t, turnstate.IsFailure(frame.StopErrMsg, frame.StopErrCode))
}

func TestPeerTurnEndFrame_GivenFailedTurn_ThenReadsAsFailure(t *testing.T) {
	frame := peerTurnEndFrame(&chat_entity.Message{}, errors.New("backend exploded"))

	assert.Equal(t, "backend exploded", frame.StopErrMsg)
	assert.Nil(t, frame.Usage, "没有用量就不带用量,零值在对端会被读成「有这个数,值是 0」")
	assert.True(t, turnstate.IsFailure(frame.StopErrMsg, frame.StopErrCode))
}

// 用户点的停止不是故障:对端拿它画「已中断」,红只留给 failed。
func TestPeerTurnEndFrame_GivenAbortedTurn_ThenReadsAsInterruptedNotFailure(t *testing.T) {
	frame := peerTurnEndFrame(&chat_entity.Message{}, agentruntime.ErrAborted)

	assert.Equal(t, wire.ErrCodeAborted, frame.StopErrCode)
	assert.NotEmpty(t, frame.StopErrMsg)
	assert.False(t, turnstate.IsFailure(frame.StopErrMsg, frame.StopErrCode))
}

func expectRunningPeerSession(t *testing.T, deps *peerSessionTestDeps, sess chat_entity.Session) {
	t.Helper()
	deps.session.EXPECT().Find(gomock.Any(), sess.ID).DoAndReturn(
		func(context.Context, int64) (*chat_entity.Session, error) {
			cp := sess
			return &cp, nil
		}).AnyTimes()
	deps.agent.EXPECT().Find(gomock.Any(), int64(7)).Return(agentForPeerSession(), nil).AnyTimes()
	deps.backend.EXPECT().Find(gomock.Any(), int64(11)).Return(&agent_backend_entity.AgentBackend{
		ID: 11, Type: string(agent_backend_entity.TypeCodex), Status: consts.ACTIVE,
	}, nil).AnyTimes()
	deps.message.EXPECT().List(gomock.Any(), sess.ID).Return(nil, nil).AnyTimes()
}

func notificationsOf(sub *peerRecordingSubscriber, method string) []peerNotification {
	var out []peerNotification
	for _, n := range sub.notifications() {
		if n.method == method {
			out = append(out, n)
		}
	}
	return out
}

func TestEmitTurnStarted_GivenAttachedPeer_ThenPeerSeesTurnStartedBeforeTheUserMessage(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	sess := chat_entity.Session{ID: 41, ConversationID: convID(41), AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}
	expectRunningPeerSession(t, deps, sess)
	sub := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, sub)
	require.NoError(t, err)

	startUserTurn(ctx, deps.svc, sess, &chat_entity.Message{
		ID: 70, SessionID: 41, Role: "user", Seq: 1,
		BlocksJSON: `[{"type":"text","data":{"text":"你好"}}]`,
	}, &chat_entity.Message{ID: 71, SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: "[]"})

	var records []peerNotification
	require.Eventually(t, func() bool {
		records = sub.notifications()
		return len(records) >= 2
	}, time.Second, time.Millisecond)
	assert.Equal(t, wire.NotifyTurnStarted, records[0].method, "开轮帧排在用户那一行之前")
	assert.Equal(t, wire.TurnStartedFrame{ConversationID: convID(41)}, records[0].params)
	assert.Len(t, notificationsOf(sub, wire.NotifyTurnStarted), 1)
}

func TestFailTurn_GivenAttachedPeer_ThenPeerSeesFailedTerminalFrameAfterTheRowSettles(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	sess := chat_entity.Session{ID: 41, ConversationID: convID(41), AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}
	expectRunningPeerSession(t, deps, sess)
	sub := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, sub)
	require.NoError(t, err)

	deps.message.EXPECT().UpsertBlocks(gomock.Any(), gomock.Any(), gomock.Nil(), -1).Return(nil)
	deps.session.EXPECT().Update(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, row *chat_entity.Session) error {
			assert.Equal(t, "error", row.AgentStatus)
			assert.Empty(t, notificationsOf(sub, wire.NotifyRunResultDone),
				"终态帧必须在会话行落成终态之后才发:对端收到即查清单")
			return nil
		})

	running := sess
	failing := &chat_entity.Message{ID: 71, SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: "[]"}
	deps.svc.failTurn(ctx, &running, failing, StreamName(41, failing.ID), errors.New("backend exploded"))

	require.Eventually(t, func() bool { return len(notificationsOf(sub, wire.NotifyRunResultDone)) == 1 }, time.Second, time.Millisecond)
	frame := notificationsOf(sub, wire.NotifyRunResultDone)[0].params.(wire.RunResultDoneFrame)
	assert.Equal(t, convID(41), frame.ConversationID)
	assert.Equal(t, "backend exploded", frame.StopErrMsg)
	assert.True(t, turnstate.IsFailure(frame.StopErrMsg, frame.StopErrCode))
}

func TestAbortTurnBeforeStream_GivenAttachedPeer_ThenPeerSeesInterruptedTerminalFrame(t *testing.T) {
	deps := setupPeerSessionTest(t)
	ctx := context.Background()
	sess := chat_entity.Session{ID: 41, ConversationID: convID(41), AgentID: 7, AgentStatus: "running", Status: consts.ACTIVE}
	expectRunningPeerSession(t, deps, sess)
	sub := newRecordingPeerSubscriber()
	_, err := deps.svc.AttachPeerSession(ctx, wire.SessionAttachParams{ConversationID: convID(41)}, sub)
	require.NoError(t, err)

	deps.message.EXPECT().UpsertBlocks(gomock.Any(), gomock.Any(), gomock.Nil(), -1).Return(nil)
	deps.session.EXPECT().Update(gomock.Any(), gomock.Any()).Return(nil)

	running := sess
	msg := &chat_entity.Message{ID: 71, SessionID: 41, Role: "assistant", Seq: 2, BlocksJSON: "[]"}
	deps.svc.abortTurnBeforeStream(ctx, &running, msg, StreamName(41, msg.ID))

	require.Eventually(t, func() bool { return len(notificationsOf(sub, wire.NotifyRunResultDone)) == 1 }, time.Second, time.Millisecond)
	frame := notificationsOf(sub, wire.NotifyRunResultDone)[0].params.(wire.RunResultDoneFrame)
	assert.Equal(t, wire.ErrCodeAborted, frame.StopErrCode)
	assert.False(t, turnstate.IsFailure(frame.StopErrMsg, frame.StopErrCode))
}

/*
两条 finalize 各要一整套 runner / repo / 事件循环才跑得起来,要守的事实只有一句:
「终态帧发出去,而且排在会话行落成终态**之后**」—— 与同目录 peer_session_tee_test
同一手法。

排在之后是协议的次序保证(agentred 的 fanout 也是先 settleSession 再发帧):对端收到
终态帧会立刻查一次清单,那时看到的必须已经是 idle / error,而不是一个正在收尾的 running。
*/
func TestTurnFinishPaths_GivenPeerSubscribers_ThenTerminalFrameFollowsTheSettledRow(t *testing.T) {
	for _, tc := range []struct{ file, fn, publish string }{
		{file: "turn_run.go", fn: "finalize", publish: "publishPeerTurnEnded"},
		{file: "autonomous_turn_run.go", fn: "finalize", publish: "publishPeerAutonomousTurnEnded"},
	} {
		t.Run(tc.file+":"+tc.fn, func(t *testing.T) {
			source, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			file, err := parser.ParseFile(token.NewFileSet(), tc.file, source, 0)
			require.NoError(t, err)

			var firstPersist, firstPublish token.Pos
			publishes := 0
			ast.Inspect(file, func(node ast.Node) bool {
				decl, ok := node.(*ast.FuncDecl)
				if !ok || decl.Name.Name != tc.fn {
					return true
				}
				ast.Inspect(decl.Body, func(inner ast.Node) bool {
					call, ok := inner.(*ast.CallExpr)
					if !ok {
						return true
					}
					switch selectorName(call) {
					case "persistSessionStatus":
						if firstPersist == 0 {
							firstPersist = call.Pos()
						}
					case tc.publish:
						publishes++
						if firstPublish == 0 {
							firstPublish = call.Pos()
						}
					}
					return true
				})
				return false
			})
			require.NotZero(t, firstPublish, "%s 里的 %s 必须把终态帧发给对端订阅者(%s)", tc.file, tc.fn, tc.publish)
			require.NotZero(t, firstPersist)
			assert.Greater(t, firstPublish, firstPersist, "终态帧必须排在会话行落成终态之后")
		})
	}
}
