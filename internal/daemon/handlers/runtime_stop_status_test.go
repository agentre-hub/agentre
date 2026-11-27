package handlers_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
	"github.com/agentre-hub/agentre/pkg/wire/turnstate"
)

// Given 用户(agrctl / 桌面 / 控制台)对一轮发了 runtime.abort, When backend 回的是 Claude CLI
// 软中断的收尾(一条不带 sentinel 的错误,码 0), Then 会话落 idle、终态帧带停止 sentinel,
// 与桌面端「用户按的停止不算故障」同一句话;没有 abort 的同样错误仍是故障 failed。
func TestRuntime_Fanout_GivenTheTurnWasAborted_WhenBackendEndsWithAnUnclassifiedError_ThenSettlesIdle(t *testing.T) {
	interrupt := errors.New("[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null")
	for name, tc := range map[string]struct {
		abort    bool
		wantStep string
		wantCode int
	}{
		"stopped by user": {abort: true, wantStep: "finish:", wantCode: turnstate.AbortedCode},
		"genuine failure": {abort: false, wantStep: "fail:", wantCode: 0},
	} {
		t.Run(name, func(t *testing.T) {
			turns := newScriptedTurns(1)
			turns.turns[0].result.StopErr = interrupt
			rt := &fullRT{runFn: turns.run, abortOutcome: agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}}
			ctx, notif, sess, h := setupRuntimeTestWithSessions(t, rt)
			cid := convID(71)
			runWithRT(t, h, ctx, cid)

			if tc.abort {
				_, err := h.Abort(ctx, &agentrewire.RuntimeAbortRequest{ConversationId: cid})
				require.NoError(t, err)
			}
			close(turns.turns[0].events)
			notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
				return frameIndex(frames, isMethod(wire.NotifyRunResultDone)) >= 0
			}, func() string { return "the turn to end" })
			steps := sess.waitSteps(t, 2)

			assert.Contains(t, steps[len(steps)-1], tc.wantStep)
			frames := notif.snapshot()
			done := frames[frameIndex(frames, isMethod(wire.NotifyRunResultDone))]
			frame, ok := done.params.(*wire.RunResultDoneFrame)
			require.True(t, ok, "params %T", done.params)
			assert.Equal(t, tc.wantCode, frame.StopErrCode)
		})
	}
}

// Given 用户对一轮发了 runtime.abort, When backend 的收尾(软中断的未分类错误)抢在中断回执
// 之前就到了(Claude CLI 的 result 帧与 control_response 并无先后保证), Then 这一轮仍按用户
// 停止落 idle —— 「这一轮被要求停止」必须在下发中断之前记下,不能等回执回来再补。
func TestRuntime_Fanout_GivenTheTurnEndsBeforeTheAbortReturns_ThenItStillSettlesIdle(t *testing.T) {
	turns := newScriptedTurns(1)
	turns.turns[0].result.StopErr = errors.New("[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null")
	rt := &fullRT{runFn: turns.run, abortOutcome: agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}}
	ctx, notif, sess, h := setupRuntimeTestWithSessions(t, rt)
	cid := convID(72)
	runWithRT(t, h, ctx, cid)
	rt.abortFn = func() {
		close(turns.turns[0].events)
		notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
			return frameIndex(frames, isMethod(wire.NotifyRunResultDone)) >= 0
		}, func() string { return "the turn to end before the abort returns" })
	}

	_, err := h.Abort(ctx, &agentrewire.RuntimeAbortRequest{ConversationId: cid})
	require.NoError(t, err)
	steps := sess.waitSteps(t, 2)

	assert.Contains(t, steps[len(steps)-1], "finish:")
	frames := notif.snapshot()
	frame, ok := frames[frameIndex(frames, isMethod(wire.NotifyRunResultDone))].params.(*wire.RunResultDoneFrame)
	require.True(t, ok)
	assert.Equal(t, turnstate.AbortedCode, frame.StopErrCode)
}

