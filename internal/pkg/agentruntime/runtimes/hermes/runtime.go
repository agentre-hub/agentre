package hermes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cago-frame/agents/provider"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime"
	"github.com/agentre-hub/agentre/internal/pkg/agentruntime/capability"
)

// gatewayTurnError marks a turn failure reported by the gateway (as opposed to a
// local I/O failure), so logs can tell the two apart without inspecting text.
type gatewayTurnError struct{ msg string }

func (e *gatewayTurnError) Error() string { return "hermes gateway: " + e.msg }

func errGatewayTurn(msg string) error { return &gatewayTurnError{msg: msg} }

// Runtime is the Hermes `serve` runtime. It dials one WebSocket connection per
// turn (the connection is the per-session ownership unit the fence guards).
type Runtime struct {
	factory SessionFactory
	// credentials supplies access tokens for gated serves. nil means only the
	// loopback `?token=` path is available.
	credentials CredentialSource

	mu     sync.Mutex
	active map[int64]*activeTurn
}

type activeTurn struct {
	sess      Session
	sessionID int64
	liveSID   string
	token     atomic.Uint64

	abortRequested  atomic.Bool
	cumulativeUsage atomic.Pointer[provider.Usage]

	// out is the turn's event stream. The drain goroutine owns it; approval
	// answers arrive on other goroutines and write through emit, which never
	// sends after closeOut.
	out       chan agentruntime.Event
	outMu     sync.Mutex
	outClosed bool

	approvalMu        sync.Mutex
	approvalResolveMu sync.Mutex
	approvals         map[string]*approvalState

	askMu        sync.Mutex
	askResolveMu sync.Mutex
	asks         map[string]*askState
}

func newActiveTurn(sess Session, sessionID int64, liveSID string) *activeTurn {
	return &activeTurn{
		sess: sess, sessionID: sessionID, liveSID: liveSID,
		out:       make(chan agentruntime.Event, 32),
		approvals: map[string]*approvalState{},
		asks:      map[string]*askState{},
	}
}

// emit writes an event from any goroutine; after the turn closed it is dropped.
func (a *activeTurn) emit(ev agentruntime.Event) {
	a.outMu.Lock()
	defer a.outMu.Unlock()
	if !a.outClosed {
		a.out <- ev
	}
}

func (a *activeTurn) closeOut() {
	a.outMu.Lock()
	defer a.outMu.Unlock()
	if !a.outClosed {
		a.outClosed = true
		close(a.out)
	}
}

var defaultRuntime = New()

func init() {
	agentruntime.RegisterRuntime(agent_backend_entity.TypeHermes, defaultRuntime)
}

// New returns a Runtime wired to the real gateway dialer and the process-wide
// credential source.
func New() *Runtime {
	return NewWithCredentials(defaultSessionFactory, DefaultCredentialSource())
}

// NewWithCredentials wires both seams: the dial factory and the gated-serve
// credential source. A nil factory falls back to the real dialer.
func NewWithCredentials(factory SessionFactory, creds CredentialSource) *Runtime {
	if factory == nil {
		factory = defaultSessionFactory
	}
	return &Runtime{
		factory:     factory,
		credentials: creds,
		active:      map[int64]*activeTurn{},
	}
}

// Capabilities declares only what this runtime actually implements: abort via
// session.interrupt, approvals via the `approval` server request, and
// answering questions via the `clarify` server request. Steer, permissions,
// compaction, fork, image input and native session reuse are not wired yet
// and must honestly report unsupported.
func (r *Runtime) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Set: map[capability.Capability]bool{
			capability.CapAbort:         true,
			capability.CapExecApproval:  true,
			capability.CapAnswerUserAsk: true,
		},
	}
}

