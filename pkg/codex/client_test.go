package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientStream_StartThreadAndMapsEvents(t *testing.T) {
	// Given a Codex app-server that accepts a fresh thread and streams one turn.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)

		initReq := readRPCReq(t, sc)
		assert.Equal(t, "initialize", initReq.Method)
		respondRPC(h, initReq, map[string]any{})

		initialized := readRPCReq(t, sc)
		assert.Equal(t, "initialized", initialized.Method)
		assert.Empty(t, initialized.ID)

		startReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/start", startReq.Method)
		assert.JSONEq(t, `{"cwd":"/tmp/work","developerInstructions":"be brief","sandbox":"read-only","approvalPolicy":"never"}`, string(startReq.Params))
		respondRPC(h, startReq, map[string]any{"thread": map[string]any{"id": "thread-new", "cwd": "/tmp/work"}})

		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-new","input":[{"type":"text","text":"hello","text_elements":[]}]}`, string(turnReq.Params))
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}})

		h.send(map[string]any{
			"method": "item/agentMessage/delta",
			"params": map[string]any{"threadId": "thread-new", "turnId": "turn-1", "itemId": "msg-1", "delta": "pong"},
		})
		h.send(map[string]any{
			"method": "thread/tokenUsage/updated",
			"params": map[string]any{
				"threadId": "thread-new",
				"turnId":   "turn-1",
				"tokenUsage": map[string]any{
					"last":               map[string]any{"inputTokens": 3, "cachedInputTokens": 1, "outputTokens": 2, "reasoningOutputTokens": 4, "totalTokens": 10},
					"total":              map[string]any{"inputTokens": 3, "cachedInputTokens": 1, "outputTokens": 2, "reasoningOutputTokens": 4, "totalTokens": 10},
					"modelContextWindow": 258400,
				},
			},
		})
		h.send(map[string]any{
			"method": "turn/completed",
			"params": map[string]any{"threadId": "thread-new", "turnId": "turn-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}},
		})
	}

	client := New(
		WithBinary("codex-test"),
		WithCwd("/tmp/work"),
		WithSystemPrompt("be brief"),
		WithSandbox(SandboxReadOnly),
		WithApproval(ApprovalNever),
		WithAppServerRunnerForTesting(runner),
	)

	// When a prompt is streamed.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "hello")
	require.NoError(t, err)

	// Then text, usage, and done are exposed as codex package events.
	var events []Event
	for stream.Next() {
		events = append(events, stream.Event())
	}
	require.NoError(t, stream.Close(ctx))
	require.Len(t, events, 3)
	assert.Equal(t, EventTextDelta, events[0].Kind)
	assert.Equal(t, "pong", events[0].Text)
	assert.Equal(t, "thread-new", events[0].SessionID)
	assert.Equal(t, EventUsage, events[1].Kind)
	assert.Equal(t, 3, events[1].Usage.PromptTokens)
	assert.Equal(t, 2, events[1].Usage.CompletionTokens)
	assert.Equal(t, 4, events[1].Usage.ReasoningTokens)
	assert.Equal(t, 258400, events[1].ContextWindow)
	assert.Equal(t, EventDone, events[2].Kind)
	assert.Equal(t, 258400, events[2].ContextWindow)
	assert.Equal(t, "thread-new", stream.SessionID())

	require.Len(t, runner.opts, 1)
	assert.Equal(t, "codex-test", runner.opts[0].Binary)
	assert.Equal(t, []string{"app-server", "--listen", "stdio://"}, runner.opts[0].Args)
	assert.Equal(t, "/tmp/work", runner.opts[0].Cwd)
}

func TestStream_IgnoresSubagentThreadMessages(t *testing.T) {
	// Given a main Codex turn that receives app-server notifications for a
	// different collab-agent thread.
	stream := newStream(nil, 0, "thread-main", "turn-main", "")
	seen := map[string]struct{}{}

	// A foreign turn/started notification must not rebind the main stream.
	done := stream.handleInbound(context.Background(), appInbound{
		Kind:   appInboundNotification,
		Method: appMethodTurnStarted,
		Params: json.RawMessage(`{"threadId":"thread-sub-1","turn":{"id":"turn-sub-1","status":"inProgress"}}`),
	}, seen)
	require.False(t, done)

	// When the subagent thread streams a message delta.
	done = stream.handleInbound(context.Background(), appInbound{
		Kind:   appInboundNotification,
		Method: appMethodItemAgentMessageDelta,
		Params: json.RawMessage(`{"threadId":"thread-sub-1","turnId":"turn-sub-1","itemId":"sub-msg-1","delta":"subagent output"}`),
	}, seen)

	// Then it is not exposed as a main assistant TextDelta and the main thread id
	// is preserved.
	require.False(t, done)
	assertNoStreamEvent(t, stream)
	assert.Equal(t, "thread-main", stream.SessionID())

	// When the same foreign thread completes an agentMessage item with full text.
	done = stream.handleInbound(context.Background(), appInbound{
		Kind:   appInboundNotification,
		Method: appMethodItemCompleted,
		Params: json.RawMessage(`{"threadId":"thread-sub-1","turnId":"turn-sub-1","item":{"type":"agentMessage","id":"sub-msg-2","text":"second subagent output"}}`),
	}, seen)

	// Then that full text is also kept out of the main assistant stream.
	require.False(t, done)
	assertNoStreamEvent(t, stream)
	assert.Equal(t, "thread-main", stream.SessionID())

	// When multiple subagent threads interleave with the main thread.
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"threadId":"thread-sub-1","turnId":"turn-sub-1","itemId":"sub-msg-3","delta":"subagent one"}`),
		json.RawMessage(`{"threadId":"thread-sub-2","turnId":"turn-sub-2","itemId":"sub-msg-4","delta":"subagent two"}`),
		json.RawMessage(`{"threadId":"thread-main","turnId":"turn-main","itemId":"main-msg-1","delta":"main output"}`),
	} {
		done = stream.handleInbound(context.Background(), appInbound{
			Kind:   appInboundNotification,
			Method: appMethodItemAgentMessageDelta,
			Params: raw,
		}, seen)
		require.False(t, done)
	}

	// Then only the main-thread delta is delivered.
	select {
	case ev := <-stream.events:
		require.Equal(t, EventTextDelta, ev.Kind)
		assert.Equal(t, "thread-main", ev.SessionID)
		assert.Equal(t, "main output", ev.Text)
	default:
		t.Fatalf("expected main thread text event")
	}
	assertNoStreamEvent(t, stream)
	assert.Equal(t, "thread-main", stream.SessionID())
}

func TestClientStream_ResumeThread(t *testing.T) {
	// Given an existing provider thread id.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		resumeReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/resume", resumeReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-old","excludeTurns":true,"cwd":"/tmp/work","approvalPolicy":"never"}`, string(resumeReq.Params))
		respondRPC(h, resumeReq, map[string]any{"thread": map[string]any{"id": "thread-old", "cwd": "/tmp/work"}})

		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-old","input":[{"type":"text","text":"again","text_elements":[]}]}`, string(turnReq.Params))
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-2", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-old", "turnId": "turn-2", "turn": map[string]any{"id": "turn-2", "status": "completed"}}})
	}

	client := New(
		WithCwd("/tmp/work"),
		WithApproval(ApprovalNever),
		WithAppServerRunnerForTesting(runner),
	)

	// When Stream runs with Resume.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "again", Resume("thread-old"))
	require.NoError(t, err)
	for stream.Next() {
	}
	require.NoError(t, stream.Close(ctx))

	// Then the app-server resume path is used and the same thread id is retained.
	assert.Equal(t, "thread-old", stream.SessionID())
}