// Given 一次 runtime.abort 被 backend 判为「没有可停的一轮」(过期的轮 token,结果 TurnKindNone),
// When 当前这一轮随后以未分类错误收场, Then 它仍是故障 failed —— 没打断任何东西的 abort
// 不能把下一处真故障洗成用户停止。
func TestRuntime_Fanout_GivenAnAbortThatInterruptedNothing_WhenTheTurnFails_ThenItStaysFailed(t *testing.T) {
	turns := newScriptedTurns(1)
	turns.turns[0].result.StopErr = errors.New("upstream exploded")
	rt := &fullRT{runFn: turns.run, abortOutcome: agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}}
	ctx, notif, sess, h := setupRuntimeTestWithSessions(t, rt)
	cid := convID(73)
	runWithRT(t, h, ctx, cid)

	_, err := h.Abort(ctx, &agentrewire.RuntimeAbortRequest{ConversationId: cid, TurnToken: 99})
	require.NoError(t, err)
	close(turns.turns[0].events)
	notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
		return frameIndex(frames, isMethod(wire.NotifyRunResultDone)) >= 0
	}, func() string { return "the turn to end" })
	steps := sess.waitSteps(t, 2)

	assert.Contains(t, steps[len(steps)-1], "fail:")
}

// Given 一次 runtime.abort 已经把中断下发到这一轮(backend 回 TurnKindUser), When 另一个调用方
// (控制台 / 另一台 agrctl)随后再发一次、却被 backend 判成没有可停的一轮(过期的轮 token),
// Then 这一轮的软中断收尾仍按用户停止落 idle —— 没打断任何东西的那一次只撤回它自己,
// 不能把已经下发的那一次一并撤掉。
func TestRuntime_Fanout_GivenADeliveredAbort_WhenALaterAbortInterruptsNothing_ThenTheTurnStillSettlesIdle(t *testing.T) {
	turns := newScriptedTurns(1)
	turns.turns[0].result.StopErr = errors.New("[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null")
	rt := &fullRT{runFn: turns.run, abortOutcome: agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}}
	ctx, notif, sess, h := setupRuntimeTestWithSessions(t, rt)
	cid := convID(74)
	runWithRT(t, h, ctx, cid)

	_, err := h.Abort(ctx, &agentrewire.RuntimeAbortRequest{ConversationId: cid})
	require.NoError(t, err)
	rt.mu.Lock()
	rt.abortOutcome = agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}
	rt.mu.Unlock()
	_, err = h.Abort(ctx, &agentrewire.RuntimeAbortRequest{ConversationId: cid, TurnToken: 99})
	require.NoError(t, err)
	close(turns.turns[0].events)
	notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
		return frameIndex(frames, isMethod(wire.NotifyRunResultDone)) >= 0
	}, func() string { return "the turn to end" })
	steps := sess.waitSteps(t, 2)

	assert.Contains(t, steps[len(steps)-1], "finish:")
	frames := notif.snapshot()
	frame, ok := frames[frameIndex(frames, isMethod(wire.NotifyRunResultDone))].params.(*wire.RunResultDoneFrame)
	require.True(t, ok)
	assert.Equal(t, turnstate.AbortedCode, frame.StopErrCode)
}

