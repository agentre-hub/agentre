// Package ctlcmd implements agrctl's top-level control verbs — list / get /
// create / update / delete / help for Agentre's resources, and send — used to
// control a running desktop without booting the Wails app.
//
// It talks to an executor's control API (on the desktop: ctl_svc, mounted on the
// httpgateway under /ctl/). The endpoint URL + token come from
// AGENTRE_CTL_ENDPOINT/AGENTRE_CTL_TOKEN (injected into Agentre sessions) or
// from the ctlendpoint handshake file the desktop writes into AppDataDir.
//
// Everything that has meaning to a user — flag parsing, name/path resolution,
// output formats and the help contract — lives here; executors only take
// id-based operations (the agentrewire Ctl* contract).
package ctlcmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Main is the process entry point; runs the command and exits. Never returns.
func Main(args []string) {
	os.Exit(run(args, osSys()))
}

// sys 是一次调用能接触到的进程环境；测试注入假的 TTY 与密钥读取。
type sys struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	lookupEnv      func(string) (string, bool)
	// stdinIsTTY 决定调用方分类（人 / 外部程序）以及能否无回显读取密钥。
	stdinIsTTY bool
	// readSecret 在终端上无回显地读一行；只在 stdinIsTTY 时调用。
	readSecret func(prompt string) (string, error)
}

func osSys() *sys {
	return &sys{
		stdin:      os.Stdin,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
		lookupEnv:  os.LookupEnv,
		stdinIsTTY: stdinIsTerminal(),
		readSecret: func(prompt string) (string, error) { return readHidden(os.Stderr, prompt) },
	}
}

// 退出码（spec「退出码」表）。
const (
	exitOK       = 0
	exitFailed   = 1 // 执行失败、被拒绝、审批超时、连接不上执行者
	exitUsage    = 2 // 未知子命令或 flag、缺必填项、定位有歧义
	exitNeedsTTY = 3 // 没有 TTY 却要输入密钥
)

// cliError 是带退出码的错误；其它错误一律按执行失败（1）处理。
type cliError struct {
	code int
	msg  string
}

func (e *cliError) Error() string { return e.msg }

func usageErrorf(format string, a ...any) error {
	return &cliError{code: exitUsage, msg: fmt.Sprintf(format, a...)}
}

// needsTTY 是「要人来操作」：裸密钥 flag 却没有终端可读。
func needsTTY(flagName string) error {
	return &cliError{code: exitNeedsTTY, msg: fmt.Sprintf(
		"--%s reads the value from an interactive terminal.\nAsk the user to run this command in their own terminal.", flagName)}
}

// run is the testable core; returns the exit code.
func run(args []string, s *sys) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(s.stderr, usageText)
		return exitUsage
	}
	return report(s, dispatch(args, s))
}

func dispatch(args []string, s *sys) error {
	verb, rest := args[0], args[1:]
	switch verb {
	case "help", "-h", "--help":
		return runHelp(rest, s.stdout)
	case "send":
		return runSend(rest, s)
	case "list":
		return runList(rest, s)
	case "get":
		return runGet(rest, s)
	case "create", "update", "delete":
		return runWrite(verb, args, s)
	default:
		return usageErrorf("unknown command %q (run 'agrctl help')", verb)
	}
}

// report 把错误写到 stderr 并换算成退出码。
func report(s *sys, err error) int {
	if err == nil {
		return exitOK
	}
	var ce *cliError
	if errors.As(err, &ce) {
		prefix := "Error: "
		if ce.code == exitNeedsTTY {
			prefix = "NEEDS TTY: "
		}
		_, _ = fmt.Fprintln(s.stderr, prefix+ce.msg)
		return ce.code
	}
	_, _ = fmt.Fprintln(s.stderr, "Error: "+err.Error())
	return exitFailed
}

// commandLine 还原本次调用的命令行，密钥 flag 的值替换为 …，供审批卡展示。
func commandLine(args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "agrctl")
	for _, a := range args {
		parts = append(parts, shellQuote(redactSecret(a)))
	}
	return strings.Join(parts, " ")
}

// exampleLine 还原一条给人照抄的命令：密钥 flag 写成裸 flag（在终端里无回显地再问一次），
// 不能带审批卡上那个 …，否则照抄就把「…」存成了密钥。
func exampleLine(args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, "agrctl")
	for _, a := range args {
		parts = append(parts, shellQuote(bareSecret(a)))
	}
	return strings.Join(parts, " ")
}

func bareSecret(arg string) string {
	for _, name := range secretFlagNames {
		for _, dashes := range []string{"--", "-"} {
			// 空值（--token= 表示清空）不含密钥，原样保留。
			if prefix := dashes + name + "="; strings.HasPrefix(arg, prefix) && arg != prefix {
				return dashes + name
			}
		}
	}
	return arg
}

func redactSecret(arg string) string {
	for _, name := range secretFlagNames {
		for _, dashes := range []string{"--", "-"} {
			if prefix := dashes + name + "="; strings.HasPrefix(arg, prefix) {
				return prefix + "…"
			}
		}
	}
	return arg
}

func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