func TestClientStreamInput_SendsImageWithoutText(t *testing.T) {
	// Given a Codex app-server that accepts multimodal user input,
	// when the client starts a turn with only a local image,
	// then turn/start receives the image without placeholder text.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		startReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/start", startReq.Method)
		respondRPC(h, startReq, map[string]any{"thread": map[string]any{"id": "thread-image"}})

		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		assert.JSONEq(t, `{
			"threadId":"thread-image",
			"input":[
				{"type":"localImage","path":"/tmp/screenshot.png","detail":"high"}
			]
		}`, string(turnReq.Params))
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-image", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-image", "turnId": "turn-image", "turn": map[string]any{"id": "turn-image", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.StreamInput(ctx, []UserInput{
		LocalImageInput("/tmp/screenshot.png", ImageDetailHigh),
	})
	require.NoError(t, err)
	for stream.Next() {
	}
	require.NoError(t, stream.Close(ctx))
}

func TestSessionStream_ReusesSingleAppServerAcrossTurns(t *testing.T) {
	// Given a persistent Codex app-server session.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		startReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/start", startReq.Method)
		respondRPC(h, startReq, map[string]any{"thread": map[string]any{"id": "thread-reused"}})

		firstTurn := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", firstTurn.Method)
		assert.JSONEq(t, `{"threadId":"thread-reused","input":[{"type":"text","text":"first","text_elements":[]}]}`, string(firstTurn.Params))
		respondRPC(h, firstTurn, map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-reused", "turnId": "turn-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}})

		secondTurn := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", secondTurn.Method)
		assert.JSONEq(t, `{"threadId":"thread-reused","input":[{"type":"text","text":"second","text_elements":[]}]}`, string(secondTurn.Params))
		respondRPC(h, secondTurn, map[string]any{"turn": map[string]any{"id": "turn-2", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-reused", "turnId": "turn-2", "turn": map[string]any{"id": "turn-2", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sess, err := client.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	// When two turns are streamed through the same session.
	first, err := sess.Stream(ctx, "first")
	require.NoError(t, err)
	for first.Next() {
	}
	require.NoError(t, first.Err())

	second, err := sess.Stream(ctx, "second")
	require.NoError(t, err)
	for second.Next() {
	}
	require.NoError(t, second.Err())

	// Then only one app-server process is started and the thread id is reused.
	require.Len(t, runner.opts, 1)
	assert.Equal(t, "thread-reused", sess.ID())
	assert.Equal(t, "thread-reused", second.SessionID())
}

func TestSessionStream_GoalTurnUsesStartedTurnID(t *testing.T) {
	// Given an active goal whose turn/start response id differs from the actual
	// turn/started notification id (Codex CLI 0.144.4 behavior).
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		startReq := readRPCReq(t, sc)
		assert.Equal(t, appMethodThreadStart, startReq.Method)
		respondRPC(h, startReq, map[string]any{"thread": map[string]any{"id": "thread-goal-stream"}})

		goalReq := readRPCReq(t, sc)
		assert.Equal(t, appMethodThreadGoalSet, goalReq.Method)
		respondRPC(h, goalReq, map[string]any{"goal": map[string]any{
			"threadId": "thread-goal-stream", "objective": "ship it", "status": "active",
		}})

		turnReq := readRPCReq(t, sc)
		assert.Equal(t, appMethodTurnStart, turnReq.Method)
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-request", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/started", "params": map[string]any{
			"threadId": "thread-goal-stream", "turn": map[string]any{"id": "turn-actual", "status": "inProgress"},
		}})
		h.send(map[string]any{"method": appMethodItemAgentMessageDelta, "params": map[string]any{
			"threadId": "thread-goal-stream", "turnId": "turn-actual", "itemId": "msg-1", "delta": "goal output",
		}})
		h.send(map[string]any{"method": appMethodTurnCompleted, "params": map[string]any{
			"threadId": "thread-goal-stream", "turn": map[string]any{"id": "turn-actual", "status": "completed"},
		}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sess, err := client.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	objective := "ship it"
	status := GoalStatusActive
	_, err = sess.SetGoal(ctx, GoalUpdate{Objective: &objective, Status: &status})
	require.NoError(t, err)
	stream, err := sess.Stream(ctx, "execute the goal")
	require.NoError(t, err)

	var events []Event
	for stream.Next() {
		events = append(events, stream.Event())
	}
	require.NoError(t, stream.Err())
	require.Len(t, events, 2)
	assert.Equal(t, EventTextDelta, events[0].Kind)
	assert.Equal(t, "goal output", events[0].Text)
	assert.Equal(t, EventDone, events[1].Kind)
}

func TestClientCompact_SendsThreadCompactStartRPC(t *testing.T) {
	// Given an existing Codex app-server thread.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		resumeReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/resume", resumeReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-old","excludeTurns":true,"cwd":"/tmp/work","approvalPolicy":"never"}`, string(resumeReq.Params))
		respondRPC(h, resumeReq, map[string]any{"thread": map[string]any{"id": "thread-old", "cwd": "/tmp/work"}})

		compactReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/compact/start", compactReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-old"}`, string(compactReq.Params))
		respondRPC(h, compactReq, map[string]any{})

		h.send(map[string]any{
			"method": "thread/compacted",
			"params": map[string]any{"threadId": "thread-old", "turnId": "compact-1"},
		})
	}

	client := New(
		WithCwd("/tmp/work"),
		WithApproval(ApprovalNever),
		WithAppServerRunnerForTesting(runner),
	)

	// When Compact runs.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Compact(ctx, "thread-old")
	require.NoError(t, err)

	// Then a manual compact boundary and done event are exposed.
	var events []Event
	for stream.Next() {
		events = append(events, stream.Event())
	}
	require.NoError(t, stream.Close(ctx))
	require.Len(t, events, 2)
	assert.Equal(t, EventCompactBoundary, events[0].Kind)
	require.NotNil(t, events[0].Compact)
	assert.Equal(t, "manual", events[0].Compact.Trigger)
	assert.Equal(t, "thread-old", events[0].SessionID)
	assert.Equal(t, EventDone, events[1].Kind)
	assert.Equal(t, "thread-old", stream.SessionID())
	assert.Equal(t, TurnStateCompleted, stream.State())
}

func TestClientGoal_SendsThreadGoalRPCs(t *testing.T) {
	// Given an existing Codex app-server thread.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		resumeReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/resume", resumeReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-goal","excludeTurns":true,"cwd":"/tmp/work","approvalPolicy":"never"}`, string(resumeReq.Params))
		respondRPC(h, resumeReq, map[string]any{"thread": map[string]any{"id": "thread-goal", "cwd": "/tmp/work"}})

		goalReq := readRPCReq(t, sc)
		switch goalReq.Method {
		case "thread/goal/set":
			assert.JSONEq(t, `{"threadId":"thread-goal","objective":"ship goal rpc","status":"active","tokenBudget":1234}`, string(goalReq.Params))
			respondRPC(h, goalReq, map[string]any{"goal": map[string]any{
				"threadId":        "thread-goal",
				"objective":       "ship goal rpc",
				"status":          "active",
				"tokenBudget":     1234,
				"tokensUsed":      0,
				"timeUsedSeconds": 0,
				"createdAt":       11,
				"updatedAt":       12,
			}})
		case "thread/goal/get":
			assert.JSONEq(t, `{"threadId":"thread-goal"}`, string(goalReq.Params))
			respondRPC(h, goalReq, map[string]any{"goal": map[string]any{
				"threadId":        "thread-goal",
				"objective":       "ship goal rpc",
				"status":          "active",
				"tokenBudget":     1234,
				"tokensUsed":      5,
				"timeUsedSeconds": 6,
				"createdAt":       11,
				"updatedAt":       12,
			}})
		case "thread/goal/clear":
			assert.JSONEq(t, `{"threadId":"thread-goal"}`, string(goalReq.Params))
			respondRPC(h, goalReq, map[string]any{"cleared": true})
		default:
			t.Fatalf("unexpected goal method %q", goalReq.Method)
		}
	}

	client := New(
		WithCwd("/tmp/work"),
		WithApproval(ApprovalNever),
		WithAppServerRunnerForTesting(runner),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// When goal metadata is set, read, and cleared.
	objective := "ship goal rpc"
	status := GoalStatusActive
	budget := 1234
	setGoal, err := client.SetGoal(ctx, "thread-goal", GoalUpdate{
		Objective:   &objective,
		Status:      &status,
		TokenBudget: &budget,
	})
	require.NoError(t, err)
	require.NotNil(t, setGoal)
	assert.Equal(t, "ship goal rpc", setGoal.Objective)
	assert.Equal(t, GoalStatusActive, setGoal.Status)
	require.NotNil(t, setGoal.TokenBudget)
	assert.Equal(t, 1234, *setGoal.TokenBudget)

	gotGoal, err := client.GetGoal(ctx, "thread-goal")
	require.NoError(t, err)
	require.NotNil(t, gotGoal)
	assert.Equal(t, 5, gotGoal.TokensUsed)

	cleared, err := client.ClearGoal(ctx, "thread-goal")
	require.NoError(t, err)
	assert.True(t, cleared)
}

