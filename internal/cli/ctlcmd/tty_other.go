//go:build !(darwin || freebsd || netbsd || openbsd || linux || windows)

package ctlcmd

import (
	"errors"
	"io"
)

// 其它平台没有终端支持：一律视为非 TTY，裸密钥 flag 因此以 NEEDS TTY 结束。
func stdinIsTerminal() bool { return false }

func readHidden(io.Writer, string) (string, error) {
	return "", errors.New("no terminal support on this platform")
}
