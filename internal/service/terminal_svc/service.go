package terminal_svc

import (
	"context"
	"encoding/base64"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cago-frame/cago/pkg/gogo"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"
)

// Output coalescing: PTY stdout accumulates and is flushed to the emitter at
// most every flushInterval, or sooner once flushThreshold bytes pile up. This
// keeps a high-frequency full-screen TUI (claude, vim) from flooding the Wails
// event bridge with hundreds of tiny events per second — a flood that drops or
// reorders events and desyncs xterm's parser into the garbled output this fixes.
// Mirrors the opskat terminal pipeline.
const (
	flushInterval                    = 10 * time.Millisecond
	flushThreshold                   = 32 * 1024
	detachedCleanupKindPreemptedOpen = "preemptedOpen"
)

var (
	ErrTerminalClosed  = errors.New("terminal closed")
	ErrTerminalNotOpen = errors.New("terminal not open")
)

type Service struct {
	selector             *BackendSelector
	emitter              Emitter
	commandScopeResolver CommandScopeResolver

	mu       sync.Mutex
	sessions map[string]*sessionEntry
	inFlight map[string]*openAttempt // pending starts, keyed by terminalID
}

// sessionEntry is the comparable ownership token for one live terminal. Handle
// itself is intentionally never compared because an interface may contain a
// valid non-comparable dynamic value.
//
// Lock contract: Service.mu only protects ownership maps. terminationMu
// serializes one entry's Close/final-retirement authority without blocking data
// emissions. Retirement may mark active=false while Service.mu is held, but it
// waits for emissionMu only after releasing Service.mu. Handle.Close, lifecycle
// logging, and emitter boundaries therefore never run under Service.mu.
type sessionEntry struct {
	ctx       context.Context
	handle    pty.Handle
	lifecycle *commandLifecycle

	terminationMu sync.Mutex
	emissionMu    sync.Mutex
	active        atomic.Bool
}

func newSessionEntry(ctx context.Context, handle pty.Handle, lifecycle *commandLifecycle) *sessionEntry {
	entryCtx := context.WithoutCancel(ctx)
	if lifecycle != nil {
		entryCtx = lifecycle.ctx
	}
	entry := &sessionEntry{ctx: entryCtx, handle: handle, lifecycle: lifecycle}
	entry.active.Store(true)
	return entry
}

func (e *sessionEntry) beginRetirement() {
	e.active.Store(false)
}

func (e *sessionEntry) drainRetirement() {
	e.emissionMu.Lock()
	e.active.Store(false)
	e.emissionMu.Unlock()
}

func (e *sessionEntry) logCommandRetirement(exitReason string) {
	if e.lifecycle != nil {
		e.lifecycle.logExited(commandExitCodeUnavailable, exitReason)
	}
}

func (e *sessionEntry) logShutdownCloseFailure() {
	if e.lifecycle != nil {
		e.lifecycle.logShutdownCloseFailure()
	}
}

// retireCurrentSession requires entry.terminationMu. It claims final authority
// only for the exact active entry, drains emissions outside Service.mu, logs the
// retirement milestone once, and detaches only that ownership token.
func (s *Service) retireCurrentSession(terminalID string, entry *sessionEntry, exitReason string) bool {
	s.mu.Lock()
	current := s.sessions[terminalID] == entry && entry.active.Load()
	if current {
		entry.beginRetirement()
	}
	s.mu.Unlock()
	if !current {
		return false
	}

	entry.drainRetirement()
	entry.logCommandRetirement(exitReason)
	s.mu.Lock()
	if s.sessions[terminalID] == entry {
		delete(s.sessions, terminalID)
	}
	s.mu.Unlock()
	return true
}

func (e *sessionEntry) emitIfActive(emit func()) bool {
	e.emissionMu.Lock()
	defer e.emissionMu.Unlock()
	if !e.active.Load() {
		return false
	}
	emit()
	return true
}

func (e *sessionEntry) emitFinalIfActive(emit func()) bool {
	e.emissionMu.Lock()
	defer e.emissionMu.Unlock()
	if !e.active.Load() {
		return false
	}
	emit()
	e.active.Store(false)
	return true
}

