package openclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// questionRecord builds a question.requested payload / question.list entry in
// the OpenClaw 2026.9.5 QuestionRecord shape.
func questionRecord(id, sessionKey string, questions ...map[string]any) map[string]any {
	return map[string]any{
		"id": id, "questions": questions, "agentId": "main", "sessionKey": sessionKey,
		"runId": "run-x", "createdAtMs": int64(100), "expiresAtMs": time.Now().Add(time.Minute).UnixMilli(),
		"status": "pending",
	}
}

func questionLifecycleEnd(runID, sessionKey string, seq int) map[string]any {
	return map[string]any{
		"type": "event", "event": "agent", "seq": seq,
		"payload": map[string]any{"runId": runID, "sessionKey": sessionKey, "seq": 1, "stream": "lifecycle", "data": map[string]any{"phase": "end"}},
	}
}

func startQuestionTurn(t *testing.T, conn *websocket.Conn, connection int) (runID, sessionKey string) {
	t.Helper()
	runtimeHandshake(t, conn, connection)
	request := runtimeReadTurnRequest(t, conn)
	var params runtimeAgentParams
	require.NoError(t, json.Unmarshal(request.Params, &params))
	runtimeWrite(t, conn, map[string]any{"type": "res", "id": request.ID, "ok": true, "payload": map[string]any{"runId": params.IdempotencyKey, "status": "accepted"}})
	return params.IdempotencyKey, params.SessionKey
}

func nextRuntimeEvent(t *testing.T, events <-chan agentruntime.Event) agentruntime.Event {
	t.Helper()
	select {
	case event, ok := <-events:
		require.True(t, ok, "runtime event stream closed early")
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for an OpenClaw runtime event")
		return nil
	}
}

// Given a turn whose Gateway raises question.requested for this session (and
// one for another session), when the user answers every question — one of
// them secret — then the card mirrors the questions (Other gated by isOther,
// option descriptions, secret flag), question.resolve carries one string array
// per questionId with the secret answer verbatim, and that answer never reaches
// a runtime event or a log line.
func TestRuntimeQuestionBecomesCardAndAnswerSendsPerQuestionArrays(t *testing.T) {
	const hiddenAnswer = "sk-live-TOPSECRET-42"
	core, logs := observer.New(zapcore.DebugLevel)
	previous := logger.Default()
	logger.SetLogger(zap.New(core))
	t.Cleanup(func() { logger.SetLogger(previous) })

	questionSent := make(chan struct{})
	allowFinish := make(chan struct{})
	resolveParams := make(chan map[string]any, 1)
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runID, sessionKey := startQuestionTurn(t, conn, connection)
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "question.requested", "seq": 2,
			"payload": questionRecord("question-other-session", "agent:main:agentre:12:999",
				map[string]any{"questionId": "x", "header": "X", "question": "Not ours?", "options": []any{}}),
		})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "question.requested", "seq": 3,
			"payload": questionRecord("question-1", sessionKey,
				map[string]any{
					"questionId": "target", "header": "Target", "question": "Where to deploy?",
					"options": []any{
						map[string]any{"label": "Staging", "description": "Safe sandbox"},
						map[string]any{"label": "Production"},
					},
				},
				map[string]any{"questionId": "token", "header": "Token", "question": "Paste the API token", "options": []any{}, "isSecret": true},
				map[string]any{
					"questionId": "extras", "header": "Extras", "question": "Anything else?", "multiSelect": true, "isOther": true,
					"options": []any{map[string]any{"label": "Notify"}, map[string]any{"label": "Tag"}},
				},
			),
		})
		close(questionSent)
		resolve := runtimeReadRequest(t, conn)
		require.Equal(t, "question.resolve", resolve.Method)
		var params map[string]any
		require.NoError(t, json.Unmarshal(resolve.Params, &params))
		resolveParams <- params
		answers := map[string]any{"answers": map[string]any{"target": []string{"Staging"}, "token": []string{hiddenAnswer}, "extras": []string{"Notify", "ship it"}}}
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": resolve.ID, "ok": true, "payload": map[string]any{"status": "answered", "answers": answers}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "question.resolved", "seq": 4,
			"payload": map[string]any{"id": "question-1", "status": "answered", "answers": answers},
		})
		<-allowFinish
		runtimeWrite(t, conn, questionLifecycleEnd(runID, sessionKey, 5))
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{
		Backend: runtimeBackend(), SessionID: 81, UserText: "deploy it",
	})
	require.NoError(t, err)
	<-questionSent
	request, ok := nextRuntimeEvent(t, events).(agentruntime.UserAskRequest)
	require.True(t, ok, "question.requested must become a question card")
	assert.Equal(t, "question-1", request.RequestID)
	require.Len(t, request.Questions, 3)
	target := request.Questions[0]
	assert.Equal(t, "target", target.ID)
	assert.Equal(t, "Target", target.Header)
	assert.Equal(t, "Where to deploy?", target.Question)
	assert.True(t, target.DisallowOther, "isOther absent → no free-form Other")
	assert.False(t, target.IsSecret)
	assert.Equal(t, []agentruntime.AskOption{{Label: "Staging", Description: "Safe sandbox"}, {Label: "Production"}}, target.Options)
	token := request.Questions[1]
	assert.Equal(t, "token", token.ID)
	assert.True(t, token.IsSecret)
	assert.Empty(t, token.Options)
	extras := request.Questions[2]
	assert.True(t, extras.MultiSelect)
	assert.True(t, extras.IsOther)
	assert.False(t, extras.DisallowOther, "isOther:true → Other input offered")

	require.NoError(t, runtime.SubmitAnswer(context.Background(), 81, "question-1", nil, []agentruntime.AskAnswer{
		{QuestionIndex: 0, Labels: []string{"Staging"}},
		{QuestionIndex: 1, Labels: []string{agentruntime.OtherAnswerLabel}, OtherText: hiddenAnswer},
		{QuestionIndex: 2, Labels: []string{"Notify", agentruntime.OtherAnswerLabel}, OtherText: "ship it"},
	}, false))
	assert.Equal(t, map[string]any{
		"id": "question-1",
		"answers": map[string]any{"answers": map[string]any{
			"target": []any{"Staging"}, "token": []any{hiddenAnswer}, "extras": []any{"Notify", "ship it"},
		}},
	}, <-resolveParams)

	resolved, ok := nextRuntimeEvent(t, events).(agentruntime.UserAskResolved)
	require.True(t, ok)
	assert.Equal(t, "question-1", resolved.RequestID)
	assert.False(t, resolved.Skipped)
	assert.Equal(t, []agentruntime.AskAnswer{
		{QuestionIndex: 0, Labels: []string{"Staging"}},
		{QuestionIndex: 1},
		{QuestionIndex: 2, Labels: []string{"Notify", agentruntime.OtherAnswerLabel}, OtherText: "ship it"},
	}, resolved.Answers, "the secret answer is recorded as answered without content")

	close(allowFinish)
	rest := collectRuntimeEvents(t, events)
	require.Len(t, rest, 1, "the Gateway's own question.resolved echo must not emit a second terminal")
	_, ok = rest[0].(agentruntime.Done)
	assert.True(t, ok)

	for _, event := range append([]agentruntime.Event{request, resolved}, rest...) {
		assert.NotContains(t, fmt.Sprintf("%+v", event), hiddenAnswer)
	}
	for _, entry := range logs.All() {
		assert.NotContains(t, entry.Message, hiddenAnswer)
		for key, value := range entry.ContextMap() {
			assert.NotContains(t, fmt.Sprint(value), hiddenAnswer, "log field %q leaked the secret answer", key)
		}
	}
}

