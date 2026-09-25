//go:build linux

package ctlcmd

import (
	"os"
	"strconv"
	"strings"
)

// processName 读 /proc/<pid>/comm；取不到返回空串。
func processName(pid int) string {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