// Given 一轮跑着时排进了插话, When 用户的 runtime.abort 正在路上、这一轮恰好正常收场(中断
// 回执还没回来), Then agentred 不替用户续跑那几条插话 —— 停止请求就是「别再往下跑」,与
// 桌面端「abort 路径跳过自动接续」同一条;插话留在 backend 里,下一轮照常带上。
func TestRuntime_Fanout_GivenAStopIsInFlightWhenTheTurnEndsCleanly_ThenLeftoverSteersAreNotReplayed(t *testing.T) {
	turns := newScriptedTurns(2)
	rt := &fullRT{
		runFn:        turns.run,
		drainFn:      drainOnce(agentruntime.ConsumedSteer{QueuedID: "q1", Text: "later"}),
		abortOutcome: agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser},
	}
	ctx, notif, sess, h := setupRuntimeTestWithSessions(t, rt)
	cid := convID(75)
	runWithRT(t, h, ctx, cid)
	rt.abortFn = func() {
		close(turns.turns[0].events)
		notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
			return frameIndex(frames, isMethod(wire.NotifyRunResultDone)) >= 0
		}, func() string { return "the turn to end before the abort returns" })
	}

	_, err := h.Abort(ctx, &agentrewire.RuntimeAbortRequest{ConversationId: cid})
	require.NoError(t, err)
	assert.Zero(t, rt.drainCount(), "a stopped turn does not take the queued steers")
	sess.waitFor(t, func(steps []string) bool { return countStep(steps, "finish:"+cid) == 1 })
	assert.Len(t, rt.runRequests(), 1, "no replay turn after a stop")
	assert.Negative(t, frameIndex(notif.snapshot(), isMethod(wire.NotifyAutonomousTurnStarted)))
	close(turns.turns[1].events)
}

// Given 一轮正常收场、残留插话由 agentred 续成下一轮, When 用户的 runtime.abort(同一条连接,
// 或 attach 进来的另一条连接)恰好落在续跑那一轮正在开起来的时候, Then 续跑那一轮的软中断
// 收尾(码 0 诊断)仍按用户停止落 idle —— 停止记号落在续跑那一轮身上,而不是已经收场的上一轮,
// 也不是一个谁都不看的临时记号。
func TestRuntime_Fanout_GivenAStopLandsWhileTheSteerReplayStarts_ThenTheReplaySettlesIdle(t *testing.T) {
	for name, viaAnotherConnection := range map[string]bool{"same connection": false, "another connection": true} {
		t.Run(name, func(t *testing.T) {
			turns := newScriptedTurns(2)
			turns.turns[1].result.StopErr = errors.New("[ede_diagnostic] result_type=user last_content_type=n/a stop_reason=null")
			replayStarting, release := make(chan struct{}), make(chan struct{})
			calls := 0
			rt := &fullRT{
				drainFn:      drainOnce(agentruntime.ConsumedSteer{QueuedID: "q1", Text: "later"}),
				abortOutcome: agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser},
			}
			rt.runFn = func(ctx context.Context) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
				calls++
				if calls == 2 {
					close(replayStarting)
					<-release
				}
				return turns.run(ctx)
			}
			notif, sess := newRecordingOutbound(), newRecordingSessions()
			h := newRuntimeHandlersOn(rt, sess, notif)
			ctx := context.Background()
			cid := convID(76)
			runWithRT(t, h, ctx, cid)
			stopper := h
			if viaAnotherConnection {
				stopper = newRuntimeHandlersOn(rt, sess, notif)
				stopper.Adopt(ctx, cid, agent_backend_entity.TypeClaudeCode)
			}

			close(turns.turns[0].events)
			<-replayStarting
			_, err := stopper.Abort(ctx, &agentrewire.RuntimeAbortRequest{ConversationId: cid})
			require.NoError(t, err)
			close(release)
			close(turns.turns[1].events)

			frames := notif.waitFramesUntil(t, func(frames []notifyFrame) bool {
				return frameIndex(frames, isMethod(wire.NotifyAutonomousTurnDone)) >= 0
			}, func() string { return "the replay turn to end" })
			frame, ok := frames[frameIndex(frames, isMethod(wire.NotifyAutonomousTurnDone))].params.(*wire.RunResultDoneFrame)
			require.True(t, ok)
			assert.Equal(t, turnstate.AbortedCode, frame.StopErrCode)
			sess.waitFor(t, func(steps []string) bool { return countStep(steps, "finish:"+cid) == 1 })
			assert.Zero(t, countStep(sess.steps(), "fail:"+cid), "a stopped replay is not a failure")
		})
	}
}
