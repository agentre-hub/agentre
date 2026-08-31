package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/mock_agentruntime"
	piagentrt "github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/piagent"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/protowire"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/runtimes/remote/wire"
	"github.com/agentre-hub/agentre/internal/pkg/orderedpipe"
	"github.com/agentre-hub/agentre/internal/pkg/protorpctest"
	"github.com/agentre-hub/agentre/internal/pkg/rpcerror"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// Compile-time guard: *Runtime must satisfy the full Runtime contract +
// every optional sub-interface. Adding a new sub-interface to agentruntime
// without implementing it here fails the build instead of silently being
// downgraded by chat_svc's type assertions.
var (
	_ agentruntime.Runtime              = (*Runtime)(nil)
	_ agentruntime.Steerer              = (*Runtime)(nil)
	_ agentruntime.SteerCanceler        = (*Runtime)(nil)
	_ agentruntime.SteerDrainer         = (*Runtime)(nil)
	_ agentruntime.Aborter              = (*Runtime)(nil)
	_ agentruntime.PermissionModeSetter = (*Runtime)(nil)
	_ agentruntime.AskAnswerSink        = (*Runtime)(nil)
	_ agentruntime.ToolPermissionSink   = (*Runtime)(nil)
	_ agentruntime.GoalController       = (*Runtime)(nil)
	_ piagentrt.RunPreparer             = (*Runtime)(nil)
)

// handlerCapture grabs the Handle("runtime.event"|"runtime.runResultDone")
// callbacks that *Runtime registers on the conn during New(), so tests can
// drive server-push notifications synchronously.
type handlerCapture struct {
	mu    sync.Mutex
	funcs map[string]func(context.Context, json.RawMessage) (any, error)
}

func newHandlerCapture() *handlerCapture {
	return &handlerCapture{funcs: map[string]func(context.Context, json.RawMessage) (any, error){}}
}

func (h *handlerCapture) record(method string, fn func(context.Context, json.RawMessage) (any, error)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.funcs[method] = fn
}

func (h *handlerCapture) deliver(t *testing.T, method string, payload any) {
	t.Helper()
	h.deliverContext(t, context.Background(), method, payload)
}

func (h *handlerCapture) deliverContext(t *testing.T, ctx context.Context, method string, payload any) {
	t.Helper()
	h.mu.Lock()
	fn, ok := h.funcs[method]
	h.mu.Unlock()
	require.True(t, ok, "no handler captured for %s", method)
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	_, err = fn(ctx, raw)
	require.NoError(t, err)
}

func setupRemote(t *testing.T) (
	*gomock.Controller,
	*mock_agentruntime.MockDaemonClientPort,
	*handlerCapture,
	*Runtime,
) {
	t.Helper()
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	cli := mock_agentruntime.NewMockDaemonClientPort(ctrl)
	capture := newHandlerCapture()
	cli.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
		func(method string, fn func(context.Context, json.RawMessage) (any, error)) {
			capture.record(method, fn)
		}).AnyTimes()
	// Closed() 由 New() 调用一次起 watchClose goroutine;返回 nil 等价于"不监
	// 听断连"——单测不需要触发断连分支,默认走纯 RPC 路径。
	cli.EXPECT().Closed().Return(nil).AnyTimes()
	rt := New(protorpctest.WrapConnection(cli), WithConversationIDResolver(convOf))
	return ctrl, cli, capture, rt
}

// ── Run ─────────────────────────────────────────────────────────────────────

func TestPrepareRun_PiExposesIdentityBeforePromptAndStartsThroughExistingRunRPC(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)

	var (
		mu       sync.Mutex
		runCalls []wire.RunParams
	)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			rp := params.(wire.RunParams)
			mu.Lock()
			runCalls = append(runCalls, rp)
			call := len(runCalls)
			mu.Unlock()
			switch call {
			case 1:
				assert.Equal(t, "pi-session-old", rp.ProviderSessionID)
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
			case 2:
				assert.Equal(t, "pi-session-old", rp.ProviderSessionID)
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "pi-session-new"}
			case 3:
				assert.Equal(t, "pi-session-new", rp.ProviderSessionID,
					"Start must identify the exact prepared generation through the existing provider-session form")
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "pi-session-new"}
			default:
				t.Fatalf("unexpected runtime.run call %d", call)
			}
			return nil
		}).Times(3)

	prepared, err := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
		Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
		SessionID:         42,
		ProviderSessionID: "pi-session-old",
		ForkAnchor:        "pi-entry-1",
		UserText:          "replacement",
	})
	require.NoError(t, err)
	identity, ok := prepared.(piagentrt.PreparedRunIdentity)
	require.True(t, ok)
	assert.Equal(t, "pi-session-new", identity.ProviderSessionID())
	mu.Lock()
	assert.Len(t, runCalls, 2, "registration + preparation must return before the prompt-start RPC")
	mu.Unlock()

	events, result, err := prepared.Start(context.Background())
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "pi-session-new", result.ProviderSessionID)
	mu.Lock()
	assert.Len(t, runCalls, 3)
	require.NotEmpty(t, runCalls[0].PermissionMode)
	assert.Equal(t, runCalls[0].PermissionMode, runCalls[1].PermissionMode)
	assert.Equal(t, runCalls[1].PermissionMode, runCalls[2].PermissionMode)
	mu.Unlock()

	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID: convOf(42), ProviderSessionID: "pi-session-new",
	})
	_, ok = <-events
	assert.False(t, ok)
}

func TestPrepareRun_PreAckBurstReturnsAckBeforeConsumerDrainsAndPreservesOrder(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)
	const burst = 96

	call := 0
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			call++
			rp := params.(wire.RunParams)
			switch call {
			case 1:
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
			case 2:
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "pi-session-burst"}
			case 3:
				for i := 0; i < burst; i++ {
					capture.deliver(t, wire.NotifyEvent, wire.EventFrame{
						ConversationID: rp.ConversationID,
						Event:          agentruntime.TextDelta{Text: fmt.Sprintf("event-%03d", i)},
					})
				}
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "pi-session-burst"}
			default:
				t.Fatalf("unexpected runtime.run call %d", call)
			}
			return nil
		}).Times(3)

	prepared, err := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
		SessionID: 164,
	})
	require.NoError(t, err)

	type startResult struct {
		events <-chan agentruntime.Event
		result *agentruntime.RunResult
		err    error
	}
	started := make(chan startResult, 1)
	go func() {
		events, result, startErr := prepared.Start(context.Background())
		started <- startResult{events: events, result: result, err: startErr}
	}()

	var got startResult
	select {
	case got = <-started:
	case <-time.After(time.Second):
		// Release the historical direct-send deadlock so the failed RED leaves no
		// parked goroutine or mock expectation behind.
		rt.mu.RLock()
		blocked := rt.sessions[int64(164)]
		rt.mu.RUnlock()
		require.NotNil(t, blocked)
		go func() {
			for i := 0; i < burst; i++ {
				<-blocked.events.Out()
			}
		}()
		got = <-started
		capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
			ConversationID: convOf(164), ProviderSessionID: "pi-session-burst",
		})
		t.Fatal("Start acknowledgement was blocked by the pre-ack event burst")
	}
	require.NoError(t, got.err)
	require.NotNil(t, got.result)

	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID: convOf(164), ProviderSessionID: "pi-session-burst",
	})
	for i := 0; i < burst; i++ {
		event, open := <-got.events
		require.True(t, open, "event stream closed at %d/%d", i, burst)
		assert.Equal(t, agentruntime.TextDelta{Text: fmt.Sprintf("event-%03d", i)}, event)
	}
	_, open := <-got.events
	assert.False(t, open)
}

