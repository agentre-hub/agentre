package hermes

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cago-frame/agents/provider"
	"github.com/gorilla/websocket"
	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

// fakeSession is a scripted gateway: the test pushes frames into events, and
// records what the runtime called.
type fakeSession struct {
	liveSID   string
	storedKey string
	baseline  *provider.Usage

	createCalls   atomic.Int64
	resumeCalls   atomic.Int64
	submitCalls   atomic.Int64
	interrupts    atomic.Int64
	closeCalls    atomic.Int64
	submitErr     error
	resumeErr     error
	createErr     error
	interruptErr  error
	lastSubmit    string
	lastResumeKey string

	events   chan Event
	errMu    sync.Mutex
	readErr  error
	closed   chan struct{}
	closeOne sync.Once
}

func newFakeSession() *fakeSession {
	return &fakeSession{
		liveSID:   "live-1",
		storedKey: "stored-1",
		events:    make(chan Event, 64),
		closed:    make(chan struct{}),
	}
}

func (f *fakeSession) Create(_ context.Context, _ string, _ int) (string, string, error) {
	f.createCalls.Add(1)
	if f.createErr != nil {
		return "", "", f.createErr
	}
	return f.liveSID, f.storedKey, nil
}

func (f *fakeSession) Resume(_ context.Context, stored string) (string, error) {
	f.resumeCalls.Add(1)
	f.lastResumeKey = stored
	if f.resumeErr != nil {
		return "", f.resumeErr
	}
	return f.liveSID, nil
}

func (f *fakeSession) Usage(context.Context, string) (*provider.Usage, error) {
	return f.baseline, nil
}

func (f *fakeSession) Submit(_ context.Context, _ /*liveSID*/, text string) error {
	f.submitCalls.Add(1)
	f.lastSubmit = text
	return f.submitErr
}

func (f *fakeSession) Interrupt(context.Context, string) error {
	f.interrupts.Add(1)
	return f.interruptErr
}

func (f *fakeSession) Events() <-chan Event { return f.events }
func (f *fakeSession) Err() error           { f.errMu.Lock(); defer f.errMu.Unlock(); return f.readErr }

func (f *fakeSession) Close(context.Context) error {
	f.closeCalls.Add(1)
	f.closeOne.Do(func() { close(f.closed) })
	return nil
}

func (f *fakeSession) finish(err error) {
	f.errMu.Lock()
	f.readErr = err
	f.errMu.Unlock()
	f.closeOne.Do(func() { close(f.closed) })
}

func (f *fakeSession) push(ev Event) { f.events <- ev }

func (f *fakeSession) closeEvents(err error) {
	f.finish(err)
	close(f.events)
}

func testBackend() *agent_backend_entity.AgentBackend {
	return &agent_backend_entity.AgentBackend{
		Type:      string(agent_backend_entity.TypeHermes),
		Name:      "hermes",
		HermesURL: "http://127.0.0.1:9119",
		EnvJSON:   "{}",
	}
}

func runRequest(sessionID int64) agentruntime.RunRequest {
	return agentruntime.RunRequest{
		Backend:   testBackend(),
		SessionID: sessionID,
		UserText:  "hello",
		Cwd:       "/tmp",
	}
}

func collect(t *testing.T, events <-chan agentruntime.Event) []agentruntime.Event {
	t.Helper()
	var out []agentruntime.Event
	for ev := range events {
		out = append(out, ev)
	}
	return out
}

// TestHermesCapabilities is the capability matrix: every declared cap=true must
// have its interface implemented, and nothing else may be declared.
func TestHermesCapabilities(t *testing.T) {
	rt := NewWithSessionFactory(nil)
	caps := rt.Capabilities()

	assert.True(t, caps.Has(capability.CapAbort), "abort is declared because session.interrupt is wired")
	_, implementsAborter := interface{}(rt).(agentruntime.Aborter)
	assert.True(t, implementsAborter, "CapAbort=true requires Aborter")

	for _, cap := range []capability.Capability{
		capability.CapSteer,
		capability.CapCancelSteer,
		capability.CapDrainSteer,
		capability.CapSetPermission,
		capability.CapAnswerUserAsk,
		capability.CapToolPermission,
		capability.CapForkSession,
		capability.CapReportContextWindow,
		capability.CapCompact,
		capability.CapGoal,
		capability.CapMCPTools,
		capability.CapSkills,
		capability.CapImageInput,
		capability.CapAutonomousTurn,
		capability.CapReasoningEffort,
	} {
		assert.False(t, caps.Has(cap), "the runtime must not declare %s", cap)
	}
	// The runtime must NOT accidentally satisfy the reverse-channel interfaces.
	_, isSteerer := interface{}(rt).(agentruntime.Steerer)
	assert.False(t, isSteerer)
	_, isToolPerm := interface{}(rt).(agentruntime.ToolPermissionSink)
	assert.False(t, isToolPerm)
	_, isAskSink := interface{}(rt).(agentruntime.AskAnswerSink)
	assert.False(t, isAskSink)
	_, isPermSetter := interface{}(rt).(agentruntime.PermissionModeSetter)
	assert.False(t, isPermSetter)
	_, isAuto := interface{}(rt).(agentruntime.AutonomousTurnSource)
	assert.False(t, isAuto)
}

