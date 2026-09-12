// internal/daemon/handlers/terminal.go
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"
)

//go:generate mockgen -source=terminal.go -destination=mock_handlers/mock_terminal.go -package=mock_handlers

// PTYBackend / PTYHandle are named ports the daemon side speaks to. They
// mirror internal/pkg/pty.{Backend,Handle} but are declared here so
// mockgen can produce local mocks without crossing package boundaries.
type PTYBackend interface {
	Open(ctx context.Context, spec pty.Spec) (PTYHandle, error)
}

type PTYHandle interface {
	Write(p []byte) (int, error)
	Resize(cols, rows uint16) error
	Close() error
	Data() <-chan []byte
	Exit() <-chan pty.ExitInfo
}

// Emitter is the daemon's push-event sink.
type Emitter interface {
	Emit(ctx context.Context, name string, payload any)
}

// TerminalDataEvent is the transport-neutral daemon event. Data remains raw
// bytes from the PTY producer through the Protobuf transport.
type TerminalDataEvent struct {
	TerminalID string `json:"terminalId"`
	Data       []byte `json:"data"`
}

const (
	EventNameTerminalData = "terminal.data"
	EventNameTerminalExit = "terminal.exit"

	maxTerminalIDLength             = 128
	terminalCancelTombstoneCapacity = 256
	detachedCleanupKindLateOpen     = "lateOpen"
)

var (
	ErrTerminalNotFound       = errors.New("terminal not found")
	ErrTerminalIDInvalid      = errors.New("terminal id is invalid")
	ErrTerminalIDInUse        = errors.New("terminal id is already active or pending")
	ErrTerminalOpenCanceled   = errors.New("terminal open canceled")
	ErrTerminalHandlerClosed  = errors.New("terminal handler is closed")
	ErrTerminalCancelCapacity = errors.New("terminal pending cancellation capacity reached")
)

type pendingTerminalOpen struct {
	ctx      context.Context
	cancel   context.CancelFunc
	canceled bool
}

// terminalEntry is the identity of one active registration. The pointer, not
// the PTYHandle interface value, owns cleanup because a handle's dynamic value
// is not required to be comparable.
type terminalEntry struct {
	handle  PTYHandle
	eventMu sync.Mutex
}

type TerminalHandlers struct {
	be      PTYBackend
	emitter Emitter

	mu               sync.Mutex
	terminals        map[string]*terminalEntry
	pending          map[string]*pendingTerminalOpen
	cancelTombstones map[string]struct{}
	closed           bool
}

func NewTerminalHandlers(be PTYBackend, emitter Emitter) *TerminalHandlers {
	return &TerminalHandlers{
		be:               be,
		emitter:          emitter,
		terminals:        map[string]*terminalEntry{},
		pending:          map[string]*pendingTerminalOpen{},
		cancelTombstones: map[string]struct{}{},
	}
}

func (h *TerminalHandlers) Open(ctx context.Context, p protocol.TerminalOpenParams) (protocol.TerminalOpenResult, error) {
	id := p.TerminalID
	if id == "" {
		id = newTerminalID()
	} else if err := validateTerminalID(id); err != nil {
		return protocol.TerminalOpenResult{}, err
	}

	attempt, err := h.claimPendingOpen(ctx, id)
	if err != nil {
		return protocol.TerminalOpenResult{}, err
	}
	hd, openErr := h.be.Open(attempt.ctx, pty.Spec{
		Cwd: p.Cwd, Shell: p.Shell, Command: p.Command, Env: p.Env,
		Cols: p.Cols, Rows: p.Rows,
	})

	h.mu.Lock()
	current, stillPending := h.pending[id]
	ownsClaim := stillPending && current == attempt
	if ownsClaim {
		delete(h.pending, id)
	}
	canceled := attempt.canceled
	closed := h.closed
	openCtxErr := attempt.ctx.Err()
	register := openErr == nil && hd != nil && ownsClaim && !canceled && !closed && openCtxErr == nil
	var entry *terminalEntry
	if register {
		entry = &terminalEntry{handle: hd}
		h.terminals[id] = entry
	}
	h.mu.Unlock()
	attempt.cancel()

	if register {
		go h.pump(ctx, id, entry)
		return protocol.TerminalOpenResult{TerminalID: id}, nil
	}
	if hd != nil {
		if closeErr := hd.Close(); closeErr != nil {
			guardianCtx := context.WithoutCancel(ctx)
			logger.Ctx(guardianCtx).Warn("handlers.TerminalHandlers.Open: detached cleanup guardian started",
				zap.String("terminalId", id),
				zap.String("cleanupKind", detachedCleanupKindLateOpen))
			pty.StartDetachedCleanup(hd, func(outcome pty.DetachedCleanupOutcome) {
				logger.Ctx(guardianCtx).Info("handlers.TerminalHandlers.Open: detached cleanup guardian settled",
					zap.String("terminalId", id),
					zap.String("cleanupKind", detachedCleanupKindLateOpen),
					zap.String("outcome", string(outcome)))
			})
		}
	}
	switch {
	case closed:
		return protocol.TerminalOpenResult{}, ErrTerminalHandlerClosed
	case canceled || !ownsClaim:
		return protocol.TerminalOpenResult{}, ErrTerminalOpenCanceled
	case openErr != nil:
		return protocol.TerminalOpenResult{}, openErr
	case openCtxErr != nil:
		return protocol.TerminalOpenResult{}, openCtxErr
	default:
		return protocol.TerminalOpenResult{}, ErrTerminalOpenCanceled
	}
}