// TestPrepareRun_FinalizedGenerationDropsStaleFramesAndAllowsRetry 锁定同一条
// SessionID 上「上一轮已收尾 → 重试新开一轮」的交接:上一轮的迟到帧不得漏进新那一轮。
//
// 这条用例原本叫 ...SlowConsumerOverflowCancelsExactGeneration...,用「灌爆 128 格
// 缓冲触发溢出」来结束第一轮。溢出取消已经去掉了(消费方慢一下就判死用户一轮的代价
// 太重,改走 orderedpipe,见 handleEvent),所以改用**正常终态帧**结束第一轮 ——
// 它要守的交接语义与用什么方式结束上一轮无关。
//
// 「非消费方不得阻塞事件投递」那一半由
// TestPrepareRun_SlowConsumerKeepsItsTurnAndLosesNoEvents 接管,并且守得更严:
// 那里还要求一条事件都不丢。
func TestPrepareRun_FinalizedGenerationDropsStaleFramesAndAllowsRetry(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)
	const (
		sessionID = int64(165)
		secret    = "private-stale-event-payload"
	)

	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			rp := params.(wire.RunParams)
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "shared-native-session"}
			return nil
		}).AnyTimes()

	first, err := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
		SessionID: sessionID,
	})
	require.NoError(t, err)
	firstEvents, firstResult, err := first.Start(context.Background())
	require.NoError(t, err)

	capture.deliver(t, wire.NotifyEvent, wire.EventFrame{
		ConversationID: convOf(sessionID),
		Event:          agentruntime.TextDelta{Text: secret + "-first"},
	})
	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID: convOf(sessionID), ProviderSessionID: "shared-native-session", Model: "first-model",
	})
	for range firstEvents { //nolint:revive // 抽干到 close
	}
	require.NoError(t, firstResult.StopErr, "正常终态收尾,不该带错误")

	// 这一轮已经收尾,generation 也已从会话表摘掉。
	_, err = rt.Abort(context.Background(), sessionID, 0)
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)

	// 收尾之后、重试装上之前的迟到帧必须被丢掉,不能漏进新那一轮。
	capture.deliver(t, wire.NotifyEvent, wire.EventFrame{
		ConversationID: convOf(sessionID),
		Event:          agentruntime.TextDelta{Text: secret + "-late"},
	})

	second, err := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
		SessionID: sessionID,
	})
	require.NoError(t, err)
	secondEvents, secondResult, err := second.Start(context.Background())
	require.NoError(t, err)
	capture.deliver(t, wire.NotifyEvent, wire.EventFrame{
		ConversationID: convOf(sessionID),
		Event:          agentruntime.TextDelta{Text: "retry-current"},
	})
	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID: convOf(sessionID), ProviderSessionID: "shared-native-session", Model: "retry-model",
	})
	event, open := <-secondEvents
	require.True(t, open)
	assert.Equal(t, agentruntime.TextDelta{Text: "retry-current"}, event,
		"上一轮的迟到帧漏进了重试那一轮")
	_, open = <-secondEvents
	assert.False(t, open)
	assert.Equal(t, "retry-model", secondResult.Model)
}

func TestPrepareRun_StopDuringRegistrationWaitsForOwnerAckThenAborts(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	registrationEntered := make(chan struct{})
	allowRegistration := make(chan struct{})
	abortCalled := make(chan struct{})

	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			close(registrationEntered)
			<-allowRegistration
			rp := params.(wire.RunParams)
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
			return nil
		})
	cli.EXPECT().Call(gomock.Any(), wire.MethodAbort, wire.AbortParams{ConversationID: convOf(72)}, gomock.Any()).
		DoAndReturn(func(context.Context, string, any, any) error {
			close(abortCalled)
			return nil
		})

	prepareErrC := make(chan error, 1)
	go func() {
		_, err := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
			SessionID: 72,
		})
		prepareErrC <- err
	}()
	<-registrationEntered

	abortStarted := make(chan struct{})
	abortErrC := make(chan error, 1)
	go func() {
		close(abortStarted)
		_, err := rt.Abort(context.Background(), 72, 0)
		abortErrC <- err
	}()
	<-abortStarted
	select {
	case <-abortCalled:
		t.Fatal("Abort reached agentred before the registration owner was acknowledged")
	default:
	}
	close(allowRegistration)

	require.NoError(t, <-abortErrC)
	require.ErrorIs(t, <-prepareErrC, context.Canceled)
	<-abortCalled
}

func TestPrepareRun_PendingPiAbortCancelsTheRegisteredGeneration(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	entered := make(chan struct{})
	call := 0

	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ string, params any, result any) error {
			call++
			rp := params.(wire.RunParams)
			if call == 1 {
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
				return nil
			}
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}).Times(2)
	cli.EXPECT().Call(gomock.Any(), wire.MethodAbort, wire.AbortParams{ConversationID: convOf(73)}, gomock.Any()).
		Return(nil)

	errC := make(chan error, 1)
	go func() {
		_, err := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
			SessionID: 73,
		})
		errC <- err
	}()
	<-entered

	_, err := rt.Abort(context.Background(), 73, 0)
	require.NoError(t, err)
	require.ErrorIs(t, <-errC, context.Canceled)
	_, err = rt.Abort(context.Background(), 73, 0)
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestPrepareRun_RequestCancellationAbortsPendingDaemonGeneration(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	entered := make(chan struct{})
	abortCalled := make(chan struct{})
	call := 0

	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ string, params any, result any) error {
			call++
			rp := params.(wire.RunParams)
			if call == 1 {
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
				return nil
			}
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		}).Times(2)
	cli.EXPECT().Call(gomock.Any(), wire.MethodAbort, wire.AbortParams{ConversationID: convOf(74)}, gomock.Any()).
		DoAndReturn(func(context.Context, string, any, any) error {
			close(abortCalled)
			return nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	errC := make(chan error, 1)
	go func() {
		_, err := rt.PrepareRun(ctx, agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
			SessionID: 74,
		})
		errC <- err
	}()
	<-entered
	cancel()

	require.ErrorIs(t, <-errC, context.Canceled)
	<-abortCalled
	_, err := rt.Abort(context.Background(), 74, 0)
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestPrepareRun_StartCancellationAbortsPromptAcknowledgement(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	startEntered := make(chan struct{})
	abortCalled := make(chan struct{})
	call := 0

	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ string, params any, result any) error {
			call++
			rp := params.(wire.RunParams)
			switch call {
			case 1:
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
				return nil
			case 2:
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "pi-session-new"}
				return nil
			default:
				close(startEntered)
				<-ctx.Done()
				return ctx.Err()
			}
		}).Times(3)
	cli.EXPECT().Call(gomock.Any(), wire.MethodAbort, wire.AbortParams{ConversationID: convOf(75)}, gomock.Any()).
		DoAndReturn(func(context.Context, string, any, any) error {
			close(abortCalled)
			return nil
		})

	prepared, err := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
		SessionID: 75,
	})
	require.NoError(t, err)
	startCtx, cancelStart := context.WithCancel(context.Background())
	startErrC := make(chan error, 1)
	go func() {
		_, _, err := prepared.Start(startCtx)
		startErrC <- err
	}()
	<-startEntered
	cancelStart()

	require.ErrorIs(t, <-startErrC, context.Canceled)
	<-abortCalled
	_, err = rt.Abort(context.Background(), 75, 0)
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestPrepareRun_CurrentPiCompletionIsNotConsumedWhenAbortedOwnerEmitsNoTerminalFrame(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)

	call := 0
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			call++
			rp := params.(wire.RunParams)
			switch call {
			case 1, 4:
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
			case 2, 3, 5, 6:
				*(result.(*wire.RunAck)) = wire.RunAck{
					ConversationID: rp.ConversationID, ProviderSessionID: "shared-native-session",
				}
			default:
				t.Fatalf("unexpected runtime.run call %d", call)
			}
			return nil
		}).Times(6)
	cli.EXPECT().Call(gomock.Any(), wire.MethodAbort, wire.AbortParams{ConversationID: convOf(83)}, gomock.Any()).Return(nil)

	first, err := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
		Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
		SessionID:         83,
		ProviderSessionID: "shared-native-session",
	})
	require.NoError(t, err)
	firstEvents, _, err := first.Start(context.Background())
	require.NoError(t, err)
	require.NoError(t, first.Close(context.Background()))
	_, open := <-firstEvents
	assert.False(t, open)

	second, err := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
		Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
		SessionID:         83,
		ProviderSessionID: "shared-native-session",
	})
	require.NoError(t, err)
	secondEvents, result, err := second.Start(context.Background())
	require.NoError(t, err)

	currentEvent := agentruntime.TextDelta{Text: "current generation"}
	capture.deliver(t, wire.NotifyEvent, wire.EventFrame{ConversationID: convOf(83), Event: currentEvent})
	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID: convOf(83), ProviderSessionID: "shared-native-session", Model: "current-model",
	})
	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID: convOf(83), ProviderSessionID: "shared-native-session", Model: "duplicate-model",
	})

	select {
	case event, ok := <-secondEvents:
		require.True(t, ok)
		assert.Equal(t, agentruntime.TextDelta{Text: "current generation"}, event)
	case <-time.After(time.Second):
		t.Fatal("current generation event was consumed by the aborted owner")
	}
	_, open = <-secondEvents
	assert.False(t, open)
	assert.Equal(t, "current-model", result.Model)
}

