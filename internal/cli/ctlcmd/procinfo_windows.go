//go:build windows

package ctlcmd

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// processName 在进程快照里按 pid 找可执行文件名；取不到返回空串。
func processName(pid int) string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ProcessID) == pid {
			return windows.UTF16ToString(e.ExeFile[:])
		}
	}
	return ""
}
