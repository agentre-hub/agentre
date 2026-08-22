package claudecode

import (
	"context"
	"io"
	"os/exec"

	"github.com/agentre-ai/agentre/internal/pkg/clienv"
	"github.com/agentre-ai/agentre/internal/pkg/procattr"
)

type processSpec struct {
	binary string
	args   []string
	cwd    string
	env    map[string]string
}

// process 简单封装 *exec.Cmd + stdio pipes。stderr 暂存到内存（cap 一下避免炸）。
//
// 退出事件 channel 化：startProcess 起一个 reaper goroutine 跑 cmd.Wait()，
// 收完 exit 后把分类后的错误存到 exitErr，最后 close(exit)。后续的 wait /
// hasExited / exitErrIfDone 都从这份 cached 状态读，幂等。
//
// stdout 先由内部 goroutine 转发并读到 EOF，reaper 再调用 cmd.Wait；这样既遵守
// os/exec 的 StdoutPipe 生命周期约束，也保证 Session 看到完整输出和稳定的 EOF。
type process struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	stderrBuf *boundedBuffer

	exit     chan struct{} // reaper goroutine 退出后 close
	exitCode int           // exit channel close 之后有效
	exitErr  error         // exit channel close 之后有效（已经过 classifyStderr 包装）
}

const maxStderrBytes = 64 << 10 // 64KB；超出后丢弃前面内容

func startProcess(ctx context.Context, spec processSpec) (*process, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	searchEnv := clienv.BuildEnv(spec.env, spec.binary)
	binary, ok := clienv.ResolveBinaryForEnv(spec.binary, searchEnv)
	if !ok {
		return nil, ErrBinaryNotFound
	}
	// #nosec G204 -- binary/args come from agent backend config (CLIPath + flags
	// 由 agentruntime 装配)，不接受用户输入。
	//
	// 刻意用 exec.Command 而非 CommandContext:ctx 只守 spawn 阶段(上面那道取消
	// 检查),进程一旦起来寿命就归调用方 —— 会话跨轮常驻,而开它的那一轮 ctx 会在
	// 轮末 cancel,绑上去就是每轮结束都 SIGKILL 掉池里留着复用的进程。
	cmd := exec.Command(binary, spec.args...)
	procattr.ApplyNoConsoleWindow(cmd)
	setProcessGroup(cmd)
	if spec.cwd != "" {
		cmd.Dir = spec.cwd
	}
	// 关键：必须从 os.Environ() 继承再追加，并补全 GUI App 缺失的 PATH。
	// claude CLI 依赖 HOME 找 ~/.claude/projects、PATH 找 git/ripgrep/bash/node 等
	// 子工具；Finder/Dock 启动的 app bundle 默认拿不到用户 shell PATH。
	cmd.Env = clienv.BuildEnv(spec.env, binary)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	cmdStdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stdout, stdoutWriter := io.Pipe()
	stderrBuf := newBoundedBuffer(maxStderrBytes)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		return nil, classifyExecErr(err)
	}
	stdoutDone := make(chan struct{})
	go func() {
		_, copyErr := io.Copy(stdoutWriter, cmdStdout)
		_ = stdoutWriter.CloseWithError(copyErr)
		close(stdoutDone)
	}()
	p := &process{
		cmd:       cmd,
		stdin:     stdin,
		stdout:    stdout,
		stderrBuf: stderrBuf,
		exit:      make(chan struct{}),
	}
	// reaper：阻塞等子进程退出，分类 stderr，存到 process 然后 close(exit)。
	// 多次 wait / hasExited / exitErrIfDone 从这份 cached 结果读。
	go func() {
		// StdoutPipe requires all reads to finish before Wait. Waiting for the
		// forwarding reader prevents cmd.Wait from closing the OS pipe under an
		// in-flight caller read and turning a clean EOF into os.ErrClosed.
		<-stdoutDone
		werr := cmd.Wait()
		exit := 0
		if ee, ok := werr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else if werr != nil {
			exit = -1
		}
		p.exitCode = exit
		p.exitErr = classifyStderr(p.stderrBuf.String(), exit)
		close(p.exit)
	}()
	return p, nil
}

// wait 阻塞等子进程退出（或 ctx 取消）。
// 多次调用读同一份 cached 退出结果，幂等。reaper goroutine 在 startProcess
// 里已经在跑，这里只 select 等它收尾。
func (p *process) wait(ctx context.Context) (int, error) {
	select {
	case <-p.exit:
		return p.exitCode, p.exitErr
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

// hasExited 非阻塞查询：reaper 已经填好 exitCode/exitErr 时返 true。
// OpenSession 的健康检查窗口和 Turn 写 stdin 失败路径用它判断"现在挂没挂"。
func (p *process) hasExited() bool {
	select {
	case <-p.exit:
		return true
	default:
		return false
	}
}

// exitErrIfDone 已退出 → 返 cached 退出错误（可能是 ErrSessionNotFound 或
// *ProcessExitError，也可能是 nil 表示 exit 0 且 stderr 没命中 sentinel）；
// 还活着 → 返 nil。调用方先 hasExited / 这里返非 nil 才表示进程确实死了。
//
// 重要：返 nil 不一定代表进程活着——也可能进程已经正常 exit(0)。配合
// hasExited() 一起用：hasExited()==true 且 exitErrIfDone()==nil 表示"已退且无错"。
func (p *process) exitErrIfDone() error {
	if !p.hasExited() {
		return nil
	}
	return p.exitErr
}

// pid 交出子进程号;进程还没起来 / 已经没了时为 0。
func (p *process) pid() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// kill 给子进程所在进程组发 SIGKILL（不可被忽略），用于子进程卡死、优雅 Close(关 stdin)
// 救不回来的场景（典型：CLI 卡在 MCP 初始化连不上、根本不读 stdin）。整组硬杀会连带
// CLI 派生的孙进程（它们继承并握住 stdout pipe）一起终止，否则 reaper 的 io.Copy 等
// 不到 EOF、exit channel 永不关闭。进程死亡后 reaper 的 cmd.Wait 返回、parent-side
// stdout pipe 关闭 → scanner 拿到 EOF → readLoop 收尾未决轮。多次调用幂等。
func (p *process) kill() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = killProcessGroup(p.cmd)
	}
}

// boundedBuffer 是一个上限可丢前的 io.Writer，避免 stderr 把内存吃爆。
type boundedBuffer struct {
	buf      []byte
	capacity int
}

func newBoundedBuffer(capacity int) *boundedBuffer { return &boundedBuffer{capacity: capacity} }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.buf = append(b.buf, p...)
	if over := len(b.buf) - b.capacity; over > 0 {
		b.buf = b.buf[over:]
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string { return string(b.buf) }