// Run starts one turn against a fresh gateway connection and streams sealed
// events. The submitted session key (stored session id) is returned as
// RunResult.ProviderSessionID so the next turn can resume it.
func (r *Runtime) Run(ctx context.Context, req agentruntime.RunRequest) (<-chan agentruntime.Event, *agentruntime.RunResult, error) {
	if req.Backend == nil {
		return nil, nil, errors.New("hermes runtime: nil backend")
	}
	cwd := req.Cwd
	if cwd == "" {
		var err error
		cwd, err = agentruntime.ResolveAgentCwd(req.AgentID, req.AgentSyncID)
		if err != nil {
			return nil, nil, err
		}
	}
	spec := sessionSpec{
		URL:          strings.TrimSpace(req.Backend.HermesURL),
		AuthProvider: strings.TrimSpace(req.Backend.HermesAuthProvider),
		Credentials:  r.credentials,
	}
	// The process-wide credential source is registered by the service package, whose
	// init runs after this package's defaultRuntime is constructed. Resolve it at call
	// time so the registered source is honored instead of the nil captured at init.
	if spec.Credentials == nil {
		spec.Credentials = DefaultCredentialSource()
	}
	sess, err := r.factory(ctx, spec)
	if err != nil {
		logger.Ctx(ctx).Error("hermes.Runtime: gateway dial failed",
			zap.Int64("sessionID", req.SessionID), zap.String("gatewayURL", spec.URL), zap.Error(err))
		return nil, nil, err
	}
	started := false
	defer func() {
		if !started {
			_ = sess.Close(context.Background())
		}
	}()

	liveSID, storedKey, err := openGatewaySession(ctx, sess, req, cwd)
	if err != nil {
		return nil, nil, err
	}
	// Pre-submit cumulative usage is the turn's baseline: session.usage is
	// monotonic per stored session, so the per-turn figure is the growth.
	baseline, _ := sess.Usage(ctx, liveSID)
	if err := sess.Submit(ctx, liveSID, req.UserText); err != nil {
		return nil, nil, err
	}

	active := newActiveTurn(sess, req.SessionID, liveSID)
	active.cumulativeUsage.Store(baseline)
	result := &agentruntime.RunResult{ProviderSessionID: storedKey, TurnToken: active.token.Add(1)}
	r.register(req.SessionID, active)
	started = true

	out := active.out
	go func() {
		defer active.closeOut()
		defer r.unregister(req.SessionID, active)
		defer func() { _ = sess.Close(context.Background()) }()
		logger.Ctx(ctx).Info("hermes.Runtime: turn started",
			zap.Int64("sessionID", req.SessionID),
			zap.String("providerSessionID", storedKey),
			zap.String("liveSessionID", liveSID),
			zap.String("cwd", cwd))
		drainTurn(ctx, sess, out, result, active)
	}()
	return out, result, nil
}

// openGatewaySession creates a fresh Hermes session or resumes the persisted
// stored session key. The live runtime id differs from the stored key and is
// what every later RPC (submit/interrupt/usage) must use.
func openGatewaySession(ctx context.Context, sess Session, req agentruntime.RunRequest, cwd string) (liveSID, storedKey string, err error) {
	if stored := strings.TrimSpace(req.ProviderSessionID); stored != "" {
		live, resumeErr := sess.Resume(ctx, stored)
		if resumeErr != nil {
			return "", "", mapResumeError(resumeErr)
		}
		return live, stored, nil
	}
	live, stored, err := sess.Create(ctx, cwd)
	if err != nil {
		return "", "", err
	}
	return live, stored, nil
}

// mapResumeError turns the gateway's "session not found" into the shared
// sentinel so chat_svc clears the stale provider session id and fails the turn
// instead of silently starting a replacement session.
func mapResumeError(err error) error {
	if err == nil {
		return nil
	}
	var rpcErr *rpcCallError
	// 4007 is Hermes' `session not found` from session.resume; 4001 is the
	// generic session-not-found code. Either clears the stale provider id.
	if errors.As(err, &rpcErr) && (rpcErr.Code == 4007 || rpcErr.Code == 4001 || strings.Contains(strings.ToLower(rpcErr.Message), "not found")) {
		return fmt.Errorf("%w: %w", agentruntime.ErrSessionNotFound, err)
	}
	return err
}