// Given a pending question, when the user skips it, then question.resolve is
// sent as a cancel and the card resolves as skipped.
func TestRuntimeQuestionSkipSendsCancel(t *testing.T) {
	questionSent := make(chan struct{})
	allowFinish := make(chan struct{})
	resolveParams := make(chan map[string]any, 1)
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runID, sessionKey := startQuestionTurn(t, conn, connection)
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "question.requested", "seq": 2,
			"payload": questionRecord("question-skip", sessionKey,
				map[string]any{"questionId": "color", "header": "Color", "question": "Pick one", "options": []any{map[string]any{"label": "Red"}}}),
		})
		close(questionSent)
		resolve := runtimeReadRequest(t, conn)
		require.Equal(t, "question.resolve", resolve.Method)
		var params map[string]any
		require.NoError(t, json.Unmarshal(resolve.Params, &params))
		resolveParams <- params
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": resolve.ID, "ok": true, "payload": map[string]any{"status": gatewayCancelledStatus}})
		<-allowFinish
		runtimeWrite(t, conn, questionLifecycleEnd(runID, sessionKey, 3))
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{Backend: runtimeBackend(), SessionID: 82, UserText: "ask me"})
	require.NoError(t, err)
	<-questionSent
	_, ok := nextRuntimeEvent(t, events).(agentruntime.UserAskRequest)
	require.True(t, ok)

	require.NoError(t, runtime.SubmitAnswer(context.Background(), 82, "question-skip", nil, nil, true))
	assert.Equal(t, map[string]any{"id": "question-skip", "cancel": true}, <-resolveParams)
	resolved, ok := nextRuntimeEvent(t, events).(agentruntime.UserAskResolved)
	require.True(t, ok)
	assert.Equal(t, agentruntime.UserAskResolved{RequestID: "question-skip", Skipped: true}, resolved)
	assert.ErrorIs(t, runtime.SubmitAnswer(context.Background(), 82, "question-skip", nil, nil, true), agentruntime.ErrWaiterNotFound,
		"a resolved card is no longer answerable")

	close(allowFinish)
	rest := collectRuntimeEvents(t, events)
	require.Len(t, rest, 1)
	_, ok = rest[0].(agentruntime.Done)
	assert.True(t, ok)
}

