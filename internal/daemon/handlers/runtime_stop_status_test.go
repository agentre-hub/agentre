package handlers_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