func newTerminalID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (h *TerminalHandlers) claimPendingOpen(ctx context.Context, id string) (*pendingTerminalOpen, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrTerminalHandlerClosed
	}
	if _, canceled := h.cancelTombstones[id]; canceled {
		delete(h.cancelTombstones, id)
		return nil, ErrTerminalOpenCanceled
	}
	if _, active := h.terminals[id]; active {
		return nil, ErrTerminalIDInUse
	}
	if _, pending := h.pending[id]; pending {
		return nil, ErrTerminalIDInUse
	}
	openCtx, cancel := context.WithCancel(ctx)
	attempt := &pendingTerminalOpen{ctx: openCtx, cancel: cancel}
	h.pending[id] = attempt
	return attempt, nil
}

func validateTerminalID(id string) error {
	if len(id) == 0 || len(id) > maxTerminalIDLength || !isTerminalIDAlphaNumeric(id[0]) {
		return ErrTerminalIDInvalid
	}
	for i := 1; i < len(id); i++ {
		if !isTerminalIDAlphaNumeric(id[i]) && id[i] != '-' && id[i] != '_' {
			return ErrTerminalIDInvalid
		}
	}
	return nil
}

func isTerminalIDAlphaNumeric(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9'
}

func (h *TerminalHandlers) pump(ctx context.Context, id string, entry *terminalEntry) {
	// 256-cap buffered channel: pump reads from entry.handle.Data() and forwards to
	// this queue. If full, drop the oldest chunk, insert a throttle marker,
	// then enqueue the new chunk. Avoids blocking PTY stdout under
	// bursty/slow-consumer load.
	const bufCap = 256
	queue := make(chan []byte, bufCap)
	throttleMarker := []byte("\r\n[--- output throttled ---]\r\n")

	// forwarder goroutine: drains queue → emitter.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for data := range queue {
			// Preserve PTY chunks as raw bytes. A multibyte UTF-8 rune may span
			// reads and must not be decoded at this boundary.
			h.emitTerminalData(ctx, id, entry, data)
		}
	}()

	enqueue := func(data []byte) {
		select {
		case queue <- data:
			// enqueued normally
		default:
			// Queue full: drop oldest, insert marker, then enqueue current.
			select {
			case <-queue:
			default:
			}
			// Push marker (non-blocking; a racing consumer may have already
			// taken the freed slot — silently drop marker if still full).
			select {
			case queue <- throttleMarker:
			default:
			}
			// Try to enqueue the current chunk; drop if still full.
			select {
			case queue <- data:
			default:
			}
		}
	}

	// Data() and Exit() are independent channels with no ordering guarantee.
	// Drain every data chunk AND read the single exit value before tearing
	// down — a naive select that returns on a closed Data() channel races the
	// buffered Exit() value and drops the exit ~50% of the time (remote
	// terminal stuck "open"), or returns on Exit() while data is still
	// buffered and drops the trailing output.
	dataCh := entry.handle.Data()
	exitCh := entry.handle.Exit()
	var exitInfo pty.ExitInfo
stream:
	for {
		select {
		case data, ok := <-dataCh:
			if !ok {
				// Data closed before we observed exit; block for the single
				// exit value (real handles always deliver it).
				exitInfo = <-exitCh
				break stream
			}
			enqueue(data)
		case info := <-exitCh:
			exitInfo = info
			// Drain any already-buffered data so trailing output is queued
			// before the exit event.
			for drained := false; !drained; {
				select {
				case data, ok := <-dataCh:
					if !ok {
						drained = true
					} else {
						enqueue(data)
					}
				default:
					drained = true
				}
			}
			break stream
		}
	}

	// Flush all queued data through the forwarder before emitting exit so
	// trailing output never arrives after the exit event.
	close(queue)
	<-done

	h.emitTerminalExitAndDetach(ctx, id, entry, exitInfo)
}

