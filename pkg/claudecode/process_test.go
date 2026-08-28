package claudecode

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func readProcessStdout(t *testing.T, p *process) string {
	t.Helper()
	out := strings.Builder{}
	buf := make([]byte, 64)
	for {
		n, rerr := p.stdout.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if rerr != nil {
			if rerr != io.EOF {
				t.Fatalf("read stdout: %v", rerr)
			}
			break
		}
	}
	return out.String()
}

func runShellProcess(t *testing.T, script string, env map[string]string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	p, err := startProcess(ctx, processSpec{
		binary: "/bin/sh",
		args:   []string{"-c", script},
		env:    env,
	})
	require.NoError(t, err)

	out := readProcessStdout(t, p)
	exit, _ := p.wait(ctx)
	require.Equal(t, 0, exit)
	return out
}

// TestProcess_StreamsStdoutAndWaitsForExit 用 /bin/sh -c 'printf ...' 作为 fake
// binary，验证 Start → 读 stdout → Wait 的链路。
func TestProcess_StreamsStdoutAndWaitsForExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	p, err := startProcess(ctx, processSpec{
		binary: "/bin/sh",
		args:   []string{"-c", `printf 'a\nb\n'`},
		cwd:    "",
		env:    nil,
	})
	require.NoError(t, err)

	out := readProcessStdout(t, p)
	exitCode, _ := p.wait(ctx)
	assert.Equal(t, 0, exitCode)
	assert.Equal(t, "a\nb\n", out)
}

// TestProcess_KillTerminatesProcessGroup 钉死硬杀路径:对一个长睡、且派生了一个后台
// 孙进程(模拟 CLI 卡在 MCP 初始化、孙进程握着 stdout pipe)的进程组调 kill(),必须
// 整组 SIGKILL 掉:reaper 的 cmd.Wait 随即返回 → exit channel close,上层 readLoop 拿
// EOF 解阻塞,孙进程也必须一起死掉。
//
// 脚本必须是 `sleep 60 & wait`:后台孙进程继承并持有 stdout pipe,shell 死在 wait 上。
// 若只杀 shell 不杀孙进程,io.Copy 永远等不到 EOF → reaper 永不收尾。旧的 `sleep 60`
// 前台写法在 macOS 上孙进程恰好不握 pipe,只有 Linux CI 红;`& wait` 让 macOS 和 Linux
// 行为一致,把「只杀 shell 不杀孙进程」的 bug 稳定复现出来。
func TestProcess_KillTerminatesProcessGroup(t *testing.T) {
	ctx := context.Background()
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	// #nosec G204 -- fixed shell script; the only argument is a test-owned temp path.
	p, err := startProcess(ctx, processSpec{
		binary: "/bin/sh",
		args:   []string{"-c", fmt.Sprintf(`sleep 60 & printf '%%s\n' "$!" > "%s"; wait`, pidFile)},
	})
	require.NoError(t, err)
	require.False(t, p.hasExited(), "子进程应先存活")

	grandchild := readPIDEventually(t, pidFile)
	t.Cleanup(func() { terminatePID(grandchild) }) // 失败路径兜底,避免遗留孙进程

	p.kill()

	select {
	case <-p.exit:
	case <-time.After(5 * time.Second):
		t.Fatal("kill() 没能终止整棵进程树(孙进程仍握着 stdout pipe,reaper 卡死)")
	}
	assert.True(t, p.hasExited(), "kill 后 reaper 应已收尾")
	assertProcessGoneEventually(t, grandchild)
}

func readPIDEventually(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path) //nolint:gosec // G304: path is a test-owned file under t.TempDir.
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process pid file %s was not written", path)
	return 0
}

func assertProcessGoneEventually(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d remained alive after the termination bound", pid)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil //nolint:gosec // G204: fixed executable with an OS-assigned test PID.
}

func terminatePID(pid int) {
	if pid > 0 {
		_ = exec.Command("kill", "-9", strconv.Itoa(pid)).Run() //nolint:gosec // G204: fixed executable with an OS-assigned test PID.
	}
}

