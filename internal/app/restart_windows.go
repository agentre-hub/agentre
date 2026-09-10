//go:build windows

package app

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/agentre-hub/agentre/internal/pkg/procattr"
)

// startRelaunchHelper 用隐藏窗口的 PowerShell 先 Wait-Process 等旧进程 PID 退出,
// 再 Start-Process 拉起新实例。procattr.ApplyNoConsoleWindow 避免闪出控制台窗口。
func startRelaunchHelper(pid int, target relaunchTarget) error {
	if target.executablePath == "" {
		return fmt.Errorf("restart target executable is empty")
	}

	script := "try { Wait-Process -Id " + strconv.Itoa(pid) + " -ErrorAction SilentlyContinue } catch {}; " +
		"Start-Sleep -Milliseconds 300; " +
		"Start-Process -FilePath " + quotePowerShellSingle(target.executablePath)
	// #nosec G204 -- script 拼接的是应用自身经内部解析的可执行路径(已单引号转义),非用户输入。
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-WindowStyle", "Hidden", "-Command", script)
	procattr.ApplyNoConsoleWindow(cmd)
	return cmd.Start()
}

// quotePowerShellSingle 用单引号包裹并转义,防止路径中的特殊字符破坏 PowerShell 命令。
func quotePowerShellSingle(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
