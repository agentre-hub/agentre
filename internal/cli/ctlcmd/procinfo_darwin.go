//go:build darwin

package ctlcmd

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// processName 经 sysctl kern.proc.pid 取进程名；取不到返回空串。
func processName(pid int) string {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ""
	}
	name := kp.Proc.P_comm[:]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return string(name)
}