// TestProcess_EnvInheritsOSEnviron 验证传入 spec.env 时不会把整个进程环境清空。
// claude CLI 依赖 HOME 找 ~/.claude/projects、PATH 找 git/bash 等；如果直接
// cmd.Env = envList 把 PATH/HOME 也丢掉，子进程会卡在初始化阶段不出任何 frame —
// 用户视角就是「发出去了但一直没返回消息」。
func TestProcess_EnvInheritsOSEnviron(t *testing.T) {
	t.Setenv("CLAUDECODE_TEST_INHERIT", "from_parent")

	// 同时 echo: 调用方注入的 key + 父进程继承的 key。两者都应该出现。
	out := runShellProcess(t,
		`printf '%s\n%s\n' "$ANTHROPIC_AUTH_TOKEN" "$CLAUDECODE_TEST_INHERIT"`,
		map[string]string{"ANTHROPIC_AUTH_TOKEN": "from_caller"},
	)
	assert.Equal(t, "from_caller\nfrom_parent\n", out,
		"子进程应同时拿到调用方注入的 env 和父进程继承的 env")
}

// TestProcess_EnvCallerOverridesOSEnviron 验证调用方传入的同名 key 会覆盖
// 父进程的值（execve 后者胜出）—— 比如让单元测试可以临时改 HOME。
func TestProcess_EnvCallerOverridesOSEnviron(t *testing.T) {
	t.Setenv("CLAUDECODE_TEST_OVERRIDE", "parent_value")

	out := runShellProcess(t,
		`printf '%s\n' "$CLAUDECODE_TEST_OVERRIDE"`,
		map[string]string{"CLAUDECODE_TEST_OVERRIDE": "caller_value"},
	)
	assert.Equal(t, "caller_value\n", out, "调用方注入的值应当覆盖父进程")
}

func TestProcess_BinaryMissing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, err := startProcess(ctx, processSpec{binary: "/definitely/not/a/real/binary-xyz"})
	assert.ErrorIs(t, err, ErrBinaryNotFound)
}

// TestBoundedBuffer_DropsOldestBytesOverCapacity 覆盖 stderr 超 64KB 时的丢前路径。
// 之前这条 Write 0% 覆盖，trim-front 算错就会静默截掉 stderr。
func TestBoundedBuffer_DropsOldestBytesOverCapacity(t *testing.T) {
	b := newBoundedBuffer(4)
	_, _ = b.Write([]byte("abcdefgh"))
	assert.Equal(t, "efgh", b.String())

	// 二次写入继续丢前：'efgh' + 'ijkl' → 末尾 4 字节 'ijkl'。
	_, _ = b.Write([]byte("ijkl"))
	assert.Equal(t, "ijkl", b.String())
}

// TestProcess_WaitClassifiesResumeMissingStderr 启动一个立刻写 stderr "No conversation found"
// 并 exit 1 的子进程，验证 wait() 返回的 err errors.Is ErrSessionNotFound。
// 这是 OpenSession 健康检查能识别 "resume 失效" 的最底层依据。
//
// 这条 test 是真子进程的集成验证（startProcess + reaper + classifyStderr 一条链），
// 故走 /bin/sh -c 起真进程，不走 pipeSpawner。
func TestProcess_WaitClassifiesResumeMissingStderr(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	p := mustStartFakeResumeMissing(t, ctx)
	code, werr := p.wait(ctx)
	assert.Equal(t, 1, code)
	require.Error(t, werr)
	assert.ErrorIs(t, werr, ErrSessionNotFound)
	assert.Contains(t, werr.Error(), "No conversation found")
}

// TestProcess_WaitIdempotent 多次 wait 必须返回同一个分类后错误，且不 hang。
// 上层（Session.Close 兜底 + 0-frame fallback 可能各自调一次 wait）需要这个保证。
func TestProcess_WaitIdempotent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	p := mustStartFakeResumeMissing(t, ctx)
	code1, err1 := p.wait(ctx)
	code2, err2 := p.wait(ctx)
	assert.Equal(t, code1, code2)
	assert.ErrorIs(t, err1, ErrSessionNotFound)
	assert.ErrorIs(t, err2, ErrSessionNotFound)
}