func TestSessionSetGoal_StartsThreadBeforeFirstTurn(t *testing.T) {
	// Given a newly opened Codex session with no thread id yet.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondAppServerInit(t, h, sc)

		respondThreadStart(t, h, sc, `{"cwd":"/tmp/work","approvalPolicy":"never"}`, "thread-new-goal")

		goalReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/goal/set", goalReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-new-goal","objective":"ship before turn","status":"active"}`, string(goalReq.Params))
		respondRPC(h, goalReq, map[string]any{"goal": goalWire("thread-new-goal", "ship before turn", 0, 0)})
	}

	client := New(
		WithCwd("/tmp/work"),
		WithApproval(ApprovalNever),
		WithAppServerRunnerForTesting(runner),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess, err := client.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	// When setting a goal before the first turn, then the wrapper creates the
	// Codex thread and sets the goal against it.
	objective := "ship before turn"
	status := GoalStatusActive
	goal, err := sess.SetGoal(ctx, GoalUpdate{Objective: &objective, Status: &status})
	require.NoError(t, err)
	require.NotNil(t, goal)
	assert.Equal(t, "thread-new-goal", sess.ID())
	assert.Equal(t, "thread-new-goal", goal.ThreadID)
	assert.Equal(t, "ship before turn", goal.Objective)
}

func TestSessionGetGoal_ResumesThreadAndSendsGoalGetRPC(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondAppServerInit(t, h, sc)

		respondThreadResume(t, h, sc, `{"threadId":"thread-existing-goal","excludeTurns":true,"cwd":"/tmp/work","approvalPolicy":"never"}`, "thread-existing-goal")

		goalReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/goal/get", goalReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-existing-goal"}`, string(goalReq.Params))
		respondRPC(h, goalReq, map[string]any{"goal": goalWire("thread-existing-goal", "read existing goal", 7, 9)})
	}

	client := New(
		WithCwd("/tmp/work"),
		WithApproval(ApprovalNever),
		WithAppServerRunnerForTesting(runner),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sess, err := client.OpenSession(ctx, Resume("thread-existing-goal"))
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	goal, err := sess.GetGoal(ctx)
	require.NoError(t, err)
	require.NotNil(t, goal)
	assert.Equal(t, "thread-existing-goal", goal.ThreadID)
	assert.Equal(t, "read existing goal", goal.Objective)
	assert.Equal(t, 7, goal.TokensUsed)
}

func TestSessionClearGoal_ResumesThreadAndSendsGoalClearRPC(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondAppServerInit(t, h, sc)

		respondThreadResume(t, h, sc, `{"threadId":"thread-clear-goal","excludeTurns":true,"cwd":"/tmp/work","approvalPolicy":"never"}`, "thread-clear-goal")

		goalReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/goal/clear", goalReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-clear-goal"}`, string(goalReq.Params))
		respondRPC(h, goalReq, map[string]any{"cleared": true})
	}

	client := New(
		WithCwd("/tmp/work"),
		WithApproval(ApprovalNever),
		WithAppServerRunnerForTesting(runner),
	)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	sess, err := client.OpenSession(ctx, Resume("thread-clear-goal"))
	require.NoError(t, err)
	defer func() { _ = sess.Close(context.Background()) }()

	cleared, err := sess.ClearGoal(ctx)
	require.NoError(t, err)
	assert.True(t, cleared)
}

func TestClientGoal_RequiresThreadID(t *testing.T) {
	client := New()
	ctx := context.Background()

	_, err := client.GetGoal(ctx, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "thread id is required")

	objective := "x"
	_, err = client.SetGoal(ctx, "", GoalUpdate{Objective: &objective})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "thread id is required")

	_, err = client.ClearGoal(ctx, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "thread id is required")
}

func TestClientStream_PassesModelAndConfigOverrides(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		startReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/start", startReq.Method)
		assert.JSONEq(t, `{"model":"gpt-5-codex","approvalPolicy":"never"}`, string(startReq.Params))
		respondRPC(h, startReq, map[string]any{"thread": map[string]any{"id": "thread-config"}})

		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-config", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-config", "turnId": "turn-config", "turn": map[string]any{"id": "turn-config", "status": "completed"}}})
	}

	client := New(
		WithModel("gpt-5-codex"),
		WithConfig(`model_provider="agentre-gateway"`),
		WithConfig(`model_providers.agentre-gateway.base_url="http://127.0.0.1:60080"`),
		WithAppServerRunnerForTesting(runner),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "hello")
	require.NoError(t, err)
	for stream.Next() {
	}
	require.NoError(t, stream.Close(ctx))

	require.Len(t, runner.opts, 1)
	assert.Equal(t, []string{
		"app-server", "--listen", "stdio://",
		"--config", `model_provider="agentre-gateway"`,
		"--config", `model_providers.agentre-gateway.base_url="http://127.0.0.1:60080"`,
	}, runner.opts[0].Args)
}

func TestClientStream_PassesCollaborationModeWithThreadModel(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		startReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/start", startReq.Method)
		respondRPC(h, startReq, map[string]any{
			"thread": map[string]any{"id": "thread-plan"},
			"model":  "gpt-5.5",
		})

		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		assert.JSONEq(t, `{
			"threadId":"thread-plan",
			"input":[{"type":"text","text":"plan this","text_elements":[]}],
			"collaborationMode":{"mode":"plan","settings":{"model":"gpt-5.5"}}
		}`, string(turnReq.Params))
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-plan", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-plan", "turnId": "turn-plan", "turn": map[string]any{"id": "turn-plan", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream, err := client.Stream(ctx, "plan this", RunCollaborationMode(CollaborationPlan))
	require.NoError(t, err)
	for stream.Next() {
	}
	require.NoError(t, stream.Close(ctx))
}

func TestSession_ModelUsesThreadStartModel(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		startReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/start", startReq.Method)
		respondRPC(h, startReq, map[string]any{
			"thread": map[string]any{"id": "thread-model"},
			"model":  "gpt-5.6-sol",
		})

		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-model", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-model", "turnId": "turn-model", "turn": map[string]any{"id": "turn-model", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	sess, err := client.OpenSession(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, sess.Close(context.Background())) }()

	stream, err := sess.Stream(ctx, "hello")
	require.NoError(t, err)
	for stream.Next() {
	}
	require.NoError(t, stream.Close(ctx))

	assert.Equal(t, "gpt-5.6-sol", sess.Model())
}

func TestTurnStartParams_CollaborationModeUsesDefaultModel(t *testing.T) {
	params, err := turnStartParams(
		appThreadStartResult{ThreadID: "thread-default"},
		"plan this",
		CollaborationPlan,
		"",
	)
	require.NoError(t, err)

	raw, err := json.Marshal(params)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"threadId":"thread-default",
		"input":[{"type":"text","text":"plan this","text_elements":[]}],
		"collaborationMode":{"mode":"plan","settings":{"model":"gpt-5.5"}}
	}`, string(raw))
}