func TestPrepareRun_StaleCloseCannotOverlapNewRegistrationWhenTerminalIdentityIsEmpty(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)
	abortEntered := make(chan struct{})
	allowAbort := make(chan struct{})
	secondRegistrationEntered := make(chan struct{})
	var (
		mu   sync.Mutex
		call int
	)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			mu.Lock()
			call++
			currentCall := call
			mu.Unlock()
			rp := params.(wire.RunParams)
			switch currentCall {
			case 1, 4:
				if currentCall == 4 {
					close(secondRegistrationEntered)
				}
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
			case 2, 3, 5, 6:
				*(result.(*wire.RunAck)) = wire.RunAck{
					ConversationID: rp.ConversationID, ProviderSessionID: "shared-native-session",
				}
			default:
				t.Fatalf("unexpected runtime.run call %d", currentCall)
			}
			return nil
		}).Times(6)
	cli.EXPECT().Call(gomock.Any(), wire.MethodAbort, wire.AbortParams{ConversationID: convOf(82)}, gomock.Any()).
		DoAndReturn(func(context.Context, string, any, any) error {
			close(abortEntered)
			<-allowAbort
			return nil
		})

	first, err := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
		Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
		SessionID:         82,
		ProviderSessionID: "shared-native-session",
	})
	require.NoError(t, err)
	firstEvents, _, err := first.Start(context.Background())
	require.NoError(t, err)

	closeErrC := make(chan error, 1)
	go func() { closeErrC <- first.Close(context.Background()) }()
	<-abortEntered
	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID: convOf(82), Model: "completed-before-abort-response",
	})
	_, open := <-firstEvents
	assert.False(t, open)

	preparedC := make(chan piagentrt.PreparedRun, 1)
	errC := make(chan error, 1)
	go func() {
		prepared, prepareErr := rt.PrepareRun(context.Background(), agentruntime.RunRequest{
			Backend:           &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent)},
			SessionID:         82,
			ProviderSessionID: "shared-native-session",
		})
		preparedC <- prepared
		errC <- prepareErr
	}()

	registeredBeforeAbortSettled := false
	select {
	case <-secondRegistrationEntered:
		registeredBeforeAbortSettled = true
	case <-time.After(100 * time.Millisecond):
	}
	close(allowAbort)
	require.NoError(t, <-closeErrC)
	assert.False(t, registeredBeforeAbortSettled,
		"a stale Close must finish its exact abort before a retry can register on the daemon")
	if !registeredBeforeAbortSettled {
		<-secondRegistrationEntered
	}

	require.NoError(t, <-errC)
	second := <-preparedC
	require.NotNil(t, second)
	secondEvents, result, err := second.Start(context.Background())
	require.NoError(t, err)
	currentEvent := agentruntime.TextDelta{Text: "current generation"}
	capture.deliver(t, wire.NotifyEvent, wire.EventFrame{ConversationID: convOf(82), Event: currentEvent})
	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID: convOf(82), Model: "current-model",
	})
	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID: convOf(82), Model: "duplicate-model",
	})

	event, open := <-secondEvents
	require.True(t, open)
	assert.Equal(t, agentruntime.TextDelta{Text: "current generation"}, event)
	_, open = <-secondEvents
	assert.False(t, open)
	assert.Equal(t, "current-model", result.Model)
}

func TestHandleEvent_UnknownAndMalformedFramesNeverLogPayload(t *testing.T) {
	_, _, _, rt := setupRemote(t)
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))
	const secret = "private-prompt-and-image-payload"

	// 直接构造线上字节:这里要模拟的就是「对端发来一段本地词表以外的载荷」,
	// 而 EventFrame 现在装的是密封值,用结构体根本拼不出这种帧。
	unknown := json.RawMessage(fmt.Sprintf(
		`{"sessionId":901,"event":{"kind":%q,"text":%q}}`, secret, secret))
	_, err := rt.handleEvent(ctx, unknown)
	require.NoError(t, err)

	rt.mu.Lock()
	rt.sessions[902] = &remoteSession{id: 902, events: orderedpipe.New[agentruntime.Event](), result: &agentruntime.RunResult{}}
	rt.mu.Unlock()
	malformed := json.RawMessage(fmt.Sprintf(
		`{"sessionId":902,"event":{"kind":"not_real","detail":%q}}`, secret))
	_, err = rt.handleEvent(ctx, malformed)
	require.NoError(t, err)

	for _, entry := range logs.All() {
		assert.NotContains(t, entry.Message, secret)
		for _, value := range entry.ContextMap() {
			assert.NotContains(t, fmt.Sprint(value), secret)
		}
	}
}

func TestHandleRunResultDone_LateFrameNeverLogsStopPayload(t *testing.T) {
	_, _, _, rt := setupRemote(t)
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))
	const secret = "private-late-error-payload"

	raw, err := json.Marshal(wire.RunResultDoneFrame{
		ConversationID: convOf(903), StopErrMsg: secret, StopErrCode: wire.ErrCodeAborted,
	})
	require.NoError(t, err)
	_, err = rt.handleRunResultDone(ctx, raw)
	require.NoError(t, err)

	for _, entry := range logs.All() {
		assert.NotContains(t, entry.Message, secret)
		for _, value := range entry.ContextMap() {
			assert.NotContains(t, fmt.Sprint(value), secret)
		}
	}
}

func TestRun_Success_DispatchesEventsThenCloses(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)

	runCalls := 0
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			runCalls++
			rp, ok := params.(wire.RunParams)
			require.True(t, ok, "expected wire.RunParams, got %T", params)
			assert.Equal(t, convOf(42), rp.ConversationID)
			assert.Equal(t, "hello", rp.UserText)
			assert.True(t, rp.Compact)
			switch runCalls {
			case 1:
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
			case 2:
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "psid-early"}
			case 3:
				assert.Equal(t, "psid-early", rp.ProviderSessionID)
				*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "psid-early"}
			}
			return nil
		}).Times(3)

	events, runResult, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypePiAgent), ID: 1, Name: "x"},
		SessionID: 42,
		UserText:  "hello",
		Compact:   true,
	})
	require.NoError(t, err)
	require.NotNil(t, runResult)
	assert.Equal(t, "psid-early", runResult.ProviderSessionID)

	// Deliver a TextDelta then a runResultDone with Usage + Model.
	textJSON := agentruntime.TextDelta{Text: "hi"}
	capture.deliver(t, wire.NotifyEvent, wire.EventFrame{ConversationID: convOf(42), Event: textJSON})
	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID:    convOf(42),
		ProviderSessionID: "psid-early",
		Model:             "claude-sonnet-4-6",
		ContextWindow:     200000,
		Usage:             &wire.UsageWire{PromptTokens: 10, TotalTokens: 10},
	})

	// First event arrives.
	select {
	case ev := <-events:
		td, ok := ev.(agentruntime.TextDelta)
		require.True(t, ok, "got %T", ev)
		assert.Equal(t, "hi", td.Text)
	case <-time.After(time.Second):
		t.Fatal("never got text delta")
	}

	// Channel must close after runResultDone.
	select {
	case _, ok := <-events:
		assert.False(t, ok, "events channel must close after runResultDone")
	case <-time.After(time.Second):
		t.Fatal("events channel never closed")
	}

	// RunResult fields hydrated.
	assert.Equal(t, "psid-early", runResult.ProviderSessionID)
	assert.Equal(t, "claude-sonnet-4-6", runResult.Model)
	assert.Equal(t, 200000, runResult.ContextWindow)
	require.NotNil(t, runResult.Usage)
	assert.Equal(t, 10, runResult.Usage.PromptTokens)
	assert.NoError(t, runResult.StopErr)
}

// TestAutonomousTurns_ReconstructsForwardedTurn 验证 client 把 daemon 转发的
// Started → Event → Done 三帧还原成一个 agentruntime.AutonomousTurn:Events 收到
// 文本后 close,Result 在 close 后填好。
func TestAutonomousTurns_ReconstructsForwardedTurn(t *testing.T) {
	_, _, capture, rt := setupRemote(t)
	turns := rt.AutonomousTurns(42)

	capture.deliver(t, wire.NotifyAutonomousTurnStarted, wire.AutonomousTurnStartedFrame{
		ConversationID: convOf(42), Trigger: "background_task",
	})

	var at agentruntime.AutonomousTurn
	select {
	case at = <-turns:
	case <-time.After(time.Second):
		t.Fatal("never got autonomous turn")
	}
	assert.Equal(t, "background_task", at.Trigger)
	require.NotNil(t, at.Result)

	textJSON := agentruntime.TextDelta{Text: "autonomous:listing"}
	capture.deliver(t, wire.NotifyAutonomousTurnEvent, wire.EventFrame{ConversationID: convOf(42), Event: textJSON})
	capture.deliver(t, wire.NotifyAutonomousTurnDone, wire.RunResultDoneFrame{
		ConversationID: convOf(42), ProviderSessionID: "psid-1", Model: "claude-sonnet-4-6",
	})

	select {
	case ev := <-at.Events:
		td, ok := ev.(agentruntime.TextDelta)
		require.True(t, ok, "got %T", ev)
		assert.Equal(t, "autonomous:listing", td.Text)
	case <-time.After(time.Second):
		t.Fatal("never got autonomous event")
	}
	select {
	case _, ok := <-at.Events:
		assert.False(t, ok, "events must close after done")
	case <-time.After(time.Second):
		t.Fatal("events never closed")
	}
	assert.Equal(t, "psid-1", at.Result.ProviderSessionID)
	assert.Equal(t, "claude-sonnet-4-6", at.Result.Model)
}