// TestProcess_HasExitedAndExitErrIfDoneAliveProcess 健康存活的进程 hasExited 应当为 false
// 且 exitErrIfDone 返回 nil；关 stdin 触发退出后两个 accessor 必须反映已退出。
func TestProcess_HasExitedAndExitErrIfDoneAliveProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 健康常驻进程：只 read stdin 直到 EOF；不写 stdout/stderr。
	p, err := startProcess(ctx, processSpec{
		binary: "/bin/sh",
		args:   []string{"-c", `while IFS= read -r _line; do :; done`},
	})
	require.NoError(t, err)

	// 刚启动：fake 脚本在 `while read` 阻塞，进程必然存活。
	assert.False(t, p.hasExited(), "新起的健康进程不应当报 hasExited=true")
	assert.NoError(t, p.exitErrIfDone(), "存活进程的 exitErrIfDone 必须返 nil")

	// 关 stdin → 让 fake 脚本 `while read` 拿到 EOF 后正常退出（exit 0）。
	require.NoError(t, p.stdin.Close())

	// 等 reaper goroutine 抓到 exit。
	require.Eventually(t, p.hasExited, time.Second, 10*time.Millisecond,
		"stdin 关后 reaper 应当在百毫秒内拿到 exit")

	// 正常退出（exit 0）+ 无 stderr → exitErrIfDone 必须返 nil。
	assert.NoError(t, p.exitErrIfDone(), "exit 0 且无 stderr 时 exitErrIfDone 必须返 nil")
}

// TestProcess_HasExitedDetectsImmediateExit 启动后立刻 exit 1 的子进程，reaper
// goroutine 应当很快把 exit channel 关掉，让 OpenSession 的健康检查在 200ms
// 窗口内通过 select 拿到错误。
func TestProcess_HasExitedDetectsImmediateExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	p := mustStartFakeResumeMissing(t, ctx)

	require.Eventually(t, p.hasExited, 500*time.Millisecond, 5*time.Millisecond,
		"立刻 exit 1 的进程必须在百毫秒内被 reaper 抓到")

	err := p.exitErrIfDone()
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrSessionNotFound)
}

// mustStartFakeResumeMissing 起一个真子进程模拟 `claude --resume <gone-id>` 行为：
// /bin/sh 一行写 stderr「No conversation found …」+ exit 1，让 classifyStderr
// 命中 ErrSessionNotFound sentinel。
func mustStartFakeResumeMissing(t *testing.T, ctx context.Context) *process {
	t.Helper()
	p, err := startProcess(ctx, processSpec{
		binary: "/bin/sh",
		args: []string{"-c",
			`echo "No conversation found with session ID: 07dcda59-d426-4d66-b6d3-12d6d59bc5a3" 1>&2; exit 1`,
		},
	})
	require.NoError(t, err)
	return p
}

// 契约:startProcess 收下的 ctx 只守 spawn 阶段 —— 进程一旦起来,寿命由调用方显式
// 结束(Close 关 stdin / kill),不随这个 ctx 取消而终止。
//
// 常驻会话正是靠这条:OpenSession 目前特地传 context.Background() 绕开
// exec.CommandContext,而绕开的前提是每个调用方都记得这么做。把契约落到这一层,
// 传进来的是哪个 ctx 就都不再有区别 —— codex 那边正是忘了这件事,池里留下的是一个
// 在建它那一轮结束时就被 SIGKILL 的死进程。
func TestProcess_GivenStartContextCanceledAfterSpawn_WhenWriting_ThenProcessStaysAlive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p, err := startProcess(ctx, processSpec{
		binary: "/bin/sh",
		args:   []string{"-c", "while IFS= read -r line; do printf '%s\\n' \"$line\"; done"},
	})
	require.NoError(t, err)
	t.Cleanup(p.kill)

	cancel()

	_, writeErr := io.WriteString(p.stdin, "ping\n")
	require.NoError(t, writeErr, "cancel 之后进程应当还能收到 stdin")

	line := make(chan string, 1)
	go func() {
		text, readErr := bufio.NewReader(p.stdout).ReadString('\n')
		if readErr != nil {
			close(line)
			return
		}
		line <- strings.TrimSpace(text)
	}()
	select {
	case got, ok := <-line:
		require.True(t, ok, "cancel 之后 stdout 读到 EOF,说明进程已被 ctx 杀掉")
		assert.Equal(t, "ping", got)
	case <-time.After(5 * time.Second):
		t.Fatal("cancel 之后进程没有回显,疑似已被 ctx 杀掉")
	}
}

// 边界:ctx 在 startProcess 之前就取消 —— spawn 阶段仍归它管,一个进程都不该起来。
func TestProcess_GivenContextCanceledBeforeSpawn_ThenNothingStarts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p, err := startProcess(ctx, processSpec{binary: "/bin/sh", args: []string{"-c", "cat"}})

	if p != nil {
		p.kill()
	}
	require.Nil(t, p, "ctx 已取消时不该起进程")
	assert.ErrorIs(t, err, context.Canceled)
}
