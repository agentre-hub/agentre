//go:build windows

package ctlcmd

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// stdinIsTerminal 报告 stdin 是否连着控制台。
func stdinIsTerminal() bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode) == nil
}

// readHidden 在控制台上关掉回显读一行；提示与收尾换行写到 prompt 流。
func readHidden(prompt io.Writer, text string) (string, error) {
	h := windows.Handle(os.Stdin.Fd())
	var old uint32
	if err := windows.GetConsoleMode(h, &old); err != nil {
		return "", err
	}
	quiet := (old &^ windows.ENABLE_ECHO_INPUT) | windows.ENABLE_LINE_INPUT | windows.ENABLE_PROCESSED_INPUT
	if err := windows.SetConsoleMode(h, quiet); err != nil {
		return "", err
	}
	defer func() { _ = windows.SetConsoleMode(h, old) }()
	_, _ = io.WriteString(prompt, text)
	line, err := readLine(os.Stdin)
	_, _ = io.WriteString(prompt, "\n")
	return line, err
}