func TestHermesRun_StreamsTurnAndFillsResult(t *testing.T) {
	Convey("Given a fake gateway and a fresh session", t, func() {
		fake := newFakeSession()
		rt := NewWithSessionFactory(func(context.Context, sessionSpec) (Session, error) { return fake, nil })

		out, result, err := rt.Run(context.Background(), runRequest(7))
		require.NoError(t, err)

		fake.push(Event{Kind: EventMessageStart, Session: fake.liveSID})
		fake.push(Event{Kind: EventMessageDelta, Session: fake.liveSID, Payload: []byte(`{"text":"hel"}`)})
		fake.push(Event{Kind: EventMessageDelta, Session: fake.liveSID, Payload: []byte(`{"text":"lo"}`)})
		fake.push(Event{Kind: EventToolStart, Session: fake.liveSID, Payload: []byte(`{"tool_id":"t1","name":"terminal","args":{"command":"ls"}}`)})
		fake.push(Event{Kind: EventToolComplete, Session: fake.liveSID, Payload: []byte(`{"tool_id":"t1","name":"terminal","result":"ok"}`)})
		fake.push(Event{Kind: EventMessageComplete, Session: fake.liveSID, Payload: []byte(`{"text":"hello","status":"complete","usage":{"model":"hermes-3","prompt":10,"completion":2,"total":12}}`)})
		fake.closeEvents(io.EOF)

		events := collect(t, out)
		require.NoError(t, waitClosed(fake.closed))

		// Ordering: text deltas, tool call, tool result, usage, done.
		var kinds []string
		for _, ev := range events {
			switch e := ev.(type) {
			case agentruntime.TextDelta:
				kinds = append(kinds, "text:"+e.Text)
			case agentruntime.ToolCall:
				kinds = append(kinds, "call:"+e.ID)
			case agentruntime.ToolResult:
				kinds = append(kinds, "result:"+e.ToolCallID)
			case agentruntime.UsageUpdate:
				kinds = append(kinds, "usage")
			case agentruntime.Done:
				kinds = append(kinds, "done")
			}
		}
		assert.Equal(t, []string{"text:hel", "text:lo", "call:t1", "result:t1", "usage", "done"}, kinds)

		assert.Equal(t, "stored-1", result.ProviderSessionID)
		assert.Equal(t, "hermes-3", result.Model)
		assert.NoError(t, result.StopErr)
		require.NotNil(t, result.Usage)
		assert.Equal(t, 10, result.Usage.PromptTokens)
		assert.Equal(t, 2, result.Usage.CompletionTokens)

		assert.EqualValues(t, 1, fake.createCalls.Load(), "fresh session must create, not resume")
		assert.EqualValues(t, 0, fake.resumeCalls.Load())
		assert.Equal(t, "hello", fake.lastSubmit)
	})
}

func TestHermesRun_ResumesStoredSession(t *testing.T) {
	Convey("Given a persisted provider session id", t, func() {
		fake := newFakeSession()
		rt := NewWithSessionFactory(func(context.Context, sessionSpec) (Session, error) { return fake, nil })
		req := runRequest(8)
		req.ProviderSessionID = "stored-old"

		out, result, err := rt.Run(context.Background(), req)
		require.NoError(t, err)
		fake.push(Event{Kind: EventMessageComplete, Session: fake.liveSID, Payload: []byte(`{"text":"ok","status":"complete"}`)})
		fake.closeEvents(io.EOF)
		_ = collect(t, out)

		assert.EqualValues(t, 1, fake.resumeCalls.Load())
		assert.EqualValues(t, 0, fake.createCalls.Load())
		assert.Equal(t, "stored-old", fake.lastResumeKey)
		assert.Equal(t, "stored-old", result.ProviderSessionID)
	})
}