// Given pending questions, when the Gateway withdraws one, lets one
// expire, and reports one answered elsewhere, then the first two show skipped
// and are no longer answerable, the third shows answered, and a question still
// pending at turn end resolves as skipped.
func TestRuntimeQuestionWithdrawnExpiredAnsweredElsewhereAndTurnEnd(t *testing.T) {
	questionsSent := make(chan struct{})
	allowFinish := make(chan struct{})
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runID, sessionKey := startQuestionTurn(t, conn, connection)
		for i, id := range []string{"q-withdrawn", "q-expired", "q-elsewhere", "q-pending"} {
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "question.requested", "seq": 2 + i,
				"payload": questionRecord(id, sessionKey,
					map[string]any{"questionId": "pick", "header": "Pick", "question": "Pick one", "options": []any{map[string]any{"label": "A"}, map[string]any{"label": "B"}}}),
			})
		}
		runtimeWrite(t, conn, map[string]any{"type": "event", "event": "question.resolved", "seq": 6, "payload": map[string]any{"id": "q-withdrawn", "status": gatewayCancelledStatus}})
		runtimeWrite(t, conn, map[string]any{"type": "event", "event": "question.resolved", "seq": 7, "payload": map[string]any{"id": "q-expired", "status": "expired"}})
		runtimeWrite(t, conn, map[string]any{
			"type": "event", "event": "question.resolved", "seq": 8,
			"payload": map[string]any{"id": "q-elsewhere", "status": "answered", "answers": map[string]any{"answers": map[string]any{"pick": []string{"B"}}}},
		})
		close(questionsSent)
		<-allowFinish
		runtimeWrite(t, conn, questionLifecycleEnd(runID, sessionKey, 9))
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{Backend: runtimeBackend(), SessionID: 83, UserText: "ask me"})
	require.NoError(t, err)
	<-questionsSent
	for range 4 {
		_, ok := nextRuntimeEvent(t, events).(agentruntime.UserAskRequest)
		require.True(t, ok)
	}
	terminals := make([]agentruntime.UserAskResolved, 0, 3)
	for range 3 {
		resolved, ok := nextRuntimeEvent(t, events).(agentruntime.UserAskResolved)
		require.True(t, ok)
		terminals = append(terminals, resolved)
	}
	assert.Equal(t, []agentruntime.UserAskResolved{
		{RequestID: "q-withdrawn", Skipped: true},
		{RequestID: "q-expired", Skipped: true},
		{RequestID: "q-elsewhere", Answers: []agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"B"}}}},
	}, terminals)
	assert.ErrorIs(t, runtime.SubmitAnswer(context.Background(), 83, "q-withdrawn", nil,
		[]agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"A"}}}, false), agentruntime.ErrWaiterNotFound)

	close(allowFinish)
	rest := collectRuntimeEvents(t, events)
	require.Len(t, rest, 2)
	assert.Equal(t, agentruntime.UserAskResolved{RequestID: "q-pending", Skipped: true}, rest[0])
	_, ok := rest[1].(agentruntime.Done)
	assert.True(t, ok)
}

// Given the user answers a question the Gateway already closed, when
// question.resolve fails with QUESTION_ALREADY_TERMINAL, then the runtime asks
// question.get for the real outcome and converges the card on it instead of
// surfacing an error.
func TestRuntimeQuestionAnswerRacingGatewayTerminalConverges(t *testing.T) {
	questionSent := make(chan struct{})
	allowFinish := make(chan struct{})
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		runID, sessionKey := startQuestionTurn(t, conn, connection)
		record := questionRecord("question-race", sessionKey,
			map[string]any{"questionId": "pick", "header": "Pick", "question": "Pick one", "options": []any{map[string]any{"label": "A"}}})
		runtimeWrite(t, conn, map[string]any{"type": "event", "event": "question.requested", "seq": 2, "payload": record})
		close(questionSent)
		resolve := runtimeReadRequest(t, conn)
		require.Equal(t, "question.resolve", resolve.Method)
		runtimeWrite(t, conn, map[string]any{
			"type": "res", "id": resolve.ID, "ok": false,
			"error": map[string]any{"code": "INVALID_REQUEST", "message": "question 'question-race' is already expired", "details": map[string]any{"reason": "QUESTION_ALREADY_TERMINAL"}},
		})
		get := runtimeReadRequest(t, conn)
		require.Equal(t, "question.get", get.Method)
		require.JSONEq(t, `{"id":"question-race"}`, string(get.Params))
		record["status"] = "expired"
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": get.ID, "ok": true, "payload": map[string]any{"question": record}})
		<-allowFinish
		runtimeWrite(t, conn, questionLifecycleEnd(runID, sessionKey, 3))
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{Backend: runtimeBackend(), SessionID: 84, UserText: "ask me"})
	require.NoError(t, err)
	<-questionSent
	_, ok := nextRuntimeEvent(t, events).(agentruntime.UserAskRequest)
	require.True(t, ok)
	require.NoError(t, runtime.SubmitAnswer(context.Background(), 84, "question-race", nil,
		[]agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"A"}}}, false))
	assert.Equal(t, agentruntime.UserAskResolved{RequestID: "question-race", Skipped: true}, nextRuntimeEvent(t, events))

	close(allowFinish)
	rest := collectRuntimeEvents(t, events)
	require.Len(t, rest, 1)
	_, ok = rest[0].(agentruntime.Done)
	assert.True(t, ok)
}