// drainTurn consumes the gateway event stream until the terminal frame, a
// process death, or ctx cancellation. It owns the per-turn usage baseline and
// the final-text fallback; the translator stays stateless.
func drainTurn(ctx context.Context, sess Session, out chan<- agentruntime.Event, result *agentruntime.RunResult, active *activeTurn) {
	var (
		textSeen  bool
		turnUsage provider.Usage
		haveUsage bool
	)
	finish := func() {
		if haveUsage && !usageIsZero(&turnUsage) {
			u := turnUsage
			result.Usage = &u
		}
	}
	for {
		select {
		case <-ctx.Done():
			if active.abortRequested.Load() {
				result.StopErr = agentruntime.ErrAborted
			} else {
				result.StopErr = ctx.Err()
			}
			_ = sess.Interrupt(context.Background(), active.liveSID)
			active.expirePending()
			out <- agentruntime.ErrorEvent{Err: result.StopErr}
			finish()
			return
		case ev, ok := <-sess.Events():
			if !ok {
				if result.StopErr == nil {
					result.StopErr = streamEndedError(sess)
				}
				active.expirePending()
				out <- agentruntime.ErrorEvent{Err: result.StopErr}
				finish()
				return
			}
			switch ev.Kind {
			case EventServerRequest:
				active.handleServerRequest(ctx, ev.Request)
				continue
			case EventRequestCancel:
				active.handleRequestCancel(ev.Payload)
				continue
			case EventUnsupportedRequest:
				active.handleUnsupportedRequest(ev.Method)
				continue
			}
			events, usage, stopErr := translate(ev)
			if usage != nil {
				delta := usageDelta(usage, active.cumulativeUsage.Load())
				active.cumulativeUsage.Store(usage)
				if !usageIsZero(delta) {
					addUsage(&turnUsage, delta)
					haveUsage = true
					d := *delta
					out <- agentruntime.UsageUpdate{Usage: &d, TotalInputTokens: d.PromptTokens}
				}
				if model := usageModelFromEvent(ev); model != "" {
					result.Model = model
				}
			}
			terminal := ev.Kind == EventMessageComplete || ev.Kind == EventError
			if ev.Kind == EventMessageComplete && !textSeen {
				if text := messageCompleteText(ev.Payload); text != "" {
					out <- agentruntime.TextDelta{Text: text}
					textSeen = true
				}
			}
			if terminal {
				// Unanswered cards expire before the turn's terminal event.
				active.expirePending()
			}
			for _, e := range events {
				if _, isText := e.(agentruntime.TextDelta); isText {
					textSeen = true
				}
				out <- e
			}
			if stopErr != nil {
				result.StopErr = stopErr
			}
			if terminal {
				if result.StopErr == nil {
					out <- agentruntime.Done{}
				}
				finish()
				return
			}
		}
	}
}

// usageModelFromEvent pulls the model id out of the gateway's cumulative usage
// payload. provider.Usage has no model field, so the drain threads it through
// result.Model separately.
func usageModelFromEvent(ev Event) string {
	switch ev.Kind {
	case EventSessionUsage:
		var env usageEnvelope
		if err := json.Unmarshal(ev.Payload, &env); err == nil && env.Usage.Model != "" {
			return strings.TrimSpace(env.Usage.Model)
		}
		var p usagePayload
		if err := json.Unmarshal(ev.Payload, &p); err == nil {
			return strings.TrimSpace(p.Model)
		}
	case EventMessageComplete:
		var p messageCompletePayload
		if err := json.Unmarshal(ev.Payload, &p); err == nil {
			return strings.TrimSpace(p.Usage.Model)
		}
	}
	return ""
}

// streamEndedError distinguishes a clean end of stream (the connection closed
// before the turn completed) from a genuine read error.
func streamEndedError(sess Session) error {
	if err := sess.Err(); err != nil && !cleanStreamEnd(err) {
		return fmt.Errorf("hermes gateway: read failed: %w", err)
	}
	return errors.New("hermes gateway: stream ended before the turn completed")
}