func TestClientStream_EmitsPlanUpdatedAndUpdatePlanTool(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		respondRPC(h, readRPCReq(t, sc), map[string]any{
			"thread": map[string]any{"id": "thread-plan-events"},
			"model":  "gpt-5.5",
		})
		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-plan-events", "status": "inProgress"}})

		h.send(map[string]any{
			"method": "turn/plan/updated",
			"params": map[string]any{
				"threadId": "thread-plan-events",
				"turnId":   "turn-plan-events",
				"plan": []map[string]any{{
					"step":   "Inspect the files",
					"status": "completed",
				}, {
					"step":   "Describe the change",
					"status": "inProgress",
				}},
			},
		})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-plan-events", "turnId": "turn-plan-events", "turn": map[string]any{"id": "turn-plan-events", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream, err := client.Stream(ctx, "plan this", RunCollaborationMode(CollaborationPlan))
	require.NoError(t, err)
	var events []Event
	for stream.Next() {
		events = append(events, stream.Event())
	}
	require.NoError(t, stream.Close(ctx))

	require.Len(t, events, 4)
	assert.Equal(t, EventPlanUpdated, events[0].Kind)
	require.Len(t, events[0].Plan, 2)
	assert.Equal(t, "Inspect the files", events[0].Plan[0].Step)
	assert.Equal(t, "completed", events[0].Plan[0].Status)
	assert.Equal(t, EventPreToolUse, events[1].Kind)
	assert.Equal(t, "update_plan", events[1].Tool.Name)
	assert.Equal(t, EventPostToolUse, events[2].Kind)
	assert.Equal(t, EventDone, events[3].Kind)
}

func TestClientStream_EmitsCompletedPlanItemAsPlanText(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		respondRPC(h, readRPCReq(t, sc), map[string]any{
			"thread": map[string]any{"id": "thread-plan-item"},
			"model":  "gpt-5.5",
		})
		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-plan-item", "status": "inProgress"}})

		h.send(map[string]any{
			"method": "item/started",
			"params": map[string]any{
				"threadId": "thread-plan-item",
				"turnId":   "turn-plan-item",
				"item":     map[string]any{"type": "plan", "id": "turn-plan-item-plan", "text": ""},
			},
		})
		h.send(map[string]any{
			"method": "item/plan/delta",
			"params": map[string]any{
				"threadId": "thread-plan-item",
				"turnId":   "turn-plan-item",
				"itemId":   "turn-plan-item-plan",
				"delta":    "# Plan\n",
			},
		})
		h.send(map[string]any{
			"method": "item/completed",
			"params": map[string]any{
				"threadId": "thread-plan-item",
				"turnId":   "turn-plan-item",
				"item": map[string]any{
					"type": "plan",
					"id":   "turn-plan-item-plan",
					"text": "# Plan\n\n1. Inspect files\n2. Report findings\n",
				},
			},
		})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-plan-item", "turnId": "turn-plan-item", "turn": map[string]any{"id": "turn-plan-item", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream, err := client.Stream(ctx, "plan this", RunCollaborationMode(CollaborationPlan))
	require.NoError(t, err)
	var events []Event
	for stream.Next() {
		events = append(events, stream.Event())
	}
	require.NoError(t, stream.Close(ctx))

	require.Len(t, events, 3)
	assert.Equal(t, EventPlanUpdated, events[0].Kind)
	assert.Equal(t, "# Plan\n", events[0].PlanText)
	assert.Equal(t, EventPlanUpdated, events[1].Kind)
	assert.Equal(t, "# Plan\n\n1. Inspect files\n2. Report findings\n", events[1].PlanText)
	assert.Equal(t, EventDone, events[2].Kind)
}

func TestClientForkThread(t *testing.T) {
	// Given a source Codex thread with at least one rollout.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		forkReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/fork", forkReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-source","cwd":"/tmp/work","sandbox":"workspace-write","approvalPolicy":"never"}`, string(forkReq.Params))
		respondRPC(h, forkReq, map[string]any{
			"thread": map[string]any{"id": "thread-forked", "forkedFromId": "thread-source", "cwd": "/tmp/work"},
		})
	}

	client := New(
		WithCwd("/tmp/work"),
		WithSandbox(SandboxWorkspaceWrite),
		WithApproval(ApprovalNever),
		WithAppServerRunnerForTesting(runner),
	)

	// When thread/fork is called.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := client.ForkThread(ctx, "thread-source")

	// Then the new thread id and source thread id are returned.
	require.NoError(t, err)
	assert.Equal(t, "thread-forked", res.ThreadID)
	assert.Equal(t, "thread-source", res.ForkedFromID)
}

func TestClientRollbackThread(t *testing.T) {
	// Given a Codex thread with multiple turns.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		resumeReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/resume", resumeReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-source","excludeTurns":true,"cwd":"/tmp/work","approvalPolicy":"never"}`, string(resumeReq.Params))
		respondRPC(h, resumeReq, map[string]any{"thread": map[string]any{"id": "thread-source", "cwd": "/tmp/work"}})

		rollbackReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/rollback", rollbackReq.Method)
		assert.JSONEq(t, `{"threadId":"thread-source","numTurns":2}`, string(rollbackReq.Params))
		respondRPC(h, rollbackReq, map[string]any{
			"thread": map[string]any{"id": "thread-source", "cwd": "/tmp/work"},
		})
	}

	client := New(
		WithCwd("/tmp/work"),
		WithAppServerRunnerForTesting(runner),
	)

	// When thread/rollback is called.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	res, err := client.RollbackThread(ctx, "thread-source", 2)

	// Then the same thread id is retained after the destructive rollback.
	require.NoError(t, err)
	assert.Equal(t, "thread-source", res.ThreadID)
}

func TestClientStream_ErrorsWhenAppServerExitsBeforeTurnCompleted(t *testing.T) {
	// Given an app-server that accepts turn/start and then exits before turn/completed.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"id": "thread-dies"}})
		respondRPC(h, readRPCReq(t, sc), map[string]any{"turn": map[string]any{"id": "turn-dies", "status": "inProgress"}})
		go func() {
			time.Sleep(20 * time.Millisecond)
			_ = h.Kill()
		}()
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "hello")
	require.NoError(t, err)

	require.True(t, stream.Next())
	ev := stream.Event()
	assert.Equal(t, EventError, ev.Kind)
	assert.ErrorIs(t, ev.Err, ErrProcessDead)
	assert.False(t, stream.Next())
}

