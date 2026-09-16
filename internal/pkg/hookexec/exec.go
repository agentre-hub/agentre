package hookexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/cliprocess"
)

type osScriptRunner struct{}

// NewOSRunner 返回真起子进程的生产实现。
func NewOSRunner() ScriptRunner { return &osScriptRunner{} }

func (osScriptRunner) Run(ctx context.Context, spec RunSpec) (*RunResult, error) {
	in, err := resolve(spec.Interpreter, spec.InterpreterPath)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "agentre-hook-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, "hook"+in.Ext)
	if err := os.WriteFile(file, []byte(spec.Command), 0o600); err != nil {
		return nil, err
	}

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append(append([]string{}, in.Args...), file)
	//nolint:gosec // G204: 按设计执行用户自定义脚本；解释器经 Resolve allowlist 校验,脚本本就是 Hook 的功能本体。
	cmd := exec.CommandContext(runCtx, in.Bin, args...)
	cmd.Env = append(os.Environ(), envSlice(spec.Env)...)
	cliprocess.ApplyProcessGroup(cmd) // 平台钩子：独立进程组

	limit := spec.MaxOutputBytes
	if limit <= 0 {
		limit = 256 * 1024
	}
	var outBuf, errBuf bytes.Buffer
	outW := &cappedWriter{w: &outBuf, max: limit}
	cmd.Stdout = outW
	cmd.Stderr = &cappedWriter{w: &errBuf, max: limit}

	start := time.Now()
	cmd.Cancel = func() error { return cliprocess.KillProcessTree(cmd.Process) } // 超时/取消时杀整组
	runErr := cmd.Run()
	dur := time.Since(start)

	res := &RunResult{
		Stdout:   outBuf.Bytes(),
		Stderr:   errBuf.Bytes(),
		Duration: dur,
		TimedOut: errors.Is(runCtx.Err(), context.DeadlineExceeded),
	}
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		res.ExitCode = 0
	case errors.As(runErr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		res.ExitCode = -1
		if !res.TimedOut {
			return res, runErr // 真正的起进程失败（非退出码/非超时）
		}
	}
	return res, nil
}

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// cappedWriter 截断超限输出但继续吞剩余字节避免子进程阻塞。
type cappedWriter struct {
	w       io.Writer
	max     int
	written int
}

func (c *cappedWriter) Write(p []byte) (int, error) {
	if c.written >= c.max {
		return len(p), nil
	}
	room := c.max - c.written
	if len(p) > room {
		_, _ = c.w.Write(p[:room])
		c.written = c.max
		return len(p), nil
	}
	n, err := c.w.Write(p)
	c.written += n
	return n, err
}
