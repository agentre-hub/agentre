// Package remote implements pty.Backend by relaying ops over an agentred
// binary Protobuf RPC client on WebSocket.
package remote

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	"github.com/agentre-hub/agentre/internal/daemon/protorpc"
	pkgpty "github.com/agentre-hub/agentre/internal/pkg/pty"
	"github.com/agentre-hub/agentre/pkg/agentred/protocol"
)

const (
	openTimeout              = 5 * time.Second
	openCleanupTimeout       = time.Second
	openCleanupRetryInterval = 50 * time.Millisecond
	// terminalOperationTimeout bounds small terminal mutations on the shared
	// LAN RPC connection without treating ordinary short network jitter as a
	// failed connection.
	terminalOperationTimeout = 5 * time.Second
)

// ErrDaemonTimeout is returned by Backend.Open when agentred does not respond
// within openTimeout.
var (
	ErrDaemonTimeout      error = daemonTimeoutError{}
	ErrTerminalIDMismatch       = errors.New("agentred returned a mismatched terminal id")
	errOpenTimeout              = errors.New("remote terminal open timeout")
)

type daemonTimeoutError struct{}

func (daemonTimeoutError) Error() string   { return "agentred did not respond within 5s" }
func (daemonTimeoutError) Timeout() bool   { return true }
func (daemonTimeoutError) Temporary() bool { return true }

// Subscription is one atomic terminal event generation. Data and Exit always
// come from the same registration and remain the exact references consumed by
// the handle pump.
type Subscription struct {
	Data <-chan protocol.TerminalDataEvent
	Exit <-chan protocol.TerminalExitEvent
}

// Client is the minimal subset of the agentred ws client surface needed here.
// Abort reports whether the shared-connection safety fallback was initiated.
type Client interface {
	Call(ctx context.Context, method string, params any, out any) error
	Subscribe(terminalID string) Subscription
	Unsubscribe(terminalID string, subscription Subscription)
	Abort() error
}

// closedClient is the narrow connection-lifecycle extension used by cleanup
// guardians. ClientAdapter supplies it in production; basic Client fakes and
// compatibility callers need not implement it when Abort is authoritative.
type closedClient interface {
	Closed() <-chan struct{}
}

type openCleanupKind string

const (
	openCleanupPendingOpen    openCleanupKind = "pendingOpen"
	openCleanupMismatchedOpen openCleanupKind = "mismatchedOpen"
)

type cleanupAuthorityOutcome string

const (
	cleanupAuthorityPending           cleanupAuthorityOutcome = ""
	cleanupAuthorityCloseAcknowledged cleanupAuthorityOutcome = "closeAcknowledged"
	cleanupAuthorityConnectionAborted cleanupAuthorityOutcome = "connectionAborted"
	cleanupAuthorityConnectionClosed  cleanupAuthorityOutcome = "connectionClosed"
)

type Backend struct {
	client           Client
	release          func()
	operationTimeout time.Duration
}

func NewBackend(c Client) *Backend {
	return newBackend(c, nil, terminalOperationTimeout)
}

// NewBackendWithLease binds one successful daemon-client borrow to one Open.
// Authoritatively rejected opens release immediately; uncertain interrupted or
// mismatched opens retain the lease until cleanup ownership is confirmed. A
// successful handle releases when its terminal outcome is settled. The release
// function is guarded exactly once.
func NewBackendWithLease(c Client, release func()) *Backend {
	return newBackend(c, release, terminalOperationTimeout)
}

func newBackend(c Client, release func(), operationTimeout time.Duration) *Backend {
	return &Backend{client: c, release: release, operationTimeout: operationTimeout}
}

