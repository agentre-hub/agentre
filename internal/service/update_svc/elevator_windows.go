//go:build windows

package update_svc

import (
	"fmt"
	"os/exec"

	"github.com/agentre-hub/agentre/internal/pkg/procattr"
)

// runInstaller 运行 NSIS 安装程序（用户级安装，无需 UAC 提权）。
// 通过 CREATE_NO_WINDOW + HideWindow 抑制黑色控制台一闪而过。
func runInstaller(exePath string, args ...string) error {
	cmd := exec.Command(exePath, args...) //nolint:gosec // exePath is the installer selected by the update flow.
	procattr.ApplyNoConsoleWindow(cmd)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("run installer failed: %s: %w", string(output), err)
	}
	return nil
}
