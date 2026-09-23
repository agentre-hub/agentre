//go:build !(darwin || linux || windows)

package ctlcmd

// processName 在其它平台上不可得：父进程名留空，pid 与工作目录照常上报。
func processName(int) string { return "" }
