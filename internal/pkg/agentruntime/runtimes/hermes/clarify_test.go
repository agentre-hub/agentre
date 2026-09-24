package hermes

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

// clarifyServerRequest is the `clarify` server->client request as the codec
// hands it to the turn.
func clarifyServerRequest(id, params string) Event {
	return Event{Kind: EventServerRequest, Session: "live-1", Request: &ServerRequest{ID: id, Method: "clarify", Params: json.RawMessage(params)}}
}

func clarifyRequestCancel(id, reason string) Event {
	return Event{Kind: EventRequestCancel, Session: "live-1", Payload: json.RawMessage(`{"id":"` + id + `","method":"clarify","reason":"` + reason + `"}`)}
}

// nextAskEvent reads the turn stream until the next ask-user lifecycle event,
// skipping unrelated events.
func nextAskEvent(t *testing.T, out <-chan agentruntime.Event) agentruntime.Event {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-out:
			require.True(t, ok, "the turn ended before a clarify event arrived")
			switch ev.(type) {
			case agentruntime.UserAskRequest, agentruntime.UserAskResolved:
				return ev
			}
		case <-timeout:
			t.Fatal("no clarify event arrived")
		}
	}
}

func startClarifyTurn(t *testing.T, sessionID int64) (*fakeSession, *Runtime, <-chan agentruntime.Event) {
	t.Helper()
	fake := newFakeSession()
	rt := NewWithCredentials(func(context.Context, sessionSpec) (Session, error) { return fake, nil }, nil)
	out, _, err := rt.Run(context.Background(), runRequest(sessionID))
	require.NoError(t, err)
	return fake, rt, out
}

func decodeAnswerResult(t *testing.T, result any) map[string]any {
	t.Helper()
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(encoded, &m))
	return m
}

func TestHermesClarify_RequestBecomesQuestionCard(t *testing.T) {
	Convey("Given a turn in flight", t, func() {
		Convey("When Hermes sends a single-select clarify request Then the card offers its choices and disallows Other", func() {
			fake, _, out := startClarifyTurn(t, 400)
			fake.push(clarifyServerRequest("srq-single0001", `{"session_id":"live-1","question":"Continue with the merge?","choices":["yes","no"],"multi_select":false}`))

			ev := nextAskEvent(t, out)

			req, ok := ev.(agentruntime.UserAskRequest)
			require.True(t, ok, "want UserAskRequest, got %T", ev)
			assert.Equal(t, "srq-single0001", req.RequestID)
			require.Len(t, req.Questions, 1)
			q := req.Questions[0]
			assert.Equal(t, "Continue with the merge?", q.Question)
			assert.False(t, q.MultiSelect)
			assert.True(t, q.DisallowOther, "a question with options must not offer Other")
			require.Len(t, q.Options, 2)
			assert.Equal(t, "yes", q.Options[0].Label)
			assert.Equal(t, "no", q.Options[1].Label)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When Hermes sends a clarify request with no choices Then the card allows free text and does not disallow Other", func() {
			fake, _, out := startClarifyTurn(t, 401)
			fake.push(clarifyServerRequest("srq-freetext001", `{"session_id":"live-1","question":"What is your name?","choices":null,"multi_select":false}`))

			req, ok := nextAskEvent(t, out).(agentruntime.UserAskRequest)

			require.True(t, ok)
			require.Len(t, req.Questions, 1)
			assert.False(t, req.Questions[0].DisallowOther, "a question with no options is answered by free text alone")
			assert.Empty(t, req.Questions[0].Options)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When Hermes sends a batch clarify request Then each question keeps its qid as ID", func() {
			fake, _, out := startClarifyTurn(t, 402)
			fake.push(clarifyServerRequest("srq-batch00001", `{"session_id":"live-1","questions":[{"qid":"q1","question":"Pick a color","choices":["red","blue"]},{"qid":"q2","question":"Anything else?","choices":null}]}`))

			req, ok := nextAskEvent(t, out).(agentruntime.UserAskRequest)

			require.True(t, ok)
			require.Len(t, req.Questions, 2)
			assert.Equal(t, "q1", req.Questions[0].ID)
			assert.True(t, req.Questions[0].DisallowOther)
			assert.Equal(t, "q2", req.Questions[1].ID)
			assert.False(t, req.Questions[1].DisallowOther)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When the clarify request cannot be turned into a card Then it is declined and no card appears", func() {
			fake, _, out := startClarifyTurn(t, 403)
			fake.push(clarifyServerRequest("srq-malformed01", `{"session_id":"live-1","question":"","choices":null}`))
			fake.push(Event{Kind: EventMessageComplete, Session: fake.liveSID, Payload: []byte(`{"text":"ok","status":"complete"}`)})
			fake.closeEvents(io.EOF)

			events := collect(t, out)

			for _, ev := range events {
				_, isCard := ev.(agentruntime.UserAskRequest)
				assert.False(t, isCard, "an unparseable clarify request must not become a card")
			}
			assert.Equal(t, []fakeAnswer{{id: "srq-malformed01", rejected: true}}, fake.sentAnswers())
		})
	})
}

