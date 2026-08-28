package cliprocess

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/agentre-hub/agentre/internal/pkg/clienv"
	"github.com/agentre-hub/agentre/internal/pkg/procattr"
)

type Options struct {
	Binary string
	Args   []string
	Cwd    string
	Env    []string
}

type Handle interface {
	Stdin() io.Writer
	Stdout() io.Reader
	Stderr() io.Reader
	Wait() error
	Kill() error
	Signal(os.Signal) error
}

type execHandle struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func (h *execHandle) Stdin() io.Writer  { return h.stdin }
func (h *execHandle) Stdout() io.Reader { return h.stdout }
func (h *execHandle) Stderr() io.Reader { return h.stderr }
func (h *execHandle) Wait() error       { return h.cmd.Wait() }

// PID 交出子进程号,进程还没起来 / 已经没了时为 0。排查用:把机器上的 CLI 进程和
// 界面上的会话对上,靠的就是这个号。
func (h *execHandle) PID() int {
	if h.cmd.Process == nil {
		return 0
	}
	return h.cmd.Process.Pid
}

// Kill 收掉整棵进程树:CLI 自己派生的孙进程握着 stdout 写端,只杀父进程会让读端
// 永远等不到 EOF。
func (h *execHandle) Kill() error {
	if h.cmd.Process == nil {
		return nil
	}
	return killProcessTree(h.cmd.Process)
}

// Signal 同样按树投递(优雅中断要能传到孙进程),拒收时退回单进程。
func (h *execHandle) Signal(sig os.Signal) error {
	if h.cmd.Process == nil {
		return nil
	}
	return signalProcessTree(h.cmd.Process, sig)
}

func Start(ctx context.Context, opts Options, binaryNotFound error) (Handle, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	extraEnv := envListToMap(opts.Env)
	searchEnv := clienv.BuildEnv(extraEnv, opts.Binary)
	binary, ok := clienv.ResolveBinaryForEnv(opts.Binary, searchEnv)
	if !ok {
		return nil, binaryNotFound
	}
	// #nosec G204 -- callers pass the configured CLI binary plus fixed protocol
	// flags; launching that subprocess is the intended behavior.
	//
	// 刻意用 exec.Command 而非 CommandContext:ctx 只守 spawn 阶段(上面那道
	// 取消检查),进程一旦起来寿命就归调用方 —— CLI 会话跨轮常驻,而调用它的那一轮
	// ctx 每轮都会 cancel,绑上去等于每轮结束都 SIGKILL 掉池里留着复用的进程。
	cmd := exec.Command(binary, opts.Args...)
	procattr.ApplyNoConsoleWindow(cmd)
	applyProcessGroup(cmd)
	if opts.Cwd != "" {
		cmd.Dir = opts.Cwd
	}
	cmd.Env = clienv.BuildEnv(extraEnv, binary)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, binaryNotFound
		}
		return nil, err
	}
	return &execHandle{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr}, nil
}

func envListToMap(items []string) map[string]string {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		k, v, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		out[k] = v
	}
	return out
}

// MaxDiagnosticBytes 是诊断缓冲区保留的字节上限。
const MaxDiagnosticBytes = 64 << 10

// LockedBuffer 是并发安全的诊断缓冲区,只保留最近 MaxDiagnosticBytes 字节。
//
// 只留尾巴不是省内存的小聪明:常驻 app-server / RPC 进程可以活几个小时,把整个
// 生命周期的 stderr 留在内存里是无界增长,还让后来的退出错误更可能把很久以前的
// 凭据形状原样带出来。
type LockedBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (b *LockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(p) >= MaxDiagnosticBytes {
		b.b = append(b.b[:0], p[len(p)-MaxDiagnosticBytes:]...)
		return len(p), nil
	}
	if over := len(b.b) + len(p) - MaxDiagnosticBytes; over > 0 {
		b.b = b.b[:copy(b.b, b.b[over:])]
	}
	b.b = append(b.b, p...)
	return len(p), nil
}

func (b *LockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.b)
}