// TestAutonomousTurnEvent_ClosingRaceMustNotPanic 锁定一个真实并发缺陷:
// daemon 在自主续轮投递事件期间断连时,watchClose goroutine 调
// closeAllAutoSessions() 收尾 cur.events;关与送若不互斥 → send-on-closed-channel
// panic(读循环 goroutine 无 recover → 整进程崩)。
//
// 不变量没变,复现手法换了。旧版靠「把 cap 64 的 channel 填满、让下一次送 park 住」
// 制造窗口,而投递改走 orderedpipe 之后 Push 永不 park,那个手法既不成立也不再是
// 需要防的形状 —— 现在要防的是 Push 与 Close 真并发。所以改成让**多路并发投递**与
// 断连收尾直接对撞,由 -race 与 recover 同时把关。这比旧版更强:它不依赖任何缓冲
// 容量,换实现也照样成立。
func TestAutonomousTurnEvent_ClosingRaceMustNotPanic(t *testing.T) {
	_, _, capture, rt := setupRemote(t)
	_ = rt.AutonomousTurns(42)

	capture.deliver(t, wire.NotifyAutonomousTurnStarted, wire.AutonomousTurnStartedFrame{
		ConversationID: convOf(42), Trigger: "background_task",
	})
	require.Eventually(t, func() bool {
		a := rt.lookupAutoSession(42)
		if a == nil {
			return false
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.cur != nil
	}, time.Second, time.Millisecond, "typed notification handler must install the autonomous turn")

	newFrame := func() *wire.EventFrame {
		return &wire.EventFrame{ConversationID: convOf(42), Event: agentruntime.TextDelta{Text: "x"}}
	}

	// 八路并发投递,与断连收尾对撞。谁先谁后不确定,正是要覆盖的窗口。
	panicked := make(chan any, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { panicked <- recover() }()
			for k := 0; k < 64; k++ {
				_, _ = rt.handleAutonomousTurnEvent(context.Background(), newFrame())
			}
		}()
	}
	rt.closeAllAutoSessions(ErrDaemonDisconnected)
	wg.Wait()
	close(panicked)
	for got := range panicked {
		require.Nil(t, got,
			"closeAllAutoSessions 与 handleAutonomousTurnEvent 竞争时不得 send-on-closed panic;got=%v", got)
	}
}

// TestAutonomousTurnStarted_ClosingRaceMustNotPanic 是同一条不变量在「起一轮」路径
// 上的版本:handleAutonomousTurnStarted 往 a.out 投新 turn,而 closeAllAutoSessions()
// (watchClose goroutine,daemon 断连触发)收尾 a.out。
//
// 复现手法与 event 版一起换掉,理由同上。
func TestAutonomousTurnStarted_ClosingRaceMustNotPanic(t *testing.T) {
	_, _, _, rt := setupRemote(t)
	_ = rt.AutonomousTurns(42)

	// 预 marshal 一次,goroutine 内复用,避免在非测试 goroutine 里调 testify。
	startedFrame := &wire.AutonomousTurnStartedFrame{
		ConversationID: convOf(42), Trigger: "background_task",
	}

	panicked := make(chan any, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { panicked <- recover() }()
			for k := 0; k < 32; k++ {
				_, _ = rt.handleAutonomousTurnStarted(context.Background(), startedFrame)
			}
		}()
	}
	rt.closeAllAutoSessions(ErrDaemonDisconnected)
	wg.Wait()
	close(panicked)
	for got := range panicked {
		require.Nil(t, got,
			"closeAllAutoSessions 与 handleAutonomousTurnStarted 竞争时不得 send-on-closed panic;got=%v", got)
	}
}

func TestRun_DeliversEventArrivingBeforeRunAckReturns(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)
	textJSON := agentruntime.TextDelta{Text: "early"}

	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			rp, ok := params.(wire.RunParams)
			require.True(t, ok, "expected wire.RunParams, got %T", params)
			capture.deliver(t, wire.NotifyEvent, wire.EventFrame{ConversationID: rp.ConversationID, Event: textJSON})
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
			return nil
		})

	events, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode", ID: 1, Name: "x"},
		SessionID: 42,
		UserText:  "hello",
	})
	require.NoError(t, err)

	select {
	case ev := <-events:
		td, ok := ev.(agentruntime.TextDelta)
		require.True(t, ok, "got %T", ev)
		assert.Equal(t, "early", td.Text)
	case <-time.After(time.Second):
		t.Fatal("early event was dropped before Run ack returned")
	}

	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{ConversationID: convOf(42)})
}

func TestRun_StopErrAborted_Rehydrates(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(7)}
			return nil
		})
	events, runResult, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 7,
	})
	require.NoError(t, err)

	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID: convOf(7),
		StopErrMsg:     "aborted by user",
		StopErrCode:    wire.ErrCodeAborted,
	})
	select {
	case _, ok := <-events:
		require.False(t, ok, "typed terminal notification must close the event stream")
	case <-time.After(time.Second):
		t.Fatal("typed terminal notification did not finish the run")
	}

	require.Error(t, runResult.StopErr)
	assert.ErrorIs(t, runResult.StopErr, agentruntime.ErrAborted)
}

func TestRun_RPCError_PropagatesAndDoesNotRegister(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		Return(errors.New("transport down"))
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 5,
	})
	require.Error(t, err)

	// Steer after failed Run must see ErrNoActiveTurn — session was never
	// registered.
	err = rt.Steer(context.Background(), 5, "", "x")
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestRun_EventForUnknownSession_DroppedSilently(t *testing.T) {
	_, _, capture, _ := setupRemote(t)
	// No Run call → no session known. Delivering an event must not panic
	// nor produce an error from the handler.
	textJSON := agentruntime.TextDelta{Text: "noise"}
	capture.deliver(t, wire.NotifyEvent, wire.EventFrame{ConversationID: convOf(999), Event: textJSON})
}

func TestRuntimeNotificationLogsRedactFramesWhileDeliveryAndStopErrorStayLossless(t *testing.T) {
	const (
		unknownSentinel    = "SENTINEL_REMOTE_UNKNOWN_EVENT"
		untrustedKindValue = "SENTINEL_REMOTE_UNTRUSTED_KIND"
		malformedSentinel  = "SENTINEL_REMOTE_MALFORMED_EVENT"
		resultSentinel     = "SENTINEL_REMOTE_TOOL_RESULT"
		metaSentinel       = "SENTINEL_REMOTE_TOOL_META"
		stopErrSentinel    = "SENTINEL_REMOTE_STOP_ERROR"
	)
	_, cli, capture, rt := setupRemote(t)
	core, logs := observer.New(zapcore.DebugLevel)
	ctx := logger.WithContextLogger(context.Background(), zap.New(core))

	unknownEvent := agentruntime.ToolCall{
		ID: "unknown-safe-id", Name: "subagent", Input: json.RawMessage(`{"task":"` + unknownSentinel + `"}`),
	}
	_, err := rt.handleEvent(ctx, &wire.EventFrame{ConversationID: convOf(999), Event: unknownEvent})
	require.NoError(t, err)

	// 载荷坏掉的那一档现在挡在**转换**这一步,进不了 handler 了(帧装的是密封值)。
	// 转换失败的错误会被补齐循环原样 zap.Error 出去(reconnect.go 的
	// "replay skipped unknown Protobuf notification"),所以这里守的是那段错误文本:
	// canonical 解不出来时不得把载荷抄进错误里。
	_, _, conversionErr := protowire.ProtoNotificationToWire(&agentrewire.RpcNotification{
		Payload: &agentrewire.RpcNotification_RuntimeEvent{RuntimeEvent: &agentrewire.RuntimeEventNotification{
			ConversationId: convOf(998), Seq: 1,
			Event: &agentrewire.RuntimeEventNotification_ToolCall{ToolCall: &agentrewire.ToolCall{
				Id: "bad-safe-id", Name: "subagent",
				Input:     []byte(`{"payload":"` + malformedSentinel + `"}`),
				Canonical: []byte(`{"kind":"file_write","path":{"` + untrustedKindValue + `":true}}`),
			}},
		}},
	})
	require.Error(t, conversionErr, "坏 canonical 必须在转换这一步就被挡下")
	logger.Ctx(ctx).Warn("remote runtime: replay skipped unknown Protobuf notification",
		zap.Error(conversionErr))

	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(7)}
			return nil
		})
	events, runResult, err := rt.Run(ctx, agentruntime.RunRequest{
		Backend: &agent_backend_entity.AgentBackend{Type: "claudecode"}, SessionID: 7,
	})
	require.NoError(t, err)

	forwardedEvent := agentruntime.ToolResult{
		ToolCallID: "result-safe-id",
		Content:    resultSentinel,
		Meta:       json.RawMessage(`{"detail":"` + metaSentinel + `"}`),
	}
	capture.deliverContext(t, ctx, wire.NotifyEvent, wire.EventFrame{ConversationID: convOf(7), Event: forwardedEvent})
	select {
	case event := <-events:
		result, ok := event.(agentruntime.ToolResult)
		require.True(t, ok, "got %T", event)
		assert.Equal(t, resultSentinel, result.Content)
		assert.Contains(t, string(result.Meta), metaSentinel)
	case <-time.After(time.Second):
		t.Fatal("forwarded event was not delivered")
	}

	_, err = rt.handleRunResultDone(ctx, &wire.RunResultDoneFrame{
		ConversationID: convOf(7), StopErrMsg: stopErrSentinel,
	})
	require.NoError(t, err)
	select {
	case _, ok := <-events:
		assert.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("events channel did not close")
	}
	require.EqualError(t, runResult.StopErr, stopErrSentinel)
	captured := observedRemoteLogText(logs)
	for _, sentinel := range []string{unknownSentinel, untrustedKindValue, malformedSentinel, resultSentinel, metaSentinel, stopErrSentinel} {
		assert.NotContains(t, captured, sentinel)
	}
	// 认得出的事件、但没有对应会话:身份取 Go 类型名,由类型系统给出,天然安全。
	unknownLogs := logs.FilterMessage("remote.handleEvent: event for unknown session dropped").All()
	require.Len(t, unknownLogs, 1)
	assert.Equal(t, "agentruntime.ToolCall", unknownLogs[0].ContextMap()["eventType"])

	// 坏载荷现在挡在转换这一步,而补齐循环把转换错误原样 zap.Error 出去 —— 上面
	// 那圈 sentinel 断言覆盖的正是这条:错误文本里不得夹带任何载荷。
	rejected := logs.FilterMessage("remote runtime: replay skipped unknown Protobuf notification").All()
	require.Len(t, rejected, 1)
	assert.NotEmpty(t, rejected[0].ContextMap()["error"], "转换失败必须留下可诊断的错误")
	resultLogs := logs.FilterMessage("remote.handleRunResultDone: session ended").All()
	require.Len(t, resultLogs, 1)
	assert.Equal(t, int64(len(stopErrSentinel)), resultLogs[0].ContextMap()["stopErrBytes"])
}