func TestHermesClarify_AnswerShapes(t *testing.T) {
	Convey("Given a pending Hermes clarify card", t, func() {
		Convey("When the user picks one option on a single-select question Then Hermes receives the option text", func() {
			sessionID := int64(410)
			fake, rt, out := startClarifyTurn(t, sessionID)
			fake.push(clarifyServerRequest("srq-ans-single1", `{"session_id":"live-1","question":"Continue?","choices":["yes","no"],"multi_select":false}`))
			_ = nextAskEvent(t, out)

			err := rt.SubmitAnswer(context.Background(), sessionID, "srq-ans-single1",
				nil, []agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"no"}}}, false)

			require.NoError(t, err)
			require.Len(t, fake.sentAnswers(), 1)
			answer := fake.sentAnswers()[0]
			assert.Equal(t, "srq-ans-single1", answer.id)
			assert.False(t, answer.rejected)
			assert.Equal(t, map[string]any{"answer": "no"}, decodeAnswerResult(t, answer.result))
			resolved, ok := nextAskEvent(t, out).(agentruntime.UserAskResolved)
			require.True(t, ok)
			assert.Equal(t, "srq-ans-single1", resolved.RequestID)
			assert.False(t, resolved.Skipped)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When the user picks several options on a multi-select question Then Hermes receives the JSON array string", func() {
			sessionID := int64(411)
			fake, rt, out := startClarifyTurn(t, sessionID)
			fake.push(clarifyServerRequest("srq-ans-multi01", `{"session_id":"live-1","question":"Which fruits?","choices":["apple","banana","cherry"],"multi_select":true}`))
			_ = nextAskEvent(t, out)

			err := rt.SubmitAnswer(context.Background(), sessionID, "srq-ans-multi01",
				nil, []agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"apple", "cherry"}}}, false)

			require.NoError(t, err)
			require.Len(t, fake.sentAnswers(), 1)
			result := decodeAnswerResult(t, fake.sentAnswers()[0].result)
			var decoded []string
			require.NoError(t, json.Unmarshal([]byte(result["answer"].(string)), &decoded))
			assert.Equal(t, []string{"apple", "cherry"}, decoded)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When the question has no options Then Hermes receives the typed text", func() {
			sessionID := int64(412)
			fake, rt, out := startClarifyTurn(t, sessionID)
			fake.push(clarifyServerRequest("srq-ans-free001", `{"session_id":"live-1","question":"What is your name?","choices":null,"multi_select":false}`))
			_ = nextAskEvent(t, out)

			err := rt.SubmitAnswer(context.Background(), sessionID, "srq-ans-free001",
				nil, []agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{agentruntime.OtherAnswerLabel}, OtherText: "Ada"}}, false)

			require.NoError(t, err)
			assert.Equal(t, map[string]any{"answer": "Ada"}, decodeAnswerResult(t, fake.sentAnswers()[0].result))
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When a batch is answered Then Hermes receives the answers map keyed by qid", func() {
			sessionID := int64(413)
			fake, rt, out := startClarifyTurn(t, sessionID)
			fake.push(clarifyServerRequest("srq-ans-batch01", `{"session_id":"live-1","questions":[{"qid":"q1","question":"Pick a color","choices":["red","blue"]},{"qid":"q2","question":"Anything else?","choices":null}]}`))
			_ = nextAskEvent(t, out)

			err := rt.SubmitAnswer(context.Background(), sessionID, "srq-ans-batch01", nil, []agentruntime.AskAnswer{
				{QuestionIndex: 0, Labels: []string{"red"}},
				{QuestionIndex: 1, Labels: []string{agentruntime.OtherAnswerLabel}, OtherText: "nothing"},
			}, false)

			require.NoError(t, err)
			result := decodeAnswerResult(t, fake.sentAnswers()[0].result)
			assert.Equal(t, map[string]any{"q1": "red", "q2": "nothing"}, result["answers"])
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When a single question is skipped Then Hermes receives an empty answer string", func() {
			sessionID := int64(414)
			fake, rt, out := startClarifyTurn(t, sessionID)
			fake.push(clarifyServerRequest("srq-skip-single", `{"session_id":"live-1","question":"Continue?","choices":["yes","no"]}`))
			_ = nextAskEvent(t, out)

			err := rt.SubmitAnswer(context.Background(), sessionID, "srq-skip-single", nil, nil, true)

			require.NoError(t, err)
			assert.Equal(t, map[string]any{"answer": ""}, decodeAnswerResult(t, fake.sentAnswers()[0].result))
			resolved, ok := nextAskEvent(t, out).(agentruntime.UserAskResolved)
			require.True(t, ok)
			assert.True(t, resolved.Skipped)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When a batch is skipped Then Hermes receives no answers key, canceling the whole batch", func() {
			sessionID := int64(415)
			fake, rt, out := startClarifyTurn(t, sessionID)
			fake.push(clarifyServerRequest("srq-skip-batch0", `{"session_id":"live-1","questions":[{"qid":"q1","question":"A?","choices":["x","y"]},{"qid":"q2","question":"B?","choices":null}]}`))
			_ = nextAskEvent(t, out)

			err := rt.SubmitAnswer(context.Background(), sessionID, "srq-skip-batch0", nil, nil, true)

			require.NoError(t, err)
			result := decodeAnswerResult(t, fake.sentAnswers()[0].result)
			_, hasAnswers := result["answers"]
			assert.False(t, hasAnswers, "a skipped batch must not carry an answers key")
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		// The card offers no free text for a question with choices, but a stale
		// peer client (or a hand-built RPC) can still send one. Hermes only
		// accepts one of its choices, so the literal "__other__" (or any label it
		// never offered) must be refused here, the card left actionable, instead
		// of being sent and the card shown as answered.
		Convey("When an answer names a label that is not one of the choices Then it is refused without answering and the card stays pending", func() {
			sessionID := int64(417)
			fake, rt, out := startClarifyTurn(t, sessionID)
			fake.push(clarifyServerRequest("srq-ans-offlist", `{"session_id":"live-1","questions":[{"qid":"q1","question":"Pick a color","choices":["red","blue"],"multi_select":true}]}`))
			_ = nextAskEvent(t, out)

			err := rt.SubmitAnswer(context.Background(), sessionID, "srq-ans-offlist", nil, []agentruntime.AskAnswer{
				{QuestionIndex: 0, Labels: []string{"red", agentruntime.OtherAnswerLabel}, OtherText: "green"},
			}, false)

			require.Error(t, err)
			assert.Empty(t, fake.sentAnswers())
			err = rt.SubmitAnswer(context.Background(), sessionID, "srq-ans-offlist", nil, []agentruntime.AskAnswer{
				{QuestionIndex: 0, Labels: []string{"blue"}},
			}, false)
			require.NoError(t, err, "the card must still be answerable after a refused answer")
			require.Len(t, fake.sentAnswers(), 1)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When there is no active turn Then ErrNoActiveTurn is returned", func() {
			rt := NewWithCredentials(nil, nil)
			err := rt.SubmitAnswer(context.Background(), 499, "srq-x", nil, nil, true)
			require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
		})

		Convey("When the requestID is unknown Then it is refused without answering", func() {
			sessionID := int64(416)
			fake, rt, out := startClarifyTurn(t, sessionID)

			err := rt.SubmitAnswer(context.Background(), sessionID, "srq-unknown-ask", nil, nil, true)

			require.ErrorIs(t, err, agentruntime.ErrWaiterNotFound)
			assert.Empty(t, fake.sentAnswers())
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})
	})
}