// Given a question pending when the connection drops, when the client
// reconnects to a Gateway advertising question.list, then questions raised
// while offline are restored as cards, and a known question the Gateway no
// longer lists converges on its question.get status.
func TestRuntimeReconnectReconcilesQuestions(t *testing.T) {
	var runID, sessionKey string
	gatewayURL := runtimeGateway(t, func(conn *websocket.Conn, connection int) {
		if connection == 1 {
			runID, sessionKey = startQuestionTurn(t, conn, connection)
			runtimeWrite(t, conn, map[string]any{
				"type": "event", "event": "question.requested", "seq": 2,
				"payload": questionRecord("question-lost", sessionKey,
					map[string]any{"questionId": "pick", "header": "Pick", "question": "Pick one", "options": []any{map[string]any{"label": "A"}}}),
			})
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "question reconcile"), time.Now().Add(time.Second))
			return
		}
		runtimeHandshakeWithMethods(t, conn, connection, "question.list", "question.get", "question.resolve")
		execList := runtimeReadAfterSubscribe(t, conn)
		require.Equal(t, "exec.approval.list", execList.Method)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": execList.ID, "ok": true, "payload": []any{}})

		list := runtimeReadRequest(t, conn)
		require.Equal(t, "question.list", list.Method)
		runtimeWrite(t, conn, map[string]any{
			"type": "res", "id": list.ID, "ok": true,
			"payload": map[string]any{"questions": []any{
				questionRecord("question-restored", sessionKey,
					map[string]any{"questionId": "why", "header": "Why", "question": "Why?", "options": []any{}}),
				questionRecord("question-foreign", "agent:main:agentre:12:999",
					map[string]any{"questionId": "why", "header": "Why", "question": "Why?", "options": []any{}}),
			}},
		})
		get := runtimeReadRequest(t, conn)
		require.Equal(t, "question.get", get.Method)
		require.JSONEq(t, `{"id":"question-lost"}`, string(get.Params))
		lost := questionRecord("question-lost", sessionKey,
			map[string]any{"questionId": "pick", "header": "Pick", "question": "Pick one", "options": []any{map[string]any{"label": "A"}}})
		lost["status"] = "answered"
		lost["answers"] = map[string]any{"answers": map[string]any{"pick": []string{"A"}}}
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": get.ID, "ok": true, "payload": map[string]any{"question": lost}})

		wait := runtimeReadRequest(t, conn)
		require.Equal(t, "agent.wait", wait.Method)
		runtimeWrite(t, conn, map[string]any{"type": "res", "id": wait.ID, "ok": true, "payload": map[string]any{"runId": runID, "status": "ok"}})
	})

	runtime := New(runtimeResolver(t, gatewayURL))
	events, _, err := runtime.Run(context.Background(), agentruntime.RunRequest{Backend: runtimeBackend(), SessionID: 85, UserText: "reconnect questions"})
	require.NoError(t, err)
	collected := collectRuntimeEvents(t, events)
	require.Len(t, collected, 5)
	first, ok := collected[0].(agentruntime.UserAskRequest)
	require.True(t, ok)
	assert.Equal(t, "question-lost", first.RequestID)
	restored, ok := collected[1].(agentruntime.UserAskRequest)
	require.True(t, ok)
	assert.Equal(t, "question-restored", restored.RequestID)
	assert.Equal(t, agentruntime.UserAskResolved{
		RequestID: "question-lost", Answers: []agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"A"}}},
	}, collected[2])
	assert.Equal(t, agentruntime.UserAskResolved{RequestID: "question-restored", Skipped: true}, collected[3])
	_, ok = collected[4].(agentruntime.Done)
	assert.True(t, ok)
}