func TestClientStream_EmitsRetryableAppServerErrorNotification(t *testing.T) {
	// Given app-server reports a transient upstream disconnect that it will retry.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"id": "thread-retry"}})
		respondRPC(h, readRPCReq(t, sc), map[string]any{"turn": map[string]any{"id": "turn-retry", "status": "inProgress"}})

		h.send(map[string]any{
			"method": "error",
			"params": map[string]any{
				"threadId":  "thread-retry",
				"turnId":    "turn-retry",
				"willRetry": true,
				"error": map[string]any{
					"message":           "Reconnecting... 1/5",
					"additionalDetails": "We're currently experiencing high demand, which may cause temporary errors.",
					"codexErrorInfo": map[string]any{
						"responseStreamDisconnected": map[string]any{"httpStatusCode": nil},
					},
				},
			},
		})
		h.send(map[string]any{
			"method": "item/agentMessage/delta",
			"params": map[string]any{"threadId": "thread-retry", "turnId": "turn-retry", "itemId": "msg-1", "delta": "recovered"},
		})
		h.send(map[string]any{
			"method": "turn/completed",
			"params": map[string]any{
				"threadId": "thread-retry",
				"turnId":   "turn-retry",
				"turn":     map[string]any{"id": "turn-retry", "status": "completed"},
			},
		})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "hello")
	require.NoError(t, err)

	var events []Event
	for stream.Next() {
		events = append(events, stream.Event())
	}
	require.NoError(t, stream.Close(ctx))
	require.Len(t, events, 3)
	require.Equal(t, EventRetry, events[0].Kind)
	require.NotNil(t, events[0].Retry)
	assert.Equal(t, "Reconnecting... 1/5", events[0].Retry.Message)
	assert.Equal(t, "We're currently experiencing high demand, which may cause temporary errors.", events[0].Retry.AdditionalDetails)
	assert.Equal(t, 1, events[0].Retry.Attempt)
	assert.Equal(t, 5, events[0].Retry.MaxAttempts)
	assert.Equal(t, EventTextDelta, events[1].Kind)
	assert.Equal(t, "recovered", events[1].Text)
	assert.Equal(t, EventDone, events[2].Kind)
}

func TestClientStream_ErrorsWhenThreadStartResponseMissesID(t *testing.T) {
	// Given app-server returns an invalid thread/start response.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := client.Stream(ctx, "hello")
	assert.ErrorContains(t, err, "thread response missing id")
}

func TestClientStream_ErrorsWhenTurnStartResponseMissesID(t *testing.T) {
	// Given app-server returns an invalid turn/start response.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"id": "thread-new"}})
		respondRPC(h, readRPCReq(t, sc), map[string]any{"turn": map[string]any{"status": "inProgress"}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := client.Stream(ctx, "hello")
	assert.ErrorContains(t, err, "turn/start response missing id")
}

func TestClientForkThread_ErrorsWhenResponseMissesID(t *testing.T) {
	// Given app-server returns an invalid thread/fork response.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"forkedFromId": "source"}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := client.ForkThread(ctx, "source")
	assert.ErrorContains(t, err, "thread/fork response missing id")
}

func TestClientRollbackThread_ErrorsWhenResponseMissesID(t *testing.T) {
	// Given app-server returns an invalid thread/rollback response.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"id": "source"}})
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := client.RollbackThread(ctx, "source", 1)
	assert.ErrorContains(t, err, "thread/rollback response missing id")
}

func TestClientStream_MapsToolLifecycle(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc)
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"id": "thread-tools"}})
		respondRPC(h, readRPCReq(t, sc), map[string]any{"turn": map[string]any{"id": "turn-tools", "status": "inProgress"}})
		h.send(map[string]any{
			"method": "item/started",
			"params": map[string]any{
				"threadId": "thread-tools",
				"turnId":   "turn-tools",
				"item": map[string]any{
					"type":    "commandExecution",
					"id":      "tool-1",
					"command": "pwd",
					"cwd":     "/tmp/work",
					"status":  "inProgress",
				},
			},
		})
		h.send(map[string]any{
			"method": "item/completed",
			"params": map[string]any{
				"threadId": "thread-tools",
				"turnId":   "turn-tools",
				"item": map[string]any{
					"type":             "commandExecution",
					"id":               "tool-1",
					"command":          "pwd",
					"cwd":              "/tmp/work",
					"status":           "completed",
					"aggregatedOutput": "/tmp/work\n",
					"exitCode":         0,
				},
			},
		})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-tools", "turnId": "turn-tools", "turn": map[string]any{"id": "turn-tools", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "tools")
	require.NoError(t, err)

	var events []Event
	for stream.Next() {
		events = append(events, stream.Event())
	}
	require.NoError(t, stream.Close(ctx))
	require.Len(t, events, 3)
	assert.Equal(t, EventPreToolUse, events[0].Kind)
	assert.Equal(t, "tool-1", events[0].Tool.ID)
	assert.Equal(t, "command_execution", events[0].Tool.Name)
	assert.JSONEq(t, `{"command":"pwd","cwd":"/tmp/work"}`, string(events[0].Tool.Input))
	assert.Equal(t, EventPostToolUse, events[1].Kind)
	assert.JSONEq(t, `{"output":"/tmp/work\n","exitCode":0,"status":"completed"}`, string(events[1].Tool.Response))
	assert.Equal(t, EventDone, events[2].Kind)
}

func TestClientStream_CompletesUnknownToolItem(t *testing.T) {
	// Given Codex app-server emits a WebSearch-like tool item whose type is not
	// in the wrapper's built-in tool whitelist.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc)
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"id": "thread-web"}})
		respondRPC(h, readRPCReq(t, sc), map[string]any{"turn": map[string]any{"id": "turn-web", "status": "inProgress"}})
		h.send(map[string]any{
			"method": "item/started",
			"params": map[string]any{
				"threadId": "thread-web",
				"turnId":   "turn-web",
				"item": map[string]any{
					"type":      "webSearch",
					"id":        "search-1",
					"query":     "codex web search",
					"status":    "inProgress",
					"arguments": map[string]any{"query": "codex web search"},
				},
			},
		})
		h.send(map[string]any{
			"method": "item/completed",
			"params": map[string]any{
				"threadId": "thread-web",
				"turnId":   "turn-web",
				"item": map[string]any{
					"type":      "webSearch",
					"id":        "search-1",
					"query":     "codex web search",
					"status":    "completed",
					"arguments": map[string]any{"query": "codex web search"},
					"result": map[string]any{
						"items": []map[string]any{{
							"title": "Codex docs",
							"url":   "https://example.test/codex",
						}},
					},
				},
			},
		})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thread-web", "turnId": "turn-web", "turn": map[string]any{"id": "turn-web", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "search")
	require.NoError(t, err)

	var events []Event
	for stream.Next() {
		events = append(events, stream.Event())
	}
	require.NoError(t, stream.Close(ctx))

	require.Len(t, events, 3)
	assert.Equal(t, EventPreToolUse, events[0].Kind)
	assert.Equal(t, "search-1", events[0].Tool.ID)
	assert.Equal(t, "webSearch", events[0].Tool.Name)
	assert.JSONEq(t, `{"query":"codex web search"}`, string(events[0].Tool.Input))
	assert.Equal(t, EventPostToolUse, events[1].Kind)
	assert.Equal(t, "search-1", events[1].Tool.ID)
	assert.JSONEq(t, `{"items":[{"title":"Codex docs","url":"https://example.test/codex"}]}`, string(events[1].Tool.Response))
	assert.Equal(t, EventDone, events[2].Kind)
}