// openAttempt owns one terminal start continuously from the first blocking
// boundary through backend.Open registration. Close or a newer start cancels
// it and removes/replaces its map entry; pointer identity rejects every stale
// result returned by a cancellation-ignoring dependency.
type openAttempt struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func NewService(sel *BackendSelector, emitter Emitter) *Service {
	if emitter == nil {
		emitter = NoopEmitter{}
	}
	return &Service{
		selector: sel,
		emitter:  emitter,
		sessions: map[string]*sessionEntry{},
		inFlight: map[string]*openAttempt{},
	}
}

// Open opens an interactive login shell (original behavior).
func (s *Service) Open(ctx context.Context, terminalID string, deviceID string, cwd string, cols, rows uint16) error {
	attempt := s.claimStart(ctx, terminalID)
	defer s.releaseStart(terminalID, attempt)
	return s.open(ctx, attempt, terminalID, deviceID, pty.Spec{Cwd: cwd, Cols: cols, Rows: rows}, nil, false)
}

// OpenCommand runs a one-shot command under cwd, reusing the same
// streaming/exit/kill machinery as Open.
func (s *Service) OpenCommand(ctx context.Context, terminalID string, deviceID string, cwd string, command string, cols, rows uint16) error {
	attempt := s.claimStart(ctx, terminalID)
	defer s.releaseStart(terminalID, attempt)
	return s.openCommand(ctx, attempt, terminalID, deviceID, cwd, command, cols, rows, nil)
}

func (s *Service) openCommand(
	ctx context.Context,
	attempt *openAttempt,
	terminalID string,
	deviceID string,
	cwd string,
	command string,
	cols, rows uint16,
	lifecycle *commandLifecycle,
) error {
	return s.open(ctx, attempt, terminalID, deviceID, pty.Spec{
		Cwd: cwd, Command: command, Cols: cols, Rows: rows,
	}, lifecycle, true)
}

func (s *Service) open(
	ctx context.Context,
	attempt *openAttempt,
	terminalID string,
	deviceID string,
	spec pty.Spec,
	lifecycle *commandLifecycle,
	annotateStartFailure bool,
) error {
	backend, err := s.selector.Pick(deviceID)
	if !s.ownsStart(terminalID, attempt) {
		return preemptedStartError(lifecycle)
	}
	if err != nil {
		if annotateStartFailure {
			return annotateCommandStartError(commandStartStageBackendSelect, err)
		}
		return err
	}

	// A replacement has no retirement authority until the old Handle.Close is
	// confirmed. Keep the exact old entry current and active while Close blocks
	// or fails, so its output remains valid and a failed close can be retried.
	s.mu.Lock()
	if s.inFlight[terminalID] != attempt {
		s.mu.Unlock()
		return preemptedStartError(lifecycle)
	}
	old := s.sessions[terminalID]
	s.mu.Unlock()
	if old != nil {
		old.terminationMu.Lock()
		s.mu.Lock()
		preempted := s.inFlight[terminalID] != attempt
		current := s.sessions[terminalID] == old && old.active.Load()
		s.mu.Unlock()
		if preempted {
			old.terminationMu.Unlock()
			return preemptedStartError(lifecycle)
		}
		if current {
			closeErr := old.handle.Close()
			if closeErr != nil {
				preempted = !s.ownsStart(terminalID, attempt)
				old.terminationMu.Unlock()
				if preempted {
					return preemptedStartError(lifecycle)
				}
				if annotateStartFailure {
					return annotateCommandStartError(commandStartStageReplacementClose, closeErr)
				}
				return closeErr
			}

			s.retireCurrentSession(terminalID, old, commandExitReasonReplaced)
		}
		preempted = !s.ownsStart(terminalID, attempt)
		old.terminationMu.Unlock()
		if preempted {
			return preemptedStartError(lifecycle)
		}
	}

	// The already-allocated desktop identity must reach a remote backend before
	// terminal.open so it can subscribe and cancel under that same ID. Local
	// backends intentionally ignore this runtime-only field.
	spec.TerminalID = terminalID
	h, err := backend.Open(attempt.ctx, spec)
	var entry *sessionEntry
	if err == nil {
		entry = newSessionEntry(ctx, h, lifecycle)
	}

	// Atomically hand ownership from the start attempt to one live session
	// entry. A stale handle returned by a cancellation-ignoring backend is never
	// registered and therefore never gets a listener/pump.
	s.mu.Lock()
	preempted := s.inFlight[terminalID] != attempt
	if !preempted {
		delete(s.inFlight, terminalID)
		if err == nil {
			s.sessions[terminalID] = entry
		}
	}
	s.mu.Unlock()

	if err != nil {
		if preempted && lifecycle != nil {
			return ErrCommandStartPreempted
		}
		if annotateStartFailure {
			return annotateCommandStartError(commandStartStagePTYOpen, err)
		}
		return err
	}
	if preempted {
		// Close already returned to the caller, so it never saw this handle.
		// A failed first close transfers the exact handle to one detached
		// guardian, which drains output and retains any remote lease until close
		// retry or natural exit supplies cleanup authority.
		if closeErr := h.Close(); closeErr != nil {
			guardianCtx := context.WithoutCancel(ctx)
			logger.Ctx(guardianCtx).Warn("terminal_svc.open: detached cleanup guardian started",
				zap.String("terminalId", terminalID),
				zap.String("deviceId", deviceID),
				zap.String("cleanupKind", detachedCleanupKindPreemptedOpen))
			pty.StartDetachedCleanup(h, func(outcome pty.DetachedCleanupOutcome) {
				logger.Ctx(guardianCtx).Info("terminal_svc.open: detached cleanup guardian settled",
					zap.String("terminalId", terminalID),
					zap.String("deviceId", deviceID),
					zap.String("cleanupKind", detachedCleanupKindPreemptedOpen),
					zap.String("outcome", string(outcome)))
			})
		}
		return preemptedStartError(lifecycle)
	}
	// Log before starting the pump so even an already-exited handle preserves
	// the command lifecycle order. The entry gate also suppresses this start if
	// a replacement retired the just-registered handle first.
	if lifecycle != nil {
		entry.emitIfActive(lifecycle.logStarted)
	}
	gogo.Go(func() error {
		s.pump(terminalID, entry)
		return nil
	}, gogo.WithIgnorePanic())
	return nil
}