// cleanStreamEnd reports whether a terminal transport error means "the stream
// ended" rather than "the read failed": a normal EOF, a closed socket, or an
// explicit teardown (session deletion / host shutdown) are all ends, not faults.
func cleanStreamEnd(err error) bool {
	return err == nil ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, ErrGatewayClosed) ||
		errors.Is(err, errSessionClosed)
}

// Abort interrupts the in-flight turn via session.interrupt. It is idempotent
// and safe to call concurrently with the drain goroutine.
func (r *Runtime) Abort(ctx context.Context, sessionID int64, turnToken uint64) (agentruntime.AbortOutcome, error) {
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil || a.sess == nil {
		return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
	}
	if turnToken != 0 && a.token.Load() != turnToken {
		return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindNone}, nil
	}
	a.abortRequested.Store(true)
	if err := a.sess.Interrupt(ctx, a.liveSID); err != nil {
		a.abortRequested.Store(false)
		// The connection was already torn down while the map entry lingered: the
		// turn is over, so this is "no in-flight turn", not a stop failure.
		if errors.Is(err, errSessionClosed) {
			return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
		}
		return agentruntime.AbortOutcome{}, err
	}
	return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}, nil
}

// ResolveExecApproval answers a pending Hermes approval card of the session's
// in-flight turn with a normalized decision.
func (r *Runtime) ResolveExecApproval(ctx context.Context, sessionID int64, approvalID, decision string) (agentruntime.ExecApprovalResolution, error) {
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil {
		return agentruntime.ExecApprovalResolution{}, agentruntime.ErrNoActiveTurn
	}
	return a.resolveApproval(ctx, approvalID, decision)
}

// SubmitAnswer answers a pending Hermes clarify card of the session's
// in-flight turn. It implements agentruntime.AskAnswerSink; questions may be
// nil, since this runtime already cached them when it became the waiter (see
// clarify.go's askState).
func (r *Runtime) SubmitAnswer(ctx context.Context, sessionID int64, requestID string, questions []agentruntime.AskQuestion, answers []agentruntime.AskAnswer, skipped bool) error {
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a == nil {
		return agentruntime.ErrNoActiveTurn
	}
	return a.resolveAsk(ctx, requestID, questions, answers, skipped)
}

// CloseSession tears down the gateway connection owning this chat session's turn.
func (r *Runtime) CloseSession(ctx context.Context, sessionID int64) {
	if sessionID <= 0 {
		return
	}
	r.mu.Lock()
	a := r.active[sessionID]
	r.mu.Unlock()
	if a != nil && a.sess != nil {
		if err := a.sess.Close(ctx); err != nil {
			logger.Ctx(ctx).Warn("hermes runtime: close session failed",
				zap.Int64("sessionID", sessionID), zap.Error(err))
		}
	}
}

// CloseAllSessions tears down every in-flight gateway connection (host shutdown).
func (r *Runtime) CloseAllSessions(ctx context.Context) {
	r.mu.Lock()
	owners := make([]*activeTurn, 0, len(r.active))
	for _, a := range r.active {
		owners = append(owners, a)
	}
	r.mu.Unlock()
	for _, a := range owners {
		if a != nil && a.sess != nil {
			if err := a.sess.Close(ctx); err != nil {
				logger.Ctx(ctx).Warn("hermes runtime: close session failed on shutdown", zap.Error(err))
			}
		}
	}
}

func (r *Runtime) register(sessionID int64, a *activeTurn) {
	if sessionID <= 0 {
		return
	}
	r.mu.Lock()
	r.active[sessionID] = a
	r.mu.Unlock()
}

func (r *Runtime) unregister(sessionID int64, owner *activeTurn) {
	if sessionID <= 0 || owner == nil {
		return
	}
	r.mu.Lock()
	if r.active[sessionID] == owner {
		delete(r.active, sessionID)
	}
	r.mu.Unlock()
}
