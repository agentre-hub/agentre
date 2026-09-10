package app

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/agentre-hub/agentre/internal/bootstrap"
	"github.com/agentre-hub/agentre/internal/pkg/procattr"
)

// BugReportInfo 是「Bug 反馈」预填 GitHub issue 模板用的诊断信息。
type BugReportInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	OSLabel string `json:"osLabel"`
}

// GetBugReportInfo 返回版本/commit/系统信息，供前端拼到 issues/new 的 query 里预填。
// 版本与 commit 复用 Info()，系统标签由 detectOSVersion + osLabel 组装。
func (a *App) GetBugReportInfo() BugReportInfo {
	info := a.Info()
	return BugReportInfo{
		Version: info.Version,
		Commit:  info.Commit,
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		OSLabel: osLabel(runtime.GOOS, runtime.GOARCH, detectOSVersion()),
	}
}

// osLabel 把 GOOS/GOARCH + 可选系统版本拼成人类可读标签，例如
// "macOS 14.6 (Apple Silicon)" / "Windows 10.0.19045 (amd64)" / "Ubuntu 22.04 (amd64)"。
// 纯函数、与系统探测解耦，方便单测。
func osLabel(goos, goarch, osVer string) string {
	archSuffix := goarch
	switch goos {
	case "darwin":
		if goarch == "arm64" {
			archSuffix = "Apple Silicon"
		} else {
			archSuffix = "Intel"
		}
		if osVer != "" {
			return fmt.Sprintf("macOS %s (%s)", osVer, archSuffix)
		}
		return fmt.Sprintf("macOS (%s)", archSuffix)
	case "windows":
		if osVer != "" {
			return fmt.Sprintf("Windows %s (%s)", osVer, archSuffix)
		}
		return fmt.Sprintf("Windows (%s)", archSuffix)
	case "linux":
		if osVer != "" {
			return fmt.Sprintf("%s (%s)", osVer, archSuffix)
		}
		return fmt.Sprintf("Linux (%s)", archSuffix)
	default:
		return fmt.Sprintf("%s (%s)", goos, archSuffix)
	}
}

// detectOSVersion 尽力探测系统版本字符串；探测失败返回空串（osLabel 会退化为不带版本）。
func detectOSVersion() string {
	switch runtime.GOOS {
	case "darwin":
		out, err := exec.Command("sw_vers", "-productVersion").Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	case "linux":
		data, err := os.ReadFile("/etc/os-release")
		if err != nil {
			return ""
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			if name, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
				return strings.Trim(strings.TrimSpace(name), `"`)
			}
		}
		return ""
	case "windows":
		cmd := exec.Command("cmd", "/c", "ver")
		procattr.ApplyNoConsoleWindow(cmd)
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		text := strings.TrimSpace(string(out))
		if i := strings.Index(text, "[Version "); i >= 0 {
			if j := strings.Index(text[i:], "]"); j > 0 {
				return strings.TrimPrefix(text[i:i+j], "[Version ")
			}
		}
		return ""
	}
	return ""
}

// GetDebugLogging 返回是否开启 debug 级别日志（持久化在 app_settings）。
func (a *App) GetDebugLogging() (bool, error) {
	return bootstrap.DebugLoggingEnabled(a.ctx)
}

// SetDebugLogging 开启/关闭 debug 级别日志：持久化并立即热重载 logger，无需重启。
func (a *App) SetDebugLogging(enabled bool) error {
	return bootstrap.SetDebugLogging(a.ctx, enabled)
}