func (s *Service) claimStart(ctx context.Context, terminalID string) *openAttempt {
	attemptCtx, cancel := context.WithCancel(ctx)
	attempt := &openAttempt{ctx: attemptCtx, cancel: cancel}

	s.mu.Lock()
	previous := s.inFlight[terminalID]
	s.inFlight[terminalID] = attempt
	s.mu.Unlock()

	if previous != nil {
		previous.cancel()
	}
	return attempt
}

func (s *Service) ownsStart(terminalID string, attempt *openAttempt) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight[terminalID] == attempt
}

func (s *Service) releaseStart(terminalID string, attempt *openAttempt) {
	s.mu.Lock()
	if s.inFlight[terminalID] == attempt {
		delete(s.inFlight, terminalID)
	}
	s.mu.Unlock()
	attempt.cancel()
}

func preemptedStartError(lifecycle *commandLifecycle) error {
	if lifecycle != nil {
		return ErrCommandStartPreempted
	}
	return nil
}

func (s *Service) Write(ctx context.Context, terminalID string, data string) error {
	h := s.lookupHandle(terminalID)
	if h == nil {
		return ErrTerminalClosed
	}
	_, err := h.Write([]byte(data))
	return err
}

func (s *Service) Resize(ctx context.Context, terminalID string, cols, rows uint16) error {
	h := s.lookupHandle(terminalID)
	if h == nil {
		return ErrTerminalClosed
	}
	return h.Resize(cols, rows)
}

func (s *Service) Close(ctx context.Context, terminalID string) error {
	s.mu.Lock()
	attempt, hadInFlight := s.inFlight[terminalID]
	if hadInFlight {
		delete(s.inFlight, terminalID)
	}
	entry, hadHandle := s.sessions[terminalID]
	activeHandle := hadHandle && entry.active.Load()
	s.mu.Unlock()

	if hadInFlight {
		attempt.cancel() // preempt the in-flight Open
	}
	if !hadHandle && !hadInFlight {
		return ErrTerminalNotOpen
	}
	if activeHandle {
		entry.terminationMu.Lock()
		s.mu.Lock()
		current := s.sessions[terminalID] == entry && entry.active.Load()
		s.mu.Unlock()
		if !current {
			entry.terminationMu.Unlock()
			return nil
		}
		if err := entry.handle.Close(); err != nil {
			entry.terminationMu.Unlock()
			return err
		}
		// A failed Close leaves the current entry active for output and retry. A
		// confirmed Close retires only the exact entry still owning this ID, then
		// drains any emission already in progress before detaching it.
		s.retireCurrentSession(terminalID, entry, commandExitReasonStopped)
		entry.terminationMu.Unlock()
	}
	return nil // only inFlight was canceled, or the captured entry settled
}

