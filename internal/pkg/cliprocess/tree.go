package cliprocess

import (
	"os"
	"os/exec"
)

// ApplyProcessGroup 让 cmd 的子进程自成进程组(Unix Setpgid / Windows
// CREATE_NEW_PROCESS_GROUP),收尾时可按整棵树投递信号。调用方自己起进程、只想借
// 这份进程树语义时用它(如 hookexec)。
func ApplyProcessGroup(cmd *exec.Cmd) { applyProcessGroup(cmd) }

// KillProcessTree 回收 process 领着的整棵进程树(Unix 按组 SIGKILL 并补投 /
// Windows taskkill /T /F),进程早已消失视为收尾成功。
func KillProcessTree(process *os.Process) error { return killProcessTree(process) }
