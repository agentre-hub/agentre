package app

import (
	"errors"
	"os/exec"
	"runtime"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestValidateOpenPath(t *testing.T) {
	Convey("Given various path inputs", t, func() {
		Convey("when path is empty, then error", func() {
			_, err := validateOpenPath("")
			So(err, ShouldNotBeNil)
		})
		Convey("when path is relative, then error", func() {
			_, err := validateOpenPath("foo/bar.go")
			So(err, ShouldNotBeNil)
		})
		Convey("when path contains '..', then error", func() {
			_, err := validateOpenPath("/foo/../bar.go")
			So(err, ShouldNotBeNil)
		})
		Convey("when path has '..:line' suffix (potential bypass), then error", func() {
			_, err := validateOpenPath("/foo/..:42")
			So(err, ShouldNotBeNil)
		})
		Convey("when filename contains '..' but is not a '..' segment, then accept", func() {
			got, err := validateOpenPath("/Users/x/File..Go")
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "/Users/x/File..Go")
		})
		Convey("when POSIX absolute path with :line:col, then return without suffix", func() {
			got, err := validateOpenPath("/Users/x/foo.go:42:7")
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "/Users/x/foo.go")
		})
		Convey("when POSIX absolute path with a :start-end line range, then return without suffix", func() {
			got, err := validateOpenPath("/Users/x/foo.go:311-330")
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "/Users/x/foo.go")
		})
		Convey("when path has '..:start-end' suffix (potential bypass), then error", func() {
			_, err := validateOpenPath("/foo/..:311-330")
			So(err, ShouldNotBeNil)
		})
		Convey("when POSIX absolute path without suffix, then return as-is", func() {
			got, err := validateOpenPath("/Users/x/foo.go")
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "/Users/x/foo.go")
		})
		Convey("when path is home-anchored, then it expands to the user home", func() {
			orig := userHomeDir
			userHomeDir = func() (string, error) { return "/Users/x", nil }
			defer func() { userHomeDir = orig }()

			got, err := validateOpenPath("~/Code/foo.go:42")
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "/Users/x/Code/foo.go")

			got, err = validateOpenPath("~")
			So(err, ShouldBeNil)
			So(got, ShouldEqual, "/Users/x")
		})
		Convey("when a home-anchored path escapes with '..', then error", func() {
			orig := userHomeDir
			userHomeDir = func() (string, error) { return "/Users/x", nil }
			defer func() { userHomeDir = orig }()

			_, err := validateOpenPath("~/../etc/passwd")
			So(err, ShouldNotBeNil)
		})
		Convey("when the path names another user's home, then error", func() {
			_, err := validateOpenPath("~alice/notes.md")
			So(err, ShouldNotBeNil)
		})
		// 受提示注入的 agent 可以在转录里印出 `C:\readme.txt&calc.exe`，前端会把它判成
		// 一条可点的本地链接。这些字符在 NTFS / APFS 的文件名里本来就非法（`&|^<>` 是
		// cmd.exe 的控制符，`"` 两边都不许出现），所以不分平台一律拒绝，零代价。
		Convey("when the path carries a shell control character, then error", func() {
			for _, p := range []string{
				`C:\readme.txt&calc.exe`,
				`C:\readme.txt&&calc.exe`,
				`C:\a|calc.exe`,
				`C:\a^&calc.exe`,
				`C:\a<in.txt`,
				`C:\a>out.txt`,
				`C:\a"b.txt`,
				"/tmp/readme.txt&calc.exe",
			} {
				_, err := validateOpenPath(p)
				So(err, ShouldNotBeNil)
			}
		})
		Convey("when Windows absolute path with line suffix, then strip suffix", func() {
			got, err := validateOpenPath(`C:\Users\x\foo.go:10`)
			So(err, ShouldBeNil)
			So(got, ShouldEqual, `C:\Users\x\foo.go`)
		})
	})
}