func TestHermesRun_RefusedSubmitSurfacesOwnershipFence(t *testing.T) {
	Convey("Given the gateway refuses prompt.submit with SESSION_NOT_OWNED", t, func() {
		fake := newFakeSession()
		fake.submitErr = ErrSessionNotOwned
		rt := NewWithSessionFactory(func(context.Context, sessionSpec) (Session, error) { return fake, nil })

		out, result, err := rt.Run(context.Background(), runRequest(9))

		require.ErrorIs(t, err, ErrSessionNotOwned)
		assert.Nil(t, out)
		assert.Nil(t, result)
		assert.GreaterOrEqual(t, fake.closeCalls.Load(), int64(1), "a refused turn must not leak the child")
	})
}

func TestHermesRun_ContextCancelUnblocks(t *testing.T) {
	Convey("Given an in-flight turn and no terminal frame", t, func() {
		fake := newFakeSession()
		rt := NewWithSessionFactory(func(context.Context, sessionSpec) (Session, error) { return fake, nil })
		ctx, cancel := context.WithCancel(context.Background())

		out, result, err := rt.Run(ctx, runRequest(10))
		require.NoError(t, err)
		fake.push(Event{Kind: EventMessageDelta, Session: fake.liveSID, Payload: []byte(`{"text":"partial"}`)})

		cancel()
		events := collect(t, out) // must return, not hang

		require.ErrorIs(t, result.StopErr, context.Canceled)
		assert.GreaterOrEqual(t, fake.interrupts.Load(), int64(1), "ctx cancel must unblock the gateway I/O")
		_, hasError := lastErrorEvent(events)
		assert.True(t, hasError)
	})
}

func TestHermesAbort_InterruptsAndThreadsToken(t *testing.T) {
	Convey("Given an active turn", t, func() {
		fake := newFakeSession()
		rt := NewWithSessionFactory(func(context.Context, sessionSpec) (Session, error) { return fake, nil })
		out, result, err := rt.Run(context.Background(), runRequest(11))
		require.NoError(t, err)
		token := result.TurnToken

		outcome, err := rt.Abort(context.Background(), 11, token)
		require.NoError(t, err)
		assert.Equal(t, agentruntime.TurnKindUser, outcome.TurnKind)
		assert.EqualValues(t, 1, fake.interrupts.Load())

		// stale token is a no-op.
		outcome, err = rt.Abort(context.Background(), 11, token+99)
		require.NoError(t, err)
		assert.Equal(t, agentruntime.TurnKindNone, outcome.TurnKind)
		assert.EqualValues(t, 1, fake.interrupts.Load())

		// gateway eventually reports the interrupted turn.
		fake.push(Event{Kind: EventMessageComplete, Session: fake.liveSID, Payload: []byte(`{"text":"","status":"interrupted"}`)})
		fake.closeEvents(io.EOF)
		_ = collect(t, out)
		assert.ErrorIs(t, result.StopErr, agentruntime.ErrAborted)
	})
}

func TestHermesAbort_NoActiveTurn(t *testing.T) {
	Convey("Given no in-flight turn", t, func() {
		rt := NewWithSessionFactory(func(context.Context, sessionSpec) (Session, error) { return newFakeSession(), nil })
		_, err := rt.Abort(context.Background(), 123, 0)
		require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
	})
}

// A Stop click can race the end of a turn: the drain loop has already closed the
// gateway child while the active entry is still visible. Contract is ErrNoActiveTurn —
// a raw "file already closed" write error would surface as a bogus stop failure for a
// turn that had, in fact, finished.
func TestHermesAbort_GivenChildAlreadyClosed_ThenNoActiveTurn(t *testing.T) {
	Convey("Given the turn's gateway child has already shut down", t, func() {
		fake := newFakeSession()
		fake.interruptErr = errSessionClosed
		rt := NewWithSessionFactory(func(context.Context, sessionSpec) (Session, error) { return fake, nil })
		_, result, err := rt.Run(context.Background(), runRequest(21))
		require.NoError(t, err)

		outcome, err := rt.Abort(context.Background(), 21, result.TurnToken)
		assert.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
		// Same shape as every other backend for this path: the zero outcome, so
		// nothing attributes a user stop to a turn that had already finished.
		assert.NotEqual(t, agentruntime.TurnKindUser, outcome.TurnKind)
	})
}

