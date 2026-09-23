//go:build darwin || freebsd || netbsd || openbsd || linux

package ctlcmd

import (
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// stdinIsTerminal 报告 stdin 是否连着终端。
func stdinIsTerminal() bool {
	_, err := unix.IoctlGetTermios(int(os.Stdin.Fd()), ioctlGetTermios)
	return err == nil
}

// readHidden 在 stdin 的终端上关掉回显读一行；提示与收尾换行写到 prompt 流。
func readHidden(prompt io.Writer, text string) (string, error) {
	fd := int(os.Stdin.Fd())
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return "", err
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	quiet.Lflag |= unix.ICANON | unix.ISIG
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &quiet); err != nil {
		return "", err
	}
	defer func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, old) }()
	_, _ = io.WriteString(prompt, text)
	line, err := readLine(os.Stdin)
	_, _ = io.WriteString(prompt, "\n")
	return line, err
}