func TestOpenPath_dispatchesPlatformCommand(t *testing.T) {
	Convey("Given a stubbed exec runner", t, func() {
		var gotName string
		var gotArgs []string
		origRun := runOpenCmd
		runOpenCmd = func(name string, args ...string) error {
			gotName = name
			gotArgs = args
			return nil
		}
		defer func() { runOpenCmd = origRun }()

		Convey("when OpenPath is called with a valid absolute path", func() {
			a := &App{}
			err := a.OpenPath("/tmp/file.go:42")
			So(err, ShouldBeNil)

			switch runtime.GOOS {
			case "darwin":
				So(gotName, ShouldEqual, "open")
				So(gotArgs, ShouldResemble, []string{"/tmp/file.go"})
			case "windows":
				So(gotName, ShouldEqual, "explorer")
				So(gotArgs, ShouldResemble, []string{"/tmp/file.go"})
			default:
				So(gotName, ShouldEqual, "xdg-open")
				So(gotArgs, ShouldResemble, []string{"/tmp/file.go"})
			}
		})

		Convey("when exec returns error, then OpenPath propagates", func() {
			runOpenCmd = func(name string, args ...string) error {
				return errors.New("boom")
			}
			a := &App{}
			err := a.OpenPath("/tmp/file.go")
			So(err, ShouldNotBeNil)
		})

		Convey("when path is invalid, then exec is not called", func() {
			called := false
			runOpenCmd = func(name string, args ...string) error {
				called = true
				return nil
			}
			a := &App{}
			err := a.OpenPath("relative/path.go")
			So(err, ShouldNotBeNil)
			So(called, ShouldBeFalse)
		})
	})
}

func TestRevealPath_dispatchesPlatformCommand(t *testing.T) {
	Convey("Given a stubbed exec runner", t, func() {
		var gotName string
		var gotArgs []string
		origRun := runOpenCmd
		runOpenCmd = func(name string, args ...string) error {
			gotName = name
			gotArgs = args
			return nil
		}
		defer func() { runOpenCmd = origRun }()

		Convey("when RevealPath is called with a valid absolute path", func() {
			a := &App{}
			err := a.RevealPath("/tmp/file.go:42")
			So(err, ShouldBeNil)

			switch runtime.GOOS {
			case "darwin":
				So(gotName, ShouldEqual, "open")
				So(gotArgs, ShouldResemble, []string{"-R", "/tmp/file.go"})
			case "windows":
				So(gotName, ShouldEqual, "explorer")
				So(gotArgs, ShouldResemble, []string{"/select,/tmp/file.go"})
			default:
				So(gotName, ShouldEqual, "nautilus")
				So(gotArgs, ShouldResemble, []string{"--select", "/tmp/file.go"})
			}
		})

		Convey("when exec returns error, then RevealPath propagates", func() {
			runOpenCmd = func(name string, args ...string) error {
				return errors.New("boom")
			}
			a := &App{}
			err := a.RevealPath("/tmp/file.go")
			So(err, ShouldNotBeNil)
		})

		Convey("when path is invalid, then exec is not called", func() {
			called := false
			runOpenCmd = func(name string, args ...string) error {
				called = true
				return nil
			}
			a := &App{}
			err := a.RevealPath("relative/path.go")
			So(err, ShouldNotBeNil)
			So(called, ShouldBeFalse)
		})

		Convey("when path contains '..', then exec is not called", func() {
			called := false
			runOpenCmd = func(name string, args ...string) error {
				called = true
				return nil
			}
			a := &App{}
			err := a.RevealPath("/tmp/../etc/file.go")
			So(err, ShouldNotBeNil)
			So(called, ShouldBeFalse)
		})
	})
}

