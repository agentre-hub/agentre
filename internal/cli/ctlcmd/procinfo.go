package ctlcmd

import (
	"os"

	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// reportCaller 是外部调用方（握手 token、stdin 不是 TTY）上报给桌面审批弹窗的进程信息：
// 父进程名（尽力而为，取不到为空）、父进程 pid、工作目录。它只是提示，桌面端不把它当
// 信任依据（spec「外部调用的审批弹窗」）。
func reportCaller() *agentrewire.CtlCallerInfo {
	ppid := os.Getppid()
	wd, _ := os.Getwd()
	return &agentrewire.CtlCallerInfo{ParentProcess: processName(ppid), Pid: int64(ppid), Cwd: wd}
}
