//go:build !windows

// Package local implements pty.Backend with github.com/creack/pty.
package local

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	creackpty "github.com/creack/pty"
	"golang.org/x/sys/unix"

	pkgpty "github.com/agentre-hub/agentre/internal/pkg/pty"
)

// Backend is the local creack/pty implementation of pty.Backend.
type Backend struct{}

func NewBackend() *Backend { return &Backend{} }

const (
	sigkillGrace   = 200 * time.Millisecond
	reaperDeadline = 200 * time.Millisecond
)

// Open spawns a shell under a PTY and starts the reaper + reader goroutines.
func (b *Backend) Open(ctx context.Context, spec pkgpty.Spec) (pkgpty.Handle, error) {
	shell := spec.Shell
	if shell == "" {
		if env := os.Getenv("SHELL"); env != "" {
			shell = env
		} else {
			shell = "/bin/sh"
		}
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	args := []string{"-l"}
	if spec.Command != "" {
		args = append(args, "-c", spec.Command)
	}
	cmd := exec.Command(shell, args...) //nolint:gosec // G204: shell from spec/$SHELL; command is the user's own authorized local shell input
	cmd.Dir = spec.Cwd
	cmd.Env = append(os.Environ(), append(spec.Env, "TERM=xterm-256color")...)

	ws := &creackpty.Winsize{Cols: spec.Cols, Rows: spec.Rows}
	f, err := creackpty.StartWithSize(cmd, ws)
	if err != nil {
		return nil, err
	}
	h := &handleImpl{
		cmd:        cmd,
		file:       f,
		data:       make(chan []byte, 32),
		exit:       make(chan pkgpty.ExitInfo, 1),
		done:       make(chan struct{}),
		stopReader: make(chan struct{}),
	}
	go h.reader()
	go h.reaper()
	return h, nil
}

type handleImpl struct {
	cmd  *exec.Cmd
	file *os.File
	// fileMu 只串行化 Fd ioctl 与 reaper Close；os.File 的普通 Read/Write 自身
	// 支持并发，不把终端热路径锁进来。
	fileMu sync.Mutex

	data chan []byte
	exit chan pkgpty.ExitInfo

	mu     sync.Mutex
	closed bool
	done   chan struct{}

	stopReader chan struct{}
}

func (h *handleImpl) Write(p []byte) (int, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return 0, errors.New("pty closed")
	}
	h.mu.Unlock()
	return h.file.Write(p)
}

func (h *handleImpl) Resize(cols, rows uint16) error {
	return creackpty.Setsize(h.file, &creackpty.Winsize{Cols: cols, Rows: rows})
}

func (h *handleImpl) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	close(h.stopReader)
	h.mu.Unlock()

	rootPID, sessionID := h.ownedSession()
	foregroundPGID := h.foregroundProcessGroup(sessionID)
	h.signalProcessGroups(rootPID, sessionID, syscall.SIGHUP, foregroundPGID)
	select {
	case <-h.done:
		// shell 可能响应 HUP 后先退出，而忽略 HUP 的前台任务仍留在原进程组里。
		// root 被回收前记录的 group + session 归属仍是安全的精确所有权边界。
		h.signalProcessGroups(rootPID, sessionID, syscall.SIGKILL, foregroundPGID)
	case <-time.After(sigkillGrace):
		latestForegroundPGID := h.foregroundProcessGroup(sessionID)
		h.signalProcessGroups(rootPID, sessionID, syscall.SIGKILL,
			foregroundPGID, latestForegroundPGID)
		select {
		case <-h.done:
		case <-time.After(reaperDeadline):
		}
	}
	return nil
}

// ownedSession 返回 PTY shell 自己创建并领头的 session。creack/pty 通过 Setsid
// 建这个边界；校验失败时返回 0，让关闭路径只退化为单进程信号，绝不猜一个进程组。
func (h *handleImpl) ownedSession() (rootPID, sessionID int) {
	if h.cmd.Process == nil {
		return 0, 0
	}
	rootPID = h.cmd.Process.Pid
	sid, err := unix.Getsid(rootPID)
	if err != nil || sid != rootPID {
		return rootPID, 0
	}
	return rootPID, sid
}

// foregroundProcessGroup 从 PTY master 查询交互 shell 当前正在 wait 的前台 job。
// 交互 shell 会给 Claude/命令单独建进程组；只杀 shell 自己无法覆盖那棵任务树。
func (h *handleImpl) foregroundProcessGroup(sessionID int) int {
	if sessionID == 0 {
		return 0
	}
	h.fileMu.Lock()
	pgid, err := unix.IoctlGetInt(int(h.file.Fd()), unix.TIOCGPGRP)
	h.fileMu.Unlock()
	if err != nil || !processGroupBelongsToSession(pgid, sessionID) {
		return 0
	}
	return pgid
}

func (h *handleImpl) signalProcessGroups(
	rootPID, sessionID int,
	sig syscall.Signal,
	foregroundPGIDs ...int,
) {
	seen := make(map[int]struct{}, len(foregroundPGIDs)+1)
	for _, pgid := range append(foregroundPGIDs, rootPID) {
		if _, ok := seen[pgid]; ok {
			continue
		}
		seen[pgid] = struct{}{}
		if processGroupBelongsToSession(pgid, sessionID) {
			_ = unix.Kill(-pgid, sig)
		}
	}
	if rootPID != 0 && !processGroupBelongsToSession(rootPID, sessionID) {
		_ = h.cmd.Process.Signal(sig)
	}
}

func processGroupBelongsToSession(pgid, sessionID int) bool {
	if pgid <= 1 || sessionID <= 1 || pgid == syscall.Getpgrp() {
		return false
	}
	actualPGID, err := unix.Getpgid(pgid)
	if err != nil || actualPGID != pgid {
		return false
	}
	actualSID, err := unix.Getsid(pgid)
	return err == nil && actualSID == sessionID
}

func (h *handleImpl) Data() <-chan []byte          { return h.data }
func (h *handleImpl) Exit() <-chan pkgpty.ExitInfo { return h.exit }

func (h *handleImpl) reader() {
	// The reader is the SOLE closer of h.data: it both sends to and closes the
	// channel, so there is never a concurrent send-and-close (which would panic
	// "send on closed channel"). The reaper must NOT close h.data.
	defer close(h.data)
	buf := make([]byte, 8192)
	for {
		n, err := h.file.Read(buf)
		if n > 0 {
			out := make([]byte, n)
			copy(out, buf[:n])
			select {
			case h.data <- out:
			case <-h.stopReader:
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (h *handleImpl) reaper() {
	err := h.cmd.Wait()
	// Closing the PTY releases a reader blocked in Read. Natural process exit is
	// deliberately not sent through stopReader: if Read already returned the
	// final bytes, reader must publish them instead of randomly selecting a
	// process-completion cancellation branch and discarding them.
	h.fileMu.Lock()
	_ = h.file.Close()
	h.fileMu.Unlock()
	close(h.done)

	info := pkgpty.ExitInfo{}
	if err == nil {
		info.Reason = "natural"
	} else {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			info.Code = ee.ExitCode()
			if h.wasKilled() {
				info.Reason = "killed"
			} else {
				info.Reason = "natural"
			}
		} else {
			info.Reason = "error"
			info.Msg = err.Error()
		}
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