func TestHermesClarify_WithdrawnAndTurnEndCardsAreSkipped(t *testing.T) {
	Convey("Given a pending Hermes clarify card", t, func() {
		Convey("When Hermes withdraws it with request.cancel Then the card is marked skipped and a late answer is refused", func() {
			sessionID := int64(420)
			fake, rt, out := startClarifyTurn(t, sessionID)
			fake.push(clarifyServerRequest("srq-cancel00001", `{"session_id":"live-1","question":"Continue?","choices":["yes","no"]}`))
			_ = nextAskEvent(t, out)

			fake.push(clarifyRequestCancel("srq-cancel00001", "timeout"))
			resolved, ok := nextAskEvent(t, out).(agentruntime.UserAskResolved)

			require.True(t, ok)
			assert.Equal(t, "srq-cancel00001", resolved.RequestID)
			assert.True(t, resolved.Skipped)
			err := rt.SubmitAnswer(context.Background(), sessionID, "srq-cancel00001", nil,
				[]agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"yes"}}}, false)
			require.ErrorIs(t, err, agentruntime.ErrWaiterNotFound)
			assert.Empty(t, fake.sentAnswers())
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When request.cancel names another request Then this card stays pending", func() {
			sessionID := int64(421)
			fake, rt, out := startClarifyTurn(t, sessionID)
			fake.push(clarifyServerRequest("srq-keep0000001", `{"session_id":"live-1","question":"Continue?","choices":["yes","no"]}`))
			_ = nextAskEvent(t, out)

			fake.push(clarifyRequestCancel("srq-other-one", "timeout"))
			err := rt.SubmitAnswer(context.Background(), sessionID, "srq-keep0000001", nil,
				[]agentruntime.AskAnswer{{QuestionIndex: 0, Labels: []string{"yes"}}}, false)

			require.NoError(t, err)
			assert.Len(t, fake.sentAnswers(), 1)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		turnEnds := []struct {
			name string
			end  func(fake *fakeSession, cancel context.CancelFunc)
		}{
			{"the turn completes", func(fake *fakeSession, _ context.CancelFunc) {
				fake.push(Event{Kind: EventMessageComplete, Session: fake.liveSID, Payload: []byte(`{"text":"ok","status":"complete"}`)})
			}},
			{"the connection drops", func(fake *fakeSession, _ context.CancelFunc) { fake.closeEvents(ErrGatewayLost) }},
			{"the turn context is canceled", func(_ *fakeSession, cancel context.CancelFunc) { cancel() }},
		}
		for i, te := range turnEnds {
			Convey("When "+te.name+" with the card unanswered Then the card is marked skipped before the turn's terminal event", func() {
				sessionID := int64(430 + i)
				fake := newFakeSession()
				rt := NewWithCredentials(func(context.Context, sessionSpec) (Session, error) { return fake, nil }, nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				out, _, err := rt.Run(ctx, runRequest(sessionID))
				require.NoError(t, err)
				fake.push(clarifyServerRequest("srq-turnend00001", `{"session_id":"live-1","question":"Continue?","choices":["yes","no"]}`))
				_ = nextAskEvent(t, out)

				te.end(fake, cancel)
				events := collect(t, out)

				skippedAt, terminalAt := -1, -1
				for idx, ev := range events {
					switch e := ev.(type) {
					case agentruntime.UserAskResolved:
						if e.RequestID == "srq-turnend00001" && e.Skipped {
							skippedAt = idx
						}
					case agentruntime.Done, agentruntime.ErrorEvent:
						if terminalAt < 0 {
							terminalAt = idx
						}
					}
				}
				require.GreaterOrEqual(t, skippedAt, 0, "a pending clarify card must be marked skipped when the turn ends")
				if terminalAt >= 0 {
					assert.Less(t, skippedAt, terminalAt, "the card is skipped before the turn's terminal event")
				}
				assert.Empty(t, fake.sentAnswers())
			})
		}
	})
}
