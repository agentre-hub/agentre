package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/agentre-hub/agentre/internal/bootstrap"
	"github.com/agentre-hub/agentre/internal/pkg/procattr"
)

// runOpenCmd is the test seam for exec.Command. Tests swap it; production code
// uses the real exec.
var runOpenCmd = func(name string, args ...string) error {
	cmd := exec.Command(name, args...) //nolint:gosec
	procattr.ApplyNoConsoleWindow(cmd)
	return cmd.Run()
}

var lineSuffixRe = regexp.MustCompile(`:\d+(?:-\d+|:\d+)?$`)

// shellControlChars 是 cmd.exe 的控制符（命令分隔、管道、重定向、转义）加上引号。
//
// 进到这里的路径来自转录里的链接，也就是 **agent 的输出**：一次提示注入就能让它印出
// `C:\readme.txt&calc.exe`，前端把它判成一条可点的本地链接，用户点一下就以用户权限
// 执行了后半截——目标文件甚至不必存在。所以路径绝不能再经任何一层 shell 解析
// （见 runOpenPlatform 的 windows 分支），而这些字符在 NTFS 与 APFS 的文件名里本来
// 就非法，不分平台一律拒绝是零代价的第二道闸。
const shellControlChars = `&|^<>"`

// userHomeDir 是 os.UserHomeDir 的包级 indirection，测试可替换。
var userHomeDir = os.UserHomeDir

// OpenPath 用系统默认应用打开 path。
// path 必须是绝对路径或 "~" / "~/…" 家目录形式；包含 ".." 时拒绝（防御性，AI 输出基本不会有）。
// 末尾 :line[:col] / :start-end 后缀会被剥离 —— macOS open / xdg-open 不识别这种语法。
// 行号未来若要支持，由"编辑器 URL scheme"设置项接管（见 spec 未来工作）。
func (a *App) OpenPath(path string) error {
	cleaned, err := validateOpenPath(path)
	if err != nil {
		return err
	}
	return runOpenPlatform(runtime.GOOS, cleaned)
}

func validateOpenPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("OpenPath: path is empty")
	}
	path, err := expandHome(path)
	if err != nil {
		return "", err
	}
	if !isAbsolutePath(path) {
		return "", fmt.Errorf("OpenPath: path must be absolute: %s", path)
	}
	cleaned := lineSuffixRe.ReplaceAllString(path, "")
	if strings.ContainsAny(cleaned, shellControlChars) {
		return "", fmt.Errorf("OpenPath: path contains a shell control character: %s", path)
	}
	for _, part := range strings.FieldsFunc(cleaned, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." {
			return "", fmt.Errorf("OpenPath: path contains '..' segment: %s", path)
		}
	}
	return cleaned, nil
}

// expandHome 把 "~" / "~/…" 展开成绝对路径。转录里的链接照原样把用户看到的
// "~/Code/foo.go" 传下来（前端拿不到家目录，展开只能发生在这一侧）；"~alice/…"
// 这种指别人家目录的形式不认，原样返回后被绝对路径检查拒掉。
func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p, nil
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("OpenPath: resolve home dir: %w", err)
	}
	if p == "~" {
		return home, nil
	}
	return home + p[1:], nil
}

func isAbsolutePath(p string) bool {
	if strings.HasPrefix(p, "/") {
		return true
	}
	// Windows: C:\ 或 C:/
	if len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') {
		c := p[0]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
			return true
		}
	}
	return false
}

// RevealPath 在系统文件管理器中打开 path 所在目录并选中该文件。
// path 必须是绝对路径；包含 ".." 时拒绝。末尾 :line[:col] / :start-end 后缀会被剥离。
func (a *App) RevealPath(path string) error {
	cleaned, err := validateOpenPath(path)
	if err != nil {
		return err
	}
	return runRevealPlatform(runtime.GOOS, cleaned)
}

// OpenLogsDir 在系统文件管理器中打开 Agentre 的日志目录（不存在时先创建）。
// 用于「设置 → 版本 & 更新 → 打开日志」，方便用户取日志附到 Bug 反馈里。
func (a *App) OpenLogsDir() error {
	dir, err := bootstrap.LogsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("OpenLogsDir: create logs dir: %w", err)
	}
	return runOpenPlatform(runtime.GOOS, dir)
}

func runOpenPlatform(goos, path string) error {
	switch goos {
	case "darwin":
		return runOpenCmd("open", path)
	case "windows":
		// 刻意不是 `cmd /c start`：那条路会把整行交给 cmd.exe 解析，而 Go 的 EscapeArg
		// 只在参数含空格 / Tab / 引号时才加引号，`& | ^ < >` 会原样拼进命令行被当成控制
		// 符（validateOpenPath 已经先拒掉它们，这里不再给第二次机会）。explorer.exe 直接
		// 由 CreateProcess 起，没有任何一层 shell 参与。
		return runExplorer(path)
	default:
		return runOpenCmd("xdg-open", path)
	}
}

// runExplorer 起 explorer.exe，并吃掉它的退出码。
//
// explorer.exe 即使成功也几乎恒以非零码退出（它把退出码用作别的语义），照直当失败会让
// 每一次成功的打开 / 「在文件管理器中显示」都弹一条错误提示。只有进程根本起不来
// （可执行文件找不到之类，此时 err 不是 *exec.ExitError）才是真失败。
func runExplorer(args ...string) error {
	err := runOpenCmd("explorer", args...)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}

func runRevealPlatform(goos, path string) error {
	switch goos {
	case "darwin":
		return runOpenCmd("open", "-R", path)
	case "windows":
		return runExplorer("/select," + path)
	default:
		// nautilus 只有 GNOME 装，KDE / XFCE / Sway 等桌面上根本不存在。它起不来时
		// 回落到打开文件所在目录：选不中文件，但比只留一条错误提示强，用的还是与
		// OpenPath 同一个桌面无关的 xdg-open。
		if err := runOpenCmd("nautilus", "--select", path); err != nil {
			return runOpenCmd("xdg-open", filepath.Dir(path))
		}
		return nil
	}
}
