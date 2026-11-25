package handlers_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/daemon/handlers"
	"github.com/agentre-hub/agentre/internal/daemon/handlers/mock_handlers"
	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/model/entity/transcript_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/transcript"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
	"github.com/agentre-hub/agentre/pkg/wire/rpcerror"
)

// runtime_steer_replay_test.go 钉住 spec 2026-11-25-agrctl-account-peer 的「会话正在运行时」
// 两条 agentred 侧行为(Problem 7 / Design 10 / Hard invariant「不重复发送」):
//   - 运行中的会话上不再开新的一轮,任何对端来都一样,返回「会话运行中」;
//   - 一轮收尾时还没送进去的插话由 agentred 自己按原顺序开下一轮送出,恰好一次。

// scriptedTurns 让一个 fullRT 的每次 Run 按顺序拿到用例准备好的那一轮:事件流由用例
// 关闭,RunResult 由用例在关闭前填写(与真 runtime 的契约一致)。
type scriptedTurns struct {
	mu    sync.Mutex
	turns []scriptedTurn
	next  int
}

type scriptedTurn struct {
	events chan agentruntime.Event
	result *agentruntime.RunResult
	// err 非 nil 时这一次 Run 直接失败(backend 起不来这一轮)。
	err error
}

func newScriptedTurns(n int) *scriptedTurns {
	s := &scriptedTurns{}
	for range n {
		s.turns = append(s.turns, scriptedTurn{events: make(chan agentruntime.Event, 4), result: &agentruntime.RunResult{}})
	}
	return s
}

func (s *scriptedTurns) run(context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.next >= len(s.turns) {
		return nil, nil, fmt.Errorf("scriptedTurns: unexpected Run #%d", s.next+1)
	}
	turn := s.turns[s.next]
	s.next++
	if turn.err != nil {
		return nil, nil, turn.err
	}
	return turn.events, turn.result, nil
}

// waitFor 等到生命周期步骤满足 done(按内容而不是按条数等:收尾那几步来自 fanout 协程)。
func (s *recordingSessions) waitFor(t *testing.T, done func(steps []string) bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for !done(s.steps()) {
		select {
		case <-s.stepC:
		case <-deadline:
			t.Fatalf("timed out waiting for lifecycle steps; got %v", s.steps())
		}
	}
}

// drainOnce 扮演真 SteerInbox 的 Drain 语义:取走即清空,第二次再问就是空的。
func drainOnce(steers ...agentruntime.ConsumedSteer) func(int64) []agentruntime.ConsumedSteer {
	var once sync.Once
	return func(int64) []agentruntime.ConsumedSteer {
		var out []agentruntime.ConsumedSteer
		once.Do(func() { out = steers })
		return out
	}
}

func (r *fullRT) runRequests() []agentruntime.RunRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]agentruntime.RunRequest, 0, len(r.runReqs))
	for _, call := range r.runReqs {
		out = append(out, call.req)
	}
	return out
}

func (r *fullRT) drainCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.drainArgs)
}

func frameIndex(frames []notifyFrame, match func(notifyFrame) bool) int {
	for i, f := range frames {
		if match(f) {
			return i
		}
	}
	return -1
}

func isMethod(method string) func(notifyFrame) bool {
	return func(f notifyFrame) bool { return f.method == method }
}

func claudeRunRequest(t *testing.T, conversationID string) *agentrewire.RuntimeRunRequest {
	t.Helper()
	be := agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode)}
	return &agentrewire.RuntimeRunRequest{Backend: backendProto(t, be), ConversationId: conversationID}
}