func observedRemoteLogText(logs *observer.ObservedLogs) string {
	var out strings.Builder
	for _, entry := range logs.All() {
		_, _ = fmt.Fprintf(&out, "%s %v\n", entry.Message, entry.ContextMap())
	}
	return out.String()
}

// ── Steer ───────────────────────────────────────────────────────────────────

func TestSteer_Success(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(9)}
			return nil
		})
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 9,
	})
	require.NoError(t, err)

	cli.EXPECT().Call(gomock.Any(), wire.MethodSteer, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, _ any) error {
			sp, ok := params.(wire.SteerParams)
			require.True(t, ok)
			assert.Equal(t, convOf(9), sp.ConversationID)
			assert.Equal(t, "q-1", sp.QueuedID)
			assert.Equal(t, "stop", sp.Text)
			return nil
		})
	require.NoError(t, rt.Steer(context.Background(), 9, "q-1", "stop"))
}

func TestSteer_NoSession_ErrNoActiveTurn(t *testing.T) {
	_, _, _, rt := setupRemote(t)
	err := rt.Steer(context.Background(), 1, "", "x")
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestSteer_ServerSentinel_Rehydrates(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(3)}
			return nil
		})
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 3,
	})
	require.NoError(t, err)

	cli.EXPECT().Call(gomock.Any(), wire.MethodSteer, gomock.Any(), gomock.Any()).
		Return(&rpcerror.Error{Code: wire.ErrCodeUnsupported, Message: "no"})
	err = rt.Steer(context.Background(), 3, "", "x")
	assert.ErrorIs(t, err, agentruntime.ErrUnsupported)
}

// ── CancelSteer / DrainPending / Abort / SetPermissionMode ─────────────────

func TestCancelSteer_HappyAndSentinel(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(1)}
			return nil
		})
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 1,
	})
	require.NoError(t, err)

	cli.EXPECT().Call(gomock.Any(), wire.MethodCancelSteer, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.CancelSteerResult)) = wire.CancelSteerResult{Removed: []string{"a"}}
			return nil
		})
	removed, err := rt.CancelSteer(context.Background(), 1, "a")
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, removed)

	cli.EXPECT().Call(gomock.Any(), wire.MethodCancelSteer, gomock.Any(), gomock.Any()).
		Return(&rpcerror.Error{Code: wire.ErrCodeSteerNotFound})
	_, err = rt.CancelSteer(context.Background(), 1, "ghost")
	assert.ErrorIs(t, err, agentruntime.ErrSteerNotFound)
}

func TestDrainPending_ReturnsSteers(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(2)}
			return nil
		})
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 2,
	})
	require.NoError(t, err)

	cli.EXPECT().Call(gomock.Any(), wire.MethodDrainPending, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.DrainResult)) = wire.DrainResult{
				Steers: []agentruntime.ConsumedSteer{{QueuedID: "q1", Text: "t"}},
			}
			return nil
		})
	out := rt.DrainPending(context.Background(), 2)
	assert.Equal(t, []agentruntime.ConsumedSteer{{QueuedID: "q1", Text: "t"}}, out)
}

func TestAbort_SuccessAndNoSession(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(4)}
			return nil
		})
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 4,
	})
	require.NoError(t, err)

	cli.EXPECT().Call(gomock.Any(), wire.MethodAbort, gomock.Any(), gomock.Any()).
		Return(nil)
	_, err = rt.Abort(context.Background(), 4, 0)
	require.NoError(t, err)

	// Unknown session
	_, err = rt.Abort(context.Background(), 999, 0)
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

// TestAbort_PassesTurnTokenAndReturnsInterruptedTurnKind 钉死决策 1 的远端链路:
// remote.Runtime.Abort 把调用方的 turnToken 原样透传到 wire.AbortParams,并把 daemon
// 返回的 wire.AbortResult.TurnKind 带回给调用方(AbortOutcome.TurnKind)—— spec 测试接缝
// 「remote wire + daemon handler:AbortParams 携带 token,daemon 侧透传并返回轮类型」。
func TestAbort_PassesTurnTokenAndReturnsInterruptedTurnKind(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(4)}
			return nil
		})
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 4,
	})
	require.NoError(t, err)

	// 断言 AbortParams 携带 turnToken=42,并让 daemon 应答一个具体的被中断轮类型。
	cli.EXPECT().Call(gomock.Any(), wire.MethodAbort,
		gomock.Eq(wire.AbortParams{ConversationID: convOf(4), TurnToken: 42}), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.AbortResult)) = wire.AbortResult{TurnKind: agentruntime.TurnKindAutonomous}
			return nil
		})
	outcome, err := rt.Abort(context.Background(), 4, 42)
	require.NoError(t, err)
	assert.Equal(t, agentruntime.TurnKindAutonomous, outcome.TurnKind)
}

func TestStopBackgroundTask_SuccessAndNoSession(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(4)}
			return nil
		})
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 4,
	})
	require.NoError(t, err)

	cli.EXPECT().Call(gomock.Any(), wire.MethodStopBackgroundTask, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, _ any) error {
			sp := params.(wire.StopBackgroundTaskParams)
			assert.Equal(t, convOf(4), sp.ConversationID)
			assert.Equal(t, "b0n82mqaj", sp.TaskID)
			return nil
		})
	require.NoError(t, rt.StopBackgroundTask(context.Background(), 4, "b0n82mqaj"))

	// Unknown session → ErrNoActiveTurn(不发 RPC)
	err = rt.StopBackgroundTask(context.Background(), 999, "b0n82mqaj")
	assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
}

func TestSetPermissionMode_Success(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(6)}
			return nil
		})
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 6,
	})
	require.NoError(t, err)

	cli.EXPECT().Call(gomock.Any(), wire.MethodSetPermissionMode, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, _ any) error {
			sp := params.(wire.SetPermissionModeParams)
			assert.Equal(t, "plan", sp.Mode)
			return nil
		})
	require.NoError(t, rt.SetPermissionMode(context.Background(), 6, "plan"))
}

func TestSubmitAnswer_Success(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(8)}
			return nil
		})
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 8,
	})
	require.NoError(t, err)

	cli.EXPECT().Call(gomock.Any(), wire.MethodSubmitAnswer, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, _ any) error {
			sp := params.(wire.SubmitAnswerParams)
			assert.Equal(t, "r-1", sp.RequestID)
			assert.True(t, sp.Skipped)
			return nil
		})
	require.NoError(t, rt.SubmitAnswer(context.Background(), 8, "r-1", nil, nil, true))
}

func TestSubmitToolPermission_Success(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(10)}
			return nil
		})
	_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
		SessionID: 10,
	})
	require.NoError(t, err)

	cli.EXPECT().Call(gomock.Any(), wire.MethodSubmitToolPermission, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, _ any) error {
			sp := params.(wire.SubmitToolPermissionParams)
			assert.True(t, sp.Allow)
			assert.True(t, sp.AlwaysAllowSession)
			return nil
		})
	require.NoError(t, rt.SubmitToolPermission(context.Background(), 10, "p-1", true, true, ""))
}

