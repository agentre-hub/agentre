package hermes

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
)

const fullApprovalParams = `{"session_id":"live-1","request_id":"appr-1","command":"rm -rf build/","description":"recursive delete","choices":["once","session","always","deny"],"tool_name":"terminal"}`

// approvalServerRequest is the `approval` server->client request as the codec
// hands it to the turn.
func approvalServerRequest(id, params string) Event {
	return Event{Kind: EventServerRequest, Session: "live-1", Request: &ServerRequest{ID: id, Method: "approval", Params: json.RawMessage(params)}}
}

func requestCancel(id, reason string) Event {
	return Event{Kind: EventRequestCancel, Session: "live-1", Payload: json.RawMessage(`{"id":"` + id + `","method":"approval","reason":"` + reason + `"}`)}
}

// nextApprovalEvent reads the turn stream until the next approval lifecycle
// event, skipping unrelated events.
func nextApprovalEvent(t *testing.T, out <-chan agentruntime.Event) agentruntime.Event {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-out:
			require.True(t, ok, "the turn ended before an approval event arrived")
			switch ev.(type) {
			case agentruntime.ExecApprovalRequested, agentruntime.ExecApprovalResolved:
				return ev
			}
		case <-timeout:
			t.Fatal("no approval event arrived")
		}
	}
}

func startApprovalTurn(t *testing.T, sessionID int64) (*fakeSession, *Runtime, <-chan agentruntime.Event) {
	t.Helper()
	fake := newFakeSession()
	rt := NewWithCredentials(func(context.Context, sessionSpec) (Session, error) { return fake, nil }, nil)
	out, _, err := rt.Run(context.Background(), runRequest(sessionID))
	require.NoError(t, err)
	return fake, rt, out
}

func TestHermesApproval_RequestBecomesApprovalCard(t *testing.T) {
	Convey("Given a turn in flight", t, func() {
		cases := []struct {
			name   string
			params string
			want   []string
		}{
			{
				name:   "every choice offered maps onto every normalized decision",
				params: fullApprovalParams,
				want: []string{
					agentruntime.ApprovalDecisionAllowOnce, agentruntime.ApprovalDecisionAllowSession,
					agentruntime.ApprovalDecisionAllowAlways, agentruntime.ApprovalDecisionDeny,
				},
			},
			{
				name:   "only the choices Hermes lists are offered",
				params: `{"session_id":"live-1","request_id":"appr-1","command":"ls","choices":["once","deny"]}`,
				want:   []string{agentruntime.ApprovalDecisionAllowOnce, agentruntime.ApprovalDecisionDeny},
			},
			{
				name:   "allow_session=false and allow_permanent=false withhold session and always",
				params: `{"session_id":"live-1","request_id":"appr-1","command":"ls","choices":["once","session","always","deny"],"allow_session":false,"allow_permanent":false}`,
				want:   []string{agentruntime.ApprovalDecisionAllowOnce, agentruntime.ApprovalDecisionDeny},
			},
			{
				name:   "unknown choices are dropped",
				params: `{"session_id":"live-1","request_id":"appr-1","command":"ls","choices":["once","forever","deny"]}`,
				want:   []string{agentruntime.ApprovalDecisionAllowOnce, agentruntime.ApprovalDecisionDeny},
			},
		}
		for i, c := range cases {
			Convey("When Hermes sends an approval request where "+c.name, func() {
				fake, _, out := startApprovalTurn(t, int64(100+i))
				fake.push(approvalServerRequest("srq-aaaaaaaaaaaa", c.params))

				ev := nextApprovalEvent(t, out)

				req, ok := ev.(agentruntime.ExecApprovalRequested)
				require.True(t, ok, "want ExecApprovalRequested, got %T", ev)
				assert.Equal(t, "srq-aaaaaaaaaaaa", req.ID, "the card id is the request id the answer must echo")
				assert.Equal(t, agentruntime.ApprovalKindHermes, req.ApprovalKind)
				assert.Equal(t, c.want, req.AllowedDecisions)
				assert.Empty(t, fake.sentAnswers(), "nothing is answered until the user decides")
				fake.closeEvents(io.EOF)
				_ = collect(t, out)
			})
		}

		Convey("When the request carries a command, description and tool name Then the card shows them", func() {
			fake, _, out := startApprovalTurn(t, 110)
			fake.push(approvalServerRequest("srq-bbbbbbbbbbbb", fullApprovalParams))

			req, ok := nextApprovalEvent(t, out).(agentruntime.ExecApprovalRequested)

			require.True(t, ok)
			assert.Equal(t, "rm -rf build/", req.CommandText)
			assert.Equal(t, "recursive delete", req.Description)
			assert.Equal(t, "terminal", req.ToolName)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When the request offers no decision Agentre can present Then it is declined and no card appears", func() {
			fake, _, out := startApprovalTurn(t, 111)
			fake.push(approvalServerRequest("srq-cccccccccccc", `{"session_id":"live-1","request_id":"appr-1","command":"ls","choices":[]}`))
			fake.push(Event{Kind: EventMessageComplete, Session: fake.liveSID, Payload: []byte(`{"text":"ok","status":"complete"}`)})
			fake.closeEvents(io.EOF)

			events := collect(t, out)

			for _, ev := range events {
				_, isCard := ev.(agentruntime.ExecApprovalRequested)
				assert.False(t, isCard, "an unanswerable approval must not become a card")
			}
			assert.Equal(t, []fakeAnswer{{id: "srq-cccccccccccc", rejected: true}}, fake.sentAnswers())
		})
	})
}

