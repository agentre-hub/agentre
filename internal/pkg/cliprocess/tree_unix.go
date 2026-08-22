//go:build !windows

package cliprocess

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// errNoTreeTarget 表示某个 pid 并不领着一棵本包可以按组投递信号的进程树。
var errNoTreeTarget = errors.New("cliprocess: pid does not lead a signalable process tree")

// applyProcessGroup 让子进程自成进程组组长(Setpgid),这样收尾时可以按组投递,把 CLI
// 派生的孙进程(MCP server、git、node 等)一起带走。否则孙进程会继承并握住 stdout
// 管道的写端:读端永远等不到 EOF,readLoop 收不了尾、cmd.Wait 也回不来。
func applyProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// signalTree 把 sig 投递给 pid 领头的那个进程组。
//
// 按组投递没有「只影响一个进程」的余地,所以只对**自己领着一个组**的 pid 生效 ——
// 也就是 Start 给每个进程安排的形状。已经被回收的 pid、或只是待在别人组里的 pid,
// 领的不是我们的树:对「它的」组投递会打到调用方的所有兄弟进程上,在 go test 下那个
// 组里装着测试进程和整条工具链。这种 pid 在这里被拒掉,调用方退回单进程投递。
func signalTree(pid int, sig syscall.Signal) error {
	pgid, ok := signalableTreePGID(pid)
	if !ok {
		return errNoTreeTarget
	}
	return syscall.Kill(-pgid, sig)
}

// signalableTreePGID 解出 pid 领着的那个进程组,并做上面那道守卫;解不出时交回 false。
func signalableTreePGID(pid int) (int, bool) {
	if pid <= 1 {
		return 0, false
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return 0, false
	}
	if pgid != pid || pgid == syscall.Getpgrp() {
		return 0, false
	}
	return pgid, true
}

// signalProcessTree 按组投递 sig,拒收时退回单进程。
func signalProcessTree(process *os.Process, sig os.Signal) error {
	if process == nil {
		return nil
	}
	signalNumber, ok := sig.(syscall.Signal)
	if !ok {
		return process.Signal(sig)
	}
	if err := signalTree(process.Pid, signalNumber); err == nil {
		return nil
	}
	return process.Signal(sig)
}

// killTreeAttempts / killTreeRetryDelay 是补投的次数与间隔,见 killProcessTree。
const (
	killTreeAttempts   = 3
	killTreeRetryDelay = 2 * time.Millisecond
)

// killProcessTree 给整棵树发 SIGKILL(不可被忽略),拒收时退回单进程。
//
// 投递不止一次:一次 killpg 只覆盖内核枚举那一刻在组里的成员,而组里某个成员可能正好
// 在信号送达前一瞬 fork 出了新的子进程 —— 它已经属于这个组,却赶不上这一次投递。实测
// 就是这样漏掉过一个刚被 exec 出来的进程(shell 已经成了僵尸,它 fork 的那个还活着,
// 并且握着 stdout 写端 —— 读端于是永远等不到 EOF,收尾就此挂住)。
//
// 补投是安全且有界的:第一刀落下之后组里已经没有活着的成员能再 fork,所以窗口只有
// 「已经 fork 完、还没被这次枚举看到」那么大。整组消失(ESRCH)就提前收工。
func killProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	pgid, ok := signalableTreePGID(process.Pid)
	if !ok {
		return ignoreProcessDone(process.Kill())
	}
	switch err := syscall.Kill(-pgid, syscall.SIGKILL); {
	case err == nil, errors.Is(err, syscall.ESRCH):
		// nil = 投出去了;ESRCH = 这个组本来就没了。两者都不必退回单进程。
	default:
		return ignoreProcessDone(process.Kill())
	}
	// 补投是 best-effort:错误一律不上报。组里只剩僵尸时,内核对整组投递会回 EPERM ——
	// 那不是失败,那是「已经没有活着的成员了」。
	for attempt := 1; attempt < killTreeAttempts; attempt++ {
		time.Sleep(killTreeRetryDelay)
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
			return nil
		}
	}
	return nil
}

// ignoreProcessDone 把「进程早就没了」当成收尾成功:Kill 是幂等的收尾动作。
func ignoreProcessDone(err error) error {
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