func TestSubmitControls_GivenAlreadyHandledResult_WhenSubmitted_ThenReturnsWaiterNotFound(t *testing.T) {
	tests := []struct {
		name   string
		method string
		drive  func(*Runtime) error
	}{
		{
			name:   "answer",
			method: wire.MethodSubmitAnswer,
			drive: func(rt *Runtime) error {
				return rt.SubmitAnswer(context.Background(), 18, "r-1", nil, nil, true)
			},
		},
		{
			name:   "tool permission",
			method: wire.MethodSubmitToolPermission,
			drive: func(rt *Runtime) error {
				return rt.SubmitToolPermission(context.Background(), 18, "p-1", true, false, "")
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, cli, _, rt := setupRemote(t)
			cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
					*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: convOf(18)}
					return nil
				})
			_, _, err := rt.Run(context.Background(), agentruntime.RunRequest{
				Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode"},
				SessionID: 18,
			})
			require.NoError(t, err)

			cli.EXPECT().Call(gomock.Any(), tc.method, gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, _ any, result any) error {
					return json.Unmarshal([]byte(`{"alreadyHandled":true}`), result)
				})
			err = tc.drive(rt)
			assert.ErrorIs(t, err, agentruntime.ErrWaiterNotFound)
		})
	}
}

// ── R12 桌面侧:对端 origin 传播 ─────────────────────────────────────────────

const (
	peerSid int64 = 77 // 对端 A 发起的会话
	ownSid  int64 = 78 // 本客户端自己的会话(origin 空)
)

func strptr(s string) *string { return &s }

// peerOriginRig 造一台已认领 daemon 的连接并完成一次账号级补齐:清单交回两条运行中的
// 会话 —— 对端 A 发起(origin "peer-A")与本地自己的(origin 空),两者都被接管进 tracked。
func peerOriginRig(t *testing.T) (*fakeConn, *Runtime) {
	t.Helper()
	conn := newFakeConn()
	conn.script(func(method string, params, result any) error {
		switch method {
		case wire.MethodSessionList:
			*(result.(*wire.SessionListResult)) = wire.SessionListResult{Sessions: []wire.SessionSummary{
				{ConversationID: convOf(peerSid), PeerFingerprint: "peer-A",
					LifecycleState: wire.SessionLifecycleRunning},
				{ConversationID: convOf(ownSid), LifecycleState: wire.SessionLifecycleRunning},
			}}
		case wire.MethodSessionAttach:
			p := params.(wire.SessionAttachParams)
			*(result.(*wire.SessionAttachResult)) = wire.SessionAttachResult{
				ConversationID: p.ConversationID,
				LifecycleState: wire.SessionLifecycleRunning,
			}
		case wire.MethodSessionPull:
			p := params.(wire.SessionPullParams)
			*(result.(*wire.SessionPullResult)) = wire.SessionPullResult{Cursor: p.Cursor}
		case wire.MethodSessionPendingWaiters:
			*(result.(*wire.SessionPendingWaitersResult)) = wire.SessionPendingWaitersResult{}
		}
		return nil
	})
	rt, _, _ := newRestartRuntime(t, conn, 0)
	live, err := rt.CatchUpSessions(context.Background(), []int64{peerSid, ownSid})
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{peerSid, ownSid}, live)
	return conn, rt
}

// peerFingerprintOf 从一条控制请求参数里抽出 PeerFingerprint。
func peerFingerprintOf(t *testing.T, method string, params any) string {
	t.Helper()
	switch p := params.(type) {
	case wire.SteerParams:
		return p.PeerFingerprint
	case wire.CancelSteerParams:
		return p.PeerFingerprint
	case wire.DrainParams:
		return p.PeerFingerprint
	case wire.AbortParams:
		return p.PeerFingerprint
	case wire.StopBackgroundTaskParams:
		return p.PeerFingerprint
	case wire.SetPermissionModeParams:
		return p.PeerFingerprint
	case wire.SubmitAnswerParams:
		return p.PeerFingerprint
	case wire.SubmitToolPermissionParams:
		return p.PeerFingerprint
	case wire.GoalParams:
		return p.PeerFingerprint
	default:
		t.Fatalf("unexpected control params type %T for %s", params, method)
		return ""
	}
}

// Given 已认领 daemon 上同账号客户端要操作另一对端发起的会话,When 桌面端提交一条控制
// 请求,Then 请求把清单里学到的 PeerFingerprint 原样带过去 —— daemon 据此解析到发起对端
// (R12);本客户端自己的会话(origin 空)则省略该字段(向后兼容)。
func TestControlRequests_CarryPeerOrigin(t *testing.T) {
	conn, rt := peerOriginRig(t)

	// 每条控制请求都驱动一次,并在 wire 参数里核对 origin。
	drive := []struct {
		name   string
		drive  func() error
		method string
	}{
		{"Steer", func() error { return rt.Steer(context.Background(), peerSid, "q-1", "stop") }, wire.MethodSteer},
		{"CancelSteer", func() error { _, err := rt.CancelSteer(context.Background(), peerSid, "q-1"); return err }, wire.MethodCancelSteer},
		{"DrainPending", func() error { rt.DrainPending(context.Background(), peerSid); return nil }, wire.MethodDrainPending},
		{"Abort", func() error { _, err := rt.Abort(context.Background(), peerSid, 0); return err }, wire.MethodAbort},
		{"StopBackgroundTask", func() error { return rt.StopBackgroundTask(context.Background(), peerSid, "t-1") }, wire.MethodStopBackgroundTask},
		{"SetPermissionMode", func() error { return rt.SetPermissionMode(context.Background(), peerSid, "plan") }, wire.MethodSetPermissionMode},
		{"SubmitAnswer", func() error { return rt.SubmitAnswer(context.Background(), peerSid, "r-1", nil, nil, true) }, wire.MethodSubmitAnswer},
		{"SubmitToolPermission", func() error { return rt.SubmitToolPermission(context.Background(), peerSid, "p-1", true, false, "") }, wire.MethodSubmitToolPermission},
		{"SetGoal", func() error {
			_, err := rt.SetGoal(context.Background(), agentruntime.GoalRequest{SessionID: peerSid, Objective: strptr("x")})
			return err
		}, wire.MethodSetGoal},
	}
	for _, tc := range drive {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.drive())
			calls := conn.methodCalls(tc.method)
			require.Len(t, calls, 1, "%s 应恰好发一次 RPC", tc.method)
			assert.Equal(t, "peer-A", peerFingerprintOf(t, tc.method, calls[0].Params),
				"%s 必须把对端 origin 带进请求", tc.method)
		})
	}

	// 本地自己的会话:origin 空 → 省略该字段。
	require.NoError(t, rt.Steer(context.Background(), ownSid, "q-2", "go"))
	steers := conn.methodCalls(wire.MethodSteer)
	require.Len(t, steers, 2)
	assert.Empty(t, steers[1].Params.(wire.SteerParams).PeerFingerprint)
}

// ── Capabilities ────────────────────────────────────────────────────────────

func TestCapabilities_DefaultBeforePrefetch(t *testing.T) {
	_, _, _, rt := setupRemote(t)
	caps := rt.Capabilities()
	// Default placeholder: must at minimum expose CapSteer + CapAbort + CapAnswerUserAsk
	// so chat_svc UI gating doesn't false-flag a fresh, unprefetched runtime.
	assert.True(t, caps.Has(capability.CapSteer))
	assert.True(t, caps.Has(capability.CapAbort))
	assert.True(t, caps.Has(capability.CapAnswerUserAsk))
	// claudecode/codex(daemon 最常见 backend)都声明 CapSkills;占位矩阵对齐它们,
	// 这样 Prefetch 失败兜底时也不会误判远端不支持技能(enabledPluginsForTurn 不被吞)。
	assert.True(t, caps.Has(capability.CapSkills))
}

func TestPrefetch_CachesAndCapabilitiesReturnsIt(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	wantCaps := capability.Capabilities{
		Set: map[capability.Capability]bool{
			capability.CapSteer:               true,
			capability.CapCancelSteer:         true,
			capability.CapDrainSteer:          true,
			capability.CapAbort:               true,
			capability.CapAnswerUserAsk:       true,
			capability.CapToolPermission:      true,
			capability.CapSetPermission:       true,
			capability.CapForkSession:         true,
			capability.CapReportContextWindow: true,
		},
		PermissionModeMeta: capability.PermissionModeMeta{
			AllowedModes: []string{"default", "plan"},
			DefaultMode:  "default",
		},
	}
	cli.EXPECT().Call(gomock.Any(), wire.MethodCapabilities, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			cp := params.(wire.CapabilitiesParams)
			assert.Equal(t, "claudecode", cp.BackendType)
			*(result.(*wire.CapabilitiesResult)) = wire.CapabilitiesResult{Capabilities: wantCaps}
			return nil
		})
	require.NoError(t, rt.Prefetch(context.Background(), agent_backend_entity.TypeClaudeCode))

	caps := rt.Capabilities()
	assert.Equal(t, wantCaps, caps)

	// Second Prefetch with same backend type must hit cache → no extra RPC.
	require.NoError(t, rt.Prefetch(context.Background(), agent_backend_entity.TypeClaudeCode))
}