// emitTerminalData serializes event publication with successful Close. Once
// Close detaches the entry, queued old data cannot be attributed to a
// replacement reusing the stable ID.
func (h *TerminalHandlers) emitTerminalData(ctx context.Context, id string, entry *terminalEntry, data []byte) {
	entry.eventMu.Lock()
	defer entry.eventMu.Unlock()

	h.mu.Lock()
	owned := h.terminals[id] == entry
	h.mu.Unlock()
	if !owned {
		return
	}
	h.emitter.Emit(ctx, EventNameTerminalData, TerminalDataEvent{
		TerminalID: id, Data: append([]byte(nil), data...),
	})
}

// emitTerminalExitAndDetach keeps the entry registered until its exit event is
// published. Therefore an old exit is either published before the stable ID is
// reusable, or suppressed after Close detached it; it can never target a newer
// registration with the same ID.
func (h *TerminalHandlers) emitTerminalExitAndDetach(
	ctx context.Context,
	id string,
	entry *terminalEntry,
	exitInfo pty.ExitInfo,
) {
	entry.eventMu.Lock()
	defer entry.eventMu.Unlock()

	h.mu.Lock()
	owned := h.terminals[id] == entry
	h.mu.Unlock()
	if !owned {
		return
	}
	h.emitter.Emit(ctx, EventNameTerminalExit, protocol.TerminalExitEvent{
		TerminalID: id, Code: exitInfo.Code, Reason: exitInfo.Reason, Msg: exitInfo.Msg,
	})

	h.mu.Lock()
	if h.terminals[id] == entry {
		delete(h.terminals, id)
	}
	h.mu.Unlock()
}

type TerminalAck struct{}

func (h *TerminalHandlers) Write(ctx context.Context, p protocol.TerminalWriteParams) (TerminalAck, error) {
	h.mu.Lock()
	entry, ok := h.terminals[p.TerminalID]
	h.mu.Unlock()
	if !ok {
		return TerminalAck{}, ErrTerminalNotFound
	}
	_, err := entry.handle.Write([]byte(p.Data))
	return TerminalAck{}, err
}

func (h *TerminalHandlers) Resize(ctx context.Context, p protocol.TerminalResizeParams) (TerminalAck, error) {
	h.mu.Lock()
	entry, ok := h.terminals[p.TerminalID]
	h.mu.Unlock()
	if !ok {
		return TerminalAck{}, ErrTerminalNotFound
	}
	return TerminalAck{}, entry.handle.Resize(p.Cols, p.Rows)
}

// CloseAll terminates every live PTY and pending open owned by this connection.
func (h *TerminalHandlers) CloseAll() {
	h.mu.Lock()
	h.closed = true
	hs := make([]PTYHandle, 0, len(h.terminals))
	for _, entry := range h.terminals {
		hs = append(hs, entry.handle)
	}
	cancels := make([]context.CancelFunc, 0, len(h.pending))
	for _, attempt := range h.pending {
		attempt.canceled = true
		cancels = append(cancels, attempt.cancel)
	}
	h.terminals = map[string]*terminalEntry{}
	h.pending = map[string]*pendingTerminalOpen{}
	h.cancelTombstones = map[string]struct{}{}
	h.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
	for _, hd := range hs {
		_ = hd.Close()
	}
}

func (h *TerminalHandlers) Close(ctx context.Context, p protocol.TerminalCloseParams) (TerminalAck, error) {
	if p.CancelPendingOpen {
		if err := validateTerminalID(p.TerminalID); err != nil {
			return TerminalAck{}, err
		}
	}

	h.mu.Lock()
	if entry, ok := h.terminals[p.TerminalID]; ok {
		h.mu.Unlock()
		if err := entry.handle.Close(); err != nil {
			return TerminalAck{}, err
		}

		entry.eventMu.Lock()
		h.mu.Lock()
		if h.terminals[p.TerminalID] == entry {
			delete(h.terminals, p.TerminalID)
		}
		h.mu.Unlock()
		entry.eventMu.Unlock()
		return TerminalAck{}, nil
	}
	if !p.CancelPendingOpen {
		h.mu.Unlock()
		return TerminalAck{}, ErrTerminalNotFound
	}
	if h.closed {
		h.mu.Unlock()
		return TerminalAck{}, ErrTerminalHandlerClosed
	}
	if attempt, ok := h.pending[p.TerminalID]; ok {
		if !attempt.canceled {
			attempt.canceled = true
			cancel := attempt.cancel
			h.mu.Unlock()
			cancel()
			return TerminalAck{}, nil
		}
		h.mu.Unlock()
		return TerminalAck{}, nil
	}
	if _, ok := h.cancelTombstones[p.TerminalID]; ok {
		h.mu.Unlock()
		return TerminalAck{}, nil
	}
	if len(h.cancelTombstones) >= terminalCancelTombstoneCapacity {
		h.mu.Unlock()
		return TerminalAck{}, ErrTerminalCancelCapacity
	}
	h.cancelTombstones[p.TerminalID] = struct{}{}
	h.mu.Unlock()
	return TerminalAck{}, nil
}