func (b *Backend) Open(ctx context.Context, spec pkgpty.Spec) (pkgpty.Handle, error) {
	release := onceRelease(b.release)
	terminalID, err := terminalIDForOpen(spec.TerminalID)
	if err != nil {
		release()
		return nil, err
	}

	// Register both event channels under the stable desktop identity before the
	// request can make agentred spawn or emit anything.
	subscription := b.client.Subscribe(terminalID)
	settleFailure := sync.OnceFunc(func() {
		b.client.Unsubscribe(terminalID, subscription)
		release()
	})

	openCtx, cancel := context.WithTimeoutCause(ctx, openTimeout, errOpenTimeout)
	defer cancel()
	var res protocol.TerminalOpenResult
	err = b.client.Call(openCtx, "terminal.open", protocol.TerminalOpenParams{
		TerminalID: terminalID,
		Cwd:        spec.Cwd,
		Shell:      spec.Shell,
		Command:    spec.Command,
		Env:        spec.Env,
		Cols:       spec.Cols,
		Rows:       spec.Rows,
	}, &res)
	if err != nil {
		if returned, interrupted := interruptedOpenError(ctx, openCtx, err); interrupted {
			params := protocol.TerminalCloseParams{
				TerminalID:        terminalID,
				CancelPendingOpen: true,
			}
			b.settleFailedOpen(ctx, terminalID, openCleanupPendingOpen, &params, settleFailure)
			return nil, returned
		}
		// A generic terminal.open RPC error is an authoritative rejection: no
		// pending PTY needs an extra cancellation request.
		settleFailure()
		return nil, err
	}
	if res.TerminalID != terminalID {
		mismatchErr := fmt.Errorf(
			"%w: expected %q, got %q",
			ErrTerminalIDMismatch,
			terminalID,
			res.TerminalID,
		)
		var params *protocol.TerminalCloseParams
		if res.TerminalID != "" {
			params = &protocol.TerminalCloseParams{TerminalID: res.TerminalID}
		}
		b.settleFailedOpen(ctx, terminalID, openCleanupMismatchedOpen, params, settleFailure)
		return nil, mismatchErr
	}

	h := &handleImpl{
		client:           b.client,
		terminalID:       terminalID,
		subscription:     subscription,
		data:             make(chan []byte, 32),
		exit:             make(chan pkgpty.ExitInfo, 1),
		done:             make(chan struct{}),
		release:          release,
		operationTimeout: b.operationTimeout,
	}
	go h.pump()
	return h, nil
}

func terminalIDForOpen(supplied string) (string, error) {
	if supplied != "" {
		return supplied, nil
	}
	var id [12]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("generate remote terminal id: %w", err)
	}
	return hex.EncodeToString(id[:]), nil
}

func interruptedOpenError(
	ctx context.Context,
	openCtx context.Context,
	callErr error,
) (error, bool) {
	// A typed RPC error frame is an authoritative terminal.open rejection even
	// if caller cancellation raced its arrival. Transport/context failures have
	// no such authority and must preserve the desktop interruption outcome.
	var rpcErr *protorpc.Error
	if errors.As(callErr, &rpcErr) {
		return nil, false
	}
	if errors.Is(context.Cause(openCtx), errOpenTimeout) {
		return ErrDaemonTimeout, true
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr, true
	}
	if openCtx.Err() != nil &&
		(errors.Is(callErr, context.Canceled) || errors.Is(callErr, context.DeadlineExceeded)) {
		return callErr, true
	}
	return nil, false
}

func (b *Backend) settleFailedOpen(
	ctx context.Context,
	terminalID string,
	cleanupKind openCleanupKind,
	params *protocol.TerminalCloseParams,
	settle func(),
) {
	guardianCtx := context.WithoutCancel(ctx)
	if outcome := b.cleanupTerminal(guardianCtx, params); outcome != cleanupAuthorityPending {
		settle()
		return
	}
	logger.Ctx(guardianCtx).Warn("remote.Backend.Open: cleanup guardian started",
		zap.String("terminalId", terminalID),
		zap.String("cleanupKind", string(cleanupKind)))
	go b.guardOpenCleanup(guardianCtx, terminalID, cleanupKind, params, settle)
}