// TestRun_LaunchPermissionMode_PassThrough 钉死 RunAck.LaunchPermissionMode
// 在 remote 客户端被同步写入 RunResult.LaunchPermissionMode —— chat_svc 依赖
// 这条线在主进程侧持久化 session.PermissionModeAtLaunch(daemon 进程不
// bootstrap chat_repo,不能直接写库)。
func TestRun_LaunchPermissionMode_PassThrough(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			rp := params.(wire.RunParams)
			*(result.(*wire.RunAck)) = wire.RunAck{
				ConversationID:       rp.ConversationID,
				LaunchPermissionMode: "bypassPermissions",
			}
			return nil
		})

	_, runResult, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode", ID: 1, Name: "x"},
		SessionID: 77,
		UserText:  "go",
	})
	require.NoError(t, err)
	assert.Equal(t, "bypassPermissions", runResult.LaunchPermissionMode)
}

// TestWatchClose_InjectsStopErrAndClosesEvents 模拟 daemon 进程崩 / 网络断:
// client.Closed() 触发后,所有在飞 session 的 events channel 必须被关闭,
// RunResult.StopErr 必须 = ErrDaemonDisconnected,这样 chat_svc.runTurn 才能
// 跳出 `for ev := range events` 走 StreamError 通路解锁前端「生成中」状态。
func TestWatchClose_InjectsStopErrAndClosesEvents(t *testing.T) {
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)
	cli := mock_agentruntime.NewMockDaemonClientPort(ctrl)
	capture := newHandlerCapture()
	cli.EXPECT().Handle(gomock.Any(), gomock.Any()).DoAndReturn(
		func(method string, fn func(context.Context, json.RawMessage) (any, error)) {
			capture.record(method, fn)
		}).AnyTimes()
	closeCh := make(chan struct{})
	cli.EXPECT().Closed().Return((<-chan struct{})(closeCh)).AnyTimes()
	rt := New(protorpctest.WrapConnection(cli), WithConversationIDResolver(convOf))

	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			rp := params.(wire.RunParams)
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID}
			return nil
		})

	events, runResult, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: "claudecode", ID: 1, Name: "x"},
		SessionID: 99,
		UserText:  "hi",
	})
	require.NoError(t, err)

	// 模拟 daemon 断连。
	close(closeCh)

	// events 必须在合理时限内关闭。
	select {
	case _, ok := <-events:
		assert.False(t, ok, "events should be closed after daemon disconnect")
	case <-time.After(time.Second):
		t.Fatal("events channel not closed after daemon disconnect")
	}
	assert.ErrorIs(t, runResult.StopErr, ErrDaemonDisconnected)
}

// TestBuildRunParams_ForwardsMCPServers 钉死 buildRunParams 把 RunRequest.MCPServers
// 透传到 wire.RunParams，且 JSON round-trip 保留该字段（含 Headers map）。
// 修复 Edit 1–3 之前此测试会 FAIL（params.MCPServers 为 nil / 字段不存在）。
func TestBuildRunParams_ForwardsMCPServers(t *testing.T) {
	specs := []agentruntime.MCPServerSpec{{
		Name:    "group",
		URL:     "http://127.0.0.1:1/mcp/group/",
		Headers: map[string]string{"Authorization": "Bearer t"},
		Tools:   []string{"group_send"},
	}}
	params, err := New(newFakeConn(), WithConversationIDResolver(convOf)).buildRunParams(agentruntime.RunRequest{
		Backend:    &agent_backend_entity.AgentBackend{},
		SessionID:  9,
		MCPServers: specs,
	})
	if err != nil {
		t.Fatalf("buildRunParams: %v", err)
	}
	if len(params.MCPServers) != 1 || params.MCPServers[0].Name != "group" || params.MCPServers[0].Tools[0] != "group_send" {
		t.Fatalf("buildRunParams dropped MCPServers: %+v", params.MCPServers)
	}

	// wire JSON round-trip preserves the field.
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out wire.RunParams
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.MCPServers) != 1 || out.MCPServers[0].Headers["Authorization"] != "Bearer t" {
		t.Fatalf("MCPServers not preserved across wire JSON: %+v", out.MCPServers)
	}
}

// TestBuildRunParams_ForwardsFreshSession 钉死挂账修复(2026-08-11)的映射:chat_svc 在
// 本地 sess.ProviderSessionID 为空时置 RunRequest.FreshSession=true,必须随 wire 过线到
// daemon —— 漏传则 daemon 拿落库旧 id 续话,regenerate / provider 会话失效恢复又变回坏路径。
func TestBuildRunParams_ForwardsFreshSession(t *testing.T) {
	params, err := New(newFakeConn(), WithConversationIDResolver(convOf)).buildRunParams(agentruntime.RunRequest{
		Backend:      &agent_backend_entity.AgentBackend{},
		SessionID:    9,
		FreshSession: true,
	})
	if err != nil {
		t.Fatalf("buildRunParams: %v", err)
	}
	if !params.FreshSession {
		t.Fatalf("buildRunParams dropped FreshSession: %+v", params)
	}

	// wire JSON round-trip preserves the field.
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out wire.RunParams
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !out.FreshSession {
		t.Fatalf("FreshSession not preserved across wire JSON: %+v", out)
	}
}

// TestBuildRunParams_ForwardsEnabledPlugins 钉死 buildRunParams 把
// RunRequest.EnabledPlugins 透传到 wire.RunParams，且 JSON round-trip 保留该字段。
func TestBuildRunParams_ForwardsEnabledPlugins(t *testing.T) {
	plugins := map[string]bool{
		"browser@openai-bundled":     true,
		"superpowers@openai-curated": false,
	}
	params, err := New(newFakeConn(), WithConversationIDResolver(convOf)).buildRunParams(agentruntime.RunRequest{
		Backend:        &agent_backend_entity.AgentBackend{},
		SessionID:      9,
		EnabledPlugins: plugins,
	})
	if err != nil {
		t.Fatalf("buildRunParams: %v", err)
	}
	if len(params.EnabledPlugins) != 2 || !params.EnabledPlugins["browser@openai-bundled"] || params.EnabledPlugins["superpowers@openai-curated"] {
		t.Fatalf("buildRunParams dropped EnabledPlugins: %+v", params.EnabledPlugins)
	}

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out wire.RunParams
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.EnabledPlugins) != 2 || !out.EnabledPlugins["browser@openai-bundled"] || out.EnabledPlugins["superpowers@openai-curated"] {
		t.Fatalf("EnabledPlugins not preserved across wire JSON: %+v", out.EnabledPlugins)
	}
}

// TestBuildRunParams_ForwardsLLMProviderKey 钉死决策 9 的 wire 契约:req.LLMProviderKey
// (会话 provider_key 优先的 effectiveProviderKey)必须随 buildRunParams 透传到
// wire.RunParams.LLMProviderKey,daemon 才能按它自解。
func TestBuildRunParams_ForwardsLLMProviderKey(t *testing.T) {
	params, err := New(newFakeConn(), WithConversationIDResolver(convOf)).buildRunParams(agentruntime.RunRequest{
		Backend:        &agent_backend_entity.AgentBackend{},
		SessionID:      9,
		LLMProviderKey: "session-key",
	})
	if err != nil {
		t.Fatalf("buildRunParams: %v", err)
	}
	if params.LLMProviderKey != "session-key" {
		t.Fatalf("buildRunParams dropped LLMProviderKey: %+v", params)
	}

	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out wire.RunParams
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.LLMProviderKey != "session-key" {
		t.Fatalf("LLMProviderKey not preserved across wire JSON: %+v", out)
	}
}

// TestRun_SurfacesProviderFallbackKeyFromAck 钉死决策 9 信号回传的 remote 侧:daemon
// 在 ack.ProviderFallbackKey 回传被回退的会话 provider_key 时,remote runtime 必须把
// 它透进 RunResult,让 chat_svc 据此追加一条持久 notice(与本地 Q3 一致)。
func TestRun_SurfacesProviderFallbackKeyFromAck(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)
	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			rp := params.(wire.RunParams)
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderFallbackKey: "session-key"}
			return nil
		}).Times(1)

	events, runResult, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), ID: 1, Name: "x"},
		SessionID: 42,
		UserText:  "hello",
	})
	require.NoError(t, err)
	require.NotNil(t, runResult)
	assert.Equal(t, "session-key", runResult.ProviderFallbackKey, "ack 的 ProviderFallbackKey 必须透进 RunResult")

	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{ConversationID: convOf(42)})
	_, ok := <-events
	assert.False(t, ok)
}

