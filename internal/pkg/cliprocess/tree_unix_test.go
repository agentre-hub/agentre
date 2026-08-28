//go:build !windows

package cliprocess

import (
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Given 一个 pid 指向的正是调用方自己的进程组, When 按树投递信号, Then 调用方
// 那一组一个都不该收到。
//
// 逃出目标树的组信号会打到调用方组里的每一个兄弟进程;在 go test 下那个组里装着
// 测试进程、go 命令和它的编译/链接/vet 子进程 —— 一次投错就是整条工具链被带走,
// 而不是一个 CLI 进程树。
func TestSignalProcessTree_GivenCallersOwnGroup_WhenSignaling_ThenCallersGroupIsSpared(t *testing.T) {
	if syscall.Getpgrp() == os.Getpid() {
		t.Skip("测试进程自己就是组长:此时单进程回退与组投递无法区分")
	}

	received := make(chan os.Signal, 1)
	// SIGWINCH 对组内每个进程都是惰性的,既能观测到「逃逸」又不会伤到本测试要保护的
	// 那个 runner。
	signal.Notify(received, syscall.SIGWINCH)
	t.Cleanup(func() { signal.Stop(received) })

	// 组 id 本身永远是自己的组长,所以这个 pid 能通过任何「这是个真进程组吗」的检查,
	// 同时点的又是我们自己的组。
	_ = signalProcessTree(&os.Process{Pid: syscall.Getpgrp()}, syscall.SIGWINCH)

	select {
	case sig := <-received:
		t.Fatalf("调用方自己的进程组收到了 %v:树信号逃出了目标树", sig)
	case <-time.After(300 * time.Millisecond):
	}
}

// Given 一个自领进程组、组内还挂着孙进程的子进程, When 杀这棵树, Then 孙进程一起死。
func TestKillProcessTree_GivenGrandchildInTheSameGroup_WhenKilling_ThenGrandchildDiesToo(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "grandchild.pid")
	// #nosec G204 -- 固定的 shell 脚本,唯一参数是测试自有的临时路径。
	cmd := exec.Command("/bin/sh", "-c", `sleep 30 & printf '%s\n' "$!" > "$1"; wait`, "sh", pidFile)
	applyProcessGroup(cmd)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = killProcessTree(cmd.Process)
		_ = cmd.Wait()
	})

	grandchild := readPIDEventually(t, pidFile)
	require.NoError(t, killProcessTree(cmd.Process))

	// 先把组长回收掉:没被 wait 的僵尸照样应答 kill(pid, 0),下面的存活判定会把它读成
	// 还活着。
	require.Error(t, cmd.Wait(), "组长必须死于树杀,而不是干净退出")
	assertProcessGoneEventually(t, grandchild)
	assertProcessGoneEventually(t, cmd.Process.Pid)
}

// Given 一个已经不指向任何进程组的 pid, When 按树投递信号, Then 调用方自己那一组
// 依然不受影响。
func TestSignalProcessTree_GivenReapedPID_WhenSignaling_ThenCallersGroupIsSpared(t *testing.T) {
	if syscall.Getpgrp() == os.Getpid() {
		t.Skip("测试进程自己就是组长:此时单进程回退与组投递无法区分")
	}

	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	applyProcessGroup(cmd)
	require.NoError(t, cmd.Start())
	reaped := cmd.Process.Pid
	require.NoError(t, cmd.Wait())

	received := make(chan os.Signal, 1)
	signal.Notify(received, syscall.SIGWINCH)
	t.Cleanup(func() { signal.Stop(received) })

	_ = signalProcessTree(&os.Process{Pid: reaped}, syscall.SIGWINCH)

	select {
	case sig := <-received:
		t.Fatalf("调用方自己的进程组为一个已回收的 pid %d 收到了 %v", reaped, sig)
	case <-time.After(300 * time.Millisecond):
	}
}

func readPIDEventually(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(path) //nolint:gosec // G304: 路径是测试自有的 t.TempDir 文件。
		if err == nil {
			pid, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("pid 文件 %s 一直没被写出来", path)
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
	t.Fatalf("进程 %d 超出终止上限仍然活着", pid)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// kill -0 对「已死但还没被新父进程回收」的僵尸同样成功;用 ps 才能把还在跑的活儿
	// 和一条已终止的进程表条目区分开。
	output, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output() //nolint:gosec // G204: 固定可执行文件 + 操作系统分配的测试 PID。
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}