// cleanupTerminal reports the bounded authority outcome for remote terminal
// ownership. A terminal.close acknowledgement, successful shared-connection
// Abort, or observed connection closure each transfers cleanup authority.
func (b *Backend) cleanupTerminal(
	ctx context.Context,
	params *protocol.TerminalCloseParams,
) cleanupAuthorityOutcome {
	closed := clientClosed(b.client)
	if channelClosed(closed) {
		return cleanupAuthorityConnectionClosed
	}
	if params != nil {
		cleanupCtx, cancel := context.WithTimeout(ctx, openCleanupTimeout)
		var ack struct{}
		err := b.client.Call(cleanupCtx, "terminal.close", *params, &ack)
		cancel()
		if err == nil {
			return cleanupAuthorityCloseAcknowledged
		}
		if channelClosed(closed) {
			return cleanupAuthorityConnectionClosed
		}
	}
	if err := b.client.Abort(); err == nil {
		return cleanupAuthorityConnectionAborted
	}
	if channelClosed(closed) {
		return cleanupAuthorityConnectionClosed
	}
	return cleanupAuthorityPending
}

func (b *Backend) guardOpenCleanup(
	ctx context.Context,
	terminalID string,
	cleanupKind openCleanupKind,
	params *protocol.TerminalCloseParams,
	settle func(),
) {
	closed := clientClosed(b.client)
	outcome := cleanupAuthorityPending
	if channelClosed(closed) {
		outcome = cleanupAuthorityConnectionClosed
	} else {
		ticker := time.NewTicker(openCleanupRetryInterval)
		defer ticker.Stop()
		for outcome == cleanupAuthorityPending {
			select {
			case <-closed:
				outcome = cleanupAuthorityConnectionClosed
			case <-ticker.C:
				outcome = b.cleanupTerminal(ctx, params)
			}
		}
	}
	logger.Ctx(ctx).Info("remote.Backend.Open: cleanup guardian settled",
		zap.String("terminalId", terminalID),
		zap.String("cleanupKind", string(cleanupKind)),
		zap.String("outcome", string(outcome)))
	settle()
}

func clientClosed(client Client) <-chan struct{} {
	observer, ok := client.(closedClient)
	if !ok {
		return nil
	}
	return observer.Closed()
}

func channelClosed(closed <-chan struct{}) bool {
	select {
	case <-closed:
		return true
	default:
		return false
	}
}

func onceRelease(release func()) func() {
	if release == nil {
		return func() {}
	}
	return sync.OnceFunc(release)
}

type handleImpl struct {
	client       Client
	terminalID   string
	subscription Subscription

	data chan []byte
	exit chan pkgpty.ExitInfo

	mu                sync.Mutex
	closed            bool
	authoritativeExit bool
	closeCall         *closeCall
	done              chan struct{}
	release           func()
	operationTimeout  time.Duration
}

type closeCall struct {
	done chan struct{}
	err  error
}

func (h *handleImpl) Write(p []byte) (int, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return 0, errors.New("remote pty closed")
	}
	h.mu.Unlock()
	operationCtx, cancel := context.WithTimeout(context.Background(), h.operationTimeout)
	defer cancel()
	var ack struct{}
	err := h.client.Call(operationCtx, "terminal.write", protocol.TerminalWriteParams{
		TerminalID: h.terminalID, Data: string(p),
	}, &ack)
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (h *handleImpl) Resize(cols, rows uint16) error {
	operationCtx, cancel := context.WithTimeout(context.Background(), h.operationTimeout)
	defer cancel()
	var ack struct{}
	return h.client.Call(operationCtx, "terminal.resize", protocol.TerminalResizeParams{
		TerminalID: h.terminalID, Cols: cols, Rows: rows,
	}, &ack)
}