func TestGoal_DispatchesWireRPCsWithBackendMetadata(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	objective := "ship goal rpc"
	status := "active"
	budget := 123
	req := agentruntime.GoalRequest{
		SessionID:         42,
		AgentID:           7,
		ProviderSessionID: "thread-goal",
		Backend:           &agent_backend_entity.AgentBackend{ID: 3, Type: string(agent_backend_entity.TypeCodex), Name: "codex"},
		Cwd:               "/tmp/work",
		Objective:         &objective,
		Status:            &status,
		TokenBudget:       &budget,
	}

	cli.EXPECT().Call(gomock.Any(), wire.MethodSetGoal, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			gp, ok := params.(wire.GoalParams)
			require.True(t, ok, "expected wire.GoalParams, got %T", params)
			assert.Equal(t, convOf(42), gp.ConversationID)
			assert.Equal(t, int64(7), gp.AgentID)
			assert.Equal(t, "thread-goal", gp.ProviderSessionID)
			assert.Equal(t, "/tmp/work", gp.Cwd)
			assert.Contains(t, string(gp.Backend), `"ID":3`)
			assert.Contains(t, string(gp.Backend), `"Name":"codex"`)
			assert.Contains(t, string(gp.Backend), `"Type":"codex"`)
			require.NotNil(t, gp.Objective)
			assert.Equal(t, "ship goal rpc", *gp.Objective)
			require.NotNil(t, gp.Status)
			assert.Equal(t, "active", *gp.Status)
			require.NotNil(t, gp.TokenBudget)
			assert.Equal(t, 123, *gp.TokenBudget)
			*(result.(*wire.GoalResult)) = wire.GoalResult{Goal: &agentruntime.Goal{ThreadID: "thread-goal", Objective: "ship goal rpc", Status: "active"}}
			return nil
		})
	goal, err := rt.SetGoal(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, goal)
	assert.Equal(t, "ship goal rpc", goal.Objective)

	cli.EXPECT().Call(gomock.Any(), wire.MethodGetGoal, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			gp, ok := params.(wire.GoalParams)
			require.True(t, ok, "expected wire.GoalParams, got %T", params)
			assert.Equal(t, "thread-goal", gp.ProviderSessionID)
			assert.Contains(t, string(gp.Backend), `"ID":3`)
			assert.Contains(t, string(gp.Backend), `"Name":"codex"`)
			assert.Contains(t, string(gp.Backend), `"Type":"codex"`)
			*(result.(*wire.GoalResult)) = wire.GoalResult{Goal: &agentruntime.Goal{ThreadID: "thread-goal", Objective: "ship goal rpc", Status: "active"}}
			return nil
		})
	_, err = rt.GetGoal(context.Background(), req)
	require.NoError(t, err)

	cli.EXPECT().Call(gomock.Any(), wire.MethodClearGoal, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			gp, ok := params.(wire.GoalParams)
			require.True(t, ok, "expected wire.GoalParams, got %T", params)
			assert.Equal(t, "thread-goal", gp.ProviderSessionID)
			assert.Contains(t, string(gp.Backend), `"ID":3`)
			assert.Contains(t, string(gp.Backend), `"Name":"codex"`)
			assert.Contains(t, string(gp.Backend), `"Type":"codex"`)
			*(result.(*wire.GoalClearResult)) = wire.GoalClearResult{Cleared: true}
			return nil
		})
	cleared, err := rt.ClearGoal(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, cleared)
}

func TestRun_DirectForkTurnAcceptsChangedProviderSessionID(t *testing.T) {
	_, cli, capture, rt := setupRemote(t)

	cli.EXPECT().Call(gomock.Any(), wire.MethodRun, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			rp, ok := params.(wire.RunParams)
			require.True(t, ok, "expected wire.RunParams, got %T", params)
			assert.Equal(t, convOf(42), rp.ConversationID)
			// A direct (non-Pi) run carries the resumed identity in the ack...
			*(result.(*wire.RunAck)) = wire.RunAck{ConversationID: rp.ConversationID, ProviderSessionID: "orig-session"}
			return nil
		}).Times(1)

	events, runResult, err := rt.Run(context.Background(), agentruntime.RunRequest{
		Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeClaudeCode), ID: 1, Name: "cc"},
		SessionID: 42,
		UserText:  "hello",
	})
	require.NoError(t, err)
	require.NotNil(t, runResult)
	assert.Equal(t, "orig-session", runResult.ProviderSessionID)

	// A claudecode fork (Regenerate) legitimately changes the provider session
	// identity during the turn: the ack carries the resumed session and the final
	// runResultDone carries the forked session. That final frame must still
	// finalize the run instead of being dropped as a stale generation.
	capture.deliver(t, wire.NotifyRunResultDone, wire.RunResultDoneFrame{
		ConversationID:    convOf(42),
		ProviderSessionID: "forked-session",
	})

	select {
	case _, ok := <-events:
		assert.False(t, ok, "events channel must close after runResultDone")
	case <-time.After(time.Second):
		t.Fatal("events channel never closed: fork runResultDone was dropped")
	}
	assert.Equal(t, "forked-session", runResult.ProviderSessionID)
}

// TestBuildRunParams_ForwardsLLMModelKey 钉死决策 11 的 wire 契约:RunParams 必须
// 携带 LLMModelKey。优先取执行侧解析结果的 ModelKey（chat_svc 对远端只透传目标
// key），未提供时回落 backend 主绑定固定模型；两者皆空 = provider-default。
func TestBuildRunParams_ForwardsLLMModelKey(t *testing.T) {
	t.Run("from effective config", func(t *testing.T) {
		params, err := New(newFakeConn(), WithConversationIDResolver(convOf)).buildRunParams(agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{LLMModelKey: "backend-model"},
			SessionID: 9,
			Effective: &agentruntime.EffectiveLLMConfig{
				Mode:     agentruntime.EffectiveModeFixedModel,
				ModelKey: "session-model",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "session-model", params.LLMModelKey, "执行侧 ModelKey 优先于 backend 绑定")
	})

	t.Run("fallback to backend fixed model", func(t *testing.T) {
		params, err := New(newFakeConn(), WithConversationIDResolver(convOf)).buildRunParams(agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{LLMModelKey: "backend-model"},
			SessionID: 9,
		})
		require.NoError(t, err)
		assert.Equal(t, "backend-model", params.LLMModelKey, "无执行侧结果时回落 backend 固定模型")
	})

	t.Run("provider-default stays empty", func(t *testing.T) {
		params, err := New(newFakeConn(), WithConversationIDResolver(convOf)).buildRunParams(agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{LLMProviderKey: "pk"},
			SessionID: 9,
		})
		require.NoError(t, err)
		assert.Equal(t, "", params.LLMModelKey, "provider-default 的 model key 为空")
	})

	t.Run("pinned provider-default beats backend fixed model", func(t *testing.T) {
		// spec 决策 1：会话钉了 Provider 且选 provider-default（ModelKey 空）时，即使
		// backend 主绑定同家并固定了模型，wire 也必须保持空 model key（provider-default），
		// 不能被 backend 的固定模型带偏 —— 远端与本地同一解析语义（sessionModelKeyFor）。
		params, err := New(newFakeConn(), WithConversationIDResolver(convOf)).buildRunParams(agentruntime.RunRequest{
			Backend:   &agent_backend_entity.AgentBackend{LLMProviderKey: "pk", LLMModelKey: "backend-model"},
			SessionID: 9,
			Effective: &agentruntime.EffectiveLLMConfig{
				Mode:        agentruntime.EffectiveModeProviderDefault,
				ProviderKey: "pk",
				ModelKey:    "",
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "", params.LLMModelKey, "会话钉 provider-default 必须保持空 model key，不能被 backend 固定模型带偏")
	})
}

// TestGoalParams_ForwardsLLMTargetKeys 钉死决策 11 的 Goal 侧 wire 契约:GoalParams
// 补齐了 LLMProviderKey + LLMModelKey（与 Run 同形），goal 与 turn 共用同一会话池，
// 两边解析必须同源。
func TestGoalParams_ForwardsLLMTargetKeys(t *testing.T) {
	_, cli, _, rt := setupRemote(t)
	req := agentruntime.GoalRequest{
		SessionID: 42,
		Backend:   &agent_backend_entity.AgentBackend{Type: string(agent_backend_entity.TypeCodex), Name: "codex"},
		Effective: &agentruntime.EffectiveLLMConfig{
			Mode:        agentruntime.EffectiveModeFixedModel,
			ProviderKey: "session-provider",
			ModelKey:    "session-model",
		},
	}
	cli.EXPECT().Call(gomock.Any(), wire.MethodSetGoal, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			gp, ok := params.(wire.GoalParams)
			require.True(t, ok)
			assert.Equal(t, "session-provider", gp.LLMProviderKey)
			assert.Equal(t, "session-model", gp.LLMModelKey)
			*(result.(*wire.GoalResult)) = wire.GoalResult{Goal: &agentruntime.Goal{ThreadID: "t"}}
			return nil
		})
	_, err := rt.SetGoal(context.Background(), req)
	require.NoError(t, err)

	// 未提供执行侧结果时回落 backend 绑定。
	cli.EXPECT().Call(gomock.Any(), wire.MethodGetGoal, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, params any, result any) error {
			gp, ok := params.(wire.GoalParams)
			require.True(t, ok)
			assert.Equal(t, "", gp.LLMProviderKey)
			assert.Equal(t, "", gp.LLMModelKey)
			*(result.(*wire.GoalResult)) = wire.GoalResult{}
			return nil
		})
	_, err = rt.GetGoal(context.Background(), agentruntime.GoalRequest{SessionID: 42, Backend: &agent_backend_entity.AgentBackend{Type: "codex"}})
	require.NoError(t, err)
}
