//go:build windows

package cliprocess

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// createNewProcessGroup = CREATE_NEW_PROCESS_GROUP。
const createNewProcessGroup = 0x00000200

// applyProcessGroup 让子进程另开一个进程组,和 Unix 侧的 Setpgid 同义:控制台信号
// 不再连坐到宿主进程,收尾时整棵树由 taskkill /T 回收。
func applyProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

// signalProcessTree 在 Windows 上没有 POSIX 进程组的对应物,单进程投递即可。
func signalProcessTree(process *os.Process, sig os.Signal) error {
	if process == nil {
		return nil
	}
	return process.Signal(sig)
}

// killProcessTree 用 taskkill /T /F 回收整棵树,失败退回单进程。
func killProcessTree(process *os.Process) error {
	if process == nil {
		return nil
	}
	// #nosec G204 -- 可执行文件是固定的 taskkill,PID 由操作系统分配。
	if err := exec.Command("taskkill", "/PID", strconv.Itoa(process.Pid), "/T", "/F").Run(); err == nil {
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