type fakeAppServerRunner struct {
	t       *testing.T
	handler func(*testing.T, *fakeAppServerHandle)

	mu   sync.Mutex
	opts []procOptions
}

func TestClientStream_DoesNotBlockTurnStartResponseBehindNotificationBurst(t *testing.T) {
	// Given app-server emits more notifications than the legacy incoming buffer
	// before its turn/start response, when a turn starts, then the response must
	// still be routed and the queued notifications must remain consumable.
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondAppServerInit(t, h, sc)
		respondThreadStart(t, h, sc, `{"approvalPolicy":"never"}`, "thread-burst")

		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		for i := 0; i < 129; i++ {
			h.send(map[string]any{
				"method": "thread/tokenUsage/updated",
				"params": map[string]any{"threadId": "thread-burst"},
			})
		}
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-burst", "status": "inProgress"}})
		h.send(map[string]any{
			"method": "turn/completed",
			"params": map[string]any{
				"threadId": "thread-burst",
				"turnId":   "turn-burst",
				"turn":     map[string]any{"id": "turn-burst", "status": "completed"},
			},
		})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	stream, err := client.Stream(ctx, "burst")
	require.NoError(t, err)
	done := false
	for stream.Next() {
		if stream.Event().Kind == EventDone {
			done = true
		}
	}
	require.NoError(t, stream.Close(ctx))
	assert.True(t, done, "stream ended without EventDone")
}

func (r *fakeAppServerRunner) Start(ctx context.Context, opts procOptions) (processHandle, error) {
	_ = ctx
	r.mu.Lock()
	r.opts = append(r.opts, opts)
	r.mu.Unlock()
	h := newFakeAppServerHandle()
	go r.handler(r.t, h)
	return h, nil
}

type fakeAppServerHandle struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stderrR *strings.Reader

	done     chan struct{}
	doneOnce sync.Once
}

func newFakeAppServerHandle() *fakeAppServerHandle {
	stdinR, stdinW := io.Pipe()
	stdoutR, stdoutW := io.Pipe()
	return &fakeAppServerHandle{
		stdinR:  stdinR,
		stdinW:  stdinW,
		stdoutR: stdoutR,
		stdoutW: stdoutW,
		stderrR: strings.NewReader(""),
		done:    make(chan struct{}),
	}
}

func (h *fakeAppServerHandle) Stdin() io.Writer  { return h.stdinW }
func (h *fakeAppServerHandle) Stdout() io.Reader { return h.stdoutR }
func (h *fakeAppServerHandle) Stderr() io.Reader { return h.stderrR }
func (h *fakeAppServerHandle) Wait() error {
	<-h.done
	return nil
}
func (h *fakeAppServerHandle) Kill() error {
	h.doneOnce.Do(func() {
		_ = h.stdinW.Close()
		_ = h.stdoutW.Close()
		close(h.done)
	})
	return nil
}
func (h *fakeAppServerHandle) Signal(_ os.Signal) error { return h.Kill() }

func (h *fakeAppServerHandle) send(v any) {
	data, _ := json.Marshal(v)
	_, _ = h.stdoutW.Write(append(data, '\n'))
}

type rpcReq struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func readRPCReq(t *testing.T, sc *bufio.Scanner) rpcReq {
	t.Helper()
	if !sc.Scan() {
		t.Fatalf("server stdin closed: %v", sc.Err())
	}
	var req rpcReq
	require.NoError(t, json.Unmarshal(sc.Bytes(), &req))
	return req
}

func respondAppServerInit(t *testing.T, h *fakeAppServerHandle, sc *bufio.Scanner) {
	t.Helper()
	respondRPC(h, readRPCReq(t, sc), map[string]any{})
	_ = readRPCReq(t, sc) // initialized notification
}

func respondThreadStart(t *testing.T, h *fakeAppServerHandle, sc *bufio.Scanner, wantParams, threadID string) {
	t.Helper()
	req := readRPCReq(t, sc)
	assert.Equal(t, "thread/start", req.Method)
	assert.JSONEq(t, wantParams, string(req.Params))
	respondRPC(h, req, map[string]any{"thread": map[string]any{"id": threadID, "cwd": "/tmp/work"}})
}

func respondThreadResume(t *testing.T, h *fakeAppServerHandle, sc *bufio.Scanner, wantParams, threadID string) {
	t.Helper()
	req := readRPCReq(t, sc)
	assert.Equal(t, "thread/resume", req.Method)
	assert.JSONEq(t, wantParams, string(req.Params))
	respondRPC(h, req, map[string]any{"thread": map[string]any{"id": threadID, "cwd": "/tmp/work"}})
}

func goalWire(threadID, objective string, tokensUsed, timeUsedSeconds int) map[string]any {
	return map[string]any{
		"threadId":        threadID,
		"objective":       objective,
		"status":          "active",
		"tokensUsed":      tokensUsed,
		"timeUsedSeconds": timeUsedSeconds,
		"createdAt":       11,
		"updatedAt":       12,
	}
}

func respondRPC(h *fakeAppServerHandle, req rpcReq, result any) {
	h.send(map[string]any{"id": json.RawMessage(req.ID), "result": result})
}

func respondRPCError(h *fakeAppServerHandle, req rpcReq, code int64, message string) {
	h.send(map[string]any{
		"id": json.RawMessage(req.ID),
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	})
}

func TestStream_SteerNoActiveTurn(t *testing.T) {
	s := &Stream{}
	err := s.Steer(context.Background(), "hi")
	if !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("want ErrNoActiveTurn, got %v", err)
	}
}

func TestStream_UserMessageItemEmitsEventOnce(t *testing.T) {
	s := &Stream{events: make(chan Event, 2)}
	seen := map[string]struct{}{}
	raw := json.RawMessage(`{"threadId":"thr-1","item":{"type":"userMessage","id":"user-1","text":"follow-up"}}`)

	done := s.handleInbound(context.Background(), appInbound{
		Kind:   appInboundNotification,
		Method: appMethodItemStarted,
		Params: raw,
	}, seen)
	require.False(t, done)
	done = s.handleInbound(context.Background(), appInbound{
		Kind:   appInboundNotification,
		Method: appMethodItemCompleted,
		Params: raw,
	}, seen)
	require.False(t, done)

	select {
	case ev := <-s.events:
		assert.Equal(t, EventUserMessage, ev.Kind)
		assert.Equal(t, "follow-up", ev.Text)
		assert.Equal(t, "thr-1", ev.SessionID)
	default:
		t.Fatal("missing user message event")
	}
	select {
	case ev := <-s.events:
		t.Fatalf("duplicate user message event: %+v", ev)
	default:
	}
}

func TestStream_UserMessageContentItemEmitsTextEvent(t *testing.T) {
	s := &Stream{events: make(chan Event, 1)}
	seen := map[string]struct{}{}
	raw := json.RawMessage(`{"threadId":"thr-1","item":{"type":"userMessage","id":"user-1","content":[{"type":"text","text":"follow-up","text_elements":[]}]}}`)

	done := s.handleInbound(context.Background(), appInbound{
		Kind:   appInboundNotification,
		Method: appMethodItemStarted,
		Params: raw,
	}, seen)
	require.False(t, done)

	select {
	case ev := <-s.events:
		assert.Equal(t, EventUserMessage, ev.Kind)
		assert.Equal(t, "follow-up", ev.Text)
		assert.Equal(t, "thr-1", ev.SessionID)
	default:
		t.Fatal("missing user message event")
	}
}

