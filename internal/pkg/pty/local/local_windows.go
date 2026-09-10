//go:build windows

// Package local implements pty.Backend on Windows using the ConPTY API
// (github.com/UserExistsError/conpty). The non-Windows implementation lives in
// local.go (creack/pty); both expose the same NewBackend()/Backend/Open so
// callers (internal/app/terminal_wiring.go) need no build-tagged wiring.
package local

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/UserExistsError/conpty"

	pkgpty "github.com/agentre-hub/agentre/internal/pkg/pty"
)

// Backend is the local ConPTY implementation of pty.Backend.
type Backend struct{}

func NewBackend() *Backend { return &Backend{} }

// closeGrace bounds how long Close() waits for the reaper to tear the PTY down
// after the wait context is canceled, so a wedged ClosePseudoConsole can't hang
// the caller forever.
const closeGrace = 2 * time.Second

// Open spawns a shell under a ConPTY and starts the reaper + reader goroutines.
func (b *Backend) Open(ctx context.Context, spec pkgpty.Spec) (pkgpty.Handle, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	// ConPTY rejects zero dimensions; fall back to a sane default terminal size.
	cols, rows := int(spec.Cols), int(spec.Rows)
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}

	env := append(os.Environ(), spec.Env...)
	cpty, err := conpty.Start(
		windowsShellCommandLine(spec.Shell),
		conpty.ConPtyDimensions(cols, rows),
		conpty.ConPtyWorkDir(spec.Cwd),
		conpty.ConPtyEnv(env),
	)
	if err != nil {
		return nil, err
	}

	waitCtx, waitCancel := context.WithCancel(context.Background())
	h := &handleImpl{
		cpty:       cpty,
		waitCancel: waitCancel,
		data:       make(chan []byte, 32),
		exit:       make(chan pkgpty.ExitInfo, 1),
		done:       make(chan struct{}),
	}
	go h.reader()
	go h.reaper(waitCtx)
	return h, nil
}

// windowsShellCommandLine resolves the shell to launch and quotes it for
// CreateProcess. When spec.Shell is empty the preference is pwsh.exe
// (PowerShell 7) → powershell.exe (built-in PS5) → %COMSPEC% → cmd.exe.
func windowsShellCommandLine(shell string) string {
	if shell == "" {
		for _, candidate := range []string{"pwsh.exe", "powershell.exe"} {
			if p, lookErr := exec.LookPath(candidate); lookErr == nil {
				shell = p
				break
			}
		}
	}
	if shell == "" {
		if comspec := os.Getenv("COMSPEC"); comspec != "" {
			shell = comspec
		} else {
			shell = "cmd.exe"
		}
	}
	// EscapeArg quotes paths containing spaces (e.g. "Program Files") so
	// CreateProcess parses the executable as a single token.
	return syscall.EscapeArg(shell)
}

type handleImpl struct {
	cpty       *conpty.ConPty
	waitCancel context.CancelFunc

	data chan []byte
	exit chan pkgpty.ExitInfo

	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

func (h *handleImpl) Write(p []byte) (int, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return 0, errors.New("pty closed")
	}
	h.mu.Unlock()
	return h.cpty.Write(p)
}

func (h *handleImpl) Resize(cols, rows uint16) error {
	return h.cpty.Resize(int(cols), int(rows))
}

func (h *handleImpl) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	h.mu.Unlock()

	// Do NOT touch h.cpty here. The reaper is the sole caller of cpty.Close():
	// that call closes the process handle reaper's Wait() polls, so closing it
	// from two goroutines would race. Canceling the wait makes Wait() return
	// promptly; the reaper then kills the shell (ConPTY has no SIGHUP) via
	// cpty.Close() and signals done.
	h.waitCancel()
	select {
	case <-h.done:
	case <-time.After(closeGrace):
	}
	return nil
}

func (h *handleImpl) Data() <-chan []byte          { return h.data }
func (h *handleImpl) Exit() <-chan pkgpty.ExitInfo { return h.exit }

func (h *handleImpl) reader() {
	// The reader is the SOLE closer of h.data (mirrors the Unix backend): it
	// both sends to and closes the channel, so there is never a concurrent
	// send-and-close. It unblocks when the reaper's cpty.Close() tears down the
	// output pipe, turning a pending Read into an error/EOF.
	defer close(h.data)
	buf := make([]byte, 8192)
	for {
		n, err := h.cpty.Read(buf)
		if n > 0 {
			out := make([]byte, n)
			copy(out, buf[:n])
			select {
			case h.data <- out:
			case <-h.done:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (h *handleImpl) reaper(waitCtx context.Context) {
	code, err := h.cpty.Wait(waitCtx)
	killed := h.wasKilled()

	// reaper owns cpty.Close(): it kills a still-live shell (ConPTY has no
	// SIGHUP) AND unblocks reader() by tearing down the output pipe.
	_ = h.cpty.Close()
	close(h.done)

	info := pkgpty.ExitInfo{}
	switch {
	case killed:
		// Wait was canceled by Close(); the exit code is the STILL_ACTIVE
		// sentinel, not a real code, so don't surface it.
		info.Reason = "killed"
	case err != nil:
		info.Reason = "error"
		info.Msg = err.Error()
	default:
		info.Reason = "natural"
		info.Code = int(code)
	}
	h.exit <- info
	close(h.exit)
	// h.data is closed by reader() (its sole owner), not here — closing it from
	// two goroutines races the reader's send and panics.
}

func (h *handleImpl) wasKilled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.closed
}

var _ pkgpty.Handle = (*handleImpl)(nil)
var _ pkgpty.Backend = (*Backend)(nil)