// TestRunOpenPlatform_PerGOOS 覆盖本机 GOOS 之外的两条分支。windows 那一条尤其
// 要盯住「用哪个可执行文件」：只要经 cmd.exe，路径里的 `&` 之类就会被它当控制符,
// 而 Go 的 EscapeArg 只在含空格/Tab/引号时才加引号,拦不住。
func TestRunOpenPlatform_PerGOOS(t *testing.T) {
	Convey("Given a stubbed exec runner", t, func() {
		type call struct {
			name string
			args []string
		}
		var calls []call
		origRun := runOpenCmd
		defer func() { runOpenCmd = origRun }()
		stub := func(err func(name string) error) {
			runOpenCmd = func(name string, args ...string) error {
				calls = append(calls, call{name, args})
				return err(name)
			}
		}

		Convey("when opening on windows, then no shell interprets the path", func() {
			stub(func(string) error { return nil })
			So(runOpenPlatform("windows", `C:\Users\x\foo.txt`), ShouldBeNil)
			So(calls, ShouldHaveLength, 1)
			So(calls[0].name, ShouldNotEqual, "cmd")
			So(calls[0].name, ShouldEqual, "explorer")
			So(calls[0].args, ShouldResemble, []string{`C:\Users\x\foo.txt`})
		})

		Convey("when explorer exits non-zero on windows, then open still succeeds", func() {
			stub(func(string) error { return &exec.ExitError{} })
			So(runOpenPlatform("windows", `C:\Users\x\foo.txt`), ShouldBeNil)
		})

		Convey("when explorer itself cannot be launched on windows, then error propagates", func() {
			stub(func(string) error { return exec.ErrNotFound })
			So(runOpenPlatform("windows", `C:\Users\x\foo.txt`), ShouldNotBeNil)
		})

		Convey("when opening on darwin / linux, then the existing openers are untouched", func() {
			stub(func(string) error { return nil })
			So(runOpenPlatform("darwin", "/Users/x/foo.txt"), ShouldBeNil)
			So(runOpenPlatform("linux", "/home/x/foo.txt"), ShouldBeNil)
			So(calls, ShouldHaveLength, 2)
			So(calls[0].name, ShouldEqual, "open")
			So(calls[1].name, ShouldEqual, "xdg-open")
		})
	})
}

// TestRunRevealPlatform_PerGOOS 覆盖本机 GOOS 之外的两条分支：它们各自有一个
// 「命令报错不等于操作失败」的降级，只在 windows / linux 上才走得到。
func TestRunRevealPlatform_PerGOOS(t *testing.T) {
	Convey("Given a stubbed exec runner", t, func() {
		type call struct {
			name string
			args []string
		}
		var calls []call
		origRun := runOpenCmd
		defer func() { runOpenCmd = origRun }()
		stub := func(err func(name string) error) {
			runOpenCmd = func(name string, args ...string) error {
				calls = append(calls, call{name, args})
				return err(name)
			}
		}

		Convey("when explorer exits non-zero on windows, then reveal still succeeds", func() {
			// explorer.exe 成功时也几乎恒以非零码退出，把它当失败会让每一次成功
			// 的「在文件管理器中显示」都弹一条错误提示。
			stub(func(string) error { return &exec.ExitError{} })
			So(runRevealPlatform("windows", `C:\Users\x\foo.go`), ShouldBeNil)
			So(calls, ShouldHaveLength, 1)
			So(calls[0].name, ShouldEqual, "explorer")
		})

		Convey("when explorer itself cannot be launched on windows, then error propagates", func() {
			stub(func(string) error { return exec.ErrNotFound })
			So(runRevealPlatform("windows", `C:\Users\x\foo.go`), ShouldNotBeNil)
		})

		Convey("when nautilus is missing on linux, then fall back to opening the parent dir", func() {
			// nautilus 只有 GNOME 装；KDE / XFCE 等桌面上根本不存在，此时打开所在
			// 目录（选不中文件）也比只弹一条错误强。
			stub(func(name string) error {
				if name == "nautilus" {
					return exec.ErrNotFound
				}
				return nil
			})
			So(runRevealPlatform("linux", "/home/x/proj/foo.go"), ShouldBeNil)
			So(calls, ShouldHaveLength, 2)
			So(calls[1].name, ShouldEqual, "xdg-open")
			So(calls[1].args, ShouldResemble, []string{"/home/x/proj"})
		})

		Convey("when nautilus succeeds on linux, then no fallback is run", func() {
			stub(func(string) error { return nil })
			So(runRevealPlatform("linux", "/home/x/proj/foo.go"), ShouldBeNil)
			So(calls, ShouldHaveLength, 1)
			So(calls[0].name, ShouldEqual, "nautilus")
		})
	})
}
