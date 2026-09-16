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
	sess    Session
	liveSID string
	token   atomic.Uint64

	mu              sync.Mutex
	abortRequested  bool
	cumulativeUsage *provider.Usage
}

var defaultRuntime = New()

func init() {
	agentruntime.RegisterRuntime(agent_backend_entity.TypeHermes, defaultRuntime)
}

// defaultSessionFactory is a package-level variable so the default runtime and
// tests share one dial path; tests construct a Runtime with a fake factory
// instead of mutating the global.
var newSessionFactory SessionFactory = defaultSessionFactory

// New returns a Runtime wired to the real gateway dialer and the process-wide
// credential source.
func New() *Runtime {
	return NewWithCredentials(newSessionFactory, DefaultCredentialSource())
}

// NewWithSessionFactory is the test seam (and the injector the daemon would
// wire later). A nil factory falls back to the real dialer.
func NewWithSessionFactory(factory SessionFactory) *Runtime {
	return NewWithCredentials(factory, DefaultCredentialSource())
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
// session.interrupt. Steer, approvals, permissions, compaction, fork, image
// input and native session reuse are not wired yet and must honestly report
// unsupported.
func (r *Runtime) Capabilities() capability.Capabilities {
	return capability.Capabilities{
		Set: map[capability.Capability]bool{
			capability.CapAbort: true,
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

	active := &activeTurn{sess: sess, liveSID: liveSID, cumulativeUsage: baseline}
	result := &agentruntime.RunResult{ProviderSessionID: storedKey, TurnToken: active.token.Add(1)}
	r.register(req.SessionID, active)
	started = true

	out := make(chan agentruntime.Event, 32)
	go func() {
		defer close(out)
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
	live, stored, err := sess.Create(ctx, cwd, 80)
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
			if active.wasAbortRequested() {
				result.StopErr = agentruntime.ErrAborted
			} else {
				result.StopErr = ctx.Err()
			}
			_ = sess.Interrupt(context.Background(), active.liveSID)
			out <- agentruntime.ErrorEvent{Err: result.StopErr}
			finish()
			return
		case ev, ok := <-sess.Events():
			if !ok {
				if result.StopErr == nil {
					result.StopErr = streamEndedError(sess)
				}
				out <- agentruntime.ErrorEvent{Err: result.StopErr}
				finish()
				return
			}
			events, usage, stopErr := translate(ev)
			if usage != nil {
				delta := usageDelta(usage, active.cumulative())
				active.setCumulative(usage)
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
			if ev.Kind == EventMessageComplete && !textSeen {
				if text := messageCompleteText(ev.Payload); text != "" {
					out <- agentruntime.TextDelta{Text: text}
					textSeen = true
				}
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
			if ev.Kind == EventMessageComplete || ev.Kind == EventError {
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
	a.setAbortRequested(true)
	if err := a.sess.Interrupt(ctx, a.liveSID); err != nil {
		a.setAbortRequested(false)
		// The connection was already torn down while the map entry lingered: the
		// turn is over, so this is "no in-flight turn", not a stop failure.
		if errors.Is(err, errSessionClosed) {
			return agentruntime.AbortOutcome{}, agentruntime.ErrNoActiveTurn
		}
		return agentruntime.AbortOutcome{}, err
	}
	return agentruntime.AbortOutcome{TurnKind: agentruntime.TurnKindUser}, nil
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

func (a *activeTurn) setAbortRequested(v bool) {
	a.mu.Lock()
	a.abortRequested = v
	a.mu.Unlock()
}

func (a *activeTurn) wasAbortRequested() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.abortRequested
}

func (a *activeTurn) setCumulative(u *provider.Usage) {
	a.mu.Lock()
	a.cumulativeUsage = u
	a.mu.Unlock()
}

func (a *activeTurn) cumulative() *provider.Usage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cumulativeUsage
}