// Given 一条会话正在跑一轮, When 同一条连接或另一条连接(另一个对端)再来 runtime.run,
// Then 两次都被拒成「会话运行中」(CodeRuntimeSessionRunning),这一轮不交给 backend;
// 那一轮收尾之后再来,照常开新一轮。
func TestRuntime_Run_GivenTheConversationIsRunning_WhenAnyPeerStartsAnotherTurn_ThenItIsRejectedAsSessionRunning(t *testing.T) {
	turns := newScriptedTurns(2)
	rt := &fullRT{runFn: turns.run}
	notif, sess := newRecordingOutbound(), newRecordingSessions()
	first := newRuntimeHandlersOn(rt, sess, notif)
	// 另一条连接:生产上每条连接一个 handler,背后是同一个 Daemon 级的会话仓储。
	second := newRuntimeHandlersOn(rt, sess, notif)
	ctx := context.Background()
	cid := convID(61)

	runWithRT(t, first, ctx, cid)

	for name, h := range map[string]*handlers.RuntimeHandlers{"same connection": first, "another connection": second} {
		_, err := h.Run(ctx, claudeRunRequest(t, cid))
		require.Errorf(t, err, "%s: a running conversation must not take a new turn", name)
		rpcErr := wire.ToRPCError(err)
		require.NotNilf(t, rpcErr, "%s: the rejection must carry a typed code, got %v", name, err)
		assert.EqualValuesf(t, rpcerror.CodeRuntimeSessionRunning, rpcErr.Code, "%s", name)
		assert.ErrorIsf(t, wire.FromRPCError(rpcErr), wire.ErrSessionRunning, "%s: the code rehydrates to the sentinel", name)
	}
	require.Len(t, rt.runRequests(), 1, "a rejected turn never reaches the backend")

	close(turns.turns[0].events)
	sess.waitSteps(t, 2) // start + finish

	runWithRT(t, second, ctx, cid)
	assert.Len(t, rt.runRequests(), 2, "once the turn has ended the conversation takes a new turn again")
	close(turns.turns[1].events)
	sess.waitSteps(t, 4)
}

// Given 一轮跑着时排进来两条插话,backend 一条都没来得及送进去, When 这一轮正常收尾,
// Then agentred 自己按原顺序把它们合成下一轮开起来(恰好一次):
//   - 下一轮的提示是两条插话按顺序合并,续用上一轮 backend 给出的会话身份;
//   - 发起方先在本轮里看到这两条插话被消费(预览,清掉排队标记),再看到本轮结束,
//     然后看到 agentred 宣告的那一轮(autonomousTurn.started,trigger=steer);
//   - 会话行中途不落回 idle(下一轮已经被占住),下一轮里还能继续插话;
//   - 下一轮收尾时没有残留,不会再开第三轮。
func TestRuntime_Fanout_GivenSteersLeftWhenTheTurnEnds_ThenAgentredStartsTheNextTurnWithThemInOrderExactlyOnce(t *testing.T) {
	turns := newScriptedTurns(2)
	turns.turns[0].result.ProviderSessionID = "provider-session-1"
	rt := &fullRT{
		runFn: turns.run,
		drainFn: drainOnce(
			agentruntime.ConsumedSteer{QueuedID: "q1", Text: "first"},
			agentruntime.ConsumedSteer{QueuedID: "q2", Text: "second"},
		),
	}
	src := newFakeSteerSource()
	src.Record("q2", handlers.SteerSourceEntry{Peer: "sha256:phone", Name: "iPhone"})
	notif, sess := newRecordingOutbound(), newRecordingSessions()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor: notif.notifierFor, Sessions: sess, SessionQuery: sess, SteerSource: src,
		RuntimeFor: func(agent_backend_entity.BackendType) agentruntime.Runtime { return rt },
	})
	ctx := context.Background()
	cid := convID(62)
	req := claudeRunRequest(t, cid)
	req.Cwd, req.AgentId, req.UserText = "/work", 7, "the original question"
	_, err := h.Run(ctx, req)
	require.NoError(t, err)

	close(turns.turns[0].events)
	frames := notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
		return frameIndex(frames, isMethod(wire.NotifyAutonomousTurnStarted)) >= 0
	}, func() string { return "agentred to announce the steer replay turn" })

	runs := rt.runRequests()
	require.Len(t, runs, 2)
	next := runs[1]
	assert.Equal(t, "first\n\nsecond", next.UserText, "leftover steers are replayed in their original order")
	assert.Empty(t, next.UserBlocks)
	assert.Equal(t, handlers.RuntimeSessionKey(cid), next.SessionID)
	assert.Equal(t, cid, next.ConversationID)
	assert.Equal(t, "/work", next.Cwd)
	assert.Equal(t, int64(7), next.AgentID)
	assert.Equal(t, "provider-session-1", next.ProviderSessionID, "the next turn resumes the session the last one confirmed")

	consumed := frameIndex(frames, func(f notifyFrame) bool {
		ef, ok := f.params.(*wire.EventFrame)
		if !ok || f.method != wire.NotifyEvent || !ef.Preview {
			return false
		}
		_, ok = ef.Event.(agentruntime.SteerConsumed)
		return ok
	})
	done := frameIndex(frames, isMethod(wire.NotifyRunResultDone))
	started := frameIndex(frames, isMethod(wire.NotifyAutonomousTurnStarted))
	require.GreaterOrEqual(t, consumed, 0, "the originator learns the leftover steers were taken")
	require.Greater(t, done, consumed, "steers are reported consumed inside the turn that held them")
	require.Greater(t, started, done, "the replay turn is announced after the turn that held them ended")
	sc := frames[consumed].params.(*wire.EventFrame).Event.(agentruntime.SteerConsumed)
	require.Len(t, sc.Steers, 2)
	assert.Equal(t, "q1", sc.Steers[0].QueuedID)
	assert.Equal(t, "q2", sc.Steers[1].QueuedID)
	assert.Equal(t, devicefp.Initiator("sha256:phone"), sc.Steers[1].SourcePeer, "the submitter of each steer is kept")
	assert.Equal(t, "steer", frames[started].params.(*wire.AutonomousTurnStartedFrame).Trigger)

	assert.Zero(t, countStep(sess.steps(), "finish:"+cid), "the row stays running: the next turn already holds it")
	_, err = h.Run(ctx, claudeRunRequest(t, cid))
	assert.ErrorIs(t, err, wire.ErrSessionRunning, "nobody can squeeze a turn in between")

	_, err = h.Steer(ctx, &agentrewire.RuntimeSteerRequest{ConversationId: cid, QueuedId: "q3", Text: "third"})
	require.NoError(t, err, "the replay turn takes steers like any turn")

	close(turns.turns[1].events)
	notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
		return frameIndex(frames, isMethod(wire.NotifyAutonomousTurnDone)) >= 0
	}, func() string { return "the replay turn to finish" })
	sess.waitFor(t, func(steps []string) bool { return countStep(steps, "finish:"+cid) == 1 })
	assert.Equal(t, 2, rt.drainCount(), "each turn end asks once")
	assert.Len(t, rt.runRequests(), 2, "an empty inbox starts no third turn")
}