func TestHermesApproval_DecisionIsAnsweredByID(t *testing.T) {
	Convey("Given a pending Hermes approval card offering every decision", t, func() {
		cases := []struct {
			decision string
			choice   string
		}{
			{agentruntime.ApprovalDecisionAllowOnce, "once"},
			{agentruntime.ApprovalDecisionAllowSession, "session"},
			{agentruntime.ApprovalDecisionAllowAlways, "always"},
			{agentruntime.ApprovalDecisionDeny, "deny"},
		}
		for i, c := range cases {
			Convey("When the user picks "+c.decision+" Then Hermes receives choice "+c.choice+" under the same id", func() {
				sessionID := int64(200 + i)
				fake, rt, out := startApprovalTurn(t, sessionID)
				fake.push(approvalServerRequest("srq-dddddddddddd", fullApprovalParams))
				_ = nextApprovalEvent(t, out)

				resolution, err := rt.ResolveExecApproval(context.Background(), sessionID, "srq-dddddddddddd", c.decision)

				require.NoError(t, err)
				assert.Equal(t, agentruntime.ExecApprovalResolution{Status: "resolved", Decision: c.decision}, resolution)
				require.Len(t, fake.sentAnswers(), 1)
				answer := fake.sentAnswers()[0]
				assert.Equal(t, "srq-dddddddddddd", answer.id)
				assert.False(t, answer.rejected)
				encoded, err := json.Marshal(answer.result)
				require.NoError(t, err)
				assert.JSONEq(t, `{"choice":"`+c.choice+`"}`, string(encoded))

				resolved, ok := nextApprovalEvent(t, out).(agentruntime.ExecApprovalResolved)
				require.True(t, ok)
				assert.Equal(t, "srq-dddddddddddd", resolved.ID)
				assert.Equal(t, "resolved", resolved.Status)
				assert.Equal(t, c.decision, resolved.Decision)
				fake.closeEvents(io.EOF)
				_ = collect(t, out)
			})
		}

		Convey("When the decision is not one Hermes offered Then it is refused without answering", func() {
			fake, rt, out := startApprovalTurn(t, 210)
			fake.push(approvalServerRequest("srq-eeeeeeeeeeee", `{"session_id":"live-1","request_id":"appr-1","command":"ls","choices":["once","deny"]}`))
			_ = nextApprovalEvent(t, out)

			_, err := rt.ResolveExecApproval(context.Background(), 210, "srq-eeeeeeeeeeee", agentruntime.ApprovalDecisionAllowAlways)

			require.Error(t, err)
			assert.Empty(t, fake.sentAnswers())
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When the approval id is unknown Then it is refused without answering", func() {
			fake, rt, out := startApprovalTurn(t, 211)

			_, err := rt.ResolveExecApproval(context.Background(), 211, "srq-unknown", agentruntime.ApprovalDecisionDeny)

			require.Error(t, err)
			assert.Empty(t, fake.sentAnswers())
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When several answers race Then Hermes is answered once and every caller converges on one terminal", func() {
			fake, rt, out := startApprovalTurn(t, 212)
			fake.push(approvalServerRequest("srq-ffffffffffff", fullApprovalParams))
			_ = nextApprovalEvent(t, out)

			decisions := []string{
				agentruntime.ApprovalDecisionAllowOnce, agentruntime.ApprovalDecisionDeny,
				agentruntime.ApprovalDecisionAllowOnce, agentruntime.ApprovalDecisionAllowSession,
			}
			results := make([]agentruntime.ExecApprovalResolution, len(decisions))
			var wg sync.WaitGroup
			for i, d := range decisions {
				wg.Add(1)
				go func() {
					defer wg.Done()
					res, err := rt.ResolveExecApproval(context.Background(), 212, "srq-ffffffffffff", d)
					assert.NoError(t, err)
					results[i] = res
				}()
			}
			wg.Wait()

			require.Len(t, fake.sentAnswers(), 1, "duplicate answers must not reach Hermes")
			for _, res := range results {
				assert.Equal(t, results[0], res)
			}
			assert.Equal(t, "resolved", results[0].Status)
			fake.push(Event{Kind: EventMessageComplete, Session: fake.liveSID, Payload: []byte(`{"text":"ok","status":"complete"}`)})
			fake.closeEvents(io.EOF)
			resolvedCount := 0
			for _, ev := range collect(t, out) {
				if _, ok := ev.(agentruntime.ExecApprovalResolved); ok {
					resolvedCount++
				}
			}
			assert.Equal(t, 1, resolvedCount, "exactly one terminal event per card")
		})

		Convey("When there is no active turn Then ErrNoActiveTurn is returned", func() {
			rt := NewWithCredentials(nil, nil)
			_, err := rt.ResolveExecApproval(context.Background(), 299, "srq-x", agentruntime.ApprovalDecisionDeny)
			require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
		})
	})
}