func TestStream_AgentMessageStringContentEmitsTextEvent(t *testing.T) {
	// Given Codex app-server completes an agentMessage with content encoded as a
	// string (observed with DeepSeek Responses through the AgentRE gateway),
	// when the notification is handled, then the string is normalized to text.
	s := &Stream{events: make(chan Event, 1)}
	seen := map[string]struct{}{}
	raw := json.RawMessage(`{"threadId":"thr-1","item":{"type":"agentMessage","id":"agent-1","content":"deepseek reply"}}`)

	done := s.handleInbound(context.Background(), appInbound{
		Kind:   appInboundNotification,
		Method: appMethodItemCompleted,
		Params: raw,
	}, seen)
	require.False(t, done)

	select {
	case ev := <-s.events:
		assert.Equal(t, EventTextDelta, ev.Kind)
		assert.Equal(t, "deepseek reply", ev.Text)
		assert.Equal(t, "thr-1", ev.SessionID)
	default:
		t.Fatal("missing agent message text event")
	}
}

func TestStream_AgentMessageStringArrayContentEmitsTextEvent(t *testing.T) {
	// Given the same compatible provider emits agentMessage content as an
	// array of strings, then each element is normalized without rejecting the
	// entire app-server notification.
	s := &Stream{events: make(chan Event, 1)}
	seen := map[string]struct{}{}
	raw := json.RawMessage(`{"threadId":"thr-1","item":{"type":"agentMessage","id":"agent-1","content":["deepseek ","reply"]}}`)

	done := s.handleInbound(context.Background(), appInbound{
		Kind:   appInboundNotification,
		Method: appMethodItemCompleted,
		Params: raw,
	}, seen)
	require.False(t, done)

	select {
	case ev := <-s.events:
		assert.Equal(t, EventTextDelta, ev.Kind)
		assert.Equal(t, "deepseek reply", ev.Text)
		assert.Equal(t, "thr-1", ev.SessionID)
	default:
		t.Fatal("missing agent message text event")
	}
}

func TestStream_CompactBoundaryDedupesContextCompactionNotifications(t *testing.T) {
	s := &Stream{
		events:         make(chan Event, 4),
		sessionID:      "thr-1",
		compactSeen:    map[string]struct{}{},
		compactTrigger: "manual",
	}
	seen := map[string]struct{}{}

	rawItem := json.RawMessage(`{"threadId":"thr-1","turnId":"turn-1","item":{"type":"contextCompaction","id":"compact-item"}}`)
	done := s.handleInbound(context.Background(), appInbound{
		Kind:   appInboundNotification,
		Method: appMethodRawResponseItemCompleted,
		Params: rawItem,
	}, seen)
	require.False(t, done)

	rawThread := json.RawMessage(`{"threadId":"thr-1","turnId":"turn-1"}`)
	done = s.handleInbound(context.Background(), appInbound{
		Kind:   appInboundNotification,
		Method: appMethodThreadCompacted,
		Params: rawThread,
	}, seen)
	require.True(t, done)

	select {
	case ev := <-s.events:
		assert.Equal(t, EventCompactBoundary, ev.Kind)
		assert.Equal(t, "thr-1", ev.SessionID)
		require.NotNil(t, ev.Compact)
		assert.Equal(t, "manual", ev.Compact.Trigger)
	default:
		t.Fatal("missing compact boundary event")
	}
	select {
	case ev := <-s.events:
		assert.Equal(t, EventDone, ev.Kind)
	default:
		t.Fatal("missing done event")
	}
	select {
	case ev := <-s.events:
		t.Fatalf("unexpected extra event: %+v", ev)
	default:
	}
}

func TestStream_RequestUserInputRoundTrip(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	responseCaptured := make(chan json.RawMessage, 1)
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"id": "thr-ask"}})
		respondRPC(h, readRPCReq(t, sc), map[string]any{"turn": map[string]any{"id": "turn-ask", "status": "inProgress"}})

		h.send(map[string]any{
			"id":     "ask-1",
			"method": "item/tool/requestUserInput",
			"params": map[string]any{
				"threadId": "thr-ask",
				"turnId":   "turn-ask",
				"itemId":   "item-ask",
				"questions": []map[string]any{{
					"id":       "schema_choice",
					"header":   "Schema",
					"question": "Which column?",
					"options": []map[string]any{{
						"label":       "last_read_at",
						"description": "Timestamp.",
					}},
				}},
			},
		})

		if !sc.Scan() {
			t.Fatalf("server stdin closed before request_user_input response: %v", sc.Err())
		}
		responseCaptured <- append(json.RawMessage(nil), sc.Bytes()...)
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thr-ask", "turnId": "turn-ask", "turn": map[string]any{"id": "turn-ask", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "ask")
	require.NoError(t, err)

	require.True(t, stream.Next())
	ev := stream.Event()
	require.Equal(t, EventRequestUserInput, ev.Kind)
	require.NotNil(t, ev.RequestUserInput)
	assert.Equal(t, "ask-1", ev.RequestUserInput.RequestID)
	assert.Equal(t, "item-ask", ev.RequestUserInput.ItemID)
	require.Len(t, ev.RequestUserInput.Questions, 1)
	assert.Equal(t, "schema_choice", ev.RequestUserInput.Questions[0].ID)
	assert.Equal(t, "Which column?", ev.RequestUserInput.Questions[0].Question)
	require.Len(t, ev.RequestUserInput.Questions[0].Options, 1)
	assert.Equal(t, "last_read_at", ev.RequestUserInput.Questions[0].Options[0].Label)

	require.NoError(t, stream.SubmitUserInput(ctx, ev.RequestUserInput.RequestID, map[string][]string{
		"schema_choice": {"last_read_at"},
	}))

	select {
	case raw := <-responseCaptured:
		assert.JSONEq(t, `{
			"id":"ask-1",
			"result":{"answers":{"schema_choice":{"answers":["last_read_at"]}}}
		}`, string(raw))
	case <-time.After(2 * time.Second):
		t.Fatal("request_user_input response was not written")
	}

	for stream.Next() {
	}
	require.NoError(t, stream.Close(ctx))
}