// Given 一轮以故障或用户停止收场,排队里还有插话, When 这一轮收尾, Then agentred
// 不替用户续跑:不去取残留,也不开下一轮(与桌面端「abort 路径跳过自动接续」同一条)。
func TestRuntime_Fanout_GivenTheTurnFailedOrWasStopped_WhenItEnds_ThenLeftoverSteersAreNotReplayed(t *testing.T) {
	for name, stopErr := range map[string]error{
		"failed":  errors.New("upstream exploded"),
		"stopped": agentruntime.ErrAborted,
	} {
		t.Run(name, func(t *testing.T) {
			turns := newScriptedTurns(1)
			turns.turns[0].result.StopErr = stopErr
			rt := &fullRT{runFn: turns.run, drainFn: drainOnce(agentruntime.ConsumedSteer{QueuedID: "q1", Text: "later"})}
			ctx, notif, sess, h := setupRuntimeTestWithSessions(t, rt)
			cid := convID(63)
			runWithRT(t, h, ctx, cid)

			close(turns.turns[0].events)
			notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
				return frameIndex(frames, isMethod(wire.NotifyRunResultDone)) >= 0
			}, func() string { return "the turn to end" })
			sess.waitSteps(t, 2)

			assert.Zero(t, rt.drainCount())
			assert.Len(t, rt.runRequests(), 1)
			assert.Negative(t, frameIndex(notif.snapshot(), isMethod(wire.NotifyAutonomousTurnStarted)))
		})
	}
}

