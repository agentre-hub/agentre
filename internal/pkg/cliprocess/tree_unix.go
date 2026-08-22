//go:build !windows

package cliprocess

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
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
	if pid <= 1 {
		return errNoTreeTarget
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return errNoTreeTarget
	}
	if pgid != pid || pgid == syscall.Getpgrp() {
		return errNoTreeTarget
	}
	return syscall.Kill(-pgid, sig)
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

// killProcessTree 给整棵树发 SIGKILL(不可被忽略),拒收时退回单进程。
func killProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := signalTree(process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return ignoreProcessDone(process.Kill())
}

// ignoreProcessDone 把「进程早就没了」当成收尾成功:Kill 是幂等的收尾动作。
func ignoreProcessDone(err error) error {
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