func (s *Service) Shutdown() {
	type ownedSession struct {
		terminalID string
		entry      *sessionEntry
	}

	s.mu.Lock()
	sessions := make([]ownedSession, 0, len(s.sessions))
	for terminalID, entry := range s.sessions {
		sessions = append(sessions, ownedSession{terminalID: terminalID, entry: entry})
	}
	// Clear and cancel in-flight starts too: clearing inFlight makes each pending
	// start observe itself as preempted (so stale resolver/selector results stop,
	// and a late handle is torn down instead of registered). Cancellation also
	// unblocks context-aware resolver and backend boundaries.
	attempts := make([]*openAttempt, 0, len(s.inFlight))
	for _, a := range s.inFlight {
		attempts = append(attempts, a)
	}
	s.inFlight = map[string]*openAttempt{}
	s.mu.Unlock()
	for _, a := range attempts {
		a.cancel()
	}
	for _, session := range sessions {
		entry := session.entry
		entry.terminationMu.Lock()
		s.mu.Lock()
		current := s.sessions[session.terminalID] == entry && entry.active.Load()
		s.mu.Unlock()
		if !current {
			entry.terminationMu.Unlock()
			continue
		}
		if err := entry.handle.Close(); err != nil {
			entry.logShutdownCloseFailure()
			entry.terminationMu.Unlock()
			continue
		}
		s.retireCurrentSession(session.terminalID, entry, commandExitReasonShutdown)
		entry.terminationMu.Unlock()
	}
}

func (s *Service) lookupHandle(terminalID string) pty.Handle {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.sessions[terminalID]
	if entry == nil || !entry.active.Load() {
		return nil
	}
	return entry.handle
}

func (s *Service) pump(terminalID string, entry *sessionEntry) {
	ctx := entry.ctx
	// Data() and Exit() are independent channels with no ordering guarantee
	// between them. We must drain every data chunk AND read the single exit
	// value before emitting the exit event — otherwise a naive select that
	// returns on a closed Data() channel races the buffered Exit() value and
	// drops the exit ~50% of the time (terminal stuck "open"), or returns on
	// Exit() while data is still buffered and drops the trailing output.
	dataCh := entry.handle.Data()
	exitCh := entry.handle.Exit()

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	var pending []byte
	flush := func() {
		if len(pending) == 0 {
			return
		}
		entry.emitIfActive(func() {
			s.emitter.Emit(ctx, DataEventName(terminalID),
				map[string]string{"data": base64.StdEncoding.EncodeToString(pending)})
		})
		pending = pending[:0]
	}

	var exitInfo pty.ExitInfo
stream:
	for {
		select {
		case data, ok := <-dataCh:
			if !ok {
				// Data closed before we observed exit; flush trailing output,
				// then block for the single exit value (real handles always
				// deliver it).
				flush()
				exitInfo = <-exitCh
				break stream
			}
			pending = append(pending, data...)
			if len(pending) >= flushThreshold {
				flush()
			}
		case <-ticker.C:
			flush()
		case info := <-exitCh:
			exitInfo = info
			// Drain any already-buffered data so trailing output is flushed
			// before the exit event.
			for drained := false; !drained; {
				select {
				case data, ok := <-dataCh:
					if !ok {
						drained = true
					} else {
						pending = append(pending, data...)
					}
				default:
					drained = true
				}
			}
			break stream
		}
	}
	// Flush whatever remains so no trailing output arrives after the exit event.
	flush()

	// Lifecycle finish and terminal exit are one generation boundary. A
	// replacement either waits for both to finish or retires this entry before
	// either starts; it can never observe only half of the old final sequence.
	entry.terminationMu.Lock()
	finished := entry.emitFinalIfActive(func() {
		if entry.lifecycle != nil {
			entry.lifecycle.logExited(exitInfo.Code, exitInfo.Reason)
		}
		s.emitter.Emit(ctx, ExitEventName(terminalID), protocol.TerminalExitEvent{
			Code: exitInfo.Code, Reason: exitInfo.Reason, Msg: exitInfo.Msg,
		})
	})
	if finished {
		s.mu.Lock()
		if s.sessions[terminalID] == entry {
			delete(s.sessions, terminalID)
		}
		s.mu.Unlock()
	}
	entry.terminationMu.Unlock()
}