// A genuine interrupt failure (the child is alive) must still be reported, otherwise
// the UI would claim a stop that never reached the backend.
func TestHermesAbort_GivenInterruptFails_ThenErrorPropagates(t *testing.T) {
	Convey("Given session.interrupt fails while the child is alive", t, func() {
		fake := newFakeSession()
		fake.interruptErr = errors.New("interrupt exploded")
		rt := NewWithSessionFactory(func(context.Context, sessionSpec) (Session, error) { return fake, nil })
		_, result, err := rt.Run(context.Background(), runRequest(22))
		require.NoError(t, err)

		_, err = rt.Abort(context.Background(), 22, result.TurnToken)
		require.Error(t, err)
		assert.NotErrorIs(t, err, agentruntime.ErrNoActiveTurn)
		assert.ErrorContains(t, err, "interrupt exploded")
	})
}

// A real closed connection must reach the same ErrNoActiveTurn as the fake: Close
// signals the close before closing the socket, so the interrupt write is classified
// as "session closed" rather than a raw socket error.
func TestHermesAbort_GivenRealConnectionClosed_ThenNoActiveTurn(t *testing.T) {
	Convey("Given a real gateway session that has been closed", t, func() {
		server := newHermesTestServer(t, "tok", func(conn *websocket.Conn) {
			writeReady(t, conn)
			holdConn(conn)
		})
		sess, err := dialGateway(context.Background(), server.URL, nil, nil)
		require.NoError(t, err)
		require.NoError(t, sess.WaitReady(context.Background()))
		require.NoError(t, sess.Close(context.Background()))

		rt := NewWithSessionFactory(nil)
		active := &activeTurn{sess: sess, liveSID: "live-1"}
		active.token.Store(5)
		rt.register(31, active)

		_, err = rt.Abort(context.Background(), 31, 5)

		require.ErrorIs(t, err, agentruntime.ErrNoActiveTurn)
	})
}

func TestHermesRun_FallsBackToCompleteTextWhenNoDeltas(t *testing.T) {
	Convey("Given the gateway only sends the final text on message.complete", t, func() {
		fake := newFakeSession()
		rt := NewWithSessionFactory(func(context.Context, sessionSpec) (Session, error) { return fake, nil })
		out, _, err := rt.Run(context.Background(), runRequest(12))
		require.NoError(t, err)
		fake.push(Event{Kind: EventMessageComplete, Session: fake.liveSID, Payload: []byte(`{"text":"only-final","status":"complete"}`)})
		fake.closeEvents(io.EOF)

		events := collect(t, out)
		require.Len(t, events, 2)
		assert.Equal(t, agentruntime.TextDelta{Text: "only-final"}, events[0])
		assert.Equal(t, agentruntime.Done{}, events[1])
	})
}

func TestHermesRun_StreamEndsBeforeCompletion(t *testing.T) {
	Convey("Given the gateway dies before a terminal frame", t, func() {
		fake := newFakeSession()
		rt := NewWithSessionFactory(func(context.Context, sessionSpec) (Session, error) { return fake, nil })
		out, result, err := rt.Run(context.Background(), runRequest(13))
		require.NoError(t, err)
		fake.closeEvents(io.EOF)

		events := collect(t, out)
		require.Error(t, result.StopErr)
		_, hasError := lastErrorEvent(events)
		assert.True(t, hasError)
	})
}

func waitClosed(ch <-chan struct{}) error {
	select {
	case <-ch:
		return nil
	case <-time.After(2 * time.Second):
		return errors.New("timeout waiting for session close")
	}
}

func lastErrorEvent(events []agentruntime.Event) (agentruntime.ErrorEvent, bool) {
	for i := len(events) - 1; i >= 0; i-- {
		if e, ok := events[i].(agentruntime.ErrorEvent); ok {
			return e, true
		}
	}
	return agentruntime.ErrorEvent{}, false
}

func TestMapResumeError(t *testing.T) {
	Convey("Given a session.resume failure", t, func() {
		Convey("When the gateway reports session not found Then the shared sentinel is returned", func() {
			err := mapResumeError(&rpcCallError{Code: 4007, Message: "session not found", Method: "session.resume"})
			So(errors.Is(err, agentruntime.ErrSessionNotFound), ShouldBeTrue)
		})
		Convey("When the failure is unrelated Then it passes through", func() {
			err := mapResumeError(&rpcCallError{Code: 5000, Message: "db unavailable", Method: "session.resume"})
			So(errors.Is(err, agentruntime.ErrSessionNotFound), ShouldBeFalse)
		})
		Convey("When the error is nil Then nil is returned", func() {
			So(mapResumeError(nil), ShouldBeNil)
		})
	})
}