func (h *handleImpl) Close() error {
	h.mu.Lock()
	if call := h.closeCall; call != nil {
		h.mu.Unlock()
		<-call.done
		return call.err
	}
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	call := &closeCall{done: make(chan struct{})}
	h.closeCall = call
	h.mu.Unlock()

	operationCtx, cancel := context.WithTimeout(context.Background(), h.operationTimeout)
	var ack struct{}
	err := h.client.Call(operationCtx, "terminal.close", protocol.TerminalCloseParams{
		TerminalID: h.terminalID,
	}, &ack)
	timedOut := errors.Is(operationCtx.Err(), context.DeadlineExceeded) &&
		errors.Is(err, context.DeadlineExceeded)
	cancel()

	settledBeforeAbort := false
	abortAttempted := false
	var abortErr error
	if timedOut {
		h.mu.Lock()
		settledBeforeAbort = h.closed
		h.mu.Unlock()
		if !settledBeforeAbort {
			// A half-open terminal.close leaves every session on this shared
			// connection suspect. Abort synchronously so agentred's CloseAll is
			// the safety owner before local state or the lease is settled.
			abortAttempted = true
			abortErr = h.client.Abort()
		}
	}

	h.mu.Lock()
	settled := h.closed
	switch {
	case settled:
		// A real terminal.exit remains authoritative. A connection-lost outcome
		// caused by our successful timeout abort still returns the deadline that
		// triggered the fallback to every coalesced caller.
		if !timedOut || settledBeforeAbort || h.authoritativeExit || abortErr != nil {
			err = nil
		}
	case err == nil:
		h.closed = true
		close(h.done)
		settled = true
	case timedOut && abortAttempted && abortErr == nil:
		h.closed = true
		close(h.done)
		settled = true
	case timedOut && abortAttempted:
		err = errors.Join(err, fmt.Errorf("abort shared remote terminal connection: %w", abortErr))
	}
	call.err = err
	h.mu.Unlock()
	if settled {
		h.release()
	}
	h.mu.Lock()
	h.closeCall = nil
	close(call.done)
	h.mu.Unlock()
	return err
}

func (h *handleImpl) Data() <-chan []byte          { return h.data }
func (h *handleImpl) Exit() <-chan pkgpty.ExitInfo { return h.exit }

func (h *handleImpl) pump() {
	dataCh := h.subscription.Data
	exitCh := h.subscription.Exit
	outcome := pkgpty.ExitInfo{Reason: "connection_lost"}
	defer func() {
		h.exit <- outcome
		close(h.exit)
		close(h.data)
		h.client.Unsubscribe(h.terminalID, h.subscription)
		h.release()
	}()

	for {
		select {
		case ev, ok := <-dataCh:
			if !ok {
				// ClientAdapter queues an authoritative exit before closing data.
				// Prefer that buffered outcome; otherwise the subscription ended
				// because the connection disappeared without terminal.exit.
				authoritative := false
				select {
				case ev, exitOK := <-exitCh:
					authoritative = exitOK
					if exitOK {
						outcome = exitInfo(ev)
					}
				default:
				}
				if !h.claimDaemonExit(authoritative) {
					outcome = pkgpty.ExitInfo{Reason: "killed"}
				}
				return
			}
			if !h.forwardData(ev) {
				outcome = pkgpty.ExitInfo{Reason: "killed"}
				return
			}
		case ev, ok := <-exitCh:
			if ok {
				outcome = exitInfo(ev)
			}
			if !h.claimDaemonExit(ok) {
				outcome = pkgpty.ExitInfo{Reason: "killed"}
				return
			}
			if ok {
				h.drainDataAfterExit(dataCh)
			}
			return
		case <-h.done:
			outcome = pkgpty.ExitInfo{Reason: "killed"}
			return
		}
	}
}

func (h *handleImpl) claimDaemonExit(authoritative bool) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.closed = true
	h.authoritativeExit = authoritative
	return true
}

func exitInfo(ev protocol.TerminalExitEvent) pkgpty.ExitInfo {
	return pkgpty.ExitInfo{Code: ev.Code, Reason: ev.Reason, Msg: ev.Msg}
}

func (h *handleImpl) forwardData(ev protocol.TerminalDataEvent) bool {
	select {
	case h.data <- append([]byte(nil), ev.Data...):
		return true
	case <-h.done:
		return false
	}
}

func (h *handleImpl) drainDataAfterExit(dataCh <-chan protocol.TerminalDataEvent) {
	// A daemon exit is authoritative only after every earlier accepted frame
	// has either reached the consumer or a confirmed local Close has won.
	for ev := range dataCh {
		if !h.forwardData(ev) {
			return
		}
	}
}

var _ pkgpty.Backend = (*Backend)(nil)
var _ pkgpty.Handle = (*handleImpl)(nil)