// Given 残留插话已经取出,但下一轮开不起来, When 这一轮收尾, Then 会话照常落回
// idle(不会卡在 running 再也开不了轮),本轮照常结束,不宣告一个不存在的下一轮。
func TestRuntime_Fanout_GivenTheReplayTurnCannotStart_WhenTheTurnEnds_ThenTheConversationSettles(t *testing.T) {
	turns := newScriptedTurns(3)
	turns.turns[1].err = errors.New("claude: spawn failed")
	rt := &fullRT{runFn: turns.run, drainFn: drainOnce(agentruntime.ConsumedSteer{QueuedID: "q1", Text: "later"})}
	ctx, notif, sess, h := setupRuntimeTestWithSessions(t, rt)
	cid := convID(64)
	runWithRT(t, h, ctx, cid)

	close(turns.turns[0].events)
	notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
		return frameIndex(frames, isMethod(wire.NotifyRunResultDone)) >= 0
	}, func() string { return "the turn to end" })
	sess.waitFor(t, func(steps []string) bool { return countStep(steps, "finish:"+cid) == 1 })

	assert.Len(t, rt.runRequests(), 2, "the replay was attempted")
	assert.Negative(t, frameIndex(notif.snapshot(), isMethod(wire.NotifyAutonomousTurnStarted)))
	runWithRT(t, h, ctx, cid) // 会话没有卡在 running:新一轮照常开得起来
	close(turns.turns[2].events)
	sess.waitFor(t, func(steps []string) bool { return countStep(steps, "finish:"+cid) == 2 })
}

// Given 两条插话由不同对端提交、一轮收尾时都还没送进去, When agentred 续跑下一轮,
// Then 那一轮的转录以**一行用户消息**起手:正文是两条插话按原顺序合并,来源取第一条
// 带来源的插话(与桌面端补发本机会话时合并成的那一行同一个形状)。
func TestRuntime_Fanout_GivenSteersLeftWhenTheTurnEnds_ThenTheReplayTurnTranscriptOpensWithThemAsOneUserMessage(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	tp := mock_handlers.NewMockTranscriptPort(ctrl)
	turnMessages := func(id int64) (*transcript_entity.Message, *transcript_entity.Message, error) {
		return &transcript_entity.Message{ID: id, SessionID: 5, Role: "user", Seq: int(id), BlocksJSON: "[]"},
			&transcript_entity.Message{ID: id + 1, SessionID: 5, Role: "assistant", Seq: int(id) + 1, BlocksJSON: "[]"}, nil
	}
	gomock.InOrder(
		tp.EXPECT().StartTurn(gomock.Any(), gomock.Any(), "the original question", gomock.Any(), transcript.UserSource{}).
			Return(turnMessages(1)),
		tp.EXPECT().StartTurn(gomock.Any(), gomock.Any(), "first\n\nsecond", gomock.Nil(),
			transcript.UserSource{Device: "sha256:phone", Name: "iPhone"}).
			Return(turnMessages(3)),
	)
	tp.EXPECT().AllocateFrameSeqs(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("frames not under test")).AnyTimes()
	tp.EXPECT().Checkpoint(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	tp.EXPECT().UpsertBlocks(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	tp.EXPECT().FinishTurn(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	turns := newScriptedTurns(2)
	rt := &fullRT{runFn: turns.run, drainFn: drainOnce(
		agentruntime.ConsumedSteer{QueuedID: "q1", Text: "first"},
		agentruntime.ConsumedSteer{QueuedID: "q2", Text: "second"},
	)}
	src := newFakeSteerSource()
	src.Record("q2", handlers.SteerSourceEntry{Peer: "sha256:phone", Name: "iPhone"})
	notif, sess := newRecordingOutbound(), newRecordingSessions()
	h := handlers.NewRuntimeHandlers(handlers.RuntimeDeps{
		NotifyFor: notif.notifierFor, Sessions: sess, SessionQuery: sess, SteerSource: src, Transcript: tp,
		RuntimeFor: func(agent_backend_entity.BackendType) agentruntime.Runtime { return rt },
	})
	cid := convID(65)
	req := claudeRunRequest(t, cid)
	req.UserText = "the original question"
	_, err := h.Run(context.Background(), req)
	require.NoError(t, err)

	close(turns.turns[0].events)
	notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
		return frameIndex(frames, isMethod(wire.NotifyAutonomousTurnStarted)) >= 0
	}, func() string { return "agentred to announce the steer replay turn" })
	close(turns.turns[1].events)
	sess.waitFor(t, func(steps []string) bool { return countStep(steps, "finish:"+cid) == 1 })
}