func TestStream_ApprovalRequestRoundTrip(t *testing.T) {
	tests := []struct {
		name         string
		method       string
		params       map[string]any
		allow        bool
		alwaysAllow  bool
		wantToolName string
		wantResponse string
	}{
		{
			name:   "Given command approval request, when user allows once, then accept is returned",
			method: "item/commandExecution/requestApproval",
			params: map[string]any{
				"threadId":    "thr-approval",
				"turnId":      "turn-approval",
				"itemId":      "item-command",
				"startedAtMs": float64(1700000000000),
				"command":     "rm -rf build",
				"cwd":         "/tmp/work",
				"reason":      "needs cleanup",
			},
			allow:        true,
			wantToolName: "Bash",
			wantResponse: `{"id":"approval-1","result":{"decision":"accept"}}`,
		},
		{
			name:   "Given command approval request, when user denies, then decline is returned",
			method: "item/commandExecution/requestApproval",
			params: map[string]any{
				"threadId": "thr-approval",
				"turnId":   "turn-approval",
				"itemId":   "item-command",
				"command":  "curl https://example.com",
			},
			allow:        false,
			wantToolName: "Bash",
			wantResponse: `{"id":"approval-1","result":{"decision":"decline"}}`,
		},
		{
			name:   "Given file approval request, when user allows for session, then acceptForSession is returned",
			method: "item/fileChange/requestApproval",
			params: map[string]any{
				"threadId":    "thr-approval",
				"turnId":      "turn-approval",
				"itemId":      "item-file",
				"startedAtMs": float64(1700000000000),
				"reason":      "needs write access",
				"grantRoot":   "/tmp/work",
			},
			allow:        true,
			alwaysAllow:  true,
			wantToolName: "FileChange",
			wantResponse: `{"id":"approval-1","result":{"decision":"acceptForSession"}}`,
		},
		{
			name:   "Given permissions approval request, when user allows for session, then requested permissions are granted for session",
			method: "item/permissions/requestApproval",
			params: map[string]any{
				"threadId":    "thr-approval",
				"turnId":      "turn-approval",
				"itemId":      "item-permissions",
				"startedAtMs": float64(1700000000000),
				"cwd":         "/tmp/work",
				"reason":      "needs network and filesystem",
				"permissions": map[string]any{
					"network": map[string]any{"domains": []any{"example.com"}},
					"fileSystem": map[string]any{
						"writableRoots": []any{"/tmp/work"},
					},
				},
			},
			allow:        true,
			alwaysAllow:  true,
			wantToolName: "Permissions",
			wantResponse: `{"id":"approval-1","result":{"permissions":{"network":{"domains":["example.com"]},"fileSystem":{"writableRoots":["/tmp/work"]}},"scope":"session"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &fakeAppServerRunner{t: t}
			responseCaptured := make(chan json.RawMessage, 1)
			runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
				sc := bufio.NewScanner(h.stdinR)
				respondRPC(h, readRPCReq(t, sc), map[string]any{})
				_ = readRPCReq(t, sc) // initialized
				respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"id": "thr-approval"}})
				respondRPC(h, readRPCReq(t, sc), map[string]any{"turn": map[string]any{"id": "turn-approval", "status": "inProgress"}})

				h.send(map[string]any{
					"id":     "approval-1",
					"method": tt.method,
					"params": tt.params,
				})

				if !sc.Scan() {
					t.Fatalf("server stdin closed before approval response: %v", sc.Err())
				}
				responseCaptured <- append(json.RawMessage(nil), sc.Bytes()...)
				h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thr-approval", "turnId": "turn-approval", "turn": map[string]any{"id": "turn-approval", "status": "completed"}}})
			}

			client := New(WithAppServerRunnerForTesting(runner))
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			stream, err := client.Stream(ctx, "approval")
			require.NoError(t, err)

			require.True(t, stream.Next())
			ev := stream.Event()
			require.Equal(t, EventApprovalRequest, ev.Kind)
			require.NotNil(t, ev.Approval)
			assert.Equal(t, "approval-1", ev.Approval.RequestID)
			assert.Equal(t, tt.wantToolName, ev.Approval.ToolName)

			require.NoError(t, stream.SubmitApproval(ctx, ev.Approval.RequestID, tt.allow, tt.alwaysAllow))

			select {
			case raw := <-responseCaptured:
				assert.JSONEq(t, tt.wantResponse, string(raw))
			case <-time.After(2 * time.Second):
				t.Fatal("approval response was not written")
			}

			for stream.Next() {
			}
			require.NoError(t, stream.Close(ctx))
		})
	}
}

func TestStream_SubmitApprovalUnknownRequestReturnsNoActiveTurn(t *testing.T) {
	// Given a stream with no matching approval waiter.
	stream := newStream(nil, 0, "thread", "turn", "")

	// When the user tries to answer an unknown approval request.
	err := stream.SubmitApproval(context.Background(), "missing", true, false)

	// Then the request is rejected as no active approval turn.
	require.ErrorIs(t, err, ErrNoActiveTurn)
}

func TestStream_SteerSendsTurnSteerRPC(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	steerCaptured := make(chan rpcReq, 1)
	allowSteerResponse := make(chan struct{})
	steerReturned := make(chan struct{})
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized

		startReq := readRPCReq(t, sc)
		assert.Equal(t, "thread/start", startReq.Method)
		respondRPC(h, startReq, map[string]any{"thread": map[string]any{"id": "thr-1"}})

		turnReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/start", turnReq.Method)
		respondRPC(h, turnReq, map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}})

		// Now expect a turn/steer; capture and respond, then complete the turn.
		steerReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/steer", steerReq.Method)
		steerCaptured <- steerReq
		<-allowSteerResponse
		respondRPC(h, steerReq, map[string]any{})
		<-steerReturned
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thr-1", "turnId": "turn-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "hello")
	require.NoError(t, err)

	steerErr := make(chan error, 1)
	go func() {
		steerErr <- stream.Steer(ctx, "wait, change plan")
	}()

	select {
	case steerReq := <-steerCaptured:
		var got map[string]any
		require.NoError(t, json.Unmarshal(steerReq.Params, &got))
		assert.Equal(t, "thr-1", got["threadId"])
		assert.Equal(t, "turn-1", got["expectedTurnId"])
		input, ok := got["input"].([]any)
		require.True(t, ok, "input not an array: %v", got["input"])
		require.Len(t, input, 1)
		first := input[0].(map[string]any)
		assert.Equal(t, "text", first["type"])
		assert.Equal(t, "wait, change plan", first["text"])
	case <-time.After(2 * time.Second):
		t.Fatalf("turn/steer never captured")
	}
	close(allowSteerResponse)

	select {
	case err := <-steerErr:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatalf("Steer did not return")
	}
	close(steerReturned)

	for stream.Next() {
	}
	require.NoError(t, stream.Close(ctx))
}

func TestStream_SteerAfterTurnCompletedReturnsNoActiveTurn(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"id": "thr-1"}})
		respondRPC(h, readRPCReq(t, sc), map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}})
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thr-1", "turnId": "turn-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "hello")
	require.NoError(t, err)
	for stream.Next() {
	}
	require.NoError(t, stream.Close(ctx))

	err = stream.Steer(ctx, "too late")
	require.ErrorIs(t, err, ErrNoActiveTurn)
}

func TestStream_SteerExpectedTurnMismatchReturnsNoActiveTurn(t *testing.T) {
	runner := &fakeAppServerRunner{t: t}
	runner.handler = func(t *testing.T, h *fakeAppServerHandle) {
		sc := bufio.NewScanner(h.stdinR)
		respondRPC(h, readRPCReq(t, sc), map[string]any{})
		_ = readRPCReq(t, sc) // initialized
		respondRPC(h, readRPCReq(t, sc), map[string]any{"thread": map[string]any{"id": "thr-1"}})
		respondRPC(h, readRPCReq(t, sc), map[string]any{"turn": map[string]any{"id": "turn-1", "status": "inProgress"}})

		steerReq := readRPCReq(t, sc)
		assert.Equal(t, "turn/steer", steerReq.Method)
		respondRPCError(h, steerReq, -32602, "expectedTurnId does not match the currently active turn")
		h.send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "thr-1", "turnId": "turn-1", "turn": map[string]any{"id": "turn-1", "status": "completed"}}})
	}

	client := New(WithAppServerRunnerForTesting(runner))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.Stream(ctx, "hello")
	require.NoError(t, err)

	err = stream.Steer(ctx, "too late")
	require.ErrorIs(t, err, ErrNoActiveTurn)
	for stream.Next() {
	}
	require.NoError(t, stream.Close(ctx))
}

func assertNoStreamEvent(t *testing.T, stream *Stream) {
	t.Helper()
	select {
	case ev := <-stream.events:
		t.Fatalf("expected no stream event, got %#v", ev)
	default:
	}
}