func TestHermesApproval_WithdrawnAndTurnEndCardsTerminate(t *testing.T) {
	Convey("Given a pending Hermes approval card", t, func() {
		for i, reason := range []string{"timeout", "interrupted", "shutdown", "session_closed"} {
			Convey("When Hermes withdraws it with request.cancel reason "+reason+" Then the card expires and a late answer is not sent", func() {
				sessionID := int64(300 + i)
				fake, rt, out := startApprovalTurn(t, sessionID)
				fake.push(approvalServerRequest("srq-111111111111", fullApprovalParams))
				_ = nextApprovalEvent(t, out)

				fake.push(requestCancel("srq-111111111111", reason))
				resolved, ok := nextApprovalEvent(t, out).(agentruntime.ExecApprovalResolved)

				require.True(t, ok)
				assert.Equal(t, "srq-111111111111", resolved.ID)
				assert.Equal(t, "expired", resolved.Status)
				res, err := rt.ResolveExecApproval(context.Background(), sessionID, "srq-111111111111", agentruntime.ApprovalDecisionAllowOnce)
				require.NoError(t, err)
				assert.Equal(t, "expired", res.Status)
				assert.Empty(t, fake.sentAnswers())
				fake.closeEvents(io.EOF)
				_ = collect(t, out)
			})
		}

		Convey("When Hermes withdraws it because another surface answered Then the card is resolved", func() {
			fake, _, out := startApprovalTurn(t, 310)
			fake.push(approvalServerRequest("srq-222222222222", fullApprovalParams))
			_ = nextApprovalEvent(t, out)

			fake.push(requestCancel("srq-222222222222", "resolved"))
			resolved, ok := nextApprovalEvent(t, out).(agentruntime.ExecApprovalResolved)

			require.True(t, ok)
			assert.Equal(t, "resolved", resolved.Status)
			fake.closeEvents(io.EOF)
			_ = collect(t, out)
		})

		Convey("When request.cancel names another request Then the card stays pending", func() {
			fake, rt, out := startApprovalTurn(t, 311)
			fake.push(approvalServerRequest("srq-333333333333", fullApprovalParams))
			_ = nextApprovalEvent(t, out)

			fake.push(requestCancel("srq-other", "timeout"))
			res, err := rt.ResolveExecApproval(context.Background(), 311, "srq-333333333333", agentruntime.ApprovalDecisionDeny)

			require.NoError(t, err)
			assert.Equal(t, "resolved", res.Status)
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
			{"the gateway reports an error", func(fake *fakeSession, _ context.CancelFunc) {
				fake.push(Event{Kind: EventError, Session: fake.liveSID, Payload: []byte(`{"message":"boom"}`)})
			}},
			{"the turn is interrupted", func(fake *fakeSession, _ context.CancelFunc) {
				fake.push(Event{Kind: EventMessageComplete, Session: fake.liveSID, Payload: []byte(`{"text":"","status":"interrupted"}`)})
			}},
			{"the connection drops", func(fake *fakeSession, _ context.CancelFunc) { fake.closeEvents(ErrGatewayLost) }},
			{"the turn context is canceled", func(_ *fakeSession, cancel context.CancelFunc) { cancel() }},
		}
		for i, te := range turnEnds {
			Convey("When "+te.name+" with the card unanswered Then the card expires before the turn's terminal event", func() {
				sessionID := int64(320 + i)
				fake := newFakeSession()
				rt := NewWithCredentials(func(context.Context, sessionSpec) (Session, error) { return fake, nil }, nil)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				out, _, err := rt.Run(ctx, runRequest(sessionID))
				require.NoError(t, err)
				fake.push(approvalServerRequest("srq-444444444444", fullApprovalParams))
				_ = nextApprovalEvent(t, out)

				te.end(fake, cancel)
				events := collect(t, out)

				expiredAt, terminalAt := -1, -1
				for idx, ev := range events {
					switch e := ev.(type) {
					case agentruntime.ExecApprovalResolved:
						if e.ID == "srq-444444444444" && e.Status == "expired" {
							expiredAt = idx
						}
					case agentruntime.Done, agentruntime.ErrorEvent:
						if terminalAt < 0 {
							terminalAt = idx
						}
					}
				}
				require.GreaterOrEqual(t, expiredAt, 0, "a pending card must expire when the turn ends")
				if terminalAt >= 0 {
					assert.Less(t, expiredAt, terminalAt, "the card expires before the turn's terminal event")
				}
				assert.Empty(t, fake.sentAnswers())
			})
		}
	})
}
